// Package depart builds outbound trains from the classified bowl. Blocks are
// pulled in the order the departure program demands, subject to train length,
// weight and axle ceilings and to the tonnage rating of the assigned power. The
// result is a consist manifest plus the list of cars left behind.
package depart

import (
	"fmt"
	"sort"

	"HumpYard/internal/config"
	"HumpYard/internal/hazmat"
	"HumpYard/internal/model"
	"HumpYard/internal/occupancy"
)

// Reasons a car is held out of a consist.
const (
	HeldLength    = "train-length-limit"
	HeldWeight    = "train-weight-limit"
	HeldAxles     = "train-axle-limit"
	HeldMisrouted = "misrouted-to-wrong-block"
	HeldBadOrder  = "bad-order"
	HeldNoTrain   = "no-departure-order-for-block"
)

// ConsistCar is one car in a built train, numbered from the head end.
type ConsistCar struct {
	Position int     `json:"position"`
	CarID    string  `json:"car_id"`
	Block    string  `json:"block"`
	TrackID  string  `json:"track_id"`
	LengthFt int     `json:"length_ft"`
	Tons     float64 `json:"tons"`
	Axles    int     `json:"axles"`
	Hazmat   string  `json:"hazmat_class"`
	Placard  bool    `json:"placard"`
}

// BlockFill records how much of a block made it onto a train.
type BlockFill struct {
	Block    string   `json:"block"`
	Cars     int      `json:"cars"`
	LengthFt int      `json:"length_ft"`
	Tons     float64  `json:"tons"`
	Tracks   []string `json:"tracks"`
}

// HeldCar is a car that could not ride the train it was pulled for.
type HeldCar struct {
	CarID  string `json:"car_id"`
	Block  string `json:"block"`
	Track  string `json:"track_id"`
	Reason string `json:"reason"`
	Detail string `json:"detail"`
}

// Train is one built outbound train.
type Train struct {
	TrainID      string           `json:"train_id"`
	TrackID      string           `json:"departure_track"`
	DepartMinute int              `json:"depart_minute"`
	Locomotives  []string         `json:"locomotives"`
	LocoLengthFt int              `json:"loco_length_ft"`
	LocoAxles    int              `json:"loco_axles"`
	RatedTons    float64          `json:"rated_tons"`
	Horsepower   int              `json:"horsepower"`
	Cars         []ConsistCar     `json:"cars"`
	Blocks       []BlockFill      `json:"blocks"`
	Held         []HeldCar        `json:"held_cars"`
	LengthFt     int              `json:"length_ft"`
	TrailingTons float64          `json:"trailing_tons"`
	Axles        int              `json:"axles"`
	HPPerTon     float64          `json:"horsepower_per_ton"`
	PowerShort   bool             `json:"power_short"`
	Complete     bool             `json:"complete"`
	Findings     []config.Finding `json:"findings"`
}

// Stats summarizes the departure program.
type Stats struct {
	Trains        int     `json:"trains"`
	CarsForwarded int     `json:"cars_forwarded"`
	CarsHeld      int     `json:"cars_held"`
	TotalTons     float64 `json:"total_tons"`
	TotalLengthFt int     `json:"total_length_ft"`
	PowerShort    int     `json:"trains_short_of_power"`
	Incomplete    int     `json:"trains_incomplete"`
}

// Plan is the whole departure program result.
type Plan struct {
	Trains   []Train          `json:"trains"`
	Held     []HeldCar        `json:"held_cars"`
	Stats    Stats            `json:"stats"`
	Findings []config.Finding `json:"findings"`
}

// builder carries the state shared while building all trains.
type builder struct {
	cfg      *config.Config
	index    model.CarIndex
	occ      occupancy.Result
	consumed map[string]bool
}

// Build assembles every outbound train in the departure program.
func Build(cfg *config.Config, order model.YardOrder, occ occupancy.Result) (Plan, error) {
	index, err := model.NewCarIndex(order.AllCars())
	if err != nil {
		return Plan{}, err
	}
	b := &builder{cfg: cfg, index: index, occ: occ, consumed: map[string]bool{}}
	plan := Plan{}
	for _, spec := range cfg.Departures {
		plan.Trains = append(plan.Trains, b.buildTrain(spec))
	}
	plan.Held = b.leftovers(plan.Trains)
	plan.Stats = digest(plan)
	plan.Findings = collectFindings(plan)
	return plan, nil
}

