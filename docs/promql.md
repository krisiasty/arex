# PromQL and alerting

Queries worth having, and alert rules that fire on things that matter.

## Useful queries

```promql
# Uptime
time() - arista_boot_timestamp_seconds

# Memory utilisation — available, not free
(1 - arista_memory_available_bytes / arista_memory_total_bytes) * 100

# Interface utilisation as a percentage of link speed
rate(arista_interface_in_octets_total[5m]) * 8
  / on(switch, interface) arista_interface_speed_bits_per_second * 100

# PSU load %
arista_psu_output_power_watts / arista_psu_capacity_watts * 100

# Fan speed deviation (failing fan indicator)
arista_fan_speed_actual_percent - arista_fan_speed_configured_percent

# BGP session uptime
time() - arista_bgp_peer_state_change_timestamp_seconds

# Prefixes rejected by inbound policy
arista_bgp_peer_prefixes_received - arista_bgp_peer_prefixes_accepted

# Optical receive margin above the optic's own low warning, in dB
arista_transceiver_rx_power_dbm
  - on(switch, interface) group_left
    arista_transceiver_rx_power_threshold_dbm{level="low_warn"}

# Attach model/version to any metric
rate(arista_interface_in_octets_total[5m])
  * on(switch) group_left(model, version) arista_info
```

## Alerting

The rules are shipped as files rather than reproduced here, so there is one copy
to keep correct: [`monitoring/`](../monitoring/) holds them in the neutral
`groups:` format plus a `PrometheusRule` and a `VMRule` generated from it. See
[monitoring/README.md](../monitoring/README.md) for which file your evaluator
wants.

They are not in the Helm chart on purpose. arex runs where the switches are
reachable, which is often not where the metrics are evaluated.

Two of those rules deserve the comments they carry. Both are cases where the obvious version of the rule fires
permanently and gets deleted in its first week:

- **Dark optics.** A cage with an optic installed but nothing plugged in reports `rxPower` at the EOS floor of
  -30 dBm, while transmitting normally. That is below the optic's own low alarm, so an ungated rule alerts for
  ever. Gating on `arista_interface_link_up == 1` keeps the alert on links you actually care about.
- **Repaired fibre.** FEC error values persist after the fault is fixed, so
  `arista_phy_fec_uncorrected_codewords > 0` never clears. Requiring `delta()` over the gauge instead means the
  rule fires only when the counter actually moves. Note that FEC is a weak alerting source in general — see
  [FEC counters are diagnostic](metrics.md#fec-counters-are-diagnostic-not-an-alerting-source).

---

Back to the [README](../README.md). See also the [metrics reference](metrics.md).
