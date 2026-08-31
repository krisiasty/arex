#!/usr/bin/env python3
"""Validate the shared contract of arex Grafana dashboard JSON files."""

from __future__ import annotations

import argparse
import json
import re
import sys
from pathlib import Path
from typing import Any


ROOT = Path(__file__).resolve().parent.parent
GRAFANA_DIR = ROOT / "monitoring" / "grafana"
UID_RE = re.compile(r"^[A-Za-z0-9_-]{8,40}$")
GENERIC_RUNTIME_METRIC_RE = re.compile(r"\b(?:go|process)_[A-Za-z_:][A-Za-z0-9_:]*")
DATASOURCE_VALUES = {"$datasource", "${datasource}"}
DASHBOARD_IDENTITIES = {
    "arex-health.json": ("arex-exporter-health", "arex / Exporter health"),
    "fleet-overview.json": ("arex-fleet-overview", "arex / Fleet overview"),
    "interfaces-optics.json": ("arex-interfaces-optics", "arex / Interfaces and optics"),
    "minimal.json": ("arex-template", "arex / Dashboard template"),
    "routing-overlay.json": ("arex-routing-overlay", "arex / Routing and overlay"),
    "switch-detail.json": ("arex-switch-detail", "arex / Switch detail"),
}


class DuplicateKeyError(ValueError):
    """A JSON object contains the same key more than once."""


def reject_duplicate_keys(pairs: list[tuple[str, Any]]) -> dict[str, Any]:
    result: dict[str, Any] = {}
    for key, value in pairs:
        if key in result:
            raise DuplicateKeyError(f"duplicate JSON key {key!r}")
        result[key] = value
    return result


def canonical_json(value: Any) -> str:
    return json.dumps(value, ensure_ascii=False, indent=2) + "\n"


def display_path(path: Path) -> str:
    try:
        return str(path.resolve().relative_to(ROOT))
    except ValueError:
        return str(path)


def query_text(variable: dict[str, Any]) -> str:
    query = variable.get("query")
    if isinstance(query, str):
        return query
    if isinstance(query, dict) and isinstance(query.get("query"), str):
        return query["query"]
    return ""


def validate_datasource(value: Any, location: str) -> list[str]:
    if isinstance(value, str):
        if value in DATASOURCE_VALUES:
            return []
        return [f"{location} hard-codes datasource {value!r}"]

    if not isinstance(value, dict):
        return [f"{location} datasource must be a string or object"]

    uid = value.get("uid")
    if uid not in DATASOURCE_VALUES:
        return [f"{location}.uid hard-codes datasource {uid!r}"]
    if value.get("type") != "prometheus":
        return [f"{location}.type must be 'prometheus'"]
    return []


def find_datasource_errors(value: Any, location: str = "dashboard") -> list[str]:
    errors: list[str] = []
    if isinstance(value, dict):
        for key, child in value.items():
            child_location = f"{location}.{key}"
            if key == "datasource":
                errors.extend(validate_datasource(child, child_location))
            else:
                errors.extend(find_datasource_errors(child, child_location))
    elif isinstance(value, list):
        for index, child in enumerate(value):
            errors.extend(find_datasource_errors(child, f"{location}[{index}]"))
    return errors


def validate_variables(dashboard: dict[str, Any]) -> list[str]:
    errors: list[str] = []
    templating = dashboard.get("templating")
    if not isinstance(templating, dict) or not isinstance(templating.get("list"), list):
        return ["templating.list must be an array"]

    variables = templating["list"]
    names = [variable.get("name") for variable in variables if isinstance(variable, dict)]
    if len(names) != len(set(names)):
        errors.append("templating.list contains duplicate variable names")

    by_name = {
        variable.get("name"): variable
        for variable in variables
        if isinstance(variable, dict) and isinstance(variable.get("name"), str)
    }
    for required in ("datasource", "job", "switch"):
        if required not in by_name:
            errors.append(f"templating.list is missing {required!r}")

    datasource = by_name.get("datasource", {})
    if datasource.get("type") != "datasource" or datasource.get("query") != "prometheus":
        errors.append("datasource variable must select Prometheus datasources")

    job = by_name.get("job", {})
    if job.get("type") != "query" or "label_values(arex_build_info, job)" not in query_text(job):
        errors.append("job variable must read job from arex_build_info")

    switch = by_name.get("switch", {})
    switch_query = query_text(switch)
    if switch.get("type") != "query" or "label_values(arista_info" not in switch_query:
        errors.append("switch variable must read switch from arista_info")
    if '$job' not in switch_query:
        errors.append("switch variable must be restricted by $job")

    positions = [names.index(name) for name in ("datasource", "job", "switch") if name in names]
    if positions != sorted(positions):
        errors.append("variables must be ordered datasource, job, switch")
    return errors


def validate_links(dashboard: dict[str, Any]) -> list[str]:
    links = dashboard.get("links")
    if not isinstance(links, list):
        return ["links must be an array"]

    for link in links:
        if not isinstance(link, dict) or link.get("type") != "dashboards":
            continue
        tags = link.get("tags")
        if link.get("includeVars") is True and link.get("keepTime") is True and isinstance(tags, list):
            if "arex" in tags:
                return []
    return ["links must include arex dashboard navigation with variables and time"]


