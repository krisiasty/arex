from __future__ import annotations

import copy
import json
import sys
import tempfile
import unittest
from pathlib import Path


ROOT = Path(__file__).resolve().parents[2]
sys.path.insert(0, str(ROOT / "hack"))

import check_grafana  # noqa: E402


FIXTURE = ROOT / "monitoring" / "grafana" / "testdata" / "minimal.json"


class CheckGrafanaTest(unittest.TestCase):
    @classmethod
    def setUpClass(cls) -> None:
        cls.dashboard = json.loads(FIXTURE.read_text(encoding="utf-8"))

    def write_dashboard(self, directory: str, name: str, dashboard: object) -> Path:
        path = Path(directory) / name
        path.write_text(check_grafana.canonical_json(dashboard), encoding="utf-8")
        return path

    def test_minimal_dashboard_is_valid(self) -> None:
        self.assertEqual(check_grafana.check_paths([FIXTURE]), [])

    def test_hardcoded_datasource_is_rejected(self) -> None:
        dashboard = copy.deepcopy(self.dashboard)
        dashboard["templating"]["list"][1]["datasource"]["uid"] = "prometheus-production"
        with tempfile.TemporaryDirectory() as directory:
            path = self.write_dashboard(directory, "dashboard.json", dashboard)
            errors = check_grafana.check_paths([path])
        self.assertTrue(any("hard-codes datasource" in error for error in errors), errors)

    def test_duplicate_uid_is_rejected(self) -> None:
        with tempfile.TemporaryDirectory() as directory:
            first = self.write_dashboard(directory, "first.json", self.dashboard)
            second = self.write_dashboard(directory, "second.json", self.dashboard)
            errors = check_grafana.check_paths([first, second])
        self.assertTrue(any("duplicates" in error for error in errors), errors)

    def test_dashboard_navigation_is_required(self) -> None:
        dashboard = copy.deepcopy(self.dashboard)
        dashboard["links"] = []
        with tempfile.TemporaryDirectory() as directory:
            path = self.write_dashboard(directory, "dashboard.json", dashboard)
            errors = check_grafana.check_paths([path])
        self.assertTrue(any("navigation" in error for error in errors), errors)

    def test_visualization_panel_requires_no_value_text(self) -> None:
        dashboard = copy.deepcopy(self.dashboard)
        dashboard["panels"] = [{"fieldConfig": {"defaults": {}}, "type": "stat"}]
        with tempfile.TemporaryDirectory() as directory:
            path = self.write_dashboard(directory, "dashboard.json", dashboard)
            errors = check_grafana.check_paths([path])
        self.assertTrue(any("noValue" in error for error in errors), errors)

    def test_generic_runtime_metric_must_be_scoped_to_arex(self) -> None:
        dashboard = copy.deepcopy(self.dashboard)
        dashboard["panels"] = [
            {
                "fieldConfig": {"defaults": {"noValue": "Exporter absent"}},
                "targets": [{"expr": 'go_goroutines{job=~"$job"}'}],
                "type": "timeseries",
            }
        ]
        with tempfile.TemporaryDirectory() as directory:
            path = self.write_dashboard(directory, "dashboard.json", dashboard)
            errors = check_grafana.check_paths([path])
        self.assertTrue(any("must be scoped to arex_build_info" in error for error in errors), errors)

    def test_kubernetes_resource_metric_must_be_scoped_to_arex(self) -> None:
        dashboard = copy.deepcopy(self.dashboard)
        dashboard["panels"] = [
            {
                "fieldConfig": {"defaults": {"noValue": "Kubernetes metrics unavailable"}},
                "targets": [{"expr": 'rate(container_cpu_usage_seconds_total{container!=""}[5m])'}],
                "type": "timeseries",
            }
        ]
        with tempfile.TemporaryDirectory() as directory:
            path = self.write_dashboard(directory, "dashboard.json", dashboard)
            errors = check_grafana.check_paths([path])
        self.assertTrue(
            any("Kubernetes resource metric must be scoped to arex_build_info" in error for error in errors)
        )

    def test_noncanonical_json_is_rejected(self) -> None:
        with tempfile.TemporaryDirectory() as directory:
            path = Path(directory) / "dashboard.json"
            path.write_text(json.dumps(self.dashboard), encoding="utf-8")
            errors = check_grafana.check_paths([path])
        self.assertTrue(any("canonically formatted" in error for error in errors), errors)


if __name__ == "__main__":
    unittest.main()
