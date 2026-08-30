package config

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"
)

// ModuleConfig controls one command group: whether it is collected, and how
// often.
//
// A module's interval is separate from pollInterval because what the groups
// measure moves at very different rates. Interface counters and error
// detail change continuously and are the signals worth alerting on. Optical
// power and laser bias drift over weeks, so polling them every 30 seconds
// costs switch CPU for resolution nobody can use. PHY registers are worse
// still: on a switch observed for 123 days, its FEC counters had not moved
// for 107 of them.
type ModuleConfig struct {
	Enabled bool

	// Interval is zero when unset, meaning the module's default applies.
	Interval time.Duration
}

// defaultModuleInterval is the polling interval for modules whose data does
// not repay frequent collection. Anything absent here defaults to
// pollInterval.
//
// transceiver carries the genuinely predictive signals -- receive power and
// laser bias trend downward and upward respectively over weeks before a link
// fails -- so it is worth collecting, but not often. phy is a troubleshooting
// instrument rather than a predictor: its per-layer fault and flap counters
// localise a problem once you know there is one, while its error counters sit
// unchanged for months. ntp is bounded by ntpd itself: it polls its own
// upstream every 64 seconds at the fastest, so anything quicker re-reads
// numbers that cannot have moved. capacity is slow-moving too, and its
// highWatermark records the peak between polls, so a longer interval still
// sees a spike a plain gauge would have missed. esi is by far the largest
// overlay command -- 71% of the overlay payload, growing with every multihomed
// host -- and the slowest-moving: a designated-forwarder change is a
// consequence of a link or session failure the faster modules already report.
var defaultModuleInterval = map[string]time.Duration{
	"ntp":         time.Minute,
	"capacity":    5 * time.Minute,
	"esi":         15 * time.Minute,
	"transceiver": 5 * time.Minute,
	"phy":         15 * time.Minute,
}

// CollectSet is a collect block: command group name to its settings.
type CollectSet map[string]ModuleConfig

// UnmarshalJSON decodes the entries itself so an error can name the key that
// caused it. encoding/json does not add field context to errors returned by a
// value's own UnmarshalJSON, and "collect entry true is wrong" is no help in a
// block listing thirteen groups.
func (c *CollectSet) UnmarshalJSON(b []byte) error {
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(b, &raw); err != nil {
		return fmt.Errorf("collect must be an object of command groups: %w", err)
	}

	out := make(CollectSet, len(raw))
	for _, key := range sortedKeys(raw) {
		var m ModuleConfig
		if err := m.UnmarshalJSON(raw[key]); err != nil {
			return fmt.Errorf("collect %q: %w", key, err)
		}
		out[key] = m
	}
	*c = out
	return nil
}

// sortedKeys gives map iteration a fixed order, so a block with two mistakes
// in it always reports the same one first.
func sortedKeys(m map[string]json.RawMessage) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// UnmarshalJSON decodes one collect entry. A group is always an object:
//
//	"interfaces":  {"enabled": true}
//	"phy":         {"enabled": false}
//	"transceiver": {"enabled": true, "interval": "5m"}
//
// "enabled" is required for the same reason the collect block itself is: an
// absent value would have to mean something, and either meaning is a guess
// about intent.
func (m *ModuleConfig) UnmarshalJSON(b []byte) error {
	var obj struct {
		Enabled  *bool  `json:"enabled"`
		Interval string `json:"interval"`
	}
	// Checked before decoding, so a non-object is told the expected shape
	// rather than handed a dump of the decoder's struct type.
	if len(bytes.TrimSpace(b)) == 0 || bytes.TrimSpace(b)[0] != '{' {
		return fmt.Errorf("must be an object like "+
			"{\"enabled\": true, \"interval\": \"5m\"}, not %s", b)
	}
	dec := json.NewDecoder(bytes.NewReader(b))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&obj); err != nil {
		// The decoder prefixes its messages with "json: ", which says nothing
		// to someone reading their own config file back.
		return fmt.Errorf("%s; a group takes \"enabled\" and \"interval\"",
			strings.TrimPrefix(err.Error(), "json: "))
	}
	if obj.Enabled == nil {
		return errors.New("missing \"enabled\"; every group must state it explicitly")
	}
	m.Enabled = *obj.Enabled

	if obj.Interval != "" {
		d, err := time.ParseDuration(obj.Interval)
		if err != nil {
			return fmt.Errorf("invalid interval %q: %w", obj.Interval, err)
		}
		if d <= 0 {
			return fmt.Errorf("interval %q must be positive", obj.Interval)
		}
		m.Interval = d
	}
	return nil
}

// resolveInterval returns the interval a module should be polled at.
//
// pollInterval is a floor: the loop cannot tick faster than its own interval,
// so a module cannot be polled more often than that however it is configured.
// A default slower than pollInterval is honoured; a default faster than it is
// raised, which is what happens when someone sets a very long pollInterval.
func resolveInterval(module string, explicit, pollInterval time.Duration) time.Duration {
	d := explicit
	if d == 0 {
		d = defaultModuleInterval[module]
	}
	if d < pollInterval {
		return pollInterval
	}
	return d
}
