package rehandle

import (
	"path/filepath"
	"testing"

	"HumpYard/internal/blocking"
	"HumpYard/internal/config"
	"HumpYard/internal/depart"
	"HumpYard/internal/hump"
	"HumpYard/internal/ingest"
	"HumpYard/internal/jsonx"
	"HumpYard/internal/model"
	"HumpYard/internal/occupancy"
)

// scenario is every stage output needed by the rework analysis.
type scenario struct {
	cfg   *config.Config
	order model.YardOrder
	hp    hump.Plan
	occ   occupancy.Result
	dp    depart.Plan
}

// run executes every stage. pre mutates the configuration before planning and
// post mutates it afterwards, which lets a test show the analysis a plan that
// no longer matches the blocking rules.
func run(t *testing.T, pre, post func(*config.Config)) scenario {
	t.Helper()
	cfg, _, err := config.Load(filepath.Join("..", "..", "examples", "config.json"))
	if err != nil {
		t.Fatalf("config.Load: %v", err)
	}
	res, err := ingest.Load(filepath.Join("..", "..", "examples", "order.json"), cfg)
	if err != nil {
		t.Fatalf("ingest.Load: %v", err)
	}
	if pre != nil {
		pre(cfg)
	}
	bp := blocking.Build(cfg, res.Order)
	hp := hump.Build(cfg, res.Order, bp)
	occ, err := occupancy.Simulate(cfg, res.Order, hp)
	if err != nil {
		t.Fatalf("Simulate: %v", err)
	}
	dp, err := depart.Build(cfg, res.Order, occ)
	if err != nil {
		t.Fatalf("depart.Build: %v", err)
	}
	if post != nil {
		post(cfg)
	}
	if post != nil {
		post(cfg)
	}
	return scenario{cfg: cfg, order: res.Order, hp: hp, occ: occ, dp: dp}
}

func TestAnalyzeCountsTheExampleYard(t *testing.T) {
	s := run(t, nil, nil)
	rep, err := Analyze(s.cfg, s.order, s.hp, s.occ, s.dp)
	if err != nil {
		t.Fatalf("Analyze: %v", err)
	}
	if rep.TotalCars != s.order.CarCount() {
		t.Fatalf("total cars %d, want %d", rep.TotalCars, s.order.CarCount())
	}
	if rep.RehandlePct <= 0 || rep.RehandlePct >= 100 {
		t.Fatalf("rehandle percent %v", rep.RehandlePct)
	}
	if _, ok := rep.ItemFor("MRL 620884"); !ok {
		t.Fatal("bad ordered car should be listed")
	}
	item, ok := rep.ItemFor("SP 240771")
	if !ok || item.Category != CatUnblocked || !item.SecondPass {
		t.Fatalf("unblockable car item %+v", item)
	}
}

func TestRepairHoldIsNotCountedAsRehandle(t *testing.T) {
	s := run(t, nil, nil)
	rep, err := Analyze(s.cfg, s.order, s.hp, s.occ, s.dp)
	if err != nil {
		t.Fatalf("Analyze: %v", err)
	}
	repairs := 0
	for _, it := range rep.Items {
		if it.Category == CatRepair {
			repairs++
		}
	}
	if repairs == 0 {
		t.Fatal("expected a repair hold in the example")
	}
	if rep.RehandleCars != len(rep.Items)-repairs {
		t.Fatalf("rehandle cars %d, items %d, repairs %d", rep.RehandleCars, len(rep.Items), repairs)
	}
}

func TestMisrouteIsDetected(t *testing.T) {
	s := run(t, nil, func(cfg *config.Config) {
		// Move BOS out of the EAST block after the cars were blocked so the
		// analysis sees cars standing on a track serving the wrong block.
		for i := range cfg.Blocks {
			if cfg.Blocks[i].ID != "EAST" {
				continue
			}
			kept := cfg.Blocks[i].Destinations[:0]
			for _, d := range cfg.Blocks[i].Destinations {
				if d != "BOS" {
					kept = append(kept, d)
				}
			}
			cfg.Blocks[i].Destinations = kept
		}
		for i := range cfg.Blocks {
			if cfg.Blocks[i].ID == "WEST" {
				cfg.Blocks[i].Destinations = append(cfg.Blocks[i].Destinations, "BOS")
			}
		}
	})
	rep, err := Analyze(s.cfg, s.order, s.hp, s.occ, s.dp)
	if err != nil {
		t.Fatalf("Analyze: %v", err)
	}
	misroutes := 0
	for _, it := range rep.Items {
		if it.Category == CatMisroute {
			misroutes++
			if !it.SecondPass {
				t.Fatalf("misrouted car %s must need a second pass", it.CarID)
			}
		}
	}
	if misroutes == 0 {
		t.Fatal("expected misrouted cars after the block change")
	}
	sawError := false
	for _, f := range rep.Findings {
		if f.Severity == config.SeverityError && f.Scope == "rehandle" {
			sawError = true
		}
	}
	if !sawError {
		t.Fatal("misroutes must raise error findings")
	}
}

func TestSecondPassIncludesOverflowedCars(t *testing.T) {
	s := run(t, func(cfg *config.Config) {
		for i := range cfg.Class {
			if cfg.Class[i].Block == "EAST" {
				cfg.Class[i].CapacityFt = 130
			}
		}
	}, nil)
	rep, err := Analyze(s.cfg, s.order, s.hp, s.occ, s.dp)
	if err != nil {
		t.Fatalf("Analyze: %v", err)
	}
	if rep.SecondPass == 0 {
		t.Fatal("expected second pass cars after overflow")
	}
	if len(rep.SecondPassCars()) != rep.SecondPass {
		t.Fatalf("SecondPassCars returned %d, stats say %d", len(rep.SecondPassCars()), rep.SecondPass)
	}
	if rep.SecondPassPct <= 0 {
		t.Fatalf("second pass percent %v", rep.SecondPassPct)
	}
}

func TestCountsAreSortedAndComplete(t *testing.T) {
	s := run(t, nil, nil)
	rep, err := Analyze(s.cfg, s.order, s.hp, s.occ, s.dp)
	if err != nil {
		t.Fatalf("Analyze: %v", err)
	}
	total := 0
	for i, c := range rep.Counts {
		total += c.Cars
		if i > 0 && rep.Counts[i-1].Category >= c.Category {
			t.Fatalf("categories are not sorted: %+v", rep.Counts)
		}
	}
	if total != len(rep.Items) {
		t.Fatalf("category totals %d, items %d", total, len(rep.Items))
	}
	for i := 1; i < len(rep.Items); i++ {
		if rep.Items[i-1].CarID >= rep.Items[i].CarID {
			t.Fatalf("items are not sorted by car id")
		}
	}
}

func TestAnalyzeIsDeterministic(t *testing.T) {
	s := run(t, nil, nil)
	rep, err := Analyze(s.cfg, s.order, s.hp, s.occ, s.dp)
	if err != nil {
		t.Fatalf("Analyze: %v", err)
	}
	first, err := jsonx.MarshalCanonical(rep)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	for i := 0; i < 5; i++ {
		again, err := Analyze(s.cfg, s.order, s.hp, s.occ, s.dp)
		if err != nil {
			t.Fatalf("Analyze: %v", err)
		}
		data, err := jsonx.MarshalCanonical(again)
		if err != nil {
			t.Fatalf("marshal: %v", err)
		}
		if string(data) != string(first) {
			t.Fatal("rework analysis is not deterministic")
		}
	}
}
