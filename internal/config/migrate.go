// Schema migrations advance older config documents to CurrentSchemaVersion.
package config

import (
	"fmt"
	"net/url"
	"strings"
)

func legacyRequirementTitle(rawURL string) string {
	u, err := url.Parse(rawURL)
	if err == nil && u.Hostname() != "" {
		if label := strings.TrimSuffix(u.Hostname()+u.EscapedPath(), "/"); label != "" {
			return label
		}
	}
	return "未命名需求"
}

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
		case 2:
			// v3 makes the requirement name mandatory and its URL optional.
			// Older records always had a URL, so retain a useful visible name.
			for i := range c.TAPDRequirements {
				if strings.TrimSpace(c.TAPDRequirements[i].Title) == "" {
					c.TAPDRequirements[i].Title = legacyRequirementTitle(c.TAPDRequirements[i].URL)
				}
			}
			c.SchemaVersion = 3
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
