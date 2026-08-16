// Package model holds the HumpYard domain types: rolling stock, yard tracks,
// power, crews and shifts. Types here are plain data with validation and
// deterministic ordering helpers; all planning logic lives in other packages.
package model

import (
	"fmt"
	"sort"
	"strings"
)

// Cut restriction values. A restriction narrows how a car may be switched.
const (
	// CutFree means the car may be humped without special handling.
	CutFree = "free"
	// CutNoHump means the car must never be shoved over the crest.
	CutNoHump = "no-hump"
	// CutFlatOnly means the car may only be moved by flat switching.
	CutFlatOnly = "flat-only"
	// CutCushion means the car has a cushioned draft gear and needs a
	// reduced retarder setting, but may still be humped.
	CutCushion = "cushion"
	// CutNoUncouple means the car must stay coupled to its drawbar mate.
	CutNoUncouple = "no-uncouple"
)

// validCutRestrictions is the closed set accepted by the decoder.
var validCutRestrictions = []string{CutFree, CutNoHump, CutFlatOnly, CutCushion, CutNoUncouple}

// Car is one piece of rolling stock in the yard.
type Car struct {
	Mark        string  `json:"mark"`
	Number      string  `json:"number"`
	Kind        string  `json:"kind"`
	LengthFt    int     `json:"length_ft"`
	TareTons    float64 `json:"tare_tons"`
	GrossTons   float64 `json:"gross_tons"`
	Axles       int     `json:"axles"`
	HazmatClass string  `json:"hazmat_class"`
	Placard     bool    `json:"placard"`
	Destination string  `json:"destination"`
	Restriction string  `json:"restriction"`
	DrawbarMate string  `json:"drawbar_mate"`
	EasyRoller  bool    `json:"easy_roller"`
	RoughRider  bool    `json:"rough_rider"`
	BadOrder    bool    `json:"bad_order"`
	BadOrderWhy string  `json:"bad_order_why"`
}

// ID returns the canonical car identifier, for example "BNSF 471203".
func (c Car) ID() string {
	return c.Mark + " " + c.Number
}

// Hazmat reports whether the car carries a regulated commodity.
func (c Car) Hazmat() bool {
	return strings.TrimSpace(c.HazmatClass) != ""
}

// LadingTons is the net weight riding on the car.
func (c Car) LadingTons() float64 {
	net := c.GrossTons - c.TareTons
	if net < 0 {
		return 0
	}
	return net
}

// Loaded reports whether the car carries lading.
func (c Car) Loaded() bool {
	return c.LadingTons() > 0.05
}

// Flat reports whether the car is barred from the hump by its restriction.
func (c Car) Flat() bool {
	return c.Restriction == CutNoHump || c.Restriction == CutFlatOnly
}

// Normalize trims surrounding whitespace, upper-cases identifiers and applies
// the default restriction so later comparisons are exact.
func (c *Car) Normalize() {
	c.Mark = strings.ToUpper(strings.TrimSpace(c.Mark))
	c.Number = strings.TrimSpace(c.Number)
	c.Kind = strings.ToLower(strings.TrimSpace(c.Kind))
	c.HazmatClass = strings.ToUpper(strings.TrimSpace(c.HazmatClass))
	c.Destination = strings.ToUpper(strings.TrimSpace(c.Destination))
	c.Restriction = strings.ToLower(strings.TrimSpace(c.Restriction))
	c.DrawbarMate = strings.ToUpper(strings.TrimSpace(c.DrawbarMate))
	c.BadOrderWhy = strings.TrimSpace(c.BadOrderWhy)
	if c.Restriction == "" {
		c.Restriction = CutFree
	}
}

