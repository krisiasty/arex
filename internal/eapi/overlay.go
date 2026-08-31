package eapi

// ShowVXLANVTEP maps the output of "show vxlan vtep".
//
// Interfaces is the authoritative association between a VXLAN interface and
// its remote VTEPs. VTEPTunnelTypes adds how each VTEP is used: unicast,
// flood, or both.
type ShowVXLANVTEP struct {
	Interfaces      map[string]VXLANVTEPInterface `json:"interfaces"`
	VTEPTunnelTypes map[string]VTEPTunnelTypes    `json:"vtepTunnelTypes"`
}

// VXLANVTEPInterface is one VXLAN interface's known remote VTEPs.
type VXLANVTEPInterface struct {
	VTEPs []string `json:"vteps"`
}

// VTEPTunnelTypes describes how one remote VTEP is used.
type VTEPTunnelTypes struct {
	TunnelTypes []VTEPTunnelType `json:"tunnelTypes"`
}

// VTEPTunnelType is one use of a remote VTEP, such as flood or unicast.
type VTEPTunnelType struct {
	TunnelType string `json:"tunnelType"`
}

// ShowInterfaceVXLAN maps the output of "show interfaces vxlan 1".
type ShowInterfaceVXLAN struct {
	Interfaces map[string]VXLANInterface `json:"interfaces"`
}

// VXLANInterface is the operational and mapping state of a VXLAN interface.
type VXLANInterface struct {
	Name               string `json:"name"`
	ForwardingModel    string `json:"forwardingModel"`
	LineProtocolStatus string `json:"lineProtocolStatus"`
	InterfaceStatus    string `json:"interfaceStatus"`
	SourceInterface    string `json:"srcIpIntf"`
	SourceAddress      string `json:"srcIpAddr"`
	UDPPort            int    `json:"udpPort"`
	ReplicationMode    string `json:"replicationMode"`
	Encapsulation      string `json:"vxlanEncapsulation"`

	VLANToVNIMap  map[string]VXLANVNI         `json:"vlanToVniMap"`
	VLANToVTEPMap map[string]VXLANRemoteVTEPs `json:"vlanToVtepList"`
	VRFToVNIMap   map[string]uint32           `json:"vrfToVniMap"`
}

// VXLANVNI is one VLAN-to-VNI mapping.
type VXLANVNI struct {
	VNI    uint32 `json:"vni"`
	Source string `json:"source"`
}

// VXLANRemoteVTEPs is the flood list for one VLAN.
type VXLANRemoteVTEPs struct {
	IPv4 []string `json:"remoteVtepAddr"`
	IPv6 []string `json:"remoteVtepAddr6"`
}

// ShowVXLANAddressTableCount maps "show vxlan address-table count".
type ShowVXLANAddressTableCount struct {
	VTEPCounts map[string]uint64 `json:"vtepCounts"`
}

// ShowBGPEVPNSummary maps "show bgp evpn summary". Its VRF and peer shapes
// match the IPv4 BGP summary closely enough to share BGPVrf and BGPPeer.
type ShowBGPEVPNSummary struct {
	Vrfs map[string]BGPVrf `json:"vrfs"`
}

// ShowBGPEVPNRouteTypeCount maps "show bgp evpn route-type count".
//
// EOS reports path entries here. A route learned from two peers can therefore
// count twice, unlike the unique-NLRI cardinality returned by a filtered
// per-route-type count command.
type ShowBGPEVPNRouteTypeCount struct {
	AutoDiscovery   uint64 `json:"autoDiscovery"`
	MACIP           uint64 `json:"macIp"`
	IMET            uint64 `json:"imet"`
	EthernetSegment uint64 `json:"ethernetSegment"`
	IPPrefixIPv4    uint64 `json:"ipPrefixIpv4"`
	IPPrefixIPv6    uint64 `json:"ipPrefixIpv6"`
}

// ShowBGPEVPNInstance maps "show bgp evpn instance".
type ShowBGPEVPNInstance struct {
	Instances map[string]BGPEVPNInstance `json:"bgpEvpnInstances"`
}

// BGPEVPNInstance is one VLAN-aware bundle and its Ethernet segments.
type BGPEVPNInstance struct {
	RD               string                     `json:"rd"`
	ImportRTs        []uint64                   `json:"importRts"`
	ExportRTs        []uint64                   `json:"exportRts"`
	ServiceInterface string                     `json:"serviceIntf"`
	LocalVTEP        string                     `json:"localVxlanIp"`
	VXLANEnabled     bool                       `json:"vxlanEnabled"`
	MPLSEnabled      bool                       `json:"mplsEnabled"`
	EthernetSegments map[string]EthernetSegment `json:"ethernetSegments"`
}

// EthernetSegment is one ESI's state and designated-forwarder election.
//
// DFElectionAlgorithm and DFElectionState are mutually exclusive: EOS reports
// the algorithm once an election has settled and the state while it has not.
// Across 118 segment entries captured from a pair of leaves, every one carried
// exactly one of the two.
type EthernetSegment struct {
	Interface      string `json:"intf"`
	RedundancyMode string `json:"redundancyMode"`
	State          string `json:"state"`

	DFElectionAlgorithm string `json:"dFElectionAlgorithm"`

	// DFElectionState is why there is no forwarder -- "pending" on a segment
	// whose local link is down. Present only when DFElectionAlgorithm is not.
	DFElectionState string `json:"dFElectionState"`

	// DFPeer carries an empty IP when no forwarder is elected, rather than
	// being absent or null.
	DFPeer          EVPNPeerIP   `json:"dFPeer"`
	NonDFPeers      []EVPNPeerIP `json:"nonDFPeers"`
	ForwardingPeers []EVPNPeerIP `json:"forwardingPeers"`
}

// Up reports whether this switch's own link into the segment is up.
//
// It is not a fabric-wide verdict: when one leaf of a multihomed pair loses its
// link, the survivor still reports the segment up, correctly, because its own
// link is fine. ForwardingPeers is what shows the segment lost a path.
func (s EthernetSegment) Up() bool { return s.State == "up" }

// HasDesignatedForwarder reports whether an election has produced a forwarder.
// Checked explicitly rather than relying on the empty IP failing to match a
// local VTEP address, which would be right by accident.
func (s EthernetSegment) HasDesignatedForwarder() bool { return s.DFPeer.IP != "" }

// DesignatedForwarder reports whether localVTEP is the elected forwarder for
// this segment. DF election runs per VLAN-aware bundle, so the same ESI can
// have a different forwarder in each instance it belongs to -- that is the
// modulus algorithm spreading the role, not a fault.
func (s EthernetSegment) DesignatedForwarder(localVTEP string) bool {
	return s.HasDesignatedForwarder() && s.DFPeer.IP == localVTEP
}

// EVPNPeerIP is the common peer object used by ESI election lists.
type EVPNPeerIP struct {
	IP string `json:"ip"`
}
