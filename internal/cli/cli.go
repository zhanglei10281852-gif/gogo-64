// Package cli implements the HumpYard command line. Every subcommand is
// offline, reads only the files it is pointed at, and writes either
// deterministic text or indented JSON.
package cli

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"sort"
	"strings"

	"HumpYard/internal/config"
	"HumpYard/internal/ingest"
	"HumpYard/internal/pipeline"
	"HumpYard/internal/report"
	"HumpYard/internal/store"
)

// Exit codes returned by Run.
const (
	// ExitOK means the command succeeded and found no errors.
	ExitOK = 0
	// ExitUsage means the command line or an input file was unusable.
	ExitUsage = 1
	// ExitFindings means the command ran but the result holds error findings.
	ExitFindings = 2
)

// Name is the program name used in usage output.
const Name = "humpyard"

// Version is the CLI version string.
const Version = "1.0.0"

// options holds the flags shared by the subcommands.
type options struct {
	configPath string
	orderPath  string
	storeDir   string
	format     string
	quiet      bool
}

// command is one subcommand implementation.
type command struct {
	name    string
	summary string
	run     func(*env, []string) error
}

// env carries the output streams and resolved options.
type env struct {
	out  io.Writer
	err  io.Writer
	opts options
	code int
}

// Run dispatches a subcommand and returns the process exit code.
func Run(args []string, stdout, stderr io.Writer) int {
	e := &env{out: stdout, err: stderr}
	if len(args) == 0 {
		usage(stderr)
		return ExitUsage
	}
	switch args[0] {
	case "-h", "--help", "help":
		usage(stdout)
		return ExitOK
	case "-v", "--version", "version":
		fmt.Fprintf(stdout, "%s %s\n", Name, Version)
		return ExitOK
	}
	cmd, ok := lookup(args[0])
	if !ok {
		fmt.Fprintf(stderr, "%s: unknown command %q\n", Name, args[0])
		usage(stderr)
		return ExitUsage
	}
	if err := cmd.run(e, args[1:]); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return ExitUsage
		}
		fmt.Fprintf(stderr, "%s %s: %v\n", Name, cmd.name, err)
		return ExitUsage
	}
	if e.code != ExitOK {
		return e.code
	}
	return ExitOK
}

// commands is the ordered subcommand table.
func commands() []command {
	return []command{
		{"validate", "check a configuration and optionally a yard order", cmdValidate},
		{"ingest", "decode a yard order and summarize it", cmdIngest},
		{"block", "map destinations to blocks and blocks to tracks", cmdBlock},
		{"hump", "sequence the crest into cuts and flat moves", cmdHump},
		{"occupancy", "simulate track occupancy and validate hazmat placement", cmdOccupancy},
		{"build", "assemble outbound trains and manifests", cmdBuild},
		{"rehandle", "derive rework and the rehandle percentage", cmdRehandle},
		{"plan", "run the whole chain and persist a snapshot", cmdPlan},
		{"report", "render a stored plan snapshot", cmdReport},
		{"verify", "verify the store ledger and audit chain", cmdVerify},
	}
}

// lookup finds a subcommand by name.
func lookup(name string) (command, bool) {
	for _, c := range commands() {
		if c.name == name {
			return c, true
		}
	}
	return command{}, false
}

// usage prints the top level help text.
func usage(w io.Writer) {
	fmt.Fprintf(w, "%s %s - railway hump yard classification planner\n\n", Name, Version)
	fmt.Fprintf(w, "usage: %s <command> [flags]\n\ncommands:\n", Name)
	for _, c := range commands() {
		fmt.Fprintf(w, "  %-10s %s\n", c.name, c.summary)
	}
	fmt.Fprintf(w, "\ncommon flags:\n")
	fmt.Fprintf(w, "  -config path   configuration JSON document\n")
	fmt.Fprintf(w, "  -order path    yard order JSON or JSONL document\n")
	fmt.Fprintf(w, "  -store dir     local store directory\n")
	fmt.Fprintf(w, "  -format kind   text or json output, default text\n")
	fmt.Fprintf(w, "  -quiet         suppress informational text on stdout\n")
	fmt.Fprintf(w, "\nexit codes: %d ok, %d usage or input error, %d error findings\n", ExitOK, ExitUsage, ExitFindings)
}

