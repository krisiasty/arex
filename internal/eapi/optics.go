package eapi

import (
	"encoding/json"
	"strings"
)

// TrimmedString is a JSON string with surrounding whitespace removed.
// EOS pads some vendor fields and not others: the same optic reports
// vendorSn as "XXX000a`0004    " from "show interfaces phy detail" and
// "XXX000a`0004" from "show interfaces transceiver detail". Untrimmed,
// those become two distinct label values for one physical part.
type TrimmedString string

func (s *TrimmedString) UnmarshalJSON(b []byte) error {
	var raw string
	if err := json.Unmarshal(b, &raw); err != nil {
		return err
	}
	*s = TrimmedString(strings.TrimSpace(raw))
	return nil
}

// ShowTransceiverDetail maps "show interfaces transceiver detail".
//
// Interfaces with no optic installed are absent from the map entirely.
type ShowTransceiverDetail struct {
	Interfaces map[string]Transceiver `json:"interfaces"`
}

// Transceiver holds DOM readings and per-optic thresholds for one interface.
//
// On a broken-out cage every subinterface reports the same Slot and the same
// module-level Temperature and Voltage; TxBias, TxPower and RxPower are
// per-lane and differ. Channel identifies the lane within the cage.
type Transceiver struct {
	Slot       string        `json:"slot"`
	Channel    string        `json:"channel"`
	VendorSn   TrimmedString `json:"vendorSn"`
	MediaType  string        `json:"mediaType"`
	UpdateTime float64       `json:"updateTime"`

	Temperature float64 `json:"temperature"` // Celsius, module-level
	Voltage     float64 `json:"voltage"`     // Volts, module-level
	TxBias      float64 `json:"txBias"`      // mA, per lane
	TxPower     float64 `json:"txPower"`     // dBm, per lane
	RxPower     float64 `json:"rxPower"`     // dBm, per lane

	// Details is keyed by parameter name: temperature, voltage, txBias,
	// txPower, rxPower, totalRxPower.
	Details map[string]DomThresholds `json:"details"`
}

// DomThresholds holds an optic's own alarm and warning limits for one
// parameter.
//
// Pointers, not plain float64: EOS reports totalRxPower with the
// *Overridden flags but no limits at all, while txBias legitimately has a
// low alarm of 0.0. Absent and zero must stay distinguishable, or arex
// would publish a fabricated limit and alerts would compare against it.
type DomThresholds struct {
	HighAlarm *float64 `json:"highAlarm"`
	HighWarn  *float64 `json:"highWarn"`
	LowAlarm  *float64 `json:"lowAlarm"`
	LowWarn   *float64 `json:"lowWarn"`
}

// ShowPhyDetail maps "show interfaces phy detail".
type ShowPhyDetail struct {
	Interfaces map[string]InterfacePhy `json:"interfacePhyStatuses"`
}

// InterfacePhy is the PHY view of one interface.
type InterfacePhy struct {
	PhyStatuses    []PhyStatus        `json:"phyStatuses"`
	InterfaceState PhyInterfaceState  `json:"interfaceState"`
	Transceiver    PhyTransceiverInfo `json:"transceiver"`
	MacFaults      PhyMacFaults       `json:"macFaults"`
}

// PhyStatus is one PHY within an interface. There can be more than one
// (line side and system side), distinguished by Description.Location.
//
// The serdes block is deliberately not parsed. It is roughly half the
// payload, its eye measurements are meaningless while the link is down
// (Ethernet1/2 reported eyeRight 50059 against 296 on a live link), and it
// drifts on every poll. It is tuning telemetry, not an alerting signal.
type PhyStatus struct {
	Description    PhyDescription `json:"description"`
	Chip           PhyChip        `json:"chip"`
	PCS            PhyPCS         `json:"pcs"`
	FEC            *PhyFEC        `json:"fec"`
	PMA            PhyPMA         `json:"pma"`
	PhyState       PhyStringAttr  `json:"phyState"`
	OperSpeed      string         `json:"operSpeed"`
	InterruptCount uint64         `json:"interruptCount"`
}

// PhyDescription names the PHY chip and which side of the port it serves.
type PhyDescription struct {
	PhyChipName string `json:"phyChipName"`
	Location    string `json:"location"`
}

