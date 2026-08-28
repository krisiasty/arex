package buildinfo

import (
	"strings"
	"testing"
)

// -version has to answer the question "which build is this", and every field
// falls back to a placeholder rather than being empty: an empty value is
// indistinguishable from an absent one, both in a terminal and in PromQL.
func TestStringNamesTheBinaryAndNeverHasEmptyFields(t *testing.T) {
	got := String()
	if !strings.HasPrefix(got, "arex ") {
		t.Errorf("version output should start with the program name: %q", got)
	}
	for _, want := range []string{"commit ", "built ", "go"} {
		if !strings.Contains(got, want) {
			t.Errorf("version output is missing %q: %q", want, got)
		}
	}
	// A field left empty would render as "commit , built" and read as a bug.
	if strings.Contains(got, "  ") || strings.Contains(got, ", )") {
		t.Errorf("an empty field left a gap: %q", got)
	}
}

func TestFieldsHaveFallbacks(t *testing.T) {
	b := Get()
	for name, v := range map[string]string{
		"version":   b.Version,
		"revision":  b.Revision,
		"goVersion": b.GoVersion,
		"modified":  b.Modified,
		"built":     b.Built,
	} {
		if v == "" {
			t.Errorf("%s is empty; it should fall back to a placeholder", name)
		}
	}
}

// A built binary reports its injected version; a test binary cannot, so this
// only checks the plumbing exists and is used.
func TestInjectedVersionWins(t *testing.T) {
	saved := Version
	t.Cleanup(func() { Version = saved; reset() })

	Version = "v9.9.9"
	reset()
	if got := Get().Version; got != "v9.9.9" {
		t.Errorf("version = %q, want the linker-injected value", got)
	}
	if !strings.Contains(String(), "v9.9.9") {
		t.Errorf("String() = %q, want it to carry the version", String())
	}
}
