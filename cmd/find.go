package cmd

import (
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/yithcai/flow/internal/config"
	"github.com/yithcai/flow/internal/errs"
	"github.com/yithcai/flow/internal/finder"
)

// findFlags is the shared option bag used by ad-hoc `flow find` and by
// save/run subcommands. Pointers let us tell "unset" from "explicit empty".
type findFlags struct {
	paths          []string
	name           string
	regex          string
	kind           string
	exts           []string
	modifiedWithin string
	modifiedAfter  string
	modifiedBefore string
	size           string
	exclude        []string
	hidden         bool
	sort           string
	reverse        bool
	limit          int
	pathsOnly      bool
}

func (f *findFlags) bind(c *cobra.Command, includeOutputFlags bool) {
	c.Flags().StringSliceVarP(&f.paths, "path", "p", nil, "search root; repeatable; supports @project-alias")
	c.Flags().StringVarP(&f.name, "name", "n", "", `filename glob, e.g. "*_test.go"`)
	c.Flags().StringVar(&f.regex, "regex", "", "RE2 against the basename")
	c.Flags().StringVarP(&f.kind, "type", "t", "", "f|d|l (file / dir / symlink); empty = any")
	c.Flags().StringSliceVar(&f.exts, "ext", nil, "extension filter; repeatable, e.g. --ext .go --ext .rs")
	c.Flags().StringVar(&f.modifiedWithin, "modified-within", "", "relative window: 30m / 24h / 7d / 2w")
	c.Flags().StringVar(&f.modifiedAfter, "modified-after", "", "absolute lower bound (YYYY-MM-DD)")
	c.Flags().StringVar(&f.modifiedBefore, "modified-before", "", "absolute upper bound (YYYY-MM-DD)")
	c.Flags().StringVar(&f.size, "size", "", "+N / -N / N with K/M/G suffix (files only)")
	c.Flags().StringSliceVar(&f.exclude, "exclude", nil, "extra exclude pattern; repeatable")
	c.Flags().BoolVar(&f.hidden, "hidden", false, "include dotfiles and the default skip list")
	c.Flags().StringVar(&f.sort, "sort", "mtime", "mtime | name | size")
	c.Flags().BoolVarP(&f.reverse, "reverse", "r", false, "reverse sort order")
	c.Flags().IntVarP(&f.limit, "limit", "l", 0, "stop after N hits (0 = unlimited)")
	if includeOutputFlags {
		c.Flags().BoolVar(&f.pathsOnly, "paths-only", false, "print only paths (one per line)")
	}
}

// applyOverrides patches a stored FindPref with any flags the user passed.
// Empty / zero values from the CLI mean "use the saved value".
func (f *findFlags) applyOverrides(p *config.FindPref) {
	if len(f.paths) > 0 {
		p.Paths = f.paths
	}
	if f.name != "" {
		p.NamePattern = f.name
	}
	if f.regex != "" {
		p.Regex = f.regex
	}
	if f.kind != "" {
		p.Kind = f.kind
	}
	if len(f.exts) > 0 {
		p.Exts = f.exts
	}
	if f.modifiedWithin != "" {
		p.ModifiedWithin = f.modifiedWithin
		p.ModifiedAfter = "" // mutually exclusive with relative window
	}
	if f.modifiedAfter != "" {
		p.ModifiedAfter = f.modifiedAfter
	}
	if f.modifiedBefore != "" {
		p.ModifiedBefore = f.modifiedBefore
	}
	if f.size != "" {
		p.Size = f.size
	}
	if len(f.exclude) > 0 {
		p.Exclude = append(append([]string{}, p.Exclude...), f.exclude...)
	}
	if f.hidden {
		p.Hidden = true
	}
	if f.sort != "" && f.sort != "mtime" {
		p.Sort = f.sort
	}
	if f.reverse {
		p.Reverse = true
	}
	if f.limit > 0 {
		p.Limit = f.limit
	}
}

// buildQuery turns a FindPref into a finder.Query, resolving @aliases.
func buildQuery(c *config.Config, p config.FindPref) (finder.Query, error) {
	q := finder.Query{
		Name:    p.NamePattern,
		Regex:   p.Regex,
		Kind:    finder.Kind(strings.TrimSpace(p.Kind)),
		Exts:    p.Exts,
		Exclude: p.Exclude,
		Hidden:  p.Hidden,
		Sort:    finder.SortKey(strings.TrimSpace(p.Sort)),
		Reverse: p.Reverse,
		Limit:   p.Limit,
		SizeMin: -1,
		SizeMax: -1,
	}

	for _, raw := range p.Paths {
		expanded, err := expandCwd(c, raw)
		if err != nil {
			return q, err
		}
		q.Roots = append(q.Roots, expanded)
	}

	if p.ModifiedWithin != "" {
		d, err := finder.ParseDuration(p.ModifiedWithin)
		if err != nil {
			return q, errs.UserWrap(err, "could not parse --modified-within",
				"examples: 30m, 24h, 7d, 2w")
		}
		q.After = time.Now().Add(-d)
	}
	if p.ModifiedAfter != "" {
		t, err := finder.ParseDate(p.ModifiedAfter)
		if err != nil {
			return q, errs.UserWrap(err, "could not parse --modified-after",
				"expected format: YYYY-MM-DD, e.g. 2026-05-01")
		}
		q.After = t
	}
	if p.ModifiedBefore != "" {
		t, err := finder.ParseDate(p.ModifiedBefore)
		if err != nil {
			return q, errs.UserWrap(err, "could not parse --modified-before",
				"expected format: YYYY-MM-DD")
		}
		// Use end-of-day so YYYY-MM-DD is inclusive on both ends.
		q.Before = t.Add(24*time.Hour - time.Second)
	}
	if p.Size != "" {
		min, max, err := finder.ParseSize(p.Size)
		if err != nil {
			return q, errs.UserWrap(err, "could not parse --size",
				"examples: +1M (>=1 MiB), -10K (<=10 KiB), 512 (exactly 512 B)")
		}
		q.SizeMin = min
		q.SizeMax = max
	}

	return q, nil
}

