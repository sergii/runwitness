package gateintegration

import (
	"reflect"
	"testing"
)

func TestStripGateScopePreservesTargetArguments(t *testing.T) {
	args := []string{
		"run",
		"--baseline",
		"0198f5f0-0000-7000-8000-000000000001",
		"--gate-scope",
		"new",
		"--",
		"ruby",
		"spec.rb",
		"--gate-scope",
		"target-value",
	}

	stripped, scope, requested, hasBaseline, err := stripGateScope(args)
	if err != nil {
		t.Fatalf("stripGateScope returned error: %v", err)
	}
	if scope != "new" || !requested || !hasBaseline {
		t.Fatalf("unexpected parsed options: scope=%q requested=%v hasBaseline=%v", scope, requested, hasBaseline)
	}

	expected := []string{
		"run",
		"--baseline",
		"0198f5f0-0000-7000-8000-000000000001",
		"--",
		"ruby",
		"spec.rb",
		"--gate-scope",
		"target-value",
	}
	if !reflect.DeepEqual(expected, stripped) {
		t.Fatalf("unexpected stripped args:\nwant: %#v\n got: %#v", expected, stripped)
	}
}

func TestStripGateScopeRejectsUnknownValue(t *testing.T) {
	_, _, _, _, err := stripGateScope([]string{
		"run",
		"--baseline",
		"0198f5f0-0000-7000-8000-000000000001",
		"--gate-scope",
		"future",
		"--",
		"true",
	})
	if err == nil {
		t.Fatal("expected unknown gate scope to fail")
	}
}

func TestStripGateScopeRequiresBaseline(t *testing.T) {
	_, _, requested, hasBaseline, err := stripGateScope([]string{
		"run",
		"--gate-scope",
		"new",
		"--",
		"true",
	})
	if err != nil {
		t.Fatalf("unexpected parser error: %v", err)
	}
	if !requested || hasBaseline {
		t.Fatalf("unexpected parser state: requested=%v hasBaseline=%v", requested, hasBaseline)
	}
}
