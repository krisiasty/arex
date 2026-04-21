package collector

import (
	"encoding/json"
	"fmt"
	"log"
	"sync"
	"time"

	"github.com/yourusername/arex/config"
	"github.com/yourusername/arex/internal/eapi"
)

// SwitchData holds the latest collected data for one switch.
type SwitchData struct {
	mu          sync.RWMutex
	Label       string
	LastSuccess time.Time
	ScrapeErr   error

	Version     eapi.ShowVersion
	ProcessTop  eapi.ShowProcessesTop
	EnvTemp     eapi.ShowEnvironmentTemp
	EnvPower    eapi.ShowEnvironmentPower
	EnvCooling  eapi.ShowEnvironmentCooling
	Interfaces  eapi.ShowInterfaces
	BGPSummary  eapi.ShowBGPSummary
}

// RLock / RUnlock expose the read lock for callers rendering metrics.
func (d *SwitchData) RLock()   { d.mu.RLock() }
func (d *SwitchData) RUnlock() { d.mu.RUnlock() }

// Store holds SwitchData for all configured switches.
// The map is populated once at startup and never modified afterward,
// so the map itself needs no mutex — only the per-switch data does.
type Store struct {
	switches map[string]*SwitchData // keyed by switch label
}

// NewStore initialises an empty Store for the given switch configs.
func NewStore(switches []config.SwitchConfig) *Store {
	s := &Store{
		switches: make(map[string]*SwitchData, len(switches)),
	}
	for _, sw := range switches {
		s.switches[sw.Label()] = &SwitchData{Label: sw.Label()}
	}
	return s
}

// All returns all SwitchData entries. The returned slice is safe to iterate;
// callers must hold each entry's read lock when accessing fields.
func (s *Store) All() []*SwitchData {
	out := make([]*SwitchData, 0, len(s.switches))
	for _, v := range s.switches {
		out = append(out, v)
	}
	return out
}

// commands is the ordered list of EOS CLI commands arex issues per poll.
var commands = []string{
	"show version",
	"show processes top once",
	"show system environment temperature",
	"show system environment power",
	"show system environment cooling",
	"show interfaces",
	"show ip bgp summary",
}

// indices into the results slice — must match commands order above.
const (
	idxVersion = iota
	idxProcessTop
	idxEnvTemp
	idxEnvPower
	idxEnvCooling
	idxInterfaces
	idxBGP
)

// Collect performs a single poll of the switch and updates the store.
func Collect(client *eapi.Client, data *SwitchData) {
	results, err := client.Run(commands)
	if err != nil {
		setError(data, fmt.Errorf("eAPI call failed: %w", err))
		return
	}

	if len(results) != len(commands) {
		setError(data, fmt.Errorf("expected %d results, got %d", len(commands), len(results)))
		return
	}

	// Parse all results before taking the lock so we hold it as briefly as possible.
	var (
		version    eapi.ShowVersion
		processTop eapi.ShowProcessesTop
		envTemp    eapi.ShowEnvironmentTemp
		envPower   eapi.ShowEnvironmentPower
		envCooling eapi.ShowEnvironmentCooling
		interfaces eapi.ShowInterfaces
		bgp        eapi.ShowBGPSummary
	)

	parsers := []struct {
		idx  int
		dst  interface{}
		name string
	}{
		{idxVersion, &version, "show version"},
		{idxProcessTop, &processTop, "show processes top once"},
		{idxEnvTemp, &envTemp, "show system environment temperature"},
		{idxEnvPower, &envPower, "show system environment power"},
		{idxEnvCooling, &envCooling, "show system environment cooling"},
		{idxInterfaces, &interfaces, "show interfaces"},
		{idxBGP, &bgp, "show ip bgp summary"},
	}

	for _, p := range parsers {
		if err := json.Unmarshal(results[p.idx], p.dst); err != nil {
			setError(data, fmt.Errorf("parse %s: %w", p.name, err))
			return
		}
	}

	// All parsing succeeded — update the store under lock.
	data.mu.Lock()
	defer data.mu.Unlock()

	data.Version     = version
	data.ProcessTop  = processTop
	data.EnvTemp     = envTemp
	data.EnvPower    = envPower
	data.EnvCooling  = envCooling
	data.Interfaces  = interfaces
	data.BGPSummary  = bgp
	data.ScrapeErr   = nil
	data.LastSuccess = time.Now()
}

// PollLoop runs Collect on every tick until the process exits.
// It collects once immediately on startup before waiting for the first tick.
func PollLoop(client *eapi.Client, data *SwitchData, interval time.Duration) {
	log.Printf("[%s] starting poller (interval: %s)", data.Label, interval)
	Collect(client, data)

	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for range ticker.C {
		Collect(client, data)
	}
}

func setError(data *SwitchData, err error) {
	log.Printf("[%s] collection error: %v", data.Label, err)
	data.mu.Lock()
	defer data.mu.Unlock()
	data.ScrapeErr = err
}
