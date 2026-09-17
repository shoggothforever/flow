// Package scheduler runs flow's recurring tasks. It loads the config,
// resolves each Schedule's next-fire time, sleeps until then, dispatches
// to the matching `flow <kind> run <target>` subcommand and appends a
// line to history.jsonl.
//
// Design choices:
//   - Single-process: one scheduler per config file. We acquire an
//     advisory file lock so two `flow scheduler run` calls don't race.
//   - Re-execs flow itself for each run, instead of importing every
//     runner package. Picks up new flags / fixes automatically and keeps
//     scheduler logic small.
//   - Reloads config on each tick so edits via the UI/CLI take effect
//     within ~5 seconds without restarting the daemon.
//   - History is JSON Lines, append-only, fsync after each write.
package scheduler

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"sync"
	"syscall"
	"time"

	"golang.org/x/sys/unix"

	"github.com/yithcai/flow/internal/config"
	"github.com/yithcai/flow/internal/cron"
)

// HistoryEntry is one line in history.jsonl.
type HistoryEntry struct {
	Name     string    `json:"name"`
	Kind     string    `json:"kind"`
	Target   string    `json:"target"`
	StartTS  time.Time `json:"start"`
	Duration float64   `json:"duration_sec"`
	ExitCode int       `json:"exit_code"`
	Error    string    `json:"error,omitempty"`
}

// planEntry is one row of the in-memory firing plan.
type planEntry struct {
	spec    string
	enabled bool
	next    time.Time
}

// Run blocks running the scheduler loop until ctx is cancelled.
// flowBin: absolute path to the flow binary (so we can re-exec).
// configPath: the config file (for the --config flag passed to children).
// historyPath: where to append run records.
// tick: how often we re-load config and check fire times.
func Run(ctx context.Context, flowBin, configPath, historyPath string, tick, runTimeout time.Duration, log func(format string, a ...any)) error {
	if log == nil {
		log = func(string, ...any) {}
	}
	if tick <= 0 {
		tick = 5 * time.Second
	}

	// Single-instance lock so two schedulers don't double-fire schedules.
	lockPath := configPath + ".scheduler.lock"
	lock, err := acquireLock(lockPath)
	if err != nil {
		return fmt.Errorf("another scheduler is already running for %s (lock: %s)", configPath, lockPath)
	}
	defer lock.Close()

	// Make sure the history directory exists.
	if err := os.MkdirAll(filepath.Dir(historyPath), 0o755); err != nil {
		return fmt.Errorf("could not create history dir: %w", err)
	}

	store, err := config.NewStore(configPath)
	if err != nil {
		return err
	}

	// nextFire is keyed by schedule name; resets when the schedule
	// expression / enabled flag changes.
	plan := map[string]planEntry{}
	var planMu sync.Mutex

	// running tracks schedules whose previous fire is still executing, so a
	// slow or hung target can't pile up a fresh goroutine + child process on
	// every tick. Bounded by the number of distinct schedules.
	running := map[string]bool{}
	var runningMu sync.Mutex

	log("scheduler: started, tick=%s, history=%s", tick, historyPath)
	t := time.NewTicker(tick)
	defer t.Stop()

	for {
		now := time.Now()
		cfg, err := store.Load()
		if err != nil {
			log("scheduler: config load failed: %v", err)
		} else {
			planMu.Lock()
			rebuildPlan(cfg.Schedules, plan, now, log)
			due := pickDue(plan, now)
			planMu.Unlock()

			for _, name := range due {
				sc := cfg.FindSchedule(name)
				if sc == nil {
					continue
				}
				// Skip this fire if the previous run hasn't finished yet,
				// otherwise a hung target leaks one goroutine+process per tick.
				runningMu.Lock()
				busy := running[name]
				if !busy {
					running[name] = true
				}
				runningMu.Unlock()
				if busy {
					log("scheduler: %s still running from a previous fire; skipping", name)
				} else {
					go func(s config.Schedule) {
						defer func() {
							runningMu.Lock()
							delete(running, s.Name)
							runningMu.Unlock()
						}()
						runOnce(s, flowBin, configPath, historyPath, runTimeout, log)
					}(*sc)
				}
				// Recompute next fire immediately so we don't repeat within the tick.
				planMu.Lock()
				if pe, ok := plan[name]; ok {
					if sched, perr := parseExpr(pe.spec); perr == nil {
						pe.next = sched.Next(now)
						plan[name] = pe
					}
				}
				planMu.Unlock()
			}
		}

		select {
		case <-ctx.Done():
			log("scheduler: stopping (%v)", ctx.Err())
			return nil
		case <-t.C:
		}
	}
}

