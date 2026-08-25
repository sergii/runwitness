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
EVIDENCE_SCHEMA_PATH = REPO_ROOT / "schemas" / "evidence-v1.schema.json"


FAKE_RAILS = r'''
class RunWitnessContractErrorReporter
  def initialize
    @subscribers = []
  end

  def subscribe(subscriber)
    @subscribers << subscriber
  end

  def report(error, handled: true, severity: :warning, context: {}, source: "application")
    @subscribers.each do |subscriber|
      subscriber.report(
        error,
        handled: handled,
        severity: severity,
        context: context,
        source: source,
      )
    end
    nil
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

count = Integer(ENV.fetch("RW_SQL_COUNT", "1"))
statement_a = ENV.fetch("RW_SQL_A", "SELECT * FROM orders WHERE id = $1")
statement_b = ENV.fetch("RW_SQL_B", statement_a)

count.times do |index|
  sql = index.odd? ? statement_b : statement_a
  ActiveSupport::Notifications.instrument(
    "sql.active_record",
    sql: sql,
    name: "Order Load",
    cached: false,
  )
end

if ENV["RW_SQL_NOISE"] == "1"
  ActiveSupport::Notifications.instrument(
    "sql.active_record",
    sql: "SELECT * FROM cached_orders",
    name: "Order Load",
    cached: true,
  )
  ActiveSupport::Notifications.instrument(
    "sql.active_record",
    sql: "SELECT name FROM sqlite_master",
    name: "SCHEMA",
    cached: false,
  )
  ActiveSupport::Notifications.instrument(
    "sql.active_record",
    sql: "BEGIN",
    name: "transaction",
    cached: false,
  )
  ActiveSupport::Notifications.instrument(
    "sql.active_record",
    sql: "   \n\t  ",
    name: "Order Load",
    cached: false,
  )
end

puts "tests-pass"
'''


