package gateintegration

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"

	"github.com/sergii/runwitness/internal/baselineintegration"
)

func Main(args []string) int {
	stripped, scope, requested, hasBaseline, err := stripGateScope(args)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		fmt.Fprintln(os.Stderr, "usage: runwitness run [--label <name>] [--otel] [--rails] [--baseline <run_id>] [--gate-scope <all|new>] -- <command> [args...]")
		return 2
	}
	if !requested {
		return baselineintegration.Main(args)
	}
	if !hasBaseline {
		fmt.Fprintln(os.Stderr, "--gate-scope requires --baseline")
		return 2
	}

	workingDirectory, err := os.Getwd()
	if err != nil {
		fmt.Fprintf(os.Stderr, "runwitness: get working directory for gate scope: %v\n", err)
		return 2
	}
	workingDirectory, err = filepath.Abs(workingDirectory)
	if err != nil {
		fmt.Fprintf(os.Stderr, "runwitness: resolve working directory for gate scope: %v\n", err)
		return 2
	}

	beforeRuns, err := runDirectorySet(workingDirectory)
	if err != nil {
		fmt.Fprintf(os.Stderr, "runwitness: inspect Runs before gate scope: %v\n", err)
		return 2
	}

	resultExit := baselineintegration.Main(stripped)

	runDirectory, found, err := newRunDirectory(workingDirectory, beforeRuns)
	if err != nil {
		fmt.Fprintf(os.Stderr, "runwitness: locate gate-scoped Run: %v\n", err)
		return 2
	}
	if !found {
		return resultExit
	}

	finalExit, err := applyGateScope(runDirectory, scope, resultExit)
	if err != nil {
		message := fmt.Sprintf("gate scope failed: %v", err)
		_ = markGateScopeError(runDirectory, scope, message)
		fmt.Fprintf(os.Stderr, "runwitness: %s\n", message)
		return 2
	}
	return finalExit
}

func stripGateScope(args []string) ([]string, string, bool, bool, error) {
	if len(args) == 0 || args[0] != "run" {
		return append([]string(nil), args...), "", false, false, nil
	}

	result := make([]string, 0, len(args))
	scope := ""
	requested := false
	hasBaseline := false
	beforeSeparator := true

	for index := 0; index < len(args); index++ {
		argument := args[index]
		if beforeSeparator && argument == "--" {
			beforeSeparator = false
			result = append(result, argument)
			continue
		}
		if beforeSeparator && argument == "--baseline" {
			hasBaseline = true
			result = append(result, argument)
			if index+1 < len(args) {
				index++
				result = append(result, args[index])
			}
			continue
		}
		if beforeSeparator && argument == "--gate-scope" {
			if requested {
				return nil, "", false, false, errors.New("--gate-scope may be specified only once")
			}
			if index+1 >= len(args) || args[index+1] == "--" || args[index+1] == "" {
				return nil, "", false, false, errors.New("--gate-scope requires one of: all, new")
			}
			scope = args[index+1]
			requested = true
			index++
			continue
		}
		result = append(result, argument)
	}

	if requested {
		switch scope {
		case "all", "new":
		default:
			return nil, "", false, false, fmt.Errorf("--gate-scope must be one of: all, new; got %q", scope)
		}
	}

	return result, scope, requested, hasBaseline, nil
}

func applyGateScope(runDirectory, scope string, originalExit int) (int, error) {
	document, err := readRunDocument(filepath.Join(runDirectory, "run.json"))
	if err != nil {
		return 2, err
	}

	baseline, ok := document["baseline"].(map[string]any)
	if !ok {
		return 2, errors.New("baseline comparison did not record baseline metadata")
	}
	baseline["finding_gate_scope"] = scope

	verdict, ok := document["verdict"].(map[string]any)
	if !ok {
		return 2, errors.New("run.json has no verdict object")
	}
	if status, _ := verdict["status"].(string); status == "error" {
		if err := writeRunDocument(runDirectory, document); err != nil {
			return 2, err
		}
		return originalExit, nil
	}

	if scope == "new" {
		diff, ok := document["diff"].(map[string]any)
		if !ok {
			return 2, errors.New("baseline comparison did not produce a reliable diff")
		}
		newFindingIDs, err := stringArray(diff["new"])
		if err != nil {
			return 2, fmt.Errorf("read new Finding IDs: %w", err)
		}
		if err := applyNewFindingScope(document, newFindingIDs); err != nil {
			return 2, err
		}
	}

	if err := writeRunDocument(runDirectory, document); err != nil {
		return 2, err
	}
	return exitCodeForDocument(document, originalExit), nil
}

func applyNewFindingScope(document map[string]any, newFindingIDs []string) error {
	verdict, ok := document["verdict"].(map[string]any)
	if !ok {
		return errors.New("run.json has no verdict object")
	}
	gates, ok := verdict["gates"].([]any)
	if !ok {
		return errors.New("run.json verdict gates is not an array")
	}

	newSet := stringSet(newFindingIDs)
	for _, rawGate := range gates {
		gate, ok := rawGate.(map[string]any)
		if !ok {
			return errors.New("run.json contains a non-object gate")
		}
		findingIDs, err := stringArray(gate["finding_ids"])
		if err != nil {
			return err
		}
		eligible := make([]string, 0, len(findingIDs))
		for _, findingID := range findingIDs {
			if _, isNew := newSet[findingID]; isNew {
				eligible = append(eligible, findingID)
			}
		}
		sort.Strings(eligible)
		gate["finding_ids"] = eligible

		outcome, _ := gate["outcome"].(string)
		if outcome == "triggered" && len(eligible) == 0 {
			gate["outcome"] = "passed"
		}
		if ruleID, _ := gate["rule_id"].(string); ruleID == "runtime.no_errors" {
			gate["message"] = fmt.Sprintf("%d new runtime error finding(s) observed", len(eligible))
		}
	}

	verdict["status"] = recomputeVerdictStatus(document, gates)
	return nil
}

