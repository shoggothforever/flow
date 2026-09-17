package ui

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/yithcai/flow/internal/config"
	"github.com/yithcai/flow/internal/finder"
	"github.com/yithcai/flow/internal/search"
	"github.com/yithcai/flow/internal/template"
)

// streamCmd runs cmd in cwd, streaming combined stdout/stderr to w as
// Server-Sent Events. The final event is "done" with the exit code.
//
// Cancellation: when the client disconnects (closes the EventSource /
// aborts the fetch), the request context is cancelled and we send
// SIGTERM to the entire child process group; if it's still alive 2s
// later we follow up with SIGKILL. Putting the child in its own pgid
// lets us reap shell-spawned grand-children too (the typical "bash -c
// foo" → start_all.sh → 30 server binaries case).
func streamCmd(w http.ResponseWriter, r *http.Request, cwd, name string, args []string, env []string) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		writeErr(w, http.StatusInternalServerError, "streaming not supported")
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no") // disable nginx buffering if any

	ctx := r.Context()

	// emit is called concurrently from both pump goroutines, so guard the
	// ResponseWriter: http.ResponseWriter is not safe for concurrent writes.
	var emitMu sync.Mutex
	emit := func(event, data string) {
		emitMu.Lock()
		defer emitMu.Unlock()
		data = strings.ReplaceAll(data, "\r\n", "\n")
		for _, line := range strings.Split(data, "\n") {
			fmt.Fprintf(w, "event: %s\ndata: %s\n\n", event, line)
		}
		flusher.Flush()
	}

	c := exec.Command(name, args...)
	if cwd != "" {
		c.Dir = cwd
	}
	if len(env) > 0 {
		c.Env = append(os.Environ(), env...)
	}
	// Put the child (and anything it spawns) into its own process group
	// so we can signal them all at once.
	c.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}

	stdout, err := c.StdoutPipe()
	if err != nil {
		emit("error", "stdout pipe: "+err.Error())
		emit("done", "1")
		return
	}
	stderr, err := c.StderrPipe()
	if err != nil {
		emit("error", "stderr pipe: "+err.Error())
		emit("done", "1")
		return
	}

	emit("info", fmt.Sprintf("$ %s %s   (cwd=%s)", name, strings.Join(args, " "), cwd))

	if err := c.Start(); err != nil {
		emit("error", "failed to start: "+err.Error())
		emit("done", "1")
		return
	}
	pgid := c.Process.Pid // child is the leader of its own group

	// Watch the request context: when the client disconnects we kill the
	// whole process group. Done channel signals when the command finished
	// naturally so the watcher exits cleanly.
	done := make(chan struct{})
	cancelled := make(chan struct{}, 1)
	go func() {
		select {
		case <-ctx.Done():
			// Cancel: politely first, then forcefully.
			_ = syscall.Kill(-pgid, syscall.SIGTERM)
			select {
			case cancelled <- struct{}{}:
			default:
			}
			t := time.NewTimer(2 * time.Second)
			defer t.Stop()
			select {
			case <-done:
			case <-t.C:
				_ = syscall.Kill(-pgid, syscall.SIGKILL)
			}
		case <-done:
		}
	}()

	// Drain both pipes to completion BEFORE Wait(): Wait closes the pipes, so
	// calling it while a pump is still reading races and can truncate output.
	var pumps sync.WaitGroup
	pumps.Add(2)
	go func() { defer pumps.Done(); pump(stdout, "stdout", emit) }()
	go func() { defer pumps.Done(); pump(stderr, "stderr", emit) }()
	pumps.Wait()

	rc := 0
	waitErr := c.Wait()
	close(done)
	if waitErr != nil {
		if ee, ok := waitErr.(*exec.ExitError); ok {
			rc = ee.ExitCode()
		} else {
			rc = -1
		}
	}

	// Distinguish cancelled vs. crashed in the final event.
	select {
	case <-cancelled:
		// Best-effort message back to client. Note: the client may already
		// be gone (that's why we got cancelled), but if they're listening
		// to a fresh EventSource on the same connection this still helps.
		emit("info", "[cancelled by client]")
		emit("done", "130") // SIGINT-style exit code, matches Ctrl-C convention
		return
	default:
	}
	if waitErr != nil && rc == 0 {
		emit("error", waitErr.Error())
	}
	emit("done", fmt.Sprintf("%d", rc))
}

