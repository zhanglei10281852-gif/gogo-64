// Package shift turns a yard plan into shift work. Every cut, flat move,
// inspection and train build becomes a task; tasks are placed in the shift
// whose window covers them and handed to a qualified crew that still has duty
// hours left. Ties are broken by least loaded crew, then crew identifier.
package shift

import (
	"fmt"
	"sort"

	"HumpYard/internal/config"
	"HumpYard/internal/depart"
	"HumpYard/internal/hump"
	"HumpYard/internal/model"
)

// Task kinds.
const (
	KindInspect = "inspect"
	KindHump    = "hump"
	KindFlat    = "flat"
	KindBuild   = "build"
	KindRide    = "ride"
)

// InspectMinutesPerCar is the inbound inspection allowance per car.
const InspectMinutesPerCar = 1

// InspectMinimumMinutes is the floor for any inbound inspection task.
const InspectMinimumMinutes = 15

// BuildSetupMinutes is the fixed allowance for coupling and air on a departure.
const BuildSetupMinutes = 20

// Task is one unit of yard work that a crew must perform.
type Task struct {
	ID            string `json:"id"`
	Kind          string `json:"kind"`
	Subject       string `json:"subject"`
	TrackID       string `json:"track_id"`
	Cars          int    `json:"cars"`
	Minutes       int    `json:"minutes"`
	StartMinute   int    `json:"start_minute"`
	Qualification string `json:"qualification"`
}

// Assignment binds a task to a shift and crew.
type Assignment struct {
	TaskID      string `json:"task_id"`
	Kind        string `json:"kind"`
	Subject     string `json:"subject"`
	ShiftID     string `json:"shift_id"`
	CrewID      string `json:"crew_id"`
	StartMinute int    `json:"start_minute"`
	EndMinute   int    `json:"end_minute"`
	Minutes     int    `json:"minutes"`
}

// Unassigned is a task no crew could take, with the reason.
type Unassigned struct {
	TaskID  string `json:"task_id"`
	Kind    string `json:"kind"`
	Subject string `json:"subject"`
	Reason  string `json:"reason"`
}

// CrewLoad is the duty load of one crew after assignment.
type CrewLoad struct {
	CrewID     string  `json:"crew_id"`
	ShiftID    string  `json:"shift_id"`
	Tasks      int     `json:"tasks"`
	Minutes    int     `json:"minutes"`
	MaxMinutes int     `json:"max_minutes"`
	UtilPct    float64 `json:"utilization_percent"`
}

// ShiftLoad is the work placed in one shift.
type ShiftLoad struct {
	ShiftID     string `json:"shift_id"`
	Tasks       int    `json:"tasks"`
	Minutes     int    `json:"minutes"`
	Cars        int    `json:"cars"`
	StartMinute int    `json:"start_minute"`
	EndMinute   int    `json:"end_minute"`
}

// Stats summarizes the shift plan.
type Stats struct {
	Tasks        int     `json:"tasks"`
	Assigned     int     `json:"assigned"`
	Unassigned   int     `json:"unassigned"`
	TotalMinutes int     `json:"total_minutes"`
	CrewMinutes  int     `json:"crew_minutes"`
	AvgUtilPct   float64 `json:"average_utilization_percent"`
}

// Plan is the whole shift assignment result.
type Plan struct {
	Tasks       []Task           `json:"tasks"`
	Assignments []Assignment     `json:"assignments"`
	Unassigned  []Unassigned     `json:"unassigned"`
	CrewLoads   []CrewLoad       `json:"crew_loads"`
	ShiftLoads  []ShiftLoad      `json:"shift_loads"`
	Stats       Stats            `json:"stats"`
	Findings    []config.Finding `json:"findings"`
}

// crewState tracks running duty minutes for one crew.
type crewState struct {
	crew    model.Crew
	minutes int
	tasks   int
}

