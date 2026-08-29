package eapi

import (
	"maps"
	"slices"
)

// ShowVersion maps the output of "show version".
type ShowVersion struct {
	MfgName          string  `json:"mfgName"`
	ModelName        string  `json:"modelName"`
	HardwareRevision string  `json:"hardwareRevision"`
	SerialNumber     string  `json:"serialNumber"`
	SystemMacAddress string  `json:"systemMacAddress"`
	Version          string  `json:"version"`
	Architecture     string  `json:"architecture"`
	BootupTimestamp  float64 `json:"bootupTimestamp"`
	Uptime           float64 `json:"uptime"`
	MemTotal         uint64  `json:"memTotal"` // kilobytes
	MemFree          uint64  `json:"memFree"`  // kilobytes
}

// ShowProcessesTop maps the output of "show processes top once".
type ShowProcessesTop struct {
	TimeInfo struct {
		CurrentTime float64   `json:"currentTime"`
		UpTime      float64   `json:"upTime"`
		LoadAvg     []float64 `json:"loadAvg"` // [1min, 5min, 15min]
	} `json:"timeInfo"`
	CPUInfo struct {
		CPU struct {
			User   float64 `json:"user"`
			System float64 `json:"system"`
			Nice   float64 `json:"nice"`
			Idle   float64 `json:"idle"`
			IoWait float64 `json:"ioWait"`
			HwIrq  float64 `json:"hwIrq"`
			SwIrq  float64 `json:"swIrq"`
			Stolen float64 `json:"stolen"`
		} `json:"%Cpu(s)"`
	} `json:"cpuInfo"`
	MemInfo struct {
		Physical struct {
			Total  uint64 `json:"memTotal"` // kilobytes
			Used   uint64 `json:"memUsed"`
			Free   uint64 `json:"memFree"`
			Buffer uint64 `json:"memBuffer"`
		} `json:"physicalMem"`
	} `json:"memInfo"`
}

// ShowEnvironmentTemp maps the output of "show system environment temperature".
type ShowEnvironmentTemp struct {
	SystemStatus       string       `json:"systemStatus"`
	ShutdownOnOverheat bool         `json:"shutdownOnOverheat"`
	TempSensors        []TempSensor `json:"tempSensors"`
	PowerSupplySlots   []struct {
		RelPos      string       `json:"relPos"`
		TempSensors []TempSensor `json:"tempSensors"`
	} `json:"powerSupplySlots"`
}

// TempSensor represents a single temperature sensor.
type TempSensor struct {
	Name               string  `json:"name"`
	Description        string  `json:"description"`
	HwStatus           string  `json:"hwStatus"`
	CurrentTemperature float64 `json:"currentTemperature"`
	MaxTemperature     float64 `json:"maxTemperature"`
	OverheatThreshold  float64 `json:"overheatThreshold"`
	CriticalThreshold  float64 `json:"criticalThreshold"`
	InAlertState       bool    `json:"inAlertState"`
	AlertCount         int     `json:"alertCount"`
	Position           string  `json:"position"`
}

// ShowEnvironmentPower maps the output of "show system environment power".
type ShowEnvironmentPower struct {
	PowerSupplies map[string]PowerSupply `json:"powerSupplies"`
}

// PowerSupply represents a single PSU.
type PowerSupply struct {
	ModelName     string                   `json:"modelName"`
	Capacity      float64                  `json:"capacity"`
	Dominant      bool                     `json:"dominant"`
	InputCurrent  float64                  `json:"inputCurrent"`
	OutputCurrent float64                  `json:"outputCurrent"`
	InputVoltage  float64                  `json:"inputVoltage"`
	OutputVoltage float64                  `json:"outputVoltage"`
	OutputPower   float64                  `json:"outputPower"`
	State         string                   `json:"state"`
	Uptime        float64                  `json:"uptime"`
	Managed       bool                     `json:"managed"`
	Fans          map[string]PSUFan        `json:"fans"`
	TempSensors   map[string]PSUTempSensor `json:"tempSensors"`
}

// PSUFan is a fan embedded in a PSU (from show environment power).
type PSUFan struct {
	Status string `json:"status"`
	Speed  int    `json:"speed"`
}

// PSUTempSensor is a temp sensor embedded in a PSU (from show environment power).
type PSUTempSensor struct {
	Status      string  `json:"status"`
	Temperature float64 `json:"temperature"`
}

// ShowEnvironmentCooling maps the output of "show system environment cooling".
type ShowEnvironmentCooling struct {
	SystemStatus               string        `json:"systemStatus"`
	FansStatus                 string        `json:"fansStatus"`
	AmbientTemperature         float64       `json:"ambientTemperature"`
	AirflowDirection           string        `json:"airflowDirection"`
	CoolingMode                string        `json:"coolingMode"`
	ShutdownOnInsufficientFans bool          `json:"shutdownOnInsufficientFans"`
	FanTraySlots               []FanTraySlot `json:"fanTraySlots"`
	PowerSupplySlots           []FanTraySlot `json:"powerSupplySlots"`
}

