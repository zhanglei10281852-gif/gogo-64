// Package hump sequences the crest. It decides which cars may be shoved over
// the hump, groups them into cuts bound for a single classification track, and
// records the cars that must be flat switched instead, with the reason.
package hump

import (
	"fmt"
	"math"
	"sort"

	"HumpYard/internal/blocking"
	"HumpYard/internal/config"
	"HumpYard/internal/model"
)

// Movement kinds.
const (
	// KindHump is a cut shoved over the crest.
	KindHump = "hump"
	// KindFlat is a flat switching move made with a yard engine.
	KindFlat = "flat"
)

// Retarder settings applied to a cut.
const (
	RetarderLight  = "light"
	RetarderNormal = "normal"
	RetarderHeavy  = "heavy"
)

// Reasons a car cannot be humped.
const (
	ReasonBadOrder    = "bad-order"
	ReasonRestriction = "cut-restriction"
	ReasonExcessLen   = "excessive-length"
	ReasonHazmatFlat  = "hazmat-flat-switch-only"
	ReasonRetarder    = "exceeds-retarder-capacity"
	ReasonNoTrack     = "no-classification-track"
	ReasonFlatTrack   = "track-reachable-by-flat-only"
	ReasonDrawbar     = "drawbar-pair-split"
)

// Cut is a group of cars shoved over the crest together, all bound for the
// same classification track.
type Cut struct {
	Index         int      `json:"index"`
	TrainID       string   `json:"train_id"`
	ReceivingID   string   `json:"receiving_track"`
	TrackID       string   `json:"track_id"`
	Block         string   `json:"block"`
	CarIDs        []string `json:"car_ids"`
	LengthFt      int      `json:"length_ft"`
	Tons          float64  `json:"tons"`
	Axles         int      `json:"axles"`
	Retarder      string   `json:"retarder_setting"`
	RiderRequired bool     `json:"rider_required"`
	StartMinute   int      `json:"start_minute"`
	EndMinute     int      `json:"end_minute"`
}

// FlatMove is a car set aside for flat switching, with the governing reason.
type FlatMove struct {
	Index       int      `json:"index"`
	TrainID     string   `json:"train_id"`
	ReceivingID string   `json:"receiving_track"`
	TrackID     string   `json:"track_id"`
	CarIDs      []string `json:"car_ids"`
	Reason      string   `json:"reason"`
	Detail      string   `json:"detail"`
	LengthFt    int      `json:"length_ft"`
	Tons        float64  `json:"tons"`
	StartMinute int      `json:"start_minute"`
	EndMinute   int      `json:"end_minute"`
}

// Movement is a cut or a flat move in execution order. Downstream packages
// replay movements to simulate track occupancy.
type Movement struct {
	Seq         int      `json:"seq"`
	Kind        string   `json:"kind"`
	TrainID     string   `json:"train_id"`
	TrackID     string   `json:"track_id"`
	CarIDs      []string `json:"car_ids"`
	StartMinute int      `json:"start_minute"`
	EndMinute   int      `json:"end_minute"`
	Note        string   `json:"note"`
}

// Plan is the full crest sequencing result.
type Plan struct {
	Cuts      []Cut            `json:"cuts"`
	FlatMoves []FlatMove       `json:"flat_moves"`
	Movements []Movement       `json:"movements"`
	Stats     Stats            `json:"stats"`
	Findings  []config.Finding `json:"findings"`
}

// Stats summarizes the crest plan.
type Stats struct {
	CarsHumped     int     `json:"cars_humped"`
	CarsFlat       int     `json:"cars_flat_switched"`
	Cuts           int     `json:"cuts"`
	RiderCuts      int     `json:"rider_cuts"`
	HumpMinutes    int     `json:"hump_minutes"`
	FlatMinutes    int     `json:"flat_minutes"`
	FirstMinute    int     `json:"first_minute"`
	LastMinute     int     `json:"last_minute"`
	AverageCutCars float64 `json:"average_cut_cars"`
}

