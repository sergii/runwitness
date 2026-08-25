package railsqueryintegration

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

	"github.com/sergii/runwitness/internal/railsintegration"
)

const railsSQLQueryCountRuleID = "rails.sql.query_count"

func Main(args []string) int {
	if !railsRequested(args) {
		return railsintegration.Main(args)
	}

	workingDirectory, err := os.Getwd()
	if err != nil {
		fmt.Fprintf(os.Stderr, "runwitness: get working directory for Rails SQL findings: %v\n", err)
		return 2
	}
	workingDirectory, err = filepath.Abs(workingDirectory)
	if err != nil {
		fmt.Fprintf(os.Stderr, "runwitness: resolve working directory for Rails SQL findings: %v\n", err)
		return 2
	}

	beforeRuns, err := runDirectorySet(workingDirectory)
	if err != nil {
		fmt.Fprintf(os.Stderr, "runwitness: inspect Runs before Rails SQL findings: %v\n", err)
		return 2
	}

	resultExit := railsintegration.Main(args)

	runDirectory, found, err := newRunDirectory(workingDirectory, beforeRuns)
	if err != nil {
		fmt.Fprintf(os.Stderr, "runwitness: locate Rails SQL Run: %v\n", err)
		return 2
	}
	if !found {
		return resultExit
	}

	if err := applyQueryFindings(runDirectory); err != nil {
		message := fmt.Sprintf("Rails SQL finding derivation failed: %v", err)
		_ = markRunError(runDirectory, message)
		fmt.Fprintf(os.Stderr, "runwitness: %s\n", message)
		return 2
	}
	return resultExit
}

func railsRequested(args []string) bool {
	if len(args) == 0 || args[0] != "run" {
		return false
	}
	for _, argument := range args[1:] {
		if argument == "--" {
			return false
		}
		if argument == "--rails" {
			return true
		}
	}
	return false
}

type evidenceRecord struct {
	EvidenceID string         `json:"evidence_id"`
	Source     string         `json:"source"`
	Kind       string         `json:"kind"`
	Attributes map[string]any `json:"attributes"`
}

type queryGroup struct {
	statement    string
	evidenceRefs []string
}

func applyQueryFindings(runDirectory string) error {
	document, err := readRunDocument(filepath.Join(runDirectory, "run.json"))
	if err != nil {
		return err
	}

	groups, err := readQueryGroups(filepath.Join(runDirectory, "evidence.jsonl"))
	if err != nil {
		return err
	}
	if len(groups) == 0 {
		return nil
	}

	findings, err := findingArray(document["findings"])
	if err != nil {
		return err
	}

	statements := make([]string, 0, len(groups))
	for statement := range groups {
		statements = append(statements, statement)
	}
	sort.Strings(statements)

	for _, statement := range statements {
		group := groups[statement]
		findingID := stableFindingID(
			"finding.v1",
			"database.query_count",
			railsSQLQueryCountRuleID,
			statement,
		)
		findings = append(findings, map[string]any{
			"finding_id":    findingID,
			"kind":          "database.query_count",
			"severity":      "info",
			"rule_id":       railsSQLQueryCountRuleID,
			"sources":       []string{"rails"},
			"summary":       querySummary(statement, len(group.evidenceRefs)),
			"evidence_refs": append([]string(nil), group.evidenceRefs...),
		})
	}
	document["findings"] = findings

	summary, ok := document["summary"].(map[string]any)
	if !ok {
		return errors.New("run.json has no summary object")
	}
	summary["finding_count"] = len(findings)

	return writeRunDocument(runDirectory, document)
}

func readQueryGroups(path string) (map[string]*queryGroup, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("open evidence.jsonl for Rails SQL findings: %w", err)
	}
	defer file.Close()

	groups := make(map[string]*queryGroup)
	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, 64*1024), 4*1024*1024)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		var record evidenceRecord
		if err := json.Unmarshal([]byte(line), &record); err != nil {
			return nil, fmt.Errorf("decode Evidence for Rails SQL findings: %w", err)
		}
		if record.Source != "rails" || record.Kind != "rails.sql" {
			continue
		}
		statement, _ := record.Attributes["sql.statement"].(string)
		statement = strings.Join(strings.Fields(statement), " ")
		if statement == "" {
			return nil, errors.New("rails.sql Evidence has no normalized sql.statement")
		}
		if record.EvidenceID == "" {
			return nil, errors.New("rails.sql Evidence has no evidence_id")
		}
		group := groups[statement]
		if group == nil {
			group = &queryGroup{statement: statement, evidenceRefs: make([]string, 0, 1)}
			groups[statement] = group
		}
		group.evidenceRefs = append(group.evidenceRefs, record.EvidenceID)
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("read Evidence for Rails SQL findings: %w", err)
	}
	return groups, nil
}

func querySummary(statement string, count int) string {
	return fmt.Sprintf("Rails SQL query observed %d time(s): %s", count, statement)
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

func findingArray(raw any) ([]map[string]any, error) {
	if typed, ok := raw.([]map[string]any); ok {
		return append([]map[string]any(nil), typed...), nil
	}
	items, ok := raw.([]any)
	if !ok {
		return nil, errors.New("run.json Findings is not an array")
	}
	result := make([]map[string]any, 0, len(items))
	for _, item := range items {
		finding, ok := item.(map[string]any)
		if !ok {
			return nil, errors.New("run.json contains a non-object Finding")
		}
		result = append(result, finding)
	}
	return result, nil
}

func readRunDocument(path string) (map[string]any, error) {
	payload, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read run.json for Rails SQL findings: %w", err)
	}
	var document map[string]any
	if err := json.Unmarshal(payload, &document); err != nil {
		return nil, fmt.Errorf("decode run.json for Rails SQL findings: %w", err)
	}
	return document, nil
}

func writeRunDocument(runDirectory string, document map[string]any) error {
	payload, err := json.MarshalIndent(document, "", "  ")
	if err != nil {
		return fmt.Errorf("encode run.json after Rails SQL findings: %w", err)
	}
	payload = append(payload, '\n')

	temporary := filepath.Join(runDirectory, "run.json.rails-sql.tmp")
	final := filepath.Join(runDirectory, "run.json")
	if err := os.WriteFile(temporary, payload, 0o644); err != nil {
		return fmt.Errorf("write Rails-SQL-updated run.json: %w", err)
	}
	if err := os.Rename(temporary, final); err != nil {
		return fmt.Errorf("publish Rails-SQL-updated run.json: %w", err)
	}
	return nil
}

func markRunError(runDirectory, message string) error {
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
