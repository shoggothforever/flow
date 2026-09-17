// Package config defines the on-disk schema for the flow CLI and the Store
// that loads, validates and atomically saves it.
package config

import (
	"errors"
	"fmt"
	"strings"
)

// CurrentSchemaVersion is bumped whenever a non-additive change to the schema lands.
const CurrentSchemaVersion = 2

// Config is the single root document persisted to JSON.
type Config struct {
	SchemaVersion    int               `json:"schema_version"`
	Projects         []Project         `json:"projects,omitempty"`
	Scripts          []Script          `json:"scripts,omitempty"`
	Cmds             []CmdAlias        `json:"cmds,omitempty"`
	Greps            []GrepPref        `json:"greps,omitempty"`
	Finds            []FindPref        `json:"finds,omitempty"`
	Schedules        []Schedule        `json:"schedules,omitempty"`
	TAPDRequirements []TAPDRequirement `json:"tapd_requirements,omitempty"`
	AgentSessions    []AgentSession    `json:"agent_sessions,omitempty"`
}

// Project is a directory bookmark.
type Project struct {
	Alias string   `json:"alias"`
	Path  string   `json:"path"`
	Tags  []string `json:"tags,omitempty"`
}

// Script is a named shell script invocation.
type Script struct {
	Alias   string   `json:"alias"`
	Cwd     string   `json:"cwd,omitempty"`  // optional working directory; supports paths and @project aliases
	Command string   `json:"command"`        // shell command string ("bash -c" style)
	Args    []string `json:"args,omitempty"` // appended verbatim
	Env     []string `json:"env,omitempty"`  // KEY=VAL pairs, applied on top of os env
}

// CmdAlias is a parametrised command template.
type CmdAlias struct {
	Alias    string            `json:"alias"`
	Template string            `json:"template"` // contains {{key}} or {{key:default}}
	Defaults map[string]string `json:"defaults,omitempty"`
	// Positional names how positional arguments map onto template
	// placeholders. Example: Positional=["port","proto"] means
	// `flow kill-port 8080 udp` is equivalent to `port=8080 proto=udp`.
	// Explicit key=value pairs always win over positional fills.
	Positional []string `json:"positional,omitempty"`
	Cwd        string   `json:"cwd,omitempty"`
}

// GrepPref captures a saved search preference.
type GrepPref struct {
	Name       string   `json:"name"`
	Pattern    string   `json:"pattern"`
	Paths      []string `json:"paths,omitempty"`
	Include    []string `json:"include,omitempty"` // glob filters
	Exclude    []string `json:"exclude,omitempty"`
	Regex      bool     `json:"regex,omitempty"`
	Word       bool     `json:"word,omitempty"`
	IgnoreCase bool     `json:"ignore_case,omitempty"`
}

// FindPref is a saved file-locator preference (mirrors finder.Query but in
// a serialisable form). Empty fields are treated as "not set".
type FindPref struct {
	Name           string   `json:"name"`
	Paths          []string `json:"paths,omitempty"`           // supports @project-alias
	NamePattern    string   `json:"name_pattern,omitempty"`    // glob, e.g. "*_test.go"
	Regex          string   `json:"regex,omitempty"`           // RE2 against basename
	Kind           string   `json:"kind,omitempty"`            // "f" | "d" | "l" (empty = any)
	Exts           []string `json:"exts,omitempty"`            // ".go", ".rs"
	ModifiedWithin string   `json:"modified_within,omitempty"` // 30m / 24h / 7d / 2w
	ModifiedAfter  string   `json:"modified_after,omitempty"`  // YYYY-MM-DD
	ModifiedBefore string   `json:"modified_before,omitempty"` // YYYY-MM-DD
	Size           string   `json:"size,omitempty"`            // +1M / -10K
	Exclude        []string `json:"exclude,omitempty"`
	Hidden         bool     `json:"hidden,omitempty"`
	Sort           string   `json:"sort,omitempty"` // mtime | name | size
	Reverse        bool     `json:"reverse,omitempty"`
	Limit          int      `json:"limit,omitempty"`
}

