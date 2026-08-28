package config

import (
	"os"
	"path/filepath"
	"testing"
)

// Load parses operator-supplied YAML through gopkg.in/yaml.v3 and then through
// this package's own unmarshalers. Neither may panic on any input: a config
// file is the one thing arex reads before it can report anything, and a panic
// there is a process that dies without saying why.
//
// yaml.v3 has had panics on malformed input in the past, which is the other
// reason this exists.
func FuzzLoad(f *testing.F) {
	f.Add("")
	f.Add("{")
	f.Add("collect:")
	f.Add("switches: []")
	f.Add("pollInterval: 30s\ncollect:\n  interfaces:\n    enabled: true\n")
	f.Add(`{"collect":{"phy":{"enabled":true,"interval":"5m"}}}`)
	f.Add("collect:\n  interfaces: &a\n    enabled: true\n  bgp: *a\n") // an anchor
	f.Add("\x00\x01\x02")
	f.Add("switches:\n  - host: 1\n    username: [x]\n")
	f.Add("stalenessLimit: !!binary AAAA\n")

	dir := f.TempDir()
	f.Fuzz(func(t *testing.T, body string) {
		p := filepath.Join(dir, "config.yaml")
		if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
		// Errors are the expected outcome for almost every input. Only a panic
		// is a failure, so the result is deliberately discarded.
		_, _ = Load(p)
	})
}