func pump(rc io.ReadCloser, kind string, emit func(string, string)) {
	defer rc.Close()
	scanner := bufio.NewScanner(rc)
	scanner.Buffer(make([]byte, 64*1024), 1024*1024)
	for scanner.Scan() {
		emit(kind, scanner.Text())
	}
}

// expandCwd resolves @alias/~/$VAR forms (mirrors cmd.expandCwd, copied here
// so the ui package doesn't import cmd).
func expandCwd(c *config.Config, raw string) (string, error) {
	if raw == "" {
		return "", nil
	}
	if strings.HasPrefix(raw, "@") {
		alias := strings.TrimPrefix(raw, "@")
		p := c.FindProject(alias)
		if p == nil {
			return "", fmt.Errorf("@%s references an unknown project", alias)
		}
		return expandPath(p.Path)
	}
	return expandPath(raw)
}

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

// run/script/<alias> -----------------------------------------------------
func (s *Server) handleRunScript(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	alias := itemName(r.URL.Path, "/api/run/script/")
	c, err := s.Store.Load()
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	sc := c.FindScript(alias)
	if sc == nil {
		writeErr(w, http.StatusNotFound, "no script named "+alias)
		return
	}
	cwd, err := expandCwd(c, sc.Cwd)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	streamCmd(w, r, cwd, "bash", []string{"-c", sc.Command + " " + strings.Join(sc.Args, " ")}, sc.Env)
}

// run/cmd/<alias> --------------------------------------------------------
func (s *Server) handleRunCmd(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	alias := itemName(r.URL.Path, "/api/run/cmd/")
	c, err := s.Store.Load()
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	cm := c.FindCmd(alias)
	if cm == nil {
		writeErr(w, http.StatusNotFound, "no cmd named "+alias)
		return
	}
	var body struct {
		Vars map[string]string `json:"vars"`
	}
	_ = json.NewDecoder(r.Body).Decode(&body)
	merged := map[string]string{}
	for k, v := range cm.Defaults {
		merged[k] = v
	}
	for k, v := range body.Vars {
		merged[k] = v
	}
	rendered, err := template.Render(cm.Template, merged)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	cwd, err := expandCwd(c, cm.Cwd)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	streamCmd(w, r, cwd, "bash", []string{"-c", rendered}, nil)
}

// run/grep/<name> --------------------------------------------------------
func (s *Server) handleRunGrep(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	name := itemName(r.URL.Path, "/api/run/grep/")
	c, err := s.Store.Load()
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	g := c.FindGrep(name)
	if g == nil {
		writeErr(w, http.StatusNotFound, "no grep preset named "+name)
		return
	}
	paths := make([]string, 0, len(g.Paths))
	for _, p := range g.Paths {
		expanded, err := expandCwd(c, p)
		if err != nil {
			writeErr(w, http.StatusBadRequest, err.Error())
			return
		}
		paths = append(paths, expanded)
	}
	backend := search.Detect()
	if backend == search.BackendNone {
		writeErr(w, http.StatusFailedDependency, "no search backend (install ripgrep or grep)")
		return
	}
	bin, args, err := search.BuildArgs(backend, search.Spec{
		Pattern: g.Pattern, Paths: paths, Include: g.Include, Exclude: g.Exclude,
		Regex: g.Regex, Word: g.Word, IgnoreCase: g.IgnoreCase,
	})
	if err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	streamCmd(w, r, "", bin, args, nil)
}

