package baselineintegration

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"

	"github.com/sergii/runwitness/internal/railsqueryintegration"
)

var uuidv7Pattern = regexp.MustCompile(`^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-7[0-9a-fA-F]{3}-[89abAB][0-9a-fA-F]{3}-[0-9a-fA-F]{12}$`)

type findingDiff struct {
	New       []string `json:"new"`
	Resolved  []string `json:"resolved"`
	Unchanged []string `json:"unchanged"`
	Regressed []string `json:"regressed"`
	Improved  []string `json:"improved"`
}

func Main(args []string) int {
	stripped, baselineID, requested, err := stripBaseline(args)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		fmt.Fprintln(os.Stderr, "usage: runwitness run [--label <name>] [--otel] [--rails] [--baseline <run_id>] -- <command> [args...]")
		return 2
	}
	if !requested {
		return railsqueryintegration.Main(args)
	}
	if !uuidv7Pattern.MatchString(baselineID) {
		fmt.Fprintf(os.Stderr, "runwitness: baseline run ID %q is not a UUIDv7\n", baselineID)
		return 2
	}

	workingDirectory, err := os.Getwd()
	if err != nil {
		fmt.Fprintf(os.Stderr, "runwitness: get working directory for baseline comparison: %v\n", err)
		return 2
	}
	workingDirectory, err = filepath.Abs(workingDirectory)
	if err != nil {
		fmt.Fprintf(os.Stderr, "runwitness: resolve working directory for baseline comparison: %v\n", err)
		return 2
	}

	baseline, err := loadBaseline(workingDirectory, baselineID)
	if err != nil {
		fmt.Fprintf(os.Stderr, "runwitness: baseline %s: %v\n", baselineID, err)
		return 2
	}

	beforeRuns, err := runDirectorySet(workingDirectory)
	if err != nil {
		fmt.Fprintf(os.Stderr, "runwitness: inspect Runs before baseline comparison: %v\n", err)
		return 2
	}

	resultExit := railsqueryintegration.Main(stripped)

	runDirectory, found, err := newRunDirectory(workingDirectory, beforeRuns)
	if err != nil {
		fmt.Fprintf(os.Stderr, "runwitness: locate baseline-compared Run: %v\n", err)
		return 2
	}
	if !found {
		return resultExit
	}

	if err := applyBaseline(runDirectory, baselineID, baseline); err != nil {
		message := fmt.Sprintf("baseline comparison failed: %v", err)
		_ = markComparisonError(runDirectory, baselineID, message)
		fmt.Fprintf(os.Stderr, "runwitness: %s\n", message)
		return 2
	}

	return resultExit
}

func stripBaseline(args []string) ([]string, string, bool, error) {
	if len(args) == 0 || args[0] != "run" {
		return append([]string(nil), args...), "", false, nil
	}

	result := make([]string, 0, len(args))
	var baselineID string
	requested := false
	beforeSeparator := true

	for index := 0; index < len(args); index++ {
		argument := args[index]
		if beforeSeparator && argument == "--" {
			beforeSeparator = false
			result = append(result, argument)
			continue
		}
		if beforeSeparator && argument == "--baseline" {
			if requested {
				return nil, "", false, errors.New("--baseline may be specified only once")
			}
			if index+1 >= len(args) || args[index+1] == "--" || args[index+1] == "" {
				return nil, "", false, errors.New("--baseline requires a non-empty Run ID")
			}
			baselineID = args[index+1]
			requested = true
			index++
			continue
		}
		result = append(result, argument)
	}

	return result, baselineID, requested, nil
}

func loadBaseline(workingDirectory, baselineID string) (map[string]any, error) {
	path := filepath.Join(workingDirectory, ".runwitness", "runs", baselineID, "run.json")
	document, err := readRunDocument(path)
	if err != nil {
		return nil, err
	}

	run, ok := document["run"].(map[string]any)
	if !ok {
		return nil, errors.New("run.json has no Run object")
	}
	actualID, _ := run["run_id"].(string)
	if actualID == "" {
		return nil, errors.New("run.json has no Run ID")
	}
	if actualID != baselineID {
		return nil, fmt.Errorf("run.json Run ID %q does not match requested baseline", actualID)
	}
	if _, err := findingIDs(document); err != nil {
		return nil, err
	}

	return document, nil
}

