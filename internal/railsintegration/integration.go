package railsintegration

import (
	"bufio"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/sergii/runwitness/internal/railsadapter"
	"github.com/sergii/runwitness/internal/runner"
)

const (
	railsHandledErrorRuleID = "rails.error.handled"
	runtimeNoErrorsRuleID   = "runtime.no_errors"
)

func Main(args []string) int {
	stripped, enabled, err := stripRailsFlag(args)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		fmt.Fprintln(os.Stderr, "usage: runwitness run [--label <name>] [--otel] [--rails] -- <command> [args...]")
		return 2
	}
	if !enabled {
		return runner.Main(args)
	}

	workingDirectory, err := os.Getwd()
	if err != nil {
		fmt.Fprintf(os.Stderr, "runwitness: get working directory for Rails adapter: %v\n", err)
		return 2
	}
	workingDirectory, err = filepath.Abs(workingDirectory)
	if err != nil {
		fmt.Fprintf(os.Stderr, "runwitness: resolve working directory for Rails adapter: %v\n", err)
		return 2
	}

	temporaryDirectory, err := os.MkdirTemp("", "runwitness-rails-")
	if err != nil {
		fmt.Fprintf(os.Stderr, "runwitness: prepare Rails adapter: %v\n", err)
		return 2
	}
	defer os.RemoveAll(temporaryDirectory)

	adapter, overrides, err := railsadapter.Prepare(
		temporaryDirectory,
		workingDirectory,
		os.Getenv("RUBYOPT"),
		os.Getenv("RUBYLIB"),
	)
	if err != nil {
		fmt.Fprintf(os.Stderr, "runwitness: prepare Rails adapter: %v\n", err)
		return 2
	}
	defer adapter.Close()

	beforeRuns, err := runDirectorySet(workingDirectory)
	if err != nil {
		fmt.Fprintf(os.Stderr, "runwitness: inspect Runs before Rails observation: %v\n", err)
		return 2
	}

	restoreEnvironment := applyEnvironment(overrides)
	coreExit := runner.Main(stripped)
	restoreEnvironment()

	runDirectory, found, err := newRunDirectory(workingDirectory, beforeRuns)
	if err != nil {
		fmt.Fprintf(os.Stderr, "runwitness: locate Rails-observed Run: %v\n", err)
		return 2
	}
	if !found {
		return coreExit
	}

	railEvidence, subscribed, collectErr := adapter.Collect()
	if collectErr != nil {
		if err := applyRailsAdapterError(runDirectory, "error", collectErr.Error()); err != nil {
			fmt.Fprintf(os.Stderr, "runwitness: record Rails adapter error: %v\n", err)
		}
		return 2
	}
	if !subscribed {
		message := "Rails.error reporter was not observed during target execution"
		if err := applyRailsAdapterError(runDirectory, "unavailable", message); err != nil {
			fmt.Fprintf(os.Stderr, "runwitness: record Rails adapter unavailability: %v\n", err)
		}
		return 2
	}

	resultExit, err := applyRailsEvidence(runDirectory, railEvidence, coreExit)
	if err != nil {
		if recordErr := applyRailsAdapterError(runDirectory, "error", err.Error()); recordErr != nil {
			fmt.Fprintf(os.Stderr, "runwitness: record Rails adapter error: %v\n", recordErr)
		}
		fmt.Fprintf(os.Stderr, "runwitness: collect Rails evidence: %v\n", err)
		return 2
	}
	return resultExit
}

func stripRailsFlag(args []string) ([]string, bool, error) {
	if len(args) == 0 || args[0] != "run" {
		return append([]string(nil), args...), false, nil
	}

	result := make([]string, 0, len(args))
	enabled := false
	beforeSeparator := true
	for _, argument := range args {
		if beforeSeparator && argument == "--" {
			beforeSeparator = false
			result = append(result, argument)
			continue
		}
		if beforeSeparator && argument == "--rails" {
			if enabled {
				return nil, false, errors.New("--rails may be specified only once")
			}
			enabled = true
			continue
		}
		result = append(result, argument)
	}
	return result, enabled, nil
}