class RunWitnessRailsSQLV010Contract(unittest.TestCase):
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
            raise AssertionError("Ruby is required by the Rails SQL acceptance contract")

        cls.run_validator = Draft202012Validator(
            json.loads(RUN_SCHEMA_PATH.read_text()),
            format_checker=FormatChecker(),
        )
        cls.evidence_validator = Draft202012Validator(
            json.loads(EVIDENCE_SCHEMA_PATH.read_text()),
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
        statement_a: str = "SELECT * FROM orders WHERE id = $1",
        statement_b: str | None = None,
        noise: bool = False,
        baseline_run_id: str | None = None,
    ):
        runs_root = cwd / ".runwitness" / "runs"
        before = set(runs_root.iterdir()) if runs_root.exists() else set()

        env = os.environ.copy()
        env["RW_SQL_COUNT"] = str(count)
        env["RW_SQL_A"] = statement_a
        env["RW_SQL_B"] = statement_b if statement_b is not None else statement_a
        env["RW_SQL_NOISE"] = "1" if noise else "0"

        argv = [str(self.runner_bin), "run", "--rails"]
        if baseline_run_id is not None:
            argv.extend(["--baseline", baseline_run_id])
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

        evidence = [
            json.loads(line)
            for line in (run_dir / "evidence.jsonl").read_text().splitlines()
            if line.strip()
        ]
        for record in evidence:
            record_errors = sorted(
                self.evidence_validator.iter_errors(record),
                key=lambda error: list(error.path),
            )
            self.assertEqual([], record_errors)

        return result, document, evidence

    def query_finding(self, document):
        matches = [
            finding
            for finding in document["findings"]
            if finding["kind"] == "database.query_count"
            and finding["rule_id"] == "rails.sql.query_count"
        ]
        self.assertEqual(1, len(matches))
        return matches[0]

    def test_sql_evidence_normalizes_whitespace_and_ignores_framework_noise(self):
        with tempfile.TemporaryDirectory() as temp:
            cwd = Path(temp)
            target = self.create_fake_framework(cwd)

            result, document, evidence = self.observed_run(
                cwd,
                target,
                count=2,
                statement_a="  SELECT  *  FROM orders\nWHERE id = $1  ",
                statement_b="SELECT * FROM orders WHERE id = $1",
                noise=True,
            )

            self.assertEqual(0, result.returncode, result.stderr)
            self.assertEqual("pass", document["verdict"]["status"])
            self.assertEqual(2, document["summary"]["evidence_count"])
            self.assertEqual(1, document["summary"]["finding_count"])
            self.assertEqual(2, len(evidence))

            for record in evidence:
                self.assertEqual("rails", record["source"])
                self.assertEqual("rails.sql", record["kind"])
                self.assertEqual(
                    "SELECT * FROM orders WHERE id = $1",
                    record["attributes"]["sql.statement"],
                )
                self.assertEqual("Order Load", record["attributes"]["sql.name"])
                self.assertIs(False, record["attributes"]["sql.cached"])
                self.assertGreaterEqual(record["attributes"]["sql.duration_ms"], 0)

            finding = self.query_finding(document)
            self.assertEqual("info", finding["severity"])
            self.assertEqual(["rails"], finding["sources"])
            self.assertEqual(
                [record["evidence_id"] for record in evidence],
                finding["evidence_refs"],
            )
            self.assertNotIn("comparison", finding)

    def test_higher_query_count_is_regressed_with_stable_identity(self):
        with tempfile.TemporaryDirectory() as temp:
            cwd = Path(temp)
            target = self.create_fake_framework(cwd)

            baseline_result, baseline, _ = self.observed_run(cwd, target, count=1)
            self.assertEqual(0, baseline_result.returncode, baseline_result.stderr)
            baseline_finding = self.query_finding(baseline)

            current_result, current, evidence = self.observed_run(
                cwd,
                target,
                count=3,
                baseline_run_id=baseline["run"]["run_id"],
            )
            current_finding = self.query_finding(current)

            self.assertEqual(0, current_result.returncode, current_result.stderr)
            self.assertEqual("pass", current["verdict"]["status"])
            self.assertEqual(baseline_finding["finding_id"], current_finding["finding_id"])
            self.assertEqual(3, len(evidence))
            self.assertEqual("warning", current_finding["severity"])
            self.assertEqual(
                {
                    "baseline": 1,
                    "current": 3,
                    "delta": 2,
                    "delta_percent": 200.0,
                    "unit": "queries",
                },
                current_finding["comparison"],
            )
            finding_id = current_finding["finding_id"]
            self.assertEqual([], current["diff"]["new"])
            self.assertEqual([], current["diff"]["resolved"])
            self.assertEqual([], current["diff"]["unchanged"])
            self.assertEqual([finding_id], current["diff"]["regressed"])
            self.assertEqual([], current["diff"]["improved"])
            self.assertFalse(any(g["rule_id"].startswith("database.") for g in current["verdict"]["gates"]))

    def test_lower_query_count_is_improved(self):
        with tempfile.TemporaryDirectory() as temp:
            cwd = Path(temp)
            target = self.create_fake_framework(cwd)

            _, baseline, _ = self.observed_run(cwd, target, count=3)
            current_result, current, _ = self.observed_run(
                cwd,
                target,
                count=1,
                baseline_run_id=baseline["run"]["run_id"],
            )
            finding = self.query_finding(current)

            self.assertEqual(0, current_result.returncode, current_result.stderr)
            self.assertEqual("pass", current["verdict"]["status"])
            self.assertEqual("info", finding["severity"])
            self.assertEqual(1, finding["comparison"]["current"])
            self.assertEqual(3, finding["comparison"]["baseline"])
            self.assertEqual(-2, finding["comparison"]["delta"])
            self.assertAlmostEqual(-66.66666666666667, finding["comparison"]["delta_percent"])
            self.assertEqual([finding["finding_id"]], current["diff"]["improved"])
            self.assertEqual([], current["diff"]["regressed"])
            self.assertEqual([], current["diff"]["unchanged"])

    def test_equal_query_count_remains_unchanged_with_comparison(self):
        with tempfile.TemporaryDirectory() as temp:
            cwd = Path(temp)
            target = self.create_fake_framework(cwd)

            _, baseline, _ = self.observed_run(cwd, target, count=2)
            current_result, current, _ = self.observed_run(
                cwd,
                target,
                count=2,
                baseline_run_id=baseline["run"]["run_id"],
            )
            finding = self.query_finding(current)

            self.assertEqual(0, current_result.returncode, current_result.stderr)
            self.assertEqual("info", finding["severity"])
            self.assertEqual(
                {
                    "baseline": 2,
                    "current": 2,
                    "delta": 0,
                    "delta_percent": 0.0,
                    "unit": "queries",
                },
                finding["comparison"],
            )
            self.assertEqual([finding["finding_id"]], current["diff"]["unchanged"])
            self.assertEqual([], current["diff"]["regressed"])
            self.assertEqual([], current["diff"]["improved"])


if __name__ == "__main__":
    unittest.main(verbosity=2)
