package queryregressiongateintegration

import (
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"sort"
	"strconv"

	"github.com/sergii/runwitness/internal/gateintegration"
)

const (
	queryRegressionRuleID = "database.no_query_count_regressions"
	queryCountKind        = "database.query_count"
	railsQueryCountRuleID = "rails.sql.query_count"
)

type queryRegressionPolicy struct {
	Requested         bool
	HasBaseline       bool
	Tolerance         int
	ToleranceExplicit bool
}

func Main(args []string) int {
	stripped, policy, err := stripQueryRegressionGate(args)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		fmt.Fprintln(os.Stderr, usage())
		return 2
	}
	if !policy.Requested {
		return gateintegration.Main(args)
	}
	if !policy.HasBaseline {
		fmt.Fprintln(os.Stderr, "--fail-on-query-regression requires --baseline")
		return 2
	}

	workingDirectory, err := os.Getwd()
	if err != nil {
		fmt.Fprintf(os.Stderr, "runwitness: get working directory for query regression gate: %v\n", err)
		return 2
	}
	workingDirectory, err = filepath.Abs(workingDirectory)
	if err != nil {
		fmt.Fprintf(os.Stderr, "runwitness: resolve working directory for query regression gate: %v\n", err)
		return 2
	}

	beforeRuns, err := runDirectorySet(workingDirectory)
	if err != nil {
		fmt.Fprintf(os.Stderr, "runwitness: inspect Runs before query regression gate: %v\n", err)
		return 2
	}

	resultExit := gateintegration.Main(stripped)

	runDirectory, found, err := newRunDirectory(workingDirectory, beforeRuns)
	if err != nil {
		fmt.Fprintf(os.Stderr, "runwitness: locate query-regression-gated Run: %v\n", err)
		return 2
	}
	if !found {
		return resultExit
	}

	finalExit, err := applyQueryRegressionGate(runDirectory, resultExit, policy)
	if err != nil {
		message := fmt.Sprintf("query regression gate failed: %v", err)
		_ = markGateError(runDirectory, message)
		fmt.Fprintf(os.Stderr, "runwitness: %s\n", message)
		return 2
	}
	return finalExit
}

func usage() string {
	return "usage: runwitness run [--label <name>] [--otel] [--rails] [--baseline <run_id>] [--gate-scope <all|new>] [--fail-on-query-regression] [--query-count-tolerance <queries>] -- <command> [args...]"
}

func stripQueryRegressionGate(args []string) ([]string, queryRegressionPolicy, error) {
	policy := queryRegressionPolicy{}
	if len(args) == 0 || args[0] != "run" {
		return append([]string(nil), args...), policy, nil
	}

	result := make([]string, 0, len(args))
	beforeSeparator := true

	for index := 0; index < len(args); index++ {
		argument := args[index]
		if beforeSeparator && argument == "--" {
			beforeSeparator = false
			result = append(result, argument)
			continue
		}
		if beforeSeparator && argument == "--baseline" {
			policy.HasBaseline = true
			result = append(result, argument)
			if index+1 < len(args) {
				index++
				result = append(result, args[index])
			}
			continue
		}
		if beforeSeparator && argument == "--fail-on-query-regression" {
			if policy.Requested {
				return nil, queryRegressionPolicy{}, errors.New("--fail-on-query-regression may be specified only once")
			}
			policy.Requested = true
			continue
		}
		if beforeSeparator && argument == "--query-count-tolerance" {
			if policy.ToleranceExplicit {
				return nil, queryRegressionPolicy{}, errors.New("--query-count-tolerance may be specified only once")
			}
			if index+1 >= len(args) || args[index+1] == "--" || args[index+1] == "" {
				return nil, queryRegressionPolicy{}, errors.New("--query-count-tolerance requires a non-negative integer")
			}
			value, parseErr := strconv.Atoi(args[index+1])
			if parseErr != nil || value < 0 {
				return nil, queryRegressionPolicy{}, fmt.Errorf("--query-count-tolerance requires a non-negative integer; got %q", args[index+1])
			}
			policy.Tolerance = value
			policy.ToleranceExplicit = true
			index++
			continue
		}
		result = append(result, argument)
	}

	if policy.ToleranceExplicit && !policy.Requested {
		return nil, queryRegressionPolicy{}, errors.New("--query-count-tolerance requires --fail-on-query-regression")
	}

	return result, policy, nil
}

func applyQueryRegressionGate(runDirectory string, originalExit int, policy queryRegressionPolicy) (int, error) {
	document, err := readRunDocument(filepath.Join(runDirectory, "run.json"))
	if err != nil {
		return 2, err
	}

	verdict, ok := document["verdict"].(map[string]any)
	if !ok {
		return 2, errors.New("run.json has no verdict object")
	}
	gates, err := objectArray(verdict["gates"])
	if err != nil {
		return 2, fmt.Errorf("read verdict gates: %w", err)
	}
	for _, gate := range gates {
		if stringValue(gate["rule_id"]) == queryRegressionRuleID {
			return 2, errors.New("run.json already contains query regression gate")
		}
	}

	if stringValue(verdict["status"]) == "error" {
		gate := map[string]any{
			"rule_id":     queryRegressionRuleID,
			"action":      "fail",
			"outcome":     "skipped",
			"finding_ids": []string{},
			"message":     "query regression gate skipped because Run verdict is error",
		}
		applyToleranceParameters(gate, policy)
		gates = append(gates, gate)
		verdict["gates"] = gates
		if err := writeRunDocument(runDirectory, document); err != nil {
			return 2, err
		}
		return 2, nil
	}

	diff, ok := document["diff"].(map[string]any)
	if !ok {
		return 2, errors.New("baseline comparison did not produce a reliable diff")
	}
	regressed, err := stringArray(diff["regressed"])
	if err != nil {
		return 2, fmt.Errorf("read regressed Finding IDs: %w", err)
	}

	eligible, err := eligibleQueryRegressionIDs(document, regressed, policy.Tolerance)
	if err != nil {
		return 2, err
	}

	outcome := "passed"
	if len(eligible) > 0 {
		outcome = "triggered"
	}
	gate := map[string]any{
		"rule_id":     queryRegressionRuleID,
		"action":      "fail",
		"outcome":     outcome,
		"finding_ids": eligible,
		"message":     queryRegressionMessage(len(eligible), policy),
	}
	applyToleranceParameters(gate, policy)
	gates = append(gates, gate)
	verdict["gates"] = gates

	if len(eligible) > 0 {
		verdict["status"] = "fail"
	}

	if err := writeRunDocument(runDirectory, document); err != nil {
		return 2, err
	}
	return exitCodeForVerdict(stringValue(verdict["status"]), originalExit), nil
}