func parseExpr(spec string) (*cron.Schedule, error) {
	return cron.Parse(spec)
}

// scheduleSpec is the active expression (cron or every) of a schedule.
func scheduleSpec(s config.Schedule) string {
	if s.Cron != "" {
		return s.Cron
	}
	return s.Every
}

func rebuildPlan(schedules []config.Schedule, plan map[string]planEntry, now time.Time, log func(string, ...any)) {
	seen := map[string]bool{}
	for _, s := range schedules {
		seen[s.Name] = true
		spec := scheduleSpec(s)
		pe, ok := plan[s.Name]
		if !ok || pe.spec != spec || pe.enabled != s.Enabled {
			sched, err := parseExpr(spec)
			if err != nil {
				log("scheduler: %s: invalid expression %q: %v", s.Name, spec, err)
				delete(plan, s.Name)
				continue
			}
			next := sched.Next(now)
			plan[s.Name] = planEntry{spec: spec, enabled: s.Enabled, next: next}
			if s.Enabled {
				log("scheduler: %s scheduled, next=%s (%s)", s.Name, next.Format(time.RFC3339), spec)
			}
		}
	}
	for name := range plan {
		if !seen[name] {
			delete(plan, name)
		}
	}
}

func pickDue(plan map[string]planEntry, now time.Time) []string {
	var due []string
	for name, pe := range plan {
		if pe.enabled && !pe.next.After(now) {
			due = append(due, name)
		}
	}
	sort.Strings(due)
	return due
}

// runOnce dispatches a single schedule. It re-execs the flow binary with
// the appropriate run subcommand and writes a history line on completion.
// If timeout > 0, the run (whole process group) is killed once it elapses.
func runOnce(s config.Schedule, flowBin, configPath, historyPath string, timeout time.Duration, log func(string, ...any)) {
	args := []string{"--config", configPath}
	switch s.Kind {
	case "script":
		args = append(args, "script", "run", s.Target)
		if len(s.ExtraArgs) > 0 {
			args = append(args, "--")
			args = append(args, s.ExtraArgs...)
		}
	case "cmd":
		args = append(args, "cmd", "run", s.Target)
		for k, v := range s.Vars {
			args = append(args, fmt.Sprintf("%s=%s", k, v))
		}
	case "grep":
		args = append(args, "grep", "run", s.Target)
	case "find":
		args = append(args, "find", "run", s.Target)
	default:
		log("scheduler: %s: unknown kind %q", s.Name, s.Kind)
		return
	}

	start := time.Now()
	ctx := context.Background()
	if timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, timeout)
		defer cancel()
	}
	cmd := exec.CommandContext(ctx, flowBin, args...)
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	// On timeout, kill the whole process group: the re-exec'd `flow ... run`
	// spawns bash and possibly long-lived grand-children, which the default
	// Cancel (signal the direct child only) would leave behind.
	cmd.Cancel = func() error {
		if cmd.Process != nil {
			return syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
		}
		return nil
	}
	cmd.WaitDelay = 5 * time.Second
	// Capture stdout/stderr but don't keep them; just discard.
	cmd.Stdout = io.Discard
	cmd.Stderr = io.Discard
	cmd.Stdin = nil

	runErr := cmd.Run()
	rc := 0
	errMsg := ""
	if runErr != nil {
		if ee, ok := runErr.(*exec.ExitError); ok {
			rc = ee.ExitCode()
		} else {
			rc = -1
			errMsg = runErr.Error()
		}
	}
	if ctx.Err() == context.DeadlineExceeded {
		rc = -1
		errMsg = fmt.Sprintf("timed out after %s", timeout)
	}

	entry := HistoryEntry{
		Name: s.Name, Kind: s.Kind, Target: s.Target,
		StartTS:  start,
		Duration: time.Since(start).Seconds(),
		ExitCode: rc,
		Error:    errMsg,
	}
	if err := appendHistory(historyPath, entry); err != nil {
		log("scheduler: %s: history append failed: %v", s.Name, err)
	}
	log("scheduler: %s ran (kind=%s target=%s rc=%d duration=%.1fs)", s.Name, s.Kind, s.Target, rc, entry.Duration)
}

