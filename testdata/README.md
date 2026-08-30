# Test fixtures

Real `show ... | json` output captured from an Arista DCS-7050CX3-32C-R running EOS 4.35.4M,
then anonymized. Field names, types, value magnitudes, float precision and structural quirks are
preserved exactly — those are what the tests assert on.

## Anonymization

Identifiers were replaced consistently across every file. Shapes were not changed.

| Real | Fixture |
| --- | --- |
| chassis serial | `ABC1234567X` |
| MAC addresses | `00:1c:73:00:00:0N` |
| management address | `192.0.2.33` |
| NTP servers | `192.0.2.36`, `192.0.2.37` |
| underlay peers | `198.51.100.10`, `198.51.100.11` |
| transit peers | `203.0.113.164`, `203.0.113.168` |
| transit ASN | `64500` |
| local/underlay ASN | `4200000000`, `4200000001` |
| peer descriptions | `transit-rtr-01`, `transit-rtr-02` |
| interface description | `host-a01 (planned) bond10` |
| non-default VRF names | `TENANT_PRIV`, `TENANT_PUBLIC` |
| optic serials | `XXX000AB0001A`, `XXX000ab0002`, `XXX000a` + backtick + `0003`, … |

Deliberately preserved because tests depend on them:

- `inOctets: 40456835181534` exceeds uint32, so uint64 handling stays exercised.
- ASNs stay above int32 max — they are JSON **strings** in EOS, and that is what broke the exporter.
- Optic serials keep a backtick, as the real EEPROM values do, and keep their original length and
  letter/digit positions — the `XXX` prefix marks them as placeholders rather than plausible vendor
  codes, and the trailing sequence number distinguishes the optics.
- `INTERNET` is a generic name and was left as it was. The two tenant VRFs have no peers, which is
  what the fixture exists to cover: a VRF present in `show ip bgp summary vrf all` with an empty
  `peers` object.
- `show_interfaces_phy_detail.json` keeps the trailing whitespace EOS pads `vendorSn` with; the
  same optic reports it unpadded in `show_interfaces_transceiver_detail.json`.
- `lowFrequncyPeakingFilter` keeps EOS's spelling of "Frequncy".
- `pidDriverStats` keeps `1.3618325534300776e-232`.

## The two NTP captures

`show ntp associations` is captured twice, because the interesting failures are only visible in the degraded one.

- `show_ntp_associations_synced.json` — one peer, `condition: sys.peer`, reach register full.
- `show_ntp_associations.json` — the same switch minutes later, having lost sync, plus a second configured server
  that has never answered. This is the primary fixture: the golden exposition is rendered from it.

Deliberately preserved in the degraded capture, because the tests exist for them:

- `lastReceived: -2208988800.0` on the peer that never answered. That is the NTP epoch, 1900-01-01, in Unix
  seconds — EOS's "never", not a time. Exported literally it is a timestamp 126 years old.
- `delay: 0.0` and `offset: 0.0` on that same peer. A server that has never exchanged a packet reports a perfect
  zero offset, which is indistinguishable from a flawlessly disciplined clock and is why the headline offset
  series is emitted only for the selected peer.
- `stratumLevel: 16` and `refid: ".INIT."`, NTP's sentinels for "unsynchronised" and "no packet has ever
  returned".
- `jitter: 0.000119` on a peer with no measurements at all, against `jitter: 0.0` on the one that has some. The
  values are not ordered the way intuition suggests.
- The reach register `[false × 7, true]`, a peer with exactly one recent success. Only the count is exported, so
  the test does not depend on whether index 0 is the oldest sample or the newest — which these captures do not
  settle.

The synchronised capture lists one peer while the degraded one lists two, so `peers` cannot be assumed to hold
every configured server.

## show hardware capacity

Nothing in this capture identifies anything, so it is stored exactly as the switch printed it. It comes from the
most loaded of three switches sampled -- real MAC, host and next-hop counts rather than a lab-idle table of zeros,
which would pass a test with the field names wrong and prove nothing.

