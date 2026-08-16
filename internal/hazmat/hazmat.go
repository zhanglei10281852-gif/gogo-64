// Package hazmat validates hazardous material placement in a standing car
// order. Two kinds of rule are enforced: buffer-car spacing between
// incompatible classes, and prohibition of certain classes near an occupied
// caboose or crew position. A per-track placard ceiling is checked as well.
package hazmat

import (
	"fmt"
	"sort"

	"HumpYard/internal/config"
	"HumpYard/internal/model"
)

// Rule identifiers reported in violations.
const (
	RuleBuffer  = "buffer-spacing"
	RuleCaboose = "caboose-adjacency"
	RulePlacard = "placard-limit"
	RuleUnknown = "undeclared-class"
)

// Placement is the standing order of one track or train. Cars are listed from
// the leading end to the trailing end. When CabooseAtRear is set the trailing
// end holds an occupied caboose position; when CrewAtFront is set the leading
// end holds an occupied crew position such as a manned locomotive cab.
type Placement struct {
	TrackID       string      `json:"track_id"`
	Kind          string      `json:"kind"`
	Cars          []model.Car `json:"-"`
	CabooseAtRear bool        `json:"caboose_at_rear"`
	CrewAtFront   bool        `json:"crew_at_front"`
}

// Violation is one broken hazmat rule.
type Violation struct {
	TrackID  string `json:"track_id"`
	Rule     string `json:"rule"`
	CarA     string `json:"car_a"`
	CarB     string `json:"car_b"`
	ClassA   string `json:"class_a"`
	ClassB   string `json:"class_b"`
	Actual   int    `json:"actual_buffer"`
	Required int    `json:"required_buffer"`
	Message  string `json:"message"`
}

// TrackTally counts hazmat exposure on one track.
type TrackTally struct {
	TrackID    string `json:"track_id"`
	HazmatCars int    `json:"hazmat_cars"`
	Placards   int    `json:"placards"`
	Limit      int    `json:"placard_limit"`
}

// Report is the hazmat validation result.
type Report struct {
	Checked    int          `json:"placements_checked"`
	Cars       int          `json:"cars_checked"`
	Violations []Violation  `json:"violations"`
	Tallies    []TrackTally `json:"tallies"`
}

// OK reports whether the placement satisfies every hazmat rule.
func (r Report) OK() bool {
	return len(r.Violations) == 0
}

// Findings converts violations into configuration findings for uniform output.
func (r Report) Findings() []config.Finding {
	out := make([]config.Finding, 0, len(r.Violations))
	for _, v := range r.Violations {
		out = append(out, config.Finding{
			Severity: config.SeverityError,
			Scope:    "hazmat",
			Subject:  v.TrackID,
			Message:  v.Message,
		})
	}
	return out
}

// Validate checks every placement against the configured hazmat regime.
func Validate(cfg *config.Config, placements []Placement) Report {
	rep := Report{Checked: len(placements)}
	declared := map[string]bool{}
	for _, cl := range cfg.Hazmat.Classes {
		declared[cl] = true
	}
	for _, p := range placements {
		rep.Cars += len(p.Cars)
		rep.Violations = append(rep.Violations, checkBuffers(cfg, p)...)
		rep.Violations = append(rep.Violations, checkCaboose(cfg, p)...)
		rep.Violations = append(rep.Violations, checkCrewFront(cfg, p)...)
		rep.Violations = append(rep.Violations, checkPlacards(cfg, p)...)
		rep.Violations = append(rep.Violations, checkDeclared(declared, p)...)
		rep.Tallies = append(rep.Tallies, tally(cfg, p))
	}
	sortViolations(rep.Violations)
	sort.SliceStable(rep.Tallies, func(i, j int) bool { return rep.Tallies[i].TrackID < rep.Tallies[j].TrackID })
	return rep
}

// checkBuffers enforces buffer-car spacing between incompatible classes.
func checkBuffers(cfg *config.Config, p Placement) []Violation {
	var out []Violation
	for i := 0; i < len(p.Cars); i++ {
		a := p.Cars[i]
		if !a.Hazmat() {
			continue
		}
		for j := i + 1; j < len(p.Cars); j++ {
			b := p.Cars[j]
			if !b.Hazmat() {
				continue
			}
			need, regulated := cfg.Hazmat.BufferFor(a.HazmatClass, b.HazmatClass)
			if !regulated {
				continue
			}
			between := j - i - 1
			if between >= need {
				// Spacing satisfied for this pair; keep scanning
				// because a car further back may belong to a class
				// with a larger requirement.
				continue
			}
			out = append(out, Violation{
				TrackID:  p.TrackID,
				Rule:     RuleBuffer,
				CarA:     a.ID(),
				CarB:     b.ID(),
				ClassA:   a.HazmatClass,
				ClassB:   b.HazmatClass,
				Actual:   between,
				Required: need,
				Message: fmt.Sprintf("%s (%s) and %s (%s) are separated by %d cars, %d required",
					a.ID(), a.HazmatClass, b.ID(), b.HazmatClass, between, need),
			})
		}
	}
	return out
}

