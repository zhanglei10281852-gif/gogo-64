// Package pipeline runs the full planning chain in a fixed order and collects
// the result into a single snapshot that can be stored, re-read and reported on
// without recomputation.
package pipeline

import (
	"fmt"
	"sort"

	"HumpYard/internal/blocking"
	"HumpYard/internal/config"
	"HumpYard/internal/depart"
	"HumpYard/internal/hazmat"
	"HumpYard/internal/hump"
	"HumpYard/internal/ingest"
	"HumpYard/internal/model"
	"HumpYard/internal/occupancy"
	"HumpYard/internal/rehandle"
	"HumpYard/internal/shift"
	"HumpYard/internal/store"
)

// SchemaVersion is the snapshot schema version.
const SchemaVersion = 1

// Snapshot is the complete result of a planning run.
type Snapshot struct {
	SchemaVersion int              `json:"schema_version"`
	YardID        string           `json:"yard_id"`
	YardName      string           `json:"yard_name"`
	OrderID       string           `json:"order_id"`
	Source        string           `json:"source"`
	ConfigSHA     string           `json:"config_sha256"`
	OrderSHA      string           `json:"order_sha256"`
	Ingest        ingest.Stats     `json:"ingest"`
	Blocking      blocking.Plan    `json:"blocking"`
	Hump          hump.Plan        `json:"hump"`
	Occupancy     occupancy.Result `json:"occupancy"`
	Hazmat        hazmat.Report    `json:"hazmat"`
	Departures    depart.Plan      `json:"departures"`
	Rehandle      rehandle.Report  `json:"rehandle"`
	Shifts        shift.Plan       `json:"shifts"`
	Findings      []config.Finding `json:"findings"`
}

// Counts is a compact digest of a snapshot used by the report command.
type Counts struct {
	InboundCars   int     `json:"inbound_cars"`
	Humped        int     `json:"humped_cars"`
	FlatSwitched  int     `json:"flat_switched_cars"`
	Forwarded     int     `json:"forwarded_cars"`
	Held          int     `json:"held_cars"`
	RehandlePct   float64 `json:"rehandle_percent"`
	HazmatIssues  int     `json:"hazmat_violations"`
	Errors        int     `json:"errors"`
	Warnings      int     `json:"warnings"`
	CrewTasks     int     `json:"crew_tasks"`
	UnassignedTsk int     `json:"unassigned_tasks"`
}

// Run executes the whole planning chain.
func Run(cfg *config.Config, res *ingest.Result) (*Snapshot, error) {
	order := res.Order
	index, err := model.NewCarIndex(order.AllCars())
	if err != nil {
		return nil, err
	}
	configSHA, err := store.Digest(cfg)
	if err != nil {
		return nil, err
	}
	orderSHA, err := store.Digest(order)
	if err != nil {
		return nil, err
	}
	snap := &Snapshot{
		SchemaVersion: SchemaVersion,
		YardID:        cfg.Yard.ID,
		YardName:      cfg.Yard.Name,
		OrderID:       order.OrderID,
		Source:        res.Source,
		ConfigSHA:     configSHA,
		OrderSHA:      orderSHA,
		Ingest:        res.Stats,
	}
	snap.Blocking = blocking.Build(cfg, order)
	snap.Hump = hump.Build(cfg, order, snap.Blocking)
	occ, err := occupancy.Simulate(cfg, order, snap.Hump)
	if err != nil {
		return nil, err
	}
	snap.Occupancy = occ
	dp, err := depart.Build(cfg, order, occ)
	if err != nil {
		return nil, err
	}
	snap.Departures = dp
	placements := inboundPlacements(order)
	placements = append(placements, occ.Placements(index)...)
	placements = append(placements, dp.Placements(index)...)
	snap.Hazmat = hazmat.Validate(cfg, placements)
	rh, err := rehandle.Analyze(cfg, order, snap.Hump, occ, dp)
	if err != nil {
		return nil, err
	}
	snap.Rehandle = rh
	snap.Shifts = shift.Build(cfg, order, snap.Hump, dp)
	snap.Findings = mergeFindings(res, snap)
	return snap, nil
}

// inboundPlacements renders each arrival as a hazmat placement so that the
// standing order a train arrives in is validated before anything is switched.
func inboundPlacements(order model.YardOrder) []hazmat.Placement {
	out := make([]hazmat.Placement, 0, len(order.Trains))
	for _, t := range order.Trains {
		out = append(out, hazmat.Placement{
			TrackID:       t.ID,
			Kind:          "inbound",
			Cars:          t.Cars,
			CabooseAtRear: t.CabooseAtRear(),
			CrewAtFront:   t.CrewAtHead(),
		})
	}
	return out
}

