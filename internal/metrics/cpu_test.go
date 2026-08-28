package metrics

import (
	"encoding/json"
	"math"
	"strconv"
	"testing"
	"time"

	"github.com/krisiasty/arex/config"
	"github.com/krisiasty/arex/internal/collector"
)

// overrideRunner serves the fixtures, with one command's payload replaced.
type overrideRunner struct {
	fixtureRunner
	cmd  string
	body string
}

func (o overrideRunner) Run(cmds []string) ([]json.RawMessage, error) {
	out, err := o.fixtureRunner.Run(cmds)
	if err != nil {
		return nil, err
	}
	for i, c := range cmds {
		if c == o.cmd {
			out[i] = json.RawMessage(o.body)
		}
	}
	return out, nil
}

// EOS splits CPU time eight ways. Exporting six of them means the components
// do not add up, so anyone reconstructing utilisation by summing them
// undercounts by however much the switch spent servicing interrupts.
func TestCPUComponentsSumToOneHundred(t *testing.T) {
	const topSample = `{
	  "timeInfo": {"currentTime": 1787914906.9, "upTime": 10550103.5, "loadAvg": [1.04, 1.08, 1.02]},
	  "cpuInfo": {"%Cpu(s)": {
	    "user": 11.1, "system": 3.0, "nice": 0.4, "idle": 83.2,
	    "ioWait": 0.2, "hwIrq": 0.4, "swIrq": 0.7, "stolen": 1.0
	  }},
	  "memInfo": {"physicalMem": {"memTotal": 16280678, "memUsed": 3472179,
	    "memFree": 7324160, "memBuffer": 6603571}}
	}`

	store, err := collector.NewStore([]config.SwitchConfig{
		{Host: "h", Username: "u", Password: "p", Name: "sw1"},
	}, config.CollectSet{"processes": {Enabled: true}}, 30*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	sd := store.All()[0]
	collector.Collect(overrideRunner{fixtureRunner{t}, "show processes top once", topSample}, sd)

	out := gather(t, store, 90*time.Second)

	total := 0.0
	for _, mode := range []string{"user", "system", "nice", "idle", "iowait", "irq", "softirq", "steal"} {
		got := sample(out, "arista_cpu_"+mode+"_percent", "")
		if got == "" {
			t.Errorf("arista_cpu_%s_percent is not exported", mode)
			continue
		}
		v, perr := strconv.ParseFloat(got, 64)
		if perr != nil {
			t.Fatalf("arista_cpu_%s_percent = %q: %v", mode, got, perr)
		}
		total += v
	}
	if math.Abs(total-100) > 0.001 {
		t.Errorf("CPU components sum to %.3f, want 100", total)
	}
}
