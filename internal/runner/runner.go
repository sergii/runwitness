package runner

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

const Version = "0.0.1-dev"

type Document struct {
	SchemaVersion int       `json:"schema_version"`
	Run           Run       `json:"run"`
	Adapters      []Adapter `json:"adapters"`
	Summary       Summary   `json:"summary"`
	Findings      []Finding `json:"findings"`
	Verdict       Verdict   `json:"verdict"`
}

type Run struct {
	RunID            string      `json:"run_id"`
	Environment      string      `json:"environment"`
	StartedAt        string      `json:"started_at"`
	FinishedAt       string      `json:"finished_at"`
	DurationMS       int64       `json:"duration_ms"`
	RunnerVersion    string      `json:"runner_version"`
	WorkingDirectory string      `json:"working_directory"`
	Command          Command     `json:"command"`
	Process          Process     `json:"process"`
	Repository       *Repository `json:"repository,omitempty"`
	Git              *Git        `json:"git,omitempty"`
}

type Command struct {
	Argv []string `json:"argv"`
}

type Process struct {
	ExitCode *int `json:"exit_code"`
}

type Repository struct {
	Root string `json:"root"`
}

type Git struct {
	Branch *string  `json:"branch,omitempty"`
	Before GitState `json:"before"`
	After  GitState `json:"after"`
}

type GitState struct {
	HeadSHA  string  `json:"head_sha"`
	Dirty    bool    `json:"dirty"`
	DiffHash *string `json:"diff_hash,omitempty"`
}

type Adapter struct {
	Name    string `json:"name"`
	Status  string `json:"status"`
	Version string `json:"version,omitempty"`
	Message string `json:"message,omitempty"`
}

type Summary struct {
	EvidenceCount int `json:"evidence_count"`
	FindingCount  int `json:"finding_count"`
}

type Finding map[string]any

type Verdict struct {
	Status  string `json:"status"`
	Gates   []Gate `json:"gates"`
	Message string `json:"message,omitempty"`
}

type Gate map[string]any

type gitSnapshot struct {
	Repository *Repository
	Branch     *string
	State      *GitState
}

func Main(args []string) int {
	target, err := parseRunArgs(args)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		fmt.Fprintln(os.Stderr, "usage: runwitness run -- <command> [args...]")
		return 2
	}

	verdict, err := Execute(target)
	if err != nil {
		fmt.Fprintf(os.Stderr, "runwitness: %v\n", err)
		return 2
	}

	switch verdict {
	case "pass", "warn":
		return 0
	case "fail":
		return 1
	default:
		return 2
	}
}

func parseRunArgs(args []string) ([]string, error) {
	if len(args) < 3 || args[0] != "run" {
		return nil, errors.New("expected run command")
	}

	separator := -1
	for i := 1; i < len(args); i++ {
		if args[i] == "--" {
			separator = i
			break
		}
	}

	if separator == -1 || separator == len(args)-1 {
		return nil, errors.New("target command is required after --")
	}

	return append([]string(nil), args[separator+1:]...), nil
}

func Execute(target []string) (string, error) {
	started := time.Now().UTC()
	runID, err := newUUIDv7(started)
	if err != nil {
		return "error", fmt.Errorf("create run id: %w", err)
	}

	workingDirectory, err := os.Getwd()
	if err != nil {
		return "error", fmt.Errorf("get working directory: %w", err)
	}
	workingDirectory, err = filepath.Abs(workingDirectory)
	if err != nil {
		return "error", fmt.Errorf("resolve working directory: %w", err)
	}

	before, err := inspectGit(workingDirectory)
	if err != nil {
		return "error", fmt.Errorf("inspect git state before execution: %w", err)
	}

	runDirectory := filepath.Join(workingDirectory, ".runwitness", "runs", runID)
	if err := os.MkdirAll(runDirectory, 0o755); err != nil {
		return "error", fmt.Errorf("create run directory: %w", err)
	}

	evidencePath := filepath.Join(runDirectory, "evidence.jsonl")
	evidenceFile, err := os.OpenFile(evidencePath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o644)
	if err != nil {
		return "error", fmt.Errorf("create evidence.jsonl: %w", err)
	}
	if err := evidenceFile.Close(); err != nil {
		return "error", fmt.Errorf("close evidence.jsonl: %w", err)
	}

	stdoutFile, err := os.Create(filepath.Join(runDirectory, "stdout.log"))
	if err != nil {
		return "error", fmt.Errorf("create stdout.log: %w", err)
	}

	stderrFile, err := os.Create(filepath.Join(runDirectory, "stderr.log"))
	if err != nil {
		_ = stdoutFile.Close()
		return "error", fmt.Errorf("create stderr.log: %w", err)
	}

	cmd := exec.Command(target[0], target[1:]...)
	cmd.Dir = workingDirectory
	cmd.Env = append(os.Environ(), "RUNWITNESS_RUN_ID="+runID)
	cmd.Stdout = io.MultiWriter(os.Stdout, stdoutFile)
	cmd.Stderr = io.MultiWriter(os.Stderr, stderrFile)

	runErr := cmd.Run()

	closeErr := errors.Join(stdoutFile.Close(), stderrFile.Close())
	if closeErr != nil {
		return "error", fmt.Errorf("close process logs: %w", closeErr)
	}

	var exitCode *int
	verdictStatus := "pass"
	verdictMessage := ""
	var internalRunError error

	if runErr == nil {
		code := 0
		exitCode = &code
	} else {
		var exitError *exec.ExitError
		if errors.As(runErr, &exitError) {
			code := exitError.ExitCode()
			exitCode = &code
			verdictStatus = "fail"
		} else {
			verdictStatus = "error"
			verdictMessage = runErr.Error()
			internalRunError = runErr
		}
	}

	after, err := inspectGit(workingDirectory)
	if err != nil {
		return "error", fmt.Errorf("inspect git state after execution: %w", err)
	}

	finished := time.Now().UTC()
	durationMS := finished.Sub(started).Milliseconds()
	if durationMS < 0 {
		durationMS = 0
	}

	document := Document{
		SchemaVersion: 1,
		Run: Run{
			RunID:            runID,
			Environment:      "local",
			StartedAt:        started.Format(time.RFC3339Nano),
			FinishedAt:       finished.Format(time.RFC3339Nano),
			DurationMS:       durationMS,
			RunnerVersion:    Version,
			WorkingDirectory: workingDirectory,
			Command: Command{
				Argv: append([]string(nil), target...),
			},
			Process: Process{ExitCode: exitCode},
		},
		Adapters: make([]Adapter, 0),
		Summary: Summary{
			EvidenceCount: 0,
			FindingCount:  0,
		},
		Findings: make([]Finding, 0),
		Verdict: Verdict{
			Status:  verdictStatus,
			Gates:   make([]Gate, 0),
			Message: verdictMessage,
		},
	}

	applyGitSnapshots(&document.Run, before, after)

	if err := writeRunDocument(runDirectory, document); err != nil {
		return "error", err
	}

	if internalRunError != nil {
		return "error", fmt.Errorf("start target command: %w", internalRunError)
	}

	return verdictStatus, nil
}

