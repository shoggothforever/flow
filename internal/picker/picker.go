// Package picker is a tiny in-terminal single-select list with fzf-style
// fuzzy filtering. It opens /dev/tty so it keeps working when the parent
// command is piping stdin/stdout, and falls back to a non-interactive
// error if no tty is available.
//
// Keybindings:
//
//	↑ / k          move up
//	↓ / j          move down
//	PgUp / PgDn    move by page
//	Home / End     jump
//	Enter          select
//	Esc / Ctrl-C   cancel
//	any other key  appends to the filter (fuzzy match)
//	Backspace      delete last filter character
//	Ctrl-U         clear filter
package picker

import (
	"errors"
	"fmt"
	"io"
	"os"
	"os/signal"
	"sort"
	"strings"
	"sync"
	"syscall"

	"golang.org/x/sys/unix"
)

// ErrCancelled is returned when the user pressed Esc or Ctrl-C.
var ErrCancelled = errors.New("picker: cancelled")

// ErrNoTTY is returned when stdin or stdout is not a terminal (e.g. piped).
var ErrNoTTY = errors.New("picker: not a tty")

// activeRestore guards a single in-flight picker session: when set, a
// signal handler can restore the terminal even if the goroutine running
// Pick() never gets to its deferred cleanup.
var (
	activeMu      sync.Mutex
	activeRestore func()
)

// installPanicAndSignalGuards wires up the safety net. Returns a function
// the caller must defer to remove the guards on the normal happy path.
func installPanicAndSignalGuards(restore func()) func() {
	activeMu.Lock()
	activeRestore = restore
	activeMu.Unlock()

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM, syscall.SIGHUP, syscall.SIGQUIT)
	stop := make(chan struct{})
	go func() {
		select {
		case sig := <-sigCh:
			activeMu.Lock()
			r := activeRestore
			activeRestore = nil
			activeMu.Unlock()
			if r != nil {
				r()
			}
			// Re-raise the signal with default handler so the process exits
			// the way the user expects.
			signal.Reset(sig.(syscall.Signal))
			_ = syscall.Kill(os.Getpid(), sig.(syscall.Signal))
		case <-stop:
			return
		}
	}()
	return func() {
		signal.Stop(sigCh)
		close(stop)
		activeMu.Lock()
		activeRestore = nil
		activeMu.Unlock()
	}
}

// Item is a row in the picker. Label is shown bold, Detail is dimmed next to it.
type Item struct {
	Label  string // primary key, e.g. alias name
	Detail string // secondary text, shown dimmed; not used for matching by default
	Match  string // optional override for matching (defaults to Label+" "+Detail)
}

// Pick shows items in a list and returns the chosen index. The prompt
// is shown above the list.
func Pick(prompt string, items []Item) (int, error) {
	if len(items) == 0 {
		return -1, fmt.Errorf("picker: nothing to pick from")
	}
	tty, err := openTTY()
	if err != nil {
		return -1, ErrNoTTY
	}
	defer tty.Close()

	st := newState(prompt, items, tty)

	// Belt-and-braces: also handle async signals and panics so the terminal
	// is *always* restored. Without this, Ctrl-C reaching us before defer
	// runs (or any panic) would leave bash with echo turned off.
	uninstall := installPanicAndSignalGuards(st.restore)
	defer uninstall()
	defer func() {
		if r := recover(); r != nil {
			st.restore()
			panic(r) // re-panic with terminal already cleaned
		}
	}()
	defer st.restore()

	st.render()

	for {
		key, err := readKey(tty)
		if err != nil {
			return -1, err
		}
		switch key.kind {
		case keyEnter:
			if len(st.filtered) == 0 {
				continue
			}
			return st.filtered[st.cursor], nil
		case keyEsc, keyCtrlC:
			return -1, ErrCancelled
		case keyUp:
			st.move(-1)
		case keyDown:
			st.move(1)
		case keyPgUp:
			st.move(-st.viewport())
		case keyPgDn:
			st.move(st.viewport())
		case keyHome:
			st.cursor = 0
			st.scroll = 0
		case keyEnd:
			st.cursor = len(st.filtered) - 1
		case keyBackspace:
			if len(st.query) > 0 {
				st.query = st.query[:len(st.query)-1]
				st.refilter()
			}
		case keyCtrlU:
			st.query = ""
			st.refilter()
		case keyRune:
			st.query += string(key.r)
			st.refilter()
		}
		st.render()
	}
}