// disposition is the intermediate decision for one car.
type disposition struct {
	car      model.Car
	position int
	track    string
	block    string
	hump     bool
	rider    bool
	retarder string
	reason   string
	detail   string
}

// Build sequences the crest for a yard order using an existing blocking plan.
func Build(cfg *config.Config, order model.YardOrder, bp blocking.Plan) Plan {
	plan := Plan{}
	clock := 0
	if len(order.Trains) > 0 {
		clock = order.Trains[0].ArrivalMinute
	}
	cutIndex := 0
	flatIndex := 0
	for _, train := range order.Trains {
		lead := 0
		if rt, ok := cfg.ReceivingByID(train.ReceivingID); ok {
			lead = rt.LeadMinutes
		}
		if ready := train.ArrivalMinute + lead; ready > clock {
			clock = ready
		}
		dispositions := classify(cfg, train, bp, &plan.Findings)
		flats := collectFlats(dispositions)
		for _, group := range flats {
			flatIndex++
			move := buildFlatMove(cfg, train, group, flatIndex, clock)
			clock = move.EndMinute
			plan.FlatMoves = append(plan.FlatMoves, move)
		}
		for _, group := range collectCuts(cfg, dispositions) {
			cutIndex++
			cut := buildCut(cfg, train, group, cutIndex, clock)
			clock = cut.EndMinute
			plan.Cuts = append(plan.Cuts, cut)
		}
	}
	plan.Movements = movements(plan)
	plan.Stats = digest(plan)
	plan.Findings = append(plan.Findings, ruleFindings(cfg, plan)...)
	sortFindings(plan.Findings)
	return plan
}

// classify decides for each car in a train whether it may be humped.
func classify(cfg *config.Config, train model.InboundTrain, bp blocking.Plan, findings *[]config.Finding) []disposition {
	out := make([]disposition, 0, len(train.Cars))
	for pos, car := range train.Cars {
		d := disposition{car: car, position: pos + 1, hump: true, retarder: RetarderNormal}
		a, ok := bp.AssignmentFor(car.ID())
		if ok {
			d.track = a.TrackID
			d.block = a.Block
		}
		if d.block == "" && d.track != "" {
			// An unblocked or diverted car still stands on a track that
			// serves some block; label the cut with that block.
			if t, found := cfg.ClassTrackByID(d.track); found {
				d.block = t.Block
			}
		}
		applyRules(cfg, &d)
		out = append(out, d)
	}
	applyDrawbarRules(out, findings)
	return out
}

// applyRules evaluates every hump eligibility rule against one car.
func applyRules(cfg *config.Config, d *disposition) {
	car := d.car
	switch {
	case car.BadOrder:
		d.hump = false
		d.reason = ReasonBadOrder
		d.detail = car.BadOrderWhy
	case car.Flat():
		d.hump = false
		d.reason = ReasonRestriction
		d.detail = fmt.Sprintf("restriction %s", car.Restriction)
	case car.LengthFt > cfg.Hump.MaxCarLengthFt:
		d.hump = false
		d.reason = ReasonExcessLen
		d.detail = fmt.Sprintf("%d ft exceeds crest limit %d ft", car.LengthFt, cfg.Hump.MaxCarLengthFt)
	case car.Hazmat() && cfg.Hazmat.RequiresFlatSwitch(car.HazmatClass):
		d.hump = false
		d.reason = ReasonHazmatFlat
		d.detail = fmt.Sprintf("hazmat class %s must be flat switched", car.HazmatClass)
	case car.GrossTons > cfg.Hump.RetarderMaxTons:
		d.hump = false
		d.reason = ReasonRetarder
		d.detail = fmt.Sprintf("%.1f tons exceeds retarder capacity %.1f tons", car.GrossTons, cfg.Hump.RetarderMaxTons)
	case d.track == "":
		d.hump = false
		d.reason = ReasonNoTrack
		d.detail = fmt.Sprintf("destination %s has no classification track", car.Destination)
	}
	if !d.hump {
		return
	}
	track, ok := cfg.ClassTrackByID(d.track)
	if ok && track.HasRestriction(model.ResFlatOnly) {
		d.hump = false
		d.reason = ReasonFlatTrack
		d.detail = fmt.Sprintf("track %s is flat-switch only", track.ID)
		return
	}
	if car.RoughRider && cfg.Hump.RiderRequiredRough {
		d.rider = true
	}
	if ok && track.GradePct < cfg.Hump.MinBowlGradePct && !car.EasyRoller {
		d.rider = true
	}
	switch {
	case car.Restriction == model.CutCushion:
		d.retarder = RetarderLight
	case car.EasyRoller:
		d.retarder = RetarderHeavy
	case car.GrossTons > cfg.Hump.RetarderMaxTons*0.8:
		d.retarder = RetarderHeavy
	}
}

