package cmd

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/spf13/cobra"

	"github.com/yithcai/flow/internal/config"
	"github.com/yithcai/flow/internal/cron"
	"github.com/yithcai/flow/internal/errs"
	"github.com/yithcai/flow/internal/scheduler"
)

func init() {
	scheduleCmd := &cobra.Command{
		Use:     "schedule",
		Aliases: []string{"sched"},
		Short:   "Manage recurring tasks (cron / every / daily / weekdays)",
	}

	// add ----------------------------------------------------------------
	var addCron, addEvery, addKind, addTarget, addOnFail string
	var addDisabled bool
	var addVars, addExtraArgs []string
	addCmd := &cobra.Command{
		Use:   "add <name>",
		Short: "Register a schedule (cron OR every; pick one)",
		Long: `Examples:
  flow schedule add nightly  --cron "0 3 * * *"     --kind script --target backup
  flow schedule add tick     --every "every 5m"     --kind script --target heartbeat
  flow schedule add work-am  --every "weekdays 09:00" --kind cmd --target deploy --var env=prod
`,
		Args: cobra.ExactArgs(1),
		RunE: func(_ *cobra.Command, args []string) error {
			name := args[0]
			if (addCron == "") == (addEvery == "") {
				return errs.User(
					"exactly one of --cron / --every must be set",
					`examples:  --cron "0 3 * * *"   |   --every "every 5m"   |   --every "weekdays 09:00"`,
				)
			}
			expr := addCron
			if expr == "" {
				expr = addEvery
			}
			if _, err := cron.Parse(expr); err != nil {
				return errs.UserWrap(err, "invalid schedule expression",
					`see  flow schedule examples  for accepted forms`)
			}
			if addKind != "script" && addKind != "cmd" && addKind != "grep" && addKind != "find" {
				return errs.User("--kind must be one of script / cmd / grep / find",
					`example:  --kind script --target build`)
			}
			vars, err := parseKVPairs(addVars)
			if err != nil {
				return errs.UserWrap(err, "invalid --var",
					"pass as KEY=VALUE; repeatable")
			}
			s, err := loadStore()
			if err != nil {
				return err
			}
			return s.Mutate(func(c *config.Config) error {
				if c.FindSchedule(name) != nil {
					return errs.User(
						fmt.Sprintf("schedule %q already exists", name),
						"remove it first:  flow schedule rm "+name,
					)
				}
				c.Schedules = append(c.Schedules, config.Schedule{
					Name:      name,
					Cron:      addCron,
					Every:     addEvery,
					Kind:      addKind,
					Target:    addTarget,
					Vars:      vars,
					ExtraArgs: addExtraArgs,
					Enabled:   !addDisabled,
					OnFail:    addOnFail,
				})
				fmt.Printf("Added schedule %s (%s %s on %s/%s)\n", name, addKind, addTarget,
					firstNonEmpty2(addCron, addEvery), enabledLabel(!addDisabled))
				return nil
			})
		},
	}
	addCmd.Flags().StringVar(&addCron, "cron", "", `5-field cron, e.g. "0 */2 * * *"`)
	addCmd.Flags().StringVar(&addEvery, "every", "", `simplified, e.g. "every 5m" / "daily 09:30" / "weekdays 18:00"`)
	addCmd.Flags().StringVar(&addKind, "kind", "script", "what to run: script | cmd | grep | find")
	addCmd.Flags().StringVar(&addTarget, "target", "", "alias / name of the resource to run")
	addCmd.Flags().StringSliceVar(&addVars, "var", nil, "KEY=VALUE for kind=cmd; repeatable")
	addCmd.Flags().StringSliceVar(&addExtraArgs, "extra-arg", nil, "extra argv appended after `--`; repeatable")
	addCmd.Flags().BoolVar(&addDisabled, "disabled", false, "register but start paused")
	addCmd.Flags().StringVar(&addOnFail, "on-fail", "log", "log | stop")
	_ = addCmd.MarkFlagRequired("target")

	// list / show / rm / pause / resume / runnow ----------------------------
	listCmd := &cobra.Command{
		Use:   "list",
		Short: "List schedules with next-fire times",
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
			if len(c.Schedules) == 0 {
				fmt.Println("(no schedules yet -- add one with: flow schedule add ...)")
				return nil
			}
			now := time.Now()
			rows := make([][]string, 0, len(c.Schedules))
			for _, sc := range c.Schedules {
				expr := firstNonEmpty2(sc.Cron, sc.Every)
				next := "?"
				if parsed, err := cron.Parse(expr); err == nil {
					next = parsed.Next(now).Format("2006-01-02 15:04")
				}
				state := "ON"
				if !sc.Enabled {
					state = "OFF"
				}
				rows = append(rows, []string{
					sc.Name, state, expr, sc.Kind + ":" + sc.Target, next,
				})
			}
			printTable([]string{"NAME", "STATE", "WHEN", "RUNS", "NEXT"}, rows)
			return nil
		},
	}

	rmCmd := &cobra.Command{
		Use:   "rm <name>",
		Short: "Remove a schedule",
		Args:  cobra.ExactArgs(1),
		RunE: func(_ *cobra.Command, args []string) error {
			s, err := loadStore()
			if err != nil {
				return err
			}
			return s.Mutate(func(c *config.Config) error {
				for i, sc := range c.Schedules {
					if sc.Name == args[0] {
						c.Schedules = append(c.Schedules[:i], c.Schedules[i+1:]...)
						fmt.Printf("Removed schedule %s\n", args[0])
						return nil
					}
				}
				return errs.User(fmt.Sprintf("no schedule named %q", args[0]),
					"see saved schedules:  flow schedule list")
			})
		},
	}

	toggleCmd := func(use, short string, target bool) *cobra.Command {
		return &cobra.Command{
			Use:   use,
			Short: short,
			Args:  cobra.ExactArgs(1),
			RunE: func(_ *cobra.Command, args []string) error {
				s, err := loadStore()
				if err != nil {
					return err
				}
				return s.Mutate(func(c *config.Config) error {
					sc := c.FindSchedule(args[0])
					if sc == nil {
						return errs.User(fmt.Sprintf("no schedule named %q", args[0]),
							"see  flow schedule list")
					}
					sc.Enabled = target
					fmt.Printf("schedule %s -> %s\n", sc.Name, enabledLabel(target))
					return nil
				})
			},
		}
	}
	pauseCmd := toggleCmd("pause <name>", "Disable a schedule (kept in config)", false)
	resumeCmd := toggleCmd("resume <name>", "Enable a paused schedule", true)

	showCmd := &cobra.Command{
		Use:   "show <name>",
		Short: "Show details of a schedule",
		Args:  cobra.ExactArgs(1),
		RunE: func(_ *cobra.Command, args []string) error {
			s, err := loadStore()
			if err != nil {
				return err
			}
			c, err := s.Load()
			if err != nil {
				return err
			}
			sc := c.FindSchedule(args[0])
			if sc == nil {
				return errs.User(fmt.Sprintf("no schedule named %q", args[0]),
					"see  flow schedule list")
			}
			expr := firstNonEmpty2(sc.Cron, sc.Every)
			fmt.Printf("name:    %s\n", sc.Name)
			fmt.Printf("when:    %s\n", expr)
			fmt.Printf("runs:    %s %s\n", sc.Kind, sc.Target)
			if len(sc.Vars) > 0 {
				kv := make([]string, 0, len(sc.Vars))
				for k, v := range sc.Vars {
					kv = append(kv, k+"="+v)
				}
				fmt.Printf("vars:    %s\n", strings.Join(kv, " "))
			}
			if len(sc.ExtraArgs) > 0 {
				fmt.Printf("args:    %s\n", strings.Join(sc.ExtraArgs, " "))
			}
			fmt.Printf("state:   %s\n", enabledLabel(sc.Enabled))
			if parsed, err := cron.Parse(expr); err == nil {
				fmt.Printf("next:    %s\n", parsed.Next(time.Now()).Format(time.RFC3339))
			}
			return nil
		},
	}

	runnowCmd := &cobra.Command{
		Use:   "run-now <name>",
		Short: "Trigger a schedule immediately (out of band)",
		Args:  cobra.ExactArgs(1),
		RunE: func(_ *cobra.Command, args []string) error {
			s, err := loadStore()
			if err != nil {
				return err
			}
			c, err := s.Load()
			if err != nil {
				return err
			}
			sc := c.FindSchedule(args[0])
			if sc == nil {
				return errs.User(fmt.Sprintf("no schedule named %q", args[0]),
					"see  flow schedule list")
			}
			return dispatchScheduleNow(*sc)
		},
	}

	// history ----------------------------------------------------------------
	var histLimit int
	historyCmd := &cobra.Command{
		Use:   "history",
		Short: "Show recent runs (from history.jsonl)",
		Args:  cobra.NoArgs,
		RunE: func(_ *cobra.Command, _ []string) error {
			s, err := loadStore()
			if err != nil {
				return err
			}
			path := historyPathFor(s.Path)
			entries, err := scheduler.ReadHistory(path, histLimit)
			if err != nil {
				return errs.System("could not read history", err)
			}
			if len(entries) == 0 {
				fmt.Println("(no runs yet)")
				return nil
			}
			rows := make([][]string, 0, len(entries))
			for _, e := range entries {
				ok := "OK"
				if e.ExitCode != 0 {
					ok = fmt.Sprintf("FAIL(%d)", e.ExitCode)
				}
				rows = append(rows, []string{
					e.StartTS.Format("2006-01-02 15:04:05"),
					e.Name,
					e.Kind + ":" + e.Target,
					fmt.Sprintf("%.1fs", e.Duration),
					ok,
				})
			}
			printTable([]string{"START", "NAME", "RUNS", "DURATION", "STATUS"}, rows)
			return nil
		},
	}
	historyCmd.Flags().IntVar(&histLimit, "limit", 50, "show at most N most recent rows")

	// examples helper --------------------------------------------------------
	examplesCmd := &cobra.Command{
		Use:   "examples",
		Short: "Print accepted schedule expressions",
		Args:  cobra.NoArgs,
		RunE: func(_ *cobra.Command, _ []string) error {
			fmt.Print(scheduleExamples)
			return nil
		},
	}

	scheduleCmd.AddCommand(addCmd, listCmd, rmCmd, pauseCmd, resumeCmd, showCmd, runnowCmd, historyCmd, examplesCmd)
	rootCmd.AddCommand(scheduleCmd)
}