func queryRegressionMessage(eligibleCount int, policy queryRegressionPolicy) string {
	if policy.ToleranceExplicit {
		return fmt.Sprintf("%d query-count regression finding(s) exceeded tolerance %d queries", eligibleCount, policy.Tolerance)
	}
	return fmt.Sprintf("%d query-count regression finding(s) observed", eligibleCount)
}

func applyToleranceParameters(gate map[string]any, policy queryRegressionPolicy) {
	if !policy.ToleranceExplicit {
		return
	}
	gate["parameters"] = map[string]any{
		"max_delta": policy.Tolerance,
		"unit":      "queries",
	}
}

func eligibleQueryRegressionIDs(document map[string]any, regressed []string, tolerance int) ([]string, error) {
	regressedSet := stringSet(regressed)
	findings, err := objectArray(document["findings"])
	if err != nil {
		return nil, fmt.Errorf("read Findings: %w", err)
	}

	eligibleSet := make(map[string]struct{})
	for _, finding := range findings {
		findingID := stringValue(finding["finding_id"])
		if _, isRegressed := regressedSet[findingID]; !isRegressed {
			continue
		}
		if stringValue(finding["kind"]) != queryCountKind {
			continue
		}
		if stringValue(finding["rule_id"]) != railsQueryCountRuleID {
			continue
		}

		delta, err := queryCountDelta(finding)
		if err != nil {
			return nil, fmt.Errorf("evaluate query-count Finding %q: %w", findingID, err)
		}
		if delta > float64(tolerance) {
			eligibleSet[findingID] = struct{}{}
		}
	}

	eligible := make([]string, 0, len(eligibleSet))
	for findingID := range eligibleSet {
		eligible = append(eligible, findingID)
	}
	sort.Strings(eligible)
	return eligible, nil
}

func queryCountDelta(finding map[string]any) (float64, error) {
	comparison, ok := finding["comparison"].(map[string]any)
	if !ok {
		return 0, errors.New("comparison is missing or is not an object")
	}

	raw, exists := comparison["delta"]
	if !exists {
		return 0, errors.New("comparison.delta is missing")
	}

	var delta float64
	switch value := raw.(type) {
	case float64:
		delta = value
	case float32:
		delta = float64(value)
	case int:
		delta = float64(value)
	case int8:
		delta = float64(value)
	case int16:
		delta = float64(value)
	case int32:
		delta = float64(value)
	case int64:
		delta = float64(value)
	case uint:
		delta = float64(value)
	case uint8:
		delta = float64(value)
	case uint16:
		delta = float64(value)
	case uint32:
		delta = float64(value)
	case uint64:
		delta = float64(value)
	default:
		return 0, fmt.Errorf("comparison.delta must be an integer query count, got %T", raw)
	}

	if math.IsNaN(delta) || math.IsInf(delta, 0) || delta < 0 || math.Trunc(delta) != delta {
		return 0, fmt.Errorf("comparison.delta must be a non-negative integer query count, got %v", raw)
	}
	return delta, nil
}

func exitCodeForVerdict(status string, fallback int) int {
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

func objectArray(raw any) ([]map[string]any, error) {
	if typed, ok := raw.([]map[string]any); ok {
		return append([]map[string]any(nil), typed...), nil
	}
	items, ok := raw.([]any)
	if !ok {
		return nil, errors.New("expected an array of objects")
	}
	result := make([]map[string]any, 0, len(items))
	for _, item := range items {
		object, ok := item.(map[string]any)
		if !ok {
			return nil, errors.New("array contains a non-object value")
		}
		result = append(result, object)
	}
	return result, nil
}

func stringArray(raw any) ([]string, error) {
	if typed, ok := raw.([]string); ok {
		return append([]string(nil), typed...), nil
	}
	items, ok := raw.([]any)
	if !ok {
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

func stringValue(raw any) string {
	value, _ := raw.(string)
	return value
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
		return fmt.Errorf("encode run.json after query regression gate: %w", err)
	}
	payload = append(payload, '\n')

	temporary := filepath.Join(runDirectory, "run.json.query-regression-gate.tmp")
	final := filepath.Join(runDirectory, "run.json")
	if err := os.WriteFile(temporary, payload, 0o644); err != nil {
		return fmt.Errorf("write query-regression-gated run.json: %w", err)
	}
	if err := os.Rename(temporary, final); err != nil {
		return fmt.Errorf("publish query-regression-gated run.json: %w", err)
	}
	return nil
}

func markGateError(runDirectory, message string) error {
	document, err := readRunDocument(filepath.Join(runDirectory, "run.json"))
	if err != nil {
		return err
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
