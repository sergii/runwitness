package mcpintegration

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/sergii/runwitness/internal/runner"
)

const defaultListLimit = 20

var uuidv7Pattern = regexp.MustCompile(`^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-7[0-9a-fA-F]{3}-[89abAB][0-9a-fA-F]{3}-[0-9a-fA-F]{12}$`)

type listRunsInput struct {
	Limit *int `json:"limit,omitempty" jsonschema:"maximum number of Runs to return, from 1 to 100"`
}

type listRunsOutput struct {
	Runs []runSummary `json:"runs"`
}

type getRunInput struct {
	RunID string `json:"run_id" jsonschema:"UUIDv7 identifier of the Run to retrieve"`
}

type getRunOutput struct {
	Run map[string]any `json:"run"`
}

type runSummary struct {
	RunID     string         `json:"run_id"`
	StartedAt string         `json:"started_at"`
	Label     string         `json:"label,omitempty"`
	Command   commandSummary `json:"command"`
	Verdict   verdictSummary `json:"verdict"`
	Summary   countSummary   `json:"summary"`
}

type commandSummary struct {
	Argv []string `json:"argv"`
}

type verdictSummary struct {
	Status string `json:"status"`
}

type countSummary struct {
	EvidenceCount int `json:"evidence_count"`
	FindingCount  int `json:"finding_count"`
}

type storedRunSummary struct {
	SchemaVersion int `json:"schema_version"`
	Run           struct {
		RunID     string `json:"run_id"`
		StartedAt string `json:"started_at"`
		Label     string `json:"label,omitempty"`
		Command   struct {
			Argv []string `json:"argv"`
		} `json:"command"`
	} `json:"run"`
	Summary struct {
		EvidenceCount int `json:"evidence_count"`
		FindingCount  int `json:"finding_count"`
	} `json:"summary"`
	Verdict struct {
		Status string `json:"status"`
	} `json:"verdict"`
}

type orderedRunSummary struct {
	summary runSummary
	started time.Time
}

func Main() int {
	workingDirectory, err := os.Getwd()
	if err != nil {
		fmt.Fprintf(os.Stderr, "runwitness: get MCP working directory: %v\n", err)
		return 2
	}
	workingDirectory, err = filepath.Abs(workingDirectory)
	if err != nil {
		fmt.Fprintf(os.Stderr, "runwitness: resolve MCP working directory: %v\n", err)
		return 2
	}

	server := mcp.NewServer(
		&mcp.Implementation{Name: "runwitness", Version: runner.Version},
		&mcp.ServerOptions{Capabilities: &mcp.ServerCapabilities{}},
	)

	mcp.AddTool(
		server,
		&mcp.Tool{
			Name:        "list_runs",
			Description: "List local RunWitness Runs, newest first.",
		},
		listRunsHandler(workingDirectory),
	)
	mcp.AddTool(
		server,
		&mcp.Tool{
			Name:        "get_run",
			Description: "Get one canonical local RunWitness run.json document by Run ID.",
		},
		getRunHandler(workingDirectory),
	)
	mcp.AddTool(
		server,
		&mcp.Tool{
			Name:        "get_evidence",
			Description: "Get one canonical normalized Evidence record from a local Run by Run ID and Evidence ID.",
		},
		getEvidenceHandler(workingDirectory),
	)

	if err := server.Run(context.Background(), &mcp.StdioTransport{}); err != nil {
		fmt.Fprintf(os.Stderr, "runwitness: MCP server: %v\n", err)
		return 2
	}
	return 0
}

func listRunsHandler(workingDirectory string) func(context.Context, *mcp.CallToolRequest, listRunsInput) (*mcp.CallToolResult, listRunsOutput, error) {
	return func(_ context.Context, _ *mcp.CallToolRequest, input listRunsInput) (*mcp.CallToolResult, listRunsOutput, error) {
		limit := defaultListLimit
		if input.Limit != nil {
			limit = *input.Limit
		}
		if limit < 1 || limit > 100 {
			return nil, listRunsOutput{}, fmt.Errorf("limit must be between 1 and 100")
		}

		runs, err := listRuns(workingDirectory, limit)
		if err != nil {
			return nil, listRunsOutput{}, err
		}
		return nil, listRunsOutput{Runs: runs}, nil
	}
}

func getRunHandler(workingDirectory string) func(context.Context, *mcp.CallToolRequest, getRunInput) (*mcp.CallToolResult, getRunOutput, error) {
	return func(_ context.Context, _ *mcp.CallToolRequest, input getRunInput) (*mcp.CallToolResult, getRunOutput, error) {
		if !uuidv7Pattern.MatchString(input.RunID) {
			return nil, getRunOutput{}, fmt.Errorf("run_id must be a UUIDv7")
		}

		document, _, err := readCanonicalRun(workingDirectory, input.RunID)
		if err != nil {
			return nil, getRunOutput{}, err
		}
		return nil, getRunOutput{Run: document}, nil
	}
}

