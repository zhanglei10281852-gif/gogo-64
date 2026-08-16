// Package report renders planning results as deterministic text. Every table
// has fixed column widths, every list is pre-sorted by the producing package,
// and no wall-clock time or random value ever appears in the output.
package report

import (
	"fmt"
	"io"
	"strings"

	"HumpYard/internal/blocking"
	"HumpYard/internal/config"
	"HumpYard/internal/depart"
	"HumpYard/internal/hazmat"
	"HumpYard/internal/hump"
	"HumpYard/internal/ingest"
	"HumpYard/internal/jsonx"
	"HumpYard/internal/occupancy"
	"HumpYard/internal/pipeline"
	"HumpYard/internal/rehandle"
	"HumpYard/internal/shift"
	"HumpYard/internal/store"
)

// ruleWidth is the width of section rules in text output.
const ruleWidth = 78

// JSON writes v as indented JSON.
func JSON(w io.Writer, v any) error {
	data, err := jsonx.MarshalIndent(v)
	if err != nil {
		return err
	}
	_, err = w.Write(data)
	return err
}

// section writes a titled section header.
func section(b *strings.Builder, title string) {
	b.WriteString(title)
	b.WriteString("\n")
	b.WriteString(strings.Repeat("-", ruleWidth))
	b.WriteString("\n")
}

// kv writes an aligned key/value line.
func kv(b *strings.Builder, key string, format string, args ...any) {
	fmt.Fprintf(b, "  %-26s %s\n", key, fmt.Sprintf(format, args...))
}

// Minutes renders a minute offset as a d+hh:mm clock reading.
func Minutes(m int) string {
	day := m / 1440
	rest := m % 1440
	return fmt.Sprintf("%d+%02d:%02d", day, rest/60, rest%60)
}

// Findings renders a finding list as text.
func Findings(findings []config.Finding) string {
	var b strings.Builder
	if len(findings) == 0 {
		return "  no findings\n"
	}
	for _, f := range findings {
		fmt.Fprintf(&b, "  %-7s %-16s %-22s %s\n", f.Severity, f.Scope, truncate(f.Subject, 22), f.Message)
	}
	return b.String()
}

// Validation renders a configuration validation report.
func Validation(cfg *config.Config, rep config.Report) string {
	var b strings.Builder
	section(&b, "CONFIGURATION")
	kv(&b, "yard", "%s (%s)", cfg.Yard.Name, cfg.Yard.ID)
	kv(&b, "blocks", "%d", len(cfg.Blocks))
	kv(&b, "classification tracks", "%d", len(cfg.Class))
	kv(&b, "receiving tracks", "%d", len(cfg.Receiving))
	kv(&b, "departure tracks", "%d", len(cfg.Departure))
	kv(&b, "locomotives", "%d", len(cfg.Power))
	kv(&b, "crews", "%d", len(cfg.Crews))
	kv(&b, "shifts", "%d", len(cfg.Shifts))
	kv(&b, "departure orders", "%d", len(cfg.Departures))
	kv(&b, "hazmat classes", "%s", strings.Join(cfg.Hazmat.Classes, " "))
	kv(&b, "incompatible pairs", "%d", len(cfg.Hazmat.IncompatiblePairs))
	b.WriteString("\n")
	section(&b, "FINDINGS")
	b.WriteString(Findings(rep.Findings))
	return b.String()
}