// Schedule binds a runnable (script / cmd / grep / find) to a recurring
// time expression. Exactly one of Cron / Every must be set.
type Schedule struct {
	Name      string            `json:"name"`
	Cron      string            `json:"cron,omitempty"`  // 5-field crontab, e.g. "0 */2 * * *"
	Every     string            `json:"every,omitempty"` // simplified: "every 2h", "daily 09:30", "weekdays 18:00"
	Kind      string            `json:"kind"`            // script | cmd | grep | find
	Target    string            `json:"target"`          // alias / name of the resource
	Vars      map[string]string `json:"vars,omitempty"`  // for kind=cmd
	ExtraArgs []string          `json:"extra_args,omitempty"`
	Enabled   bool              `json:"enabled"`
	OnFail    string            `json:"on_fail,omitempty"` // "log" (default) | "stop"
}

// Validate runs structural checks. It returns the first error found; callers
// should treat any returned error as user-facing.
func (c *Config) Validate() error {
	if c == nil {
		return errors.New("config is nil")
	}
	if c.SchemaVersion <= 0 {
		c.SchemaVersion = CurrentSchemaVersion
	}
	if c.SchemaVersion > CurrentSchemaVersion {
		return fmt.Errorf("schema_version %d is newer than supported %d (upgrade flow)", c.SchemaVersion, CurrentSchemaVersion)
	}

	if err := uniqueAliases("projects", projectAliases(c.Projects)); err != nil {
		return err
	}
	for i, p := range c.Projects {
		if strings.TrimSpace(p.Alias) == "" {
			return fmt.Errorf("projects[%d]: alias is required", i)
		}
		if strings.TrimSpace(p.Path) == "" {
			return fmt.Errorf("projects[%s]: path is required", p.Alias)
		}
	}

	if err := uniqueAliases("scripts", scriptAliases(c.Scripts)); err != nil {
		return err
	}
	for i, s := range c.Scripts {
		if strings.TrimSpace(s.Alias) == "" {
			return fmt.Errorf("scripts[%d]: alias is required", i)
		}
		if strings.TrimSpace(s.Command) == "" {
			return fmt.Errorf("scripts[%s]: command is required", s.Alias)
		}
	}

	if err := uniqueAliases("cmds", cmdAliases(c.Cmds)); err != nil {
		return err
	}
	for i, x := range c.Cmds {
		if strings.TrimSpace(x.Alias) == "" {
			return fmt.Errorf("cmds[%d]: alias is required", i)
		}
		if strings.TrimSpace(x.Template) == "" {
			return fmt.Errorf("cmds[%s]: template is required", x.Alias)
		}
	}

	if err := uniqueAliases("greps", grepNames(c.Greps)); err != nil {
		return err
	}
	for i, g := range c.Greps {
		if strings.TrimSpace(g.Name) == "" {
			return fmt.Errorf("greps[%d]: name is required", i)
		}
		if strings.TrimSpace(g.Pattern) == "" {
			return fmt.Errorf("greps[%s]: pattern is required", g.Name)
		}
	}

	if err := uniqueAliases("finds", findNames(c.Finds)); err != nil {
		return err
	}
	for i, f := range c.Finds {
		if strings.TrimSpace(f.Name) == "" {
			return fmt.Errorf("finds[%d]: name is required", i)
		}
		if len(f.Paths) == 0 {
			return fmt.Errorf("finds[%s]: at least one path is required", f.Name)
		}
		if k := strings.TrimSpace(f.Kind); k != "" && k != "f" && k != "d" && k != "l" {
			return fmt.Errorf("finds[%s]: kind must be one of f / d / l", f.Name)
		}
		if s := strings.TrimSpace(f.Sort); s != "" && s != "mtime" && s != "name" && s != "size" {
			return fmt.Errorf("finds[%s]: sort must be one of mtime / name / size", f.Name)
		}
	}

	if err := uniqueAliases("schedules", scheduleNames(c.Schedules)); err != nil {
		return err
	}
	for i, sc := range c.Schedules {
		if strings.TrimSpace(sc.Name) == "" {
			return fmt.Errorf("schedules[%d]: name is required", i)
		}
		hasCron := strings.TrimSpace(sc.Cron) != ""
		hasEvery := strings.TrimSpace(sc.Every) != ""
		if hasCron == hasEvery {
			return fmt.Errorf("schedules[%s]: exactly one of cron / every must be set", sc.Name)
		}
		switch sc.Kind {
		case "script", "cmd", "grep", "find":
		default:
			return fmt.Errorf("schedules[%s]: kind must be one of script / cmd / grep / find", sc.Name)
		}
		if strings.TrimSpace(sc.Target) == "" {
			return fmt.Errorf("schedules[%s]: target is required", sc.Name)
		}
		if of := strings.TrimSpace(sc.OnFail); of != "" && of != "log" && of != "stop" {
			return fmt.Errorf("schedules[%s]: on_fail must be 'log' or 'stop'", sc.Name)
		}
	}

	return c.validateAgentSessions()
}