func applyEnvironment(overrides map[string]string) func() {
	type previousValue struct {
		value string
		set   bool
	}
	previous := make(map[string]previousValue, len(overrides))
	for key, value := range overrides {
		oldValue, set := os.LookupEnv(key)
		previous[key] = previousValue{value: oldValue, set: set}
		_ = os.Setenv(key, value)
	}

	return func() {
		for key, value := range previous {
			if value.set {
				_ = os.Setenv(key, value.value)
			} else {
				_ = os.Unsetenv(key)
			}
		}
	}
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

func applyRailsEvidence(runDirectory string, evidence []railsadapter.Evidence, coreExit int) (int, error) {
	document, err := readRunDocument(runDirectory)
	if err != nil {
		return 2, err
	}

	run, ok := document["run"].(map[string]any)
	if !ok {
		return 2, errors.New("run.json has no Run object")
	}
	runID, _ := run["run_id"].(string)
	if runID == "" {
		return 2, errors.New("run.json has no Run ID")
	}

	evidencePath := filepath.Join(runDirectory, "evidence.jsonl")
	offset, err := evidenceRecordCount(evidencePath)
	if err != nil {
		return 2, err
	}
	if err := appendRailsEvidence(evidencePath, runID, offset, evidence); err != nil {
		return 2, err
	}

	appendAdapter(document, map[string]any{"name": "rails", "status": "ok"})

	findings := objectArray(document["findings"])
	newFindingIDs := make([]string, 0)
	for index, item := range evidence {
		if item.Kind != "rails.error" {
			continue
		}
		handled, _ := item.Attributes["error.handled"].(bool)
		if !handled {
			continue
		}

		errorClass := stringValue(item.Attributes["error.class"])
		errorMessage := stringValue(item.Attributes["error.message"])
		source := stringValue(item.Attributes["error.source"])
		path := stringValue(item.Attributes["error.location.path"])
		label := stringValue(item.Attributes["error.location.label"])
		findingID := stableFindingID(
			"finding.v1",
			"runtime.handled_error",
			railsHandledErrorRuleID,
			errorClass,
			source,
			path,
			label,
		)
		summary := "Rails.error reported a handled error"
		if errorClass != "" {
			summary += ": " + errorClass
		}
		if errorMessage != "" {
			summary += ": " + errorMessage
		}

		findings = append(findings, map[string]any{
			"finding_id":    findingID,
			"kind":          "runtime.handled_error",
			"severity":      "warning",
			"rule_id":       railsHandledErrorRuleID,
			"sources":       []string{"rails"},
			"summary":       summary,
			"evidence_refs": []string{evidenceRecordID(runID, offset+index)},
		})
		newFindingIDs = append(newFindingIDs, findingID)
	}
	document["findings"] = findings

	summary := ensureObject(document, "summary")
	summary["evidence_count"] = offset + len(evidence)
	summary["finding_count"] = len(findings)

	verdict := ensureObject(document, "verdict")
	if len(newFindingIDs) > 0 {
		mergeNoErrorsGate(verdict, newFindingIDs)
		if verdict["status"] != "error" {
			verdict["status"] = "fail"
		}
	}

	if err := writeRunDocument(runDirectory, document); err != nil {
		return 2, err
	}

	status, _ := verdict["status"].(string)
	switch status {
	case "pass", "warn":
		return 0, nil
	case "fail":
		return 1, nil
	case "error":
		return 2, nil
	default:
		return coreExit, nil
	}
}

func applyRailsAdapterError(runDirectory, status, message string) error {
	document, err := readRunDocument(runDirectory)
	if err != nil {
		return err
	}
	appendAdapter(document, map[string]any{
		"name":    "rails",
		"status":  status,
		"message": message,
	})
	verdict := ensureObject(document, "verdict")
	verdict["status"] = "error"
	verdict["message"] = message
	return writeRunDocument(runDirectory, document)
}

func appendRailsEvidence(path, runID string, offset int, evidence []railsadapter.Evidence) error {
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return fmt.Errorf("open evidence.jsonl for Rails evidence: %w", err)
	}
	encoder := json.NewEncoder(file)
	for index, item := range evidence {
		attributes := item.Attributes
		if attributes == nil {
			attributes = map[string]any{}
		}
		payload := item.Payload
		if payload == nil {
			payload = map[string]any{}
		}
		record := map[string]any{
			"schema_version": 1,
			"evidence_id":    evidenceRecordID(runID, offset+index),
			"run_id":         runID,
			"source":         "rails",
			"kind":           item.Kind,
			"observed_at":    item.ObservedAt.UTC().Format("2006-01-02T15:04:05.999999999Z07:00"),
			"attributes":     attributes,
			"payload":        payload,
		}
		if err := encoder.Encode(record); err != nil {
			_ = file.Close()
			return fmt.Errorf("write Rails evidence: %w", err)
		}
	}
	if err := file.Close(); err != nil {
		return fmt.Errorf("close Rails evidence stream: %w", err)
	}
	return nil
}

