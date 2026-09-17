package picker

import (
	"errors"
	"io"
	"os"
)

type keyKind int

const (
	keyRune keyKind = iota
	keyEnter
	keyEsc
	keyCtrlC
	keyCtrlU
	keyBackspace
	keyUp
	keyDown
	keyPgUp
	keyPgDn
	keyHome
	keyEnd
)

type keyEvent struct {
	kind keyKind
	r    rune
}

// readKey blocks until one keystroke is available on f and returns its
// semantic kind. We support the tiny subset of ANSI escape sequences the
// picker actually uses.
func readKey(f *os.File) (keyEvent, error) {
	buf := make([]byte, 1)
	n, err := f.Read(buf)
	if err != nil {
		if errors.Is(err, io.EOF) {
			return keyEvent{kind: keyEsc}, nil
		}
		return keyEvent{}, err
	}
	if n == 0 {
		return keyEvent{kind: keyEsc}, nil
	}
	c := buf[0]
	switch c {
	case 0x03:
		return keyEvent{kind: keyCtrlC}, nil
	case 0x15:
		return keyEvent{kind: keyCtrlU}, nil
	case 0x0D, 0x0A:
		return keyEvent{kind: keyEnter}, nil
	case 0x7F, 0x08:
		return keyEvent{kind: keyBackspace}, nil
	case 0x1B:
		// Could be a bare Esc or the start of a CSI sequence.
		seq := make([]byte, 5)
		// Best-effort read of remaining bytes; if nothing follows, treat as Esc.
		_ = setReadDeadlineShort(f)
		nn, _ := f.Read(seq)
		_ = clearReadDeadline(f)
		if nn == 0 {
			return keyEvent{kind: keyEsc}, nil
		}
		s := string(seq[:nn])
		switch s {
		case "[A", "OA":
			return keyEvent{kind: keyUp}, nil
		case "[B", "OB":
			return keyEvent{kind: keyDown}, nil
		case "[5~":
			return keyEvent{kind: keyPgUp}, nil
		case "[6~":
			return keyEvent{kind: keyPgDn}, nil
		case "[H", "OH", "[1~", "[7~":
			return keyEvent{kind: keyHome}, nil
		case "[F", "OF", "[4~", "[8~":
			return keyEvent{kind: keyEnd}, nil
		}
		return keyEvent{kind: keyEsc}, nil
	}
	if c >= 0x20 && c < 0x7F {
		return keyEvent{kind: keyRune, r: rune(c)}, nil
	}
	// Unknown control byte; ignore by sending a no-op rune (space won't be appended... return blank rune is awkward).
	// Treat as benign and re-read.
	return readKey(f)
}

// We don't really set a deadline (regular files don't support one on
// linux ttys via SetDeadline reliably); these are stubs left in place
// so the call sites stay clean if we ever switch to non-blocking I/O.
func setReadDeadlineShort(f *os.File) error { return nil }
func clearReadDeadline(f *os.File) error    { return nil }