// FanTraySlot represents a fan tray or PSU slot in the cooling output.
type FanTraySlot struct {
	Label  string `json:"label"`
	Status string `json:"status"`
	Speed  int    `json:"speed"`
	Fans   []Fan  `json:"fans"`
}

// Fan represents a single fan within a tray or PSU.
type Fan struct {
	Label                     string  `json:"label"`
	MaxSpeed                  int     `json:"maxSpeed"`
	ConfiguredSpeed           int     `json:"configuredSpeed"`
	ActualSpeed               int     `json:"actualSpeed"`
	Status                    string  `json:"status"`
	Uptime                    float64 `json:"uptime"`
	SpeedStable               bool    `json:"speedStable"`
	SpeedHwOverride           bool    `json:"speedHwOverride"`
	VendorModel               string  `json:"vendorModel"`
	LastSpeedStableChangeTime float64 `json:"lastSpeedStableChangeTime"`
}

// ShowInterfaces maps the output of "show interfaces".
type ShowInterfaces struct {
	Interfaces map[string]Interface `json:"interfaces"`
}

// Interface represents a single interface entry.
type Interface struct {
	Name                      string            `json:"name"`
	LineProtocolStatus        string            `json:"lineProtocolStatus"`
	InterfaceStatus           string            `json:"interfaceStatus"`
	Description               string            `json:"description"`
	InterfaceMembership       string            `json:"interfaceMembership"`
	Bandwidth                 uint64            `json:"bandwidth"` // bits per second
	MTU                       int               `json:"mtu"`
	LastStatusChangeTimestamp float64           `json:"lastStatusChangeTimestamp"`
	InterfaceCounters         InterfaceCounters `json:"interfaceCounters"`
}

// InterfaceCounters holds the counter fields for an interface.
type InterfaceCounters struct {
	InOctets  uint64 `json:"inOctets"`
	OutOctets uint64 `json:"outOctets"`

	InDiscards  uint64 `json:"inDiscards"`
	OutDiscards uint64 `json:"outDiscards"`

	InputErrors  uint64 `json:"totalInErrors"`
	OutputErrors uint64 `json:"totalOutErrors"`

	InUcastPkts     uint64 `json:"inUcastPkts"`
	InMulticastPkts uint64 `json:"inMulticastPkts"`
	InBroadcastPkts uint64 `json:"inBroadcastPkts"`

	OutUcastPkts     uint64 `json:"outUcastPkts"`
	OutMulticastPkts uint64 `json:"outMulticastPkts"`
	OutBroadcastPkts uint64 `json:"outBroadcastPkts"`

	LinkStatusChanges  uint64  `json:"linkStatusChanges"`
	LastClear          float64 `json:"lastClear"`
	CounterRefreshTime float64 `json:"counterRefreshTime"`

	InputErrorsDetail  InputErrorsDetail  `json:"inputErrorsDetail"`
	OutputErrorsDetail OutputErrorsDetail `json:"outputErrorsDetail"`
}

// InputErrorsDetail breaks down inbound errors by cause.
type InputErrorsDetail struct {
	RuntFrames      uint64 `json:"runtFrames"`
	GiantFrames     uint64 `json:"giantFrames"`
	FcsErrors       uint64 `json:"fcsErrors"`
	AlignmentErrors uint64 `json:"alignmentErrors"`
	SymbolErrors    uint64 `json:"symbolErrors"`
	RxPause         uint64 `json:"rxPause"`
}

// OutputErrorsDetail breaks down outbound errors by cause.
type OutputErrorsDetail struct {
	Collisions            uint64 `json:"collisions"`
	LateCollisions        uint64 `json:"lateCollisions"`
	DeferredTransmissions uint64 `json:"deferredTransmissions"`
	TxPause               uint64 `json:"txPause"`
}

// ShowBGPSummary maps the output of "show ip bgp summary vrf all".
type ShowBGPSummary struct {
	Vrfs map[string]BGPVrf `json:"vrfs"`
}

// BGPVrf represents BGP state within a single VRF.
type BGPVrf struct {
	RouterID string             `json:"routerId"`
	Asn      string             `json:"asn"`
	Peers    map[string]BGPPeer `json:"peers"`
}

// BGPPeer represents a single BGP neighbor.
//
// Asn is a string: EOS quotes it, and 4-byte private ASNs exceed int32.
type BGPPeer struct {
	PeerState        string  `json:"peerState"`
	Description      string  `json:"description"`
	Asn              string  `json:"asn"`
	PrefixReceived   int     `json:"prefixReceived"`
	PrefixAccepted   int     `json:"prefixAccepted"`
	PrefixAdvertised int     `json:"prefixAdvertised"`
	UnderMaintenance bool    `json:"underMaintenance"`
	UpDownTime       float64 `json:"upDownTime"`
}