// applyDrawbarRules forces both halves of a drawbar pair to the same handling.
// A pair that is not standing adjacent, or whose halves are bound for different
// tracks, must be flat switched because it cannot be uncoupled at the crest.
func applyDrawbarRules(ds []disposition, findings *[]config.Finding) {
	index := map[string]int{}
	for i, d := range ds {
		index[d.car.ID()] = i
	}
	for i := range ds {
		mate := ds[i].car.DrawbarMate
		if mate == "" {
			continue
		}
		j, ok := index[mate]
		if !ok {
			continue
		}
		adjacent := j == i+1 || j == i-1
		sameTrack := ds[i].track == ds[j].track
		if adjacent && sameTrack {
			if !ds[i].hump || !ds[j].hump {
				mirror(&ds[i], &ds[j])
			}
			if ds[i].rider || ds[j].rider {
				ds[i].rider = true
				ds[j].rider = true
			}
			continue
		}
		detail := fmt.Sprintf("drawbar mate %s is not adjacent", mate)
		if adjacent && !sameTrack {
			detail = fmt.Sprintf("drawbar mate %s is bound for track %s", mate, ds[j].track)
		}
		if ds[i].hump {
			ds[i].hump = false
			ds[i].reason = ReasonDrawbar
			ds[i].detail = detail
			*findings = append(*findings, config.Finding{
				Severity: config.SeverityWarn,
				Scope:    "car",
				Subject:  ds[i].car.ID(),
				Message:  "flat switched: " + detail,
			})
		}
	}
}

// mirror copies the flat-switch decision from whichever half already carries
// one onto the other half of a drawbar pair.
func mirror(a, b *disposition) {
	if !a.hump && b.hump {
		b.hump = false
		b.reason = a.reason
		b.detail = "drawbar mate " + a.car.ID() + ": " + a.detail
		return
	}
	if !b.hump && a.hump {
		a.hump = false
		a.reason = b.reason
		a.detail = "drawbar mate " + b.car.ID() + ": " + b.detail
	}
}

// flatGroup is a set of cars pulled together for one flat move.
type flatGroup struct {
	trackID string
	reason  string
	detail  string
	cars    []model.Car
}

// collectFlats groups flat-switch cars by target track and reason so that a
// single engine move handles as many cars as possible.
func collectFlats(ds []disposition) []flatGroup {
	order := []string{}
	groups := map[string]*flatGroup{}
	for _, d := range ds {
		if d.hump {
			continue
		}
		key := d.track + "|" + d.reason
		g, ok := groups[key]
		if !ok {
			g = &flatGroup{trackID: d.track, reason: d.reason, detail: d.detail}
			groups[key] = g
			order = append(order, key)
		}
		g.cars = append(g.cars, d.car)
	}
	sort.Strings(order)
	out := make([]flatGroup, 0, len(order))
	for _, key := range order {
		out = append(out, *groups[key])
	}
	return out
}

// cutGroup is a set of consecutive humpable cars for one track.
type cutGroup struct {
	trackID  string
	block    string
	retarder string
	rider    bool
	cars     []model.Car
}

