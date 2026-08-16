package model

import (
	"fmt"
	"sort"
	"strings"
)

// Crew qualifications.
const (
	// QualHump lets a crew work the crest and the trim end.
	QualHump = "hump"
	// QualFlat lets a crew flat switch cars that cannot be humped.
	QualFlat = "flat"
	// QualRider lets a crew ride cars down the bowl.
	QualRider = "rider"
	// QualRoadTrain lets a crew take a built train off the yard.
	QualRoadTrain = "road-train"
	// QualInspect lets a crew perform inbound inspections.
	QualInspect = "inspect"
)

// validQualifications is the closed set accepted by the decoder.
var validQualifications = []string{QualHump, QualFlat, QualRider, QualRoadTrain, QualInspect}

// Locomotive is a unit assigned to yard or road service.
type Locomotive struct {
	ID         string  `json:"id"`
	Model      string  `json:"model"`
	LengthFt   int     `json:"length_ft"`
	WeightTons float64 `json:"weight_tons"`
	Axles      int     `json:"axles"`
	RatedTons  float64 `json:"rated_tons"`
	Horsepower int     `json:"horsepower"`
	YardOnly   bool    `json:"yard_service_only"`
}

// Normalize canonicalizes identifiers.
func (l *Locomotive) Normalize() {
	l.ID = strings.ToUpper(strings.TrimSpace(l.ID))
	l.Model = strings.TrimSpace(l.Model)
}

// Validate checks a locomotive definition.
func (l Locomotive) Validate() error {
	if l.ID == "" {
		return fmt.Errorf("locomotive id is required")
	}
	if l.Model == "" {
		return fmt.Errorf("locomotive %q: model is required", l.ID)
	}
	if l.LengthFt < 30 || l.LengthFt > 120 {
		return fmt.Errorf("locomotive %q: length_ft %d out of range 30..120", l.ID, l.LengthFt)
	}
	if l.WeightTons <= 0 {
		return fmt.Errorf("locomotive %q: weight_tons must be positive", l.ID)
	}
	if l.Axles < 4 || l.Axles > 8 {
		return fmt.Errorf("locomotive %q: axles %d out of range 4..8", l.ID, l.Axles)
	}
	if l.RatedTons <= 0 {
		return fmt.Errorf("locomotive %q: rated_tons must be positive", l.ID)
	}
	if l.Horsepower < 500 || l.Horsepower > 8000 {
		return fmt.Errorf("locomotive %q: horsepower %d out of range 500..8000", l.ID, l.Horsepower)
	}
	return nil
}

// SortLocomotives orders units by identifier.
func SortLocomotives(units []Locomotive) {
	sort.SliceStable(units, func(i, j int) bool { return units[i].ID < units[j].ID })
}

// Crew is a switching or road crew available to the yard.
type Crew struct {
	ID             string   `json:"id"`
	Name           string   `json:"name"`
	Qualifications []string `json:"qualifications"`
	MaxDutyMinutes int      `json:"max_duty_minutes"`
	HomeShift      string   `json:"home_shift"`
}

// Normalize canonicalizes identifiers and sorts qualifications.
func (c *Crew) Normalize() {
	c.ID = strings.ToUpper(strings.TrimSpace(c.ID))
	c.Name = strings.TrimSpace(c.Name)
	c.HomeShift = strings.ToUpper(strings.TrimSpace(c.HomeShift))
	for i := range c.Qualifications {
		c.Qualifications[i] = strings.ToLower(strings.TrimSpace(c.Qualifications[i]))
	}
	sort.Strings(c.Qualifications)
}

// Validate checks a crew definition.
func (c Crew) Validate() error {
	if c.ID == "" {
		return fmt.Errorf("crew id is required")
	}
	if c.Name == "" {
		return fmt.Errorf("crew %q: name is required", c.ID)
	}
	if len(c.Qualifications) == 0 {
		return fmt.Errorf("crew %q: at least one qualification is required", c.ID)
	}
	seen := map[string]bool{}
	for _, q := range c.Qualifications {
		if !containsString(validQualifications, q) {
			return fmt.Errorf("crew %q: qualification %q not in %s", c.ID, q, strings.Join(validQualifications, ", "))
		}
		if seen[q] {
			return fmt.Errorf("crew %q: duplicate qualification %q", c.ID, q)
		}
		seen[q] = true
	}
	if c.MaxDutyMinutes < 60 || c.MaxDutyMinutes > 720 {
		return fmt.Errorf("crew %q: max_duty_minutes %d out of range 60..720", c.ID, c.MaxDutyMinutes)
	}
	if c.HomeShift == "" {
		return fmt.Errorf("crew %q: home_shift is required", c.ID)
	}
	return nil
}

// Qualified reports whether the crew holds the named qualification.
func (c Crew) Qualified(q string) bool {
	return containsString(c.Qualifications, q)
}

// SortCrews orders crews by identifier.
func SortCrews(crews []Crew) {
	sort.SliceStable(crews, func(i, j int) bool { return crews[i].ID < crews[j].ID })
}

// Shift is a working period during which crews are on duty.
type Shift struct {
	ID              string `json:"id"`
	Name            string `json:"name"`
	StartMinute     int    `json:"start_minute"`
	DurationMinutes int    `json:"duration_minutes"`
	HumpCapacity    int    `json:"hump_capacity_cars"`
	RiderCount      int    `json:"rider_count"`
}

// Normalize canonicalizes identifiers.
func (s *Shift) Normalize() {
	s.ID = strings.ToUpper(strings.TrimSpace(s.ID))
	s.Name = strings.TrimSpace(s.Name)
}

// Validate checks a shift definition.
func (s Shift) Validate() error {
	if s.ID == "" {
		return fmt.Errorf("shift id is required")
	}
	if s.Name == "" {
		return fmt.Errorf("shift %q: name is required", s.ID)
	}
	if s.StartMinute < 0 || s.StartMinute > 1439 {
		return fmt.Errorf("shift %q: start_minute %d out of range 0..1439", s.ID, s.StartMinute)
	}
	if s.DurationMinutes < 60 || s.DurationMinutes > 720 {
		return fmt.Errorf("shift %q: duration_minutes %d out of range 60..720", s.ID, s.DurationMinutes)
	}
	if s.HumpCapacity < 0 || s.HumpCapacity > 5000 {
		return fmt.Errorf("shift %q: hump_capacity_cars %d out of range 0..5000", s.ID, s.HumpCapacity)
	}
	if s.RiderCount < 0 || s.RiderCount > 20 {
		return fmt.Errorf("shift %q: rider_count %d out of range 0..20", s.ID, s.RiderCount)
	}
	return nil
}

// EndMinute is the exclusive end of the shift on a rolling clock.
func (s Shift) EndMinute() int {
	return s.StartMinute + s.DurationMinutes
}

// SortShifts orders shifts by start minute then identifier.
func SortShifts(shifts []Shift) {
	sort.SliceStable(shifts, func(i, j int) bool {
		if shifts[i].StartMinute != shifts[j].StartMinute {
			return shifts[i].StartMinute < shifts[j].StartMinute
		}
		return shifts[i].ID < shifts[j].ID
	})
}
