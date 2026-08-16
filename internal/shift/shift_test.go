package shift

import (
	"path/filepath"
	"strings"
	"testing"

	"HumpYard/internal/blocking"
	"HumpYard/internal/config"
	"HumpYard/internal/depart"
	"HumpYard/internal/hump"
	"HumpYard/internal/ingest"
	"HumpYard/internal/jsonx"
	"HumpYard/internal/model"
	"HumpYard/internal/occupancy"
)

// stage loads the examples and runs every stage before shift assignment.
func stage(t *testing.T, tweak func(*config.Config)) (*config.Config, model.YardOrder, hump.Plan, depart.Plan) {
	t.Helper()
	cfg, _, err := config.Load(filepath.Join("..", "..", "examples", "config.json"))
	if err != nil {
		t.Fatalf("config.Load: %v", err)
	}
	res, err := ingest.Load(filepath.Join("..", "..", "examples", "order.json"), cfg)
	if err != nil {
		t.Fatalf("ingest.Load: %v", err)
	}
	if tweak != nil {
		tweak(cfg)
	}
	bp := blocking.Build(cfg, res.Order)
	hp := hump.Build(cfg, res.Order, bp)
	occ, err := occupancy.Simulate(cfg, res.Order, hp)
	if err != nil {
		t.Fatalf("Simulate: %v", err)
	}
	dp, err := depart.Build(cfg, res.Order, occ)
	if err != nil {
		t.Fatalf("depart.Build: %v", err)
	}
	return cfg, res.Order, hp, dp
}

func TestTasksCoverEveryMoveAndTrain(t *testing.T) {
	cfg, order, hp, dp := stage(t, nil)
	tasks := Tasks(cfg, order, hp, dp)
	kinds := map[string]int{}
	for _, task := range tasks {
		kinds[task.Kind]++
		if task.Minutes <= 0 {
			t.Fatalf("task %s has %d minutes", task.ID, task.Minutes)
		}
	}
	if kinds[KindBuild] != len(dp.Trains) {
		t.Fatalf("%d build tasks for %d trains", kinds[KindBuild], len(dp.Trains))
	}
	if kinds[KindFlat] != len(hp.FlatMoves) {
		t.Fatalf("%d flat tasks for %d flat moves", kinds[KindFlat], len(hp.FlatMoves))
	}
	if kinds[KindHump]+kinds[KindRide] != len(hp.Cuts) {
		t.Fatalf("%d crest tasks for %d cuts", kinds[KindHump]+kinds[KindRide], len(hp.Cuts))
	}
	uninspected := 0
	for _, tr := range order.Trains {
		if !tr.Inspected {
			uninspected++
		}
	}
	if kinds[KindInspect] != uninspected {
		t.Fatalf("%d inspection tasks for %d uninspected trains", kinds[KindInspect], uninspected)
	}
	for i := 1; i < len(tasks); i++ {
		if tasks[i-1].StartMinute > tasks[i].StartMinute {
			t.Fatal("tasks are not ordered by start minute")
		}
	}
}

func TestBuildAssignsQualifiedCrews(t *testing.T) {
	cfg, order, hp, dp := stage(t, nil)
	plan := Build(cfg, order, hp, dp)
	if plan.Stats.Unassigned != 0 {
		t.Fatalf("unassigned tasks: %+v", plan.Unassigned)
	}
	byID := map[string]Task{}
	for _, task := range plan.Tasks {
		byID[task.ID] = task
	}
	for _, a := range plan.Assignments {
		crew, ok := cfg.CrewByID(a.CrewID)
		if !ok {
			t.Fatalf("unknown crew %s", a.CrewID)
		}
		task := byID[a.TaskID]
		if !crew.Qualified(task.Qualification) {
			t.Fatalf("crew %s is not qualified for %s", crew.ID, task.Qualification)
		}
		if crew.HomeShift != a.ShiftID {
			t.Fatalf("crew %s works shift %s but was assigned %s", crew.ID, crew.HomeShift, a.ShiftID)
		}
		shift, ok := cfg.ShiftByID(a.ShiftID)
		if !ok {
			t.Fatalf("unknown shift %s", a.ShiftID)
		}
		if a.StartMinute < shift.StartMinute || a.StartMinute >= shift.EndMinute() {
			t.Fatalf("task %s at %d is outside shift %s", a.TaskID, a.StartMinute, shift.ID)
		}
	}
}