// Ingested renders an ingest result.
func Ingested(res *ingest.Result) string {
	var b strings.Builder
	section(&b, "YARD ORDER "+res.Order.OrderID)
	kv(&b, "source", "%s", res.Source)
	kv(&b, "yard", "%s", res.Order.YardID)
	kv(&b, "trains", "%d", res.Stats.Trains)
	kv(&b, "cars", "%d", res.Stats.Cars)
	kv(&b, "total length", "%d ft", res.Stats.TotalLengthFt)
	kv(&b, "total weight", "%.1f tons", res.Stats.TotalTons)
	kv(&b, "total axles", "%d", res.Stats.TotalAxles)
	kv(&b, "hazmat cars", "%d (%d placarded)", res.Stats.HazmatCars, res.Stats.PlacardedCars)
	kv(&b, "bad order cars", "%d", res.Stats.BadOrderCars)
	kv(&b, "flat-only cars", "%d", res.Stats.FlatOnlyCars)
	kv(&b, "rough riders", "%d", res.Stats.RoughRiders)
	kv(&b, "easy rollers", "%d", res.Stats.EasyRollers)
	kv(&b, "drawbar pairs", "%d", res.Stats.DrawbarPairs)
	if len(res.Stats.UnknownDestinations) > 0 {
		kv(&b, "unknown destinations", "%s", strings.Join(res.Stats.UnknownDestinations, " "))
	}
	b.WriteString("\n")
	section(&b, "TRAINS")
	fmt.Fprintf(&b, "  %-10s %-8s %-10s %5s %8s %10s\n", "TRAIN", "ARRIVE", "TRACK", "CARS", "LENGTH", "TONS")
	for _, t := range res.Order.Trains {
		length := 0
		tons := 0.0
		for _, c := range t.Cars {
			length += c.LengthFt
			tons += c.GrossTons
		}
		fmt.Fprintf(&b, "  %-10s %-8s %-10s %5d %8d %10.1f\n", t.ID, Minutes(t.ArrivalMinute), t.ReceivingID, len(t.Cars), length, tons)
	}
	b.WriteString("\n")
	section(&b, "FINDINGS")
	b.WriteString(Findings(res.Findings))
	return b.String()
}

// Blocking renders a blocking plan.
func Blocking(plan blocking.Plan) string {
	var b strings.Builder
	d := plan.Digest()
	section(&b, "BLOCKING SUMMARY")
	kv(&b, "cars", "%d", d.Cars)
	kv(&b, "assigned", "%d", d.Assigned)
	kv(&b, "overflow", "%d", d.Overflow)
	kv(&b, "repair", "%d", d.Repair)
	kv(&b, "unblocked", "%d", d.Unblocked)
	kv(&b, "rejected", "%d", d.Rejected)
	kv(&b, "tracks used", "%d of %d", d.TracksUsed, d.Tracks)
	kv(&b, "bowl fill", "%.1f%%", d.FillPercent)
	b.WriteString("\n")
	section(&b, "TRACK LOADING")
	fmt.Fprintf(&b, "  %-10s %-8s %5s %8s %8s %10s %10s\n", "TRACK", "BLOCK", "CARS", "USED FT", "FREE FT", "USED TONS", "FREE TONS")
	for _, t := range plan.Tracks {
		fmt.Fprintf(&b, "  %-10s %-8s %5d %8d %8d %10.1f %10.1f\n",
			t.TrackID, t.Block, len(t.CarIDs), t.UsedFt, t.RemainingFt, t.UsedTons, t.RemainingTons)
	}
	b.WriteString("\n")
	section(&b, "ASSIGNMENTS")
	fmt.Fprintf(&b, "  %-14s %-8s %-6s %-6s %-10s %-10s %s\n", "CAR", "TRAIN", "DEST", "BLOCK", "TRACK", "STATUS", "REASON")
	for _, a := range plan.Assignments {
		fmt.Fprintf(&b, "  %-14s %-8s %-6s %-6s %-10s %-10s %s\n",
			a.CarID, a.TrainID, a.Destination, dash(a.Block), dash(a.TrackID), a.Status, a.Reason)
	}
	b.WriteString("\n")
	section(&b, "FINDINGS")
	b.WriteString(Findings(plan.Findings))
	return b.String()
}