// buildTrain assembles one outbound train.
func (b *builder) buildTrain(spec config.DepartureOrder) Train {
	tr := Train{
		TrainID:      spec.TrainID,
		TrackID:      spec.TrackID,
		DepartMinute: spec.DepartMin,
		Locomotives:  append([]string(nil), spec.Locomotives...),
	}
	for _, id := range spec.Locomotives {
		if unit, ok := b.cfg.LocomotiveByID(id); ok {
			tr.LocoLengthFt += unit.LengthFt + b.cfg.Yard.CouplerSlackFt
			tr.LocoAxles += unit.Axles
			tr.RatedTons += unit.RatedTons
			tr.Horsepower += unit.Horsepower
		}
	}
	tr.LengthFt = tr.LocoLengthFt
	tr.Axles = tr.LocoAxles
	position := 0
	for _, blockID := range spec.BlockOrder {
		fill := BlockFill{Block: blockID}
		for _, track := range b.pullTracks(blockID) {
			pulled := 0
			for _, carID := range track.CarIDs {
				if b.consumed[carID] {
					continue
				}
				car, ok := b.index[carID]
				if !ok {
					continue
				}
				if held, reason, detail := b.reject(car, blockID); held {
					if reason == HeldMisrouted || reason == HeldBadOrder {
						continue
					}
					tr.Held = append(tr.Held, HeldCar{CarID: carID, Block: blockID, Track: track.TrackID, Reason: reason, Detail: detail})
					continue
				}
				if reason, detail, over := b.exceeds(spec, tr, car); over {
					tr.Held = append(tr.Held, HeldCar{CarID: carID, Block: blockID, Track: track.TrackID, Reason: reason, Detail: detail})
					continue
				}
				position++
				tr.Cars = append(tr.Cars, ConsistCar{
					Position: position,
					CarID:    carID,
					Block:    blockID,
					TrackID:  track.TrackID,
					LengthFt: car.LengthFt,
					Tons:     car.GrossTons,
					Axles:    car.Axles,
					Hazmat:   car.HazmatClass,
					Placard:  car.Placard,
				})
				tr.LengthFt += car.LengthFt + b.cfg.Yard.CouplerSlackFt
				tr.TrailingTons += car.GrossTons
				tr.Axles += car.Axles
				fill.Cars++
				fill.LengthFt += car.LengthFt
				fill.Tons += car.GrossTons
				b.consumed[carID] = true
				pulled++
			}
			if pulled > 0 {
				fill.Tracks = append(fill.Tracks, track.TrackID)
			}
		}
		sort.Strings(fill.Tracks)
		tr.Blocks = append(tr.Blocks, fill)
	}
	b.finishTrain(spec, &tr)
	return tr
}

// pullTracks lists the classification tracks a block is pulled from, excluding
// the repair track. Tracks are pulled in identifier order.
func (b *builder) pullTracks(blockID string) []occupancy.TrackState {
	var out []occupancy.TrackState
	for _, t := range b.occ.Tracks {
		if t.Block != blockID {
			continue
		}
		if t.TrackID == b.cfg.Yard.RepairTrack {
			continue
		}
		if len(t.CarIDs) == 0 {
			continue
		}
		out = append(out, t)
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].TrackID < out[j].TrackID })
	return out
}

// reject reports whether a car standing in a block must not ride this train at
// all, independent of the train ceilings.
func (b *builder) reject(car model.Car, blockID string) (bool, string, string) {
	if car.BadOrder {
		return true, HeldBadOrder, car.BadOrderWhy
	}
	want, ok := b.cfg.BlockForDestination(car.Destination)
	if !ok {
		return true, HeldMisrouted, fmt.Sprintf("destination %s maps to no block", car.Destination)
	}
	if want != blockID {
		return true, HeldMisrouted, fmt.Sprintf("car belongs to block %s, standing in %s", want, blockID)
	}
	return false, "", ""
}