// PhyChip identifies the PHY hardware. oui/model/rev are omitted: they read
// 0 on every sampled interface.
type PhyChip struct {
	ModelName   string `json:"modelName"`
	HwRev       string `json:"hwRev"`
	FirmwareRev string `json:"firmwareRev"`
}

// PhyPCS is the physical coding sublayer.
//
// BlockLock is a pointer because it only exists on links without FEC. With
// RS-FEC running, FEC alignment lock replaces it, so a link has exactly one
// of PCS.BlockLock and FEC.AlignmentLock.
type PhyPCS struct {
	LinkStatus            PhyStringAttr  `json:"linkStatus"`
	BlockLock             *PhyBoolAttr   `json:"blockLock"`
	HighBer               PhyBoolAttr    `json:"highBer"`
	LastHighBerCount      PhyCounterAttr `json:"lastHighBerCount"`
	LastErroredBlockCount PhyCounterAttr `json:"lastErroredBlockCount"`
}

// PhyFEC is forward error correction state. Absent on links that run no FEC
// (10G here); present from 25G upward.
type PhyFEC struct {
	Encoding     string `json:"encoding"`
	CodewordSize string `json:"codewordSize"`

	AlignmentLock        PhyBoolAttr    `json:"alignmentLock"`
	CorrectedCodewords   PhyCounterAttr `json:"correctedCodewords"`
	UncorrectedCodewords PhyCounterAttr `json:"uncorrectedCodewords"`

	// CorrectedSymbols is per-lane and only populated at native speeds; it is
	// empty on breakout lanes. Note the shape difference against LaneMap,
	// which is a flat int map despite being a sibling field.
	CorrectedSymbols map[string]PhyCounterAttr `json:"correctedSymbols"`
	LaneMap          map[string]int            `json:"laneMap"`
}

// PhyPMA is the physical medium attachment sublayer.
type PhyPMA struct {
	LinkStatus       PhyStringAttr            `json:"linkStatus"`
	SignalDetect     PhyBoolAttr              `json:"signalDetect"`
	LaneSignalDetect map[string]PhyBoolAttr   `json:"laneSignalDetect"`
	LaneLinkStatus   map[string]PhyStringAttr `json:"laneLinkStatus"`
}

// PhyInterfaceState is the interface-level state. It spells its value
// "current" where every other attribute uses "value".
type PhyInterfaceState struct {
	Current    string  `json:"current"`
	Changes    uint64  `json:"changes"`
	LastChange float64 `json:"lastChange"`
}

// PhyTransceiverInfo is the optic as reported by the PHY command.
type PhyTransceiverInfo struct {
	MediaType PhyStringAttr `json:"mediaType"`
	VendorSn  TrimmedString `json:"vendorSn"`
}

// PhyMacFaults reports MAC-layer fault signalling.
type PhyMacFaults struct {
	LocalFault  PhyBoolAttr `json:"localFault"`
	RemoteFault PhyBoolAttr `json:"remoteFault"`
}

// The PHY command wraps nearly every attribute as {value, changes,
// lastChange}. Changes counts transitions since boot and only ever rises,
// so it is the one field here that is reliably counter-shaped.

// PhyStringAttr is a string-valued PHY attribute.
type PhyStringAttr struct {
	Value      string  `json:"value"`
	Changes    uint64  `json:"changes"`
	LastChange float64 `json:"lastChange"`
}

// PhyBoolAttr is a boolean-valued PHY attribute.
type PhyBoolAttr struct {
	Value      bool    `json:"value"`
	Changes    uint64  `json:"changes"`
	LastChange float64 `json:"lastChange"`
}

// PhyCounterAttr is a numeric PHY attribute.
//
// Value is exposed as a gauge, not a counter. Its semantics are genuinely
// ambiguous: Ethernet4/1 reported uncorrectedCodewords value 3 with changes
// 13, which fits neither a monotonic total nor a per-interval snapshot, and
// the counters are manually clearable. Typing it as a counter would make
// rate() produce confident nonsense if it turns out to be a last-observed
// count; typing a cumulative counter as a gauge only forgoes convenience.
type PhyCounterAttr struct {
	Value      float64 `json:"value"`
	Changes    uint64  `json:"changes"`
	LastChange float64 `json:"lastChange"`
}
