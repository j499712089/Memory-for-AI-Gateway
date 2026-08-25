package skill

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
)

// SemVer is a parsed semantic version in the shape "MAJOR.MINOR.PATCH".
// Pre-release labels are accepted but not advanced by the bump helpers.
type SemVer struct {
	Major      int
	Minor      int
	Patch      int
	Prerelease string
}

// ParseSemVer parses a SemVer string. It accepts the standard three-part
// form with an optional "-prerelease" suffix.
func ParseSemVer(value string) (SemVer, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return SemVer{}, fmt.Errorf("semver is empty")
	}
	core := value
	if index := strings.IndexByte(value, '-'); index >= 0 {
		core = value[:index]
	}
	parts := strings.Split(core, ".")
	if len(parts) != 3 {
		return SemVer{}, fmt.Errorf("semver %q must have major.minor.patch", value)
	}
	parsed := SemVer{}
	var err error
	if parsed.Major, err = strconv.Atoi(parts[0]); err != nil {
		return SemVer{}, fmt.Errorf("semver major %q: %w", parts[0], err)
	}
	if parsed.Minor, err = strconv.Atoi(parts[1]); err != nil {
		return SemVer{}, fmt.Errorf("semver minor %q: %w", parts[1], err)
	}
	if parsed.Patch, err = strconv.Atoi(parts[2]); err != nil {
		return SemVer{}, fmt.Errorf("semver patch %q: %w", parts[2], err)
	}
	if parsed.Major < 0 || parsed.Minor < 0 || parsed.Patch < 0 {
		return SemVer{}, fmt.Errorf("semver %q must not contain negative numbers", value)
	}
	if index := strings.IndexByte(value, '-'); index >= 0 {
		parsed.Prerelease = value[index+1:]
	}
	return parsed, nil
}

// String renders the version back to its canonical form.
func (v SemVer) String() string {
	if v.Prerelease != "" {
		return fmt.Sprintf("%d.%d.%d-%s", v.Major, v.Minor, v.Patch, v.Prerelease)
	}
	return fmt.Sprintf("%d.%d.%d", v.Major, v.Minor, v.Patch)
}

// NextPatch returns the next patch version (default bump for skill fixes).
func (v SemVer) NextPatch() SemVer {
	v.Prerelease = ""
	v.Patch++
	return v
}

// NextMinor returns the next minor version (default bump for new behaviour).
func (v SemVer) NextMinor() SemVer {
	v.Prerelease = ""
	v.Minor++
	v.Patch = 0
	return v
}

// NextMajor returns the next major version (breaking change).
func (v SemVer) NextMajor() SemVer {
	v.Prerelease = ""
	v.Major++
	v.Minor = 0
	v.Patch = 0
	return v
}

// NextVersion advances a version string by the given bump level, defaulting
// to patch when bump is empty.
func NextVersion(current, bump string) (string, error) {
	parsed, err := ParseSemVer(current)
	if err != nil {
		return "", err
	}
	switch strings.ToLower(strings.TrimSpace(bump)) {
	case "major":
		return parsed.NextMajor().String(), nil
	case "minor":
		return parsed.NextMinor().String(), nil
	default: // patch is the conservative default
		return parsed.NextPatch().String(), nil
	}
}

// Lifecycle transitions allowed by the skill store.
var validTransitions = map[string][]string{
	"candidate":  {"approved", "deprecated"},
	"approved":   {"deprecated"},
	"deprecated": {"candidate"},
}

// ValidTransition reports whether moving from -> to is allowed.
func ValidTransition(from, to string) bool {
	allowed, ok := validTransitions[from]
	if !ok {
		return false
	}
	for _, next := range allowed {
		if next == to {
			return true
		}
	}
	return false
}

// Step is one executable step of a skill.
type Step struct {
	Order int    `json:"order"`
	Title string `json:"title"`
	Body  string `json:"body"`
}

// Validation rules attached to a skill.
type Validation struct {
	InputSchema   map[string]any `json:"input_schema,omitempty"`
	OutputSchema  map[string]any `json:"output_schema,omitempty"`
	Required      []string       `json:"required,omitempty"`
	PassCriteria  string         `json:"pass_criteria,omitempty"`
	ErrorBoundary string         `json:"error_boundary,omitempty"`
}

// Skill is the domain model validated by the skill_review worker before it
// reaches the db layer. It mirrors db.Skill without importing it so the
// package stays dependency-light.
type Skill struct {
	Name            string     `json:"name"`
	DisplayName     string     `json:"display_name,omitempty"`
	Version         string     `json:"version"`
	Status          string     `json:"status"`
	Scope           string     `json:"scope"`
	TriggerBoundary string     `json:"trigger_boundary,omitempty"`
	Steps           []Step     `json:"steps,omitempty"`
	Validation      Validation `json:"validation,omitempty"`
	SourceIDs       []string   `json:"source_ids,omitempty"`
	ResourceRefs    []string   `json:"resource_refs,omitempty"`
	Entrypoint      string     `json:"entrypoint,omitempty"`
	ManifestPath    string     `json:"manifest_path,omitempty"`
}

// Validate checks the required fields of a skill. A skill is only eligible
// for approval when it carries a stable name, a SemVer version, at least one
// step, and a trigger boundary.
func (s Skill) Validate() error {
	if strings.TrimSpace(s.Name) == "" {
		return fmt.Errorf("skill name is required")
	}
	for _, char := range s.Name {
		if !(char == '-' || (char >= 'a' && char <= 'z') || (char >= '0' && char <= '9')) {
			return fmt.Errorf("skill name %q must be kebab-case (lowercase letters, digits, dashes)", s.Name)
		}
	}
	if s.Version == "" {
		return fmt.Errorf("skill version is required")
	}
	if _, err := ParseSemVer(s.Version); err != nil {
		return fmt.Errorf("skill version: %w", err)
	}
	if s.Status != "" && !ValidStatus(s.Status) {
		return fmt.Errorf("skill status %q is not valid", s.Status)
	}
	if s.Scope != "" && s.Scope != "personal" && s.Scope != "team" {
		return fmt.Errorf("skill scope %q must be personal or team", s.Scope)
	}
	if len(s.Steps) == 0 {
		return fmt.Errorf("skill must declare at least one step")
	}
	if strings.TrimSpace(s.TriggerBoundary) == "" {
		return fmt.Errorf("skill trigger_boundary is required")
	}
	return nil
}

// ValidStatus reports whether status is a known lifecycle state.
func ValidStatus(status string) bool {
	switch status {
	case "candidate", "approved", "deprecated":
		return true
	}
	return false
}

// ToDBShape converts the domain model into the JSON fragments the db layer
// stores. It keeps the wire format stable across worker restarts.
func (s Skill) ToDBShape() (stepsJSON, validationJSON, sourceJSON, resourceJSON []byte) {
	stepsJSON, _ = json.Marshal(s.Steps)
	validationJSON, _ = json.Marshal(s.Validation)
	sourceJSON, _ = json.Marshal(s.SourceIDs)
	resourceJSON, _ = json.Marshal(s.ResourceRefs)
	return stepsJSON, validationJSON, sourceJSON, resourceJSON
}