// Validate checks a single car for internally consistent data.
func (c Car) Validate() error {
	if c.Mark == "" {
		return fmt.Errorf("car mark is required")
	}
	if !isAlphaUpper(c.Mark) {
		return fmt.Errorf("car %q: mark must be letters only", c.ID())
	}
	if c.Number == "" {
		return fmt.Errorf("car %q: number is required", c.Mark)
	}
	if !isDigits(c.Number) {
		return fmt.Errorf("car %q: number must be digits only", c.ID())
	}
	if c.Kind == "" {
		return fmt.Errorf("car %q: kind is required", c.ID())
	}
	if c.LengthFt <= 0 || c.LengthFt > 200 {
		return fmt.Errorf("car %q: length_ft %d out of range 1..200", c.ID(), c.LengthFt)
	}
	if c.TareTons <= 0 {
		return fmt.Errorf("car %q: tare_tons must be positive", c.ID())
	}
	if c.GrossTons < c.TareTons {
		return fmt.Errorf("car %q: gross_tons %.2f below tare_tons %.2f", c.ID(), c.GrossTons, c.TareTons)
	}
	if c.Axles < 2 || c.Axles > 16 || c.Axles%2 != 0 {
		return fmt.Errorf("car %q: axles %d must be an even number in 2..16", c.ID(), c.Axles)
	}
	if c.Destination == "" {
		return fmt.Errorf("car %q: destination is required", c.ID())
	}
	if c.Placard && !c.Hazmat() {
		return fmt.Errorf("car %q: placard set without hazmat_class", c.ID())
	}
	if !containsString(validCutRestrictions, c.Restriction) {
		return fmt.Errorf("car %q: restriction %q not in %s", c.ID(), c.Restriction, strings.Join(validCutRestrictions, ", "))
	}
	if c.Restriction == CutNoUncouple && c.DrawbarMate == "" {
		return fmt.Errorf("car %q: restriction %q requires drawbar_mate", c.ID(), CutNoUncouple)
	}
	if c.BadOrder && c.BadOrderWhy == "" {
		return fmt.Errorf("car %q: bad_order requires bad_order_why", c.ID())
	}
	if c.EasyRoller && c.RoughRider {
		return fmt.Errorf("car %q: cannot be both easy_roller and rough_rider", c.ID())
	}
	return nil
}

// SortCars orders cars by identifier, which is the project-wide tie-break.
func SortCars(cars []Car) {
	sort.SliceStable(cars, func(i, j int) bool { return cars[i].ID() < cars[j].ID() })
}

// CarIndex maps car identifiers to cars for quick lookups.
type CarIndex map[string]Car

// NewCarIndex builds an index and reports the first duplicate identifier.
func NewCarIndex(cars []Car) (CarIndex, error) {
	idx := make(CarIndex, len(cars))
	for _, c := range cars {
		if _, dup := idx[c.ID()]; dup {
			return nil, fmt.Errorf("duplicate car %q", c.ID())
		}
		idx[c.ID()] = c
	}
	return idx, nil
}

// IDs returns the indexed identifiers in lexical order.
func (idx CarIndex) IDs() []string {
	out := make([]string, 0, len(idx))
	for id := range idx {
		out = append(out, id)
	}
	sort.Strings(out)
	return out
}

// TotalLengthFt sums car lengths.
func TotalLengthFt(cars []Car) int {
	total := 0
	for _, c := range cars {
		total += c.LengthFt
	}
	return total
}

// TotalTons sums gross weights.
func TotalTons(cars []Car) float64 {
	total := 0.0
	for _, c := range cars {
		total += c.GrossTons
	}
	return total
}

// TotalAxles sums axle counts.
func TotalAxles(cars []Car) int {
	total := 0
	for _, c := range cars {
		total += c.Axles
	}
	return total
}

// isAlphaUpper reports whether s consists solely of A-Z characters.
func isAlphaUpper(s string) bool {
	if s == "" {
		return false
	}
	for _, r := range s {
		if r < 'A' || r > 'Z' {
			return false
		}
	}
	return true
}

// isDigits reports whether s consists solely of decimal digits.
func isDigits(s string) bool {
	if s == "" {
		return false
	}
	for _, r := range s {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}

// containsString reports whether list holds want.
func containsString(list []string, want string) bool {
	for _, item := range list {
		if item == want {
			return true
		}
	}
	return false
}

// ContainsString is the exported form of containsString for sibling packages.
func ContainsString(list []string, want string) bool {
	return containsString(list, want)
}
