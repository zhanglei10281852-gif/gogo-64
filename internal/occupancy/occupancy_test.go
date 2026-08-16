package occupancy

import (
	"path/filepath"
	"testing"

	"HumpYard/internal/blocking"
	"HumpYard/internal/config"
	"HumpYard/internal/hump"
	"HumpYard/internal/ingest"
	"HumpYard/internal/jsonx"
	"HumpYard/internal/model"
)

// stage loads the examples and runs blocking and crest sequencing.
func stage(t *testing.T) (*config.Config, model.YardOrder, hump.Plan) {
	t.Helper()
	cfg, _, err := config.Load(filepath.Join("..", "..", "examples", "config.json"))
	if err != nil {
		t.Fatalf("config.Load: %v", err)
	}
	res, err := ingest.Load(filepath.Join("..", "..", "examples", "order.json"), cfg)
	if err != nil {
		t.Fatalf("ingest.Load: %v", err)
	}
	bp := blocking.Build(cfg, res.Order)
	return cfg, res.Order, hump.Build(cfg, res.Order, bp)
}

func TestSimulatePlacesEveryCar(t *testing.T) {
	cfg, order, hp := stage(t)
	res, err := Simulate(cfg, order, hp)
	if err != nil {
		t.Fatalf("Simulate: %v", err)
	}
	if len(res.Events) != order.CarCount() {
		t.Fatalf("%d events for %d cars", len(res.Events), order.CarCount())
	}
	if res.Stats.CarsRefused != 0 {
		t.Fatalf("cars refused: %v", res.Refused)
	}
	placed := 0
	for _, tr := range res.Tracks {
		placed += len(tr.CarIDs)
		if tr.RemainingFt < 0 || tr.RemainingTons < 0 {
			t.Fatalf("track %s is over its limits", tr.TrackID)
		}
	}
	if placed != order.CarCount() {
		t.Fatalf("tracks hold %d cars, order has %d", placed, order.CarCount())
	}
}

func TestFinalOrderFollowsMovementOrder(t *testing.T) {
	cfg, order, hp := stage(t)
	res, err := Simulate(cfg, order, hp)
	if err != nil {
		t.Fatalf("Simulate: %v", err)
	}
	want := map[string][]string{}
	for _, m := range hp.Movements {
		want[m.TrackID] = append(want[m.TrackID], m.CarIDs...)
	}
	for _, tr := range res.Tracks {
		expected := want[tr.TrackID]
		if len(expected) != len(tr.CarIDs) {
			continue
		}
		for i := range expected {
			if expected[i] != tr.CarIDs[i] {
				t.Fatalf("track %s order %v, want %v", tr.TrackID, tr.CarIDs, expected)
			}
		}
	}
}

func TestOverflowWhenTargetTrackIsFull(t *testing.T) {
	cfg, order, hp := stage(t)
	for i := range cfg.Class {
		if cfg.Class[i].Block == "EAST" {
			cfg.Class[i].CapacityFt = 120
		}
	}
	res, err := Simulate(cfg, order, hp)
	if err != nil {
		t.Fatalf("Simulate: %v", err)
	}
	if len(res.Overflow) == 0 {
		t.Fatal("expected overflow cars")
	}
	for _, id := range res.Overflow {
		if got := res.FinalTrackOf(id); got != cfg.Yard.OverflowTrack {
			t.Fatalf("overflow car %s came to rest on %q", id, got)
		}
	}
}

func TestRefusedWhenOverflowIsAlsoFull(t *testing.T) {
	cfg, order, hp := stage(t)
	for i := range cfg.Class {
		cfg.Class[i].CapacityFt = 110
	}
	res, err := Simulate(cfg, order, hp)
	if err != nil {
		t.Fatalf("Simulate: %v", err)
	}
	if res.Stats.CarsRefused == 0 {
		t.Fatal("expected refused cars when every track is tiny")
	}
	sawError := false
	for _, f := range res.Findings {
		if f.Severity == config.SeverityError {
			sawError = true
		}
	}
	if !sawError {
		t.Fatal("refused cars must raise an error finding")
	}
}

func TestTrackRestrictionsDivertCars(t *testing.T) {
	cfg, order, hp := stage(t)
	for i := range cfg.Class {
		if cfg.Class[i].Block == "SOUTH" {
			cfg.Class[i].Restrictions = []string{model.ResNoPlacard}
		}
	}
	res, err := Simulate(cfg, order, hp)
	if err != nil {
		t.Fatalf("Simulate: %v", err)
	}
	if got := res.FinalTrackOf("NS 655412"); got != cfg.Yard.OverflowTrack {
		t.Fatalf("placarded car went to %q, want the overflow track", got)
	}
}

func TestPlacementsCarryCabooseFlag(t *testing.T) {
	cfg, order, hp := stage(t)
	res, err := Simulate(cfg, order, hp)
	if err != nil {
		t.Fatalf("Simulate: %v", err)
	}
	index, err := model.NewCarIndex(order.AllCars())
	if err != nil {
		t.Fatalf("NewCarIndex: %v", err)
	}
	placements := res.Placements(index)
	if len(placements) == 0 {
		t.Fatal("no placements produced")
	}
	for _, p := range placements {
		track, ok := cfg.ClassTrackByID(p.TrackID)
		if !ok {
			t.Fatalf("unknown track %s", p.TrackID)
		}
		if p.CabooseAtRear != track.CabooseSpot {
			t.Fatalf("track %s caboose flag %v, want %v", p.TrackID, p.CabooseAtRear, track.CabooseSpot)
		}
		if len(p.Cars) == 0 {
			t.Fatalf("placement %s has no cars", p.TrackID)
		}
	}
}

func TestSimulateIsDeterministic(t *testing.T) {
	cfg, order, hp := stage(t)
	res, err := Simulate(cfg, order, hp)
	if err != nil {
		t.Fatalf("Simulate: %v", err)
	}
	first, err := jsonx.MarshalCanonical(res)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	for i := 0; i < 5; i++ {
		again, err := Simulate(cfg, order, hp)
		if err != nil {
			t.Fatalf("Simulate: %v", err)
		}
		data, err := jsonx.MarshalCanonical(again)
		if err != nil {
			t.Fatalf("marshal: %v", err)
		}
		if string(data) != string(first) {
			t.Fatal("occupancy simulation is not deterministic")
		}
	}
}

func TestSimulateRejectsUnknownCar(t *testing.T) {
	cfg, order, hp := stage(t)
	hp.Movements = append(hp.Movements, hump.Movement{
		Seq: 999, Kind: hump.KindHump, TrackID: "C01", CarIDs: []string{"ZZZZ 999999"},
	})
	if _, err := Simulate(cfg, order, hp); err == nil {
		t.Fatal("expected an error for an unknown car")
	}
}