// Mutators ---------------------------------------------------------------

// FindProject returns a pointer to the project with the given alias or nil.
func (c *Config) FindProject(alias string) *Project {
	for i := range c.Projects {
		if c.Projects[i].Alias == alias {
			return &c.Projects[i]
		}
	}
	return nil
}

// FindScript returns a pointer to the script with the given alias or nil.
func (c *Config) FindScript(alias string) *Script {
	for i := range c.Scripts {
		if c.Scripts[i].Alias == alias {
			return &c.Scripts[i]
		}
	}
	return nil
}

// FindCmd returns a pointer to the cmd alias or nil.
func (c *Config) FindCmd(alias string) *CmdAlias {
	for i := range c.Cmds {
		if c.Cmds[i].Alias == alias {
			return &c.Cmds[i]
		}
	}
	return nil
}

// FindGrep returns a pointer to the grep preset or nil.
func (c *Config) FindGrep(name string) *GrepPref {
	for i := range c.Greps {
		if c.Greps[i].Name == name {
			return &c.Greps[i]
		}
	}
	return nil
}

// FindFind returns a pointer to the find preset or nil.
func (c *Config) FindFind(name string) *FindPref {
	for i := range c.Finds {
		if c.Finds[i].Name == name {
			return &c.Finds[i]
		}
	}
	return nil
}

// FindSchedule returns a pointer to the schedule or nil.
func (c *Config) FindSchedule(name string) *Schedule {
	for i := range c.Schedules {
		if c.Schedules[i].Name == name {
			return &c.Schedules[i]
		}
	}
	return nil
}

// helpers --------------------------------------------------------------

func projectAliases(in []Project) []string {
	out := make([]string, len(in))
	for i, p := range in {
		out[i] = p.Alias
	}
	return out
}

func scriptAliases(in []Script) []string {
	out := make([]string, len(in))
	for i, s := range in {
		out[i] = s.Alias
	}
	return out
}

func cmdAliases(in []CmdAlias) []string {
	out := make([]string, len(in))
	for i, x := range in {
		out[i] = x.Alias
	}
	return out
}

func grepNames(in []GrepPref) []string {
	out := make([]string, len(in))
	for i, g := range in {
		out[i] = g.Name
	}
	return out
}

func findNames(in []FindPref) []string {
	out := make([]string, len(in))
	for i, f := range in {
		out[i] = f.Name
	}
	return out
}

func scheduleNames(in []Schedule) []string {
	out := make([]string, len(in))
	for i, s := range in {
		out[i] = s.Name
	}
	return out
}

func uniqueAliases(kind string, names []string) error {
	seen := make(map[string]struct{}, len(names))
	for _, n := range names {
		if _, ok := seen[n]; ok {
			return fmt.Errorf("%s: duplicate alias/name %q", kind, n)
		}
		seen[n] = struct{}{}
	}
	return nil
}