func recomputeVerdictStatus(document map[string]any, gates []any) string {
	verdict, _ := document["verdict"].(map[string]any)
	if verdict != nil {
		if status, _ := verdict["status"].(string); status == "error" {
			return "error"
		}
	}
	if targetFailed(document) {
		return "fail"
	}

	status := "pass"
	for _, rawGate := range gates {
		gate, ok := rawGate.(map[string]any)
		if !ok {
			continue
		}
		outcome, _ := gate["outcome"].(string)
		if outcome == "error" {
			return "error"
		}
		if outcome != "triggered" {
			continue
		}
		action, _ := gate["action"].(string)
		switch action {
		case "fail":
			return "fail"
		case "warn":
			status = "warn"
		}
	}
	return status
}

func targetFailed(document map[string]any) bool {
	run, ok := document["run"].(map[string]any)
	if !ok {
		return false
	}
	process, ok := run["process"].(map[string]any)
	if !ok {
		return false
	}
	exitCode, exists := process["exit_code"]
	if !exists || exitCode == nil {
		return false
	}
	value, ok := exitCode.(float64)
	return ok && value != 0
}

func exitCodeForDocument(document map[string]any, fallback int) int {
	verdict, ok := document["verdict"].(map[string]any)
	if !ok {
		return fallback
	}
	status, _ := verdict["status"].(string)
	switch status {
	case "pass", "warn":
		return 0
	case "fail":
		return 1
	case "error":
		return 2
	default:
		return fallback
	}
}

func stringArray(raw any) ([]string, error) {
	items, ok := raw.([]any)
	if !ok {
		if typed, typedOK := raw.([]string); typedOK {
			return append([]string(nil), typed...), nil
		}
		return nil, errors.New("expected an array of Finding IDs")
	}
	result := make([]string, 0, len(items))
	for _, item := range items {
		value, ok := item.(string)
		if !ok {
			return nil, errors.New("Finding ID list contains a non-string value")
		}
		result = append(result, value)
	}
	return result, nil
}

func stringSet(values []string) map[string]struct{} {
	result := make(map[string]struct{}, len(values))
	for _, value := range values {
		result[value] = struct{}{}
	}
	return result
}

func readRunDocument(path string) (map[string]any, error) {
	payload, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var document map[string]any
	if err := json.Unmarshal(payload, &document); err != nil {
		return nil, fmt.Errorf("decode run.json: %w", err)
	}
	return document, nil
}

func writeRunDocument(runDirectory string, document map[string]any) error {
	payload, err := json.MarshalIndent(document, "", "  ")
	if err != nil {
		return fmt.Errorf("encode run.json after gate scope: %w", err)
	}
	payload = append(payload, '\n')

	temporary := filepath.Join(runDirectory, "run.json.gate-scope.tmp")
	final := filepath.Join(runDirectory, "run.json")
	if err := os.WriteFile(temporary, payload, 0o644); err != nil {
		return fmt.Errorf("write gate-scoped run.json: %w", err)
	}
	if err := os.Rename(temporary, final); err != nil {
		return fmt.Errorf("publish gate-scoped run.json: %w", err)
	}
	return nil
}

func markGateScopeError(runDirectory, scope, message string) error {
	document, err := readRunDocument(filepath.Join(runDirectory, "run.json"))
	if err != nil {
		return err
	}
	if baseline, ok := document["baseline"].(map[string]any); ok {
		baseline["finding_gate_scope"] = scope
	}
	verdict, ok := document["verdict"].(map[string]any)
	if !ok {
		verdict = map[string]any{}
		document["verdict"] = verdict
	}
	verdict["status"] = "error"
	verdict["message"] = message
	return writeRunDocument(runDirectory, document)
}

func runDirectorySet(workingDirectory string) (map[string]struct{}, error) {
	root := filepath.Join(workingDirectory, ".runwitness", "runs")
	entries, err := os.ReadDir(root)
	if os.IsNotExist(err) {
		return map[string]struct{}{}, nil
	}
	if err != nil {
		return nil, err
	}

	result := make(map[string]struct{}, len(entries))
	for _, entry := range entries {
		if entry.IsDir() {
			result[entry.Name()] = struct{}{}
		}
	}
	return result, nil
}

func newRunDirectory(workingDirectory string, before map[string]struct{}) (string, bool, error) {
	root := filepath.Join(workingDirectory, ".runwitness", "runs")
	entries, err := os.ReadDir(root)
	if os.IsNotExist(err) {
		return "", false, nil
	}
	if err != nil {
		return "", false, err
	}

	created := make([]string, 0, 1)
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		if _, existed := before[entry.Name()]; !existed {
			created = append(created, entry.Name())
		}
	}
	if len(created) == 0 {
		return "", false, nil
	}
	if len(created) != 1 {
		sort.Strings(created)
		return "", false, fmt.Errorf("expected one new Run directory, found %d: %v", len(created), created)
	}
	return filepath.Join(root, created[0]), true, nil
}