Preserved because the tests turn on them:

- `MMU_MCAST/MmuReplHead` appears twice, once with `chip: "Linecard0/0"` and once with `chip: ""`, carrying
  identical values. Table alone is not a key; table, feature and chip together are.
- `NextHop` reports a `feature: ""` row of 280 where its feature rows sum to 281, and `OverlayEcmp` and
  `UnderlayEcmp` use their empty-feature row for a different resource with its own `maxLimit`. An empty feature
  cannot be assumed to be the table's total.
- `Host/V6Hosts` has `used: 0` and `free: 147208` against a `maxLimit` of 147455. `free` belongs to the pool the
  table shares, so `used + free != maxLimit` on 15 of the 75 rows.
- `MMU_MCAST/MmuReplHead` has `highWatermark` 856 against `used` 641, the only rows in the capture where the peak
  is meaningfully above the present value.
- `IFP` sits at 707 of 9216 while `Slice-1` and `Slice-2` are at 287 of 768. The aggregate is not what runs out.
- `usedPercent` is kept as EOS wrote it -- `0` for a row at 0.978% -- because a test asserts arex exports the
  ratio instead.

The row order is EOS's own and means nothing: captures from three switches each began with a different row. A
test reverses the array and asserts the output is unchanged.

`show_hardware_capacity_spine.json` is the same command from a spine in the same fabric, kept for the shapes the
leaf cannot show:

- `MAC` is entirely zero. The spine forwards for the fabric without learning addresses itself, so this is a table
  that exists and holds nothing -- which a test distinguishes from a table that is absent.
- `LPM/V4Routes` has `highWatermark` 7 against `used` 6, so the watermark being above the present value is not a
  quirk of one table on one switch.
- `NextHop` reports a rollup of 51 where its features sum to 52, the same off-by-one as the leaf's 280 against
  281. One capture would look like a glitch; two make it the way EOS counts.
- Its rows arrive in a third distinct order.

## Synthetic additions

Everything is real capture except the following, which cover cases the sampled switch could not
produce. They follow the schema of a real sibling entry.

- `show_interfaces.json` → **`Ethernet2/1`**: a down interface with non-zero
  `totalInErrors` (4211), `totalOutErrors` (19), `outDiscards` (23) and error detail. Every real
  interface reported zero for these, so a test asserting zero would pass with the old wrong field
  names too and prove nothing.
- `show_ip_bgp_summary_vrf_all.json` → **`203.0.113.168`** was set to `peerState: "Idle"` with
  `underMaintenance: true`, to cover the not-Established path.

## Schema variation covered

`show_interfaces_phy_detail.json` holds three interfaces because the PHY schema differs by speed:

| Interface | Speed | `fec` block | `pcs.blockLock` | `correctedSymbols` | serdes lanes |
| --- | --- | --- | --- | --- | --- |
| `Ethernet1/1` | 10Gbps | absent | present | — | 1 |
| `Ethernet4/1` | 25Gbps | present | absent | empty | 1 |
| `Ethernet29/1` | 100Gbps | present | absent | 4 lanes | 4 |

With RS-FEC running, FEC alignment lock replaces PCS block lock, so a link has one or the other —
never both.

`show_interfaces_transceiver_detail.json` covers three media types with materially different
thresholds (`txBias` high alarm is 11.0 on 100GBASE-SR4 but 80.0 on 40GBASE-LRL4), `totalRxPower`
reporting no threshold values at all, and two dark optics whose `rxPower` sits at the EOS floor
of -30.0 dBm — below their own low alarm, while transmitting normally.

## EVPN/VXLAN captures

The VXLAN fixtures cover two independent fabrics. Fabric A has two leaf VTEPs learned for both
unicast and flood traffic plus a spine with no VXLAN interface. Fabric B is a back-to-back leaf
pair whose VTEPs are learned for flood traffic only.

