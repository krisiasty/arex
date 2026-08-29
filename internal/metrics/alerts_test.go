package metrics

import (
	"os"
	"regexp"
	"sort"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

// metricRef finds anything shaped like one of arex's metric names.
var metricRef = regexp.MustCompile(`\b(ar(?:ista|ex)_[a-z0-9_]+)\b`)

// A rule naming a metric that does not exist fires never and is noticed by
// nobody, which is the worst way for an alert to fail. The catalogue is the
// authority, so every name the shipped rules mention has to appear in it.
func TestAlertRulesOnlyReferenceRealMetrics(t *testing.T) {
	body, err := os.ReadFile("../../monitoring/alerts.yaml")
	if err != nil {
		t.Fatal(err)
	}

	var unknown []string
	seen := map[string]bool{}
	for _, m := range metricRef.FindAllStringSubmatch(string(body), -1) {
		name := m[1]
		if seen[name] {
			continue
		}
		seen[name] = true
		// Counters are exposed with a _total suffix that the catalogue carries,
		// so names are compared as written.
		if _, ok := descs[name]; !ok {
			unknown = append(unknown, name)
		}
	}
	sort.Strings(unknown)
	if len(unknown) > 0 {
		t.Errorf("alert rules reference metrics that are not in the catalogue: %s",
			strings.Join(unknown, ", "))
	}
	if len(seen) < 15 {
		t.Errorf("only %d metrics referenced; the rules file looks truncated", len(seen))
	}
}

// The labels the annotations interpolate have to exist on the metric being
// alerted on, or the summary renders an empty string where a switch name
// should be.
func TestAlertAnnotationsUseRealLabels(t *testing.T) {
	body, err := os.ReadFile("../../monitoring/alerts.yaml")
	if err != nil {
		t.Fatal(err)
	}

	// Every label used anywhere in the catalogue.
	known := map[string]bool{}
	for _, def := range metricDefs {
		for _, l := range def.labels {
			known[l] = true
		}
	}

	ref := regexp.MustCompile(`\$labels\.([a-z_]+)`)
	var unknown []string
	for _, m := range ref.FindAllStringSubmatch(string(body), -1) {
		if !known[m[1]] {
			unknown = append(unknown, m[1])
		}
	}
	if len(unknown) > 0 {
		sort.Strings(unknown)
		t.Errorf("annotations interpolate labels no metric has: %s", strings.Join(unknown, ", "))
	}
}

// The per-consumer files are generated, and CI fails if they drift. This
// catches the other half: a generator that produces something valid-looking
// with the rules nested wrongly, which a diff check cannot see.
func TestGeneratedRuleFilesCarryTheSameRules(t *testing.T) {
	type group struct {
		Name  string           `yaml:"name"`
		Rules []map[string]any `yaml:"rules"`
	}
	rules := func(path string, nested bool) int {
		t.Helper()
		//nolint:gosec // G304: the paths are literals a few lines below
		b, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		var doc struct {
			Groups []group `yaml:"groups"`
			Spec   struct {
				Groups []group `yaml:"groups"`
			} `yaml:"spec"`
		}
		if err := yaml.Unmarshal(b, &doc); err != nil {
			t.Fatalf("%s: %v", path, err)
		}
		gs := doc.Groups
		if nested {
			gs = doc.Spec.Groups
		}
		n := 0
		for _, g := range gs {
			n += len(g.Rules)
		}
		return n
	}

	want := rules("../../monitoring/alerts.yaml", false)
	if want == 0 {
		t.Fatal("no rules in monitoring/alerts.yaml")
	}
	for _, f := range []string{
		"../../monitoring/prometheusrule.yaml",
		"../../monitoring/vmrule.yaml",
	} {
		if got := rules(f, true); got != want {
			t.Errorf("%s carries %d rules, the source has %d", f, got, want)
		}
	}
}
