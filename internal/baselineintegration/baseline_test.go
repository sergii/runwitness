package baselineintegration

import (
	"reflect"
	"testing"
)

func TestStripBaselineRemovesOnlyRunWitnessOption(t *testing.T) {
	args := []string{
		"run",
		"--otel",
		"--baseline",
		"0198f5f0-0000-7000-8000-000000000001",
		"--",
		"ruby",
		"spec.rb",
		"--baseline",
		"target-value",
	}

	stripped, baselineID, requested, err := stripBaseline(args)
	if err != nil {
		t.Fatalf("stripBaseline returned error: %v", err)
	}
	if !requested {
		t.Fatal("expected baseline to be requested")
	}
	if baselineID != "0198f5f0-0000-7000-8000-000000000001" {
		t.Fatalf("unexpected baseline ID: %q", baselineID)
	}

	expected := []string{
		"run",
		"--otel",
		"--",
		"ruby",
		"spec.rb",
		"--baseline",
		"target-value",
	}
	if !reflect.DeepEqual(expected, stripped) {
		t.Fatalf("unexpected stripped args:\nwant: %#v\n got: %#v", expected, stripped)
	}
}

func TestClassifyFindingIDsUsesStableSetSemanticsAndSorting(t *testing.T) {
	diff := classifyFindingIDs(
		[]string{"rwf_z", "rwf_a", "rwf_z", "rwf_shared"},
		[]string{"rwf_shared", "rwf_y", "rwf_b", "rwf_y"},
	)

	if !reflect.DeepEqual([]string{"rwf_b", "rwf_y"}, diff.New) {
		t.Fatalf("unexpected new Findings: %#v", diff.New)
	}
	if !reflect.DeepEqual([]string{"rwf_a", "rwf_z"}, diff.Resolved) {
		t.Fatalf("unexpected resolved Findings: %#v", diff.Resolved)
	}
	if !reflect.DeepEqual([]string{"rwf_shared"}, diff.Unchanged) {
		t.Fatalf("unexpected unchanged Findings: %#v", diff.Unchanged)
	}
	if len(diff.Regressed) != 0 || len(diff.Improved) != 0 {
		t.Fatalf("reserved comparison classes must be empty: %#v", diff)
	}
}

func TestStripBaselineRejectsDuplicateSelection(t *testing.T) {
	_, _, _, err := stripBaseline([]string{
		"run",
		"--baseline",
		"0198f5f0-0000-7000-8000-000000000001",
		"--baseline",
		"0198f5f0-0000-7000-8000-000000000002",
		"--",
		"true",
	})
	if err == nil {
		t.Fatal("expected duplicate baseline selection to fail")
	}
}
