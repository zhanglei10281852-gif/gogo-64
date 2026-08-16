package config

import (
	"fmt"
	"sort"
	"strings"

	"HumpYard/internal/model"
)

// Finding is one validation problem or advisory produced while checking a
// configuration document.
type Finding struct {
	Severity string `json:"severity"`
	Scope    string `json:"scope"`
	Subject  string `json:"subject"`
	Message  string `json:"message"`
}

// Severity values used by Finding.
const (
	SeverityError = "error"
	SeverityWarn  = "warning"
)

// Report is the ordered result of validating a configuration.
type Report struct {
	Findings []Finding `json:"findings"`
}

// Errors returns only the error-severity findings.
func (r Report) Errors() []Finding {
	var out []Finding
	for _, f := range r.Findings {
		if f.Severity == SeverityError {
			out = append(out, f)
		}
	}
	return out
}

// OK reports whether the configuration has no errors.
func (r Report) OK() bool {
	return len(r.Errors()) == 0
}

// Err collapses the error findings into a single error value.
func (r Report) Err() error {
	errs := r.Errors()
	if len(errs) == 0 {
		return nil
	}
	parts := make([]string, 0, len(errs))
	for _, f := range errs {
		parts = append(parts, fmt.Sprintf("%s %s: %s", f.Scope, f.Subject, f.Message))
	}
	return fmt.Errorf("configuration invalid: %s", strings.Join(parts, "; "))
}

// collector accumulates findings and sorts them deterministically.
type collector struct {
	findings []Finding
}

// errf records an error finding.
func (c *collector) errf(scope, subject, format string, args ...any) {
	c.findings = append(c.findings, Finding{
		Severity: SeverityError,
		Scope:    scope,
		Subject:  subject,
		Message:  fmt.Sprintf(format, args...),
	})
}

// warnf records a warning finding.
func (c *collector) warnf(scope, subject, format string, args ...any) {
	c.findings = append(c.findings, Finding{
		Severity: SeverityWarn,
		Scope:    scope,
		Subject:  subject,
		Message:  fmt.Sprintf(format, args...),
	})
}

// report finalizes the collected findings into a stable order.
func (c *collector) report() Report {
	findings := append([]Finding(nil), c.findings...)
	sort.SliceStable(findings, func(i, j int) bool {
		a, b := findings[i], findings[j]
		if a.Severity != b.Severity {
			return a.Severity < b.Severity
		}
		if a.Scope != b.Scope {
			return a.Scope < b.Scope
		}
		if a.Subject != b.Subject {
			return a.Subject < b.Subject
		}
		return a.Message < b.Message
	})
	return Report{Findings: findings}
}

// Validate performs full semantic validation of a normalized configuration.
func Validate(c *Config) Report {
	var col collector
	validateVersionAndYard(c, &col)
	validateBlocks(c, &col)
	validateClassTracks(c, &col)
	validateSupportTracks(c, &col)
	validatePower(c, &col)
	validateCrewsAndShifts(c, &col)
	validateHazmatRules(c, &col)
	validateHumpRules(c, &col)
	validateDepartureOrders(c, &col)
	return col.report()
}