func evidenceRecordCount(path string) (int, error) {
	file, err := os.Open(path)
	if err != nil {
		return 0, fmt.Errorf("open evidence.jsonl: %w", err)
	}
	defer file.Close()

	count := 0
	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		if strings.TrimSpace(scanner.Text()) != "" {
			count++
		}
	}
	if err := scanner.Err(); err != nil {
		return 0, fmt.Errorf("read evidence.jsonl: %w", err)
	}
	return count, nil
}

func evidenceRecordID(runID string, index int) string {
	return fmt.Sprintf("ev_%s_%06d", strings.ReplaceAll(runID, "-", ""), index+1)
}

func stableFindingID(parts ...string) string {
	hash := sha256.New()
	for _, part := range parts {
		_ = binary.Write(hash, binary.BigEndian, uint64(len(part)))
		_, _ = hash.Write([]byte(part))
	}
	digest := hash.Sum(nil)
	return "rwf_" + hex.EncodeToString(digest[:16])
}

func mergeNoErrorsGate(verdict map[string]any, findingIDs []string) {
	gates := objectArray(verdict["gates"])
	for _, gate := range gates {
		if stringValue(gate["rule_id"]) != runtimeNoErrorsRuleID {
			continue
		}
		existing := stringArray(gate["finding_ids"])
		merged := appendUnique(existing, findingIDs...)
		gate["finding_ids"] = merged
		gate["message"] = fmt.Sprintf("%d runtime error finding(s) observed", len(merged))
		verdict["gates"] = gates
		return
	}

	gates = append(gates, map[string]any{
		"rule_id":     runtimeNoErrorsRuleID,
		"action":      "fail",
		"outcome":     "triggered",
		"finding_ids": append([]string(nil), findingIDs...),
		"message":     fmt.Sprintf("%d runtime error finding(s) observed", len(findingIDs)),
	})
	verdict["gates"] = gates
}

func appendAdapter(document map[string]any, adapter map[string]any) {
	adapters := objectArray(document["adapters"])
	adapters = append(adapters, adapter)
	document["adapters"] = adapters
}

func readRunDocument(runDirectory string) (map[string]any, error) {
	payload, err := os.ReadFile(filepath.Join(runDirectory, "run.json"))
	if err != nil {
		return nil, fmt.Errorf("read run.json: %w", err)
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
		return fmt.Errorf("encode run.json after Rails observation: %w", err)
	}
	payload = append(payload, '\n')
	temporary := filepath.Join(runDirectory, "run.json.rails.tmp")
	final := filepath.Join(runDirectory, "run.json")
	if err := os.WriteFile(temporary, payload, 0o644); err != nil {
		return fmt.Errorf("write Rails-updated run.json: %w", err)
	}
	if err := os.Rename(temporary, final); err != nil {
		return fmt.Errorf("publish Rails-updated run.json: %w", err)
	}
	return nil
}

func ensureObject(document map[string]any, key string) map[string]any {
	if object, ok := document[key].(map[string]any); ok {
		return object
	}
	object := map[string]any{}
	document[key] = object
	return object
}

func objectArray(value any) []map[string]any {
	array, ok := value.([]any)
	if !ok {
		if typed, ok := value.([]map[string]any); ok {
			return append([]map[string]any(nil), typed...)
		}
		return []map[string]any{}
	}
	result := make([]map[string]any, 0, len(array))
	for _, item := range array {
		if object, ok := item.(map[string]any); ok {
			result = append(result, object)
		}
	}
	return result
}

func stringArray(value any) []string {
	switch array := value.(type) {
	case []string:
		return append([]string(nil), array...)
	case []any:
		result := make([]string, 0, len(array))
		for _, item := range array {
			if text, ok := item.(string); ok {
				result = append(result, text)
			}
		}
		return result
	default:
		return []string{}
	}
}

func appendUnique(values []string, additions ...string) []string {
	seen := make(map[string]struct{}, len(values)+len(additions))
	result := make([]string, 0, len(values)+len(additions))
	for _, value := range append(append([]string(nil), values...), additions...) {
		if _, ok := seen[value]; ok {
			continue
		}
		seen[value] = struct{}{}
		result = append(result, value)
	}
	return result
}

func stringValue(value any) string {
	text, _ := value.(string)
	return text
}
