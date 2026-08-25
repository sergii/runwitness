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


HANDLED_ERROR_TARGET = r'''
require "rails"

begin
  raise RuntimeError, "hidden checkout problem"
rescue => error
  Rails.error.report(
    error,
    handled: true,
    severity: :warning,
    context: { order_id: 42 },
    source: "checkout",
  )
end

puts "tests-pass"
'''


CLEAN_TARGET = r'''
require "rails"
puts "tests-pass"
'''


class RunWitnessRailsErrorV005Contract(unittest.TestCase):
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
            raise AssertionError("Ruby is required by the Rails adapter acceptance contract")

        cls.run_validator = Draft202012Validator(
            json.loads(RUN_SCHEMA_PATH.read_text()),
            format_checker=FormatChecker(),
        )
        cls.evidence_validator = Draft202012Validator(
            json.loads(EVIDENCE_SCHEMA_PATH.read_text()),
            format_checker=FormatChecker(),
        )

    def create_fake_rails(self, cwd: Path) -> None:
        (cwd / "rails.rb").write_text(textwrap.dedent(FAKE_RAILS))

    def write_target(self, cwd: Path, name: str, source: str) -> Path:
        target = cwd / name
        target.write_text(textwrap.dedent(source))
        return target

    def invoke(self, cwd: Path, *target: str) -> subprocess.CompletedProcess:
        return subprocess.run(
            [str(self.runner_bin), "run", "--rails", "--", *target],
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
        run_dir = run_dirs[0]
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
        return run_dir, document, evidence

    def test_handled_rails_error_fails_runtime_gate_while_target_exits_zero(self):
        with tempfile.TemporaryDirectory() as temp:
            cwd = Path(temp)
            self.create_fake_rails(cwd)
            target = self.write_target(cwd, "handled_error.rb", HANDLED_ERROR_TARGET)

            result = self.invoke(cwd, self.ruby, "-I", str(cwd), str(target))
            run_dir, document, evidence = self.load_single_run(cwd)

            self.assertEqual(1, result.returncode, result.stderr)
            self.assertEqual("tests-pass\n", (run_dir / "stdout.log").read_text())
            self.assertEqual(0, document["run"]["process"]["exit_code"])
            self.assertEqual("fail", document["verdict"]["status"])

            adapters = [adapter for adapter in document["adapters"] if adapter["name"] == "rails"]
            self.assertEqual(1, len(adapters))
            self.assertEqual("ok", adapters[0]["status"])

            self.assertEqual(1, document["summary"]["evidence_count"])
            self.assertEqual(1, document["summary"]["finding_count"])
            self.assertEqual(1, len(evidence))

            record = evidence[0]
            self.assertEqual("rails", record["source"])
            self.assertEqual("rails.error", record["kind"])
            self.assertEqual("RuntimeError", record["attributes"]["error.class"])
            self.assertEqual("hidden checkout problem", record["attributes"]["error.message"])
            self.assertIs(True, record["attributes"]["error.handled"])
            self.assertEqual("warning", record["attributes"]["error.severity"])
            self.assertEqual("checkout", record["attributes"]["error.source"])

            finding = document["findings"][0]
            self.assertTrue(finding["finding_id"].startswith("rwf_"))
            self.assertNotIn(document["run"]["run_id"].replace("-", ""), finding["finding_id"])
            self.assertEqual("runtime.handled_error", finding["kind"])
            self.assertEqual("warning", finding["severity"])
            self.assertEqual("rails.error.handled", finding["rule_id"])
            self.assertEqual(["rails"], finding["sources"])
            self.assertEqual([record["evidence_id"]], finding["evidence_refs"])

            gates = [gate for gate in document["verdict"]["gates"] if gate["rule_id"] == "runtime.no_errors"]
            self.assertEqual(1, len(gates))
            self.assertEqual("fail", gates[0]["action"])
            self.assertEqual("triggered", gates[0]["outcome"])
            self.assertEqual([finding["finding_id"]], gates[0]["finding_ids"])

    def test_clean_rails_run_keeps_passing_verdict(self):
        with tempfile.TemporaryDirectory() as temp:
            cwd = Path(temp)
            self.create_fake_rails(cwd)
            target = self.write_target(cwd, "clean.rb", CLEAN_TARGET)

            result = self.invoke(cwd, self.ruby, "-I", str(cwd), str(target))
            _, document, evidence = self.load_single_run(cwd)

            self.assertEqual(0, result.returncode, result.stderr)
            self.assertEqual(0, document["run"]["process"]["exit_code"])
            self.assertEqual("pass", document["verdict"]["status"])
            self.assertEqual([], evidence)
            self.assertEqual(0, document["summary"]["evidence_count"])
            self.assertEqual(0, document["summary"]["finding_count"])

            adapters = [adapter for adapter in document["adapters"] if adapter["name"] == "rails"]
            self.assertEqual(1, len(adapters))
            self.assertEqual("ok", adapters[0]["status"])

    def test_requested_rails_observation_without_rails_reporter_is_runner_error(self):
        with tempfile.TemporaryDirectory() as temp:
            cwd = Path(temp)

            result = self.invoke(cwd, self.ruby, "-e", "puts 'ruby-only'")
            _, document, evidence = self.load_single_run(cwd)

            self.assertEqual(2, result.returncode)
            self.assertEqual(0, document["run"]["process"]["exit_code"])
            self.assertEqual("error", document["verdict"]["status"])
            self.assertEqual([], evidence)

            adapters = [adapter for adapter in document["adapters"] if adapter["name"] == "rails"]
            self.assertEqual(1, len(adapters))
            self.assertIn(adapters[0]["status"], {"unavailable", "error"})


if __name__ == "__main__":
    unittest.main(verbosity=2)
