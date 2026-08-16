// Package ingest reads yard orders. A yard order is either a single strict
// JSON document describing all arrivals, or a JSONL stream with one inbound
// train per line. Both forms are validated against the configuration before
// any planning happens.
package ingest

import (
	"fmt"
	"path/filepath"
	"sort"
	"strings"

	"HumpYard/internal/config"
	"HumpYard/internal/jsonx"
	"HumpYard/internal/model"
)

// MaxOrderBytes bounds the yard order document size.
const MaxOrderBytes = 32 << 20

// Stats summarizes an ingested yard order.
type Stats struct {
	Trains              int      `json:"trains"`
	Cars                int      `json:"cars"`
	HazmatCars          int      `json:"hazmat_cars"`
	PlacardedCars       int      `json:"placarded_cars"`
	BadOrderCars        int      `json:"bad_order_cars"`
	FlatOnlyCars        int      `json:"flat_only_cars"`
	RoughRiders         int      `json:"rough_riders"`
	EasyRollers         int      `json:"easy_rollers"`
	DrawbarPairs        int      `json:"drawbar_pairs"`
	TotalLengthFt       int      `json:"total_length_ft"`
	TotalTons           float64  `json:"total_tons"`
	TotalAxles          int      `json:"total_axles"`
	UnknownDestinations []string `json:"unknown_destinations"`
}

// Result is the outcome of ingesting a yard order.
type Result struct {
	Source   string           `json:"source"`
	Order    model.YardOrder  `json:"order"`
	Stats    Stats            `json:"stats"`
	Findings []config.Finding `json:"findings"`
}

// Load reads a yard order from disk. The format is chosen by file extension:
// ".jsonl" is treated as a train-per-line stream, anything else as a single
// JSON document.
func Load(path string, cfg *config.Config) (*Result, error) {
	data, err := config.ReadLimited(path, MaxOrderBytes)
	if err != nil {
		return nil, err
	}
	ext := strings.ToLower(filepath.Ext(path))
	var order model.YardOrder
	switch ext {
	case ".jsonl", ".ndjson":
		order, err = parseJSONL(data)
	default:
		order, err = parseJSON(data)
	}
	if err != nil {
		return nil, fmt.Errorf("%s: %w", filepath.Base(path), err)
	}
	res := &Result{Source: filepath.Base(path), Order: order}
	res.Stats = Summarize(cfg, order)
	res.Findings = CrossCheck(cfg, order)
	return res, nil
}

// parseJSON decodes a whole-document yard order.
func parseJSON(data []byte) (model.YardOrder, error) {
	var order model.YardOrder
	if err := jsonx.DecodeStrict(data, &order); err != nil {
		return order, err
	}
	order.Normalize()
	if err := order.Validate(); err != nil {
		return order, err
	}
	return order, nil
}

// jsonlHeader is the optional first record of a JSONL stream. It carries the
// order envelope that the whole-document form keeps at the top level.
type jsonlHeader struct {
	Record  string `json:"record"`
	OrderID string `json:"order_id"`
	YardID  string `json:"yard_id"`
}

// jsonlTrain is a JSONL train record. The record tag is required so a stream
// can never be mistaken for a header.
type jsonlTrain struct {
	Record        string      `json:"record"`
	ID            string      `json:"id"`
	ArrivalMinute int         `json:"arrival_minute"`
	ReceivingID   string      `json:"receiving_track"`
	Inspected     bool        `json:"inspected"`
	CabooseSpot   int         `json:"caboose_position"`
	Cars          []model.Car `json:"cars"`
}

// train converts a JSONL record into the domain train type.
func (t jsonlTrain) train() model.InboundTrain {
	return model.InboundTrain{
		ID:            t.ID,
		ArrivalMinute: t.ArrivalMinute,
		ReceivingID:   t.ReceivingID,
		Inspected:     t.Inspected,
		CabooseSpot:   t.CabooseSpot,
		Cars:          t.Cars,
	}
}

// parseJSONL decodes a train-per-line yard order stream.
func parseJSONL(data []byte) (model.YardOrder, error) {
	var order model.YardOrder
	records, err := jsonx.SplitJSONL(data)
	if err != nil {
		return order, err
	}
	if len(records) == 0 {
		return order, fmt.Errorf("yard order stream is empty")
	}
	for i, rec := range records {
		kind, err := recordKind(rec)
		if err != nil {
			return order, err
		}
		switch kind {
		case "order":
			if i != 0 {
				return order, fmt.Errorf("line %d: order header must be the first record", rec.Line)
			}
			var head jsonlHeader
			if err := jsonx.DecodeRecord(rec, &head); err != nil {
				return order, err
			}
			order.OrderID = head.OrderID
			order.YardID = head.YardID
		case "train":
			var tr jsonlTrain
			if err := jsonx.DecodeRecord(rec, &tr); err != nil {
				return order, err
			}
			order.Trains = append(order.Trains, tr.train())
		default:
			return order, fmt.Errorf("line %d: unknown record kind %q", rec.Line, kind)
		}
	}
	order.Normalize()
	if err := order.Validate(); err != nil {
		return order, err
	}
	return order, nil
}

