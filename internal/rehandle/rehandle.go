// Package rehandle derives rework: cars that ended up in the wrong block, cars
// that need a second pass over the hump, and cars nothing could forward. The
// derived rehandle percentage is the yard's headline quality figure.
package rehandle

import (
	"fmt"
	"sort"

	"HumpYard/internal/config"
	"HumpYard/internal/depart"
	"HumpYard/internal/hump"
	"HumpYard/internal/model"
	"HumpYard/internal/occupancy"
)

// Rework categories.
const (
	// CatMisroute marks a car standing in a block it does not belong to.
	CatMisroute = "misroute"
	// CatSecondPass marks a car that must go over the hump again.
	CatSecondPass = "second-hump-pass"
	// CatUnplaced marks a car that never came to rest on a track.
	CatUnplaced = "unplaced"
	// CatRepair marks a bad ordered car holding on the repair track.
	CatRepair = "repair-hold"
	// CatCapacity marks a car left behind by a train ceiling.
	CatCapacity = "capacity-hold"
	// CatUnblocked marks a car whose destination maps to no block.
	CatUnblocked = "unblocked"
)

// Item is one rework finding for a single car.
type Item struct {
	CarID       string `json:"car_id"`
	Category    string `json:"category"`
	CurrentTrs  string `json:"current_track"`
	WantBlock   string `json:"want_block"`
	ActualBlock string `json:"actual_block"`
	SecondPass  bool   `json:"second_pass"`
	Detail      string `json:"detail"`
}

// CategoryCount is a per-category tally.
type CategoryCount struct {
	Category string `json:"category"`
	Cars     int    `json:"cars"`
}

// Report is the whole rework picture.
type Report struct {
	TotalCars     int              `json:"total_cars"`
	RehandleCars  int              `json:"rehandle_cars"`
	SecondPass    int              `json:"second_pass_cars"`
	RehandlePct   float64          `json:"rehandle_percent"`
	SecondPassPct float64          `json:"second_pass_percent"`
	Items         []Item           `json:"items"`
	Counts        []CategoryCount  `json:"counts"`
	Findings      []config.Finding `json:"findings"`
}

// Analyze derives the rework report from the completed plan stages.
func Analyze(cfg *config.Config, order model.YardOrder, hp hump.Plan, occ occupancy.Result, dp depart.Plan) (Report, error) {
	index, err := model.NewCarIndex(order.AllCars())
	if err != nil {
		return Report{}, err
	}
	rep := Report{TotalCars: len(index)}
	items := map[string]Item{}
	forwarded := forwardedSet(dp)
	for _, id := range index.IDs() {
		car := index[id]
		track := occ.FinalTrackOf(id)
		want, blocked := cfg.BlockForDestination(car.Destination)
		actual := blockOfTrack(cfg, track)
		switch {
		case car.BadOrder:
			items[id] = Item{CarID: id, Category: CatRepair, CurrentTrs: track, WantBlock: want,
				ActualBlock: actual, Detail: "bad order: " + car.BadOrderWhy}
		case track == "":
			items[id] = Item{CarID: id, Category: CatUnplaced, WantBlock: want, SecondPass: true,
				Detail: "no track accepted the car"}
		case !blocked:
			items[id] = Item{CarID: id, Category: CatUnblocked, CurrentTrs: track, ActualBlock: actual,
				SecondPass: true, Detail: fmt.Sprintf("destination %s maps to no block", car.Destination)}
		case actual != want:
			items[id] = Item{CarID: id, Category: CatMisroute, CurrentTrs: track, WantBlock: want,
				ActualBlock: actual, SecondPass: true,
				Detail: fmt.Sprintf("standing on %s serving block %s, belongs to block %s", track, actual, want)}
		case !forwarded[id]:
			items[id] = Item{CarID: id, Category: CatCapacity, CurrentTrs: track, WantBlock: want,
				ActualBlock: actual, Detail: heldDetail(dp, id)}
		}
	}
	for _, id := range secondPassFromCrest(hp, occ) {
		if _, exists := items[id]; exists {
			item := items[id]
			item.SecondPass = true
			items[id] = item
			continue
		}
		car := index[id]
		want, _ := cfg.BlockForDestination(car.Destination)
		track := occ.FinalTrackOf(id)
		items[id] = Item{CarID: id, Category: CatSecondPass, CurrentTrs: track, WantBlock: want,
			ActualBlock: blockOfTrack(cfg, track), SecondPass: true,
			Detail: "diverted to the overflow track at the crest"}
	}
	rep.Items = sortedItems(items)
	rep.Counts = tally(rep.Items)
	for _, it := range rep.Items {
		if it.Category != CatRepair {
			rep.RehandleCars++
		}
		if it.SecondPass {
			rep.SecondPass++
		}
	}
	if rep.TotalCars > 0 {
		rep.RehandlePct = float64(rep.RehandleCars) * 100 / float64(rep.TotalCars)
		rep.SecondPassPct = float64(rep.SecondPass) * 100 / float64(rep.TotalCars)
	}
	rep.Findings = findings(rep)
	return rep, nil
}

