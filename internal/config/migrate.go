// Schema migrations advance older config documents to CurrentSchemaVersion.
package config

import "fmt"

// Migrate brings c up to CurrentSchemaVersion in-place. It is safe to call on
// an already up-to-date config (no-op).
func Migrate(c *Config) (changed bool, err error) {
	if c.SchemaVersion == 0 {
		c.SchemaVersion = CurrentSchemaVersion
		return true, nil
	}
	for c.SchemaVersion < CurrentSchemaVersion {
		switch c.SchemaVersion {
		case 1:
			// v2 removes the tmux layout resource. Unknown legacy `layouts`
			// fields have already been ignored by JSON decoding.
			c.SchemaVersion = 2
			changed = true
		default:
			return changed, fmt.Errorf("no migration path from schema_version %d", c.SchemaVersion)
		}
	}
	if c.SchemaVersion > CurrentSchemaVersion {
		return changed, fmt.Errorf("schema_version %d is newer than supported %d", c.SchemaVersion, CurrentSchemaVersion)
	}
	return changed, nil
}