// validateVersionAndYard checks the document envelope.
func validateVersionAndYard(c *Config, col *collector) {
	if c.Version != 1 {
		col.errf("config", "version", "unsupported version %d, expected 1", c.Version)
	}
	if c.Yard.ID == "" {
		col.errf("yard", "id", "yard id is required")
	}
	if c.Yard.Name == "" {
		col.errf("yard", c.Yard.ID, "yard name is required")
	}
	if c.Yard.CrestGradePct < 0.5 || c.Yard.CrestGradePct > 6 {
		col.errf("yard", c.Yard.ID, "crest_grade_pct %.2f out of range 0.5..6", c.Yard.CrestGradePct)
	}
	if c.Yard.LongCarFt < 40 || c.Yard.LongCarFt > 120 {
		col.errf("yard", c.Yard.ID, "long_car_ft %d out of range 40..120", c.Yard.LongCarFt)
	}
	if c.Yard.CouplerSlackFt < 0 || c.Yard.CouplerSlackFt > 5 {
		col.errf("yard", c.Yard.ID, "coupler_slack_ft %d out of range 0..5", c.Yard.CouplerSlackFt)
	}
	if c.Yard.OverflowTrack == "" {
		col.errf("yard", c.Yard.ID, "overflow_track is required")
	} else if _, ok := c.ClassTrackByID(c.Yard.OverflowTrack); !ok {
		col.errf("yard", c.Yard.ID, "overflow_track %q is not a classification track", c.Yard.OverflowTrack)
	}
	if c.Yard.RepairTrack == "" {
		col.errf("yard", c.Yard.ID, "repair_track is required")
	} else if _, ok := c.ClassTrackByID(c.Yard.RepairTrack); !ok {
		col.errf("yard", c.Yard.ID, "repair_track %q is not a classification track", c.Yard.RepairTrack)
	}
	if c.Yard.RepairTrack != "" && c.Yard.RepairTrack == c.Yard.OverflowTrack {
		col.errf("yard", c.Yard.ID, "repair_track and overflow_track must differ")
	}
}

// validateBlocks checks block definitions and destination uniqueness.
func validateBlocks(c *Config, col *collector) {
	if len(c.Blocks) == 0 {
		col.errf("blocks", "-", "at least one block is required")
		return
	}
	seenID := map[string]bool{}
	seenPriority := map[int]string{}
	destOwner := map[string]string{}
	for _, b := range c.Blocks {
		if err := b.Validate(); err != nil {
			col.errf("block", b.ID, "%v", err)
			continue
		}
		if seenID[b.ID] {
			col.errf("block", b.ID, "duplicate block id")
			continue
		}
		seenID[b.ID] = true
		if other, dup := seenPriority[b.Priority]; dup {
			col.errf("block", b.ID, "priority %d already used by block %q", b.Priority, other)
		} else {
			seenPriority[b.Priority] = b.ID
		}
		for _, d := range b.Destinations {
			if other, dup := destOwner[d]; dup {
				col.warnf("block", b.ID, "destination %q also listed by block %q; lowest priority wins", d, other)
				continue
			}
			destOwner[d] = b.ID
		}
		if len(c.TracksForBlock(b.ID)) == 0 {
			col.errf("block", b.ID, "no classification track assigned to block")
		}
	}
}

// validateClassTracks checks classification tracks and their block links.
func validateClassTracks(c *Config, col *collector) {
	if len(c.Class) == 0 {
		col.errf("class_tracks", "-", "at least one classification track is required")
		return
	}
	seen := map[string]bool{}
	for _, t := range c.Class {
		if err := t.Validate(); err != nil {
			col.errf("class_track", t.ID, "%v", err)
			continue
		}
		if seen[t.ID] {
			col.errf("class_track", t.ID, "duplicate track id")
			continue
		}
		seen[t.ID] = true
		if _, ok := c.BlockByID(t.Block); !ok {
			col.errf("class_track", t.ID, "block %q is not defined", t.Block)
		}
		if t.GradePct < c.Hump.MinBowlGradePct && !t.HasRestriction(model.ResFlatOnly) {
			col.warnf("class_track", t.ID, "grade_pct %.2f is below min_bowl_grade_pct %.2f; cars may stall", t.GradePct, c.Hump.MinBowlGradePct)
		}
		if t.CabooseSpot && t.HasRestriction(model.ResNoHazmat) {
			col.warnf("class_track", t.ID, "caboose spot on a no-hazmat track is redundant")
		}
	}
}