func runQuery(c *config.Config, p config.FindPref, pathsOnly bool) error {
	if len(p.Paths) == 0 {
		return errs.User(
			"--path is required (which directory to search?)",
			`example:  flow find --path @cube --type f --modified-within 24h`,
		)
	}
	q, err := buildQuery(c, p)
	if err != nil {
		return err
	}
	hits, err := finder.Find(q)
	if err != nil {
		return errs.User(err.Error(), "see  flow find --help  for valid filters")
	}
	if pathsOnly {
		for _, h := range hits {
			fmt.Println(h.Path)
		}
		fmt.Fprintf(os.Stderr, "(%d hits)\n", len(hits))
		return nil
	}
	if len(hits) == 0 {
		fmt.Println("(no matches)")
		return nil
	}
	rows := make([][]string, 0, len(hits))
	for _, h := range hits {
		kind := string(h.Kind)
		if kind == "" {
			kind = "f"
		}
		rows = append(rows, []string{
			h.MTime.Format("2006-01-02 15:04"),
			humanSize(h.Size, h.IsDir),
			kind,
			h.Path,
		})
	}
	printTable([]string{"MTIME", "SIZE", "T", "PATH"}, rows)
	fmt.Printf("(%d hits)\n", len(hits))
	return nil
}

func humanSize(n int64, isDir bool) string {
	if isDir {
		return "-"
	}
	const k = 1024
	switch {
	case n < k:
		return fmt.Sprintf("%dB", n)
	case n < k*k:
		return fmt.Sprintf("%.1fK", float64(n)/k)
	case n < k*k*k:
		return fmt.Sprintf("%.1fM", float64(n)/(k*k))
	default:
		return fmt.Sprintf("%.1fG", float64(n)/(k*k*k))
	}
}

