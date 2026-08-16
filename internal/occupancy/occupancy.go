// Package occupancy replays the crest plan onto the classification tracks. It
// pushes each car in movement order, keeps the remaining length and weight of
// every track, detects overflow, and produces the final standing order that
// downstream hazmat validation and train building consume.
package occupancy

import (
	"fmt"
	"sort"

	"HumpYard/internal/config"
	"HumpYard/internal/hazmat"
	"HumpYard/internal/hump"
	"HumpYard/internal/model"
)

// Action values recorded in the event log.
const (
	// ActionShoved means the car came to rest on its intended track.
	ActionShoved = "shoved"
	// ActionOverflow means the intended track was full and the car went to
	// the yard overflow track.
	ActionOverflow = "overflow"
	// ActionRefused means no track could hold the car at all.
	ActionRefused = "refused"
)

// TrackState is the final condition of one classification track.
type TrackState struct {
	TrackID       string   `json:"track_id"`
	Block         string   `json:"block"`
	CapacityFt    int      `json:"capacity_ft"`
	UsedFt        int      `json:"used_ft"`
	RemainingFt   int      `json:"remaining_ft"`
	LimitTons     float64  `json:"limit_tons"`
	UsedTons      float64  `json:"used_tons"`
	RemainingTons float64  `json:"remaining_tons"`
	FillPercent   float64  `json:"fill_percent"`
	CabooseSpot   bool     `json:"caboose_spot"`
	CarIDs        []string `json:"car_ids"`
}

// Event is one placement decision in movement order.
type Event struct {
	Seq         int     `json:"seq"`
	Kind        string  `json:"kind"`
	CarID       string  `json:"car_id"`
	IntendedID  string  `json:"intended_track"`
	FinalID     string  `json:"final_track"`
	Action      string  `json:"action"`
	Reason      string  `json:"reason"`
	TrackUsedFt int     `json:"track_used_ft"`
	TrackTons   float64 `json:"track_used_tons"`
	Minute      int     `json:"minute"`
}

// Stats summarizes the simulation.
type Stats struct {
	CarsPlaced     int     `json:"cars_placed"`
	CarsOverflowed int     `json:"cars_overflowed"`
	CarsRefused    int     `json:"cars_refused"`
	TracksUsed     int     `json:"tracks_used"`
	TotalUsedFt    int     `json:"total_used_ft"`
	TotalCapacity  int     `json:"total_capacity_ft"`
	FillPercent    float64 `json:"fill_percent"`
	PeakFillTrack  string  `json:"peak_fill_track"`
	PeakFillPct    float64 `json:"peak_fill_percent"`
}

// Result is the whole occupancy simulation outcome.
type Result struct {
	Tracks   []TrackState     `json:"tracks"`
	Events   []Event          `json:"events"`
	Overflow []string         `json:"overflow_cars"`
	Refused  []string         `json:"refused_cars"`
	Stats    Stats            `json:"stats"`
	Findings []config.Finding `json:"findings"`
}

// state is the mutable occupancy of one track during the replay.
type state struct {
	track   model.ClassTrack
	usedFt  int
	tons    float64
	carIDs  []string
	slackFt int
}

// fits reports whether the car still fits by length and weight.
func (s *state) fits(c model.Car) bool {
	return s.usedFt+c.LengthFt+s.slackFt <= s.track.CapacityFt &&
		s.tons+c.GrossTons <= s.track.WeightLimitTons
}

// push places the car at the trailing end of the track.
func (s *state) push(c model.Car) {
	s.usedFt += c.LengthFt + s.slackFt
	s.tons += c.GrossTons
	s.carIDs = append(s.carIDs, c.ID())
}