// Hump renders a crest plan.
func Hump(plan hump.Plan) string {
	var b strings.Builder
	section(&b, "HUMP SEQUENCE")
	kv(&b, "cuts", "%d", plan.Stats.Cuts)
	kv(&b, "cars humped", "%d", plan.Stats.CarsHumped)
	kv(&b, "cars flat switched", "%d", plan.Stats.CarsFlat)
	kv(&b, "rider cuts", "%d", plan.Stats.RiderCuts)
	kv(&b, "crest minutes", "%d", plan.Stats.HumpMinutes)
	kv(&b, "flat minutes", "%d", plan.Stats.FlatMinutes)
	kv(&b, "window", "%s to %s", Minutes(plan.Stats.FirstMinute), Minutes(plan.Stats.LastMinute))
	kv(&b, "average cut", "%.2f cars", plan.Stats.AverageCutCars)
	b.WriteString("\n")
	section(&b, "CUTS")
	fmt.Fprintf(&b, "  %4s %-8s %-10s %-6s %5s %7s %9s %-8s %-6s %s\n",
		"#", "TRAIN", "TRACK", "BLOCK", "CARS", "FT", "TONS", "RETARDER", "RIDER", "WINDOW")
	for _, c := range plan.Cuts {
		fmt.Fprintf(&b, "  %4d %-8s %-10s %-6s %5d %7d %9.1f %-8s %-6s %s-%s\n",
			c.Index, c.TrainID, c.TrackID, c.Block, len(c.CarIDs), c.LengthFt, c.Tons,
			c.Retarder, yesNo(c.RiderRequired), Minutes(c.StartMinute), Minutes(c.EndMinute))
	}
	b.WriteString("\n")
	section(&b, "FLAT SWITCHING")
	if len(plan.FlatMoves) == 0 {
		b.WriteString("  no flat moves\n")
	} else {
		fmt.Fprintf(&b, "  %4s %-8s %-10s %5s %-26s %s\n", "#", "TRAIN", "TRACK", "CARS", "REASON", "DETAIL")
		for _, f := range plan.FlatMoves {
			fmt.Fprintf(&b, "  %4d %-8s %-10s %5d %-26s %s\n",
				f.Index, f.TrainID, dash(f.TrackID), len(f.CarIDs), f.Reason, f.Detail)
		}
	}
	b.WriteString("\n")
	section(&b, "FINDINGS")
	b.WriteString(Findings(plan.Findings))
	return b.String()
}

// Occupancy renders an occupancy simulation.
func Occupancy(res occupancy.Result) string {
	var b strings.Builder
	section(&b, "TRACK OCCUPANCY")
	kv(&b, "cars placed", "%d", res.Stats.CarsPlaced)
	kv(&b, "cars overflowed", "%d", res.Stats.CarsOverflowed)
	kv(&b, "cars refused", "%d", res.Stats.CarsRefused)
	kv(&b, "tracks used", "%d", res.Stats.TracksUsed)
	kv(&b, "bowl fill", "%.1f%% of %d ft", res.Stats.FillPercent, res.Stats.TotalCapacity)
	if res.Stats.PeakFillTrack != "" {
		kv(&b, "peak track", "%s at %.1f%%", res.Stats.PeakFillTrack, res.Stats.PeakFillPct)
	}
	b.WriteString("\n")
	section(&b, "FINAL STANDING ORDER")
	for _, t := range res.Tracks {
		fmt.Fprintf(&b, "  %-10s %-6s %4d cars %6d/%-6d ft %8.1f/%-8.1f tons %5.1f%%\n",
			t.TrackID, t.Block, len(t.CarIDs), t.UsedFt, t.CapacityFt, t.UsedTons, t.LimitTons, t.FillPercent)
		if len(t.CarIDs) > 0 {
			fmt.Fprintf(&b, "      %s\n", strings.Join(t.CarIDs, ", "))
		}
	}
	b.WriteString("\n")
	section(&b, "PLACEMENT EVENTS")
	fmt.Fprintf(&b, "  %5s %-5s %-14s %-10s %-10s %-9s %s\n", "SEQ", "KIND", "CAR", "INTENDED", "FINAL", "ACTION", "REASON")
	for _, e := range res.Events {
		fmt.Fprintf(&b, "  %5d %-5s %-14s %-10s %-10s %-9s %s\n",
			e.Seq, e.Kind, e.CarID, dash(e.IntendedID), dash(e.FinalID), e.Action, e.Reason)
	}
	b.WriteString("\n")
	section(&b, "FINDINGS")
	b.WriteString(Findings(res.Findings))
	return b.String()
}

// Hazmat renders a hazmat validation report.
func Hazmat(rep hazmat.Report) string {
	var b strings.Builder
	section(&b, "HAZMAT VALIDATION")
	kv(&b, "placements checked", "%d", rep.Checked)
	kv(&b, "cars checked", "%d", rep.Cars)
	kv(&b, "violations", "%d", len(rep.Violations))
	b.WriteString("\n")
	section(&b, "EXPOSURE")
	fmt.Fprintf(&b, "  %-14s %8s %9s %6s\n", "PLACEMENT", "HAZMAT", "PLACARDS", "LIMIT")
	for _, t := range rep.Tallies {
		fmt.Fprintf(&b, "  %-14s %8d %9d %6d\n", t.TrackID, t.HazmatCars, t.Placards, t.Limit)
	}
	b.WriteString("\n")
	section(&b, "VIOLATIONS")
	if len(rep.Violations) == 0 {
		b.WriteString("  none\n")
		return b.String()
	}
	for _, v := range rep.Violations {
		fmt.Fprintf(&b, "  %-14s %-18s %s\n", v.TrackID, v.Rule, v.Message)
	}
	return b.String()
}

