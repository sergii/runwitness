#!/usr/bin/env python3

import json
import os
import subprocess
import sys
import tempfile
import time
import unittest
from pathlib import Path


REPO_ROOT = Path(__file__).resolve().parents[2]
PROTOCOL_VERSION = "2025-06-18"


class MCPClient:
    def __init__(self, runner_bin: Path, cwd: Path):
        self.process = subprocess.Popen(
            [str(runner_bin), "mcp"],
            cwd=cwd,
            text=True,
            stdin=subprocess.PIPE,
            stdout=subprocess.PIPE,
            stderr=subprocess.PIPE,
            bufsize=1,
        )
        self.next_id = 1
        try:
            self.initialize()
        except Exception:
            self.close()
            raise

    def send(self, message):
        if self.process.stdin is None:
            raise AssertionError("MCP stdin is unavailable")
        self.process.stdin.write(json.dumps(message, separators=(",", ":")) + "\n")
        self.process.stdin.flush()

    def request(self, method, params=None):
        request_id = self.next_id
        self.next_id += 1
        message = {"jsonrpc": "2.0", "id": request_id, "method": method}
        if params is not None:
            message["params"] = params
        self.send(message)

        if self.process.stdout is None:
            raise AssertionError("MCP stdout is unavailable")

        while True:
            line = self.process.stdout.readline()
            if line == "":
                stderr = ""
                if self.process.stderr is not None:
                    stderr = self.process.stderr.read()
                raise AssertionError(
                    f"MCP server exited before replying to {method!r}; "
                    f"returncode={self.process.poll()} stderr={stderr!r}"
                )
            response = json.loads(line)
            if response.get("id") == request_id:
                return response

    def initialize(self):
        response = self.request(
            "initialize",
            {
                "protocolVersion": PROTOCOL_VERSION,
                "capabilities": {},
                "clientInfo": {"name": "runwitness-acceptance", "version": "0.0.8-contract"},
            },
        )
        if "error" in response:
            raise AssertionError(f"MCP initialize failed: {response['error']!r}")
        self.send({"jsonrpc": "2.0", "method": "notifications/initialized", "params": {}})

    def call_tool(self, name, arguments=None):
        response = self.request(
            "tools/call",
            {"name": name, "arguments": arguments or {}},
        )
        if "error" in response:
            raise AssertionError(f"MCP protocol error calling {name!r}: {response['error']!r}")
        return response["result"]

    def close(self):
        if self.process.stdin is not None and not self.process.stdin.closed:
            self.process.stdin.close()
        try:
            self.process.wait(timeout=3)
        except subprocess.TimeoutExpired:
            self.process.terminate()
            self.process.wait(timeout=3)
        for stream in (self.process.stdout, self.process.stderr):
            if stream is not None and not stream.closed:
                stream.close()


