package mcpintegration

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

var (
	evidenceIDPattern   = regexp.MustCompile(`^ev_[A-Za-z0-9._-]+$`)
	evidenceKindPattern = regexp.MustCompile(`^[a-z0-9]+(?:[._-][a-z0-9]+)+$`)
)

type getEvidenceInput struct {
	RunID      string `json:"run_id" jsonschema:"UUIDv7 identifier of the Run containing the Evidence"`
	EvidenceID string `json:"evidence_id" jsonschema:"Evidence v1 identifier to retrieve"`
}

type getEvidenceOutput struct {
	Evidence map[string]any `json:"evidence"`
}

type storedEvidence struct {
	SchemaVersion int            `json:"schema_version"`
	EvidenceID    string         `json:"evidence_id"`
	RunID         string         `json:"run_id"`
	Source        string         `json:"source"`
	Kind          string         `json:"kind"`
	ObservedAt    string         `json:"observed_at"`
	Attributes    map[string]any `json:"attributes"`
	Payload       map[string]any `json:"payload"`
}

func getEvidenceHandler(workingDirectory string) func(context.Context, *mcp.CallToolRequest, getEvidenceInput) (*mcp.CallToolResult, getEvidenceOutput, error) {
	return func(_ context.Context, _ *mcp.CallToolRequest, input getEvidenceInput) (*mcp.CallToolResult, getEvidenceOutput, error) {
		if !uuidv7Pattern.MatchString(input.RunID) {
			return nil, getEvidenceOutput{}, fmt.Errorf("run_id must be a UUIDv7")
		}
		if !evidenceIDPattern.MatchString(input.EvidenceID) {
			return nil, getEvidenceOutput{}, fmt.Errorf("evidence_id is invalid")
		}

		evidence, err := readCanonicalEvidence(workingDirectory, input.RunID, input.EvidenceID)
		if err != nil {
			return nil, getEvidenceOutput{}, err
		}
		return nil, getEvidenceOutput{Evidence: evidence}, nil
	}
}

func readCanonicalEvidence(workingDirectory, expectedRunID, expectedEvidenceID string) (map[string]any, error) {
	if _, _, err := readCanonicalRun(workingDirectory, expectedRunID); err != nil {
		return nil, err
	}

	evidencePath := filepath.Join(workingDirectory, ".runwitness", "runs", expectedRunID, "evidence.jsonl")
	fileInfo, err := os.Lstat(evidencePath)
	if err != nil {
		return nil, fmt.Errorf("open evidence.jsonl: %w", err)
	}
	if !fileInfo.Mode().IsRegular() || fileInfo.Mode()&os.ModeSymlink != 0 {
		return nil, errors.New("evidence.jsonl is not a regular local file")
	}

	file, err := os.Open(evidencePath)
	if err != nil {
		return nil, fmt.Errorf("read evidence.jsonl: %w", err)
	}
	defer file.Close()

	decoder := json.NewDecoder(file)
	seen := make(map[string]struct{})
	var found map[string]any

	for recordIndex := 0; ; recordIndex++ {
		var raw json.RawMessage
		if err := decoder.Decode(&raw); err != nil {
			if errors.Is(err, io.EOF) {
				break
			}
			return nil, fmt.Errorf("decode evidence.jsonl record %d: %w", recordIndex+1, err)
		}

		var document map[string]any
		if err := json.Unmarshal(raw, &document); err != nil {
			return nil, fmt.Errorf("decode Evidence record %d: %w", recordIndex+1, err)
		}
		var stored storedEvidence
		if err := json.Unmarshal(raw, &stored); err != nil {
			return nil, fmt.Errorf("decode Evidence identity %d: %w", recordIndex+1, err)
		}
		if err := validateStoredEvidence(stored, expectedRunID); err != nil {
			return nil, fmt.Errorf("Evidence record %d: %w", recordIndex+1, err)
		}
		if _, duplicate := seen[stored.EvidenceID]; duplicate {
			return nil, fmt.Errorf("evidence.jsonl contains duplicate evidence_id %q", stored.EvidenceID)
		}
		seen[stored.EvidenceID] = struct{}{}

		if stored.EvidenceID == expectedEvidenceID {
			found = document
		}
	}

	if found == nil {
		return nil, fmt.Errorf("Evidence %s was not found in Run %s", expectedEvidenceID, expectedRunID)
	}
	return found, nil
}

func validateStoredEvidence(stored storedEvidence, expectedRunID string) error {
	if stored.SchemaVersion != 1 {
		return fmt.Errorf("invalid schema_version %d", stored.SchemaVersion)
	}
	if !evidenceIDPattern.MatchString(stored.EvidenceID) {
		return fmt.Errorf("invalid evidence_id %q", stored.EvidenceID)
	}
	if stored.RunID != expectedRunID {
		return fmt.Errorf("run_id %q does not match requested Run %q", stored.RunID, expectedRunID)
	}
	if stored.Source == "" {
		return errors.New("source must be non-empty")
	}
	if !evidenceKindPattern.MatchString(stored.Kind) {
		return fmt.Errorf("invalid kind %q", stored.Kind)
	}
	if _, err := time.Parse(time.RFC3339Nano, stored.ObservedAt); err != nil {
		return fmt.Errorf("invalid observed_at: %w", err)
	}
	if stored.Attributes == nil {
		return errors.New("attributes must be an object")
	}
	if stored.Payload == nil {
		return errors.New("payload must be an object")
	}
	return nil
}
