#!/usr/bin/env python3

import json
import os
import shutil
import subprocess
import tempfile
import textwrap
import unittest
from pathlib import Path

from jsonschema import Draft202012Validator, FormatChecker


REPO_ROOT = Path(__file__).resolve().parents[2]
RUN_SCHEMA_PATH = REPO_ROOT / "schemas" / "run-v1.schema.json"


FAKE_RAILS = r'''
class RunWitnessContractErrorReporter
  def initialize
    @subscribers = []
  end

  def subscribe(subscriber)
    @subscribers << subscriber
  end
end

module Rails
  def self.error
    @error ||= RunWitnessContractErrorReporter.new
  end
end
'''


FAKE_NOTIFICATIONS = r'''
module ActiveSupport
  module Notifications
    @subscribers = Hash.new { |hash, key| hash[key] = [] }

    class << self
      def subscribe(name, &block)
        @subscribers[name] << block
        block
      end

      def instrument(name, payload = {})
        started = Time.now
        result = block_given? ? yield : nil
        finished = Time.now
        @subscribers[name].each do |subscriber|
          subscriber.call(name, started, finished, "fixture-event", payload)
        end
        result
      end
    end
  end
end
'''


SQL_TARGET = r'''
require "rails"
require "active_support/notifications"

queries = [
  ["SELECT * FROM orders WHERE id = $1", Integer(ENV.fetch("RW_SQL_A", "0"))],
  ["SELECT * FROM users WHERE id = $1", Integer(ENV.fetch("RW_SQL_B", "0"))],
]

queries.each do |sql, count|
  count.times do
    ActiveSupport::Notifications.instrument(
      "sql.active_record",
      sql: sql,
      name: "Record Load",
      cached: false,
    )
  end
end

puts "tests-pass"
'''


