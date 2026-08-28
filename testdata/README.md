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