// validateSupportTracks checks receiving and departure tracks.
func validateSupportTracks(c *Config, col *collector) {
	if len(c.Receiving) == 0 {
		col.errf("receiving_tracks", "-", "at least one receiving track is required")
	}
	if len(c.Departure) == 0 {
		col.errf("departure_tracks", "-", "at least one departure track is required")
	}
	seen := map[string]bool{}
	for _, t := range c.Receiving {
		if err := t.Validate(model.RoleReceiving); err != nil {
			col.errf("receiving_track", t.ID, "%v", err)
			continue
		}
		if seen[t.ID] {
			col.errf("receiving_track", t.ID, "duplicate track id")
		}
		seen[t.ID] = true
	}
	for _, t := range c.Departure {
		if err := t.Validate(model.RoleDeparture); err != nil {
			col.errf("departure_track", t.ID, "%v", err)
			continue
		}
		if seen[t.ID] {
			col.errf("departure_track", t.ID, "track id already used")
		}
		seen[t.ID] = true
	}
	for _, ct := range c.Class {
		if seen[ct.ID] {
			col.errf("class_track", ct.ID, "identifier collides with a support track")
		}
	}
}

// validatePower checks locomotive definitions.
func validatePower(c *Config, col *collector) {
	if len(c.Power) == 0 {
		col.errf("locomotives", "-", "at least one locomotive is required")
		return
	}
	seen := map[string]bool{}
	for _, l := range c.Power {
		if err := l.Validate(); err != nil {
			col.errf("locomotive", l.ID, "%v", err)
			continue
		}
		if seen[l.ID] {
			col.errf("locomotive", l.ID, "duplicate locomotive id")
		}
		seen[l.ID] = true
	}
}

// validateCrewsAndShifts checks crews, shifts and their cross references.
func validateCrewsAndShifts(c *Config, col *collector) {
	if len(c.Shifts) == 0 {
		col.errf("shifts", "-", "at least one shift is required")
	}
	seenShift := map[string]bool{}
	for _, s := range c.Shifts {
		if err := s.Validate(); err != nil {
			col.errf("shift", s.ID, "%v", err)
			continue
		}
		if seenShift[s.ID] {
			col.errf("shift", s.ID, "duplicate shift id")
		}
		seenShift[s.ID] = true
	}
	for i := 1; i < len(c.Shifts); i++ {
		prev, cur := c.Shifts[i-1], c.Shifts[i]
		if cur.StartMinute < prev.EndMinute() {
			col.errf("shift", cur.ID, "starts at minute %d before shift %q ends at %d", cur.StartMinute, prev.ID, prev.EndMinute())
		}
	}
	if len(c.Crews) == 0 {
		col.errf("crews", "-", "at least one crew is required")
	}
	seenCrew := map[string]bool{}
	humpCrews := 0
	for _, cr := range c.Crews {
		if err := cr.Validate(); err != nil {
			col.errf("crew", cr.ID, "%v", err)
			continue
		}
		if seenCrew[cr.ID] {
			col.errf("crew", cr.ID, "duplicate crew id")
		}
		seenCrew[cr.ID] = true
		if !seenShift[cr.HomeShift] {
			col.errf("crew", cr.ID, "home_shift %q is not defined", cr.HomeShift)
		}
		if cr.Qualified(model.QualHump) {
			humpCrews++
		}
		if shift, ok := c.ShiftByID(cr.HomeShift); ok && cr.MaxDutyMinutes < shift.DurationMinutes {
			col.warnf("crew", cr.ID, "max_duty_minutes %d is shorter than shift %q duration %d", cr.MaxDutyMinutes, shift.ID, shift.DurationMinutes)
		}
	}
	if humpCrews == 0 {
		col.errf("crews", "-", "at least one crew must hold the %q qualification", model.QualHump)
	}
}

