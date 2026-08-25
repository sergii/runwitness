#!/usr/bin/env python3

import json
import os
import subprocess
import sys
import tempfile
import unittest
from pathlib import Path

from jsonschema import Draft202012Validator, FormatChecker


REPO_ROOT = Path(__file__).resolve().parents[2]
SCHEMA_PATH = REPO_ROOT / "schemas" / "run-v1.schema.json"


class RunWitnessCLIV001Contract(unittest.TestCase):
    @classmethod
    def setUpClass(cls):
        configured = os.environ.get("RUNWITNESS_BIN")
        cls.runner_bin = Path(configured) if configured else REPO_ROOT / "bin" / "runwitness"
        if not cls.runner_bin.is_absolute():
            cls.runner_bin = (REPO_ROOT / cls.runner_bin).resolve()
        if not cls.runner_bin.exists():
            raise AssertionError(f"RunWitness binary does not exist at {cls.runner_bin}")

        schema = json.loads(SCHEMA_PATH.read_text())
        cls.validator = Draft202012Validator(schema, format_checker=FormatChecker())

    def invoke(self, cwd: Path, *args: str) -> subprocess.CompletedProcess:
        return subprocess.run(
            [str(self.runner_bin), *args],
            cwd=cwd,
            text=True,
            stdout=subprocess.PIPE,
            stderr=subprocess.PIPE,
            check=False,
        )

    def load_single_run(self, cwd: Path):
        runs_root = cwd / ".runwitness" / "runs"
        self.assertTrue(runs_root.is_dir())
        run_dirs = [path for path in runs_root.iterdir() if path.is_dir()]
        self.assertEqual(1, len(run_dirs))
        document = json.loads((run_dirs[0] / "run.json").read_text())
        errors = sorted(self.validator.iter_errors(document), key=lambda error: list(error.path))
        self.assertEqual([], errors)
        return run_dirs[0], document

    def test_version_is_stable_and_does_not_create_run(self):
        with tempfile.TemporaryDirectory() as temp:
            cwd = Path(temp)
            result = self.invoke(cwd, "--version")

            self.assertEqual(0, result.returncode, result.stderr)
            self.assertEqual("RunWitness v0.0.11\n", result.stdout)
            self.assertEqual("", result.stderr)
            self.assertFalse((cwd / ".runwitness").exists())

    def test_label_is_recorded_and_not_forwarded_to_target(self):
        with tempfile.TemporaryDirectory() as temp:
            cwd = Path(temp)
            target = [sys.executable, "-c", "import sys; print(len(sys.argv))"]
            result = self.invoke(cwd, "run", "--label", "smoke", "--", *target)

            self.assertEqual(0, result.returncode, result.stderr)
            run_dir, document = self.load_single_run(cwd)
            self.assertEqual("smoke", document["run"]["label"])
            self.assertEqual(target, document["run"]["command"]["argv"])
            self.assertEqual("1\n", (run_dir / "stdout.log").read_text())

    def test_unknown_option_is_usage_error_before_run_boundary(self):
        with tempfile.TemporaryDirectory() as temp:
            cwd = Path(temp)
            result = self.invoke(cwd, "run", "--wat", "--", sys.executable, "-c", "print('no')")

            self.assertEqual(2, result.returncode)
            self.assertIn("unknown option", result.stderr.lower())
            self.assertFalse((cwd / ".runwitness").exists())

    def test_missing_target_command_is_recorded_as_runner_error(self):
        with tempfile.TemporaryDirectory() as temp:
            cwd = Path(temp)
            missing = "runwitness-command-that-does-not-exist-7d2497"
            result = self.invoke(cwd, "run", "--", missing)

            self.assertEqual(2, result.returncode)
            _, document = self.load_single_run(cwd)
            self.assertEqual([missing], document["run"]["command"]["argv"])
            self.assertIsNone(document["run"]["process"].get("exit_code"))
            self.assertEqual("error", document["verdict"]["status"])
            self.assertTrue(document["verdict"].get("message"))

    def test_empty_label_is_rejected_before_run_boundary(self):
        with tempfile.TemporaryDirectory() as temp:
            cwd = Path(temp)
            result = self.invoke(cwd, "run", "--label", "", "--", sys.executable, "-c", "print('no')")

            self.assertEqual(2, result.returncode)
            self.assertIn("label", result.stderr.lower())
            self.assertFalse((cwd / ".runwitness").exists())


if __name__ == "__main__":
    unittest.main(verbosity=2)
