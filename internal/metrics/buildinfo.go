package metrics

import (
	"io"
	"runtime"
	"runtime/debug"
	"sync"
)

// Version can be set at link time for released builds:
//
//	go build -ldflags "-X github.com/krisiasty/arex/internal/metrics.Version=v1.2.3"
//
// Left unset, the module version recorded by the toolchain is used, which is
// "(devel)" for a local build.
var Version string

type buildInfoLabels struct {
	version   string
	revision  string
	goVersion string
	modified  string
}

var (
	buildInfoOnce sync.Once
	cachedBuild   buildInfoLabels
)

// buildInfo reports what is running.
//
// The revision comes from the VCS stamps the Go toolchain embeds
// automatically when building from a repository, so a plain "go build"
// produces a usable answer without link-time flags. Every label falls back
// to a placeholder: an empty label value is indistinguishable from an absent
// one in PromQL, which would make "which build is this" unanswerable exactly
// when it matters.
func buildInfo() buildInfoLabels {
	buildInfoOnce.Do(func() {
		b := buildInfoLabels{
			version:   Version,
			revision:  "unknown",
			goVersion: runtime.Version(),
			modified:  "unknown",
		}
		if info, ok := debug.ReadBuildInfo(); ok {
			if b.version == "" {
				b.version = info.Main.Version
			}
			for _, s := range info.Settings {
				switch s.Key {
				case "vcs.revision":
					b.revision = s.Value
				case "vcs.modified":
					b.modified = s.Value
				}
			}
		}
		if b.version == "" {
			b.version = "unknown"
		}
		if b.goVersion == "" {
			b.goVersion = "unknown"
		}
		cachedBuild = b
	})
	return cachedBuild
}

// writeBuildInfo emits arex's own identity.
//
// Prefixed arex_ rather than arista_ because it describes the exporter
// process, not a switch: it is the only series here with no switch label,
// and that structural difference is worth making visible in the name.
func writeBuildInfo(w io.Writer) {
	b := buildInfo()
	gauge(w, "arex_build_info", labels(
		"version", b.version,
		"revision", b.revision,
		"go_version", b.goVersion,
		"modified", b.modified,
	), 1)
}
