package ui

import (
	"strings"
	"testing"
)

func TestIndexIncludesResponsiveAccessibleTabShell(t *testing.T) {
	data, err := staticFS.ReadFile("static/index.html")
	if err != nil {
		t.Fatal(err)
	}
	html := string(data)
	for _, marker := range []string{
		".hidden { display: none !important; }",
		"@media (max-width: 560px)",
		`role="dialog" aria-modal="true"`,
		`role="tablist" aria-label="Run output tabs"`,
		`id="log-empty"`,
		"function refreshRunButtons()",
		"function updateLogControls()",
		`data-action="run"`,
	} {
		if !strings.Contains(html, marker) {
			t.Errorf("index.html missing %q", marker)
		}
	}
	for _, obsolete := range []string{
		"let CURRENT_RUN",
		"function setRunButtonState(",
		`onclick="runItem(`,
	} {
		if strings.Contains(html, obsolete) {
			t.Errorf("index.html still contains obsolete pattern %q", obsolete)
		}
	}
}

func TestIndexResourceSectionsHaveMatchingNavigation(t *testing.T) {
	data, err := staticFS.ReadFile("static/index.html")
	if err != nil {
		t.Fatal(err)
	}
	html := string(data)
	for _, resource := range []string{"projects", "scripts", "cmds", "greps", "finds", "schedules"} {
		if !strings.Contains(html, `data-tab="`+resource+`"`) {
			t.Errorf("missing navigation for %s", resource)
		}
		if !strings.Contains(html, `id="tab-`+resource+`"`) {
			t.Errorf("missing section for %s", resource)
		}
	}
	if strings.Contains(html, `data-tab="layouts"`) || strings.Contains(html, `id="tab-layouts"`) {
		t.Error("legacy layout UI is still present")
	}
}
