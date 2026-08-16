package depart

import (
	"path/filepath"
	"testing"

	"HumpYard/internal/blocking"
	"HumpYard/internal/config"
	"HumpYard/internal/hump"
	"HumpYard/internal/ingest"
	"HumpYard/internal/jsonx"
	"HumpYard/internal/model"
	"HumpYard/internal/occupancy"
)

// stage loads the examples and runs every stage before train building.
func stage(t *testing.T) (*config.Config, model.YardOrder, occupancy.Result) {
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
	hp := hump.Build(cfg, res.Order, bp)
	occ, err := occupancy.Simulate(cfg, res.Order, hp)
	if err != nil {
		t.Fatalf("Simulate: %v", err)
	}
	return cfg, res.Order, occ
}

func TestBuildForwardsBlocksInOrder(t *testing.T) {
	cfg, order, occ := stage(t)
	plan, err := Build(cfg, order, occ)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	if len(plan.Trains) != len(cfg.Departures) {
		t.Fatalf("%d trains for %d orders", len(plan.Trains), len(cfg.Departures))
	}
	train := plan.Trains[0]
	wantOrder := cfg.Departures[0].BlockOrder
	seen := []string{}
	last := ""
	for _, c := range train.Cars {
		if c.Block != last {
			seen = append(seen, c.Block)
			last = c.Block
		}
	}
	if len(seen) != len(wantOrder) {
		t.Fatalf("block sequence %v, want %v", seen, wantOrder)
	}
	for i := range wantOrder {
		if seen[i] != wantOrder[i] {
			t.Fatalf("block sequence %v, want %v", seen, wantOrder)
		}
	}
	for i, c := range train.Cars {
		if c.Position != i+1 {
			t.Fatalf("car %s is at position %d", c.CarID, c.Position)
		}
	}
}

func TestNoCarRidesTwoTrains(t *testing.T) {
	cfg, order, occ := stage(t)
	plan, err := Build(cfg, order, occ)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	seen := map[string]string{}
	for _, tr := range plan.Trains {
		for _, c := range tr.Cars {
			if other, dup := seen[c.CarID]; dup {
				t.Fatalf("car %s rides both %s and %s", c.CarID, other, tr.TrainID)
			}
			seen[c.CarID] = tr.TrainID
		}
	}
}

func TestBadOrderAndMisroutedCarsAreNotForwarded(t *testing.T) {
	cfg, order, occ := stage(t)
	plan, err := Build(cfg, order, occ)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	for _, tr := range plan.Trains {
		for _, c := range tr.Cars {
			if c.CarID == "MRL 620884" {
				t.Fatal("a bad ordered car must not be forwarded")
			}
			if c.CarID == "SP 240771" {
				t.Fatal("an unblockable car must not be forwarded")
			}
		}
	}
	held := map[string]string{}
	for _, h := range plan.Held {
		held[h.CarID] = h.Reason
	}
	if held["MRL 620884"] != HeldBadOrder {
		t.Fatalf("bad order car held for %q", held["MRL 620884"])
	}
	if held["SP 240771"] != HeldMisrouted {
		t.Fatalf("unblockable car held for %q", held["SP 240771"])
	}
}

func TestWeightCeilingHoldsCars(t *testing.T) {
	cfg, order, occ := stage(t)
	cfg.Departures[0].MaxTons = 400
	plan, err := Build(cfg, order, occ)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	tr := plan.Trains[0]
	if tr.TrailingTons > 400 {
		t.Fatalf("train carries %.1f tons over the 400 ton ceiling", tr.TrailingTons)
	}
	found := false
	for _, h := range tr.Held {
		if h.Reason == HeldWeight {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected a weight hold, got %+v", tr.Held)
	}
	if tr.Complete {
		t.Fatal("a train with held cars is not complete")
	}
}

func TestLengthAndAxleCeilingsHoldCars(t *testing.T) {
	cfg, order, occ := stage(t)
	cfg.Departures[0].MaxLengthFt = 400
	cfg.Departures[1].MaxAxles = 30
	plan, err := Build(cfg, order, occ)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	if plan.Trains[0].LengthFt > 400 {
		t.Fatalf("train length %d over the ceiling", plan.Trains[0].LengthFt)
	}
	if plan.Trains[1].Axles > 30 {
		t.Fatalf("train axles %d over the ceiling", plan.Trains[1].Axles)
	}
	reasons := map[string]bool{}
	for _, tr := range plan.Trains {
		for _, h := range tr.Held {
			reasons[h.Reason] = true
		}
	}
	if !reasons[HeldLength] || !reasons[HeldAxles] {
		t.Fatalf("expected length and axle holds, got %v", reasons)
	}
}

func TestPowerShortIsDetected(t *testing.T) {
	cfg, order, occ := stage(t)
	for i := range cfg.Power {
		cfg.Power[i].RatedTons = 100
	}
	plan, err := Build(cfg, order, occ)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	if plan.Stats.PowerShort == 0 {
		t.Fatal("expected trains short of power")
	}
	sawError := false
	for _, f := range plan.Findings {
		if f.Severity == config.SeverityError {
			sawError = true
		}
	}
	if !sawError {
		t.Fatal("short power must raise an error finding")
	}
}

func TestManifestAndPlacements(t *testing.T) {
	cfg, order, occ := stage(t)
	plan, err := Build(cfg, order, occ)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	tr := plan.Trains[0]
	lines := tr.Manifest()
	if len(lines) != len(tr.Cars)+1 {
		t.Fatalf("manifest has %d lines for %d cars", len(lines), len(tr.Cars))
	}
	index, err := model.NewCarIndex(order.AllCars())
	if err != nil {
		t.Fatalf("NewCarIndex: %v", err)
	}
	placements := plan.Placements(index)
	if len(placements) == 0 {
		t.Fatal("no placements produced")
	}
	for _, p := range placements {
		if !p.CrewAtFront {
			t.Fatalf("train %s placement must mark the crew position", p.TrackID)
		}
	}
}

func TestBuildIsDeterministic(t *testing.T) {
	cfg, order, occ := stage(t)
	plan, err := Build(cfg, order, occ)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	first, err := jsonx.MarshalCanonical(plan)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	for i := 0; i < 5; i++ {
		again, err := Build(cfg, order, occ)
		if err != nil {
			t.Fatalf("Build: %v", err)
		}
		data, err := jsonx.MarshalCanonical(again)
		if err != nil {
			t.Fatalf("marshal: %v", err)
		}
		if string(data) != string(first) {
			t.Fatal("departure program is not deterministic")
		}
	}
}

func TestMinCarsIsEnforced(t *testing.T) {
	cfg, order, occ := stage(t)
	cfg.Departures[2].MinCars = 400
	plan, err := Build(cfg, order, occ)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	tr := plan.Trains[2]
	if tr.Complete {
		t.Fatal("train should be incomplete")
	}
	found := false
	for _, f := range tr.Findings {
		if f.Severity == config.SeverityError {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected a minimum car error, got %+v", tr.Findings)
	}
}