// bind registers the shared flags on a flag set.
func (e *env) bind(fs *flag.FlagSet, need map[string]bool) {
	if need["config"] {
		fs.StringVar(&e.opts.configPath, "config", "", "configuration JSON document")
	}
	if need["order"] {
		fs.StringVar(&e.opts.orderPath, "order", "", "yard order JSON or JSONL document")
	}
	if need["store"] {
		fs.StringVar(&e.opts.storeDir, "store", "", "local store directory")
	}
	fs.StringVar(&e.opts.format, "format", "text", "output format: text or json")
	fs.BoolVar(&e.opts.quiet, "quiet", false, "suppress informational text")
}

// parse parses a subcommand flag set and validates the shared options.
func (e *env) parse(fs *flag.FlagSet, args []string, required []string) error {
	fs.SetOutput(e.err)
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() > 0 {
		return fmt.Errorf("unexpected argument %q", fs.Arg(0))
	}
	if e.opts.format != "text" && e.opts.format != "json" {
		return fmt.Errorf("format %q must be text or json", e.opts.format)
	}
	for _, req := range required {
		switch req {
		case "config":
			if e.opts.configPath == "" {
				return fmt.Errorf("-config is required")
			}
		case "order":
			if e.opts.orderPath == "" {
				return fmt.Errorf("-order is required")
			}
		case "store":
			if e.opts.storeDir == "" {
				return fmt.Errorf("-store is required")
			}
		}
	}
	return nil
}

// emit writes either the text rendering or the JSON value.
func (e *env) emit(text string, value any) error {
	if e.opts.format == "json" {
		return report.JSON(e.out, value)
	}
	if e.opts.quiet {
		return nil
	}
	_, err := io.WriteString(e.out, text)
	return err
}

// note writes an informational line unless quiet or JSON output is active.
func (e *env) note(format string, args ...any) {
	if e.opts.quiet || e.opts.format == "json" {
		return
	}
	fmt.Fprintf(e.out, format+"\n", args...)
}

// flagFindings sets the findings exit code when any error finding is present.
func (e *env) flagFindings(findings []config.Finding) {
	for _, f := range findings {
		if f.Severity == config.SeverityError {
			e.code = ExitFindings
			return
		}
	}
}

// loadConfig loads and validates the configuration.
func (e *env) loadConfig() (*config.Config, config.Report, error) {
	cfg, rep, err := config.Load(e.opts.configPath)
	if err != nil {
		return nil, rep, err
	}
	return cfg, rep, nil
}

// loadOrder loads the yard order against a configuration.
func (e *env) loadOrder(cfg *config.Config) (*ingest.Result, error) {
	return ingest.Load(e.opts.orderPath, cfg)
}

// stage runs the pipeline up to the point every planning command needs.
func (e *env) stage() (*config.Config, *ingest.Result, *pipeline.Snapshot, error) {
	cfg, _, err := e.loadConfig()
	if err != nil {
		return nil, nil, nil, err
	}
	res, err := e.loadOrder(cfg)
	if err != nil {
		return nil, nil, nil, err
	}
	if ingest.HasErrors(res.Findings) {
		e.code = ExitFindings
	}
	snap, err := pipeline.Run(cfg, res)
	if err != nil {
		return nil, nil, nil, err
	}
	return cfg, res, snap, nil
}

// cmdValidate implements the validate subcommand.
func cmdValidate(e *env, args []string) error {
	fs := flag.NewFlagSet("validate", flag.ContinueOnError)
	e.bind(fs, map[string]bool{"config": true, "order": true})
	if err := e.parse(fs, args, []string{"config"}); err != nil {
		return err
	}
	cfg, rep, err := e.loadConfig()
	if err != nil {
		return err
	}
	findings := append([]config.Finding(nil), rep.Findings...)
	var orderRes *ingest.Result
	if e.opts.orderPath != "" {
		orderRes, err = e.loadOrder(cfg)
		if err != nil {
			return err
		}
		findings = append(findings, orderRes.Findings...)
	}
	sortFindings(findings)
	e.flagFindings(findings)
	payload := struct {
		Yard        string           `json:"yard_id"`
		ConfigOK    bool             `json:"config_ok"`
		Blocks      int              `json:"blocks"`
		ClassTracks int              `json:"classification_tracks"`
		Order       *ingest.Stats    `json:"order,omitempty"`
		Findings    []config.Finding `json:"findings"`
	}{
		Yard:        cfg.Yard.ID,
		ConfigOK:    rep.OK(),
		Blocks:      len(cfg.Blocks),
		ClassTracks: len(cfg.Class),
		Findings:    findings,
	}
	text := report.Validation(cfg, config.Report{Findings: findings})
	if orderRes != nil {
		payload.Order = &orderRes.Stats
		text += "\n" + report.Ingested(orderRes)
	}
	return e.emit(text, payload)
}