// Departures renders a departure program.
func Departures(plan depart.Plan) string {
	var b strings.Builder
	section(&b, "DEPARTURE PROGRAM")
	kv(&b, "trains", "%d", plan.Stats.Trains)
	kv(&b, "cars forwarded", "%d", plan.Stats.CarsForwarded)
	kv(&b, "cars held", "%d", plan.Stats.CarsHeld)
	kv(&b, "total tonnage", "%.1f", plan.Stats.TotalTons)
	kv(&b, "total length", "%d ft", plan.Stats.TotalLengthFt)
	kv(&b, "short of power", "%d", plan.Stats.PowerShort)
	kv(&b, "incomplete trains", "%d", plan.Stats.Incomplete)
	for _, tr := range plan.Trains {
		b.WriteString("\n")
		section(&b, "TRAIN "+tr.TrainID)
		kv(&b, "departure track", "%s", tr.TrackID)
		kv(&b, "depart", "%s", Minutes(tr.DepartMinute))
		kv(&b, "power", "%s", strings.Join(tr.Locomotives, "+"))
		kv(&b, "rated tonnage", "%.0f", tr.RatedTons)
		kv(&b, "trailing tonnage", "%.1f", tr.TrailingTons)
		kv(&b, "length", "%d ft", tr.LengthFt)
		kv(&b, "axles", "%d", tr.Axles)
		kv(&b, "horsepower per ton", "%.2f", tr.HPPerTon)
		kv(&b, "complete", "%s", yesNo(tr.Complete))
		fmt.Fprintf(&b, "  %-26s %s\n", "blocks", blockSummary(tr.Blocks))
		for _, line := range tr.Manifest()[1:] {
			fmt.Fprintf(&b, "  %s\n", line)
		}
		if len(tr.Held) > 0 {
			fmt.Fprintf(&b, "  %-26s\n", "held cars")
			for _, h := range tr.Held {
				fmt.Fprintf(&b, "    %-14s %-24s %s\n", h.CarID, h.Reason, h.Detail)
			}
		}
	}
	b.WriteString("\n")
	section(&b, "FINDINGS")
	b.WriteString(Findings(plan.Findings))
	return b.String()
}

// blockSummary renders the per-block fill of a train on one line.
func blockSummary(fills []depart.BlockFill) string {
	parts := make([]string, 0, len(fills))
	for _, f := range fills {
		parts = append(parts, fmt.Sprintf("%s:%d", f.Block, f.Cars))
	}
	if len(parts) == 0 {
		return "none"
	}
	return strings.Join(parts, " ")
}

// Rehandle renders a rework report.
func Rehandle(rep rehandle.Report) string {
	var b strings.Builder
	section(&b, "REHANDLE")
	kv(&b, "cars in yard", "%d", rep.TotalCars)
	kv(&b, "rehandle cars", "%d", rep.RehandleCars)
	kv(&b, "rehandle rate", "%.2f%%", rep.RehandlePct)
	kv(&b, "second hump pass", "%d (%.2f%%)", rep.SecondPass, rep.SecondPassPct)
	b.WriteString("\n")
	section(&b, "CATEGORIES")
	for _, c := range rep.Counts {
		fmt.Fprintf(&b, "  %-20s %5d\n", c.Category, c.Cars)
	}
	if len(rep.Counts) == 0 {
		b.WriteString("  none\n")
	}
	b.WriteString("\n")
	section(&b, "ITEMS")
	if len(rep.Items) == 0 {
		b.WriteString("  none\n")
	} else {
		fmt.Fprintf(&b, "  %-14s %-18s %-10s %-6s %-6s %-6s %s\n", "CAR", "CATEGORY", "TRACK", "WANT", "ACTUAL", "2ND", "DETAIL")
		for _, it := range rep.Items {
			fmt.Fprintf(&b, "  %-14s %-18s %-10s %-6s %-6s %-6s %s\n",
				it.CarID, it.Category, dash(it.CurrentTrs), dash(it.WantBlock), dash(it.ActualBlock), yesNo(it.SecondPass), it.Detail)
		}
	}
	b.WriteString("\n")
	section(&b, "FINDINGS")
	b.WriteString(Findings(rep.Findings))
	return b.String()
}

