package model

import (
	"fmt"
	"sort"
	"strings"
)

// Track restriction values applied to classification tracks.
const (
	// ResNoHazmat bars any car with a hazmat class.
	ResNoHazmat = "no-hazmat"
	// ResNoPlacard bars placarded cars but allows residue.
	ResNoPlacard = "no-placard"
	// ResNoRoughRider bars cars that roll badly.
	ResNoRoughRider = "no-rough-rider"
	// ResNoLongCar bars cars above the yard long-car threshold.
	ResNoLongCar = "no-long-car"
	// ResFlatOnly means the track is only reachable by flat switching.
	ResFlatOnly = "flat-only"
	// ResNoLoaded bars loaded cars, for example on a light repair track.
	ResNoLoaded = "no-loaded"
)

// validTrackRestrictions is the closed set accepted by the decoder.
var validTrackRestrictions = []string{
	ResNoHazmat, ResNoPlacard, ResNoRoughRider, ResNoLongCar, ResFlatOnly, ResNoLoaded,
}

// Block is a group of destinations that ride together to the same next yard.
type Block struct {
	ID           string   `json:"id"`
	Name         string   `json:"name"`
	Priority     int      `json:"priority"`
	Destinations []string `json:"destinations"`
	FlatSwitch   bool     `json:"flat_switch"`
}

// Normalize upper-cases identifiers and sorts destinations.
func (b *Block) Normalize() {
	b.ID = strings.ToUpper(strings.TrimSpace(b.ID))
	b.Name = strings.TrimSpace(b.Name)
	for i := range b.Destinations {
		b.Destinations[i] = strings.ToUpper(strings.TrimSpace(b.Destinations[i]))
	}
	sort.Strings(b.Destinations)
}

// Validate checks a block definition.
func (b Block) Validate() error {
	if b.ID == "" {
		return fmt.Errorf("block id is required")
	}
	if b.Name == "" {
		return fmt.Errorf("block %q: name is required", b.ID)
	}
	if b.Priority < 1 || b.Priority > 999 {
		return fmt.Errorf("block %q: priority %d out of range 1..999", b.ID, b.Priority)
	}
	if len(b.Destinations) == 0 {
		return fmt.Errorf("block %q: at least one destination is required", b.ID)
	}
	seen := map[string]bool{}
	for _, d := range b.Destinations {
		if d == "" {
			return fmt.Errorf("block %q: empty destination", b.ID)
		}
		if seen[d] {
			return fmt.Errorf("block %q: duplicate destination %q", b.ID, d)
		}
		seen[d] = true
	}
	return nil
}

// ClassTrack is a classification (bowl) track that receives humped cars.
type ClassTrack struct {
	ID              string   `json:"id"`
	Block           string   `json:"block"`
	CapacityFt      int      `json:"capacity_ft"`
	WeightLimitTons float64  `json:"weight_limit_tons"`
	GradePct        float64  `json:"grade_pct"`
	Restrictions    []string `json:"restrictions"`
	CabooseSpot     bool     `json:"caboose_spot"`
	RetarderID      string   `json:"retarder_id"`
}

// Normalize canonicalizes identifiers and sorts restrictions.
func (t *ClassTrack) Normalize() {
	t.ID = strings.ToUpper(strings.TrimSpace(t.ID))
	t.Block = strings.ToUpper(strings.TrimSpace(t.Block))
	t.RetarderID = strings.ToUpper(strings.TrimSpace(t.RetarderID))
	for i := range t.Restrictions {
		t.Restrictions[i] = strings.ToLower(strings.TrimSpace(t.Restrictions[i]))
	}
	sort.Strings(t.Restrictions)
}

