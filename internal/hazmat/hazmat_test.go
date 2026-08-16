package hazmat

import (
	"testing"

	"HumpYard/internal/config"
	"HumpYard/internal/model"
)

// rules builds a small hazmat regime for the tests.
func rules() *config.Config {
	cfg := &config.Config{}
	cfg.Hazmat = config.HazmatRules{
		Classes: []string{"1.1", "2.3", "3", "8"},
		IncompatiblePairs: []config.HazmatPair{
			{ClassA: "1.1", ClassB: "3", BufferCars: 3},
			{ClassA: "2.3", ClassB: "8", BufferCars: 2},
		},
		CabooseProhibited:   []string{"1.1", "2.3"},
		FlatSwitchOnly:      []string{"1.1"},
		MaxPlacardsPerTrack: 3,
		CabooseBufferCars:   2,
	}
	return cfg
}

// car builds a car with an optional hazmat class.
func car(number, class string, placard bool) model.Car {
	return model.Car{
		Mark: "TILX", Number: number, Kind: "tank", LengthFt: 60,
		TareTons: 35, GrossTons: 130, Axles: 4, Destination: "ALB",
		HazmatClass: class, Placard: placard, Restriction: model.CutFree,
	}
}

func TestBufferSpacingViolationIsReported(t *testing.T) {
	cfg := rules()
	p := Placement{TrackID: "C01", Kind: "classification", Cars: []model.Car{
		car("1", "2.3", true),
		car("2", "", false),
		car("3", "8", true),
	}}
	rep := Validate(cfg, []Placement{p})
	if rep.OK() {
		t.Fatal("expected a buffer violation")
	}
	v := rep.Violations[0]
	if v.Rule != RuleBuffer || v.Actual != 1 || v.Required != 2 {
		t.Fatalf("unexpected violation %+v", v)
	}
}

func TestBufferSpacingSatisfied(t *testing.T) {
	cfg := rules()
	p := Placement{TrackID: "C01", Cars: []model.Car{
		car("1", "2.3", true),
		car("2", "", false),
		car("3", "", false),
		car("4", "8", true),
	}}
	rep := Validate(cfg, []Placement{p})
	if !rep.OK() {
		t.Fatalf("expected no violations, got %+v", rep.Violations)
	}
	if rep.Cars != 4 || rep.Checked != 1 {
		t.Fatalf("unexpected counts %+v", rep)
	}
}

func TestLargerBufferIsCheckedBeyondASatisfiedPair(t *testing.T) {
	cfg := rules()
	// The 2.3/8 pair is satisfied at two cars apart, but the 1.1/3 pair
	// three positions later still needs three buffer cars.
	p := Placement{TrackID: "C02", Cars: []model.Car{
		car("1", "2.3", true),
		car("2", "", false),
		car("3", "", false),
		car("4", "8", true),
		car("5", "1.1", true),
		car("6", "", false),
		car("7", "3", true),
	}}
	rep := Validate(cfg, []Placement{p})
	found := false
	for _, v := range rep.Violations {
		if v.ClassA == "1.1" && v.ClassB == "3" {
			found = true
			if v.Actual != 1 || v.Required != 3 {
				t.Fatalf("unexpected spacing %+v", v)
			}
		}
	}
	if !found {
		t.Fatalf("1.1/3 violation was not reported: %+v", rep.Violations)
	}
}

func TestCabooseAdjacencyIsEnforced(t *testing.T) {
	cfg := rules()
	p := Placement{TrackID: "C06", CabooseAtRear: true, Cars: []model.Car{
		car("1", "", false),
		car("2", "2.3", true),
	}}
	rep := Validate(cfg, []Placement{p})
	if rep.OK() {
		t.Fatal("expected a caboose violation")
	}
	if rep.Violations[0].Rule != RuleCaboose {
		t.Fatalf("unexpected rule %q", rep.Violations[0].Rule)
	}
	p.CabooseAtRear = false
	if rep := Validate(cfg, []Placement{p}); !rep.OK() {
		t.Fatalf("no caboose means no violation, got %+v", rep.Violations)
	}
}