// collectCuts groups the humpable cars of a train into cuts. Cars are grouped
// while they remain bound for the same track, the cut stays inside the crest
// cut limit, and no rider is needed. A rider cut carries a single car, or two
// cars when they are drawbar mates.
func collectCuts(cfg *config.Config, ds []disposition) []cutGroup {
	var out []cutGroup
	var cur *cutGroup
	flush := func() {
		if cur != nil && len(cur.cars) > 0 {
			out = append(out, *cur)
		}
		cur = nil
	}
	for _, d := range ds {
		if !d.hump {
			continue
		}
		limit := cfg.Hump.MaxCutCars
		if d.rider {
			limit = 1
			if d.car.DrawbarMate != "" {
				limit = 2
			}
		}
		if cur != nil {
			sameTrack := cur.trackID == d.track
			sameRider := cur.rider == d.rider
			room := len(cur.cars) < limit && len(cur.cars) < cfg.Hump.MaxCutCars
			if !sameTrack || !sameRider || !room {
				flush()
			}
		}
		if cur == nil {
			cur = &cutGroup{trackID: d.track, block: d.block, retarder: d.retarder, rider: d.rider}
		}
		cur.cars = append(cur.cars, d.car)
		if d.retarder == RetarderLight {
			cur.retarder = RetarderLight
		} else if d.retarder == RetarderHeavy && cur.retarder != RetarderLight {
			cur.retarder = RetarderHeavy
		}
	}
	flush()
	return out
}

// buildCut renders a cut group with timing.
func buildCut(cfg *config.Config, train model.InboundTrain, g cutGroup, index, start int) Cut {
	ids := carIDs(g.cars)
	minutes := humpMinutes(cfg, len(g.cars))
	return Cut{
		Index:         index,
		TrainID:       train.ID,
		ReceivingID:   train.ReceivingID,
		TrackID:       g.trackID,
		Block:         g.block,
		CarIDs:        ids,
		LengthFt:      model.TotalLengthFt(g.cars),
		Tons:          model.TotalTons(g.cars),
		Axles:         model.TotalAxles(g.cars),
		Retarder:      g.retarder,
		RiderRequired: g.rider,
		StartMinute:   start,
		EndMinute:     start + minutes,
	}
}

// buildFlatMove renders a flat group with timing.
func buildFlatMove(cfg *config.Config, train model.InboundTrain, g flatGroup, index, start int) FlatMove {
	minutes := cfg.Hump.FlatMinutesPerCar * len(g.cars)
	return FlatMove{
		Index:       index,
		TrainID:     train.ID,
		ReceivingID: train.ReceivingID,
		TrackID:     g.trackID,
		CarIDs:      carIDs(g.cars),
		Reason:      g.reason,
		Detail:      g.detail,
		LengthFt:    model.TotalLengthFt(g.cars),
		Tons:        model.TotalTons(g.cars),
		StartMinute: start,
		EndMinute:   start + minutes,
	}
}

// humpMinutes converts a car count into whole crest minutes.
func humpMinutes(cfg *config.Config, cars int) int {
	rate := cfg.Hump.CarsPerMinute
	if rate <= 0 {
		rate = 1
	}
	return int(math.Ceil(float64(cars)/rate)) + cfg.Hump.CutChangeMinutes
}

// carIDs extracts identifiers preserving order.
func carIDs(cars []model.Car) []string {
	out := make([]string, 0, len(cars))
	for _, c := range cars {
		out = append(out, c.ID())
	}
	return out
}

// movements flattens cuts and flat moves into a single execution order sorted
// by start minute, then kind, then index.
func movements(plan Plan) []Movement {
	var out []Movement
	for _, f := range plan.FlatMoves {
		out = append(out, Movement{
			Kind: KindFlat, TrainID: f.TrainID, TrackID: f.TrackID, CarIDs: f.CarIDs,
			StartMinute: f.StartMinute, EndMinute: f.EndMinute, Note: f.Reason,
		})
	}
	for _, c := range plan.Cuts {
		note := "retarder " + c.Retarder
		if c.RiderRequired {
			note += ", rider"
		}
		out = append(out, Movement{
			Kind: KindHump, TrainID: c.TrainID, TrackID: c.TrackID, CarIDs: c.CarIDs,
			StartMinute: c.StartMinute, EndMinute: c.EndMinute, Note: note,
		})
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].StartMinute != out[j].StartMinute {
			return out[i].StartMinute < out[j].StartMinute
		}
		if out[i].Kind != out[j].Kind {
			return out[i].Kind < out[j].Kind
		}
		return firstID(out[i]) < firstID(out[j])
	})
	for i := range out {
		out[i].Seq = i + 1
	}
	return out
}