// exceeds reports whether adding a car would break a train ceiling.
func (b *builder) exceeds(spec config.DepartureOrder, tr Train, car model.Car) (string, string, bool) {
	if tr.LengthFt+car.LengthFt+b.cfg.Yard.CouplerSlackFt > spec.MaxLengthFt {
		return HeldLength, fmt.Sprintf("adding %d ft would exceed the %d ft limit", car.LengthFt, spec.MaxLengthFt), true
	}
	if tr.TrailingTons+car.GrossTons > spec.MaxTons {
		return HeldWeight, fmt.Sprintf("adding %.1f tons would exceed the %.1f ton limit", car.GrossTons, spec.MaxTons), true
	}
	if tr.Axles+car.Axles > spec.MaxAxles {
		return HeldAxles, fmt.Sprintf("adding %d axles would exceed the %d axle limit", car.Axles, spec.MaxAxles), true
	}
	return "", "", false
}

// finishTrain computes derived figures and per-train findings.
func (b *builder) finishTrain(spec config.DepartureOrder, tr *Train) {
	if tr.TrailingTons > 0 {
		tr.HPPerTon = float64(tr.Horsepower) / tr.TrailingTons
	}
	tr.PowerShort = tr.RatedTons < tr.TrailingTons
	tr.Complete = len(tr.Cars) >= spec.MinCars && len(tr.Held) == 0
	if tr.PowerShort {
		tr.Findings = append(tr.Findings, config.Finding{
			Severity: config.SeverityError,
			Scope:    "train",
			Subject:  tr.TrainID,
			Message: fmt.Sprintf("trailing tonnage %.1f exceeds the rated %.1f tons of the assigned power",
				tr.TrailingTons, tr.RatedTons),
		})
	}
	if len(tr.Cars) < spec.MinCars {
		tr.Findings = append(tr.Findings, config.Finding{
			Severity: config.SeverityError,
			Scope:    "train",
			Subject:  tr.TrainID,
			Message:  fmt.Sprintf("train has %d cars, the order requires at least %d", len(tr.Cars), spec.MinCars),
		})
	}
	if track, ok := b.cfg.DepartureByID(spec.TrackID); ok {
		if tr.LengthFt > track.CapacityFt {
			tr.Findings = append(tr.Findings, config.Finding{
				Severity: config.SeverityError,
				Scope:    "train",
				Subject:  tr.TrainID,
				Message:  fmt.Sprintf("consist length %d ft exceeds departure track %s capacity %d ft", tr.LengthFt, track.ID, track.CapacityFt),
			})
		}
		if tr.TrailingTons > track.WeightLimitTons {
			tr.Findings = append(tr.Findings, config.Finding{
				Severity: config.SeverityError,
				Scope:    "train",
				Subject:  tr.TrainID,
				Message:  fmt.Sprintf("consist weight %.1f tons exceeds departure track %s limit %.1f tons", tr.TrailingTons, track.ID, track.WeightLimitTons),
			})
		}
	}
	for _, h := range tr.Held {
		tr.Findings = append(tr.Findings, config.Finding{
			Severity: config.SeverityWarn,
			Scope:    "train",
			Subject:  tr.TrainID,
			Message:  fmt.Sprintf("car %s held: %s (%s)", h.CarID, h.Reason, h.Detail),
		})
	}
	sort.SliceStable(tr.Held, func(i, j int) bool { return tr.Held[i].CarID < tr.Held[j].CarID })
	sortFindings(tr.Findings)
}