// mergeFindings collects the findings of every stage into one sorted list.
func mergeFindings(res *ingest.Result, snap *Snapshot) []config.Finding {
	var out []config.Finding
	out = append(out, res.Findings...)
	out = append(out, snap.Blocking.Findings...)
	out = append(out, snap.Hump.Findings...)
	out = append(out, snap.Occupancy.Findings...)
	out = append(out, snap.Hazmat.Findings()...)
	out = append(out, snap.Departures.Findings...)
	out = append(out, snap.Rehandle.Findings...)
	out = append(out, snap.Shifts.Findings...)
	sort.SliceStable(out, func(i, j int) bool {
		a, b := out[i], out[j]
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
	return dedupe(out)
}

// dedupe removes exact duplicate findings while preserving order.
func dedupe(findings []config.Finding) []config.Finding {
	seen := map[string]bool{}
	out := make([]config.Finding, 0, len(findings))
	for _, f := range findings {
		key := f.Severity + "|" + f.Scope + "|" + f.Subject + "|" + f.Message
		if seen[key] {
			continue
		}
		seen[key] = true
		out = append(out, f)
	}
	return out
}

// Digest computes the compact counts of a snapshot.
func (s *Snapshot) Digest() Counts {
	c := Counts{
		InboundCars:   s.Ingest.Cars,
		Humped:        s.Hump.Stats.CarsHumped,
		FlatSwitched:  s.Hump.Stats.CarsFlat,
		Forwarded:     s.Departures.Stats.CarsForwarded,
		Held:          s.Departures.Stats.CarsHeld,
		RehandlePct:   s.Rehandle.RehandlePct,
		HazmatIssues:  len(s.Hazmat.Violations),
		CrewTasks:     s.Shifts.Stats.Assigned,
		UnassignedTsk: s.Shifts.Stats.Unassigned,
	}
	for _, f := range s.Findings {
		if f.Severity == config.SeverityError {
			c.Errors++
			continue
		}
		c.Warnings++
	}
	return c
}

// Ledger renders the snapshot as append-only work ledger entries. Entries are
// ordered by stage and then by the deterministic key of each stage.
func (s *Snapshot) Ledger() []store.LedgerEntry {
	var out []store.LedgerEntry
	for _, m := range s.Hump.Movements {
		out = append(out, store.LedgerEntry{
			Kind:    "movement-" + m.Kind,
			Subject: m.TrainID,
			TrackID: m.TrackID,
			Cars:    len(m.CarIDs),
			Minutes: m.EndMinute - m.StartMinute,
			Detail:  m.Note,
		})
	}
	for _, t := range s.Departures.Trains {
		out = append(out, store.LedgerEntry{
			Kind:    "departure",
			Subject: t.TrainID,
			TrackID: t.TrackID,
			Cars:    len(t.Cars),
			Minutes: 0,
			Detail: fmt.Sprintf("%d ft, %.1f tons, %d axles, hpt %.2f",
				t.LengthFt, t.TrailingTons, t.Axles, t.HPPerTon),
		})
	}
	for _, a := range s.Shifts.Assignments {
		out = append(out, store.LedgerEntry{
			Kind:    "crew-" + a.Kind,
			Subject: a.CrewID,
			TrackID: "",
			Cars:    0,
			Minutes: a.Minutes,
			Detail:  fmt.Sprintf("shift %s task %s", a.ShiftID, a.TaskID),
		})
	}
	for _, it := range s.Rehandle.Items {
		out = append(out, store.LedgerEntry{
			Kind:    "rehandle-" + it.Category,
			Subject: it.CarID,
			TrackID: it.CurrentTrs,
			Cars:    1,
			Minutes: 0,
			Detail:  it.Detail,
		})
	}
	return out
}

// Persist saves the snapshot, appends the ledger and records audit entries.
func Persist(st *store.Store, snap *Snapshot) ([]store.AuditRecord, error) {
	if err := st.SetIdentity(snap.YardID, snap.OrderID); err != nil {
		return nil, err
	}
	if err := st.SaveSnapshot(snap); err != nil {
		return nil, err
	}
	if err := st.AppendLedger(snap.Ledger()); err != nil {
		return nil, err
	}
	var records []store.AuditRecord
	steps := []struct {
		action  string
		subject string
		detail  string
		payload any
	}{
		{"ingest", snap.OrderID, fmt.Sprintf("%d trains, %d cars", snap.Ingest.Trains, snap.Ingest.Cars), snap.Ingest},
		{"block", snap.YardID, fmt.Sprintf("%d assignments", len(snap.Blocking.Assignments)), snap.Blocking.Digest()},
		{"hump", snap.YardID, fmt.Sprintf("%d cuts, %d flat moves", len(snap.Hump.Cuts), len(snap.Hump.FlatMoves)), snap.Hump.Stats},
		{"occupancy", snap.YardID, fmt.Sprintf("%d cars placed", snap.Occupancy.Stats.CarsPlaced), snap.Occupancy.Stats},
		{"hazmat", snap.YardID, fmt.Sprintf("%d violations", len(snap.Hazmat.Violations)), snap.Hazmat.Tallies},
		{"build", snap.YardID, fmt.Sprintf("%d trains", len(snap.Departures.Trains)), snap.Departures.Stats},
		{"rehandle", snap.YardID, fmt.Sprintf("%.2f percent", snap.Rehandle.RehandlePct), snap.Rehandle.Counts},
		{"plan", snap.YardID, fmt.Sprintf("%d tasks assigned", snap.Shifts.Stats.Assigned), snap.Shifts.Stats},
	}
	for _, step := range steps {
		rec, err := st.Append(step.action, step.subject, step.detail, step.payload)
		if err != nil {
			return nil, err
		}
		records = append(records, rec)
	}
	return records, nil
}
