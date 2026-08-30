package collector

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/krisiasty/arex/config"
)

// rawRunner returns the same payload for every command, whatever it is.
type rawRunner struct{ body []byte }

func (r rawRunner) Run(cmds []string) ([]json.RawMessage, error) {
	out := make([]json.RawMessage, 0, len(cmds))
	for range cmds {
		out = append(out, json.RawMessage(r.body))
	}
	return out, nil
}

// Everything a switch returns is decoded on a poll goroutine, which is the one
// path in arex where a panic is fatal: the HTTP handlers are isolated by
// net/http, but a poller taking the process down stops collection for every
// switch.
//
// EOS output varies by platform and release, so "our code does not panic" has
// to hold for shapes no fixture contains. This drives the whole decode path --
// every command's unmarshal and every apply function -- with arbitrary bytes.
func FuzzCollect(f *testing.F) {
	f.Add([]byte(`{}`))
	f.Add([]byte(`null`))
	f.Add([]byte(`[]`))
	f.Add([]byte(`{"interfaces":null}`))
	f.Add([]byte(`{"interfaces":{"Ethernet1":{"interfaceCounters":null}}}`))
	f.Add([]byte(`{"vrfs":{"default":{"peers":null}}}`))
	f.Add([]byte(`{"timeInfo":{"loadAvg":[]}}`))
	f.Add([]byte(`{"timeInfo":{"loadAvg":[1]}}`))
	f.Add([]byte(`{"interfacePhyStatuses":{"Ethernet1":{"phyStatuses":[]}}}`))
	f.Add([]byte(`{"interfaces":{"Ethernet1":{"transceiver":{"vendorSn":123}}}}`))
	f.Add([]byte(`{"powerSupplies":{"1":{"outputPower":"not a number"}}}`))
	f.Add([]byte(`{"modelName":null,"memTotal":"x"}`))
	f.Add([]byte("\xff\xfe"))

	collect := make(config.CollectSet, len(config.CollectKeys))
	for _, k := range config.CollectKeys {
		collect[k] = config.ModuleConfig{Enabled: true, Interval: 30 * time.Second}
	}

	f.Fuzz(func(_ *testing.T, body []byte) {
		data := newSwitchDataFromSpecs("sw1", "", commandsFor(collect, "", 30*time.Second))
		// A decode error is the expected outcome for almost every input; it is
		// recorded per command and is not a failure here. Only a panic is.
		Collect(rawRunner{body}, data)
	})
}
