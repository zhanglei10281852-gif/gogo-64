// Package config defines the HumpYard configuration document and its strict
// loader. A configuration describes one physical yard: its blocks, tracks,
// power, crews, shifts, hazmat rules, hump rules and the outbound trains that
// must be built.
package config

import (
	"sort"
	"strings"

	"HumpYard/internal/model"
)

// Config is the whole configuration document.
type Config struct {
	Version    int                  `json:"version"`
	Yard       Yard                 `json:"yard"`
	Blocks     []model.Block        `json:"blocks"`
	Class      []model.ClassTrack   `json:"classification_tracks"`
	Receiving  []model.SupportTrack `json:"receiving_tracks"`
	Departure  []model.SupportTrack `json:"departure_tracks"`
	Power      []model.Locomotive   `json:"locomotives"`
	Crews      []model.Crew         `json:"crews"`
	Shifts     []model.Shift        `json:"shifts"`
	Hazmat     HazmatRules          `json:"hazmat_rules"`
	Hump       HumpRules            `json:"hump_rules"`
	Departures []DepartureOrder     `json:"departure_orders"`
}

// Yard holds yard-wide physical parameters.
type Yard struct {
	ID             string  `json:"id"`
	Name           string  `json:"name"`
	CrestGradePct  float64 `json:"crest_grade_pct"`
	LongCarFt      int     `json:"long_car_ft"`
	OverflowTrack  string  `json:"overflow_track"`
	RepairTrack    string  `json:"repair_track"`
	CouplerSlackFt int     `json:"coupler_slack_ft"`
}

// HazmatPair is a pair of incompatible hazmat classes together with the number
// of buffer cars that must separate them on a classification track.
type HazmatPair struct {
	ClassA     string `json:"class_a"`
	ClassB     string `json:"class_b"`
	BufferCars int    `json:"buffer_cars"`
}

// Key returns the canonical ordered pair key used for lookups.
func (p HazmatPair) Key() string {
	a, b := p.ClassA, p.ClassB
	if a > b {
		a, b = b, a
	}
	return a + "|" + b
}

// HazmatRules describes the separation regime enforced on classification and
// departure tracks.
type HazmatRules struct {
	Classes             []string     `json:"classes"`
	IncompatiblePairs   []HazmatPair `json:"incompatible_pairs"`
	CabooseProhibited   []string     `json:"caboose_prohibited_classes"`
	FlatSwitchOnly      []string     `json:"flat_switch_only_classes"`
	MaxPlacardsPerTrack int          `json:"max_placards_per_track"`
	CabooseBufferCars   int          `json:"caboose_buffer_cars"`
}

// HumpRules describes the constraints applied when sequencing the crest.
type HumpRules struct {
	MaxCutCars         int     `json:"max_cut_cars"`
	MaxCarLengthFt     int     `json:"max_car_length_ft"`
	RetarderMaxTons    float64 `json:"retarder_max_tons"`
	RiderRequiredRough bool    `json:"rider_required_for_rough_rider"`
	MinBowlGradePct    float64 `json:"min_bowl_grade_pct"`
	CutChangeMinutes   int     `json:"cut_change_minutes"`
	CarsPerMinute      float64 `json:"cars_per_minute"`
	FlatMinutesPerCar  int     `json:"flat_minutes_per_car"`
}

// DepartureOrder is one outbound train the yard must assemble.
type DepartureOrder struct {
	TrainID     string   `json:"train_id"`
	TrackID     string   `json:"departure_track"`
	BlockOrder  []string `json:"block_order"`
	MaxLengthFt int      `json:"max_length_ft"`
	MaxTons     float64  `json:"max_tons"`
	MaxAxles    int      `json:"max_axles"`
	Locomotives []string `json:"locomotives"`
	DepartMin   int      `json:"depart_minute"`
	MinCars     int      `json:"min_cars"`
}

// Normalize canonicalizes the departure order.
func (d *DepartureOrder) Normalize() {
	d.TrainID = strings.ToUpper(strings.TrimSpace(d.TrainID))
	d.TrackID = strings.ToUpper(strings.TrimSpace(d.TrackID))
	for i := range d.BlockOrder {
		d.BlockOrder[i] = strings.ToUpper(strings.TrimSpace(d.BlockOrder[i]))
	}
	for i := range d.Locomotives {
		d.Locomotives[i] = strings.ToUpper(strings.TrimSpace(d.Locomotives[i]))
	}
	sort.Strings(d.Locomotives)
}

// Normalize canonicalizes the whole document and sorts every collection into
// deterministic order.
func (c *Config) Normalize() {
	c.Yard.ID = strings.ToUpper(strings.TrimSpace(c.Yard.ID))
	c.Yard.Name = strings.TrimSpace(c.Yard.Name)
	c.Yard.OverflowTrack = strings.ToUpper(strings.TrimSpace(c.Yard.OverflowTrack))
	c.Yard.RepairTrack = strings.ToUpper(strings.TrimSpace(c.Yard.RepairTrack))
	for i := range c.Blocks {
		c.Blocks[i].Normalize()
	}
	for i := range c.Class {
		c.Class[i].Normalize()
	}
	for i := range c.Receiving {
		c.Receiving[i].Normalize()
	}
	for i := range c.Departure {
		c.Departure[i].Normalize()
	}
	for i := range c.Power {
		c.Power[i].Normalize()
	}
	for i := range c.Crews {
		c.Crews[i].Normalize()
	}
	for i := range c.Shifts {
		c.Shifts[i].Normalize()
	}
	for i := range c.Departures {
		c.Departures[i].Normalize()
	}
	c.Hazmat.normalize()
	sort.SliceStable(c.Blocks, func(i, j int) bool {
		if c.Blocks[i].Priority != c.Blocks[j].Priority {
			return c.Blocks[i].Priority < c.Blocks[j].Priority
		}
		return c.Blocks[i].ID < c.Blocks[j].ID
	})
	model.SortClassTracks(c.Class)
	model.SortSupportTracks(c.Receiving)
	model.SortSupportTracks(c.Departure)
	model.SortLocomotives(c.Power)
	model.SortCrews(c.Crews)
	model.SortShifts(c.Shifts)
	sort.SliceStable(c.Departures, func(i, j int) bool {
		if c.Departures[i].DepartMin != c.Departures[j].DepartMin {
			return c.Departures[i].DepartMin < c.Departures[j].DepartMin
		}
		return c.Departures[i].TrainID < c.Departures[j].TrainID
	})
}