def validate_panels(dashboard: dict[str, Any]) -> list[str]:
    panels = dashboard.get("panels")
    if not isinstance(panels, list):
        return ["panels must be an array"]

    errors: list[str] = []
    for index, panel in enumerate(panels):
        if not isinstance(panel, dict):
            errors.append(f"panels[{index}] must be an object")
            continue
        if panel.get("type") == "row":
            continue
        defaults = panel.get("fieldConfig", {}).get("defaults", {})
        no_value = defaults.get("noValue") if isinstance(defaults, dict) else None
        if not isinstance(no_value, str) or not no_value.strip():
            errors.append(f"panels[{index}] must set fieldConfig.defaults.noValue")

        targets = panel.get("targets", [])
        if not isinstance(targets, list):
            continue
        for target_index, target in enumerate(targets):
            expression = target.get("expr") if isinstance(target, dict) else None
            if not isinstance(expression, str):
                continue
            if GENERIC_RUNTIME_METRIC_RE.search(expression) and "arex_build_info" not in expression:
                errors.append(
                    f"panels[{index}].targets[{target_index}] generic runtime metric must be scoped to arex_build_info"
                )
    return errors


def validate_dashboard(path: Path, dashboard: Any) -> list[str]:
    if not isinstance(dashboard, dict):
        return ["dashboard root must be an object"]

    errors: list[str] = []
    if "id" in dashboard:
        errors.append("database-assigned id must be omitted")
    if dashboard.get("schemaVersion") != 42:
        errors.append("schemaVersion must be 42")
    if dashboard.get("version") != 1:
        errors.append("version must be 1 in canonical source")
    if dashboard.get("refresh") != "30s":
        errors.append("refresh must be '30s'")
    if dashboard.get("timezone") != "browser":
        errors.append("timezone must be 'browser'")

    uid = dashboard.get("uid")
    if not isinstance(uid, str) or not UID_RE.fullmatch(uid):
        errors.append("uid must contain 8-40 letters, digits, underscores or hyphens")
    title = dashboard.get("title")
    if not isinstance(title, str) or not title.strip():
        errors.append("title must be a non-empty string")

    identity = DASHBOARD_IDENTITIES.get(path.name)
    if identity is not None and (uid, title) != identity:
        errors.append(f"{path.name} must use uid {identity[0]!r} and title {identity[1]!r}")

    tags = dashboard.get("tags")
    if not isinstance(tags, list) or not {"arex", "arista"}.issubset(tags):
        errors.append("tags must include 'arex' and 'arista'")

    time_range = dashboard.get("time")
    if not isinstance(time_range, dict) or not time_range.get("from") or not time_range.get("to"):
        errors.append("time must define from and to")

    errors.extend(validate_variables(dashboard))
    errors.extend(validate_links(dashboard))
    errors.extend(validate_panels(dashboard))
    errors.extend(find_datasource_errors(dashboard))
    return errors


def check_paths(paths: list[Path], format_files: bool = False) -> list[str]:
    errors: list[str] = []
    dashboards: list[tuple[Path, Any]] = []
    for path in paths:
        try:
            text = path.read_text(encoding="utf-8")
            dashboard = json.loads(text, object_pairs_hook=reject_duplicate_keys)
        except (OSError, UnicodeError, json.JSONDecodeError, DuplicateKeyError) as error:
            errors.append(f"{display_path(path)}: {error}")
            continue

        canonical = canonical_json(dashboard)
        if format_files:
            path.write_text(canonical, encoding="utf-8")
        elif text != canonical:
            errors.append(f"{display_path(path)}: JSON is not canonically formatted; run with --format")
        dashboards.append((path, dashboard))

    uid_paths: dict[str, Path] = {}
    for path, dashboard in dashboards:
        for error in validate_dashboard(path, dashboard):
            errors.append(f"{display_path(path)}: {error}")
        if not isinstance(dashboard, dict) or not isinstance(dashboard.get("uid"), str):
            continue
        uid = dashboard["uid"]
        if uid in uid_paths:
            errors.append(
                f"{display_path(path)}: uid {uid!r} duplicates {display_path(uid_paths[uid])}"
            )
        else:
            uid_paths[uid] = path
    return errors


def parse_args() -> argparse.Namespace:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--format", action="store_true", help="rewrite JSON with canonical formatting")
    parser.add_argument("paths", nargs="*", type=Path, help="dashboard JSON paths; defaults to monitoring/grafana")
    return parser.parse_args()


def main() -> int:
    args = parse_args()
    paths = args.paths or sorted(GRAFANA_DIR.rglob("*.json"))
    if not paths:
        print("check-grafana: no dashboard JSON files found", file=sys.stderr)
        return 1

    errors = check_paths(paths, format_files=args.format)
    for error in errors:
        print(f"check-grafana: {error}", file=sys.stderr)
    if errors:
        return 1

    action = "formatted and validated" if args.format else "validated"
    print(f"check-grafana: {action} {len(paths)} dashboard(s)")
    return 0


if __name__ == "__main__":
    sys.exit(main())