// Simulate replays the crest plan and returns the resulting occupancy.
func Simulate(cfg *config.Config, order model.YardOrder, plan hump.Plan) (Result, error) {
	index, err := model.NewCarIndex(order.AllCars())
	if err != nil {
		return Result{}, err
	}
	states := map[string]*state{}
	for _, t := range cfg.Class {
		states[t.ID] = &state{track: t, slackFt: cfg.Yard.CouplerSlackFt}
	}
	res := Result{}
	seq := 0
	for _, m := range plan.Movements {
		for _, carID := range m.CarIDs {
			car, ok := index[carID]
			if !ok {
				return Result{}, fmt.Errorf("movement %d references unknown car %q", m.Seq, carID)
			}
			seq++
			res.Events = append(res.Events, place(cfg, states, m, car, seq))
		}
	}
	for _, e := range res.Events {
		switch e.Action {
		case ActionOverflow:
			res.Overflow = append(res.Overflow, e.CarID)
		case ActionRefused:
			res.Refused = append(res.Refused, e.CarID)
		}
	}
	sort.Strings(res.Overflow)
	sort.Strings(res.Refused)
	res.Tracks = finalize(states)
	res.Stats = digest(res)
	res.Findings = findings(cfg, res)
	return res, nil
}

// place resolves one car onto a track, falling back to the overflow track.
func place(cfg *config.Config, states map[string]*state, m hump.Movement, car model.Car, seq int) Event {
	ev := Event{
		Seq:        seq,
		Kind:       m.Kind,
		CarID:      car.ID(),
		IntendedID: m.TrackID,
		Minute:     m.StartMinute,
	}
	target, ok := states[m.TrackID]
	if ok {
		if allowed, why := target.track.Accepts(car, cfg.Yard.LongCarFt); !allowed {
			ok = false
			ev.Reason = why
		} else if !target.fits(car) {
			ok = false
			ev.Reason = fmt.Sprintf("track %s has %d ft and %.1f tons left", target.track.ID,
				target.track.CapacityFt-target.usedFt, target.track.WeightLimitTons-target.tons)
		}
	} else {
		ev.Reason = fmt.Sprintf("track %q is not a classification track", m.TrackID)
	}
	if ok {
		target.push(car)
		ev.Action = ActionShoved
		ev.FinalID = target.track.ID
		ev.TrackUsedFt = target.usedFt
		ev.TrackTons = target.tons
		return ev
	}
	over, hasOverflow := states[cfg.Yard.OverflowTrack]
	if hasOverflow && over.track.ID != m.TrackID {
		if allowed, _ := over.track.Accepts(car, cfg.Yard.LongCarFt); allowed && over.fits(car) {
			over.push(car)
			ev.Action = ActionOverflow
			ev.FinalID = over.track.ID
			ev.TrackUsedFt = over.usedFt
			ev.TrackTons = over.tons
			return ev
		}
	}
	ev.Action = ActionRefused
	ev.FinalID = ""
	if ev.Reason == "" {
		ev.Reason = "no track available"
	}
	return ev
}

