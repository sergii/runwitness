#!/usr/bin/env python3

import json
import os
import subprocess
import sys
import tempfile
import unittest
import uuid
from datetime import datetime
from pathlib import Path

from jsonschema import Draft202012Validator, FormatChecker


REPO_ROOT = Path(__file__).resolve().parents[2]
SCHEMA_PATH = REPO_ROOT / "schemas" / "run-v1.schema.json"


def parse_datetime(value: str) -> datetime:
    return datetime.fromisoformat(value.replace("Z", "+00:00"))


class RunWitnessV001Contract(unittest.TestCase):
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

        schema = json.loads(SCHEMA_PATH.read_text())
        cls.validator = Draft202012Validator(schema, format_checker=FormatChecker())

    def run_runner(self, cwd: Path, target_argv: list[str]) -> subprocess.CompletedProcess:
        return subprocess.run(
            [str(self.runner_bin), "run", "--", *target_argv],
            cwd=cwd,
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
        run_json_path = run_dir / "run.json"
        self.assertTrue(run_json_path.is_file(), "run.json must exist")
        self.assertTrue((run_dir / "evidence.jsonl").is_file(), "evidence.jsonl must exist")
        self.assertTrue((run_dir / "stdout.log").is_file(), "stdout.log must exist")
        self.assertTrue((run_dir / "stderr.log").is_file(), "stderr.log must exist")

        document = json.loads(run_json_path.read_text())
        errors = sorted(self.validator.iter_errors(document), key=lambda error: list(error.path))
        self.assertEqual([], errors, "run.json must validate against schemas/run-v1.schema.json")

        self.assertEqual(document["run"]["run_id"], run_dir.name)
        parsed_id = uuid.UUID(document["run"]["run_id"])
        self.assertEqual(7, parsed_id.version, "run_id must be UUIDv7")

        started_at = parse_datetime(document["run"]["started_at"])
        finished_at = parse_datetime(document["run"]["finished_at"])
        self.assertGreaterEqual(finished_at, started_at)
        self.assertGreaterEqual(document["run"]["duration_ms"], 0)

        return run_dir, document

    def test_successful_command_creates_schema_valid_run_and_preserves_argv(self):
        with tempfile.TemporaryDirectory() as temp:
            cwd = Path(temp)
            expected_args = ["alpha beta", "--flag=x=y", ""]
            code = "import json, sys; print(json.dumps(sys.argv[1:]))"
            target = [sys.executable, "-c", code, *expected_args]

            result = self.run_runner(cwd, target)

            self.assertEqual(0, result.returncode, result.stderr)
            run_dir, document = self.load_single_run(cwd)
            self.assertEqual(target, document["run"]["command"]["argv"])
            self.assertEqual(0, document["run"]["process"]["exit_code"])
            self.assertEqual("pass", document["verdict"]["status"])
            self.assertEqual(expected_args, json.loads((run_dir / "stdout.log").read_text()))
            self.assertEqual("", (run_dir / "stderr.log").read_text())

    def test_stdout_and_stderr_are_captured_separately(self):
        with tempfile.TemporaryDirectory() as temp:
            cwd = Path(temp)
            code = (
                "import sys; "
                "print('hello-out'); "
                "print('hello-err', file=sys.stderr)"
            )

            result = self.run_runner(cwd, [sys.executable, "-c", code])

            self.assertEqual(0, result.returncode, result.stderr)
            run_dir, _ = self.load_single_run(cwd)
            self.assertEqual("hello-out\n", (run_dir / "stdout.log").read_text())
            self.assertEqual("hello-err\n", (run_dir / "stderr.log").read_text())

    def test_run_id_is_propagated_to_target_process(self):
        with tempfile.TemporaryDirectory() as temp:
            cwd = Path(temp)
            code = "import os; print(os.environ['RUNWITNESS_RUN_ID'])"

            result = self.run_runner(cwd, [sys.executable, "-c", code])

            self.assertEqual(0, result.returncode, result.stderr)
            run_dir, document = self.load_single_run(cwd)
            observed_run_id = (run_dir / "stdout.log").read_text().strip()
            self.assertEqual(document["run"]["run_id"], observed_run_id)

    def test_failing_target_preserves_target_exit_code_and_returns_runner_fail_code(self):
        with tempfile.TemporaryDirectory() as temp:
            cwd = Path(temp)
            code = (
                "import sys; "
                "print('before-failure'); "
                "print('failure-detail', file=sys.stderr); "
                "sys.exit(7)"
            )

            result = self.run_runner(cwd, [sys.executable, "-c", code])

            self.assertEqual(1, result.returncode, "RunWitness CLI exit code for verdict=fail must be 1")
            run_dir, document = self.load_single_run(cwd)
            self.assertEqual(7, document["run"]["process"]["exit_code"])
            self.assertEqual("fail", document["verdict"]["status"])
            self.assertEqual("before-failure\n", (run_dir / "stdout.log").read_text())
            self.assertEqual("failure-detail\n", (run_dir / "stderr.log").read_text())

    def test_non_git_working_directory_is_a_valid_run(self):
        with tempfile.TemporaryDirectory() as temp:
            cwd = Path(temp)

            result = self.run_runner(cwd, [sys.executable, "-c", "print('ok')"])

            self.assertEqual(0, result.returncode, result.stderr)
            _, document = self.load_single_run(cwd)
            self.assertNotIn("git", document["run"])
            self.assertNotIn("repository", document["run"])

    def test_git_state_is_captured_before_and_after_target_execution(self):
        with tempfile.TemporaryDirectory() as temp:
            cwd = Path(temp)
            subprocess.run(["git", "init"], cwd=cwd, check=True, stdout=subprocess.PIPE, stderr=subprocess.PIPE)
            subprocess.run(["git", "config", "user.email", "contract@example.test"], cwd=cwd, check=True)
            subprocess.run(["git", "config", "user.name", "RunWitness Contract"], cwd=cwd, check=True)

            tracked = cwd / "tracked.txt"
            tracked.write_text("committed\n")
            subprocess.run(["git", "add", "tracked.txt"], cwd=cwd, check=True)
            subprocess.run(["git", "commit", "-m", "fixture"], cwd=cwd, check=True, stdout=subprocess.PIPE, stderr=subprocess.PIPE)

            head = subprocess.run(
                ["git", "rev-parse", "HEAD"],
                cwd=cwd,
                check=True,
                text=True,
                stdout=subprocess.PIPE,
            ).stdout.strip()

            tracked.write_text("dirty-before\n")
            code = "from pathlib import Path; Path('tracked.txt').write_text('dirty-after\\n')"

            result = self.run_runner(cwd, [sys.executable, "-c", code])

            self.assertEqual(0, result.returncode, result.stderr)
            _, document = self.load_single_run(cwd)
            self.assertEqual(str(cwd.resolve()), document["run"]["repository"]["root"])

            git = document["run"]["git"]
            self.assertEqual(head, git["before"]["head_sha"])
            self.assertEqual(head, git["after"]["head_sha"])
            self.assertTrue(git["before"]["dirty"])
            self.assertTrue(git["after"]["dirty"])
            self.assertIsNotNone(git["before"].get("diff_hash"))
            self.assertIsNotNone(git["after"].get("diff_hash"))
            self.assertNotEqual(git["before"]["diff_hash"], git["after"]["diff_hash"])


if __name__ == "__main__":
    unittest.main(verbosity=2)