// Validate checks a classification track definition.
func (t ClassTrack) Validate() error {
	if t.ID == "" {
		return fmt.Errorf("class track id is required")
	}
	if t.Block == "" {
		return fmt.Errorf("class track %q: block is required", t.ID)
	}
	if t.CapacityFt < 100 || t.CapacityFt > 20000 {
		return fmt.Errorf("class track %q: capacity_ft %d out of range 100..20000", t.ID, t.CapacityFt)
	}
	if t.WeightLimitTons <= 0 {
		return fmt.Errorf("class track %q: weight_limit_tons must be positive", t.ID)
	}
	if t.GradePct < -5 || t.GradePct > 5 {
		return fmt.Errorf("class track %q: grade_pct %.2f out of range -5..5", t.ID, t.GradePct)
	}
	seen := map[string]bool{}
	for _, r := range t.Restrictions {
		if !containsString(validTrackRestrictions, r) {
			return fmt.Errorf("class track %q: restriction %q not in %s", t.ID, r, strings.Join(validTrackRestrictions, ", "))
		}
		if seen[r] {
			return fmt.Errorf("class track %q: duplicate restriction %q", t.ID, r)
		}
		seen[r] = true
	}
	return nil
}

// HasRestriction reports whether the track carries the named restriction.
func (t ClassTrack) HasRestriction(name string) bool {
	return containsString(t.Restrictions, name)
}

// Accepts reports whether a car may be classified onto the track, returning a
// human readable reason when it may not. longCarFt is the yard threshold above
// which a car counts as a long car.
func (t ClassTrack) Accepts(c Car, longCarFt int) (bool, string) {
	if t.HasRestriction(ResNoHazmat) && c.Hazmat() {
		return false, "track bars hazmat"
	}
	if t.HasRestriction(ResNoPlacard) && c.Placard {
		return false, "track bars placarded cars"
	}
	if t.HasRestriction(ResNoRoughRider) && c.RoughRider {
		return false, "track bars rough riders"
	}
	if t.HasRestriction(ResNoLongCar) && c.LengthFt > longCarFt {
		return false, fmt.Sprintf("track bars cars longer than %d ft", longCarFt)
	}
	if t.HasRestriction(ResNoLoaded) && c.Loaded() {
		return false, "track bars loaded cars"
	}
	return true, ""
}

// SortClassTracks orders tracks by identifier.
func SortClassTracks(tracks []ClassTrack) {
	sort.SliceStable(tracks, func(i, j int) bool { return tracks[i].ID < tracks[j].ID })
}

// SupportTrack is a receiving or departure track. It has no block assignment.
type SupportTrack struct {
	ID              string  `json:"id"`
	Role            string  `json:"role"`
	CapacityFt      int     `json:"capacity_ft"`
	WeightLimitTons float64 `json:"weight_limit_tons"`
	LeadMinutes     int     `json:"lead_minutes"`
}

// Support track roles.
const (
	RoleReceiving = "receiving"
	RoleDeparture = "departure"
)

// Normalize canonicalizes identifiers and role names.
func (t *SupportTrack) Normalize() {
	t.ID = strings.ToUpper(strings.TrimSpace(t.ID))
	t.Role = strings.ToLower(strings.TrimSpace(t.Role))
}

// Validate checks a support track definition against the expected role.
func (t SupportTrack) Validate(wantRole string) error {
	if t.ID == "" {
		return fmt.Errorf("%s track id is required", wantRole)
	}
	if t.Role != wantRole {
		return fmt.Errorf("track %q: role %q must be %q", t.ID, t.Role, wantRole)
	}
	if t.CapacityFt < 100 || t.CapacityFt > 30000 {
		return fmt.Errorf("track %q: capacity_ft %d out of range 100..30000", t.ID, t.CapacityFt)
	}
	if t.WeightLimitTons <= 0 {
		return fmt.Errorf("track %q: weight_limit_tons must be positive", t.ID)
	}
	if t.LeadMinutes < 0 || t.LeadMinutes > 600 {
		return fmt.Errorf("track %q: lead_minutes %d out of range 0..600", t.ID, t.LeadMinutes)
	}
	return nil
}

// SortSupportTracks orders support tracks by identifier.
func SortSupportTracks(tracks []SupportTrack) {
	sort.SliceStable(tracks, func(i, j int) bool { return tracks[i].ID < tracks[j].ID })
}
