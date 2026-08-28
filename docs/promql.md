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

## Alerting examples

```yaml
groups:
  - name: arista
    rules:
      - alert: SwitchScrapeDown
        expr: arista_scrape_success == 0
        for: 2m

      - alert: SwitchCommandFailing
        expr: arista_command_success == 0
        for: 15m
        annotations:
          summary: "{{ $labels.switch }}: eAPI command {{ $labels.command }} is failing"

      - alert: SwitchMemoryLow
        expr: arista_memory_available_bytes / arista_memory_total_bytes < 0.1
        for: 15m

      - alert: SwitchTemperatureAlert
        expr: arista_temperature_alert == 1
        for: 1m

      - alert: SwitchTemperatureNearOverheat
        expr: |
          arista_temperature_celsius
            > on(switch, sensor) arista_temperature_overheat_threshold_celsius * 0.9
        for: 5m

      - alert: SwitchFanDown
        expr: arista_fan_ok == 0
        for: 1m

      - alert: SwitchPSUDown
        expr: arista_psu_ok == 0
        for: 1m

      - alert: SwitchPSUHighLoad
        expr: arista_psu_output_power_watts / arista_psu_capacity_watts > 0.8
        for: 5m

      - alert: SwitchBGPPeerDown
        expr: arista_bgp_peer_up == 0 and arista_bgp_peer_under_maintenance == 0
        for: 2m

      - alert: SwitchInterfaceDown
        expr: arista_interface_link_up == 0
        for: 2m

      - alert: SwitchInterfaceFlapping
        expr: rate(arista_interface_link_status_changes_total[15m]) * 900 > 3
        for: 5m

      - alert: SwitchInterfaceErrors
        expr: rate(arista_interface_in_errors_total[15m]) > 0
        for: 10m

      # Gated on link_up: a populated but dark cage sits at the -30 dBm floor
      # for ever, well below its own low alarm, and would alert permanently.
      - alert: OpticRxPowerLow
        expr: |
          arista_transceiver_rx_power_dbm
            < on(switch, interface) group_left
              arista_transceiver_rx_power_threshold_dbm{level="low_warn"}
          and on(switch, interface) arista_interface_link_up == 1
        for: 10m

      - alert: OpticTemperatureHigh
        expr: |
          arista_transceiver_temperature_celsius
            > on(switch, interface) group_left
              arista_transceiver_temperature_threshold_celsius{level="high_warn"}
        for: 5m

      # Interface counters, not FEC: these refresh every poll, whereas FEC
      # registers on the tested switch were static for 107 days. See
      # "FEC counters are diagnostic, not an alerting source".
      - alert: LinkFCSErrors
        expr: rate(arista_interface_in_errors_detail_total{cause="fcsErrors"}[15m]) > 0
        for: 10m

      # FEC as a rule at all is weak; if you want one, require the counter to
      # have actually moved recently rather than testing the gauge for > 0.
      - alert: OpticFECUncorrectedErrors
        expr: |
          delta(arista_phy_fec_uncorrected_codewords[1h]) > 0
          and on(switch, interface) arista_interface_link_up == 1
        for: 5m

      - alert: LinkMacFault
        expr: arista_phy_mac_local_fault == 1 or arista_phy_mac_remote_fault == 1
        for: 5m

      - alert: LinkHighBER
        expr: arista_phy_pcs_high_ber == 1
        for: 5m
```

Two rules above deserve the comments they carry. Both are cases where the obvious version of the rule fires
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