const scheduleExamples = `Cron (5 fields: minute hour day-of-month month day-of-week):
  "0 3 * * *"        every day at 03:00
  "0 */2 * * *"      every 2 hours on the hour
  "30 9 * * 1-5"     09:30 Mon-Fri
  "0 0 1 * *"        midnight on the 1st of every month
  "@hourly"          top of every hour
  "@daily"           00:00 every day
  "@weekly"          00:00 every Sunday

Every / shortcut form:
  "every 30s"        every 30 seconds (min granularity)
  "every 5m"         every 5 minutes
  "every 2h"         every 2 hours
  "daily 09:30"      09:30 every day
  "weekdays 18:00"   Mon-Fri at 18:00
  "weekends 09:00"   Sat+Sun at 09:00
  "mon 09:00"        Monday at 09:00 (sun/mon/tue/wed/thu/fri/sat)
`

func enabledLabel(b bool) string {
	if b {
		return "enabled"
	}
	return "paused"
}

func firstNonEmpty2(a, b string) string {
	if a != "" {
		return a
	}
	return b
}

func historyPathFor(configPath string) string {
	return scheduler.HistoryPathFor(configPath)
}

// dispatchScheduleNow re-execs `flow <kind> run <target> ...` synchronously.
// Used by `flow schedule run-now` and the UI Run button.
func dispatchScheduleNow(s config.Schedule) error {
	self, err := os.Executable()
	if err != nil {
		return errs.System("could not locate flow binary", err)
	}
	args := []string{}
	switch s.Kind {
	case "script":
		args = []string{"script", "run", s.Target}
		if len(s.ExtraArgs) > 0 {
			args = append(args, "--")
			args = append(args, s.ExtraArgs...)
		}
	case "cmd":
		args = []string{"cmd", "run", s.Target}
		for k, v := range s.Vars {
			args = append(args, fmt.Sprintf("%s=%s", k, v))
		}
	case "grep":
		args = []string{"grep", "run", s.Target}
	case "find":
		args = []string{"find", "run", s.Target}
	default:
		return errs.User("unknown kind: "+s.Kind, "")
	}
	if flagConfigPath != "" {
		args = append([]string{"--config", flagConfigPath}, args...)
	}
	c := exec.Command(self, args...)
	c.Stdin = os.Stdin
	c.Stdout = os.Stdout
	c.Stderr = os.Stderr
	return c.Run()
}

