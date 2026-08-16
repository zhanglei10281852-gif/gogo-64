package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// examplePath resolves a file in the repository examples directory.
func examplePath(name string) string {
	return filepath.Join("..", "..", "examples", name)
}

func TestLoadExampleConfiguration(t *testing.T) {
	cfg, rep, err := Load(examplePath("config.json"))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if !rep.OK() {
		t.Fatalf("example configuration reported errors: %v", rep.Err())
	}
	if cfg.Yard.ID != "WBKH" {
		t.Fatalf("yard id %q", cfg.Yard.ID)
	}
	if len(cfg.Class) != 9 {
		t.Fatalf("classification tracks %d", len(cfg.Class))
	}
	if got, ok := cfg.BlockForDestination("BOS"); !ok || got != "EAST" {
		t.Fatalf("BOS resolved to %q (%v)", got, ok)
	}
	if _, ok := cfg.BlockForDestination("NOWHERE"); ok {
		t.Fatal("unknown destination should not resolve")
	}
}

func TestNormalizeSortsCollections(t *testing.T) {
	cfg, _, err := Load(examplePath("config.json"))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	for i := 1; i < len(cfg.Class); i++ {
		if cfg.Class[i-1].ID > cfg.Class[i].ID {
			t.Fatalf("classification tracks are not sorted: %s before %s", cfg.Class[i-1].ID, cfg.Class[i].ID)
		}
	}
	for i := 1; i < len(cfg.Blocks); i++ {
		if cfg.Blocks[i-1].Priority > cfg.Blocks[i].Priority {
			t.Fatalf("blocks are not in priority order")
		}
	}
	for i := 1; i < len(cfg.Hazmat.IncompatiblePairs); i++ {
		if cfg.Hazmat.IncompatiblePairs[i-1].Key() > cfg.Hazmat.IncompatiblePairs[i].Key() {
			t.Fatalf("hazmat pairs are not sorted")
		}
	}
}

func TestParseRejectsUnknownField(t *testing.T) {
	data, err := os.ReadFile(examplePath("config.json"))
	if err != nil {
		t.Fatalf("read example: %v", err)
	}
	broken := strings.Replace(string(data), "\"long_car_ft\"", "\"long_car_feet\"", 1)
	if _, _, err := Parse([]byte(broken)); err == nil {
		t.Fatal("expected a decode error for an unknown field")
	}
}

func TestValidateReportsMissingBlockTrack(t *testing.T) {
	cfg := mustLoad(t)
	cfg.Class = cfg.Class[:0]
	rep := Validate(cfg)
	if rep.OK() {
		t.Fatal("configuration without classification tracks must fail")
	}
	if !strings.Contains(rep.Err().Error(), "classification track") {
		t.Fatalf("unexpected error: %v", rep.Err())
	}
}

func TestValidateRejectsDuplicateBlockPriority(t *testing.T) {
	cfg := mustLoad(t)
	cfg.Blocks[1].Priority = cfg.Blocks[0].Priority
	rep := Validate(cfg)
	if rep.OK() {
		t.Fatal("duplicate block priorities must fail")
	}
	if !strings.Contains(rep.Err().Error(), "priority") {
		t.Fatalf("unexpected error: %v", rep.Err())
	}
}

func TestValidateRejectsOverlappingShifts(t *testing.T) {
	cfg := mustLoad(t)
	cfg.Shifts[1].StartMinute = cfg.Shifts[0].StartMinute + 10
	rep := Validate(cfg)
	if rep.OK() {
		t.Fatal("overlapping shifts must fail")
	}
	if !strings.Contains(rep.Err().Error(), "before shift") {
		t.Fatalf("unexpected error: %v", rep.Err())
	}
}

func TestValidateRejectsYardOnlyPowerOnRoadTrain(t *testing.T) {
	cfg := mustLoad(t)
	cfg.Departures[0].Locomotives = []string{"YE01"}
	rep := Validate(cfg)
	if rep.OK() {
		t.Fatal("yard-only power on a road train must fail")
	}
	if !strings.Contains(rep.Err().Error(), "yard service") {
		t.Fatalf("unexpected error: %v", rep.Err())
	}
}

func TestValidateRejectsUndeclaredHazmatClassInPair(t *testing.T) {
	cfg := mustLoad(t)
	cfg.Hazmat.IncompatiblePairs[0].ClassA = "9.9"
	rep := Validate(cfg)
	if rep.OK() {
		t.Fatal("undeclared hazmat class must fail")
	}
}

func TestValidateRequiresRepairAndOverflowToDiffer(t *testing.T) {
	cfg := mustLoad(t)
	cfg.Yard.RepairTrack = cfg.Yard.OverflowTrack
	rep := Validate(cfg)
	if rep.OK() {
		t.Fatal("repair and overflow tracks must differ")
	}
}

func TestHazmatRuleLookups(t *testing.T) {
	cfg := mustLoad(t)
	buffer, ok := cfg.Hazmat.BufferFor("3", "1.1")
	if !ok || buffer != 3 {
		t.Fatalf("buffer for 1.1/3 is %d (%v)", buffer, ok)
	}
	if _, ok := cfg.Hazmat.BufferFor("3", "8"); ok {
		t.Fatal("3/8 is not a regulated pair in the example")
	}
	if !cfg.Hazmat.RequiresFlatSwitch("1.1") {
		t.Fatal("class 1.1 must be flat switched")
	}
	if !cfg.Hazmat.CabooseBarred("2.3") {
		t.Fatal("class 2.3 is barred next to a caboose")
	}
}

func TestDestinationCatalogIsSorted(t *testing.T) {
	cfg := mustLoad(t)
	cat := cfg.DestinationCatalog()
	if len(cat) == 0 {
		t.Fatal("catalog is empty")
	}
	for i := 1; i < len(cat); i++ {
		if cat[i-1] >= cat[i] {
			t.Fatalf("catalog is not sorted: %v", cat)
		}
	}
}

func TestFindingsSortIsStable(t *testing.T) {
	col := collector{}
	col.warnf("z", "b", "later")
	col.errf("a", "a", "first")
	col.warnf("a", "a", "middle")
	rep := col.report()
	if rep.Findings[0].Severity != SeverityError {
		t.Fatalf("errors must sort first: %+v", rep.Findings)
	}
	if rep.Findings[1].Scope != "a" || rep.Findings[2].Scope != "z" {
		t.Fatalf("unexpected order: %+v", rep.Findings)
	}
}

// mustLoad loads the example configuration or fails the test.
func mustLoad(t *testing.T) *Config {
	t.Helper()
	cfg, _, err := Load(examplePath("config.json"))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	return cfg
}
