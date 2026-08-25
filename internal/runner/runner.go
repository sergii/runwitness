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
	"sort"
	"strings"
	"time"
)

const Version = "0.0.1"

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
	Label            string      `json:"label,omitempty"`
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

type RunOptions struct {
	Label  string
	Target []string
}

type gitSnapshot struct {
	Repository *Repository
	Branch     *string
	State      *GitState
}

func Main(args []string) int {
	if len(args) == 1 && args[0] == "--version" {
		fmt.Printf("RunWitness v%s\n", Version)
		return 0
	}

	options, err := parseRunArgs(args)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		fmt.Fprintln(os.Stderr, "usage: runwitness run [--label <name>] -- <command> [args...]")
		return 2
	}

	verdict, err := ExecuteWithOptions(options)
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

func parseRunArgs(args []string) (RunOptions, error) {
	if len(args) == 0 || args[0] != "run" {
		return RunOptions{}, errors.New("expected run command")
	}

	options := RunOptions{}
	separator := -1

	for i := 1; i < len(args); i++ {
		switch args[i] {
		case "--":
			separator = i
			i = len(args)
		case "--label":
			if i+1 >= len(args) || args[i+1] == "--" {
				return RunOptions{}, errors.New("--label requires a non-empty value")
			}
			if args[i+1] == "" {
				return RunOptions{}, errors.New("--label requires a non-empty value")
			}
			if options.Label != "" {
				return RunOptions{}, errors.New("--label may be specified only once")
			}
			options.Label = args[i+1]
			i++
		default:
			return RunOptions{}, fmt.Errorf("unknown option %q", args[i])
		}
	}

	if separator == -1 || separator == len(args)-1 {
		return RunOptions{}, errors.New("target command is required after --")
	}

	options.Target = append([]string(nil), args[separator+1:]...)
	return options, nil
}

func Execute(target []string) (string, error) {
	return ExecuteWithOptions(RunOptions{Target: target})
}

func ExecuteWithOptions(options RunOptions) (string, error) {
	target := options.Target
	if len(target) == 0 {
		return "error", errors.New("target command is required")
	}

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
			Label:            options.Label,
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

	pathspec := observedGitPathspec()

	statusArgs := append(
		[]string{"status", "--porcelain=v1", "--untracked-files=normal", "--"},
		pathspec...,
	)
	statusOutput, err := gitOutput(root, statusArgs...)
	if err != nil {
		return nil, err
	}
	dirty := strings.TrimSpace(statusOutput) != ""

	var diffHash *string
	if dirty {
		value, err := gitStateFingerprint(root, pathspec)
		if err != nil {
			return nil, err
		}
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

func observedGitPathspec() []string {
	return []string{
		".",
		":(glob,exclude).runwitness",
		":(glob,exclude).runwitness/**",
		":(glob,exclude)**/.runwitness",
		":(glob,exclude)**/.runwitness/**",
	}
}

func gitStateFingerprint(root string, pathspec []string) (string, error) {
	diffArgs := append(
		[]string{"diff", "--binary", "--no-ext-diff", "HEAD", "--"},
		pathspec...,
	)
	trackedDiff, err := gitOutput(root, diffArgs...)
	if err != nil {
		return "", err
	}

	untrackedArgs := append(
		[]string{"ls-files", "--others", "--exclude-standard", "-z", "--"},
		pathspec...,
	)
	untrackedOutput, err := gitOutput(root, untrackedArgs...)
	if err != nil {
		return "", err
	}

	untrackedPaths := strings.Split(untrackedOutput, "\x00")
	sort.Strings(untrackedPaths)

	hash := sha256.New()
	if err := writeFingerprintPart(hash, []byte("tracked-diff")); err != nil {
		return "", err
	}
	if err := writeFingerprintPart(hash, []byte(trackedDiff)); err != nil {
		return "", err
	}

	for _, relativePath := range untrackedPaths {
		if relativePath == "" {
			continue
		}
		if err := hashUntrackedPath(hash, root, relativePath); err != nil {
			return "", err
		}
	}

	return "sha256:" + hex.EncodeToString(hash.Sum(nil)), nil
}

func hashUntrackedPath(writer io.Writer, root, relativePath string) error {
	fullPath := filepath.Join(root, filepath.FromSlash(relativePath))
	info, err := os.Lstat(fullPath)
	if err != nil {
		return fmt.Errorf("inspect untracked path %q: %w", relativePath, err)
	}

	if err := writeFingerprintPart(writer, []byte("untracked")); err != nil {
		return err
	}
	if err := writeFingerprintPart(writer, []byte(relativePath)); err != nil {
		return err
	}

	switch {
	case info.Mode()&os.ModeSymlink != 0:
		if err := writeFingerprintPart(writer, []byte("symlink")); err != nil {
			return err
		}
		target, err := os.Readlink(fullPath)
		if err != nil {
			return fmt.Errorf("read untracked symlink %q: %w", relativePath, err)
		}
		return writeFingerprintPart(writer, []byte(target))
	case info.Mode().IsRegular():
		if err := writeFingerprintPart(writer, []byte("regular")); err != nil {
			return err
		}
		if err := binary.Write(writer, binary.BigEndian, uint64(info.Size())); err != nil {
			return fmt.Errorf("write untracked file size: %w", err)
		}
		file, err := os.Open(fullPath)
		if err != nil {
			return fmt.Errorf("open untracked file %q: %w", relativePath, err)
		}
		_, copyErr := io.Copy(writer, file)
		closeErr := file.Close()
		if copyErr != nil {
			return fmt.Errorf("hash untracked file %q: %w", relativePath, copyErr)
		}
		if closeErr != nil {
			return fmt.Errorf("close untracked file %q: %w", relativePath, closeErr)
		}
		return nil
	default:
		if err := writeFingerprintPart(writer, []byte("special")); err != nil {
			return err
		}
		return writeFingerprintPart(writer, []byte(info.Mode().String()))
	}
}

func writeFingerprintPart(writer io.Writer, value []byte) error {
	if err := binary.Write(writer, binary.BigEndian, uint64(len(value))); err != nil {
		return fmt.Errorf("write fingerprint length: %w", err)
	}
	if _, err := writer.Write(value); err != nil {
		return fmt.Errorf("write fingerprint value: %w", err)
	}
	return nil
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