// validateHazmatRules checks the hazmat regime for internal consistency.
func validateHazmatRules(c *Config, col *collector) {
	h := c.Hazmat
	if len(h.Classes) == 0 {
		col.errf("hazmat", "classes", "at least one hazmat class must be declared")
	}
	known := map[string]bool{}
	for _, cl := range h.Classes {
		if cl == "" {
			col.errf("hazmat", "classes", "empty hazmat class")
			continue
		}
		if known[cl] {
			col.errf("hazmat", "classes", "duplicate hazmat class %q", cl)
		}
		known[cl] = true
	}
	seenPair := map[string]bool{}
	for _, p := range h.IncompatiblePairs {
		if p.ClassA == p.ClassB {
			col.errf("hazmat", p.Key(), "a class cannot be incompatible with itself")
			continue
		}
		if !known[p.ClassA] {
			col.errf("hazmat", p.Key(), "class %q is not declared", p.ClassA)
		}
		if !known[p.ClassB] {
			col.errf("hazmat", p.Key(), "class %q is not declared", p.ClassB)
		}
		if p.BufferCars < 1 || p.BufferCars > 20 {
			col.errf("hazmat", p.Key(), "buffer_cars %d out of range 1..20", p.BufferCars)
		}
		if seenPair[p.Key()] {
			col.errf("hazmat", p.Key(), "duplicate incompatible pair")
		}
		seenPair[p.Key()] = true
	}
	for _, cl := range h.CabooseProhibited {
		if !known[cl] {
			col.errf("hazmat", "caboose_prohibited_classes", "class %q is not declared", cl)
		}
	}
	for _, cl := range h.FlatSwitchOnly {
		if !known[cl] {
			col.errf("hazmat", "flat_switch_only_classes", "class %q is not declared", cl)
		}
	}
	if h.MaxPlacardsPerTrack < 1 || h.MaxPlacardsPerTrack > 200 {
		col.errf("hazmat", "max_placards_per_track", "value %d out of range 1..200", h.MaxPlacardsPerTrack)
	}
	if h.CabooseBufferCars < 0 || h.CabooseBufferCars > 20 {
		col.errf("hazmat", "caboose_buffer_cars", "value %d out of range 0..20", h.CabooseBufferCars)
	}
}

// validateHumpRules checks crest sequencing parameters.
func validateHumpRules(c *Config, col *collector) {
	h := c.Hump
	if h.MaxCutCars < 1 || h.MaxCutCars > 100 {
		col.errf("hump", "max_cut_cars", "value %d out of range 1..100", h.MaxCutCars)
	}
	if h.MaxCarLengthFt < 40 || h.MaxCarLengthFt > 200 {
		col.errf("hump", "max_car_length_ft", "value %d out of range 40..200", h.MaxCarLengthFt)
	}
	if h.MaxCarLengthFt < c.Yard.LongCarFt {
		col.errf("hump", "max_car_length_ft", "value %d is below yard long_car_ft %d", h.MaxCarLengthFt, c.Yard.LongCarFt)
	}
	if h.RetarderMaxTons <= 0 {
		col.errf("hump", "retarder_max_tons", "value must be positive")
	}
	if h.MinBowlGradePct < 0 || h.MinBowlGradePct > 5 {
		col.errf("hump", "min_bowl_grade_pct", "value %.2f out of range 0..5", h.MinBowlGradePct)
	}
	if h.CutChangeMinutes < 0 || h.CutChangeMinutes > 60 {
		col.errf("hump", "cut_change_minutes", "value %d out of range 0..60", h.CutChangeMinutes)
	}
	if h.CarsPerMinute <= 0 || h.CarsPerMinute > 20 {
		col.errf("hump", "cars_per_minute", "value %.2f out of range 0..20", h.CarsPerMinute)
	}
	if h.FlatMinutesPerCar < 1 || h.FlatMinutesPerCar > 120 {
		col.errf("hump", "flat_minutes_per_car", "value %d out of range 1..120", h.FlatMinutesPerCar)
	}
}