// Build derives tasks from the plan stages and assigns them.
func Build(cfg *config.Config, order model.YardOrder, hp hump.Plan, dp depart.Plan) Plan {
	plan := Plan{Tasks: Tasks(cfg, order, hp, dp)}
	states := map[string]*crewState{}
	for _, c := range cfg.Crews {
		states[c.ID] = &crewState{crew: c}
	}
	shiftCars := map[string]int{}
	shiftMinutes := map[string]int{}
	shiftTasks := map[string]int{}
	for _, task := range plan.Tasks {
		sh, ok := shiftFor(cfg, task.StartMinute)
		if !ok {
			plan.Unassigned = append(plan.Unassigned, Unassigned{
				TaskID: task.ID, Kind: task.Kind, Subject: task.Subject,
				Reason: fmt.Sprintf("minute %d falls outside every shift window", task.StartMinute),
			})
			continue
		}
		crew, why := pickCrew(cfg, states, sh, task)
		if crew == nil {
			plan.Unassigned = append(plan.Unassigned, Unassigned{
				TaskID: task.ID, Kind: task.Kind, Subject: task.Subject, Reason: why,
			})
			continue
		}
		crew.minutes += task.Minutes
		crew.tasks++
		plan.Assignments = append(plan.Assignments, Assignment{
			TaskID: task.ID, Kind: task.Kind, Subject: task.Subject, ShiftID: sh.ID,
			CrewID: crew.crew.ID, StartMinute: task.StartMinute,
			EndMinute: task.StartMinute + task.Minutes, Minutes: task.Minutes,
		})
		shiftCars[sh.ID] += task.Cars
		shiftMinutes[sh.ID] += task.Minutes
		shiftTasks[sh.ID]++
	}
	plan.CrewLoads = crewLoads(cfg, states)
	plan.ShiftLoads = shiftLoads(cfg, shiftTasks, shiftMinutes, shiftCars)
	plan.Stats = digest(plan)
	plan.Findings = findings(cfg, plan)
	sortAssignments(plan.Assignments)
	sort.SliceStable(plan.Unassigned, func(i, j int) bool { return plan.Unassigned[i].TaskID < plan.Unassigned[j].TaskID })
	return plan
}

// Tasks derives the ordered task list from the plan stages.
func Tasks(cfg *config.Config, order model.YardOrder, hp hump.Plan, dp depart.Plan) []Task {
	var out []Task
	for _, train := range order.Trains {
		if train.Inspected {
			continue
		}
		minutes := len(train.Cars) * InspectMinutesPerCar
		if minutes < InspectMinimumMinutes {
			minutes = InspectMinimumMinutes
		}
		out = append(out, Task{
			ID:            "INSP-" + train.ID,
			Kind:          KindInspect,
			Subject:       train.ID,
			TrackID:       train.ReceivingID,
			Cars:          len(train.Cars),
			Minutes:       minutes,
			StartMinute:   train.ArrivalMinute,
			Qualification: model.QualInspect,
		})
	}
	for _, cut := range hp.Cuts {
		qual := model.QualHump
		if cut.RiderRequired {
			qual = model.QualRider
		}
		kind := KindHump
		if cut.RiderRequired {
			kind = KindRide
		}
		out = append(out, Task{
			ID:            fmt.Sprintf("CUT-%04d", cut.Index),
			Kind:          kind,
			Subject:       cut.TrainID,
			TrackID:       cut.TrackID,
			Cars:          len(cut.CarIDs),
			Minutes:       cut.EndMinute - cut.StartMinute,
			StartMinute:   cut.StartMinute,
			Qualification: qual,
		})
	}
	for _, fm := range hp.FlatMoves {
		out = append(out, Task{
			ID:            fmt.Sprintf("FLAT-%04d", fm.Index),
			Kind:          KindFlat,
			Subject:       fm.TrainID,
			TrackID:       fm.TrackID,
			Cars:          len(fm.CarIDs),
			Minutes:       fm.EndMinute - fm.StartMinute,
			StartMinute:   fm.StartMinute,
			Qualification: model.QualFlat,
		})
	}
	for _, tr := range dp.Trains {
		minutes := len(tr.Cars) + BuildSetupMinutes
		lead := 0
		if track, ok := cfg.DepartureByID(tr.TrackID); ok {
			lead = track.LeadMinutes
		}
		start := tr.DepartMinute - minutes - lead
		if start < 0 {
			start = 0
		}
		out = append(out, Task{
			ID:            "BUILD-" + tr.TrainID,
			Kind:          KindBuild,
			Subject:       tr.TrainID,
			TrackID:       tr.TrackID,
			Cars:          len(tr.Cars),
			Minutes:       minutes,
			StartMinute:   start,
			Qualification: model.QualRoadTrain,
		})
	}
	sortTasks(out)
	return out
}

// shiftFor returns the shift whose window covers a minute.
func shiftFor(cfg *config.Config, minute int) (model.Shift, bool) {
	for _, s := range cfg.Shifts {
		if minute >= s.StartMinute && minute < s.EndMinute() {
			return s, true
		}
	}
	return model.Shift{}, false
}

// pickCrew selects the least loaded qualified crew in a shift that can still
// absorb the task, or explains why none can.
func pickCrew(cfg *config.Config, states map[string]*crewState, sh model.Shift, task Task) (*crewState, string) {
	var candidates []*crewState
	for _, c := range cfg.Crews {
		st := states[c.ID]
		if st == nil || c.HomeShift != sh.ID {
			continue
		}
		if !c.Qualified(task.Qualification) {
			continue
		}
		candidates = append(candidates, st)
	}
	if len(candidates) == 0 {
		return nil, fmt.Sprintf("shift %s has no crew qualified for %s work", sh.ID, task.Qualification)
	}
	sort.SliceStable(candidates, func(i, j int) bool {
		if candidates[i].minutes != candidates[j].minutes {
			return candidates[i].minutes < candidates[j].minutes
		}
		return candidates[i].crew.ID < candidates[j].crew.ID
	})
	for _, st := range candidates {
		if st.minutes+task.Minutes <= st.crew.MaxDutyMinutes {
			return st, ""
		}
	}
	return nil, fmt.Sprintf("every %s crew in shift %s is out of duty hours", task.Qualification, sh.ID)
}

