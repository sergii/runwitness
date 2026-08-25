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
		"--query-count-tolerance",
		"2",
		"--",
		"ruby",
		"spec.rb",
		"--fail-on-query-regression",
		"--query-count-tolerance",
		"99",
	}

	stripped, policy, err := stripQueryRegressionGate(args)
	if err != nil {
		t.Fatalf("stripQueryRegressionGate returned error: %v", err)
	}
	if !policy.Requested {
		t.Fatal("expected query regression gate to be requested")
	}
	if !policy.HasBaseline {
		t.Fatal("expected baseline to be detected")
	}
	if !policy.ToleranceExplicit || policy.Tolerance != 2 {
		t.Fatalf("unexpected tolerance policy: %#v", policy)
	}

	expected := []string{
		"run",
		"--baseline",
		"0198f5f0-0000-7000-8000-000000000001",
		"--",
		"ruby",
		"spec.rb",
		"--fail-on-query-regression",
		"--query-count-tolerance",
		"99",
	}
	if !reflect.DeepEqual(expected, stripped) {
		t.Fatalf("unexpected stripped args:\nwant: %#v\n got: %#v", expected, stripped)
	}
}

func TestStripQueryRegressionGateRejectsDuplicateOption(t *testing.T) {
	_, _, err := stripQueryRegressionGate([]string{
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

func TestStripQueryRegressionGateRejectsToleranceWithoutGate(t *testing.T) {
	_, _, err := stripQueryRegressionGate([]string{
		"run",
		"--baseline",
		"0198f5f0-0000-7000-8000-000000000001",
		"--query-count-tolerance",
		"1",
		"--",
		"true",
	})
	if err == nil {
		t.Fatal("expected tolerance without query regression gate to fail")
	}
}

func TestStripQueryRegressionGateRejectsInvalidTolerance(t *testing.T) {
	for _, value := range []string{"", "-1", "1.5", "wat"} {
		t.Run(value, func(t *testing.T) {
			_, _, err := stripQueryRegressionGate([]string{
				"run",
				"--baseline",
				"0198f5f0-0000-7000-8000-000000000001",
				"--fail-on-query-regression",
				"--query-count-tolerance",
				value,
				"--",
				"true",
			})
			if err == nil {
				t.Fatalf("expected tolerance %q to fail", value)
			}
		})
	}
}

func TestEligibleQueryRegressionIDsFiltersByToleranceAndFindingSemantics(t *testing.T) {
	document := map[string]any{
		"findings": []any{
			map[string]any{
				"finding_id": "rwf_b",
				"kind":       queryCountKind,
				"rule_id":    railsQueryCountRuleID,
				"comparison": map[string]any{"delta": float64(2)},
			},
			map[string]any{
				"finding_id": "rwf_a",
				"kind":       queryCountKind,
				"rule_id":    railsQueryCountRuleID,
				"comparison": map[string]any{"delta": float64(1)},
			},
			map[string]any{
				"finding_id": "rwf_runtime",
				"kind":       "runtime.error",
				"rule_id":    "otel.span.error",
			},
		},
	}

	eligible, err := eligibleQueryRegressionIDs(document, []string{"rwf_runtime", "rwf_b", "rwf_a"}, 1)
	if err != nil {
		t.Fatalf("eligibleQueryRegressionIDs returned error: %v", err)
	}
	expected := []string{"rwf_b"}
	if !reflect.DeepEqual(expected, eligible) {
		t.Fatalf("unexpected eligible IDs: want %#v, got %#v", expected, eligible)
	}
}

func TestEligibleQueryRegressionIDsRequiresComparisonDelta(t *testing.T) {
	document := map[string]any{
		"findings": []any{
			map[string]any{
				"finding_id": "rwf_missing",
				"kind":       queryCountKind,
				"rule_id":    railsQueryCountRuleID,
			},
		},
	}

	_, err := eligibleQueryRegressionIDs(document, []string{"rwf_missing"}, 0)
	if err == nil {
		t.Fatal("expected missing comparison delta to fail")
	}
}
