// Store is responsible for resolving the config path, loading, atomically
// saving, and serialising concurrent writers via flock.
package config

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"time"

	"golang.org/x/sys/unix"
)

// DefaultRelPath is the default location under $HOME for the config file.
const DefaultRelPath = ".config/flow/config.json"

// EnvVar overrides the default path when set.
const EnvVar = "FLOW_CONFIG"

// ResolvePath returns the effective config path, honouring (in order):
//  1. explicit override
//  2. FLOW_CONFIG environment variable
//  3. $HOME/.config/flow/config.json
func ResolvePath(override string) (string, error) {
	if override != "" {
		return expandHome(override)
	}
	if env := os.Getenv(EnvVar); env != "" {
		return expandHome(env)
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("locate home dir: %w", err)
	}
	return filepath.Join(home, DefaultRelPath), nil
}

// Store is the entrypoint for config IO.
type Store struct {
	Path string
}

// NewStore resolves the config path and returns a Store. The file does not need
// to exist yet; Load will return an empty Config in that case.
func NewStore(override string) (*Store, error) {
	p, err := ResolvePath(override)
	if err != nil {
		return nil, err
	}
	return &Store{Path: p}, nil
}

// Load reads the config from disk. If the file does not exist, returns a fresh
// Config with the current schema version (and no error).
func (s *Store) Load() (*Config, error) {
	data, err := os.ReadFile(s.Path)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return &Config{SchemaVersion: CurrentSchemaVersion}, nil
		}
		return nil, fmt.Errorf("read %s: %w", s.Path, err)
	}
	var c Config
	if len(data) == 0 {
		c.SchemaVersion = CurrentSchemaVersion
		return &c, nil
	}
	if err := json.Unmarshal(data, &c); err != nil {
		return nil, fmt.Errorf("parse %s: %w", s.Path, err)
	}
	if c.SchemaVersion == 0 {
		c.SchemaVersion = CurrentSchemaVersion
	}
	return &c, nil
}

// Save validates and atomically writes the config to disk:
//  1. ensure parent dir exists
//  2. write to <path>.tmp, fsync, rename
//  3. file mode 0600 (config may contain machine-specific paths)
func (s *Store) Save(c *Config) error {
	if err := c.Validate(); err != nil {
		return err
	}
	if c.SchemaVersion == 0 {
		c.SchemaVersion = CurrentSchemaVersion
	}
	dir := filepath.Dir(s.Path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("create config dir: %w", err)
	}

	data, err := json.MarshalIndent(c, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal config: %w", err)
	}
	data = append(data, '\n')

	tmp, err := os.CreateTemp(dir, ".config-*.json.tmp")
	if err != nil {
		return fmt.Errorf("create temp file: %w", err)
	}
	tmpPath := tmp.Name()
	cleanup := true
	defer func() {
		if cleanup {
			_ = os.Remove(tmpPath)
		}
	}()
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("write temp file: %w", err)
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("fsync temp file: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("close temp file: %w", err)
	}
	if err := os.Chmod(tmpPath, 0o600); err != nil {
		return fmt.Errorf("chmod temp file: %w", err)
	}
	if err := os.Rename(tmpPath, s.Path); err != nil {
		return fmt.Errorf("rename temp file: %w", err)
	}
	cleanup = false
	return nil
}

// Mutate runs fn under an exclusive flock on the config file (or its lockfile
// when the config doesn't exist yet), passing in the loaded config. If fn
// returns nil, the (possibly modified) config is saved.
func (s *Store) Mutate(fn func(*Config) error) error {
	if err := os.MkdirAll(filepath.Dir(s.Path), 0o755); err != nil {
		return fmt.Errorf("create config dir: %w", err)
	}
	lockPath := s.Path + ".lock"
	lf, err := os.OpenFile(lockPath, os.O_RDWR|os.O_CREATE, 0o600)
	if err != nil {
		return fmt.Errorf("open lockfile: %w", err)
	}
	defer lf.Close()

	if err := flockWithTimeout(int(lf.Fd()), 3*time.Second); err != nil {
		return err
	}
	defer func() { _ = unix.Flock(int(lf.Fd()), unix.LOCK_UN) }()

	c, err := s.Load()
	if err != nil {
		return err
	}
	if err := fn(c); err != nil {
		return err
	}
	return s.Save(c)
}

// flockWithTimeout retries non-blocking flock until success or timeout.
func flockWithTimeout(fd int, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	for {
		err := unix.Flock(fd, unix.LOCK_EX|unix.LOCK_NB)
		if err == nil {
			return nil
		}
		if !errors.Is(err, unix.EWOULDBLOCK) {
			return fmt.Errorf("flock: %w", err)
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("config is locked by another flow process; try again in a moment")
		}
		time.Sleep(50 * time.Millisecond)
	}
}

// expandHome expands a leading ~ to the user's home directory.
func expandHome(p string) (string, error) {
	if len(p) == 0 || p[0] != '~' {
		return p, nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	if p == "~" {
		return home, nil
	}
	if p[1] == '/' {
		return filepath.Join(home, p[2:]), nil
	}
	return p, nil
}