// validateDepartureOrders checks the outbound program.
func validateDepartureOrders(c *Config, col *collector) {
	if len(c.Departures) == 0 {
		col.errf("departure_orders", "-", "at least one departure order is required")
		return
	}
	seen := map[string]bool{}
	trackUse := map[string]string{}
	for _, d := range c.Departures {
		if d.TrainID == "" {
			col.errf("departure_order", "-", "train_id is required")
			continue
		}
		if seen[d.TrainID] {
			col.errf("departure_order", d.TrainID, "duplicate train_id")
			continue
		}
		seen[d.TrainID] = true
		if _, ok := c.DepartureByID(d.TrackID); !ok {
			col.errf("departure_order", d.TrainID, "departure_track %q is not defined", d.TrackID)
		} else if other, used := trackUse[d.TrackID]; used {
			col.warnf("departure_order", d.TrainID, "departure_track %q also used by train %q", d.TrackID, other)
		} else {
			trackUse[d.TrackID] = d.TrainID
		}
		if len(d.BlockOrder) == 0 {
			col.errf("departure_order", d.TrainID, "block_order must list at least one block")
		}
		seenBlock := map[string]bool{}
		for _, b := range d.BlockOrder {
			if _, ok := c.BlockByID(b); !ok {
				col.errf("departure_order", d.TrainID, "block %q is not defined", b)
			}
			if seenBlock[b] {
				col.errf("departure_order", d.TrainID, "duplicate block %q in block_order", b)
			}
			seenBlock[b] = true
		}
		if d.MaxLengthFt < 100 || d.MaxLengthFt > 30000 {
			col.errf("departure_order", d.TrainID, "max_length_ft %d out of range 100..30000", d.MaxLengthFt)
		}
		if d.MaxTons <= 0 {
			col.errf("departure_order", d.TrainID, "max_tons must be positive")
		}
		if d.MaxAxles < 4 || d.MaxAxles > 2000 {
			col.errf("departure_order", d.TrainID, "max_axles %d out of range 4..2000", d.MaxAxles)
		}
		if d.DepartMin < 0 || d.DepartMin > 2879 {
			col.errf("departure_order", d.TrainID, "depart_minute %d out of range 0..2879", d.DepartMin)
		}
		if d.MinCars < 0 || d.MinCars > 500 {
			col.errf("departure_order", d.TrainID, "min_cars %d out of range 0..500", d.MinCars)
		}
		validateDeparturePower(c, col, d)
		if track, ok := c.DepartureByID(d.TrackID); ok && d.MaxLengthFt > track.CapacityFt {
			col.warnf("departure_order", d.TrainID, "max_length_ft %d exceeds track %q capacity %d ft", d.MaxLengthFt, track.ID, track.CapacityFt)
		}
	}
}

// validateDeparturePower checks the assigned power for a departure order.
func validateDeparturePower(c *Config, col *collector, d DepartureOrder) {
	if len(d.Locomotives) == 0 {
		col.errf("departure_order", d.TrainID, "at least one locomotive must be assigned")
		return
	}
	seen := map[string]bool{}
	rated := 0.0
	for _, id := range d.Locomotives {
		unit, ok := c.LocomotiveByID(id)
		if !ok {
			col.errf("departure_order", d.TrainID, "locomotive %q is not defined", id)
			continue
		}
		if seen[id] {
			col.errf("departure_order", d.TrainID, "duplicate locomotive %q", id)
			continue
		}
		seen[id] = true
		if unit.YardOnly {
			col.errf("departure_order", d.TrainID, "locomotive %q is restricted to yard service", id)
		}
		rated += unit.RatedTons
	}
	if rated > 0 && rated < d.MaxTons {
		col.warnf("departure_order", d.TrainID, "assigned power rated %.0f tons is below max_tons %.0f", rated, d.MaxTons)
	}
}

// DestinationCatalog lists every destination the configuration can block, in
// lexical order.
func (c *Config) DestinationCatalog() []string {
	seen := map[string]bool{}
	for _, b := range c.Blocks {
		for _, d := range b.Destinations {
			seen[d] = true
		}
	}
	out := make([]string, 0, len(seen))
	for d := range seen {
		out = append(out, d)
	}
	sort.Strings(out)
	return out
}
