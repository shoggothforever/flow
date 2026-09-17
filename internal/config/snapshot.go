package config

import "fmt"

// ExportSnapshot validates the active config and atomically writes a portable
// copy to destination. Runtime state such as scheduler history is not part of
// Config and is therefore never exported.
func ExportSnapshot(source *Store, destination string) (string, error) {
	cfg, err := source.Load()
	if err != nil {
		return "", err
	}
	if err := cfg.Validate(); err != nil {
		return "", err
	}
	dest, err := NewStore(destination)
	if err != nil {
		return "", err
	}
	if err := dest.Save(cfg); err != nil {
		return "", fmt.Errorf("write snapshot: %w", err)
	}
	return dest.Path, nil
}

// ImportSnapshot validates and migrates a snapshot before replacing the active
// config under the destination store's normal flock + atomic-write protection.
func ImportSnapshot(source string, destination *Store) (string, error) {
	src, err := NewStore(source)
	if err != nil {
		return "", err
	}
	cfg, err := src.Load()
	if err != nil {
		return "", err
	}
	if _, err := Migrate(cfg); err != nil {
		return "", err
	}
	if err := cfg.Validate(); err != nil {
		return "", err
	}
	if err := destination.Mutate(func(current *Config) error {
		*current = *cfg
		return nil
	}); err != nil {
		return "", fmt.Errorf("replace active config: %w", err)
	}
	return src.Path, nil
}
