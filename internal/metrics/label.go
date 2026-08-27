package metrics

import (
	"strconv"
	"strings"
)

// The Prometheus text format defines exactly three escape sequences in a
// label value: \\ \n and \". Anything else -- Go's %q would emit \t, \x1b or
// é -- is rejected by the parser, which discards the whole scrape. Label
// values here carry operator-authored text (port and BGP peer descriptions)
// and vendor EEPROM strings, so they cannot be trusted to be clean.
func escapeLabelValue(s string) string {
	if !needsEscape(s) {
		return s
	}
	var b strings.Builder
	b.Grow(len(s) + 8)
	for _, r := range s {
		switch {
		case r == '\\':
			b.WriteString(`\\`)
		case r == '"':
			b.WriteString(`\"`)
		case r == '\n':
			b.WriteString(`\n`)
		case r < 0x20 || r == 0x7f:
			// No escape exists for other control characters; replace them
			// rather than dropping, so adjacent words stay separated.
			b.WriteByte(' ')
		default:
			b.WriteRune(r)
		}
	}
	return b.String()
}

func needsEscape(s string) bool {
	for i := 0; i < len(s); i++ {
		if c := s[i]; c == '\\' || c == '"' || c < 0x20 || c == 0x7f {
			return true
		}
	}
	return false
}

// labels builds an escaped label set from alternating name/value pairs.
// A trailing name with no value is ignored.
func labels(kv ...string) string {
	var b strings.Builder
	for i := 0; i+1 < len(kv); i += 2 {
		if b.Len() > 0 {
			b.WriteByte(',')
		}
		b.WriteString(kv[i])
		b.WriteString(`="`)
		b.WriteString(escapeLabelValue(kv[i+1]))
		b.WriteByte('"')
	}
	return b.String()
}

// join concatenates label sets.
func join(sets ...string) string {
	out := make([]string, 0, len(sets))
	for _, s := range sets {
		if s != "" {
			out = append(out, s)
		}
	}
	return strings.Join(out, ",")
}

func itoa(v int) string    { return strconv.Itoa(v) }
func utoa(v uint64) string { return strconv.FormatUint(v, 10) }