class RunWitnessQueryCountToleranceV012Contract(unittest.TestCase):
    @classmethod
    def setUpClass(cls):
        configured = os.environ.get("RUNWITNESS_BIN")
        cls.runner_bin = Path(configured) if configured else REPO_ROOT / "bin" / "runwitness"
        if not cls.runner_bin.is_absolute():
            cls.runner_bin = (REPO_ROOT / cls.runner_bin).resolve()
        if not cls.runner_bin.exists():
            raise AssertionError(f"RunWitness binary does not exist at {cls.runner_bin}")

        cls.ruby = shutil.which("ruby")
        if cls.ruby is None:
            raise AssertionError("Ruby is required by the query count tolerance contract")

        cls.run_validator = Draft202012Validator(
            json.loads(RUN_SCHEMA_PATH.read_text()),
            format_checker=FormatChecker(),
        )

    def create_fake_framework(self, cwd: Path) -> Path:
        (cwd / "rails.rb").write_text(textwrap.dedent(FAKE_RAILS))
        notifications_dir = cwd / "active_support"
        notifications_dir.mkdir()
        (notifications_dir / "notifications.rb").write_text(textwrap.dedent(FAKE_NOTIFICATIONS))
        target = cwd / "sql_target.rb"
        target.write_text(textwrap.dedent(SQL_TARGET))
        return target

    def observed_run(
        self,
        cwd: Path,
        target: Path,
        *,
        count_a: int,
        count_b: int = 0,
        baseline_run_id: str | None = None,
        fail_on_query_regression: bool = False,
        tolerance: int | None = None,
        gate_scope: str | None = None,
    ):
        runs_root = cwd / ".runwitness" / "runs"
        before = set(runs_root.iterdir()) if runs_root.exists() else set()

        env = os.environ.copy()
        env["RW_SQL_A"] = str(count_a)
        env["RW_SQL_B"] = str(count_b)

        argv = [str(self.runner_bin), "run", "--rails"]
        if baseline_run_id is not None:
            argv.extend(["--baseline", baseline_run_id])
        if gate_scope is not None:
            argv.extend(["--gate-scope", gate_scope])
        if fail_on_query_regression:
            argv.append("--fail-on-query-regression")
        if tolerance is not None:
            argv.extend(["--query-count-tolerance", str(tolerance)])
        argv.extend(["--", self.ruby, "-I", str(cwd), str(target)])

        result = subprocess.run(
            argv,
            cwd=cwd,
            env=env,
            text=True,
            stdout=subprocess.PIPE,
            stderr=subprocess.PIPE,
            check=False,
        )

        self.assertTrue(runs_root.is_dir(), result.stderr)
        after = set(runs_root.iterdir())
        created = [path for path in after - before if path.is_dir()]
        self.assertEqual(1, len(created), result.stderr)
        document = json.loads((created[0] / "run.json").read_text())
        errors = sorted(self.run_validator.iter_errors(document), key=lambda error: list(error.path))
        self.assertEqual([], errors)
        return result, document

    def query_findings(self, document):
        return sorted(
            [
                finding
                for finding in document["findings"]
                if finding["kind"] == "database.query_count"
                and finding["rule_id"] == "rails.sql.query_count"
            ],
            key=lambda finding: finding["finding_id"],
        )

    def query_regression_gate(self, document):
        matches = [
            gate
            for gate in document["verdict"]["gates"]
            if gate["rule_id"] == "database.no_query_count_regressions"
        ]
        self.assertEqual(1, len(matches))
        return matches[0]

    def test_delta_equal_to_tolerance_remains_regressed_but_gate_passes(self):
        with tempfile.TemporaryDirectory() as temp:
            cwd = Path(temp)
            target = self.create_fake_framework(cwd)

            _, baseline = self.observed_run(cwd, target, count_a=5)
            result, current = self.observed_run(
                cwd,
                target,
                count_a=6,
                baseline_run_id=baseline["run"]["run_id"],
                fail_on_query_regression=True,
                tolerance=1,
            )

            finding = self.query_findings(current)[0]
            gate = self.query_regression_gate(current)

            self.assertEqual(0, result.returncode, result.stderr)
            self.assertEqual("pass", current["verdict"]["status"])
            self.assertEqual("warning", finding["severity"])
            self.assertEqual(1, finding["comparison"]["delta"])
            self.assertEqual([finding["finding_id"]], current["diff"]["regressed"])
            self.assertEqual("passed", gate["outcome"])
            self.assertEqual([], gate["finding_ids"])
            self.assertEqual({"max_delta": 1, "unit": "queries"}, gate["parameters"])

    def test_delta_above_tolerance_triggers_gate(self):
        with tempfile.TemporaryDirectory() as temp:
            cwd = Path(temp)
            target = self.create_fake_framework(cwd)

            _, baseline = self.observed_run(cwd, target, count_a=5)
            result, current = self.observed_run(
                cwd,
                target,
                count_a=7,
                baseline_run_id=baseline["run"]["run_id"],
                fail_on_query_regression=True,
                tolerance=1,
            )

            finding = self.query_findings(current)[0]
            gate = self.query_regression_gate(current)

            self.assertEqual(1, result.returncode, result.stderr)
            self.assertEqual(0, current["run"]["process"]["exit_code"])
            self.assertEqual("fail", current["verdict"]["status"])
            self.assertEqual(2, finding["comparison"]["delta"])
            self.assertEqual("triggered", gate["outcome"])
            self.assertEqual([finding["finding_id"]], gate["finding_ids"])
            self.assertEqual({"max_delta": 1, "unit": "queries"}, gate["parameters"])

    def test_tolerance_filters_each_regressed_query_finding_independently(self):
        with tempfile.TemporaryDirectory() as temp:
            cwd = Path(temp)
            target = self.create_fake_framework(cwd)

            _, baseline = self.observed_run(cwd, target, count_a=5, count_b=5)
            result, current = self.observed_run(
                cwd,
                target,
                count_a=6,
                count_b=8,
                baseline_run_id=baseline["run"]["run_id"],
                fail_on_query_regression=True,
                tolerance=1,
            )

            findings = self.query_findings(current)
            self.assertEqual(2, len(findings))
            deltas = {finding["finding_id"]: finding["comparison"]["delta"] for finding in findings}
            self.assertEqual([finding["finding_id"] for finding in findings], sorted(current["diff"]["regressed"]))

            gate = self.query_regression_gate(current)
            expected = sorted(finding_id for finding_id, delta in deltas.items() if delta > 1)
            self.assertEqual(1, result.returncode, result.stderr)
            self.assertEqual(expected, gate["finding_ids"])
            self.assertEqual(1, len(gate["finding_ids"]))

    def test_tolerance_does_not_get_filtered_by_new_scope(self):
        with tempfile.TemporaryDirectory() as temp:
            cwd = Path(temp)
            target = self.create_fake_framework(cwd)

            _, baseline = self.observed_run(cwd, target, count_a=5)
            result, current = self.observed_run(
                cwd,
                target,
                count_a=7,
                baseline_run_id=baseline["run"]["run_id"],
                fail_on_query_regression=True,
                tolerance=1,
                gate_scope="new",
            )

            finding = self.query_findings(current)[0]
            gate = self.query_regression_gate(current)
            self.assertEqual([], current["diff"]["new"])
            self.assertEqual([finding["finding_id"]], current["diff"]["regressed"])
            self.assertEqual(1, result.returncode, result.stderr)
            self.assertEqual("triggered", gate["outcome"])

    def test_tolerance_requires_query_regression_gate_before_run_boundary(self):
        with tempfile.TemporaryDirectory() as temp:
            cwd = Path(temp)
            target = self.create_fake_framework(cwd)

            result = subprocess.run(
                [
                    str(self.runner_bin),
                    "run",
                    "--rails",
                    "--baseline",
                    "0198f5f0-0000-7000-8000-000000000001",
                    "--query-count-tolerance",
                    "1",
                    "--",
                    self.ruby,
                    "-I",
                    str(cwd),
                    str(target),
                ],
                cwd=cwd,
                text=True,
                stdout=subprocess.PIPE,
                stderr=subprocess.PIPE,
                check=False,
            )

            self.assertEqual(2, result.returncode)
            self.assertIn("fail-on-query-regression", result.stderr.lower())
            self.assertFalse((cwd / ".runwitness").exists())

    def test_invalid_and_duplicate_tolerance_are_usage_errors(self):
        invalid_argv = [
            ["--query-count-tolerance", "-1"],
            ["--query-count-tolerance", "1.5"],
            ["--query-count-tolerance", "wat"],
            ["--query-count-tolerance", ""],
            ["--query-count-tolerance", "1", "--query-count-tolerance", "2"],
        ]

        for arguments in invalid_argv:
            with self.subTest(arguments=arguments), tempfile.TemporaryDirectory() as temp:
                cwd = Path(temp)
                target = self.create_fake_framework(cwd)
                result = subprocess.run(
                    [
                        str(self.runner_bin),
                        "run",
                        "--rails",
                        "--baseline",
                        "0198f5f0-0000-7000-8000-000000000001",
                        "--fail-on-query-regression",
                        *arguments,
                        "--",
                        self.ruby,
                        "-I",
                        str(cwd),
                        str(target),
                    ],
                    cwd=cwd,
                    text=True,
                    stdout=subprocess.PIPE,
                    stderr=subprocess.PIPE,
                    check=False,
                )

                self.assertEqual(2, result.returncode)
                self.assertIn("tolerance", result.stderr.lower())
                self.assertFalse((cwd / ".runwitness").exists())


if __name__ == "__main__":
    unittest.main(verbosity=2)
