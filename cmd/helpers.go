package cmd

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/yithcai/flow/internal/config"
	"github.com/yithcai/flow/internal/errs"
	"github.com/yithcai/flow/internal/matcher"
)

// loadStore returns a Store using the global --config / FLOW_CONFIG / default chain.
func loadStore() (*config.Store, error) {
	s, err := config.NewStore(flagConfigPath)
	if err != nil {
		return nil, errs.UserWrap(err, "could not resolve config path",
			"set FLOW_CONFIG=/path/to/config.json or pass --config /path")
	}
	return s, nil
}

// expandPath expands a leading ~ and any ${VAR}/$VAR references against the env.
func expandPath(p string) (string, error) {
	if p == "" {
		return "", nil
	}
	if p[0] == '~' {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", err
		}
		if p == "~" {
			p = home
		} else if p[1] == '/' {
			p = filepath.Join(home, p[2:])
		}
	}
	return os.ExpandEnv(p), nil
}

// resolveProjectAlias finds a project by exact alias or unique fuzzy match.
// Returns a user-facing error with hint on miss/ambiguity.
func resolveProjectAlias(c *config.Config, query string) (*config.Project, error) {
	names := make([]string, len(c.Projects))
	for i, p := range c.Projects {
		names[i] = p.Alias
	}
	name, ok, tied := matcher.Resolve(query, names)
	if !ok {
		if len(tied) > 0 {
			return nil, errs.User(
				fmt.Sprintf("project alias %q is ambiguous: matches %s", query, strings.Join(tied, ", ")),
				"use a more specific alias or run  flow project list",
			)
		}
		return nil, errs.User(
			fmt.Sprintf("no project matches %q", query),
			"register one first:  flow project add <alias> <path>",
		)
	}
	return c.FindProject(name), nil
}

func resolveScript(c *config.Config, q string) (*config.Script, error) {
	names := make([]string, len(c.Scripts))
	for i, s := range c.Scripts {
		names[i] = s.Alias
	}
	name, ok, tied := matcher.Resolve(q, names)
	if !ok {
		if len(tied) > 0 {
			return nil, errs.User(
				fmt.Sprintf("script %q is ambiguous: matches %s", q, strings.Join(tied, ", ")),
				"use the full script alias",
			)
		}
		return nil, errs.User(
			fmt.Sprintf("no script matches %q", q),
			"register one:  flow script add <alias> --command \"...\"",
		)
	}
	return c.FindScript(name), nil
}

func resolveCmd(c *config.Config, q string) (*config.CmdAlias, error) {
	names := make([]string, len(c.Cmds))
	for i, x := range c.Cmds {
		names[i] = x.Alias
	}
	name, ok, tied := matcher.Resolve(q, names)
	if !ok {
		if len(tied) > 0 {
			return nil, errs.User(
				fmt.Sprintf("cmd %q is ambiguous: matches %s", q, strings.Join(tied, ", ")),
				"use the full cmd alias",
			)
		}
		return nil, errs.User(
			fmt.Sprintf("no cmd matches %q", q),
			"register one:  flow cmd add <alias> --template \"...\"",
		)
	}
	return c.FindCmd(name), nil
}

func resolveGrep(c *config.Config, q string) (*config.GrepPref, error) {
	names := make([]string, len(c.Greps))
	for i, g := range c.Greps {
		names[i] = g.Name
	}
	name, ok, tied := matcher.Resolve(q, names)
	if !ok {
		if len(tied) > 0 {
			return nil, errs.User(
				fmt.Sprintf("grep preset %q is ambiguous: matches %s", q, strings.Join(tied, ", ")),
				"use the full preset name",
			)
		}
		return nil, errs.User(
			fmt.Sprintf("no grep preset matches %q", q),
			"save one:  flow grep save <name> --pattern \"...\"",
		)
	}
	return c.FindGrep(name), nil
}

func resolveFind(c *config.Config, q string) (*config.FindPref, error) {
	names := make([]string, len(c.Finds))
	for i, f := range c.Finds {
		names[i] = f.Name
	}
	name, ok, tied := matcher.Resolve(q, names)
	if !ok {
		if len(tied) > 0 {
			return nil, errs.User(
				fmt.Sprintf("find preset %q is ambiguous: matches %s", q, strings.Join(tied, ", ")),
				"use the full preset name",
			)
		}
		return nil, errs.User(
			fmt.Sprintf("no find preset matches %q", q),
			"save one:  flow find save <name> --path @<proj> ...",
		)
	}
	return c.FindFind(name), nil
}

// expandCwd resolves a cwd field that may be either a project alias prefixed
// with @ or a regular filesystem path.
func expandCwd(c *config.Config, raw string) (string, error) {
	if raw == "" {
		return "", nil
	}
	if strings.HasPrefix(raw, "@") {
		alias := strings.TrimPrefix(raw, "@")
		p := c.FindProject(alias)
		if p == nil {
			return "", errs.User(
				fmt.Sprintf("@%s references an unknown project", alias),
				"register it first:  flow project add "+alias+" /path/to/dir",
			)
		}
		return expandPath(p.Path)
	}
	return expandPath(raw)
}

// printTable prints a simple two-column table to stdout.
func printTable(headers []string, rows [][]string) {
	widths := make([]int, len(headers))
	for i, h := range headers {
		widths[i] = len(h)
	}
	for _, row := range rows {
		for i, c := range row {
			if i >= len(widths) {
				continue
			}
			if len(c) > widths[i] {
				widths[i] = len(c)
			}
		}
	}
	pad := func(s string, w int) string {
		if len(s) >= w {
			return s
		}
		return s + strings.Repeat(" ", w-len(s))
	}
	headerCells := make([]string, len(headers))
	for i, h := range headers {
		headerCells[i] = pad(h, widths[i])
	}
	fmt.Println(strings.Join(headerCells, "  "))
	for _, row := range rows {
		cells := make([]string, len(row))
		for i, c := range row {
			cells[i] = pad(c, widths[i])
		}
		fmt.Println(strings.Join(cells, "  "))
	}
}