// Shifts renders a shift plan.
func Shifts(plan shift.Plan) string {
	var b strings.Builder
	section(&b, "SHIFT PLAN")
	kv(&b, "tasks", "%d", plan.Stats.Tasks)
	kv(&b, "assigned", "%d", plan.Stats.Assigned)
	kv(&b, "unassigned", "%d", plan.Stats.Unassigned)
	kv(&b, "task minutes", "%d", plan.Stats.TotalMinutes)
	kv(&b, "crew minutes", "%d", plan.Stats.CrewMinutes)
	kv(&b, "average utilization", "%.1f%%", plan.Stats.AvgUtilPct)
	b.WriteString("\n")
	section(&b, "SHIFT LOAD")
	fmt.Fprintf(&b, "  %-8s %-16s %6s %8s %6s\n", "SHIFT", "WINDOW", "TASKS", "MINUTES", "CARS")
	for _, l := range plan.ShiftLoads {
		fmt.Fprintf(&b, "  %-8s %-16s %6d %8d %6d\n",
			l.ShiftID, Minutes(l.StartMinute)+"-"+Minutes(l.EndMinute), l.Tasks, l.Minutes, l.Cars)
	}
	b.WriteString("\n")
	section(&b, "CREW LOAD")
	fmt.Fprintf(&b, "  %-8s %-8s %6s %8s %8s %7s\n", "CREW", "SHIFT", "TASKS", "MINUTES", "LIMIT", "UTIL")
	for _, l := range plan.CrewLoads {
		fmt.Fprintf(&b, "  %-8s %-8s %6d %8d %8d %6.1f%%\n",
			l.CrewID, l.ShiftID, l.Tasks, l.Minutes, l.MaxMinutes, l.UtilPct)
	}
	b.WriteString("\n")
	section(&b, "ASSIGNMENTS")
	fmt.Fprintf(&b, "  %-12s %-8s %-10s %-8s %-8s %-16s\n", "TASK", "KIND", "SUBJECT", "SHIFT", "CREW", "WINDOW")
	for _, a := range plan.Assignments {
		fmt.Fprintf(&b, "  %-12s %-8s %-10s %-8s %-8s %-16s\n",
			a.TaskID, a.Kind, a.Subject, a.ShiftID, a.CrewID, Minutes(a.StartMinute)+"-"+Minutes(a.EndMinute))
	}
	if len(plan.Unassigned) > 0 {
		b.WriteString("\n")
		section(&b, "UNASSIGNED")
		for _, u := range plan.Unassigned {
			fmt.Fprintf(&b, "  %-12s %-8s %-10s %s\n", u.TaskID, u.Kind, u.Subject, u.Reason)
		}
	}
	b.WriteString("\n")
	section(&b, "FINDINGS")
	b.WriteString(Findings(plan.Findings))
	return b.String()
}

// Snapshot renders the whole plan snapshot.
func Snapshot(snap *pipeline.Snapshot) string {
	var b strings.Builder
	d := snap.Digest()
	section(&b, "HUMPYARD PLAN "+snap.OrderID)
	kv(&b, "yard", "%s (%s)", snap.YardName, snap.YardID)
	kv(&b, "source", "%s", snap.Source)
	kv(&b, "config sha256", "%s", snap.ConfigSHA)
	kv(&b, "order sha256", "%s", snap.OrderSHA)
	kv(&b, "inbound cars", "%d", d.InboundCars)
	kv(&b, "humped", "%d", d.Humped)
	kv(&b, "flat switched", "%d", d.FlatSwitched)
	kv(&b, "forwarded", "%d", d.Forwarded)
	kv(&b, "held", "%d", d.Held)
	kv(&b, "rehandle rate", "%.2f%%", d.RehandlePct)
	kv(&b, "hazmat violations", "%d", d.HazmatIssues)
	kv(&b, "crew tasks", "%d assigned, %d unassigned", d.CrewTasks, d.UnassignedTsk)
	kv(&b, "findings", "%d errors, %d warnings", d.Errors, d.Warnings)
	b.WriteString("\n")
	b.WriteString(Blocking(snap.Blocking))
	b.WriteString("\n")
	b.WriteString(Hump(snap.Hump))
	b.WriteString("\n")
	b.WriteString(Occupancy(snap.Occupancy))
	b.WriteString("\n")
	b.WriteString(Hazmat(snap.Hazmat))
	b.WriteString("\n")
	b.WriteString(Departures(snap.Departures))
	b.WriteString("\n")
	b.WriteString(Rehandle(snap.Rehandle))
	b.WriteString("\n")
	b.WriteString(Shifts(snap.Shifts))
	b.WriteString("\n")
	section(&b, "ALL FINDINGS")
	b.WriteString(Findings(snap.Findings))
	return b.String()
}