// PickLabels is a convenience for the common "just pick a string" case.
func PickLabels(prompt string, labels []string) (string, error) {
	items := make([]Item, len(labels))
	for i, l := range labels {
		items[i] = Item{Label: l}
	}
	idx, err := Pick(prompt, items)
	if err != nil {
		return "", err
	}
	return labels[idx], nil
}

// state ------------------------------------------------------------------

type state struct {
	prompt   string
	items    []Item
	filtered []int // indices into items, ordered by score
	query    string
	cursor   int // index into filtered
	scroll   int // first visible row (index into filtered)
	tty      *os.File
	prevTerm *unix.Termios
	rendered int  // number of lines drawn last time, for cleanup
	restored bool // guards against double-restore from signal + defer
}

func newState(prompt string, items []Item, tty *os.File) *state {
	prev, _ := setRaw(tty)
	st := &state{prompt: prompt, items: items, tty: tty, prevTerm: prev}
	st.refilter()
	// hide cursor
	fmt.Fprint(tty, "\x1b[?25l")
	return st
}

// restore is safe to call from any goroutine and is idempotent. It clears
// the rendered region, shows the cursor and reverts termios. We do *not*
// touch tty.Close() here because the signal-handler path may race with
// the main goroutine's defer chain.
func (st *state) restore() {
	if st == nil {
		return
	}
	if st.restored {
		// Even on second call, make absolutely sure echo is on -- a paranoid
		// `stty sane` equivalent for the (rare) case the first restore raced.
		ensureSane(st.tty)
		return
	}
	st.restored = true

	// Cursor is on the last drawn line. Walk up to the first one, clearing
	// each, then clear the leading blank line we inserted in the first
	// render() call, and finally leave the cursor on column 0 of where the
	// user's prompt was before we drew anything.
	for i := 0; i < st.rendered-1; i++ {
		fmt.Fprint(st.tty, "\x1b[2K\x1b[1A") // clear, up
	}
	fmt.Fprint(st.tty, "\x1b[2K") // clear the very first drawn line too
	// Also wipe the spacer newline we emitted before the first render.
	fmt.Fprint(st.tty, "\x1b[1A\x1b[2K")
	fmt.Fprint(st.tty, "\r\x1b[?25h")

	if st.prevTerm != nil {
		_ = unix.IoctlSetTermios(int(st.tty.Fd()), unix.TCSETS, st.prevTerm)
	} else {
		ensureSane(st.tty)
	}
}

// ensureSane is the last-ditch fallback when we never captured the previous
// termios (or the previous restore failed): force the canonical/echo flags
// back on so bash remains usable.
func ensureSane(tty *os.File) {
	t, err := unix.IoctlGetTermios(int(tty.Fd()), unix.TCGETS)
	if err != nil {
		return
	}
	t.Lflag |= unix.ICANON | unix.ECHO | unix.ECHOE | unix.ECHOK | unix.ECHOCTL | unix.ISIG
	t.Iflag |= unix.IXON | unix.ICRNL | unix.BRKINT
	t.Oflag |= unix.OPOST | unix.ONLCR
	t.Cc[unix.VMIN] = 1
	t.Cc[unix.VTIME] = 0
	_ = unix.IoctlSetTermios(int(tty.Fd()), unix.TCSETS, t)
}

func (st *state) viewport() int { return 10 }

func (st *state) move(delta int) {
	if len(st.filtered) == 0 {
		return
	}
	st.cursor += delta
	if st.cursor < 0 {
		st.cursor = 0
	}
	if st.cursor >= len(st.filtered) {
		st.cursor = len(st.filtered) - 1
	}
	if st.cursor < st.scroll {
		st.scroll = st.cursor
	}
	vp := st.viewport()
	if st.cursor >= st.scroll+vp {
		st.scroll = st.cursor - vp + 1
	}
}

func (st *state) refilter() {
	st.filtered = filter(st.items, st.query)
	st.cursor = 0
	st.scroll = 0
}