func applyBaseline(runDirectory, baselineID string, baseline map[string]any) error {
	currentPath := filepath.Join(runDirectory, "run.json")
	current, err := readRunDocument(currentPath)
	if err != nil {
		return err
	}

	current["baseline"] = map[string]any{"run_id": baselineID}

	verdict, ok := current["verdict"].(map[string]any)
	if !ok {
		return errors.New("current run.json has no verdict object")
	}
	status, _ := verdict["status"].(string)
	if status == "error" {
		delete(current, "diff")
		return writeRunDocument(runDirectory, current)
	}

	diff, err := classifyFindings(baseline, current)
	if err != nil {
		return err
	}
	current["diff"] = map[string]any{
		"new":       diff.New,
		"resolved":  diff.Resolved,
		"unchanged": diff.Unchanged,
		"regressed": diff.Regressed,
		"improved":  diff.Improved,
	}
	return writeRunDocument(runDirectory, current)
}

func classifyFindings(baselineDocument, currentDocument map[string]any) (findingDiff, error) {
	baseline, err := findingsByID(baselineDocument)
	if err != nil {
		return findingDiff{}, fmt.Errorf("read baseline Findings: %w", err)
	}
	current, err := findingsByID(currentDocument)
	if err != nil {
		return findingDiff{}, fmt.Errorf("read current Findings: %w", err)
	}

	diff := findingDiff{
		New:       make([]string, 0),
		Resolved:  make([]string, 0),
		Unchanged: make([]string, 0),
		Regressed: make([]string, 0),
		Improved:  make([]string, 0),
	}

	for findingID, currentFinding := range current {
		baselineFinding, existed := baseline[findingID]
		if !existed {
			diff.New = append(diff.New, findingID)
			continue
		}

		if isQueryCountFinding(baselineFinding) && isQueryCountFinding(currentFinding) {
			baselineCount, err := evidenceReferenceCount(baselineFinding)
			if err != nil {
				return findingDiff{}, fmt.Errorf("baseline query-count Finding %s: %w", findingID, err)
			}
			currentCount, err := evidenceReferenceCount(currentFinding)
			if err != nil {
				return findingDiff{}, fmt.Errorf("current query-count Finding %s: %w", findingID, err)
			}
			if baselineCount <= 0 || currentCount <= 0 {
				return findingDiff{}, fmt.Errorf("query-count Finding %s must have at least one Evidence reference", findingID)
			}

			delta := currentCount - baselineCount
			deltaPercent := (float64(delta) / float64(baselineCount)) * 100.0
			currentFinding["comparison"] = map[string]any{
				"baseline":      baselineCount,
				"current":       currentCount,
				"delta":         delta,
				"delta_percent": deltaPercent,
				"unit":          "queries",
			}

			switch {
			case currentCount > baselineCount:
				currentFinding["severity"] = "warning"
				diff.Regressed = append(diff.Regressed, findingID)
			case currentCount < baselineCount:
				currentFinding["severity"] = "info"
				diff.Improved = append(diff.Improved, findingID)
			default:
				currentFinding["severity"] = "info"
				diff.Unchanged = append(diff.Unchanged, findingID)
			}
			continue
		}

		diff.Unchanged = append(diff.Unchanged, findingID)
	}

	for findingID := range baseline {
		if _, stillPresent := current[findingID]; !stillPresent {
			diff.Resolved = append(diff.Resolved, findingID)
		}
	}

	sort.Strings(diff.New)
	sort.Strings(diff.Resolved)
	sort.Strings(diff.Unchanged)
	sort.Strings(diff.Regressed)
	sort.Strings(diff.Improved)
	return diff, nil
}

func isQueryCountFinding(finding map[string]any) bool {
	kind, _ := finding["kind"].(string)
	ruleID, _ := finding["rule_id"].(string)
	return kind == "database.query_count" && ruleID == "rails.sql.query_count"
}