// forwardedSet lists every car that rode an outbound train.
func forwardedSet(dp depart.Plan) map[string]bool {
	out := map[string]bool{}
	for _, tr := range dp.Trains {
		for _, c := range tr.Cars {
			out[c.CarID] = true
		}
	}
	return out
}

// heldDetail explains why a car was not forwarded.
func heldDetail(dp depart.Plan, carID string) string {
	for _, h := range dp.Held {
		if h.CarID == carID {
			if h.Detail != "" {
				return h.Reason + ": " + h.Detail
			}
			return h.Reason
		}
	}
	for _, tr := range dp.Trains {
		for _, h := range tr.Held {
			if h.CarID == carID {
				return fmt.Sprintf("train %s held the car: %s", tr.TrainID, h.Reason)
			}
		}
	}
	return "no departure order pulled the car"
}

// secondPassFromCrest lists cars the crest simulation diverted or refused.
func secondPassFromCrest(hp hump.Plan, occ occupancy.Result) []string {
	seen := map[string]bool{}
	for _, e := range occ.Events {
		if e.Action == occupancy.ActionOverflow || e.Action == occupancy.ActionRefused {
			seen[e.CarID] = true
		}
	}
	for _, f := range hp.FlatMoves {
		if f.Reason != hump.ReasonNoTrack && f.Reason != hump.ReasonFlatTrack {
			continue
		}
		for _, id := range f.CarIDs {
			seen[id] = true
		}
	}
	out := make([]string, 0, len(seen))
	for id := range seen {
		out = append(out, id)
	}
	sort.Strings(out)
	return out
}

// blockOfTrack returns the block a classification track serves.
func blockOfTrack(cfg *config.Config, trackID string) string {
	if trackID == "" {
		return ""
	}
	if t, ok := cfg.ClassTrackByID(trackID); ok {
		return t.Block
	}
	return ""
}

// sortedItems renders the item map in car identifier order.
func sortedItems(items map[string]Item) []Item {
	ids := make([]string, 0, len(items))
	for id := range items {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	out := make([]Item, 0, len(ids))
	for _, id := range ids {
		out = append(out, items[id])
	}
	return out
}

// tally counts items per category in category order.
func tally(items []Item) []CategoryCount {
	counts := map[string]int{}
	for _, it := range items {
		counts[it.Category]++
	}
	cats := make([]string, 0, len(counts))
	for c := range counts {
		cats = append(cats, c)
	}
	sort.Strings(cats)
	out := make([]CategoryCount, 0, len(cats))
	for _, c := range cats {
		out = append(out, CategoryCount{Category: c, Cars: counts[c]})
	}
	return out
}

// findings raises advisories about the rework level.
func findings(rep Report) []config.Finding {
	var out []config.Finding
	for _, it := range rep.Items {
		if it.Category != CatMisroute {
			continue
		}
		out = append(out, config.Finding{
			Severity: config.SeverityError,
			Scope:    "rehandle",
			Subject:  it.CarID,
			Message:  it.Detail,
		})
	}
	if rep.RehandlePct > 15 {
		out = append(out, config.Finding{
			Severity: config.SeverityWarn,
			Scope:    "rehandle",
			Subject:  "yard",
			Message:  fmt.Sprintf("rehandle rate %.1f%% is above the 15%% review threshold", rep.RehandlePct),
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

// ItemFor returns the rework item recorded for a car.
func (r Report) ItemFor(carID string) (Item, bool) {
	for _, it := range r.Items {
		if it.CarID == carID {
			return it, true
		}
	}
	return Item{}, false
}

// SecondPassCars lists the cars needing another trip over the crest.
func (r Report) SecondPassCars() []string {
	var out []string
	for _, it := range r.Items {
		if it.SecondPass {
			out = append(out, it.CarID)
		}
	}
	return out
}
