#!/usr/bin/env python3

import json
import os
import subprocess
import sys
import tempfile
import textwrap
import unittest
from pathlib import Path

from jsonschema import Draft202012Validator, FormatChecker


REPO_ROOT = Path(__file__).resolve().parents[2]
RUN_SCHEMA_PATH = REPO_ROOT / "schemas" / "run-v1.schema.json"


FAKE_OTLP_MCP = r'''#!/usr/bin/env python3
import json
import os
import sys

SPAN_NAMES = [name for name in os.environ.get("RW_FIXTURE_SPAN_NAMES", "").split(",") if name]


def send(payload):
    sys.stdout.write(json.dumps(payload, separators=(",", ":")) + "\n")
    sys.stdout.flush()


def tool_result(request_id, structured):
    send({
        "jsonrpc": "2.0",
        "id": request_id,
        "result": {
            "content": [],
            "structuredContent": structured,
            "isError": False,
        },
    })


for raw in sys.stdin:
    raw = raw.strip()
    if not raw:
        continue

    message = json.loads(raw)
    method = message.get("method")

    if method == "initialize":
        params = message.get("params", {})
        send({
            "jsonrpc": "2.0",
            "id": message["id"],
            "result": {
                "protocolVersion": params.get("protocolVersion", "2025-06-18"),
                "capabilities": {"tools": {}},
                "serverInfo": {"name": "fake-otlp-mcp", "version": "baseline-diff-v006"},
            },
        })
        continue

    if method == "notifications/initialized":
        continue

    if method == "ping":
        send({"jsonrpc": "2.0", "id": message["id"], "result": {}})
        continue

    if method != "tools/call":
        if "id" in message:
            send({
                "jsonrpc": "2.0",
                "id": message["id"],
                "error": {"code": -32601, "message": "method not found"},
            })
        continue

    params = message.get("params", {})
    name = params.get("name")

    if name == "get_otlp_endpoint":
        tool_result(message["id"], {
            "endpoint": "127.0.0.1:4317",
            "protocol": "grpc",
            "environment_vars": {
                "OTEL_EXPORTER_OTLP_ENDPOINT": "127.0.0.1:4317",
                "OTEL_EXPORTER_OTLP_PROTOCOL": "grpc",
            },
        })
        continue

    if name == "create_snapshot":
        snapshot_name = params.get("arguments", {}).get("name", "snapshot")
        tool_result(message["id"], {"name": snapshot_name})
        continue

    if name == "get_snapshot_data":
        traces = []
        for index, span_name in enumerate(SPAN_NAMES, start=1):
            traces.append({
                "trace_id": f"{index:032x}",
                "span_id": f"{index:016x}",
                "service_name": "baseline-fixture-service",
                "span_name": span_name,
                "start_time_unix_nano": 1787637600000000000 + index,
                "end_time_unix_nano": 1787637600100000000 + index,
                "status": "ERROR",
                "attributes": {"fixture": "baseline-diff"},
            })
        tool_result(message["id"], {
            "traces": traces,
            "logs": [],
            "metrics": [],
        })
        continue

    tool_result(message["id"], {})
'''