// cmdIngest implements the ingest subcommand.
func cmdIngest(e *env, args []string) error {
	fs := flag.NewFlagSet("ingest", flag.ContinueOnError)
	e.bind(fs, map[string]bool{"config": true, "order": true, "store": true})
	if err := e.parse(fs, args, []string{"config", "order"}); err != nil {
		return err
	}
	cfg, _, err := e.loadConfig()
	if err != nil {
		return err
	}
	res, err := e.loadOrder(cfg)
	if err != nil {
		return err
	}
	e.flagFindings(res.Findings)
	if e.opts.storeDir != "" {
		st, err := store.Open(e.opts.storeDir)
		if err != nil {
			return err
		}
		if err := st.SetIdentity(cfg.Yard.ID, res.Order.OrderID); err != nil {
			return err
		}
		rec, err := st.Append("ingest", res.Order.OrderID,
			fmt.Sprintf("%d trains, %d cars from %s", res.Stats.Trains, res.Stats.Cars, res.Source), res.Stats)
		if err != nil {
			return err
		}
		e.note("audit record %d recorded, hash %s", rec.Seq, rec.Hash)
	}
	return e.emit(report.Ingested(res), res)
}

// cmdBlock implements the block subcommand.
func cmdBlock(e *env, args []string) error {
	fs := flag.NewFlagSet("block", flag.ContinueOnError)
	e.bind(fs, map[string]bool{"config": true, "order": true})
	if err := e.parse(fs, args, []string{"config", "order"}); err != nil {
		return err
	}
	_, _, snap, err := e.stage()
	if err != nil {
		return err
	}
	e.flagFindings(snap.Blocking.Findings)
	return e.emit(report.Blocking(snap.Blocking), snap.Blocking)
}

// cmdHump implements the hump subcommand.
func cmdHump(e *env, args []string) error {
	fs := flag.NewFlagSet("hump", flag.ContinueOnError)
	e.bind(fs, map[string]bool{"config": true, "order": true})
	if err := e.parse(fs, args, []string{"config", "order"}); err != nil {
		return err
	}
	_, _, snap, err := e.stage()
	if err != nil {
		return err
	}
	e.flagFindings(snap.Hump.Findings)
	return e.emit(report.Hump(snap.Hump), snap.Hump)
}

// cmdOccupancy implements the occupancy subcommand.
func cmdOccupancy(e *env, args []string) error {
	fs := flag.NewFlagSet("occupancy", flag.ContinueOnError)
	e.bind(fs, map[string]bool{"config": true, "order": true})
	if err := e.parse(fs, args, []string{"config", "order"}); err != nil {
		return err
	}
	_, _, snap, err := e.stage()
	if err != nil {
		return err
	}
	e.flagFindings(snap.Occupancy.Findings)
	e.flagFindings(snap.Hazmat.Findings())
	payload := struct {
		Occupancy any `json:"occupancy"`
		Hazmat    any `json:"hazmat"`
	}{snap.Occupancy, snap.Hazmat}
	text := report.Occupancy(snap.Occupancy) + "\n" + report.Hazmat(snap.Hazmat)
	return e.emit(text, payload)
}

// cmdBuild implements the build subcommand.
func cmdBuild(e *env, args []string) error {
	fs := flag.NewFlagSet("build", flag.ContinueOnError)
	e.bind(fs, map[string]bool{"config": true, "order": true})
	if err := e.parse(fs, args, []string{"config", "order"}); err != nil {
		return err
	}
	_, _, snap, err := e.stage()
	if err != nil {
		return err
	}
	e.flagFindings(snap.Departures.Findings)
	return e.emit(report.Departures(snap.Departures), snap.Departures)
}

// cmdRehandle implements the rehandle subcommand.
func cmdRehandle(e *env, args []string) error {
	fs := flag.NewFlagSet("rehandle", flag.ContinueOnError)
	e.bind(fs, map[string]bool{"config": true, "order": true})
	if err := e.parse(fs, args, []string{"config", "order"}); err != nil {
		return err
	}
	_, _, snap, err := e.stage()
	if err != nil {
		return err
	}
	e.flagFindings(snap.Rehandle.Findings)
	return e.emit(report.Rehandle(snap.Rehandle), snap.Rehandle)
}