class RunWitnessMCPV008Contract(unittest.TestCase):
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

    def create_run(self, cwd: Path, label: str):
        result = self.invoke(
            cwd,
            "run",
            "--label",
            label,
            "--",
            sys.executable,
            "-c",
            f"print({label!r})",
        )
        self.assertEqual(0, result.returncode, result.stderr)
        runs_root = cwd / ".runwitness" / "runs"
        run_dirs = sorted(path for path in runs_root.iterdir() if path.is_dir())
        documents = [json.loads((path / "run.json").read_text()) for path in run_dirs]
        for document in documents:
            if document["run"].get("label") == label:
                return document
        self.fail(f"could not locate Run with label {label!r}")

    def assert_tool_error(self, result):
        self.assertTrue(result.get("isError"), result)

    def test_empty_store_lists_cleanly_without_creating_runwitness_directory(self):
        with tempfile.TemporaryDirectory() as temp:
            cwd = Path(temp)
            client = MCPClient(self.runner_bin, cwd)
            try:
                result = client.call_tool("list_runs")
                self.assertFalse(result.get("isError", False), result)
                self.assertEqual({"runs": []}, result.get("structuredContent"))
                self.assertFalse((cwd / ".runwitness").exists())
            finally:
                client.close()

    def test_tools_list_preserves_v008_read_surface(self):
        with tempfile.TemporaryDirectory() as temp:
            cwd = Path(temp)
            client = MCPClient(self.runner_bin, cwd)
            try:
                response = client.request("tools/list", {})
                self.assertNotIn("error", response)
                tools = response["result"]["tools"]
                names = {tool["name"] for tool in tools}
                self.assertTrue({"list_runs", "get_run"}.issubset(names), names)
            finally:
                client.close()

    def test_list_runs_returns_newest_first_stable_summaries(self):
        with tempfile.TemporaryDirectory() as temp:
            cwd = Path(temp)
            older = self.create_run(cwd, "older")
            time.sleep(0.01)
            newer = self.create_run(cwd, "newer")

            client = MCPClient(self.runner_bin, cwd)
            try:
                result = client.call_tool("list_runs", {"limit": 20})
                self.assertFalse(result.get("isError", False), result)
                runs = result["structuredContent"]["runs"]
                self.assertEqual(
                    [newer["run"]["run_id"], older["run"]["run_id"]],
                    [item["run_id"] for item in runs],
                )
                self.assertEqual(["newer", "older"], [item.get("label") for item in runs])
                for item in runs:
                    self.assertEqual(
                        {"run_id", "started_at", "label", "command", "verdict", "summary"},
                        set(item),
                    )
                    self.assertIsInstance(item["command"]["argv"], list)
                    self.assertIn(item["verdict"]["status"], {"pass", "warn", "fail", "error"})
                    self.assertIsInstance(item["summary"]["evidence_count"], int)
                    self.assertIsInstance(item["summary"]["finding_count"], int)
            finally:
                client.close()

    def test_get_run_returns_exact_canonical_document(self):
        with tempfile.TemporaryDirectory() as temp:
            cwd = Path(temp)
            expected = self.create_run(cwd, "inspect-me")
            run_id = expected["run"]["run_id"]

            client = MCPClient(self.runner_bin, cwd)
            try:
                result = client.call_tool("get_run", {"run_id": run_id})
                self.assertFalse(result.get("isError", False), result)
                self.assertEqual(expected, result["structuredContent"]["run"])
            finally:
                client.close()

    def test_tool_validation_errors_do_not_terminate_server(self):
        with tempfile.TemporaryDirectory() as temp:
            cwd = Path(temp)
            expected = self.create_run(cwd, "still-readable")
            run_id = expected["run"]["run_id"]

            client = MCPClient(self.runner_bin, cwd)
            try:
                self.assert_tool_error(client.call_tool("list_runs", {"limit": 0}))
                self.assert_tool_error(client.call_tool("list_runs", {"limit": 101}))
                self.assert_tool_error(client.call_tool("get_run", {"run_id": "../../etc/passwd"}))
                self.assert_tool_error(
                    client.call_tool(
                        "get_run",
                        {"run_id": "0198f5f0-0000-7000-8000-000000000001"},
                    )
                )

                result = client.call_tool("get_run", {"run_id": run_id})
                self.assertFalse(result.get("isError", False), result)
                self.assertEqual(expected, result["structuredContent"]["run"])
            finally:
                client.close()

    def test_malformed_run_is_not_silently_omitted_by_list_runs(self):
        with tempfile.TemporaryDirectory() as temp:
            cwd = Path(temp)
            expected = self.create_run(cwd, "good")
            malformed_id = "0198f5f0-0000-7000-8000-000000000002"
            malformed_dir = cwd / ".runwitness" / "runs" / malformed_id
            malformed_dir.mkdir(parents=True)
            (malformed_dir / "run.json").write_text("{not-json\n")

            client = MCPClient(self.runner_bin, cwd)
            try:
                self.assert_tool_error(client.call_tool("list_runs"))

                # A data error in one tool call must not kill the local MCP server.
                run_id = expected["run"]["run_id"]
                result = client.call_tool("get_run", {"run_id": run_id})
                self.assertFalse(result.get("isError", False), result)
                self.assertEqual(expected, result["structuredContent"]["run"])
            finally:
                client.close()


if __name__ == "__main__":
    unittest.main(verbosity=2)
