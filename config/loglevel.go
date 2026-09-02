package config

import (
	"fmt"
	"log/slog"
	"strings"
)

// LogLevels are the accepted values for logLevel and -log-level, in the order
// they are reported when one is rejected.
//
// An explicit table rather than slog.Level.UnmarshalText, which also accepts
// arithmetic like "Error-8" -- that marshals back as "INFO", so a config could
// name a level it does not appear to name. It also accepts "WARNING" nowhere:
// slog's vocabulary is WARN alone, while syslog, Python and most operators say
// warning. Both spell the same level here, since being told "warning is not a
// level" helps nobody.
var LogLevels = []string{"debug", "info", "warn", "warning", "error"}

// ParseLogLevel converts a configured level name to its slog level.
//
// Matching ignores case, so WARN, Warn and warn are one value.
func ParseLogLevel(name string) (slog.Level, error) {
	switch strings.ToLower(strings.TrimSpace(name)) {
	case "debug":
		return slog.LevelDebug, nil
	case "info":
		return slog.LevelInfo, nil
	case "warn", "warning":
		return slog.LevelWarn, nil
	case "error":
		return slog.LevelError, nil
	}
	return 0, fmt.Errorf("unknown log level %q: expected one of %s",
		name, strings.Join(LogLevels, ", "))
}
