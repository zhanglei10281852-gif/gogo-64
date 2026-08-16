package depart

import (
	"fmt"
	"strings"
	"testing"

	"HumpYard/internal/config"
	"HumpYard/internal/model"
	"HumpYard/internal/occupancy"
)

// shortTrackYard builds a yard with one eastbound classification track and one
// departure track. The train ceilings are generous; the departure track is the
// tight constraint.
func shortTrackYard(departureCapacityFt int, departureLimitTons float64) *config.Config {
	cfg := &config.Config{Version: 1}
	cfg.Yard = config.Yard{
		ID:             "WBKH",
		Name:           "Westbrook Hump Yard",
		CrestGradePct:  2.4,
		LongCarFt:      89,
		OverflowTrack:  "C08",
		RepairTrack:    "C09",
		CouplerSlackFt: 2,
	}
	cfg.Blocks = []model.Block{
		{ID: "EAST", Name: "Eastbound manifest", Priority: 10, Destinations: []string{"ALB"}},
	}
	cfg.Class = []model.ClassTrack{
		{ID: "C01", Block: "EAST", CapacityFt: 5000, WeightLimitTons: 9000, GradePct: 1.2},
	}
	cfg.Departure = []model.SupportTrack{
		{ID: "D01", Role: model.RoleDeparture, CapacityFt: departureCapacityFt,
			WeightLimitTons: departureLimitTons, LeadMinutes: 20},
	}
	cfg.Power = []model.Locomotive{
		{ID: "RD10", Model: "SD40-2", LengthFt: 68, WeightTons: 195, Axles: 6,
			RatedTons: 9000, Horsepower: 3000},
	}
	cfg.Departures = []config.DepartureOrder{
		{TrainID: "T410", TrackID: "D01", BlockOrder: []string{"EAST"},
			MaxLengthFt: 5000, MaxTons: 8000, MaxAxles: 400,
			Locomotives: []string{"RD10"}, DepartMin: 690, MinCars: 0},
	}
	cfg.Hazmat = config.HazmatRules{MaxPlacardsPerTrack: 12}
	return cfg
}

// eastboundOrder builds one arrival of identical 60 ft eastbound cars.
func eastboundOrder(count int) model.YardOrder {
	cars := make([]model.Car, 0, count)
	for i := 0; i < count; i++ {
		cars = append(cars, model.Car{
			Mark: "AAAX", Number: fmt.Sprintf("10%04d", i+1),
			Kind: "boxcar", LengthFt: 60, TareTons: 30, GrossTons: 100, Axles: 4,
			Destination: "ALB", Restriction: model.CutFree,
		})
	}
	return model.YardOrder{
		OrderID: "WBKH-9004",
		YardID:  "WBKH",
		Trains: []model.InboundTrain{{
			ID: "T101", ArrivalMinute: 30, ReceivingID: "R01", Inspected: true, Cars: cars,
		}},
	}
}

// standingOnC01 reports the arrival as already classified on C01.
func standingOnC01(order model.YardOrder) occupancy.Result {
	ids := make([]string, 0, order.CarCount())
	usedFt := 0
	tons := 0.0
	for _, car := range order.AllCars() {
		ids = append(ids, car.ID())
		usedFt += car.LengthFt + 2
		tons += car.GrossTons
	}
	return occupancy.Result{Tracks: []occupancy.TrackState{{
		TrackID: "C01", Block: "EAST", CapacityFt: 5000, UsedFt: usedFt,
		RemainingFt: 5000 - usedFt, LimitTons: 9000, UsedTons: tons,
		RemainingTons: 9000 - tons, CarIDs: ids,
	}}}
}

// trackErrors returns the error findings that name a support track.
func trackErrors(findings []config.Finding, trackID string) []config.Finding {
	var out []config.Finding
	for _, f := range findings {
		if f.Severity == config.SeverityError && strings.Contains(f.Message, trackID) {
			out = append(out, f)
		}
	}
	return out
}

func TestBuildReportsAConsistLongerThanItsDepartureTrack(t *testing.T) {
	order := eastboundOrder(10)
	occ := standingOnC01(order)

	// The consist is 690 ft of power and cars on a 600 ft departure track.
	cfg := shortTrackYard(600, 9000)
	plan, err := Build(cfg, order, occ)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	if len(plan.Trains) != 1 {
		t.Fatalf("built %d trains, want 1", len(plan.Trains))
	}
	tr := plan.Trains[0]
	if len(tr.Cars) != 10 || len(tr.Held) != 0 {
		t.Fatalf("train carries %d cars and holds %d, want 10 and 0", len(tr.Cars), len(tr.Held))
	}
	if tr.LengthFt != 690 {
		t.Fatalf("consist length %d ft, want 690 ft", tr.LengthFt)
	}
	got := trackErrors(tr.Findings, "D01")
	if len(got) != 1 {
		t.Fatalf("expected one departure track error, got %d: %+v", len(got), tr.Findings)
	}
	if got[0].Scope != "train" || got[0].Subject != "T410" {
		t.Fatalf("unexpected finding %+v", got[0])
	}
	if len(trackErrors(plan.Findings, "D01")) != 1 {
		t.Fatalf("the plan level findings must carry the error too: %+v", plan.Findings)
	}

	// A departure track that is long enough raises nothing.
	roomy := shortTrackYard(5000, 9000)
	roomyPlan, err := Build(roomy, order, occ)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	for _, f := range roomyPlan.Findings {
		if f.Severity == config.SeverityError {
			t.Fatalf("a compliant train must not raise errors: %+v", roomyPlan.Findings)
		}
	}

	// The weight allowance of the same track is still policed.
	heavy := shortTrackYard(5000, 500)
	heavyPlan, err := Build(heavy, order, occ)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	if len(trackErrors(heavyPlan.Trains[0].Findings, "D01")) != 1 {
		t.Fatalf("expected one departure track weight error: %+v", heavyPlan.Trains[0].Findings)
	}
}