func (st *state) render() {
	// Move to top-left of our region, then print prompt + filter + items.
	if st.rendered > 0 {
		// Already drawn once: rewind to the first line we drew.
		fmt.Fprintf(st.tty, "\r\x1b[%dA", st.rendered-1)
	} else {
		// First render: the cursor is wherever the user's previous command
		// left it (typically at the end of the typed command line). Drop to
		// a fresh line so we have a known, column-0 starting point. This
		// prevents the header from wrapping next to the existing prompt.
		fmt.Fprint(st.tty, "\r\n")
	}
	out := &strings.Builder{}

	// Header line
	out.WriteString("\x1b[2K") // clear line
	out.WriteString("\x1b[1m")
	out.WriteString(st.prompt)
	out.WriteString("\x1b[0m  \x1b[2m(↑↓ select, type to filter, Enter to confirm, Esc to cancel)\x1b[0m\n")

	// Filter row
	out.WriteString("\x1b[2K\x1b[36m> \x1b[0m")
	out.WriteString(st.query)
	out.WriteString("\x1b[2m_\x1b[0m\n")

	// Items
	vp := st.viewport()
	end := st.scroll + vp
	if end > len(st.filtered) {
		end = len(st.filtered)
	}
	for row := 0; row < vp; row++ {
		out.WriteString("\x1b[2K") // clear line
		idx := st.scroll + row
		if idx >= end {
			out.WriteString("\n")
			continue
		}
		it := st.items[st.filtered[idx]]
		if idx == st.cursor {
			out.WriteString("\x1b[7m▶ ") // reverse video
		} else {
			out.WriteString("  ")
		}
		out.WriteString(it.Label)
		if it.Detail != "" {
			out.WriteString("  \x1b[2m")
			out.WriteString(truncate(it.Detail, 80))
			out.WriteString("\x1b[0m")
		}
		out.WriteString("\x1b[0m\n")
	}

	// Footer
	out.WriteString("\x1b[2K")
	if len(st.filtered) == 0 {
		out.WriteString("\x1b[2m(no matches)\x1b[0m")
	} else {
		fmt.Fprintf(out, "\x1b[2m%d/%d\x1b[0m", len(st.filtered), len(st.items))
	}

	st.rendered = vp + 3 // header + filter + items + footer (footer overwrites... see below)
	// Above, items take vp lines, plus header (1), filter (1), footer (1) = vp+3
	io.WriteString(st.tty, out.String())
}

// truncate keeps n displayable chars; rough but good enough for ascii.
func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n-1] + "…"
}

// filter returns indices of items whose Match string contains all
// characters of q in order (fzf-style); empty query returns all in
// original order.
func filter(items []Item, q string) []int {
	if q == "" {
		out := make([]int, len(items))
		for i := range items {
			out[i] = i
		}
		return out
	}
	q = strings.ToLower(q)
	type scored struct {
		i, score int
	}
	var hits []scored
	for i, it := range items {
		hay := it.Match
		if hay == "" {
			hay = it.Label + " " + it.Detail
		}
		hay = strings.ToLower(hay)
		if score, ok := fuzzyScore(hay, q); ok {
			hits = append(hits, scored{i, score})
		}
	}
	sort.SliceStable(hits, func(i, j int) bool { return hits[i].score < hits[j].score })
	out := make([]int, len(hits))
	for i, h := range hits {
		out[i] = h.i
	}
	return out
}

// fuzzyScore returns (score, true) if all characters of q appear in hay
// in order. Lower score = better match. Substring matches and matches at
// word boundaries are scored higher (i.e. lower number).
func fuzzyScore(hay, q string) (int, bool) {
	if q == "" {
		return 0, true
	}
	if i := strings.Index(hay, q); i >= 0 {
		// substring match: score by position (earlier is better).
		return i, true
	}
	// fall back to subsequence match.
	hi, qi := 0, 0
	score := 0
	prev := -2
	for hi < len(hay) && qi < len(q) {
		if hay[hi] == q[qi] {
			if hi != prev+1 {
				score += 100 + hi // gap penalty
			}
			prev = hi
			qi++
		}
		hi++
	}
	if qi == len(q) {
		return 1000 + score, true
	}
	return 0, false
}