// recordKind peeks at the record tag of a JSONL line.
func recordKind(rec jsonx.LineRecord) (string, error) {
	var probe struct {
		Record string `json:"record"`
	}
	if err := jsonx.DecodeLoose(rec.Raw, &probe); err != nil {
		return "", fmt.Errorf("line %d: %w", rec.Line, err)
	}
	if probe.Record == "" {
		return "", fmt.Errorf("line %d: record tag is required", rec.Line)
	}
	return strings.ToLower(strings.TrimSpace(probe.Record)), nil
}

// Summarize computes aggregate statistics for a yard order.
func Summarize(cfg *config.Config, order model.YardOrder) Stats {
	st := Stats{Trains: len(order.Trains)}
	unknown := map[string]bool{}
	drawbars := 0
	for _, t := range order.Trains {
		for _, c := range t.Cars {
			st.Cars++
			st.TotalLengthFt += c.LengthFt
			st.TotalTons += c.GrossTons
			st.TotalAxles += c.Axles
			if c.Hazmat() {
				st.HazmatCars++
			}
			if c.Placard {
				st.PlacardedCars++
			}
			if c.BadOrder {
				st.BadOrderCars++
			}
			if c.Flat() {
				st.FlatOnlyCars++
			}
			if c.RoughRider {
				st.RoughRiders++
			}
			if c.EasyRoller {
				st.EasyRollers++
			}
			if c.DrawbarMate != "" {
				drawbars++
			}
			if _, ok := cfg.BlockForDestination(c.Destination); !ok {
				unknown[c.Destination] = true
			}
		}
	}
	st.DrawbarPairs = drawbars / 2
	st.UnknownDestinations = sortedSet(unknown)
	return st
}

// CrossCheck validates an order against the configuration: receiving tracks
// must exist and hold the arrival, destinations should be blockable, and
// hazmat classes should be declared.
func CrossCheck(cfg *config.Config, order model.YardOrder) []config.Finding {
	var findings []config.Finding
	add := func(sev, scope, subject, format string, args ...any) {
		findings = append(findings, config.Finding{
			Severity: sev,
			Scope:    scope,
			Subject:  subject,
			Message:  fmt.Sprintf(format, args...),
		})
	}
	if cfg.Yard.ID != "" && order.YardID != "" && cfg.Yard.ID != order.YardID {
		add(config.SeverityError, "order", order.OrderID, "yard_id %q does not match configured yard %q", order.YardID, cfg.Yard.ID)
	}
	declared := map[string]bool{}
	for _, cl := range cfg.Hazmat.Classes {
		declared[cl] = true
	}
	trackLoad := map[string]int{}
	trackTons := map[string]float64{}
	for _, t := range order.Trains {
		rt, ok := cfg.ReceivingByID(t.ReceivingID)
		if !ok {
			add(config.SeverityError, "train", t.ID, "receiving_track %q is not defined", t.ReceivingID)
			continue
		}
		trackLoad[rt.ID] += model.TotalLengthFt(t.Cars) + cfg.Yard.CouplerSlackFt*len(t.Cars)
		trackTons[rt.ID] += model.TotalTons(t.Cars)
		if !t.Inspected {
			add(config.SeverityWarn, "train", t.ID, "arrival is not inspected; cars must be inspected before humping")
		}
		for _, c := range t.Cars {
			if c.Hazmat() && !declared[c.HazmatClass] {
				add(config.SeverityError, "car", c.ID(), "hazmat_class %q is not declared in hazmat_rules.classes", c.HazmatClass)
			}
			if _, ok := cfg.BlockForDestination(c.Destination); !ok {
				add(config.SeverityWarn, "car", c.ID(), "destination %q has no block; car will be reworked", c.Destination)
			}
			if c.LengthFt > cfg.Hump.MaxCarLengthFt && !c.Flat() {
				add(config.SeverityWarn, "car", c.ID(), "length %d ft exceeds hump limit %d ft; car will be flat switched", c.LengthFt, cfg.Hump.MaxCarLengthFt)
			}
		}
	}
	for _, id := range jsonx.SortedKeys(trackLoad) {
		rt, ok := cfg.ReceivingByID(id)
		if !ok {
			continue
		}
		if trackLoad[id] > rt.CapacityFt {
			add(config.SeverityError, "receiving_track", id, "standing load %d ft exceeds capacity %d ft", trackLoad[id], rt.CapacityFt)
		}
		if trackTons[id] > rt.WeightLimitTons {
			add(config.SeverityError, "receiving_track", id, "standing weight %.1f tons exceeds limit %.1f tons", trackTons[id], rt.WeightLimitTons)
		}
	}
	sortFindings(findings)
	return findings
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

// sortedSet renders a set as a sorted slice.
func sortedSet(set map[string]bool) []string {
	out := make([]string, 0, len(set))
	for k := range set {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// HasErrors reports whether any finding is an error.
func HasErrors(findings []config.Finding) bool {
	for _, f := range findings {
		if f.Severity == config.SeverityError {
			return true
		}
	}
	return false
}
