package hump

import (
	"path/filepath"
	"testing"

	"HumpYard/internal/blocking"
	"HumpYard/internal/config"
	"HumpYard/internal/ingest"
	"HumpYard/internal/jsonx"
	"HumpYard/internal/model"
)

// load reads the example configuration and yard order and blocks it.
func load(t *testing.T) (*config.Config, model.YardOrder, blocking.Plan) {
	t.Helper()
	cfg, _, err := config.Load(filepath.Join("..", "..", "examples", "config.json"))
	if err != nil {
		t.Fatalf("config.Load: %v", err)
	}
	res, err := ingest.Load(filepath.Join("..", "..", "examples", "order.json"), cfg)
	if err != nil {
		t.Fatalf("ingest.Load: %v", err)
	}
	return cfg, res.Order, blocking.Build(cfg, res.Order)
}

// flatReasonOf returns the recorded flat-switch reason for a car.
func flatReasonOf(plan Plan, carID string) string {
	for _, f := range plan.FlatMoves {
		for _, id := range f.CarIDs {
			if id == carID {
				return f.Reason
			}
		}
	}
	return ""
}

func TestBuildCoversEveryCarExactlyOnce(t *testing.T) {
	cfg, order, bp := load(t)
	plan := Build(cfg, order, bp)
	seen := map[string]int{}
	for _, m := range plan.Movements {
		for _, id := range m.CarIDs {
			seen[id]++
		}
	}
	if len(seen) != order.CarCount() {
		t.Fatalf("movements cover %d cars, order holds %d", len(seen), order.CarCount())
	}
	for id, n := range seen {
		if n != 1 {
			t.Fatalf("car %s appears %d times", id, n)
		}
	}
	if plan.Stats.CarsHumped+plan.Stats.CarsFlat != order.CarCount() {
		t.Fatalf("humped %d plus flat %d does not equal %d", plan.Stats.CarsHumped, plan.Stats.CarsFlat, order.CarCount())
	}
}

func TestFlatSwitchReasons(t *testing.T) {
	cfg, order, bp := load(t)
	plan := Build(cfg, order, bp)
	want := map[string]string{
		"MRL 620884":  ReasonBadOrder,
		"DTTX 724110": ReasonExcessLen,
		"AOK 100234":  ReasonRetarder,
		"EEC 300117":  ReasonHazmatFlat,
		"CSXT 142200": ReasonRestriction,
	}
	for car, reason := range want {
		if got := flatReasonOf(plan, car); got != reason {
			t.Fatalf("car %s flat reason %q, want %q", car, got, reason)
		}
	}
	counts := plan.FlatReasonCounts()
	if counts[ReasonBadOrder] != 1 {
		t.Fatalf("bad order count %d", counts[ReasonBadOrder])
	}
}

func TestCutsStayWithinLimitsAndSingleTrack(t *testing.T) {
	cfg, order, bp := load(t)
	plan := Build(cfg, order, bp)
	for _, cut := range plan.Cuts {
		if len(cut.CarIDs) > cfg.Hump.MaxCutCars {
			t.Fatalf("cut %d holds %d cars over the limit of %d", cut.Index, len(cut.CarIDs), cfg.Hump.MaxCutCars)
		}
		if cut.TrackID == "" {
			t.Fatalf("cut %d has no track", cut.Index)
		}
		if cut.RiderRequired && len(cut.CarIDs) > 2 {
			t.Fatalf("rider cut %d holds %d cars", cut.Index, len(cut.CarIDs))
		}
	}
}

func TestDrawbarPairStaysTogether(t *testing.T) {
	cfg, order, bp := load(t)
	plan := Build(cfg, order, bp)
	found := false
	for _, cut := range plan.Cuts {
		hasA, hasB := false, false
		for _, id := range cut.CarIDs {
			if id == "GTW 583202" {
				hasA = true
			}
			if id == "GTW 583203" {
				hasB = true
			}
		}
		if hasA || hasB {
			if !(hasA && hasB) {
				t.Fatalf("cut %d split the drawbar pair: %v", cut.Index, cut.CarIDs)
			}
			found = true
		}
	}
	if !found {
		t.Fatal("drawbar pair was never humped")
	}
}

func TestDrawbarPairSplitByBlockIsFlatSwitched(t *testing.T) {
	cfg, order, bp := load(t)
	for i := range order.Trains {
		for j := range order.Trains[i].Cars {
			if order.Trains[i].Cars[j].ID() == "GTW 583203" {
				order.Trains[i].Cars[j].Destination = "ALB"
			}
		}
	}
	bp = blocking.Build(cfg, order)
	plan := Build(cfg, order, bp)
	if got := flatReasonOf(plan, "GTW 583202"); got != ReasonDrawbar {
		t.Fatalf("GTW 583202 reason %q, want %q", got, ReasonDrawbar)
	}
	if got := flatReasonOf(plan, "GTW 583203"); got != ReasonDrawbar {
		t.Fatalf("GTW 583203 reason %q, want %q", got, ReasonDrawbar)
	}
}

func TestRoughRiderNeedsARider(t *testing.T) {
	cfg, order, bp := load(t)
	plan := Build(cfg, order, bp)
	for _, cut := range plan.Cuts {
		for _, id := range cut.CarIDs {
			if id == "KCS 130544" && !cut.RiderRequired {
				t.Fatalf("cut %d with a rough rider does not request a rider", cut.Index)
			}
		}
	}
	if plan.Stats.RiderCuts == 0 {
		t.Fatal("expected at least one rider cut")
	}
}

func TestMovementsAreOrderedAndNumbered(t *testing.T) {
	cfg, order, bp := load(t)
	plan := Build(cfg, order, bp)
	for i, m := range plan.Movements {
		if m.Seq != i+1 {
			t.Fatalf("movement %d has seq %d", i, m.Seq)
		}
		if i > 0 && plan.Movements[i-1].StartMinute > m.StartMinute {
			t.Fatalf("movements are not ordered by minute at %d", i)
		}
	}
	byTrack := plan.CarsByTrack()
	if len(byTrack) == 0 {
		t.Fatal("no cars by track")
	}
}

func TestBuildIsDeterministic(t *testing.T) {
	cfg, order, bp := load(t)
	first, err := jsonx.MarshalCanonical(Build(cfg, order, bp))
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	for i := 0; i < 5; i++ {
		again, err := jsonx.MarshalCanonical(Build(cfg, order, bp))
		if err != nil {
			t.Fatalf("marshal: %v", err)
		}
		if string(again) != string(first) {
			t.Fatal("crest plan is not deterministic")
		}
	}
}

func TestCrestCapacityFindingIsRaised(t *testing.T) {
	cfg, order, bp := load(t)
	for i := range cfg.Shifts {
		cfg.Shifts[i].HumpCapacity = 1
	}
	plan := Build(cfg, order, bp)
	found := false
	for _, f := range plan.Findings {
		if f.Scope == "hump" && f.Severity == config.SeverityError {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected a crest capacity error, got %+v", plan.Findings)
	}
}

func TestHumpMinutesRoundUp(t *testing.T) {
	cfg := &config.Config{}
	cfg.Hump.CarsPerMinute = 3
	cfg.Hump.CutChangeMinutes = 2
	if got := humpMinutes(cfg, 4); got != 4 {
		t.Fatalf("humpMinutes(4) = %d, want 4", got)
	}
	if got := humpMinutes(cfg, 3); got != 3 {
		t.Fatalf("humpMinutes(3) = %d, want 3", got)
	}
}