func TestDutyLimitsAreNeverExceeded(t *testing.T) {
	cfg, order, hp, dp := stage(t, func(cfg *config.Config) {
		for i := range cfg.Crews {
			cfg.Crews[i].MaxDutyMinutes = 60
		}
	})
	plan := Build(cfg, order, hp, dp)
	for _, load := range plan.CrewLoads {
		if load.Minutes > load.MaxMinutes {
			t.Fatalf("crew %s worked %d minutes over its %d minute limit", load.CrewID, load.Minutes, load.MaxMinutes)
		}
	}
	if plan.Stats.Unassigned == 0 {
		t.Fatal("expected unassigned tasks once duty hours are tight")
	}
	found := false
	for _, u := range plan.Unassigned {
		if strings.Contains(u.Reason, "duty hours") {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected a duty hour reason, got %+v", plan.Unassigned)
	}
}

func TestMissingQualificationLeavesTasksUnassigned(t *testing.T) {
	cfg, order, hp, dp := stage(t, func(cfg *config.Config) {
		for i := range cfg.Crews {
			kept := cfg.Crews[i].Qualifications[:0]
			for _, q := range cfg.Crews[i].Qualifications {
				if q != model.QualRider {
					kept = append(kept, q)
				}
			}
			cfg.Crews[i].Qualifications = kept
		}
	})
	plan := Build(cfg, order, hp, dp)
	found := false
	for _, u := range plan.Unassigned {
		if strings.Contains(u.Reason, "rider") {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected rider work to be unassigned, got %+v", plan.Unassigned)
	}
}

func TestWorkOutsideShiftWindowsIsUnassigned(t *testing.T) {
	cfg, order, hp, dp := stage(t, func(cfg *config.Config) {
		cfg.Shifts = cfg.Shifts[:1]
		cfg.Shifts[0].StartMinute = 1200
		cfg.Shifts[0].DurationMinutes = 120
	})
	plan := Build(cfg, order, hp, dp)
	found := false
	for _, u := range plan.Unassigned {
		if strings.Contains(u.Reason, "outside every shift window") {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected out-of-window tasks, got %+v", plan.Unassigned)
	}
}

func TestLoadBalancingPrefersTheLeastLoadedCrew(t *testing.T) {
	cfg, order, hp, dp := stage(t, func(cfg *config.Config) {
		second := cfg.Crews[0]
		second.ID = "AH2"
		second.Name = "First trick relief hump crew"
		cfg.Crews = append(cfg.Crews, second)
		cfg.Normalize()
	})
	plan := Build(cfg, order, hp, dp)
	var a, b int
	for _, load := range plan.CrewLoads {
		switch load.CrewID {
		case "AH1":
			a = load.Minutes
		case "AH2":
			b = load.Minutes
		}
	}
	if a == 0 || b == 0 {
		t.Fatalf("both hump crews should get work, got %d and %d", a, b)
	}
	diff := a - b
	if diff < 0 {
		diff = -diff
	}
	if diff > 10 {
		t.Fatalf("work is unbalanced: %d against %d", a, b)
	}
}

func TestBuildIsDeterministic(t *testing.T) {
	cfg, order, hp, dp := stage(t, nil)
	first, err := jsonx.MarshalCanonical(Build(cfg, order, hp, dp))
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	for i := 0; i < 5; i++ {
		again, err := jsonx.MarshalCanonical(Build(cfg, order, hp, dp))
		if err != nil {
			t.Fatalf("marshal: %v", err)
		}
		if string(again) != string(first) {
			t.Fatal("shift plan is not deterministic")
		}
	}
}

func TestAssignmentsForCrew(t *testing.T) {
	cfg, order, hp, dp := stage(t, nil)
	plan := Build(cfg, order, hp, dp)
	total := 0
	for _, load := range plan.CrewLoads {
		got := plan.AssignmentsFor(load.CrewID)
		if len(got) != load.Tasks {
			t.Fatalf("crew %s has %d assignments but load says %d", load.CrewID, len(got), load.Tasks)
		}
		total += len(got)
	}
	if total != len(plan.Assignments) {
		t.Fatalf("crew assignments total %d, plan has %d", total, len(plan.Assignments))
	}
}
