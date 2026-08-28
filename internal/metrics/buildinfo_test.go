package metrics

import (
	"strings"
	"testing"
	"time"

	"github.com/krisiasty/arex/config"
	"github.com/krisiasty/arex/internal/collector"
)

// Knowing which build is running is the first question asked of anything
// deployed, so it is exposed rather than left to the operator to track.
func TestBuildInfoIsExposed(t *testing.T) {
	store, err := collector.NewStore([]config.SwitchConfig{
		{Host: "h", Username: "u", Password: "p", Name: "sw1"},
	}, collectAll(), 30*time.Second)
	if err != nil {
		t.Fatal(err)
	}

	out := gather(t, store, 90*time.Second)

	line := ""
	for _, l := range strings.Split(out, "\n") {
		if strings.HasPrefix(l, "arex_build_info{") {
			line = l
		}
	}
	if line == "" {
		t.Fatal("no arex_build_info series")
	}
	t.Logf("%s", line)

	for _, label := range []string{"version=", "revision=", "go_version="} {
		if !strings.Contains(line, label) {
			t.Errorf("build info missing %s: %s", label, line)
		}
	}
	if !strings.HasSuffix(line, " 1") {
		t.Errorf("build info should be an info metric with value 1: %s", line)
	}
	// It describes the process, not a switch, which is why it carries no
	// switch label and uses its own prefix.
	if strings.Contains(line, `switch=`) {
		t.Errorf("build info must not be per-switch: %s", line)
	}
}

// Emitted once regardless of how many switches are configured.
func TestBuildInfoEmittedOncePerScrape(t *testing.T) {
	store, err := collector.NewStore([]config.SwitchConfig{
		{Host: "h1", Username: "u", Password: "p", Name: "sw1"},
		{Host: "h2", Username: "u", Password: "p", Name: "sw2"},
		{Host: "h3", Username: "u", Password: "p", Name: "sw3"},
	}, collectAll(), 30*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	out := gather(t, store, 90*time.Second)
	if n := strings.Count(out, "arex_build_info{"); n != 1 {
		t.Errorf("%d build info series, want 1", n)
	}
}

// Go version is always knowable; the rest may be absent in an unusual build,
// and must degrade to a placeholder rather than an empty label. Read through
// this package's own view, since that is what feeds the metric.
func TestBuildInfoNeverHasEmptyLabels(t *testing.T) {
	info := BuildLabels()
	for name, v := range map[string]string{
		"version": info.Version, "revision": info.Revision, "go_version": info.GoVersion,
	} {
		if v == "" {
			t.Errorf("%s is empty; want a placeholder such as \"unknown\"", name)
		}
	}
}
