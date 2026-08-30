// Package buildinfo reports which build is running.
//
// One place, so the -version output, the startup log line and the
// arex_build_info metric cannot disagree about what is deployed.
package buildinfo

import (
	"runtime"
	"runtime/debug"
	"sync"
)

// Version can be set at link time for released builds:
//
//	go build -ldflags "-X github.com/krisiasty/arex/internal/buildinfo.Version=v1.2.3"
//
// Left unset, the module version recorded by the toolchain is used, which is
// "(devel)" for a local build.
var Version string

// Build is what is running.
type Build struct {
	Version   string
	Revision  string
	GoVersion string
	Modified  string
	Built     string
}

// String is the one-line answer to "which build is this".
func (b Build) String() string {
	return "arex " + b.Version +
		" (commit " + b.Revision +
		", built " + b.Built +
		", " + b.GoVersion +
		", modified " + b.Modified + ")"
}

// String reports the running build in one line.
func String() string { return Get().String() }

var (
	once   sync.Once
	cached Build
)

// Get reports what is running.
//
// The revision, the modified flag and the build time come from the VCS stamps
// the Go toolchain embeds automatically when building from a repository, so a
// plain "go build" produces a usable answer without link-time flags. Every
// field falls back to a placeholder: an empty value is indistinguishable from
// an absent one, both in a terminal and in PromQL, which would make "which
// build is this" unanswerable exactly when it matters.
func Get() Build {
	once.Do(load)
	return cached
}

func load() {
	b := Build{
		Version:   Version,
		Revision:  "unknown",
		GoVersion: runtime.Version(),
		Modified:  "unknown",
		Built:     "unknown",
	}
	if info, ok := debug.ReadBuildInfo(); ok {
		if b.Version == "" {
			b.Version = info.Main.Version
		}
		for _, s := range info.Settings {
			switch s.Key {
			case "vcs.revision":
				b.Revision = s.Value
			case "vcs.modified":
				b.Modified = s.Value
			case "vcs.time":
				b.Built = s.Value
			}
		}
	}
	if b.Version == "" {
		b.Version = "unknown"
	}
	if b.GoVersion == "" {
		b.GoVersion = "unknown"
	}
	cached = b
}
