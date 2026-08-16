package model

import (
	"fmt"
	"sort"
	"strings"
)

// InboundTrain is one arrival standing on a receiving track. Cars are listed in
// standing order from the leading end (nearest the hump lead) to the rear.
type InboundTrain struct {
	ID            string `json:"id"`
	ArrivalMinute int    `json:"arrival_minute"`
	ReceivingID   string `json:"receiving_track"`
	Inspected     bool   `json:"inspected"`
	CabooseSpot   int    `json:"caboose_position"`
	Cars          []Car  `json:"cars"`
}

// Normalize canonicalizes the train and every car it holds.
func (t *InboundTrain) Normalize() {
	t.ID = strings.ToUpper(strings.TrimSpace(t.ID))
	t.ReceivingID = strings.ToUpper(strings.TrimSpace(t.ReceivingID))
	for i := range t.Cars {
		t.Cars[i].Normalize()
	}
}

// Validate checks the train envelope and each car.
func (t InboundTrain) Validate() error {
	if t.ID == "" {
		return fmt.Errorf("train id is required")
	}
	if t.ArrivalMinute < 0 || t.ArrivalMinute > 2879 {
		return fmt.Errorf("train %q: arrival_minute %d out of range 0..2879", t.ID, t.ArrivalMinute)
	}
	if t.ReceivingID == "" {
		return fmt.Errorf("train %q: receiving_track is required", t.ID)
	}
	if len(t.Cars) == 0 {
		return fmt.Errorf("train %q: at least one car is required", t.ID)
	}
	if t.CabooseSpot < 0 || t.CabooseSpot > len(t.Cars) {
		return fmt.Errorf("train %q: caboose_position %d out of range 0..%d", t.ID, t.CabooseSpot, len(t.Cars))
	}
	seen := map[string]bool{}
	for i, c := range t.Cars {
		if err := c.Validate(); err != nil {
			return fmt.Errorf("train %q position %d: %w", t.ID, i+1, err)
		}
		if seen[c.ID()] {
			return fmt.Errorf("train %q: duplicate car %q", t.ID, c.ID())
		}
		seen[c.ID()] = true
	}
	if err := t.validateDrawbars(seen); err != nil {
		return err
	}
	return nil
}

// validateDrawbars checks that every drawbar mate exists in the same train and
// that the pairing is symmetric.
func (t InboundTrain) validateDrawbars(present map[string]bool) error {
	mates := map[string]string{}
	for _, c := range t.Cars {
		if c.DrawbarMate == "" {
			continue
		}
		if c.DrawbarMate == c.ID() {
			return fmt.Errorf("train %q: car %q lists itself as drawbar_mate", t.ID, c.ID())
		}
		if !present[c.DrawbarMate] {
			return fmt.Errorf("train %q: car %q drawbar_mate %q not in train", t.ID, c.ID(), c.DrawbarMate)
		}
		mates[c.ID()] = c.DrawbarMate
	}
	for id, mate := range mates {
		if mates[mate] != id {
			return fmt.Errorf("train %q: drawbar pairing between %q and %q is not symmetric", t.ID, id, mate)
		}
	}
	return nil
}

// CarCount returns the number of cars in the train.
func (t InboundTrain) CarCount() int {
	return len(t.Cars)
}

// CabooseAtRear reports whether an occupied caboose stands coupled to the
// trailing end of the arrival.
func (t InboundTrain) CabooseAtRear() bool {
	return t.CabooseSpot > 0 && t.CabooseSpot == len(t.Cars)
}

// CrewAtHead reports whether an occupied crew position stands at the leading
// end of the arrival.
func (t InboundTrain) CrewAtHead() bool {
	return t.CabooseSpot == 1
}

// YardOrder is the full set of arrivals presented to the planner.
type YardOrder struct {
	OrderID string         `json:"order_id"`
	YardID  string         `json:"yard_id"`
	Trains  []InboundTrain `json:"trains"`
}

// Normalize canonicalizes the order and sorts trains into planning order.
func (o *YardOrder) Normalize() {
	o.OrderID = strings.ToUpper(strings.TrimSpace(o.OrderID))
	o.YardID = strings.ToUpper(strings.TrimSpace(o.YardID))
	for i := range o.Trains {
		o.Trains[i].Normalize()
	}
	SortInboundTrains(o.Trains)
}

// Validate checks the order envelope and each train.
func (o YardOrder) Validate() error {
	if o.OrderID == "" {
		return fmt.Errorf("order_id is required")
	}
	if o.YardID == "" {
		return fmt.Errorf("yard_id is required")
	}
	if len(o.Trains) == 0 {
		return fmt.Errorf("at least one inbound train is required")
	}
	seenTrain := map[string]bool{}
	seenCar := map[string]string{}
	for _, t := range o.Trains {
		if err := t.Validate(); err != nil {
			return err
		}
		if seenTrain[t.ID] {
			return fmt.Errorf("duplicate train %q", t.ID)
		}
		seenTrain[t.ID] = true
		for _, c := range t.Cars {
			if other, dup := seenCar[c.ID()]; dup {
				return fmt.Errorf("car %q appears in trains %q and %q", c.ID(), other, t.ID)
			}
			seenCar[c.ID()] = t.ID
		}
	}
	return nil
}

// AllCars returns every car in the order, ordered by train then standing
// position.
func (o YardOrder) AllCars() []Car {
	var out []Car
	for _, t := range o.Trains {
		out = append(out, t.Cars...)
	}
	return out
}

// CarCount returns the total number of inbound cars.
func (o YardOrder) CarCount() int {
	total := 0
	for _, t := range o.Trains {
		total += len(t.Cars)
	}
	return total
}

// TrainOf returns the train identifier holding the given car.
func (o YardOrder) TrainOf(carID string) string {
	for _, t := range o.Trains {
		for _, c := range t.Cars {
			if c.ID() == carID {
				return t.ID
			}
		}
	}
	return ""
}

// SortInboundTrains orders trains by arrival minute then identifier so the
// planning order never depends on input order.
func SortInboundTrains(trains []InboundTrain) {
	sort.SliceStable(trains, func(i, j int) bool {
		if trains[i].ArrivalMinute != trains[j].ArrivalMinute {
			return trains[i].ArrivalMinute < trains[j].ArrivalMinute
		}
		return trains[i].ID < trains[j].ID
	})
}
