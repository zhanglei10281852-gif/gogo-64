// Package blocking maps inbound cars to blocks and blocks to classification
// tracks. The mapping honours track length and weight capacity, per-track
// physical restrictions and block priority, and it resolves ties in a fixed
// way so the same input always yields the same plan.
package blocking

import (
	"fmt"
	"sort"

	"HumpYard/internal/config"
	"HumpYard/internal/model"
)

// Assignment status values.
const (
	// StatusAssigned means the car has a classification track in its block.
	StatusAssigned = "assigned"
	// StatusOverflow means the block was full and the car went to the yard
	// overflow track.
	StatusOverflow = "overflow"
	// StatusRepair means the car is bad ordered and went to the repair track.
	StatusRepair = "repair"
	// StatusUnblocked means the destination maps to no block.
	StatusUnblocked = "unblocked"
	// StatusRejected means no track in the yard could hold the car.
	StatusRejected = "rejected"
)

// Assignment is the blocking decision for one car.
type Assignment struct {
	CarID       string `json:"car_id"`
	TrainID     string `json:"train_id"`
	Position    int    `json:"position"`
	Destination string `json:"destination"`
	Block       string `json:"block"`
	Priority    int    `json:"priority"`
	TrackID     string `json:"track_id"`
	Status      string `json:"status"`
	Reason      string `json:"reason"`
}

// TrackLoad is the projected occupancy of one classification track after
// blocking.
type TrackLoad struct {
	TrackID       string   `json:"track_id"`
	Block         string   `json:"block"`
	CapacityFt    int      `json:"capacity_ft"`
	UsedFt        int      `json:"used_ft"`
	RemainingFt   int      `json:"remaining_ft"`
	LimitTons     float64  `json:"limit_tons"`
	UsedTons      float64  `json:"used_tons"`
	RemainingTons float64  `json:"remaining_tons"`
	CarIDs        []string `json:"car_ids"`
}

// Plan is the full blocking result.
type Plan struct {
	Assignments []Assignment     `json:"assignments"`
	Tracks      []TrackLoad      `json:"tracks"`
	Unblocked   []string         `json:"unblocked_cars"`
	Overflow    []string         `json:"overflow_cars"`
	Rejected    []string         `json:"rejected_cars"`
	Findings    []config.Finding `json:"findings"`
}

// AssignmentFor returns the assignment for a car identifier.
func (p Plan) AssignmentFor(carID string) (Assignment, bool) {
	for _, a := range p.Assignments {
		if a.CarID == carID {
			return a, true
		}
	}
	return Assignment{}, false
}

// TrackFor returns the track assigned to a car, or the empty string.
func (p Plan) TrackFor(carID string) string {
	if a, ok := p.AssignmentFor(carID); ok {
		return a.TrackID
	}
	return ""
}

// BlockFor returns the block assigned to a car, or the empty string.
func (p Plan) BlockFor(carID string) string {
	if a, ok := p.AssignmentFor(carID); ok {
		return a.Block
	}
	return ""
}

// tracker holds the running occupancy of one classification track.
type tracker struct {
	track     model.ClassTrack
	usedFt    int
	usedTons  float64
	placards  int
	carIDs    []string
	slackFt   int
	longCarFt int
}

// fits reports whether a car still fits by length and weight.
func (t *tracker) fits(c model.Car) bool {
	return t.usedFt+c.LengthFt+t.slackFt <= t.track.CapacityFt &&
		t.usedTons+c.GrossTons <= t.track.WeightLimitTons
}

// remainingFt is the unoccupied length on the track.
func (t *tracker) remainingFt() int {
	return t.track.CapacityFt - t.usedFt
}

// remainingTons is the unused weight allowance on the track.
func (t *tracker) remainingTons() float64 {
	return t.track.WeightLimitTons - t.usedTons
}

// push records a car onto the track.
func (t *tracker) push(c model.Car) {
	t.usedFt += c.LengthFt + t.slackFt
	t.usedTons += c.GrossTons
	if c.Placard {
		t.placards++
	}
	t.carIDs = append(t.carIDs, c.ID())
}

// load renders the tracker as a reportable track load.
func (t *tracker) load() TrackLoad {
	ids := append([]string(nil), t.carIDs...)
	return TrackLoad{
		TrackID:       t.track.ID,
		Block:         t.track.Block,
		CapacityFt:    t.track.CapacityFt,
		UsedFt:        t.usedFt,
		RemainingFt:   t.remainingFt(),
		LimitTons:     t.track.WeightLimitTons,
		UsedTons:      t.usedTons,
		RemainingTons: t.remainingTons(),
		CarIDs:        ids,
	}
}