class RunWitnessBaselineDiffV006Contract(unittest.TestCase):
    @classmethod
    def setUpClass(cls):
        configured = os.environ.get("RUNWITNESS_BIN")
        cls.runner_bin = Path(configured) if configured else REPO_ROOT / "bin" / "runwitness"
        if not cls.runner_bin.is_absolute():
            cls.runner_bin = (REPO_ROOT / cls.runner_bin).resolve()
        if not cls.runner_bin.exists():
            raise AssertionError(f"RunWitness binary does not exist at {cls.runner_bin}")

        cls.run_validator = Draft202012Validator(
            json.loads(RUN_SCHEMA_PATH.read_text()),
            format_checker=FormatChecker(),
        )

    def create_backend(self, cwd: Path) -> Path:
        backend = cwd / "fake-otlp-mcp"
        backend.write_text(textwrap.dedent(FAKE_OTLP_MCP))
        backend.chmod(0o755)
        return backend

    def observed_run(self, cwd: Path, backend: Path, span_names, baseline_run_id=None):
        runs_root = cwd / ".runwitness" / "runs"
        before = set(runs_root.iterdir()) if runs_root.exists() else set()

        env = os.environ.copy()
        env["RUNWITNESS_OTLP_MCP_BIN"] = str(backend)
        env["RW_FIXTURE_SPAN_NAMES"] = ",".join(span_names)

        target = [sys.executable, "-c", "print('target-ok')"]
        argv = [str(self.runner_bin), "run", "--otel"]
        if baseline_run_id is not None:
            argv.extend(["--baseline", baseline_run_id])
        argv.extend(["--", *target])

        result = subprocess.run(
            argv,
            cwd=cwd,
            env=env,
            text=True,
            stdout=subprocess.PIPE,
            stderr=subprocess.PIPE,
            check=False,
        )

        self.assertTrue(runs_root.exists(), result.stderr)
        after = set(runs_root.iterdir())
        created = [path for path in after - before if path.is_dir()]
        self.assertEqual(1, len(created), result.stderr)

        document = json.loads((created[0] / "run.json").read_text())
        errors = sorted(self.run_validator.iter_errors(document), key=lambda error: list(error.path))
        self.assertEqual([], errors)
        self.assertEqual(target, document["run"]["command"]["argv"])
        self.assertEqual(0, document["run"]["process"]["exit_code"])

        expected_exit = 1 if span_names else 0
        expected_verdict = "fail" if span_names else "pass"
        self.assertEqual(expected_exit, result.returncode, result.stderr)
        self.assertEqual(expected_verdict, document["verdict"]["status"])
        return result, document

    def assert_empty_reserved_diff(self, diff):
        self.assertEqual([], diff["regressed"])
        self.assertEqual([], diff["improved"])

    def test_same_logical_finding_is_unchanged(self):
        with tempfile.TemporaryDirectory() as temp:
            cwd = Path(temp)
            backend = self.create_backend(cwd)

            _, baseline = self.observed_run(cwd, backend, ["checkout.capture"])
            baseline_id = baseline["run"]["run_id"]
            baseline_finding_id = baseline["findings"][0]["finding_id"]

            _, current = self.observed_run(
                cwd,
                backend,
                ["checkout.capture"],
                baseline_run_id=baseline_id,
            )

            self.assertEqual({"run_id": baseline_id}, current["baseline"])
            self.assertEqual(baseline_finding_id, current["findings"][0]["finding_id"])
            self.assertEqual([], current["diff"]["new"])
            self.assertEqual([], current["diff"]["resolved"])
            self.assertEqual([baseline_finding_id], current["diff"]["unchanged"])
            self.assert_empty_reserved_diff(current["diff"])

            gate = next(g for g in current["verdict"]["gates"] if g["rule_id"] == "runtime.no_errors")
            self.assertEqual("triggered", gate["outcome"])
            self.assertEqual([baseline_finding_id], gate["finding_ids"])

    def test_replacing_logical_finding_is_new_and_resolved(self):
        with tempfile.TemporaryDirectory() as temp:
            cwd = Path(temp)
            backend = self.create_backend(cwd)

            _, baseline = self.observed_run(cwd, backend, ["checkout.capture"])
            baseline_id = baseline["run"]["run_id"]
            old_id = baseline["findings"][0]["finding_id"]

            _, current = self.observed_run(
                cwd,
                backend,
                ["refund.capture"],
                baseline_run_id=baseline_id,
            )
            new_id = current["findings"][0]["finding_id"]

            self.assertNotEqual(old_id, new_id)
            self.assertEqual([new_id], current["diff"]["new"])
            self.assertEqual([old_id], current["diff"]["resolved"])
            self.assertEqual([], current["diff"]["unchanged"])
            self.assert_empty_reserved_diff(current["diff"])

    def test_baseline_only_finding_is_resolved_without_triggering_current_gate(self):
        with tempfile.TemporaryDirectory() as temp:
            cwd = Path(temp)
            backend = self.create_backend(cwd)

            _, baseline = self.observed_run(cwd, backend, ["checkout.capture"])
            baseline_id = baseline["run"]["run_id"]
            old_id = baseline["findings"][0]["finding_id"]

            _, current = self.observed_run(cwd, backend, [], baseline_run_id=baseline_id)

            self.assertEqual([], current["findings"])
            self.assertEqual([], current["diff"]["new"])
            self.assertEqual([old_id], current["diff"]["resolved"])
            self.assertEqual([], current["diff"]["unchanged"])
            self.assert_empty_reserved_diff(current["diff"])
            self.assertFalse(any(g["rule_id"] == "runtime.no_errors" for g in current["verdict"]["gates"]))

    def test_current_only_finding_is_new(self):
        with tempfile.TemporaryDirectory() as temp:
            cwd = Path(temp)
            backend = self.create_backend(cwd)

            _, baseline = self.observed_run(cwd, backend, [])
            baseline_id = baseline["run"]["run_id"]

            _, current = self.observed_run(
                cwd,
                backend,
                ["checkout.capture"],
                baseline_run_id=baseline_id,
            )
            current_id = current["findings"][0]["finding_id"]

            self.assertEqual([current_id], current["diff"]["new"])
            self.assertEqual([], current["diff"]["resolved"])
            self.assertEqual([], current["diff"]["unchanged"])
            self.assert_empty_reserved_diff(current["diff"])

    def test_diff_lists_are_unique_and_lexicographically_sorted(self):
        with tempfile.TemporaryDirectory() as temp:
            cwd = Path(temp)
            backend = self.create_backend(cwd)

            _, baseline = self.observed_run(cwd, backend, ["old.one", "old.two"])
            baseline_id = baseline["run"]["run_id"]

            _, current = self.observed_run(
                cwd,
                backend,
                ["new.one", "new.two"],
                baseline_run_id=baseline_id,
            )

            self.assertEqual(2, len(current["diff"]["new"]))
            self.assertEqual(2, len(current["diff"]["resolved"]))
            self.assertEqual(sorted(set(current["diff"]["new"])), current["diff"]["new"])
            self.assertEqual(sorted(set(current["diff"]["resolved"])), current["diff"]["resolved"])
            self.assert_empty_reserved_diff(current["diff"])

    def test_missing_baseline_prevents_target_execution_and_run_creation(self):
        with tempfile.TemporaryDirectory() as temp:
            cwd = Path(temp)
            marker = cwd / "target-ran"
            missing_id = "0198f5f0-0000-7000-8000-000000000001"

            result = subprocess.run(
                [
                    str(self.runner_bin),
                    "run",
                    "--baseline",
                    missing_id,
                    "--",
                    sys.executable,
                    "-c",
                    f"from pathlib import Path; Path({str(marker)!r}).write_text('ran')",
                ],
                cwd=cwd,
                text=True,
                stdout=subprocess.PIPE,
                stderr=subprocess.PIPE,
                check=False,
            )

            self.assertEqual(2, result.returncode)
            self.assertIn("baseline", result.stderr.lower())
            self.assertFalse(marker.exists())
            self.assertFalse((cwd / ".runwitness").exists())


if __name__ == "__main__":
    unittest.main(verbosity=2)