// cmdPlan implements the plan subcommand.
func cmdPlan(e *env, args []string) error {
	fs := flag.NewFlagSet("plan", flag.ContinueOnError)
	e.bind(fs, map[string]bool{"config": true, "order": true, "store": true})
	if err := e.parse(fs, args, []string{"config", "order"}); err != nil {
		return err
	}
	_, _, snap, err := e.stage()
	if err != nil {
		return err
	}
	e.flagFindings(snap.Findings)
	if e.opts.storeDir != "" {
		st, err := store.Open(e.opts.storeDir)
		if err != nil {
			return err
		}
		records, err := pipeline.Persist(st, snap)
		if err != nil {
			return err
		}
		digest, err := st.SnapshotDigest()
		if err != nil {
			return err
		}
		e.note("snapshot written to %s, sha256 %s", st.Dir(), digest)
		if len(records) > 0 {
			e.note("audit chain head %s after %d records", records[len(records)-1].Hash, records[len(records)-1].Seq)
		}
	}
	return e.emit(report.Snapshot(snap), snap)
}

// cmdReport implements the report subcommand.
func cmdReport(e *env, args []string) error {
	fs := flag.NewFlagSet("report", flag.ContinueOnError)
	e.bind(fs, map[string]bool{"store": true})
	section := fs.String("section", "all", "section to render: all, ledger, audit, summary")
	if err := e.parse(fs, args, []string{"store"}); err != nil {
		return err
	}
	st, err := store.Open(e.opts.storeDir)
	if err != nil {
		return err
	}
	switch *section {
	case "ledger":
		entries, err := st.ReadLedger()
		if err != nil {
			return err
		}
		return e.emit(report.Ledger(entries), entries)
	case "audit":
		records, err := st.ReadAudit()
		if err != nil {
			return err
		}
		return e.emit(report.Audit(records), records)
	case "summary", "all":
		var snap pipeline.Snapshot
		if err := st.LoadSnapshot(&snap); err != nil {
			return err
		}
		e.flagFindings(snap.Findings)
		if *section == "summary" {
			return e.emit(summaryText(&snap), snap.Digest())
		}
		return e.emit(report.Snapshot(&snap), &snap)
	default:
		return fmt.Errorf("section %q must be all, summary, ledger or audit", *section)
	}
}

// summaryText renders the compact snapshot digest as text.
func summaryText(snap *pipeline.Snapshot) string {
	d := snap.Digest()
	var b strings.Builder
	fmt.Fprintf(&b, "plan %s yard %s\n", snap.OrderID, snap.YardID)
	fmt.Fprintf(&b, "  inbound %d, humped %d, flat %d, forwarded %d, held %d\n",
		d.InboundCars, d.Humped, d.FlatSwitched, d.Forwarded, d.Held)
	fmt.Fprintf(&b, "  rehandle %.2f%%, hazmat violations %d\n", d.RehandlePct, d.HazmatIssues)
	fmt.Fprintf(&b, "  tasks %d assigned, %d unassigned\n", d.CrewTasks, d.UnassignedTsk)
	fmt.Fprintf(&b, "  findings %d errors, %d warnings\n", d.Errors, d.Warnings)
	return b.String()
}

// cmdVerify implements the verify subcommand.
func cmdVerify(e *env, args []string) error {
	fs := flag.NewFlagSet("verify", flag.ContinueOnError)
	e.bind(fs, map[string]bool{"store": true})
	if err := e.parse(fs, args, []string{"store"}); err != nil {
		return err
	}
	st, err := store.Open(e.opts.storeDir)
	if err != nil {
		return err
	}
	rep, err := st.VerifyChain()
	if err != nil {
		return err
	}
	files, err := st.Files()
	if err != nil {
		return err
	}
	if !rep.StoreOK {
		e.code = ExitFindings
	}
	payload := struct {
		Chain store.ChainReport `json:"chain"`
		Meta  store.Meta        `json:"meta"`
		Files []string          `json:"files"`
	}{rep, st.Meta(), files}
	return e.emit(report.Verify(rep, st.Meta(), files), payload)
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