// Build computes the blocking plan for a yard order.
func Build(cfg *config.Config, order model.YardOrder) Plan {
	trackers := newTrackers(cfg)
	plan := Plan{}
	var findings []config.Finding
	for _, train := range order.Trains {
		for pos, car := range train.Cars {
			a := Assignment{
				CarID:       car.ID(),
				TrainID:     train.ID,
				Position:    pos + 1,
				Destination: car.Destination,
			}
			assignOne(cfg, trackers, &a, car, &findings)
			plan.Assignments = append(plan.Assignments, a)
		}
	}
	sort.SliceStable(plan.Assignments, func(i, j int) bool {
		return plan.Assignments[i].CarID < plan.Assignments[j].CarID
	})
	for _, id := range sortedTrackerIDs(trackers) {
		plan.Tracks = append(plan.Tracks, trackers[id].load())
	}
	for _, a := range plan.Assignments {
		switch a.Status {
		case StatusUnblocked:
			plan.Unblocked = append(plan.Unblocked, a.CarID)
		case StatusOverflow:
			plan.Overflow = append(plan.Overflow, a.CarID)
		case StatusRejected:
			plan.Rejected = append(plan.Rejected, a.CarID)
		}
	}
	findings = append(findings, capacityFindings(cfg, trackers)...)
	sortFindings(findings)
	plan.Findings = findings
	return plan
}

// assignOne resolves the destination of a single car and places it.
func assignOne(cfg *config.Config, trackers map[string]*tracker, a *Assignment, car model.Car, findings *[]config.Finding) {
	if car.BadOrder {
		a.Status = StatusRepair
		a.Reason = "bad order: " + car.BadOrderWhy
		a.TrackID = cfg.Yard.RepairTrack
		if tr, ok := trackers[cfg.Yard.RepairTrack]; ok {
			a.Block = tr.track.Block
			if !tr.fits(car) {
				a.Status = StatusRejected
				a.Reason = fmt.Sprintf("repair track %s is full", tr.track.ID)
				a.TrackID = ""
				*findings = append(*findings, finding(config.SeverityError, "car", car.ID(), "repair track %s cannot hold car", tr.track.ID))
				return
			}
			tr.push(car)
		}
		return
	}
	blockID, ok := cfg.BlockForDestination(car.Destination)
	if !ok {
		a.Status = StatusUnblocked
		a.Reason = fmt.Sprintf("destination %s maps to no block", car.Destination)
		placeOverflow(cfg, trackers, a, car, findings)
		return
	}
	a.Block = blockID
	if b, found := cfg.BlockByID(blockID); found {
		a.Priority = b.Priority
	}
	candidates := eligibleTrackers(cfg, trackers, blockID, car)
	if len(candidates) == 0 {
		a.Reason = fmt.Sprintf("no track in block %s can take the car", blockID)
		a.Status = StatusOverflow
		placeOverflow(cfg, trackers, a, car, findings)
		return
	}
	chosen := candidates[0]
	chosen.push(car)
	a.TrackID = chosen.track.ID
	a.Status = StatusAssigned
	a.Reason = fmt.Sprintf("block %s track %s", blockID, chosen.track.ID)
}

// placeOverflow attempts to place a car on the yard overflow track.
func placeOverflow(cfg *config.Config, trackers map[string]*tracker, a *Assignment, car model.Car, findings *[]config.Finding) {
	tr, ok := trackers[cfg.Yard.OverflowTrack]
	if !ok {
		a.Status = StatusRejected
		a.TrackID = ""
		*findings = append(*findings, finding(config.SeverityError, "car", car.ID(), "overflow track %s is not defined", cfg.Yard.OverflowTrack))
		return
	}
	if ok, why := tr.track.Accepts(car, tr.longCarFt); !ok {
		a.Status = StatusRejected
		a.TrackID = ""
		*findings = append(*findings, finding(config.SeverityError, "car", car.ID(), "overflow track %s refuses car: %s", tr.track.ID, why))
		return
	}
	if !tr.fits(car) {
		a.Status = StatusRejected
		a.TrackID = ""
		*findings = append(*findings, finding(config.SeverityError, "car", car.ID(), "overflow track %s is full", tr.track.ID))
		return
	}
	tr.push(car)
	a.TrackID = tr.track.ID
	if a.Status == StatusUnblocked {
		return
	}
	a.Status = StatusOverflow
}