func listRuns(workingDirectory string, limit int) ([]runSummary, error) {
	runsRoot := filepath.Join(workingDirectory, ".runwitness", "runs")
	entries, err := os.ReadDir(runsRoot)
	if errors.Is(err, os.ErrNotExist) {
		return []runSummary{}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read Run store: %w", err)
	}

	ordered := make([]orderedRunSummary, 0, len(entries))
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		runID := entry.Name()
		if !uuidv7Pattern.MatchString(runID) {
			return nil, fmt.Errorf("Run store contains invalid Run directory %q", runID)
		}

		_, stored, err := readCanonicalRun(workingDirectory, runID)
		if err != nil {
			return nil, fmt.Errorf("read Run %s: %w", runID, err)
		}
		started, err := time.Parse(time.RFC3339Nano, stored.Run.StartedAt)
		if err != nil {
			return nil, fmt.Errorf("Run %s has invalid started_at: %w", runID, err)
		}

		ordered = append(ordered, orderedRunSummary{
			started: started,
			summary: runSummary{
				RunID:     stored.Run.RunID,
				StartedAt: stored.Run.StartedAt,
				Label:     stored.Run.Label,
				Command:   commandSummary{Argv: append([]string(nil), stored.Run.Command.Argv...)},
				Verdict:   verdictSummary{Status: stored.Verdict.Status},
				Summary: countSummary{
					EvidenceCount: stored.Summary.EvidenceCount,
					FindingCount:  stored.Summary.FindingCount,
				},
			},
		})
	}

	sort.Slice(ordered, func(i, j int) bool {
		if ordered[i].started.Equal(ordered[j].started) {
			return ordered[i].summary.RunID > ordered[j].summary.RunID
		}
		return ordered[i].started.After(ordered[j].started)
	})

	if len(ordered) > limit {
		ordered = ordered[:limit]
	}
	result := make([]runSummary, len(ordered))
	for index := range ordered {
		result[index] = ordered[index].summary
	}
	return result, nil
}

func readCanonicalRun(workingDirectory, expectedRunID string) (map[string]any, storedRunSummary, error) {
	runDirectory := filepath.Join(workingDirectory, ".runwitness", "runs", expectedRunID)
	runInfo, err := os.Lstat(runDirectory)
	if err != nil {
		return nil, storedRunSummary{}, fmt.Errorf("open Run %s: %w", expectedRunID, err)
	}
	if !runInfo.IsDir() || runInfo.Mode()&os.ModeSymlink != 0 {
		return nil, storedRunSummary{}, fmt.Errorf("Run %s is not a local Run directory", expectedRunID)
	}

	runPath := filepath.Join(runDirectory, "run.json")
	fileInfo, err := os.Lstat(runPath)
	if err != nil {
		return nil, storedRunSummary{}, fmt.Errorf("open run.json: %w", err)
	}
	if !fileInfo.Mode().IsRegular() || fileInfo.Mode()&os.ModeSymlink != 0 {
		return nil, storedRunSummary{}, errors.New("run.json is not a regular local file")
	}

	payload, err := os.ReadFile(runPath)
	if err != nil {
		return nil, storedRunSummary{}, fmt.Errorf("read run.json: %w", err)
	}

	var document map[string]any
	if err := json.Unmarshal(payload, &document); err != nil {
		return nil, storedRunSummary{}, fmt.Errorf("decode run.json: %w", err)
	}
	var stored storedRunSummary
	if err := json.Unmarshal(payload, &stored); err != nil {
		return nil, storedRunSummary{}, fmt.Errorf("decode Run summary: %w", err)
	}
	if err := validateStoredRun(stored, expectedRunID); err != nil {
		return nil, storedRunSummary{}, err
	}
	return document, stored, nil
}

func validateStoredRun(stored storedRunSummary, expectedRunID string) error {
	if stored.SchemaVersion <= 0 {
		return errors.New("run.json has no valid schema_version")
	}
	if stored.Run.RunID != expectedRunID {
		return fmt.Errorf("run.json Run ID %q does not match requested Run %q", stored.Run.RunID, expectedRunID)
	}
	if stored.Run.StartedAt == "" {
		return errors.New("run.json has no started_at")
	}
	if len(stored.Run.Command.Argv) == 0 {
		return errors.New("run.json has no command argv")
	}
	switch stored.Verdict.Status {
	case "pass", "warn", "fail", "error":
	default:
		return fmt.Errorf("run.json has invalid verdict status %q", stored.Verdict.Status)
	}
	if stored.Summary.EvidenceCount < 0 || stored.Summary.FindingCount < 0 {
		return errors.New("run.json has invalid summary counts")
	}
	return nil
}