// leftovers lists cars that were never forwarded by any train.
func (b *builder) leftovers(trains []Train) []HeldCar {
	var out []HeldCar
	for _, id := range b.index.IDs() {
		if b.consumed[id] {
			continue
		}
		car := b.index[id]
		track := b.occ.FinalTrackOf(id)
		switch {
		case car.BadOrder:
			out = append(out, HeldCar{CarID: id, Track: track, Reason: HeldBadOrder, Detail: car.BadOrderWhy})
			continue
		case track == "":
			out = append(out, HeldCar{CarID: id, Track: "", Reason: HeldNoTrain, Detail: "car was never placed on a track"})
			continue
		}
		block, ok := b.cfg.BlockForDestination(car.Destination)
		if !ok {
			out = append(out, HeldCar{CarID: id, Track: track, Reason: HeldMisrouted, Detail: fmt.Sprintf("destination %s maps to no block", car.Destination)})
			continue
		}
		standing := b.blockOfTrack(track)
		if standing != block {
			out = append(out, HeldCar{CarID: id, Block: block, Track: track, Reason: HeldMisrouted,
				Detail: fmt.Sprintf("standing in block %s but belongs to %s", standing, block)})
			continue
		}
		out = append(out, HeldCar{CarID: id, Block: block, Track: track, Reason: heldReasonFromTrains(trains, id)})
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].CarID < out[j].CarID })
	return out
}

// blockOfTrack returns the block a classification track serves.
func (b *builder) blockOfTrack(trackID string) string {
	if t, ok := b.cfg.ClassTrackByID(trackID); ok {
		return t.Block
	}
	return ""
}

// heldReasonFromTrains finds the ceiling that kept a car off its train, or
// reports that no departure order covers its block.
func heldReasonFromTrains(trains []Train, carID string) string {
	for _, tr := range trains {
		for _, h := range tr.Held {
			if h.CarID == carID {
				return h.Reason
			}
		}
	}
	return HeldNoTrain
}

// digest computes departure statistics.
func digest(plan Plan) Stats {
	st := Stats{Trains: len(plan.Trains)}
	for _, tr := range plan.Trains {
		st.CarsForwarded += len(tr.Cars)
		st.TotalTons += tr.TrailingTons
		st.TotalLengthFt += tr.LengthFt
		if tr.PowerShort {
			st.PowerShort++
		}
		if !tr.Complete {
			st.Incomplete++
		}
	}
	st.CarsHeld = len(plan.Held)
	return st
}

// collectFindings merges per-train findings into one sorted list.
func collectFindings(plan Plan) []config.Finding {
	var out []config.Finding
	for _, tr := range plan.Trains {
		out = append(out, tr.Findings...)
	}
	sortFindings(out)
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

// Placements converts built consists into hazmat placements. A departure train
// always carries an occupied crew position at the head end.
func (p Plan) Placements(index model.CarIndex) []hazmat.Placement {
	out := make([]hazmat.Placement, 0, len(p.Trains))
	for _, tr := range p.Trains {
		if len(tr.Cars) == 0 {
			continue
		}
		cars := make([]model.Car, 0, len(tr.Cars))
		for _, cc := range tr.Cars {
			if c, ok := index[cc.CarID]; ok {
				cars = append(cars, c)
			}
		}
		out = append(out, hazmat.Placement{
			TrackID:     tr.TrainID,
			Kind:        "departure",
			Cars:        cars,
			CrewAtFront: true,
		})
	}
	return out
}

// Manifest renders a train as a deterministic list of manifest lines.
func (tr Train) Manifest() []string {
	lines := make([]string, 0, len(tr.Cars)+1)
	lines = append(lines, fmt.Sprintf("%s power=%s rated=%.0f tons hp=%d", tr.TrainID, joinIDs(tr.Locomotives), tr.RatedTons, tr.Horsepower))
	for _, c := range tr.Cars {
		lines = append(lines, fmt.Sprintf("%3d %-14s %-6s %-6s %4d ft %8.1f t %2d ax %s",
			c.Position, c.CarID, c.Block, c.TrackID, c.LengthFt, c.Tons, c.Axles, hazLabel(c)))
	}
	return lines
}

// hazLabel renders the hazmat marking of a consist car.
func hazLabel(c ConsistCar) string {
	if c.Hazmat == "" {
		return "-"
	}
	if c.Placard {
		return c.Hazmat + " placarded"
	}
	return c.Hazmat + " residue"
}

// joinIDs renders locomotive identifiers as a compact list.
func joinIDs(ids []string) string {
	out := ""
	for i, id := range ids {
		if i > 0 {
			out += "+"
		}
		out += id
	}
	if out == "" {
		return "none"
	}
	return out
}
