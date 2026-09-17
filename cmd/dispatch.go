package cmd

// "Lazy dispatch" lets the user type the alias directly instead of typing
// the full `flow <kind> run <alias>`. It is implemented by rewriting
// os.Args before cobra sees them, so the rest of the CLI machinery stays
// unchanged.
//
// Examples (all equivalent):
//
//   flow tail-cxb --grep phase           ==  flow script run tail-cxb --grep phase
//   flow build_cook                      ==  flow script run build_cook
//   flow phase                           ==  flow grep run phase
//   flow                                 ==  flow run    (unified fuzzy picker)
//
// Resolution order when an alias collides across kinds:
//   script > cmd > grep > find
// On collision the user is shown the conflict and asked to use the
// explicit `flow <kind> run <alias>` form.

import (
	"fmt"
	"os"
	"sort"
	"strings"

	"github.com/yithcai/flow/internal/config"
	"github.com/yithcai/flow/internal/errs"
)

// kindOrder is the precedence used when an alias exists in multiple
// resource sets. The earlier wins, but we always also report the conflict
// so the user can disambiguate.
var kindOrder = []string{"script", "cmd", "grep", "find"}

// RewriteArgs inspects os.Args and, if the first positional looks like a
// known alias rather than a cobra subcommand, rewrites it into the
// canonical `flow <kind> run <alias>` form. It is safe to call before
// cobra.Execute.
//
// It is intentionally conservative: anything we can't classify with
// confidence is left alone so cobra produces its normal error/help output.
func RewriteArgs() {
	args := os.Args[1:]

	// `flow` with no args -> open the unified interactive picker.
	// We reuse the existing `flow run` command (single-level fuzzy menu
	// over scripts / cmds / greps / finds / jumps) instead of
	// inventing a parallel two-level picker.
	if len(args) == 0 {
		os.Args = append([]string{os.Args[0]}, "run")
		return
	}

	// Locate the first positional, skipping over root persistent flags
	// like `-v`/`--verbose` (bool) and `--config <path>` / `--config=<path>`.
	posIdx, ok := firstPositional(args)
	if !ok {
		// All flags, no positional. Let cobra handle it.
		return
	}
	first := args[posIdx]

	// If the first positional is already a registered cobra subcommand,
	// leave the args untouched.
	if isKnownSubcommand(first) {
		return
	}

	// Otherwise try to map the token to a saved alias. We tolerate a
	// missing/unreadable config: in that case we just fall through and
	// cobra will print "unknown command".
	store, err := config.NewStore(flagConfigPathPreparse(args))
	if err != nil {
		return
	}
	cfg, err := store.Load()
	if err != nil {
		return
	}

	hits := lookupAlias(cfg, first)
	switch len(hits) {
	case 0:
		// Not an alias either; let cobra produce its standard error.
		return
	case 1:
		kind := hits[0]
		// Splice the rewrite in place of the alias token, preserving any
		// preceding/following flags:
		//   flow [pre-flags...] <alias> [post-args...]
		// becomes
		//   flow [pre-flags...] <kind> run <alias> [post-args...]
		newArgs := make([]string, 0, len(args)+2)
		newArgs = append(newArgs, args[:posIdx]...)
		newArgs = append(newArgs, kind, "run", first)
		newArgs = append(newArgs, args[posIdx+1:]...)
		os.Args = append([]string{os.Args[0]}, newArgs...)
		return
	default:
		// Ambiguous. Print a friendly error and exit; we mirror the
		// regular User-error format used elsewhere.
		fmt.Fprintln(os.Stderr, errs.Format(errs.User(
			fmt.Sprintf("alias %q is registered as: %s", first, strings.Join(hits, ", ")),
			"disambiguate with the explicit form, e.g.  flow "+hits[0]+" run "+first,
		)))
		os.Exit(1)
	}
}

// firstPositional returns the index of the first non-flag token in args,
// skipping over the root persistent flags (-v/--verbose, --config[=v]).
// It returns (idx, true) if found, (0, false) otherwise.
func firstPositional(args []string) (int, bool) {
	for i := 0; i < len(args); i++ {
		a := args[i]
		switch {
		case a == "--":
			// Everything after `--` is positional.
			if i+1 < len(args) {
				return i + 1, true
			}
			return 0, false
		case a == "-v" || a == "--verbose":
			continue
		case a == "--config":
			i++ // skip value
			continue
		case strings.HasPrefix(a, "--config="):
			continue
		case strings.HasPrefix(a, "-"):
			// Unknown flag; leaving it for cobra is the right thing.
			return 0, false
		default:
			return i, true
		}
	}
	return 0, false
}

// flagConfigPathPreparse does a best-effort scan of the args for
// `--config <path>` / `--config=<path>` so that lazy dispatch picks up
// the same config file the user is about to use. It does not validate.
func flagConfigPathPreparse(args []string) string {
	for i, a := range args {
		switch {
		case a == "--config" && i+1 < len(args):
			return args[i+1]
		case strings.HasPrefix(a, "--config="):
			return strings.TrimPrefix(a, "--config=")
		}
	}
	return ""
}

// isKnownSubcommand returns true if `name` is one of the cobra commands
// attached to rootCmd (or a built-in like "help"/"completion"/"version").
func isKnownSubcommand(name string) bool {
	switch name {
	case "help", "completion", "version", "__complete", "__completeNoDesc":
		return true
	}
	for _, c := range rootCmd.Commands() {
		if c.Name() == name {
			return true
		}
		for _, alt := range c.Aliases {
			if alt == name {
				return true
			}
		}
	}
	return false
}

// lookupAlias returns the kinds in which `name` is registered,
// in the kindOrder precedence.
func lookupAlias(cfg *config.Config, name string) []string {
	var hits []string
	if cfg.FindScript(name) != nil {
		hits = append(hits, "script")
	}
	if cfg.FindCmd(name) != nil {
		hits = append(hits, "cmd")
	}
	if cfg.FindGrep(name) != nil {
		hits = append(hits, "grep")
	}
	if cfg.FindFind(name) != nil {
		hits = append(hits, "find")
	}
	sort.SliceStable(hits, func(i, j int) bool {
		return idx(kindOrder, hits[i]) < idx(kindOrder, hits[j])
	})
	return hits
}

func idx(xs []string, x string) int {
	for i, v := range xs {
		if v == x {
			return i
		}
	}
	return len(xs)
}