func evidenceReferenceCount(finding map[string]any) (int, error) {
	raw, ok := finding["evidence_refs"]
	if !ok {
		return 0, errors.New("Finding has no evidence_refs")
	}
	if typed, ok := raw.([]string); ok {
		return len(typed), nil
	}
	items, ok := raw.([]any)
	if !ok {
		return 0, errors.New("Finding evidence_refs is not an array")
	}
	for _, item := range items {
		if _, ok := item.(string); !ok {
			return 0, errors.New("Finding evidence_refs contains a non-string value")
		}
	}
	return len(items), nil
}

func findingsByID(document map[string]any) (map[string]map[string]any, error) {
	raw, ok := document["findings"]
	if !ok {
		return nil, errors.New("run.json has no Findings array")
	}

	result := make(map[string]map[string]any)
	if typed, ok := raw.([]map[string]any); ok {
		for _, finding := range typed {
			findingID, _ := finding["finding_id"].(string)
			if findingID == "" {
				return nil, errors.New("run.json contains a Finding without finding_id")
			}
			if _, duplicate := result[findingID]; duplicate {
				return nil, fmt.Errorf("run.json contains duplicate finding_id %q", findingID)
			}
			result[findingID] = finding
		}
		return result, nil
	}

	items, ok := raw.([]any)
	if !ok {
		return nil, errors.New("run.json Findings is not an array")
	}
	for _, item := range items {
		finding, ok := item.(map[string]any)
		if !ok {
			return nil, errors.New("run.json contains a non-object Finding")
		}
		findingID, _ := finding["finding_id"].(string)
		if findingID == "" {
			return nil, errors.New("run.json contains a Finding without finding_id")
		}
		if _, duplicate := result[findingID]; duplicate {
			return nil, fmt.Errorf("run.json contains duplicate finding_id %q", findingID)
		}
		result[findingID] = finding
	}
	return result, nil
}

func classifyFindingIDs(baselineIDs, currentIDs []string) findingDiff {
	baseline := stringSet(baselineIDs)
	current := stringSet(currentIDs)

	diff := findingDiff{
		New:       make([]string, 0),
		Resolved:  make([]string, 0),
		Unchanged: make([]string, 0),
		Regressed: make([]string, 0),
		Improved:  make([]string, 0),
	}

	for findingID := range current {
		if _, existed := baseline[findingID]; existed {
			diff.Unchanged = append(diff.Unchanged, findingID)
		} else {
			diff.New = append(diff.New, findingID)
		}
	}
	for findingID := range baseline {
		if _, stillPresent := current[findingID]; !stillPresent {
			diff.Resolved = append(diff.Resolved, findingID)
		}
	}

	sort.Strings(diff.New)
	sort.Strings(diff.Resolved)
	sort.Strings(diff.Unchanged)
	return diff
}

func findingIDs(document map[string]any) ([]string, error) {
	findings, err := findingsByID(document)
	if err != nil {
		return nil, err
	}
	ids := make([]string, 0, len(findings))
	for findingID := range findings {
		ids = append(ids, findingID)
	}
	return ids, nil
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
		return fmt.Errorf("encode run.json after baseline comparison: %w", err)
	}
	payload = append(payload, '\n')

	temporary := filepath.Join(runDirectory, "run.json.baseline.tmp")
	final := filepath.Join(runDirectory, "run.json")
	if err := os.WriteFile(temporary, payload, 0o644); err != nil {
		return fmt.Errorf("write baseline-updated run.json: %w", err)
	}
	if err := os.Rename(temporary, final); err != nil {
		return fmt.Errorf("publish baseline-updated run.json: %w", err)
	}
	return nil
}

func markComparisonError(runDirectory, baselineID, message string) error {
	document, err := readRunDocument(filepath.Join(runDirectory, "run.json"))
	if err != nil {
		return err
	}
	document["baseline"] = map[string]any{"run_id": baselineID}
	delete(document, "diff")
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
