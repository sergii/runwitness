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

count = Integer(ENV.fetch("RW_SQL_COUNT"))
count.times do
  ActiveSupport::Notifications.instrument(
    "sql.active_record",
    sql: "SELECT * FROM orders WHERE id = $1",
    name: "Order Load",
    cached: false,
  )
end

puts "tests-pass"
'''


class RunWitnessQueryRegressionGateV011Contract(unittest.TestCase):
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
            raise AssertionError("Ruby is required by the query regression gate contract")

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
        count: int,
        baseline_run_id: str | None = None,
        fail_on_query_regression: bool = False,
        gate_scope: str | None = None,
    ):
        runs_root = cwd / ".runwitness" / "runs"
        before = set(runs_root.iterdir()) if runs_root.exists() else set()

        env = os.environ.copy()
        env["RW_SQL_COUNT"] = str(count)

        argv = [str(self.runner_bin), "run", "--rails"]
        if baseline_run_id is not None:
            argv.extend(["--baseline", baseline_run_id])
        if gate_scope is not None:
            argv.extend(["--gate-scope", gate_scope])
        if fail_on_query_regression:
            argv.append("--fail-on-query-regression")
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
        run_dir = created[0]

        document = json.loads((run_dir / "run.json").read_text())
        errors = sorted(self.run_validator.iter_errors(document), key=lambda error: list(error.path))
        self.assertEqual([], errors)
        return result, document

    def query_finding(self, document):
        matches = [
            finding
            for finding in document["findings"]
            if finding["kind"] == "database.query_count"
            and finding["rule_id"] == "rails.sql.query_count"
        ]
        self.assertEqual(1, len(matches))
        return matches[0]

    def query_regression_gate(self, document):
        matches = [
            gate
            for gate in document["verdict"]["gates"]
            if gate["rule_id"] == "database.no_query_count_regressions"
        ]
        self.assertEqual(1, len(matches))
        return matches[0]

    def test_opt_in_gate_fails_regressed_query_count_while_target_exits_zero(self):
        with tempfile.TemporaryDirectory() as temp:
            cwd = Path(temp)
            target = self.create_fake_framework(cwd)

            baseline_result, baseline = self.observed_run(cwd, target, count=1)
            self.assertEqual(0, baseline_result.returncode, baseline_result.stderr)

            result, current = self.observed_run(
                cwd,
                target,
                count=3,
                baseline_run_id=baseline["run"]["run_id"],
                fail_on_query_regression=True,
            )

            finding = self.query_finding(current)
            gate = self.query_regression_gate(current)

            self.assertEqual(1, result.returncode, result.stderr)
            self.assertEqual(0, current["run"]["process"]["exit_code"])
            self.assertEqual("fail", current["verdict"]["status"])
            self.assertEqual("warning", finding["severity"])
            self.assertEqual([finding["finding_id"]], current["diff"]["regressed"])
            self.assertEqual("fail", gate["action"])
            self.assertEqual("triggered", gate["outcome"])
            self.assertEqual([finding["finding_id"]], gate["finding_ids"])

    def test_requested_gate_passes_when_query_count_is_unchanged(self):
        with tempfile.TemporaryDirectory() as temp:
            cwd = Path(temp)
            target = self.create_fake_framework(cwd)

            _, baseline = self.observed_run(cwd, target, count=2)
            result, current = self.observed_run(
                cwd,
                target,
                count=2,
                baseline_run_id=baseline["run"]["run_id"],
                fail_on_query_regression=True,
            )

            finding = self.query_finding(current)
            gate = self.query_regression_gate(current)

            self.assertEqual(0, result.returncode, result.stderr)
            self.assertEqual("pass", current["verdict"]["status"])
            self.assertEqual([finding["finding_id"]], current["diff"]["unchanged"])
            self.assertEqual("fail", gate["action"])
            self.assertEqual("passed", gate["outcome"])
            self.assertEqual([], gate["finding_ids"])

    def test_requested_gate_passes_when_query_count_improves(self):
        with tempfile.TemporaryDirectory() as temp:
            cwd = Path(temp)
            target = self.create_fake_framework(cwd)

            _, baseline = self.observed_run(cwd, target, count=3)
            result, current = self.observed_run(
                cwd,
                target,
                count=1,
                baseline_run_id=baseline["run"]["run_id"],
                fail_on_query_regression=True,
            )

            finding = self.query_finding(current)
            gate = self.query_regression_gate(current)

            self.assertEqual(0, result.returncode, result.stderr)
            self.assertEqual("pass", current["verdict"]["status"])
            self.assertEqual([finding["finding_id"]], current["diff"]["improved"])
            self.assertEqual("passed", gate["outcome"])
            self.assertEqual([], gate["finding_ids"])

    def test_without_opt_in_flag_preserves_descriptive_v010_behavior(self):
        with tempfile.TemporaryDirectory() as temp:
            cwd = Path(temp)
            target = self.create_fake_framework(cwd)

            _, baseline = self.observed_run(cwd, target, count=1)
            result, current = self.observed_run(
                cwd,
                target,
                count=3,
                baseline_run_id=baseline["run"]["run_id"],
            )

            finding = self.query_finding(current)
            self.assertEqual(0, result.returncode, result.stderr)
            self.assertEqual("pass", current["verdict"]["status"])
            self.assertEqual([finding["finding_id"]], current["diff"]["regressed"])
            self.assertFalse(
                any(
                    gate["rule_id"] == "database.no_query_count_regressions"
                    for gate in current["verdict"]["gates"]
                )
            )

    def test_gate_is_not_filtered_by_new_finding_scope(self):
        with tempfile.TemporaryDirectory() as temp:
            cwd = Path(temp)
            target = self.create_fake_framework(cwd)

            _, baseline = self.observed_run(cwd, target, count=1)
            result, current = self.observed_run(
                cwd,
                target,
                count=3,
                baseline_run_id=baseline["run"]["run_id"],
                fail_on_query_regression=True,
                gate_scope="new",
            )

            finding = self.query_finding(current)
            gate = self.query_regression_gate(current)
            self.assertEqual([], current["diff"]["new"])
            self.assertEqual([finding["finding_id"]], current["diff"]["regressed"])
            self.assertEqual(1, result.returncode, result.stderr)
            self.assertEqual("triggered", gate["outcome"])
            self.assertEqual([finding["finding_id"]], gate["finding_ids"])

    def test_gate_without_baseline_is_usage_error_before_run_boundary(self):
        with tempfile.TemporaryDirectory() as temp:
            cwd = Path(temp)
            target = self.create_fake_framework(cwd)

            result = subprocess.run(
                [
                    str(self.runner_bin),
                    "run",
                    "--rails",
                    "--fail-on-query-regression",
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
            self.assertIn("baseline", result.stderr.lower())
            self.assertFalse((cwd / ".runwitness").exists())


if __name__ == "__main__":
    unittest.main(verbosity=2)
