package metrics

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/krisiasty/arex/config"
	"github.com/krisiasty/arex/internal/collector"
)

// rawRunner answers every command with the same payload.
type rawRunner struct{ body []byte }

func (r rawRunner) Run(cmds []string) ([]json.RawMessage, error) {
	out := make([]json.RawMessage, 0, len(cmds))
	for range cmds {
		out = append(out, json.RawMessage(r.body))
	}
	return out, nil
}

// Rendering turns switch-supplied strings into metric labels -- interface
// names, VRFs, optic serials, BGP peer addresses. Prometheus defines exactly
// three escapes in a label value, so a name containing a quote or a newline is
// a way to produce output no parser accepts.
//
// This drives the whole path: arbitrary JSON decoded into the store, then a
// real Gather and text encode. A panic fails; so does output the Prometheus
// text parser rejects, which is what gather checks.
func FuzzRender(f *testing.F) {
	f.Add([]byte(`{}`))
	f.Add([]byte(`{"interfaces":{"Ethernet1":{"description":"a \"quoted\" name"}}}`))
	f.Add([]byte(`{"interfaces":{"Ethernet\n1":{"name":"x"}}}`))
	f.Add([]byte(`{"interfaces":{"Ethernet\\1":{"name":"y"}}}`))
	f.Add([]byte(`{"vrfs":{"a\"b":{"peers":{"1.2.3.4":{"peerState":"Established"}}}}}`))
	f.Add([]byte(`{"interfaces":{"E1":{"transceiver":{"vendorSn":"a\tb"}}}}`))
	f.Add([]byte(`{"modelName":"a b"}`))
	f.Add([]byte(`{"interfacePhyStatuses":{"E\"1":{"phyStatuses":[{"phyState":"linkUp"}]}}}`))

	f.Fuzz(func(t *testing.T, body []byte) {
		store, err := collector.NewStore([]config.SwitchConfig{
			{Host: "h", Username: "u", Password: "p", Name: "sw1"},
		}, collectAll(), 30*time.Second)
		if err != nil {
			t.Fatal(err)
		}
		collector.Collect(rawRunner{body}, store.All()[0])
		// gather does a real Gather and parses the exposition back, so invalid
		// output fails rather than being silently produced.
		gather(t, store, 90*time.Second)
	})
}
