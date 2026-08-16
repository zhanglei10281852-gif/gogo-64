package model

import (
	"strings"
	"testing"
)

func goodCar() Car {
	return Car{
		Mark: "BNSF", Number: "471203", Kind: "boxcar", LengthFt: 60,
		TareTons: 30.5, GrossTons: 110, Axles: 4, Destination: "ALB",
		Restriction: CutFree,
	}
}

func TestCarValidateAcceptsGoodCar(t *testing.T) {
	if err := goodCar().Validate(); err != nil {
		t.Fatalf("Validate: %v", err)
	}
}

func TestCarNormalizeAppliesDefaults(t *testing.T) {
	c := Car{Mark: " bnsf ", Number: " 12 ", Kind: " BoxCar ", Destination: " alb ", HazmatClass: " 2.3 "}
	c.Normalize()
	if c.Mark != "BNSF" || c.Number != "12" || c.Kind != "boxcar" || c.Destination != "ALB" {
		t.Fatalf("normalize left %+v", c)
	}
	if c.HazmatClass != "2.3" {
		t.Fatalf("hazmat class %q", c.HazmatClass)
	}
	if c.Restriction != CutFree {
		t.Fatalf("restriction default %q", c.Restriction)
	}
}

func TestCarValidateRejectsBadData(t *testing.T) {
	cases := []struct {
		name string
		edit func(*Car)
		want string
	}{
		{"mark with digits", func(c *Car) { c.Mark = "BN5F" }, "letters only"},
		{"number with letters", func(c *Car) { c.Number = "12A" }, "digits only"},
		{"zero length", func(c *Car) { c.LengthFt = 0 }, "length_ft"},
		{"gross below tare", func(c *Car) { c.GrossTons = 10 }, "below tare_tons"},
		{"odd axles", func(c *Car) { c.Axles = 5 }, "axles"},
		{"missing destination", func(c *Car) { c.Destination = "" }, "destination"},
		{"placard without class", func(c *Car) { c.Placard = true }, "placard"},
		{"unknown restriction", func(c *Car) { c.Restriction = "maybe" }, "restriction"},
		{"drawbar missing mate", func(c *Car) { c.Restriction = CutNoUncouple }, "drawbar_mate"},
		{"bad order without reason", func(c *Car) { c.BadOrder = true }, "bad_order_why"},
		{"roller and rider", func(c *Car) { c.EasyRoller = true; c.RoughRider = true }, "easy_roller"},
	}
	for _, tc := range cases {
		c := goodCar()
		tc.edit(&c)
		err := c.Validate()
		if err == nil {
			t.Fatalf("%s: expected an error", tc.name)
		}
		if !strings.Contains(err.Error(), tc.want) {
			t.Fatalf("%s: error %v should mention %q", tc.name, err, tc.want)
		}
	}
}

func TestCarDerivedFigures(t *testing.T) {
	c := goodCar()
	if got := c.LadingTons(); got != 79.5 {
		t.Fatalf("lading %v", got)
	}
	if !c.Loaded() {
		t.Fatal("car should count as loaded")
	}
	c.GrossTons = c.TareTons
	if c.Loaded() {
		t.Fatal("empty car should not count as loaded")
	}
	c.Restriction = CutNoHump
	if !c.Flat() {
		t.Fatal("no-hump car must be flat switched")
	}
}

func TestTrackAcceptsHonoursRestrictions(t *testing.T) {
	track := ClassTrack{ID: "C07", Block: "LOCAL", CapacityFt: 1500, WeightLimitTons: 3000,
		Restrictions: []string{ResNoHazmat, ResNoLongCar}}
	hazmat := goodCar()
	hazmat.HazmatClass = "3"
	if ok, why := track.Accepts(hazmat, 89); ok || !strings.Contains(why, "hazmat") {
		t.Fatalf("expected hazmat refusal, got %v %q", ok, why)
	}
	long := goodCar()
	long.LengthFt = 98
	if ok, why := track.Accepts(long, 89); ok || !strings.Contains(why, "longer") {
		t.Fatalf("expected long car refusal, got %v %q", ok, why)
	}
	if ok, _ := track.Accepts(goodCar(), 89); !ok {
		t.Fatal("plain car should be accepted")
	}
}

