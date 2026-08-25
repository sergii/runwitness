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
EVIDENCE_SCHEMA_PATH = REPO_ROOT / "schemas" / "evidence-v1.schema.json"


FAKE_OTLP_MCP = r'''#!/usr/bin/env python3
import json
import sys


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
                "serverInfo": {"name": "fake-otlp-mcp", "version": "runtime-error-v003"},
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
                "trace_id": "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
                "span_id": "bbbbbbbbbbbbbbbb",
                "service_name": "checkout-service",
                "span_name": "checkout.capture",
                "start_time_unix_nano": 1787637600000000000,
                "end_time_unix_nano": 1787637600100000000,
                "status": "ERROR",
                "attributes": {"fixture": "runtime-error"},
            }],
            "logs": [],
            "metrics": [],
        })
        continue

    tool_result(message["id"], {})
'''


class RunWitnessRuntimeErrorV003Contract(unittest.TestCase):
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
        cls.evidence_validator = Draft202012Validator(
            json.loads(EVIDENCE_SCHEMA_PATH.read_text()),
            format_checker=FormatChecker(),
        )

    def create_backend(self, cwd: Path) -> Path:
        backend = cwd / "fake-otlp-mcp"
        backend.write_text(textwrap.dedent(FAKE_OTLP_MCP))
        backend.chmod(0o755)
        return backend

    def test_successful_target_with_error_span_fails_runtime_gate(self):
        with tempfile.TemporaryDirectory() as temp:
            cwd = Path(temp)
            backend = self.create_backend(cwd)
            env = os.environ.copy()
            env["RUNWITNESS_OTLP_MCP_BIN"] = str(backend)

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

            runs = [path for path in (cwd / ".runwitness" / "runs").iterdir() if path.is_dir()]
            self.assertEqual(1, len(runs))
            run_dir = runs[0]
            document = json.loads((run_dir / "run.json").read_text())
            run_errors = sorted(self.run_validator.iter_errors(document), key=lambda error: list(error.path))
            self.assertEqual([], run_errors)

            evidence = [
                json.loads(line)
                for line in (run_dir / "evidence.jsonl").read_text().splitlines()
                if line.strip()
            ]
            self.assertEqual(1, len(evidence))
            evidence_errors = sorted(
                self.evidence_validator.iter_errors(evidence[0]),
                key=lambda error: list(error.path),
            )
            self.assertEqual([], evidence_errors)
            self.assertEqual("otel.span", evidence[0]["kind"])
            self.assertEqual("ERROR", evidence[0]["attributes"]["span.status"])

            self.assertEqual(1, result.returncode, result.stderr)
            self.assertEqual(0, document["run"]["process"]["exit_code"])
            self.assertEqual("fail", document["verdict"]["status"])
            self.assertEqual(1, document["summary"]["evidence_count"])
            self.assertEqual(1, document["summary"]["finding_count"])

            self.assertEqual(1, len(document["findings"]))
            finding = document["findings"][0]
            self.assertTrue(finding["finding_id"].startswith("rwf_"))
            self.assertEqual("runtime.error", finding["kind"])
            self.assertEqual("error", finding["severity"])
            self.assertEqual("otel.span.error", finding["rule_id"])
            self.assertEqual(["otel"], finding["sources"])
            self.assertEqual([evidence[0]["evidence_id"]], finding["evidence_refs"])
            self.assertIn("checkout.capture", finding["summary"])

            gates = [gate for gate in document["verdict"]["gates"] if gate["rule_id"] == "runtime.no_errors"]
            self.assertEqual(1, len(gates))
            gate = gates[0]
            self.assertEqual("fail", gate["action"])
            self.assertEqual("triggered", gate["outcome"])
            self.assertEqual([finding["finding_id"]], gate["finding_ids"])


if __name__ == "__main__":
    unittest.main(verbosity=2)