// finalize renders track states in identifier order.
func finalize(states map[string]*state) []TrackState {
	ids := make([]string, 0, len(states))
	for id := range states {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	out := make([]TrackState, 0, len(ids))
	for _, id := range ids {
		s := states[id]
		ts := TrackState{
			TrackID:       s.track.ID,
			Block:         s.track.Block,
			CapacityFt:    s.track.CapacityFt,
			UsedFt:        s.usedFt,
			RemainingFt:   s.track.CapacityFt - s.usedFt,
			LimitTons:     s.track.WeightLimitTons,
			UsedTons:      s.tons,
			RemainingTons: s.track.WeightLimitTons - s.tons,
			CabooseSpot:   s.track.CabooseSpot,
			CarIDs:        append([]string(nil), s.carIDs...),
		}
		if s.track.CapacityFt > 0 {
			ts.FillPercent = float64(s.usedFt) * 100 / float64(s.track.CapacityFt)
		}
		out = append(out, ts)
	}
	return out
}

// digest computes simulation statistics.
func digest(res Result) Stats {
	st := Stats{}
	for _, e := range res.Events {
		switch e.Action {
		case ActionShoved:
			st.CarsPlaced++
		case ActionOverflow:
			st.CarsPlaced++
			st.CarsOverflowed++
		case ActionRefused:
			st.CarsRefused++
		}
	}
	for _, t := range res.Tracks {
		st.TotalCapacity += t.CapacityFt
		st.TotalUsedFt += t.UsedFt
		if len(t.CarIDs) > 0 {
			st.TracksUsed++
		}
		if t.FillPercent > st.PeakFillPct {
			st.PeakFillPct = t.FillPercent
			st.PeakFillTrack = t.TrackID
		}
	}
	if st.TotalCapacity > 0 {
		st.FillPercent = float64(st.TotalUsedFt) * 100 / float64(st.TotalCapacity)
	}
	return st
}

// findings raises advisories about the finished occupancy.
func findings(cfg *config.Config, res Result) []config.Finding {
	var out []config.Finding
	for _, id := range res.Refused {
		out = append(out, config.Finding{
			Severity: config.SeverityError,
			Scope:    "occupancy",
			Subject:  id,
			Message:  "car could not be placed on any track",
		})
	}
	for _, t := range res.Tracks {
		if t.RemainingFt < 0 {
			out = append(out, config.Finding{
				Severity: config.SeverityError,
				Scope:    "occupancy",
				Subject:  t.TrackID,
				Message:  fmt.Sprintf("track is over capacity by %d ft", -t.RemainingFt),
			})
		}
		if t.RemainingTons < 0 {
			out = append(out, config.Finding{
				Severity: config.SeverityError,
				Scope:    "occupancy",
				Subject:  t.TrackID,
				Message:  fmt.Sprintf("track is over its weight limit by %.1f tons", -t.RemainingTons),
			})
		}
		if t.FillPercent >= 90 && t.RemainingFt >= 0 {
			out = append(out, config.Finding{
				Severity: config.SeverityWarn,
				Scope:    "occupancy",
				Subject:  t.TrackID,
				Message:  fmt.Sprintf("track is %.1f%% occupied with %d ft to spare", t.FillPercent, t.RemainingFt),
			})
		}
	}
	if len(res.Overflow) > 0 {
		out = append(out, config.Finding{
			Severity: config.SeverityWarn,
			Scope:    "occupancy",
			Subject:  cfg.Yard.OverflowTrack,
			Message:  fmt.Sprintf("%d cars were diverted to the overflow track", len(res.Overflow)),
		})
	}
	sort.SliceStable(out, func(i, j int) bool {
		a, b := out[i], out[j]
		if a.Severity != b.Severity {
			return a.Severity < b.Severity
		}
		if a.Subject != b.Subject {
			return a.Subject < b.Subject
		}
		return a.Message < b.Message
	})
	return out
}

// Placements converts the final occupancy into hazmat placements.
func (r Result) Placements(index model.CarIndex) []hazmat.Placement {
	out := make([]hazmat.Placement, 0, len(r.Tracks))
	for _, t := range r.Tracks {
		if len(t.CarIDs) == 0 {
			continue
		}
		cars := make([]model.Car, 0, len(t.CarIDs))
		for _, id := range t.CarIDs {
			if c, ok := index[id]; ok {
				cars = append(cars, c)
			}
		}
		out = append(out, hazmat.Placement{
			TrackID:       t.TrackID,
			Kind:          "classification",
			Cars:          cars,
			CabooseAtRear: t.CabooseSpot,
		})
	}
	return out
}

// TrackByID returns the final state of a track.
func (r Result) TrackByID(id string) (TrackState, bool) {
	for _, t := range r.Tracks {
		if t.TrackID == id {
			return t, true
		}
	}
	return TrackState{}, false
}

// FinalTrackOf returns the track a car came to rest on.
func (r Result) FinalTrackOf(carID string) string {
	for _, e := range r.Events {
		if e.CarID == carID {
			return e.FinalID
		}
	}
	return ""
}