// ShowNTPAssociations maps the output of "show ntp associations".
//
// Peers is keyed by server address. It tracks ntpd's associations rather than
// the running configuration, so a configured server is not guaranteed to have
// an entry — which is why synchronisation is judged by the presence of a
// selected peer and not by counting entries here.
type ShowNTPAssociations struct {
	Peers map[string]NTPPeer `json:"peers"`
}

// NTPPeer is one NTP association.
//
// Delay, Offset and Jitter are milliseconds, as ntpq reports them. They are
// only meaningful for a peer that has actually answered: a server that has
// never responded reports all three as zero, which reads exactly like a
// perfectly disciplined clock.
type NTPPeer struct {
	// Condition is ntpd's tally code: "sys.peer" for the source the clock is
	// being steered to, "candidate", "reject" and so on for the rest.
	Condition    string `json:"condition"`
	PeerIPAddr   string `json:"peerIpAddr"`
	RefID        string `json:"refid"`
	StratumLevel int    `json:"stratumLevel"` // 16 means the peer is itself unsynchronised
	PeerType     string `json:"peerType"`

	// LastReceived is -2208988800 -- the NTP epoch of 1900-01-01, in Unix
	// seconds -- for a peer that has never answered.
	LastReceived float64 `json:"lastReceived"`
	PollInterval int     `json:"pollInterval"` // seconds

	// ReachabilityHistory is ntpd's reach register as eight booleans, one per
	// recent poll. Whether index 0 is the oldest or the newest is not
	// something EOS documents, so only the count is used.
	ReachabilityHistory []bool `json:"reachabilityHistory"`

	Delay  float64 `json:"delay"`
	Offset float64 `json:"offset"`
	Jitter float64 `json:"jitter"`
}

// Selected reports whether the clock is being steered to this peer.
func (p NTPPeer) Selected() bool { return p.Condition == "sys.peer" }

// ReachableSamples counts the successful polls in the reachability register.
func (p NTPPeer) ReachableSamples() int {
	n := 0
	for _, ok := range p.ReachabilityHistory {
		if ok {
			n++
		}
	}
	return n
}

// SyncSource returns the peer the clock is being steered to, if any. Its
// absence is what "unsynchronised" means: ntpd elects exactly one sys.peer,
// and reports none while it has no source it trusts.
func (n ShowNTPAssociations) SyncSource() (NTPPeer, bool) {
	for _, addr := range slices.Sorted(maps.Keys(n.Peers)) {
		if p := n.Peers[addr]; p.Selected() {
			return p, true
		}
	}
	return NTPPeer{}, false
}

// ShowHardwareCapacity maps the output of "show hardware capacity".
//
// Tables arrives in no particular order -- captures from three switches each
// began with a different row -- so nothing may depend on its ordering.
type ShowHardwareCapacity struct {
	Tables []HardwareCapacity `json:"tables"`
}

// HardwareCapacity is one row of the hardware capacity report.
//
// Table alone does not identify a row: a table appears once per feature, and
// some appear both against a chip and with none. Table, Feature and Chip
// together are unique.
//
// A row with an empty Feature is usually the table's total, but not reliably:
// NextHop reports one that is short of its features' sum, and the two Ecmp
// tables use it for a separate resource with its own MaxLimit. Nothing here is
// derived from anything else for that reason.
type HardwareCapacity struct {
	Table   string `json:"table"`
	Feature string `json:"feature"`
	Chip    string `json:"chip"`

	Used int `json:"used"`

	// Free is what remains in the pool the whole table shares, not in this
	// row. Host/V6Hosts reports Used 0 against a MaxLimit of 147455 with only
	// 147208 free, the difference having gone to V4Hosts -- so MaxLimit - Used
	// overstates the headroom and Free is the number to believe.
	Free int `json:"free"`

	MaxLimit int `json:"maxLimit"`

	// HighWatermark is the peak since boot. It is what makes a slow poll
	// interval safe: a spike between two polls is still visible here.
	HighWatermark int `json:"highWatermark"`

	// UsedPercent is EOS's own utilisation, truncated to a whole number, so
	// anything below one percent reports zero. Parsed but not exported --
	// Used/MaxLimit says the same thing without throwing the resolution away.
	UsedPercent int `json:"usedPercent"`

	// Committed is zero in every row of every capture so far, and what it
	// counts is unclear, so it is parsed but not exported.
	Committed int `json:"committed"`

	// SharedFeatures names what is consuming this row. The assignment differs
	// between switches -- one leaf's VXLAN slice is another's EVPN slice -- so
	// it is a per-switch label rather than something documentable.
	SharedFeatures []string `json:"sharedFeatures"`
}