The matching interface fixtures preserve source and remote VTEP relationships, VLAN-to-VNI maps,
EVPN-sourced L3 VNIs, flood lists, and the empty IPv6 lists EOS emits. VLAN IDs, VNIs, VRF names,
VTEP addresses, and the non-standard secondary UDP port are synthetic and consistent within each
fabric. `Vxlan1`, `Loopback0`, UDP port 4789, the /32 mask, and the all-zero MAC are protocol or
sentinel values rather than fabric identifiers, so they remain unchanged.

`show_interface_vxlan_1_no_interface.txt` records the spine response when the command is issued
without a VXLAN interface. The switch prompt is intentionally omitted.

The `show_bgp_evpn_summary_fabric_a_*.json` fixtures contain two established EVPN peers per
switch in the default VRF. Router and peer addresses, ASNs, timestamps, and exact counters are
synthetic; numeric types, counter magnitudes, differing advertised-prefix counts, reciprocal
leaf-to-spine relationships, and the steady-state session shape remain.

The Fabric B EVPN summary fixtures cover a back-to-back pair with one established peer per leaf.

`show_bgp_evpn_route_type_count_*.json` captures all EVPN route types in one response, including
separate IPv4 and IPv6 type-5 totals. Exact nonzero counts are synthetic, but their relative scale
is retained; zero remains zero. These aggregate totals are not interchangeable with the different
cardinality returned by filtering one route type before applying `count`.

The captures support interpreting the aggregate values as path entries rather than unique NLRIs.
On the single-peer Fabric B leaves, aggregate type-2/type-3/type-5 totals equal their filtered
counts. The Fabric A spine also matches, apart from one type-2 route of capture-time drift, while
the dual-peer leaves have larger aggregate totals because remote routes can carry multiple paths.

VXLAN address-table counts remain small so quiet-fabric behavior is represented rather than
replaced with a busy synthetic table. The Fabric A leaves each report one remote VTEP with four
entries, while the spine and both Fabric B leaves return an empty `vtepCounts` object.

The paired `show_bgp_evpn_instance_fabric_a_leaf_*.json` fixtures each contain four VLAN-aware
bundles, 38 unique Ethernet segments, and 59 bundle-to-segment entries. DF ownership is
complementary: leaf 1 elects itself for most entries and its peer for the rest, and leaf 2 the
reverse. Bundle and VRF names, RDs, route targets, ESIs, Port-Channel identifiers, and peer
addresses are synthetic. Repeated segments and interfaces retain the same replacement within and
across both fixtures.

One segment, `0000:0000:0000:0000:1002` on `Port-Channel112` in `TENANT_PROD_PRIVATE`, is down.
Its shape was copied field for field from a real capture of these same two switches taken while
one leg of a multihomed link was shut, so only the identifiers are synthetic. It is the one case
the healthy fleet cannot show, and it carries three things the rest of the capture does not:

- `dFElectionAlgorithm` is **replaced by** `dFElectionState: "pending"`. The two keys are mutually
  exclusive -- across 118 captured segment entries every one had exactly one of them -- so a
  decoder that knows only the algorithm silently drops the field that says why there is no
  forwarder.
- `dFPeer` carries an **empty** `ip` rather than being absent or null, which is what makes "no DF
  elected" representable at all.
- `nonDFPeers` and `forwardingPeers` are empty arrays, not null.

The two leaves disagree about it, and that is faithful to the capture. Leaf 1, which lost the
link, reports the segment down. Leaf 2 reports it **up** -- its own link is fine -- but with
`forwardingPeers` down to one entry and itself newly elected DF. A rule watching only `state`
would see nothing wrong from leaf 2; `forwardingPeers` is what shows the segment is running on one
leg from either side.

`show_bgp_evpn_instance_fabric_a_spine_1.json` preserves the empty `bgpEvpnInstances` object
returned by a route-reflector spine that does not terminate EVPN instances locally.

The paired `show_bgp_evpn_instance_fabric_b_leaf_*.json` fixtures each contain one synthetic
storage bundle with 17 unique Ethernet segments. Every segment is up and uses modulus DF election.
Both views agree that leaf 2 is the elected DF for all 17 entries.