func applyGitSnapshots(run *Run, before, after *gitSnapshot) {
	if before == nil || before.Repository == nil || before.State == nil {
		return
	}
	if after == nil || after.Repository == nil || after.State == nil {
		return
	}
	if before.Repository.Root != after.Repository.Root {
		return
	}

	run.Repository = before.Repository
	run.Git = &Git{
		Branch: before.Branch,
		Before: *before.State,
		After:  *after.State,
	}
}

func writeRunDocument(runDirectory string, document Document) error {
	payload, err := json.MarshalIndent(document, "", "  ")
	if err != nil {
		return fmt.Errorf("encode run.json: %w", err)
	}
	payload = append(payload, '\n')

	temporaryPath := filepath.Join(runDirectory, "run.json.tmp")
	finalPath := filepath.Join(runDirectory, "run.json")

	if err := os.WriteFile(temporaryPath, payload, 0o644); err != nil {
		return fmt.Errorf("write run.json: %w", err)
	}
	if err := os.Rename(temporaryPath, finalPath); err != nil {
		return fmt.Errorf("publish run.json: %w", err)
	}
	return nil
}

func inspectGit(workingDirectory string) (*gitSnapshot, error) {
	rootOutput, err := gitOutput(workingDirectory, "rev-parse", "--show-toplevel")
	if err != nil {
		if isGitCommandFailure(err) {
			return nil, nil
		}
		return nil, err
	}

	root := strings.TrimSpace(rootOutput)
	if root == "" {
		return nil, nil
	}
	root, err = filepath.Abs(root)
	if err != nil {
		return nil, fmt.Errorf("resolve repository root: %w", err)
	}

	headOutput, err := gitOutput(root, "rev-parse", "HEAD")
	if err != nil {
		if isGitCommandFailure(err) {
			return nil, nil
		}
		return nil, err
	}
	head := strings.TrimSpace(headOutput)

	statusOutput, err := gitOutput(root, "status", "--porcelain=v1", "--untracked-files=normal")
	if err != nil {
		return nil, err
	}
	dirty := strings.TrimSpace(statusOutput) != ""

	var diffHash *string
	if dirty {
		diffOutput, err := gitOutput(root, "diff", "--binary", "--no-ext-diff", "HEAD", "--")
		if err != nil {
			return nil, err
		}
		sum := sha256.Sum256([]byte(diffOutput))
		value := "sha256:" + hex.EncodeToString(sum[:])
		diffHash = &value
	}

	var branch *string
	branchOutput, branchErr := gitOutput(root, "symbolic-ref", "--quiet", "--short", "HEAD")
	if branchErr == nil {
		value := strings.TrimSpace(branchOutput)
		if value != "" {
			branch = &value
		}
	} else if !isGitCommandFailure(branchErr) {
		return nil, branchErr
	}

	return &gitSnapshot{
		Repository: &Repository{Root: root},
		Branch:     branch,
		State: &GitState{
			HeadSHA:  head,
			Dirty:    dirty,
			DiffHash: diffHash,
		},
	}, nil
}

func gitOutput(directory string, args ...string) (string, error) {
	cmd := exec.Command("git", append([]string{"-C", directory}, args...)...)
	output, err := cmd.Output()
	if err != nil {
		return "", err
	}
	return string(output), nil
}

func isGitCommandFailure(err error) bool {
	var exitError *exec.ExitError
	return errors.As(err, &exitError)
}

func newUUIDv7(now time.Time) (string, error) {
	var raw [16]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return "", err
	}

	milliseconds := uint64(now.UnixMilli())
	raw[0] = byte(milliseconds >> 40)
	raw[1] = byte(milliseconds >> 32)
	raw[2] = byte(milliseconds >> 24)
	raw[3] = byte(milliseconds >> 16)
	raw[4] = byte(milliseconds >> 8)
	raw[5] = byte(milliseconds)

	raw[6] = (raw[6] & 0x0f) | 0x70
	raw[8] = (raw[8] & 0x3f) | 0x80

	return fmt.Sprintf(
		"%08x-%04x-%04x-%04x-%012x",
		binary.BigEndian.Uint32(raw[0:4]),
		binary.BigEndian.Uint16(raw[4:6]),
		binary.BigEndian.Uint16(raw[6:8]),
		binary.BigEndian.Uint16(raw[8:10]),
		raw[10:16],
	), nil
}