// History stays bounded so it can't grow without limit and so reads stay
// cheap: once the file crosses historyMaxBytes we rewrite it down to the
// most recent historyKeepOnRotate entries.
const (
	historyMaxBytes     = 5 << 20 // ~5 MiB
	historyKeepOnRotate = 2000
)

// historyMu serialises history writes within this process so rotation can't
// race concurrent appends (several runOnce goroutines may finish at once).
var historyMu sync.Mutex

func appendHistory(path string, e HistoryEntry) error {
	data, err := json.Marshal(e)
	if err != nil {
		return err
	}

	historyMu.Lock()
	defer historyMu.Unlock()

	f, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	if _, err := f.Write(append(data, '\n')); err != nil {
		f.Close()
		return err
	}
	if err := f.Sync(); err != nil {
		f.Close()
		return err
	}
	fi, statErr := f.Stat()
	if err := f.Close(); err != nil {
		return err
	}
	if statErr == nil && fi.Size() > historyMaxBytes {
		return rotateHistory(path, historyKeepOnRotate)
	}
	return nil
}

// rotateHistory rewrites path keeping only the last `keep` entries, via an
// atomic temp-file + rename so readers never observe a torn file.
func rotateHistory(path string, keep int) error {
	lines, err := tailLines(path, keep)
	if err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".history-*.jsonl.tmp")
	if err != nil {
		return err
	}
	tmpPath := tmp.Name()
	committed := false
	defer func() {
		if !committed {
			_ = tmp.Close()
			_ = os.Remove(tmpPath)
		}
	}()
	for _, ln := range lines {
		if _, err := tmp.Write(append(ln, '\n')); err != nil {
			return err
		}
	}
	if err := tmp.Sync(); err != nil {
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Rename(tmpPath, path); err != nil {
		return err
	}
	committed = true
	return nil
}

// ReadHistory loads the last `limit` entries from the history file
// (limit <= 0 means all). It reads only the tail of the file instead of
// slurping the whole thing, so cost stays ~O(limit) even for a large log.
func ReadHistory(path string, limit int) ([]HistoryEntry, error) {
	lines, err := tailLines(path, limit)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	out := make([]HistoryEntry, 0, len(lines))
	for _, ln := range lines {
		var e HistoryEntry
		if err := json.Unmarshal(ln, &e); err != nil {
			continue // skip a malformed/partial line rather than truncating
		}
		out = append(out, e)
	}
	return out, nil
}

// tailLines returns up to the last n non-empty lines of path, oldest-first.
// n <= 0 returns every line. It scans backward from EOF in chunks so a small
// n does not load the whole file.
func tailLines(path string, n int) ([][]byte, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	fi, err := f.Stat()
	if err != nil {
		return nil, err
	}
	size := fi.Size()
	if size == 0 {
		return nil, nil
	}

	const chunk = 64 * 1024
	var buf []byte
	offset := size
	for offset > 0 {
		readSize := int64(chunk)
		if offset < readSize {
			readSize = offset
		}
		offset -= readSize
		part := make([]byte, readSize)
		if _, err := f.ReadAt(part, offset); err != nil && err != io.EOF {
			return nil, err
		}
		buf = append(part, buf...)
		if n > 0 && bytes.Count(buf, []byte{'\n'}) > n {
			break
		}
	}

	split := bytes.Split(buf, []byte{'\n'})
	out := make([][]byte, 0, len(split))
	for _, ln := range split {
		if len(bytes.TrimSpace(ln)) == 0 {
			continue
		}
		// Copy: split aliases buf, and rotateHistory appends to these slices.
		cp := make([]byte, len(ln))
		copy(cp, ln)
		out = append(out, cp)
	}
	if n > 0 && len(out) > n {
		out = out[len(out)-n:]
	}
	return out, nil
}

// HistoryPathFor returns the canonical history.jsonl path for a given
// config file path. Both the daemon and read-only consumers (CLI / UI)
// must agree, so this is the single source of truth.
func HistoryPathFor(configPath string) string {
	return filepath.Join(filepath.Dir(configPath), "history.jsonl")
}

// acquireLock takes an advisory exclusive flock on path; returns nil
// (with err) if someone else holds it.
func acquireLock(path string) (*os.File, error) {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o644)
	if err != nil {
		return nil, err
	}
	if err := unix.Flock(int(f.Fd()), unix.LOCK_EX|unix.LOCK_NB); err != nil {
		f.Close()
		return nil, err
	}
	return f, nil
}