func TestCrewPositionAtHeadIsEnforced(t *testing.T) {
	cfg := rules()
	p := Placement{TrackID: "T410", CrewAtFront: true, Cars: []model.Car{
		car("1", "1.1", true),
		car("2", "", false),
		car("3", "", false),
	}}
	rep := Validate(cfg, []Placement{p})
	if rep.OK() {
		t.Fatal("expected a crew position violation")
	}
	if rep.Violations[0].CarB != "crew-position" {
		t.Fatalf("unexpected violation %+v", rep.Violations[0])
	}
}

func TestPlacardCeilingIsEnforced(t *testing.T) {
	cfg := rules()
	cars := []model.Car{
		car("1", "3", true), car("2", "3", true), car("3", "3", true), car("4", "3", true),
	}
	rep := Validate(cfg, []Placement{{TrackID: "C03", Cars: cars}})
	found := false
	for _, v := range rep.Violations {
		if v.Rule == RulePlacard && v.Actual == 4 && v.Required == 3 {
			found = true
		}
	}
	if !found {
		t.Fatalf("placard ceiling was not enforced: %+v", rep.Violations)
	}
}

func TestUndeclaredClassIsReported(t *testing.T) {
	cfg := rules()
	rep := Validate(cfg, []Placement{{TrackID: "C04", Cars: []model.Car{car("1", "9.9", true)}}})
	if rep.OK() {
		t.Fatal("expected an undeclared class violation")
	}
	if rep.Violations[0].Rule != RuleUnknown {
		t.Fatalf("unexpected rule %q", rep.Violations[0].Rule)
	}
}

func TestTalliesAndFindings(t *testing.T) {
	cfg := rules()
	rep := Validate(cfg, []Placement{{TrackID: "C05", Cars: []model.Car{
		car("1", "3", true), car("2", "", false),
	}}})
	if len(rep.Tallies) != 1 {
		t.Fatalf("expected one tally, got %d", len(rep.Tallies))
	}
	if rep.Tallies[0].HazmatCars != 1 || rep.Tallies[0].Placards != 1 || rep.Tallies[0].Limit != 3 {
		t.Fatalf("unexpected tally %+v", rep.Tallies[0])
	}
	if len(rep.Findings()) != len(rep.Violations) {
		t.Fatal("findings must mirror violations")
	}
}

func TestMinBufferNeededAndSeparationCandidates(t *testing.T) {
	cfg := rules()
	if got := MinBufferNeeded(cfg); got != 3 {
		t.Fatalf("MinBufferNeeded = %d, want 3", got)
	}
	p := Placement{TrackID: "C01", Cars: []model.Car{
		car("1", "2.3", true),
		car("2", "8", true),
	}}
	spots := SeparationCandidates(cfg, p)
	if len(spots) != 1 || spots[0] != 1 {
		t.Fatalf("unexpected insertion points %v", spots)
	}
}

func TestViolationsAreSortedDeterministically(t *testing.T) {
	cfg := rules()
	placements := []Placement{
		{TrackID: "C09", Cars: []model.Car{car("1", "2.3", true), car("2", "8", true)}},
		{TrackID: "C01", Cars: []model.Car{car("3", "2.3", true), car("4", "8", true)}},
	}
	rep := Validate(cfg, placements)
	if len(rep.Violations) < 2 {
		t.Fatalf("expected two violations, got %d", len(rep.Violations))
	}
	if rep.Violations[0].TrackID != "C01" {
		t.Fatalf("violations are not sorted: %+v", rep.Violations)
	}
	if rep.Tallies[0].TrackID != "C01" {
		t.Fatalf("tallies are not sorted: %+v", rep.Tallies)
	}
}