func init() {
	findCmd := &cobra.Command{
		Use:   "find",
		Short: "Locate files by name / type / mtime / size (and save presets)",
		Long: `flow find walks one or more directory roots and prints entries that
match the given filters. Results are sorted by mtime (newest first) by default.

Run with explicit flags for a one-off query, or use the subcommands to save,
list, show, run and remove reusable presets.`,
	}

	// Ad-hoc query (no subcommand args) ---------------------------------
	var adhoc findFlags
	findCmd.RunE = func(_ *cobra.Command, _ []string) error {
		s, err := loadStore()
		if err != nil {
			return err
		}
		c, err := s.Load()
		if err != nil {
			return err
		}
		p := config.FindPref{}
		adhoc.applyOverrides(&p)
		return runQuery(c, p, adhoc.pathsOnly)
	}
	adhoc.bind(findCmd, true)

	// save --------------------------------------------------------------
	var saveFlags findFlags
	saveCmd := &cobra.Command{
		Use:   "save <name>",
		Short: "Save the given filter set as a reusable find preset",
		Args:  cobra.ExactArgs(1),
		RunE: func(_ *cobra.Command, args []string) error {
			name := args[0]
			if len(saveFlags.paths) == 0 {
				return errs.User(
					"--path is required when saving a preset",
					`example:  flow find save recent-go --path @cube --type f --ext .go --modified-within 1d`,
				)
			}
			s, err := loadStore()
			if err != nil {
				return err
			}
			return s.Mutate(func(c *config.Config) error {
				if c.FindFind(name) != nil {
					return errs.User(
						fmt.Sprintf("find preset %q already exists", name),
						"remove it first:  flow find rm "+name,
					)
				}
				p := config.FindPref{Name: name}
				saveFlags.applyOverrides(&p)
				c.Finds = append(c.Finds, p)
				fmt.Printf("Saved find preset %s\n", name)
				return nil
			})
		},
	}
	saveFlags.bind(saveCmd, false)

	// list --------------------------------------------------------------
	listCmd := &cobra.Command{
		Use:   "list",
		Short: "List saved find presets",
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
			if len(c.Finds) == 0 {
				fmt.Println("(no find presets yet -- save one with: flow find save ...)")
				return nil
			}
			rows := make([][]string, 0, len(c.Finds))
			for _, p := range c.Finds {
				rows = append(rows, []string{
					p.Name,
					strings.Join(p.Paths, " "),
					strings.TrimSpace(p.Kind + " " + strings.Join(p.Exts, ",")),
					strings.TrimSpace(p.NamePattern + " " + p.Regex),
					summariseTime(p),
				})
			}
			printTable([]string{"NAME", "PATHS", "TYPE", "NAME/REGEX", "MTIME"}, rows)
			return nil
		},
	}

	// show --------------------------------------------------------------
	showCmd := &cobra.Command{
		Use:   "show [<name>]",
		Short: "Show the full filter set of a preset (omit name to pick interactively)",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(_ *cobra.Command, args []string) error {
			s, err := loadStore()
			if err != nil {
				return err
			}
			c, err := s.Load()
			if err != nil {
				return err
			}
			name, err := pickFromConfigWithUsage(args, findItems(c), "find preset",
				"save one:  flow find save <name> --path @<proj> ...",
				"flow find show <name>")
			if err != nil {
				return err
			}
			p, err := resolveFind(c, name)
			if err != nil {
				return err
			}
			fmt.Printf("name:           %s\n", p.Name)
			fmt.Printf("paths:          %s\n", strings.Join(p.Paths, " "))
			if p.NamePattern != "" {
				fmt.Printf("name pattern:   %s\n", p.NamePattern)
			}
			if p.Regex != "" {
				fmt.Printf("regex:          %s\n", p.Regex)
			}
			if p.Kind != "" {
				fmt.Printf("kind:           %s\n", p.Kind)
			}
			if len(p.Exts) > 0 {
				fmt.Printf("exts:           %s\n", strings.Join(p.Exts, ","))
			}
			if p.ModifiedWithin != "" {
				fmt.Printf("modified within: %s\n", p.ModifiedWithin)
			}
			if p.ModifiedAfter != "" {
				fmt.Printf("modified after:  %s\n", p.ModifiedAfter)
			}
			if p.ModifiedBefore != "" {
				fmt.Printf("modified before: %s\n", p.ModifiedBefore)
			}
			if p.Size != "" {
				fmt.Printf("size:           %s\n", p.Size)
			}
			if len(p.Exclude) > 0 {
				fmt.Printf("exclude:        %s\n", strings.Join(p.Exclude, " "))
			}
			if p.Hidden {
				fmt.Println("hidden:         true")
			}
			if p.Sort != "" {
				fmt.Printf("sort:           %s\n", p.Sort)
			}
			if p.Reverse {
				fmt.Println("reverse:        true")
			}
			if p.Limit > 0 {
				fmt.Printf("limit:          %d\n", p.Limit)
			}
			return nil
		},
	}

	// rm ----------------------------------------------------------------
	rmCmd := &cobra.Command{
		Use:   "rm <name>",
		Short: "Remove a find preset",
		Args:  cobra.ExactArgs(1),
		RunE: func(_ *cobra.Command, args []string) error {
			s, err := loadStore()
			if err != nil {
				return err
			}
			return s.Mutate(func(c *config.Config) error {
				for i, f := range c.Finds {
					if f.Name == args[0] {
						c.Finds = append(c.Finds[:i], c.Finds[i+1:]...)
						fmt.Printf("Removed find preset %s\n", args[0])
						return nil
					}
				}
				return errs.User(
					fmt.Sprintf("no find preset named %q", args[0]),
					"see saved presets:  flow find list",
				)
			})
		},
	}

	// run ---------------------------------------------------------------
	var runFlags findFlags
	runCmd := &cobra.Command{
		Use:   "run [<name>]",
		Short: "Run a saved find preset (omit <name> to pick interactively; flags override saved values)",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(_ *cobra.Command, args []string) error {
			s, err := loadStore()
			if err != nil {
				return err
			}
			c, err := s.Load()
			if err != nil {
				return err
			}
			name, err := pickFromConfig(args, findItems(c), "find preset",
				"save one:  flow find save <name> --path @<proj> ...")
			if err != nil {
				return err
			}
			p, err := resolveFind(c, name)
			if err != nil {
				return err
			}
			merged := *p
			runFlags.applyOverrides(&merged)
			return runQuery(c, merged, runFlags.pathsOnly)
		},
	}
	runFlags.bind(runCmd, true)

	findCmd.AddCommand(saveCmd, listCmd, showCmd, rmCmd, runCmd)
	rootCmd.AddCommand(findCmd)
}

func summariseTime(p config.FindPref) string {
	switch {
	case p.ModifiedWithin != "":
		return "<" + p.ModifiedWithin
	case p.ModifiedAfter != "" && p.ModifiedBefore != "":
		return p.ModifiedAfter + ".." + p.ModifiedBefore
	case p.ModifiedAfter != "":
		return ">=" + p.ModifiedAfter
	case p.ModifiedBefore != "":
		return "<=" + p.ModifiedBefore
	default:
		return ""
	}
}