// ===== flow scheduler (the daemon) ======================================

func init() {
	schedulerCmd := &cobra.Command{
		Use:   "scheduler",
		Short: "Run the flow scheduler daemon (or install it as a systemd --user unit)",
	}

	var runTick time.Duration
	var runTimeout time.Duration
	runCmd := &cobra.Command{
		Use:   "run",
		Short: "Run the scheduler in the foreground (Ctrl-C to stop)",
		Args:  cobra.NoArgs,
		RunE: func(_ *cobra.Command, _ []string) error {
			s, err := loadStore()
			if err != nil {
				return err
			}
			self, err := os.Executable()
			if err != nil {
				return errs.System("could not locate flow binary", err)
			}
			ctx, cancel := context.WithCancel(context.Background())
			go func() {
				sigs := make(chan os.Signal, 1)
				signal.Notify(sigs, os.Interrupt, syscall.SIGTERM)
				<-sigs
				fmt.Fprintln(os.Stderr, "\nshutting down…")
				cancel()
			}()
			fmt.Printf("flow scheduler\n  config:  %s\n  history: %s\n  binary:  %s\n",
				s.Path, historyPathFor(s.Path), self)
			return scheduler.Run(ctx, self, s.Path, historyPathFor(s.Path), runTick, runTimeout,
				func(format string, a ...any) {
					fmt.Printf("["+time.Now().Format("15:04:05")+"] "+format+"\n", a...)
				})
		},
	}
	runCmd.Flags().DurationVar(&runTick, "tick", 5*time.Second, "how often to re-load config and check fire times")
	runCmd.Flags().DurationVar(&runTimeout, "run-timeout", time.Hour, "kill a single scheduled run if it exceeds this (0 = no limit)")

	installCmd := &cobra.Command{
		Use:   "install",
		Short: "Install + enable a systemd --user unit (auto-starts on login)",
		Args:  cobra.NoArgs,
		RunE:  systemdInstall,
	}
	uninstallCmd := &cobra.Command{
		Use:   "uninstall",
		Short: "Disable + remove the systemd --user unit",
		Args:  cobra.NoArgs,
		RunE:  systemdUninstall,
	}
	statusCmd := &cobra.Command{
		Use:   "status",
		Short: "Show whether the scheduler is running (via systemctl --user)",
		Args:  cobra.NoArgs,
		RunE:  systemdStatus,
	}

	schedulerCmd.AddCommand(runCmd, installCmd, uninstallCmd, statusCmd)
	rootCmd.AddCommand(schedulerCmd)
}