func TestInboundTrainValidateChecksDrawbarSymmetry(t *testing.T) {
	a := goodCar()
	a.Restriction = CutNoUncouple
	a.DrawbarMate = "UP 100001"
	b := goodCar()
	b.Mark = "UP"
	b.Number = "100001"
	b.Restriction = CutNoUncouple
	b.DrawbarMate = "BNSF 471203"
	train := InboundTrain{ID: "T1", ReceivingID: "R01", Cars: []Car{a, b}}
	if err := train.Validate(); err != nil {
		t.Fatalf("symmetric pair should validate: %v", err)
	}
	train.Cars[1].DrawbarMate = "CSXT 999999"
	if err := train.Validate(); err == nil {
		t.Fatal("expected an error for a dangling drawbar mate")
	}
}

func TestYardOrderValidateRejectsDuplicateCars(t *testing.T) {
	car := goodCar()
	order := YardOrder{
		OrderID: "O1", YardID: "Y1",
		Trains: []InboundTrain{
			{ID: "T1", ReceivingID: "R01", Cars: []Car{car}},
			{ID: "T2", ReceivingID: "R02", Cars: []Car{car}},
		},
	}
	err := order.Validate()
	if err == nil || !strings.Contains(err.Error(), "appears in trains") {
		t.Fatalf("expected a duplicate car error, got %v", err)
	}
}

func TestSortInboundTrainsIsDeterministic(t *testing.T) {
	trains := []InboundTrain{
		{ID: "T3", ArrivalMinute: 100},
		{ID: "T1", ArrivalMinute: 100},
		{ID: "T2", ArrivalMinute: 50},
	}
	SortInboundTrains(trains)
	got := []string{trains[0].ID, trains[1].ID, trains[2].ID}
	want := []string{"T2", "T1", "T3"}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("got %v want %v", got, want)
		}
	}
}

func TestCarIndexRejectsDuplicates(t *testing.T) {
	c := goodCar()
	if _, err := NewCarIndex([]Car{c, c}); err == nil {
		t.Fatal("expected a duplicate error")
	}
	idx, err := NewCarIndex([]Car{c})
	if err != nil {
		t.Fatalf("NewCarIndex: %v", err)
	}
	if ids := idx.IDs(); len(ids) != 1 || ids[0] != "BNSF 471203" {
		t.Fatalf("unexpected ids %v", ids)
	}
}

func TestTotals(t *testing.T) {
	cars := []Car{goodCar(), goodCar()}
	cars[1].Number = "471204"
	if got := TotalLengthFt(cars); got != 120 {
		t.Fatalf("length %d", got)
	}
	if got := TotalTons(cars); got != 220 {
		t.Fatalf("tons %v", got)
	}
	if got := TotalAxles(cars); got != 8 {
		t.Fatalf("axles %d", got)
	}
}

func TestShiftEndMinuteAndCrewQualification(t *testing.T) {
	s := Shift{ID: "SA", Name: "First", StartMinute: 0, DurationMinutes: 480}
	if s.EndMinute() != 480 {
		t.Fatalf("end minute %d", s.EndMinute())
	}
	crew := Crew{ID: "AH1", Name: "Hump", Qualifications: []string{QualHump, QualRider}, MaxDutyMinutes: 600, HomeShift: "SA"}
	if err := crew.Validate(); err != nil {
		t.Fatalf("Validate: %v", err)
	}
	if !crew.Qualified(QualRider) || crew.Qualified(QualRoadTrain) {
		t.Fatal("qualification lookup is wrong")
	}
}