// checkCaboose enforces the prohibition of certain classes near an occupied
// caboose or crew position at the trailing end of the placement.
func checkCaboose(cfg *config.Config, p Placement) []Violation {
	if !p.CabooseAtRear {
		return nil
	}
	buffer := cfg.Hazmat.CabooseBufferCars
	var out []Violation
	for i, c := range p.Cars {
		if !c.Hazmat() || !cfg.Hazmat.CabooseBarred(c.HazmatClass) {
			continue
		}
		distance := len(p.Cars) - 1 - i
		if distance >= buffer {
			continue
		}
		out = append(out, Violation{
			TrackID:  p.TrackID,
			Rule:     RuleCaboose,
			CarA:     c.ID(),
			CarB:     "caboose",
			ClassA:   c.HazmatClass,
			Actual:   distance,
			Required: buffer,
			Message: fmt.Sprintf("%s (%s) stands %d cars from the occupied caboose position, %d required",
				c.ID(), c.HazmatClass, distance, buffer),
		})
	}
	return out
}

// checkCrewFront enforces the prohibition of certain classes near an occupied
// crew position at the leading end of the placement.
func checkCrewFront(cfg *config.Config, p Placement) []Violation {
	if !p.CrewAtFront {
		return nil
	}
	buffer := cfg.Hazmat.CabooseBufferCars
	var out []Violation
	for i, c := range p.Cars {
		if !c.Hazmat() || !cfg.Hazmat.CabooseBarred(c.HazmatClass) {
			continue
		}
		if i >= buffer {
			continue
		}
		out = append(out, Violation{
			TrackID:  p.TrackID,
			Rule:     RuleCaboose,
			CarA:     c.ID(),
			CarB:     "crew-position",
			ClassA:   c.HazmatClass,
			Actual:   i,
			Required: buffer,
			Message: fmt.Sprintf("%s (%s) stands %d cars from the occupied crew position, %d required",
				c.ID(), c.HazmatClass, i, buffer),
		})
	}
	return out
}

// checkPlacards enforces the per-track placard ceiling.
func checkPlacards(cfg *config.Config, p Placement) []Violation {
	count := 0
	for _, c := range p.Cars {
		if c.Placard {
			count++
		}
	}
	if count <= cfg.Hazmat.MaxPlacardsPerTrack {
		return nil
	}
	return []Violation{{
		TrackID:  p.TrackID,
		Rule:     RulePlacard,
		Actual:   count,
		Required: cfg.Hazmat.MaxPlacardsPerTrack,
		Message: fmt.Sprintf("%d placarded cars on %s exceed the ceiling of %d",
			count, p.TrackID, cfg.Hazmat.MaxPlacardsPerTrack),
	}}
}

// checkDeclared reports cars whose class is not part of the configured regime.
func checkDeclared(declared map[string]bool, p Placement) []Violation {
	var out []Violation
	for _, c := range p.Cars {
		if !c.Hazmat() || declared[c.HazmatClass] {
			continue
		}
		out = append(out, Violation{
			TrackID: p.TrackID,
			Rule:    RuleUnknown,
			CarA:    c.ID(),
			ClassA:  c.HazmatClass,
			Message: fmt.Sprintf("%s carries undeclared hazmat class %s", c.ID(), c.HazmatClass),
		})
	}
	return out
}

// tally counts hazmat exposure for one placement.
func tally(cfg *config.Config, p Placement) TrackTally {
	t := TrackTally{TrackID: p.TrackID, Limit: cfg.Hazmat.MaxPlacardsPerTrack}
	for _, c := range p.Cars {
		if c.Hazmat() {
			t.HazmatCars++
		}
		if c.Placard {
			t.Placards++
		}
	}
	return t
}

// sortViolations orders violations deterministically.
func sortViolations(vs []Violation) {
	sort.SliceStable(vs, func(i, j int) bool {
		a, b := vs[i], vs[j]
		if a.TrackID != b.TrackID {
			return a.TrackID < b.TrackID
		}
		if a.Rule != b.Rule {
			return a.Rule < b.Rule
		}
		if a.CarA != b.CarA {
			return a.CarA < b.CarA
		}
		return a.CarB < b.CarB
	})
}

// MinBufferNeeded returns the largest buffer requirement among the declared
// incompatible pairs, which is useful when sizing a cushion of buffer cars.
func MinBufferNeeded(cfg *config.Config) int {
	max := 0
	for _, p := range cfg.Hazmat.IncompatiblePairs {
		if p.BufferCars > max {
			max = p.BufferCars
		}
	}
	return max
}

// SeparationCandidates lists, for a placement, the pairs of adjacent cars where
// inserting a buffer car would fix a spacing violation. The result is ordered
// by insertion point so callers can act on it deterministically.
func SeparationCandidates(cfg *config.Config, p Placement) []int {
	seen := map[int]bool{}
	for _, v := range checkBuffers(cfg, p) {
		for i, c := range p.Cars {
			if c.ID() == v.CarA {
				seen[i+1] = true
				break
			}
		}
	}
	out := make([]int, 0, len(seen))
	for i := range seen {
		out = append(out, i)
	}
	sort.Ints(out)
	return out
}
