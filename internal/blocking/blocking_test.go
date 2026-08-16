package blocking

import (
	"path/filepath"
	"testing"

	"HumpYard/internal/config"
	"HumpYard/internal/ingest"
	"HumpYard/internal/jsonx"
	"HumpYard/internal/model"
)

// load reads the example configuration and yard order.
func load(t *testing.T) (*config.Config, model.YardOrder) {
	t.Helper()
	cfg, _, err := config.Load(filepath.Join("..", "..", "examples", "config.json"))
	if err != nil {
		t.Fatalf("config.Load: %v", err)
	}
	res, err := ingest.Load(filepath.Join("..", "..", "examples", "order.json"), cfg)
	if err != nil {
		t.Fatalf("ingest.Load: %v", err)
	}
	return cfg, res.Order
}

func TestBuildAssignsEveryCar(t *testing.T) {
	cfg, order := load(t)
	plan := Build(cfg, order)
	if len(plan.Assignments) != order.CarCount() {
		t.Fatalf("got %d assignments for %d cars", len(plan.Assignments), order.CarCount())
	}
	if len(plan.Rejected) != 0 {
		t.Fatalf("unexpected rejected cars: %v", plan.Rejected)
	}
	for i := 1; i < len(plan.Assignments); i++ {
		if plan.Assignments[i-1].CarID >= plan.Assignments[i].CarID {
			t.Fatalf("assignments are not sorted by car id at %d", i)
		}
	}
}

func TestBuildIsDeterministic(t *testing.T) {
	cfg, order := load(t)
	first, err := jsonx.MarshalCanonical(Build(cfg, order))
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	for i := 0; i < 5; i++ {
		again, err := jsonx.MarshalCanonical(Build(cfg, order))
		if err != nil {
			t.Fatalf("marshal: %v", err)
		}
		if string(again) != string(first) {
			t.Fatal("blocking plan is not deterministic")
		}
	}
}

func TestBadOrderCarGoesToRepairTrack(t *testing.T) {
	cfg, order := load(t)
	plan := Build(cfg, order)
	a, ok := plan.AssignmentFor("MRL 620884")
	if !ok {
		t.Fatal("bad ordered car is missing from the plan")
	}
	if a.Status != StatusRepair {
		t.Fatalf("status %q", a.Status)
	}
	if a.TrackID != cfg.Yard.RepairTrack {
		t.Fatalf("track %q, want %q", a.TrackID, cfg.Yard.RepairTrack)
	}
}

func TestUnknownDestinationIsUnblocked(t *testing.T) {
	cfg, order := load(t)
	plan := Build(cfg, order)
	a, ok := plan.AssignmentFor("SP 240771")
	if !ok {
		t.Fatal("car with unknown destination is missing")
	}
	if a.Status != StatusUnblocked {
		t.Fatalf("status %q", a.Status)
	}
	if a.TrackID != cfg.Yard.OverflowTrack {
		t.Fatalf("track %q, want the overflow track", a.TrackID)
	}
	if a.Block != "" {
		t.Fatalf("unblocked car should carry no block, got %q", a.Block)
	}
}

func TestCapacityOverflowUsesOverflowTrack(t *testing.T) {
	cfg, order := load(t)
	for i := range cfg.Class {
		if cfg.Class[i].Block == "EAST" {
			cfg.Class[i].CapacityFt = 130
		}
	}
	plan := Build(cfg, order)
	if len(plan.Overflow) == 0 {
		t.Fatal("expected overflow cars when the EAST block is tiny")
	}
	for _, id := range plan.Overflow {
		a, _ := plan.AssignmentFor(id)
		if a.TrackID != cfg.Yard.OverflowTrack {
			t.Fatalf("overflow car %s is on %s", id, a.TrackID)
		}
	}
	for _, tl := range plan.Tracks {
		if tl.RemainingFt < 0 {
			t.Fatalf("track %s is over capacity", tl.TrackID)
		}
	}
}

func TestWeightLimitIsHonoured(t *testing.T) {
	cfg, order := load(t)
	for i := range cfg.Class {
		cfg.Class[i].WeightLimitTons = 260
	}
	plan := Build(cfg, order)
	for _, tl := range plan.Tracks {
		if tl.UsedTons > tl.LimitTons {
			t.Fatalf("track %s carries %.1f tons over its %.1f ton limit", tl.TrackID, tl.UsedTons, tl.LimitTons)
		}
	}
}

func TestPlacardCeilingDivertsCars(t *testing.T) {
	cfg, order := load(t)
	cfg.Hazmat.MaxPlacardsPerTrack = 1
	plan := Build(cfg, order)
	for _, tl := range plan.Tracks {
		if tl.TrackID == cfg.Yard.OverflowTrack {
			continue
		}
		placards := 0
		for _, id := range tl.CarIDs {
			for _, tr := range order.Trains {
				for _, c := range tr.Cars {
					if c.ID() == id && c.Placard {
						placards++
					}
				}
			}
		}
		if placards > 1 {
			t.Fatalf("track %s holds %d placarded cars with a ceiling of 1", tl.TrackID, placards)
		}
	}
}

func TestTrackRestrictionsAreRespected(t *testing.T) {
	cfg, order := load(t)
	plan := Build(cfg, order)
	index, err := model.NewCarIndex(order.AllCars())
	if err != nil {
		t.Fatalf("NewCarIndex: %v", err)
	}
	for _, tl := range plan.Tracks {
		track, ok := cfg.ClassTrackByID(tl.TrackID)
		if !ok {
			t.Fatalf("unknown track %s", tl.TrackID)
		}
		if track.ID == cfg.Yard.RepairTrack {
			continue
		}
		for _, id := range tl.CarIDs {
			if ok, why := track.Accepts(index[id], cfg.Yard.LongCarFt); !ok {
				t.Fatalf("car %s on %s: %s", id, track.ID, why)
			}
		}
	}
}

func TestDigestCountsMatchAssignments(t *testing.T) {
	cfg, order := load(t)
	plan := Build(cfg, order)
	d := plan.Digest()
	if d.Cars != len(plan.Assignments) {
		t.Fatalf("digest counted %d cars, plan has %d", d.Cars, len(plan.Assignments))
	}
	if d.Assigned+d.Overflow+d.Repair+d.Unblocked+d.Rejected != d.Cars {
		t.Fatalf("status counts do not add up: %+v", d)
	}
	if d.FillPercent <= 0 {
		t.Fatalf("fill percent %v", d.FillPercent)
	}
}
