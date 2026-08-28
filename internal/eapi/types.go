package eapi

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
