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
                "serverInfo": {"name": "fake-otlp-mcp", "version": "test-v1"},
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
        tool_result(message["id"], {
            "name": snapshot_name,
            "trace_position": 0,
            "log_position": 0,
            "metric_position": 0,
            "message": "snapshot created",
        })
        continue

    if name == "get_snapshot_data":
        args = params.get("arguments", {})
        tool_result(message["id"], {
            "start_snapshot": args.get("start_snapshot", "start"),
            "end_snapshot": args.get("end_snapshot", "end"),
            "time_range": {
                "start_time": "2026-08-25T06:00:00Z",
                "end_time": "2026-08-25T06:00:01Z",
                "duration": "1s",
            },
            "traces": [{
                "trace_id": "0123456789abcdef0123456789abcdef",
                "span_id": "0123456789abcdef",
                "service_name": "fixture-service",
                "span_name": "fixture.operation",
                "start_time_unix_nano": 1787637600000000000,
                "end_time_unix_nano": 1787637600100000000,
                "status": "OK",
                "attributes": {"fixture.span": "yes"},
            }],
            "logs": [{
                "trace_id": "0123456789abcdef0123456789abcdef",
                "span_id": "0123456789abcdef",
                "service_name": "fixture-service",
                "severity": "INFO",
                "severity_number": 9,
                "body": "fixture log",
                "timestamp_unix_nano": 1787637600200000000,
                "attributes": {"fixture.log": "yes"},
            }],
            "metrics": [{
                "metric_name": "fixture.counter",
                "service_name": "fixture-service",
                "metric_type": "Sum",
                "timestamp_unix_nano": 1787637600300000000,
                "value": 3.0,
                "data_point_count": 1,
            }],
            "summary": {
                "trace_count": 1,
                "log_count": 1,
                "metric_count": 1,
                "services": ["fixture-service"],
                "trace_ids": ["0123456789abcdef0123456789abcdef"],
                "log_severities": {"INFO": 1},
                "metric_names": ["fixture.counter"],
            },
        })
        continue

    tool_result(message["id"], {})
