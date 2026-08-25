#!/usr/bin/env python3

import json
import os
import subprocess
import sys
import tempfile
import unittest
from pathlib import Path

from test_mcp_v008 import MCPClient


REPO_ROOT = Path(__file__).resolve().parents[2]


class RunWitnessMCPEvidenceV009Contract(unittest.TestCase):
    @classmethod
    def setUpClass(cls):
        configured = os.environ.get("RUNWITNESS_BIN")
        cls.runner_bin = Path(configured) if configured else REPO_ROOT / "bin" / "runwitness"
        if not cls.runner_bin.is_absolute():
            cls.runner_bin = (REPO_ROOT / cls.runner_bin).resolve()
        if not cls.runner_bin.exists():
            raise AssertionError(f"RunWitness binary does not exist at {cls.runner_bin}")

    def invoke(self, cwd: Path, *args: str):
        return subprocess.run(
            [str(self.runner_bin), *args],
            cwd=cwd,
            text=True,
            stdout=subprocess.PIPE,
            stderr=subprocess.PIPE,
            check=False,
        )

    def create_run(self, cwd: Path, label: str = "evidence"):
        result = self.invoke(
            cwd,
            "run",
            "--label",
            label,
            "--",
            sys.executable,
            "-c",
            "print('ok')",
        )
        self.assertEqual(0, result.returncode, result.stderr)
        runs_root = cwd / ".runwitness" / "runs"
        run_dir = next(path for path in runs_root.iterdir() if path.is_dir())
        document = json.loads((run_dir / "run.json").read_text())
        return run_dir, document

    def evidence_record(self, run_id: str, evidence_id: str = "ev_contract_001"):
        return {
            "schema_version": 1,
            "evidence_id": evidence_id,
            "run_id": run_id,
            "source": "otel",
            "kind": "otel.span",
            "observed_at": "2026-08-25T12:00:00Z",
            "attributes": {
                "service.name": "checkout",
                "http.response.status_code": 500,
            },
            "payload": {
                "name": "checkout.capture",
                "status": "ERROR",
            },
        }

    def install_evidence(self, run_dir: Path, document, records, finding_ref=None):
        payload = "".join(json.dumps(record, separators=(",", ":")) + "\n" for record in records)
        (run_dir / "evidence.jsonl").write_text(payload)
        document["summary"]["evidence_count"] = len(records)
        if finding_ref is not None:
            document["findings"] = [
                {
                    "finding_id": "rwf_contract",
                    "kind": "runtime.error",
                    "rule_id": "otel.span.error",
                    "severity": "error",
                    "evidence_refs": [finding_ref],
                }
            ]
            document["summary"]["finding_count"] = 1
        (run_dir / "run.json").write_text(json.dumps(document, indent=2) + "\n")

    def assert_tool_error(self, result):
        self.assertTrue(result.get("isError"), result)

    def test_tools_list_exposes_exact_v009_read_surface(self):
        with tempfile.TemporaryDirectory() as temp:
            client = MCPClient(self.runner_bin, Path(temp))
            try:
                response = client.request("tools/list", {})
                self.assertNotIn("error", response)
                names = {tool["name"] for tool in response["result"]["tools"]}
                self.assertEqual({"list_runs", "get_run", "get_evidence"}, names)
            finally:
                client.close()

    def test_get_evidence_dereferences_finding_to_exact_canonical_record(self):
        with tempfile.TemporaryDirectory() as temp:
            cwd = Path(temp)
            run_dir, document = self.create_run(cwd)
            run_id = document["run"]["run_id"]
            expected = self.evidence_record(run_id)
            self.install_evidence(run_dir, document, [expected], finding_ref=expected["evidence_id"])
            evidence_path = run_dir / "evidence.jsonl"
            evidence_before = evidence_path.read_bytes()

            client = MCPClient(self.runner_bin, cwd)
            try:
                run_result = client.call_tool("get_run", {"run_id": run_id})
                self.assertFalse(run_result.get("isError", False), run_result)
                evidence_id = run_result["structuredContent"]["run"]["findings"][0]["evidence_refs"][0]

                result = client.call_tool(
                    "get_evidence",
                    {"run_id": run_id, "evidence_id": evidence_id},
                )
                self.assertFalse(result.get("isError", False), result)
                self.assertEqual(expected, result["structuredContent"]["evidence"])
                self.assertEqual(evidence_before, evidence_path.read_bytes())
            finally:
                client.close()

    def test_validation_and_missing_evidence_errors_do_not_terminate_server(self):
        with tempfile.TemporaryDirectory() as temp:
            cwd = Path(temp)
            run_dir, document = self.create_run(cwd)
            run_id = document["run"]["run_id"]
            expected = self.evidence_record(run_id)
            self.install_evidence(run_dir, document, [expected])

            client = MCPClient(self.runner_bin, cwd)
            try:
                self.assert_tool_error(
                    client.call_tool(
                        "get_evidence",
                        {"run_id": "../../etc/passwd", "evidence_id": expected["evidence_id"]},
                    )
                )
                self.assert_tool_error(
                    client.call_tool(
                        "get_evidence",
                        {"run_id": run_id, "evidence_id": "../../secret"},
                    )
                )
                self.assert_tool_error(
                    client.call_tool(
                        "get_evidence",
                        {"run_id": run_id, "evidence_id": "ev_missing"},
                    )
                )

                result = client.call_tool(
                    "get_evidence",
                    {"run_id": run_id, "evidence_id": expected["evidence_id"]},
                )
                self.assertFalse(result.get("isError", False), result)
                self.assertEqual(expected, result["structuredContent"]["evidence"])
            finally:
                client.close()

    def test_malformed_and_cross_run_evidence_are_tool_errors_without_server_exit(self):
        with tempfile.TemporaryDirectory() as temp:
            cwd = Path(temp)
            run_dir, document = self.create_run(cwd)
            run_id = document["run"]["run_id"]
            expected = self.evidence_record(run_id)
            evidence_path = run_dir / "evidence.jsonl"

            client = MCPClient(self.runner_bin, cwd)
            try:
                evidence_path.write_text("{not-json\n")
                self.assert_tool_error(
                    client.call_tool(
                        "get_evidence",
                        {"run_id": run_id, "evidence_id": expected["evidence_id"]},
                    )
                )

                cross_run = self.evidence_record(
                    "0198f5f0-0000-7000-8000-000000000099",
                    expected["evidence_id"],
                )
                self.install_evidence(run_dir, document, [cross_run])
                self.assert_tool_error(
                    client.call_tool(
                        "get_evidence",
                        {"run_id": run_id, "evidence_id": expected["evidence_id"]},
                    )
                )

                self.install_evidence(run_dir, document, [expected])
                result = client.call_tool(
                    "get_evidence",
                    {"run_id": run_id, "evidence_id": expected["evidence_id"]},
                )
                self.assertFalse(result.get("isError", False), result)
            finally:
                client.close()

    def test_duplicate_evidence_ids_are_rejected_as_ambiguous(self):
        with tempfile.TemporaryDirectory() as temp:
            cwd = Path(temp)
            run_dir, document = self.create_run(cwd)
            run_id = document["run"]["run_id"]
            first = self.evidence_record(run_id, "ev_duplicate")
            second = self.evidence_record(run_id, "ev_duplicate")
            second["payload"] = {"name": "different", "status": "ERROR"}
            self.install_evidence(run_dir, document, [first, second])

            client = MCPClient(self.runner_bin, cwd)
            try:
                self.assert_tool_error(
                    client.call_tool(
                        "get_evidence",
                        {"run_id": run_id, "evidence_id": "ev_duplicate"},
                    )
                )

                self.install_evidence(run_dir, document, [first])
                result = client.call_tool(
                    "get_evidence",
                    {"run_id": run_id, "evidence_id": "ev_duplicate"},
                )
                self.assertFalse(result.get("isError", False), result)
                self.assertEqual(first, result["structuredContent"]["evidence"])
            finally:
                client.close()


if __name__ == "__main__":
    unittest.main(verbosity=2)
