package metrics

import (
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

// Build is the exported view of the build labels, for the startup log.
type Build struct {
	Version   string
	Revision  string
	GoVersion string
	Modified  string
}

// BuildLabels reports what is running, for logging at startup. The same values
// back arex_build_info, so the log line and the metric cannot disagree.
func BuildLabels() Build {
	b := buildInfo()
	return Build{
		Version:   b.version,
		Revision:  b.revision,
		GoVersion: b.goVersion,
		Modified:  b.modified,
	}
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
