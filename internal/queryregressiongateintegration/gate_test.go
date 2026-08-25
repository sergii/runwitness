package queryregressiongateintegration

import (
	"reflect"
	"testing"
)

func TestStripQueryRegressionGatePreservesTargetArguments(t *testing.T) {
	args := []string{
		"run",
		"--baseline",
		"0198f5f0-0000-7000-8000-000000000001",
		"--fail-on-query-regression",
		"--",
		"ruby",
		"spec.rb",
		"--fail-on-query-regression",
	}

	stripped, requested, hasBaseline, err := stripQueryRegressionGate(args)
	if err != nil {
		t.Fatalf("stripQueryRegressionGate returned error: %v", err)
	}
	if !requested {
		t.Fatal("expected query regression gate to be requested")
	}
	if !hasBaseline {
		t.Fatal("expected baseline to be detected")
	}

	expected := []string{
		"run",
		"--baseline",
		"0198f5f0-0000-7000-8000-000000000001",
		"--",
		"ruby",
		"spec.rb",
		"--fail-on-query-regression",
	}
	if !reflect.DeepEqual(expected, stripped) {
		t.Fatalf("unexpected stripped args:\nwant: %#v\n got: %#v", expected, stripped)
	}
}

func TestStripQueryRegressionGateRejectsDuplicateOption(t *testing.T) {
	_, _, _, err := stripQueryRegressionGate([]string{
		"run",
		"--baseline",
		"0198f5f0-0000-7000-8000-000000000001",
		"--fail-on-query-regression",
		"--fail-on-query-regression",
		"--",
		"true",
	})
	if err == nil {
		t.Fatal("expected duplicate query regression option to fail")
	}
}

func TestEligibleQueryRegressionIDsFiltersByComparisonAndFindingSemantics(t *testing.T) {
	document := map[string]any{
		"findings": []any{
			map[string]any{
				"finding_id": "rwf_b",
				"kind":       queryCountKind,
				"rule_id":    railsQueryCountRuleID,
			},
			map[string]any{
				"finding_id": "rwf_a",
				"kind":       queryCountKind,
				"rule_id":    railsQueryCountRuleID,
			},
			map[string]any{
				"finding_id": "rwf_runtime",
				"kind":       "runtime.error",
				"rule_id":    "otel.span.error",
			},
		},
	}

	eligible, err := eligibleQueryRegressionIDs(document, []string{"rwf_runtime", "rwf_b", "rwf_a"})
	if err != nil {
		t.Fatalf("eligibleQueryRegressionIDs returned error: %v", err)
	}
	expected := []string{"rwf_a", "rwf_b"}
	if !reflect.DeepEqual(expected, eligible) {
		t.Fatalf("unexpected eligible IDs: want %#v, got %#v", expected, eligible)
	}
}
