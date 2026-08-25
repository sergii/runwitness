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

TRACE_ID = os.environ["RW_FIXTURE_TRACE_ID"]
SPAN_ID = os.environ["RW_FIXTURE_SPAN_ID"]
SPAN_NAME = os.environ.get("RW_FIXTURE_SPAN_NAME", "checkout.capture")


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
                "serverInfo": {"name": "fake-otlp-mcp", "version": "finding-identity-v004"},
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
        tool_result(message["id"], {
            "traces": [{
                "trace_id": TRACE_ID,
                "span_id": SPAN_ID,
                "service_name": "checkout-service",
                "span_name": SPAN_NAME,
                "start_time_unix_nano": 1787637600000000000,
                "end_time_unix_nano": 1787637600100000000,
                "status": "ERROR",
                "attributes": {"fixture": "finding-identity"},
            }],
            "logs": [],
            "metrics": [],
        })
        continue

    tool_result(message["id"], {})
'''


class RunWitnessFindingIdentityV004Contract(unittest.TestCase):
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

    def observed_run(self, cwd: Path, backend: Path, trace_id: str, span_id: str, span_name="checkout.capture"):
        runs_root = cwd / ".runwitness" / "runs"
        before = set(runs_root.iterdir()) if runs_root.exists() else set()

        env = os.environ.copy()
        env["RUNWITNESS_OTLP_MCP_BIN"] = str(backend)
        env["RW_FIXTURE_TRACE_ID"] = trace_id
        env["RW_FIXTURE_SPAN_ID"] = span_id
        env["RW_FIXTURE_SPAN_NAME"] = span_name

        result = subprocess.run(
            [
                str(self.runner_bin),
                "run",
                "--otel",
                "--",
                sys.executable,
                "-c",
                "print('target-ok')",
            ],
            cwd=cwd,
            env=env,
            text=True,
            stdout=subprocess.PIPE,
            stderr=subprocess.PIPE,
            check=False,
        )

        after = set(runs_root.iterdir())
        created = [path for path in after - before if path.is_dir()]
        self.assertEqual(1, len(created))
        document = json.loads((created[0] / "run.json").read_text())
        errors = sorted(self.run_validator.iter_errors(document), key=lambda error: list(error.path))
        self.assertEqual([], errors)
        self.assertEqual(1, result.returncode, result.stderr)
        self.assertEqual(0, document["run"]["process"]["exit_code"])
        self.assertEqual("fail", document["verdict"]["status"])
        self.assertEqual(1, len(document["findings"]))
        return document

    def test_same_logical_problem_has_same_finding_id_across_runs(self):
        with tempfile.TemporaryDirectory() as temp:
            cwd = Path(temp)
            backend = self.create_backend(cwd)

            first = self.observed_run(
                cwd,
                backend,
                "11111111111111111111111111111111",
                "1111111111111111",
            )
            second = self.observed_run(
                cwd,
                backend,
                "22222222222222222222222222222222",
                "2222222222222222",
            )

            self.assertNotEqual(first["run"]["run_id"], second["run"]["run_id"])

            first_finding = first["findings"][0]
            second_finding = second["findings"][0]
            self.assertEqual(first_finding["finding_id"], second_finding["finding_id"])
            self.assertNotEqual(first_finding["evidence_refs"], second_finding["evidence_refs"])

            first_gate = next(g for g in first["verdict"]["gates"] if g["rule_id"] == "runtime.no_errors")
            second_gate = next(g for g in second["verdict"]["gates"] if g["rule_id"] == "runtime.no_errors")
            self.assertEqual([first_finding["finding_id"]], first_gate["finding_ids"])
            self.assertEqual([second_finding["finding_id"]], second_gate["finding_ids"])

    def test_different_logical_operation_has_different_finding_id(self):
        with tempfile.TemporaryDirectory() as temp:
            cwd = Path(temp)
            backend = self.create_backend(cwd)

            checkout = self.observed_run(
                cwd,
                backend,
                "33333333333333333333333333333333",
                "3333333333333333",
                "checkout.capture",
            )
            refund = self.observed_run(
                cwd,
                backend,
                "44444444444444444444444444444444",
                "4444444444444444",
                "refund.capture",
            )

            self.assertNotEqual(
                checkout["findings"][0]["finding_id"],
                refund["findings"][0]["finding_id"],
            )


if __name__ == "__main__":
    unittest.main(verbosity=2)