// normalize canonicalizes hazmat rule strings and orders pairs.
func (h *HazmatRules) normalize() {
	for i := range h.Classes {
		h.Classes[i] = strings.ToUpper(strings.TrimSpace(h.Classes[i]))
	}
	sort.Strings(h.Classes)
	for i := range h.CabooseProhibited {
		h.CabooseProhibited[i] = strings.ToUpper(strings.TrimSpace(h.CabooseProhibited[i]))
	}
	sort.Strings(h.CabooseProhibited)
	for i := range h.FlatSwitchOnly {
		h.FlatSwitchOnly[i] = strings.ToUpper(strings.TrimSpace(h.FlatSwitchOnly[i]))
	}
	sort.Strings(h.FlatSwitchOnly)
	for i := range h.IncompatiblePairs {
		p := &h.IncompatiblePairs[i]
		p.ClassA = strings.ToUpper(strings.TrimSpace(p.ClassA))
		p.ClassB = strings.ToUpper(strings.TrimSpace(p.ClassB))
		if p.ClassA > p.ClassB {
			p.ClassA, p.ClassB = p.ClassB, p.ClassA
		}
	}
	sort.SliceStable(h.IncompatiblePairs, func(i, j int) bool {
		return h.IncompatiblePairs[i].Key() < h.IncompatiblePairs[j].Key()
	})
}

// BlockByID returns the block with the given identifier.
func (c *Config) BlockByID(id string) (model.Block, bool) {
	for _, b := range c.Blocks {
		if b.ID == id {
			return b, true
		}
	}
	return model.Block{}, false
}

// BlockForDestination resolves a destination code to a block identifier. When
// several blocks list the destination the lowest priority number wins, then the
// lexically smallest block identifier.
func (c *Config) BlockForDestination(dest string) (string, bool) {
	best := ""
	bestPriority := 0
	for _, b := range c.Blocks {
		if !model.ContainsString(b.Destinations, dest) {
			continue
		}
		if best == "" || b.Priority < bestPriority || (b.Priority == bestPriority && b.ID < best) {
			best = b.ID
			bestPriority = b.Priority
		}
	}
	return best, best != ""
}

// TracksForBlock returns the classification tracks assigned to a block, in
// identifier order.
func (c *Config) TracksForBlock(blockID string) []model.ClassTrack {
	var out []model.ClassTrack
	for _, t := range c.Class {
		if t.Block == blockID {
			out = append(out, t)
		}
	}
	return out
}

// ClassTrackByID returns a classification track by identifier.
func (c *Config) ClassTrackByID(id string) (model.ClassTrack, bool) {
	for _, t := range c.Class {
		if t.ID == id {
			return t, true
		}
	}
	return model.ClassTrack{}, false
}

// LocomotiveByID returns a locomotive by identifier.
func (c *Config) LocomotiveByID(id string) (model.Locomotive, bool) {
	for _, l := range c.Power {
		if l.ID == id {
			return l, true
		}
	}
	return model.Locomotive{}, false
}

// CrewByID returns a crew by identifier.
func (c *Config) CrewByID(id string) (model.Crew, bool) {
	for _, cr := range c.Crews {
		if cr.ID == id {
			return cr, true
		}
	}
	return model.Crew{}, false
}

// ShiftByID returns a shift by identifier.
func (c *Config) ShiftByID(id string) (model.Shift, bool) {
	for _, s := range c.Shifts {
		if s.ID == id {
			return s, true
		}
	}
	return model.Shift{}, false
}

// ReceivingByID returns a receiving track by identifier.
func (c *Config) ReceivingByID(id string) (model.SupportTrack, bool) {
	for _, t := range c.Receiving {
		if t.ID == id {
			return t, true
		}
	}
	return model.SupportTrack{}, false
}

// DepartureByID returns a departure track by identifier.
func (c *Config) DepartureByID(id string) (model.SupportTrack, bool) {
	for _, t := range c.Departure {
		if t.ID == id {
			return t, true
		}
	}
	return model.SupportTrack{}, false
}

// BufferFor returns the required buffer-car count between two hazmat classes,
// and whether the pair is regulated at all.
func (h HazmatRules) BufferFor(a, b string) (int, bool) {
	if a == "" || b == "" {
		return 0, false
	}
	probe := HazmatPair{ClassA: a, ClassB: b}
	want := probe.Key()
	for _, p := range h.IncompatiblePairs {
		if p.Key() == want {
			return p.BufferCars, true
		}
	}
	return 0, false
}

// RequiresFlatSwitch reports whether a hazmat class may never be humped.
func (h HazmatRules) RequiresFlatSwitch(class string) bool {
	return model.ContainsString(h.FlatSwitchOnly, class)
}

// CabooseBarred reports whether a hazmat class may not stand next to an
// occupied caboose or crew position.
func (h HazmatRules) CabooseBarred(class string) bool {
	return model.ContainsString(h.CabooseProhibited, class)
}
