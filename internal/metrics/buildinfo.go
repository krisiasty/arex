package metrics

import "github.com/krisiasty/arex/internal/buildinfo"

// Build is what is running.
type Build = buildinfo.Build

// BuildLabels reports what is running, for logging at startup. The same values
// back arex_build_info, so the log line, the metric and -version cannot
// disagree. The version itself is injected into internal/buildinfo.
func BuildLabels() Build { return buildinfo.Get() }