// run/find/<name> --------------------------------------------------------
// Streams the resolved hits as stdout lines via SSE so the UI can show them
// in the same log panel.
func (s *Server) handleRunFind(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	name := itemName(r.URL.Path, "/api/run/find/")
	c, err := s.Store.Load()
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	p := c.FindFind(name)
	if p == nil {
		writeErr(w, http.StatusNotFound, "no find preset named "+name)
		return
	}
	q, err := buildFindQuery(c, *p)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	hits, err := finder.Find(q)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}

	flusher, _ := w.(http.Flusher)
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	emit := func(ev, line string) {
		fmt.Fprintf(w, "event: %s\ndata: %s\n\n", ev, line)
		if flusher != nil {
			flusher.Flush()
		}
	}
	emit("info", fmt.Sprintf("# find preset %q -> %d hits", name, len(hits)))
	for _, h := range hits {
		emit("stdout", fmt.Sprintf("%s  %10d  %s", h.MTime.Format("2006-01-02 15:04"), h.Size, h.Path))
	}
	emit("done", "0")
}

func buildFindQuery(c *config.Config, p config.FindPref) (finder.Query, error) {
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
			return q, err
		}
		q.After = time.Now().Add(-d)
	}
	if p.ModifiedAfter != "" {
		t, err := finder.ParseDate(p.ModifiedAfter)
		if err != nil {
			return q, err
		}
		q.After = t
	}
	if p.ModifiedBefore != "" {
		t, err := finder.ParseDate(p.ModifiedBefore)
		if err != nil {
			return q, err
		}
		q.Before = t.Add(24*time.Hour - time.Second)
	}
	if p.Size != "" {
		min, max, err := finder.ParseSize(p.Size)
		if err != nil {
			return q, err
		}
		q.SizeMin = min
		q.SizeMax = max
	}
	return q, nil
}

// handleRunSchedule fires a schedule out-of-band by translating it to the
// equivalent per-kind run handler.
func (s *Server) handleRunSchedule(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	name := itemName(r.URL.Path, "/api/run/schedule/")
	c, err := s.Store.Load()
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	sc := c.FindSchedule(name)
	if sc == nil {
		writeErr(w, http.StatusNotFound, "no schedule named "+name)
		return
	}
	switch sc.Kind {
	case "script":
		target := c.FindScript(sc.Target)
		if target == nil {
			writeErr(w, http.StatusBadRequest, "schedule target script not found: "+sc.Target)
			return
		}
		cwd, err := expandCwd(c, target.Cwd)
		if err != nil {
			writeErr(w, http.StatusBadRequest, err.Error())
			return
		}
		full := target.Command
		if len(target.Args) > 0 {
			full += " " + strings.Join(target.Args, " ")
		}
		if len(sc.ExtraArgs) > 0 {
			full += " " + strings.Join(sc.ExtraArgs, " ")
		}
		streamCmd(w, r, cwd, "bash", []string{"-c", full}, target.Env)
	case "cmd":
		target := c.FindCmd(sc.Target)
		if target == nil {
			writeErr(w, http.StatusBadRequest, "schedule target cmd not found: "+sc.Target)
			return
		}
		merged := map[string]string{}
		for k, v := range target.Defaults {
			merged[k] = v
		}
		for k, v := range sc.Vars {
			merged[k] = v
		}
		rendered, err := template.Render(target.Template, merged)
		if err != nil {
			writeErr(w, http.StatusBadRequest, err.Error())
			return
		}
		cwd, err := expandCwd(c, target.Cwd)
		if err != nil {
			writeErr(w, http.StatusBadRequest, err.Error())
			return
		}
		streamCmd(w, r, cwd, "bash", []string{"-c", rendered}, nil)
	case "grep":
		// Re-use existing grep run path by faking a request with the saved name.
		r2 := r.Clone(r.Context())
		r2.URL.Path = "/api/run/grep/" + sc.Target
		s.handleRunGrep(w, r2)
	case "find":
		r2 := r.Clone(r.Context())
		r2.URL.Path = "/api/run/find/" + sc.Target
		s.handleRunFind(w, r2)
	default:
		writeErr(w, http.StatusBadRequest, "unknown schedule kind: "+sc.Kind)
	}
}
