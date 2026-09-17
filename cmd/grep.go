package cmd

import (
	"fmt"
	"os"
	"strings"

	"github.com/spf13/cobra"

	"github.com/yithcai/flow/internal/config"
	"github.com/yithcai/flow/internal/errs"
	"github.com/yithcai/flow/internal/search"
)

func init() {
	grepCmd := &cobra.Command{
		Use:   "grep",
		Short: "Saved grep/ripgrep presets",
	}

	// save ---------------------------------------------------------------
	var savePattern string
	var savePaths, saveInclude, saveExclude []string
	var saveRegex, saveWord, saveIgnoreCase bool
	saveCmd := &cobra.Command{
		Use:   "save <name>",
		Short: "Save a grep preset",
		Args:  cobra.ExactArgs(1),
		RunE: func(_ *cobra.Command, args []string) error {
			name := args[0]
			if strings.TrimSpace(savePattern) == "" {
				return errs.User("--pattern is required",
					`example:  flow grep save todos --pattern TODO --include "*.go" -p .`)
			}
			s, err := loadStore()
			if err != nil {
				return err
			}
			return s.Mutate(func(c *config.Config) error {
				if c.FindGrep(name) != nil {
					return errs.User(
						fmt.Sprintf("grep preset %q already exists", name),
						"remove it first:  flow grep rm "+name,
					)
				}
				c.Greps = append(c.Greps, config.GrepPref{
					Name:       name,
					Pattern:    savePattern,
					Paths:      savePaths,
					Include:    saveInclude,
					Exclude:    saveExclude,
					Regex:      saveRegex,
					Word:       saveWord,
					IgnoreCase: saveIgnoreCase,
				})
				fmt.Printf("Saved grep preset %s\n", name)
				return nil
			})
		},
	}
	saveCmd.Flags().StringVarP(&savePattern, "pattern", "e", "", "search pattern")
	saveCmd.Flags().StringSliceVarP(&savePaths, "path", "p", nil, "search path; repeatable; supports @project-alias")
	saveCmd.Flags().StringSliceVar(&saveInclude, "include", nil, "include glob; repeatable")
	saveCmd.Flags().StringSliceVar(&saveExclude, "exclude", nil, "exclude glob; repeatable")
	saveCmd.Flags().BoolVar(&saveRegex, "regex", false, "treat pattern as a regex (default: literal)")
	saveCmd.Flags().BoolVarP(&saveWord, "word", "w", false, "match whole words")
	saveCmd.Flags().BoolVarP(&saveIgnoreCase, "ignore-case", "i", false, "case-insensitive")

	// list ---------------------------------------------------------------
	listCmd := &cobra.Command{
		Use:   "list",
		Short: "List grep presets",
		Args:  cobra.NoArgs,
		RunE: func(_ *cobra.Command, _ []string) error {
			s, err := loadStore()
			if err != nil {
				return err
			}
			c, err := s.Load()
			if err != nil {
				return err
			}
			if len(c.Greps) == 0 {
				fmt.Println("(no grep presets yet -- save one with: flow grep save ...)")
				return nil
			}
			rows := make([][]string, 0, len(c.Greps))
			for _, g := range c.Greps {
				flags := flagsString(g)
				rows = append(rows, []string{
					g.Name, g.Pattern, strings.Join(g.Paths, " "), flags,
				})
			}
			printTable([]string{"NAME", "PATTERN", "PATHS", "FLAGS"}, rows)
			return nil
		},
	}

	// rm -----------------------------------------------------------------
	rmCmd := &cobra.Command{
		Use:   "rm <name>",
		Short: "Remove a grep preset",
		Args:  cobra.ExactArgs(1),
		RunE: func(_ *cobra.Command, args []string) error {
			s, err := loadStore()
			if err != nil {
				return err
			}
			return s.Mutate(func(c *config.Config) error {
				for i, g := range c.Greps {
					if g.Name == args[0] {
						c.Greps = append(c.Greps[:i], c.Greps[i+1:]...)
						fmt.Printf("Removed grep preset %s\n", args[0])
						return nil
					}
				}
				return errs.User(
					fmt.Sprintf("no grep preset named %q", args[0]),
					"see available presets:  flow grep list",
				)
			})
		},
	}

	// run ----------------------------------------------------------------
	var runPattern string
	var runDryRun bool
	runCmd := &cobra.Command{
		Use:   "run [<name>]",
		Short: "Run a saved grep preset (omit <name> to pick interactively)",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(_ *cobra.Command, args []string) error {
			s, err := loadStore()
			if err != nil {
				return err
			}
			cfg, err := s.Load()
			if err != nil {
				return err
			}
			name, err := pickFromConfig(args, grepItems(cfg), "grep preset",
				`save one:  flow grep save <name> --pattern "..."`)
			if err != nil {
				return err
			}
			g, err := resolveGrep(cfg, name)
			if err != nil {
				return err
			}
			pattern := g.Pattern
			if runPattern != "" {
				pattern = runPattern
			}
			paths, err := resolveGrepPaths(cfg, g.Paths)
			if err != nil {
				return err
			}
			backend := search.Detect()
			if backend == search.BackendNone {
				return errs.User(
					"no search backend available",
					"install ripgrep:  cargo install ripgrep   or:  sudo yum install -y ripgrep",
				)
			}
			binName, args2, err := search.BuildArgs(backend, search.Spec{
				Pattern: pattern, Paths: paths, Include: g.Include, Exclude: g.Exclude,
				Regex: g.Regex, Word: g.Word, IgnoreCase: g.IgnoreCase,
			})
			if err != nil {
				return errs.UserWrap(err, "could not build search command",
					"check the saved preset:  flow grep list")
			}
			if runDryRun {
				fmt.Printf("would run: %s %s\n", binName, strings.Join(args2, " "))
				return nil
			}
			cwd, _ := os.Getwd()
			code, runErr := search.Run(cwd, binName, args2)
			if runErr != nil {
				return errs.System("failed to start "+binName, runErr)
			}
			if code != 0 && code != 1 { // 1 = no matches in rg/grep
				return errs.System(fmt.Sprintf("%s exited with code %d", binName, code), nil)
			}
			return nil
		},
	}
	runCmd.Flags().StringVarP(&runPattern, "pattern", "e", "", "override the saved pattern")
	runCmd.Flags().BoolVar(&runDryRun, "dry-run", false, "print the resolved command without executing")

	// pattern --------------------------------------------------------------
	// Print the (possibly combined) pattern of one or more presets so it can
	// be embedded in shell pipelines:
	//
	//   tail -F some.log | grep -E "$(flow grep pattern phase doors-open)"
	//
	// When multiple names are given, their patterns are OR-ed:  p1|p2|p3.
	// Names can be repeated as comma-separated in a single arg too:
	//
	//   flow grep pattern phase,doors-open
	patternCmd := &cobra.Command{
		Use:   "pattern <name> [<name>...]",
		Short: "Print the pattern (OR-combined for multiple names) for use in shell pipelines",
		Long: `Examples:
  flow grep pattern phase                       -> EnterPhase
  flow grep pattern phase doors-open            -> EnterPhase|OpenBattleAreaDoors
  flow grep pattern phase,doors-open,doors-close
  tail -F app.log | grep --line-buffered -E "$(flow grep pattern phase doors-open)"
`,
		Args: cobra.MinimumNArgs(1),
		RunE: func(_ *cobra.Command, args []string) error {
			s, err := loadStore()
			if err != nil {
				return err
			}
			cfg, err := s.Load()
			if err != nil {
				return err
			}
			pat, err := buildCombinedPattern(cfg, args)
			if err != nil {
				return err
			}
			fmt.Println(pat)
			return nil
		},
	}

	grepCmd.AddCommand(saveCmd, listCmd, rmCmd, runCmd, patternCmd)
	rootCmd.AddCommand(grepCmd)
}