// Verify renders a store verification report.
func Verify(rep store.ChainReport, meta store.Meta, files []string) string {
	var b strings.Builder
	section(&b, "STORE VERIFICATION")
	kv(&b, "yard", "%s", dash(meta.YardID))
	kv(&b, "order", "%s", dash(meta.OrderID))
	kv(&b, "format version", "%d", meta.FormatVersion)
	kv(&b, "ledger entries", "%d", meta.LedgerEntries)
	kv(&b, "audit records", "%d", rep.Records)
	kv(&b, "snapshots written", "%d", meta.Snapshots)
	kv(&b, "chain head", "%s", rep.Head)
	kv(&b, "metadata head", "%s", dash(rep.MetaHead))
	kv(&b, "snapshot sha256", "%s", dash(rep.Snapshot))
	kv(&b, "ledger ok", "%s", yesNo(rep.LedgerOK))
	kv(&b, "chain ok", "%s", yesNo(rep.ChainOK))
	kv(&b, "store ok", "%s", yesNo(rep.StoreOK))
	kv(&b, "files", "%s", strings.Join(files, " "))
	if rep.LedgerErr != "" {
		kv(&b, "ledger error", "%s", rep.LedgerErr)
	}
	b.WriteString("\n")
	section(&b, "PROBLEMS")
	if len(rep.Problems) == 0 {
		b.WriteString("  none\n")
		return b.String()
	}
	for _, p := range rep.Problems {
		fmt.Fprintf(&b, "  seq %-5d %-16s %s\n", p.Seq, p.Kind, p.Message)
		fmt.Fprintf(&b, "    expected %s\n", dash(p.Expected))
		fmt.Fprintf(&b, "    actual   %s\n", dash(p.Actual))
	}
	return b.String()
}

// Ledger renders store ledger entries.
func Ledger(entries []store.LedgerEntry) string {
	var b strings.Builder
	section(&b, "WORK LEDGER")
	if len(entries) == 0 {
		b.WriteString("  empty\n")
		return b.String()
	}
	fmt.Fprintf(&b, "  %5s %-22s %-14s %-10s %5s %7s %s\n", "SEQ", "KIND", "SUBJECT", "TRACK", "CARS", "MINUTES", "DETAIL")
	for _, e := range entries {
		fmt.Fprintf(&b, "  %5d %-22s %-14s %-10s %5d %7d %s\n",
			e.Seq, e.Kind, e.Subject, dash(e.TrackID), e.Cars, e.Minutes, e.Detail)
	}
	return b.String()
}

// Audit renders audit chain records.
func Audit(records []store.AuditRecord) string {
	var b strings.Builder
	section(&b, "AUDIT CHAIN")
	if len(records) == 0 {
		b.WriteString("  empty\n")
		return b.String()
	}
	fmt.Fprintf(&b, "  %5s %-12s %-12s %-16s %s\n", "SEQ", "ACTION", "SUBJECT", "HASH", "DETAIL")
	for _, r := range records {
		fmt.Fprintf(&b, "  %5d %-12s %-12s %-16s %s\n", r.Seq, r.Action, dash(r.Subject), r.Hash[:16], r.Detail)
	}
	return b.String()
}

// dash renders an empty string as a dash.
func dash(s string) string {
	if s == "" {
		return "-"
	}
	return s
}

// yesNo renders a boolean.
func yesNo(v bool) string {
	if v {
		return "yes"
	}
	return "no"
}

// truncate shortens s to at most n characters.
func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	if n <= 3 {
		return s[:n]
	}
	return s[:n-3] + "..."
}