'''


class RunWitnessOTELV002Contract(unittest.TestCase):
    @classmethod
    def setUpClass(cls):
        configured = os.environ.get("RUNWITNESS_BIN")
        cls.runner_bin = Path(configured) if configured else REPO_ROOT / "bin" / "runwitness"
        if not cls.runner_bin.is_absolute():
            cls.runner_bin = (REPO_ROOT / cls.runner_bin).resolve()

        if not cls.runner_bin.exists():
            raise AssertionError(
                f"RunWitness binary does not exist at {cls.runner_bin}. "
                "Build the implementation first or set RUNWITNESS_BIN."
            )

        run_schema = json.loads(RUN_SCHEMA_PATH.read_text())
        evidence_schema = json.loads(EVIDENCE_SCHEMA_PATH.read_text())
        cls.run_validator = Draft202012Validator(run_schema, format_checker=FormatChecker())
        cls.evidence_validator = Draft202012Validator(evidence_schema, format_checker=FormatChecker())

    def create_fake_backend(self, cwd: Path) -> Path:
        backend = cwd / "fake-otlp-mcp"
        backend.write_text(textwrap.dedent(FAKE_OTLP_MCP))
        backend.chmod(0o755)
        return backend

    def run_runner(self, cwd: Path, target_argv: list[str], *, backend: Path | None, extra_env=None):
        env = os.environ.copy()
        if backend is not None:
            env["RUNWITNESS_OTLP_MCP_BIN"] = str(backend)
        if extra_env:
            env.update(extra_env)

        return subprocess.run(
            [str(self.runner_bin), "run", "--otel", "--", *target_argv],
            cwd=cwd,
            env=env,
            text=True,
            stdout=subprocess.PIPE,
            stderr=subprocess.PIPE,
            check=False,
        )

    def load_single_run(self, cwd: Path):
        runs_root = cwd / ".runwitness" / "runs"
        self.assertTrue(runs_root.is_dir(), f"missing runs directory: {runs_root}")

        run_dirs = [path for path in runs_root.iterdir() if path.is_dir()]
        self.assertEqual(1, len(run_dirs), f"expected exactly one Run, got {run_dirs}")

        run_dir = run_dirs[0]
        document = json.loads((run_dir / "run.json").read_text())
        run_errors = sorted(self.run_validator.iter_errors(document), key=lambda error: list(error.path))
        self.assertEqual([], run_errors, "run.json must validate against schemas/run-v1.schema.json")

        evidence = []
        evidence_path = run_dir / "evidence.jsonl"
        self.assertTrue(evidence_path.is_file(), "evidence.jsonl must exist")
        for line_number, raw in enumerate(evidence_path.read_text().splitlines(), start=1):
            if not raw.strip():
                continue
            record = json.loads(raw)
            errors = sorted(self.evidence_validator.iter_errors(record), key=lambda error: list(error.path))
            self.assertEqual([], errors, f"Evidence line {line_number} must validate")
            evidence.append(record)

        return run_dir, document, evidence

    def test_otel_adapter_injects_environment_and_writes_evidence(self):
        with tempfile.TemporaryDirectory() as temp:
            cwd = Path(temp)
            backend = self.create_fake_backend(cwd)
            code = (
                "import json, os; "
                "print(json.dumps({"
                "'run_id': os.environ['RUNWITNESS_RUN_ID'], "
                "'endpoint': os.environ['OTEL_EXPORTER_OTLP_ENDPOINT'], "
                "'protocol': os.environ['OTEL_EXPORTER_OTLP_PROTOCOL'], "
                "'resource': os.environ['OTEL_RESOURCE_ATTRIBUTES']"
                "}))"
            )

            result = self.run_runner(
                cwd,
                [sys.executable, "-c", code],
                backend=backend,
                extra_env={"OTEL_RESOURCE_ATTRIBUTES": "service.name=fixture"},
            )

            self.assertEqual(0, result.returncode, result.stderr)
            run_dir, document, evidence = self.load_single_run(cwd)

            self.assertEqual("pass", document["verdict"]["status"])
            self.assertEqual(3, document["summary"]["evidence_count"])
            self.assertEqual(0, document["summary"]["finding_count"])
            self.assertEqual(3, len(evidence))

            otel_adapters = [adapter for adapter in document["adapters"] if adapter["name"] == "otel"]
            self.assertEqual(1, len(otel_adapters))
            self.assertEqual("ok", otel_adapters[0]["status"])

            kinds = {record["kind"] for record in evidence}
            self.assertEqual({"otel.span", "otel.log", "otel.metric"}, kinds)
            for record in evidence:
                self.assertEqual(document["run"]["run_id"], record["run_id"])
                self.assertEqual("otel", record["source"])

            observed_env = json.loads((run_dir / "stdout.log").read_text())
            self.assertEqual(document["run"]["run_id"], observed_env["run_id"])
            self.assertEqual("127.0.0.1:4317", observed_env["endpoint"])
            self.assertEqual("grpc", observed_env["protocol"])
            self.assertIn("service.name=fixture", observed_env["resource"])
            run_attribute = f"runwitness.run_id={document['run']['run_id']}"
            self.assertEqual(1, observed_env["resource"].split(",").count(run_attribute))

    def test_requested_otel_backend_unavailable_creates_error_run_without_executing_target(self):
        with tempfile.TemporaryDirectory() as temp:
            cwd = Path(temp)
            missing_backend = cwd / "does-not-exist"
            marker = cwd / "target-executed"
            code = "from pathlib import Path; Path('target-executed').write_text('yes')"

            result = self.run_runner(
                cwd,
                [sys.executable, "-c", code],
                backend=missing_backend,
            )

            self.assertEqual(2, result.returncode)
            self.assertFalse(marker.exists(), "target must not execute when required OTEL backend is unavailable")
            _, document, evidence = self.load_single_run(cwd)

            self.assertEqual("error", document["verdict"]["status"])
            self.assertIsNone(document["run"]["process"]["exit_code"])
            self.assertEqual(0, document["summary"]["evidence_count"])
            self.assertEqual([], evidence)

            otel_adapters = [adapter for adapter in document["adapters"] if adapter["name"] == "otel"]
            self.assertEqual(1, len(otel_adapters))
            self.assertIn(otel_adapters[0]["status"], {"unavailable", "error"})

    def test_target_failure_preserves_exit_code_and_otel_evidence(self):
        with tempfile.TemporaryDirectory() as temp:
            cwd = Path(temp)
            backend = self.create_fake_backend(cwd)
            code = "import sys; print('before-failure'); sys.exit(7)"

            result = self.run_runner(
                cwd,
                [sys.executable, "-c", code],
                backend=backend,
            )

            self.assertEqual(1, result.returncode, result.stderr)
            _, document, evidence = self.load_single_run(cwd)
            self.assertEqual(7, document["run"]["process"]["exit_code"])
            self.assertEqual("fail", document["verdict"]["status"])
            self.assertEqual(3, document["summary"]["evidence_count"])
            self.assertEqual(3, len(evidence))
            self.assertEqual("ok", next(a for a in document["adapters"] if a["name"] == "otel")["status"])


if __name__ == "__main__":
    unittest.main(verbosity=2)