// eligibleTrackers returns the trackers of a block that can hold the car, best
// candidate first. The ordering rule is: most remaining length wins, then the
// lexically smallest track identifier.
func eligibleTrackers(cfg *config.Config, trackers map[string]*tracker, blockID string, car model.Car) []*tracker {
	var out []*tracker
	for _, t := range cfg.TracksForBlock(blockID) {
		tr, ok := trackers[t.ID]
		if !ok {
			continue
		}
		if tr.track.ID == cfg.Yard.RepairTrack {
			continue
		}
		if ok, _ := tr.track.Accepts(car, tr.longCarFt); !ok {
			continue
		}
		if !tr.fits(car) {
			continue
		}
		if car.Placard && tr.placards >= cfg.Hazmat.MaxPlacardsPerTrack {
			continue
		}
		out = append(out, tr)
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].remainingFt() != out[j].remainingFt() {
			return out[i].remainingFt() > out[j].remainingFt()
		}
		return out[i].track.ID < out[j].track.ID
	})
	return out
}

// newTrackers builds a tracker per classification track.
func newTrackers(cfg *config.Config) map[string]*tracker {
	out := make(map[string]*tracker, len(cfg.Class))
	for _, t := range cfg.Class {
		out[t.ID] = &tracker{track: t, slackFt: cfg.Yard.CouplerSlackFt, longCarFt: cfg.Yard.LongCarFt}
	}
	return out
}

// sortedTrackerIDs returns tracker keys in lexical order.
func sortedTrackerIDs(trackers map[string]*tracker) []string {
	out := make([]string, 0, len(trackers))
	for id := range trackers {
		out = append(out, id)
	}
	sort.Strings(out)
	return out
}

// capacityFindings raises advisories for tracks that end up unusable or empty.
func capacityFindings(cfg *config.Config, trackers map[string]*tracker) []config.Finding {
	var out []config.Finding
	for _, id := range sortedTrackerIDs(trackers) {
		tr := trackers[id]
		if tr.usedFt == 0 {
			out = append(out, finding(config.SeverityWarn, "class_track", id, "track received no cars"))
			continue
		}
		usedPct := float64(tr.usedFt) * 100 / float64(tr.track.CapacityFt)
		if usedPct > 95 {
			out = append(out, finding(config.SeverityWarn, "class_track", id, "track is %.1f%% full by length", usedPct))
		}
		tonsPct := tr.usedTons * 100 / tr.track.WeightLimitTons
		if tonsPct > 95 {
			out = append(out, finding(config.SeverityWarn, "class_track", id, "track is %.1f%% full by weight", tonsPct))
		}
		if tr.placards > cfg.Hazmat.MaxPlacardsPerTrack {
			out = append(out, finding(config.SeverityError, "class_track", id, "%d placarded cars exceed the limit of %d", tr.placards, cfg.Hazmat.MaxPlacardsPerTrack))
		}
	}
	return out
}

// finding builds a config finding.
func finding(sev, scope, subject, format string, args ...any) config.Finding {
	return config.Finding{Severity: sev, Scope: scope, Subject: subject, Message: fmt.Sprintf(format, args...)}
}

// sortFindings orders findings deterministically.
func sortFindings(findings []config.Finding) {
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
}

// Summary is a compact digest of a blocking plan.
type Summary struct {
	Cars        int     `json:"cars"`
	Assigned    int     `json:"assigned"`
	Overflow    int     `json:"overflow"`
	Repair      int     `json:"repair"`
	Unblocked   int     `json:"unblocked"`
	Rejected    int     `json:"rejected"`
	Tracks      int     `json:"tracks"`
	TracksUsed  int     `json:"tracks_used"`
	FillPercent float64 `json:"fill_percent"`
}

// Digest computes the summary of a blocking plan.
func (p Plan) Digest() Summary {
	s := Summary{Cars: len(p.Assignments), Tracks: len(p.Tracks)}
	for _, a := range p.Assignments {
		switch a.Status {
		case StatusAssigned:
			s.Assigned++
		case StatusOverflow:
			s.Overflow++
		case StatusRepair:
			s.Repair++
		case StatusUnblocked:
			s.Unblocked++
		case StatusRejected:
			s.Rejected++
		}
	}
	capacity, used := 0, 0
	for _, t := range p.Tracks {
		capacity += t.CapacityFt
		used += t.UsedFt
		if t.UsedFt > 0 {
			s.TracksUsed++
		}
	}
	if capacity > 0 {
		s.FillPercent = float64(used) * 100 / float64(capacity)
	}
	return s
}