// buildCombinedPattern resolves the given preset names (space- or
// comma-separated) and returns a single ERE-safe pattern. Multiple
// patterns are wrapped in (...) and OR-joined.
//
// Used by `flow grep pattern` and by `flow script run --grep`.
func buildCombinedPattern(cfg *config.Config, names []string) (string, error) {
	expanded := []string{}
	for _, a := range names {
		for _, p := range strings.Split(a, ",") {
			if p = strings.TrimSpace(p); p != "" {
				expanded = append(expanded, p)
			}
		}
	}
	if len(expanded) == 0 {
		return "", errs.User("--grep requires at least one preset name",
			`example:  --grep phase           or    --grep phase,doors-open`)
	}
	pats := make([]string, 0, len(expanded))
	for _, n := range expanded {
		g, err := resolveGrep(cfg, n)
		if err != nil {
			return "", err
		}
		pat := strings.TrimSpace(g.Pattern)
		if pat == "" {
			return "", errs.User(
				fmt.Sprintf("grep preset %q has no pattern", g.Name),
				"edit it:  flow grep save "+g.Name+" --pattern ...",
			)
		}
		if !g.Regex {
			pat = regexpEscape(pat)
		}
		pats = append(pats, pat)
	}
	if len(pats) == 1 {
		return pats[0], nil
	}
	wrapped := make([]string, len(pats))
	for i, p := range pats {
		wrapped[i] = "(" + p + ")"
	}
	return strings.Join(wrapped, "|"), nil
}

// regexpEscape escapes the BRE/ERE metacharacters most filters care about.
// We don't use regexp.QuoteMeta because it also escapes characters like '/'
// that grep handles fine; keeping this minimal makes the output readable.
func regexpEscape(s string) string {
	const meta = `.+*?()[]{}|^$\`
	var b strings.Builder
	for _, r := range s {
		if strings.ContainsRune(meta, r) {
			b.WriteByte('\\')
		}
		b.WriteRune(r)
	}
	return b.String()
}

func flagsString(g config.GrepPref) string {
	var f []string
	if g.Regex {
		f = append(f, "regex")
	}
	if g.Word {
		f = append(f, "word")
	}
	if g.IgnoreCase {
		f = append(f, "icase")
	}
	if len(g.Include) > 0 {
		f = append(f, "include="+strings.Join(g.Include, ","))
	}
	if len(g.Exclude) > 0 {
		f = append(f, "exclude="+strings.Join(g.Exclude, ","))
	}
	return strings.Join(f, " ")
}

func resolveGrepPaths(cfg *config.Config, in []string) ([]string, error) {
	out := make([]string, 0, len(in))
	for _, p := range in {
		expanded, err := expandCwd(cfg, p)
		if err != nil {
			return nil, err
		}
		out = append(out, expanded)
	}
	return out, nil
}