// firstID returns the leading car identifier of a movement, used as the final
// tie-break when ordering movements.
func firstID(m Movement) string {
	if len(m.CarIDs) == 0 {
		return ""
	}
	return m.CarIDs[0]
}

// digest computes crest statistics.
func digest(plan Plan) Stats {
	st := Stats{Cuts: len(plan.Cuts)}
	first, last := -1, 0
	for _, c := range plan.Cuts {
		st.CarsHumped += len(c.CarIDs)
		st.HumpMinutes += c.EndMinute - c.StartMinute
		if c.RiderRequired {
			st.RiderCuts++
		}
		if first < 0 || c.StartMinute < first {
			first = c.StartMinute
		}
		if c.EndMinute > last {
			last = c.EndMinute
		}
	}
	for _, f := range plan.FlatMoves {
		st.CarsFlat += len(f.CarIDs)
		st.FlatMinutes += f.EndMinute - f.StartMinute
		if first < 0 || f.StartMinute < first {
			first = f.StartMinute
		}
		if f.EndMinute > last {
			last = f.EndMinute
		}
	}
	if first < 0 {
		first = 0
	}
	st.FirstMinute = first
	st.LastMinute = last
	if st.Cuts > 0 {
		st.AverageCutCars = float64(st.CarsHumped) / float64(st.Cuts)
	}
	return st
}

// ruleFindings raises advisories about the finished crest plan.
func ruleFindings(cfg *config.Config, plan Plan) []config.Finding {
	var out []config.Finding
	capacity := 0
	riders := 0
	for _, s := range cfg.Shifts {
		capacity += s.HumpCapacity
		riders += s.RiderCount
	}
	if capacity > 0 && plan.Stats.CarsHumped > capacity {
		out = append(out, config.Finding{
			Severity: config.SeverityError,
			Scope:    "hump",
			Subject:  cfg.Yard.ID,
			Message:  fmt.Sprintf("%d cars to hump exceed the daily crest capacity of %d cars", plan.Stats.CarsHumped, capacity),
		})
	}
	if riders == 0 && plan.Stats.RiderCuts > 0 {
		out = append(out, config.Finding{
			Severity: config.SeverityError,
			Scope:    "hump",
			Subject:  cfg.Yard.ID,
			Message:  fmt.Sprintf("%d cuts need a rider but no shift lists riders", plan.Stats.RiderCuts),
		})
	}
	for _, c := range plan.Cuts {
		if len(c.CarIDs) > cfg.Hump.MaxCutCars {
			out = append(out, config.Finding{
				Severity: config.SeverityError,
				Scope:    "cut",
				Subject:  fmt.Sprintf("%d", c.Index),
				Message:  fmt.Sprintf("cut holds %d cars, limit is %d", len(c.CarIDs), cfg.Hump.MaxCutCars),
			})
		}
	}
	return out
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

// CarsByTrack returns the humped car identifiers per classification track in
// crest order.
func (p Plan) CarsByTrack() map[string][]string {
	out := map[string][]string{}
	for _, m := range p.Movements {
		if m.TrackID == "" {
			continue
		}
		out[m.TrackID] = append(out[m.TrackID], m.CarIDs...)
	}
	return out
}

// FlatReasonCounts tallies flat-switch reasons for reporting.
func (p Plan) FlatReasonCounts() map[string]int {
	out := map[string]int{}
	for _, f := range p.FlatMoves {
		out[f.Reason] += len(f.CarIDs)
	}
	return out
}