// crewLoads renders crew utilization in crew identifier order.
func crewLoads(cfg *config.Config, states map[string]*crewState) []CrewLoad {
	out := make([]CrewLoad, 0, len(cfg.Crews))
	for _, c := range cfg.Crews {
		st := states[c.ID]
		if st == nil {
			continue
		}
		load := CrewLoad{
			CrewID:     c.ID,
			ShiftID:    c.HomeShift,
			Tasks:      st.tasks,
			Minutes:    st.minutes,
			MaxMinutes: c.MaxDutyMinutes,
		}
		if c.MaxDutyMinutes > 0 {
			load.UtilPct = float64(st.minutes) * 100 / float64(c.MaxDutyMinutes)
		}
		out = append(out, load)
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].CrewID < out[j].CrewID })
	return out
}

// shiftLoads renders per-shift totals in shift order.
func shiftLoads(cfg *config.Config, tasks, minutes, cars map[string]int) []ShiftLoad {
	out := make([]ShiftLoad, 0, len(cfg.Shifts))
	for _, s := range cfg.Shifts {
		out = append(out, ShiftLoad{
			ShiftID:     s.ID,
			Tasks:       tasks[s.ID],
			Minutes:     minutes[s.ID],
			Cars:        cars[s.ID],
			StartMinute: s.StartMinute,
			EndMinute:   s.EndMinute(),
		})
	}
	return out
}

// digest computes shift plan statistics.
func digest(plan Plan) Stats {
	st := Stats{
		Tasks:      len(plan.Tasks),
		Assigned:   len(plan.Assignments),
		Unassigned: len(plan.Unassigned),
	}
	for _, t := range plan.Tasks {
		st.TotalMinutes += t.Minutes
	}
	for _, a := range plan.Assignments {
		st.CrewMinutes += a.Minutes
	}
	sum := 0.0
	for _, l := range plan.CrewLoads {
		sum += l.UtilPct
	}
	if len(plan.CrewLoads) > 0 {
		st.AvgUtilPct = sum / float64(len(plan.CrewLoads))
	}
	return st
}

// findings raises advisories about the shift plan.
func findings(cfg *config.Config, plan Plan) []config.Finding {
	var out []config.Finding
	for _, u := range plan.Unassigned {
		out = append(out, config.Finding{
			Severity: config.SeverityError,
			Scope:    "shift",
			Subject:  u.TaskID,
			Message:  u.Reason,
		})
	}
	for _, l := range plan.CrewLoads {
		if l.UtilPct > 95 {
			out = append(out, config.Finding{
				Severity: config.SeverityWarn,
				Scope:    "crew",
				Subject:  l.CrewID,
				Message:  fmt.Sprintf("crew is %.1f%% of its duty limit", l.UtilPct),
			})
		}
		if l.Tasks == 0 {
			out = append(out, config.Finding{
				Severity: config.SeverityWarn,
				Scope:    "crew",
				Subject:  l.CrewID,
				Message:  "crew received no work",
			})
		}
	}
	for _, l := range plan.ShiftLoads {
		s, ok := cfg.ShiftByID(l.ShiftID)
		if !ok {
			continue
		}
		if s.HumpCapacity > 0 && l.Cars > s.HumpCapacity {
			out = append(out, config.Finding{
				Severity: config.SeverityWarn,
				Scope:    "shift",
				Subject:  l.ShiftID,
				Message:  fmt.Sprintf("%d cars worked exceed the shift capacity of %d cars", l.Cars, s.HumpCapacity),
			})
		}
	}
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
	return out
}

// sortTasks orders tasks by start minute, then kind, then identifier.
func sortTasks(tasks []Task) {
	sort.SliceStable(tasks, func(i, j int) bool {
		a, b := tasks[i], tasks[j]
		if a.StartMinute != b.StartMinute {
			return a.StartMinute < b.StartMinute
		}
		if a.Kind != b.Kind {
			return a.Kind < b.Kind
		}
		return a.ID < b.ID
	})
}

// sortAssignments orders assignments by start minute, then task identifier.
func sortAssignments(as []Assignment) {
	sort.SliceStable(as, func(i, j int) bool {
		if as[i].StartMinute != as[j].StartMinute {
			return as[i].StartMinute < as[j].StartMinute
		}
		return as[i].TaskID < as[j].TaskID
	})
}

// AssignmentsFor returns the assignments handed to one crew.
func (p Plan) AssignmentsFor(crewID string) []Assignment {
	var out []Assignment
	for _, a := range p.Assignments {
		if a.CrewID == crewID {
			out = append(out, a)
		}
	}
	return out
}
