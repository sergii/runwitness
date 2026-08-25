package railsadapter

import (
	"bufio"
	_ "embed"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

//go:embed bootstrap.rb
var bootstrapSource []byte

type Evidence struct {
	Kind       string
	ObservedAt time.Time
	Attributes map[string]any
	Payload    map[string]any
}

type Adapter struct {
	eventsPath    string
	bootstrapPath string
}

type event struct {
	Type         string         `json:"type"`
	ObservedAt   string         `json:"observed_at"`
	ErrorClass   string         `json:"error_class"`
	ErrorMessage string         `json:"error_message"`
	Handled      bool           `json:"handled"`
	Severity     string         `json:"severity"`
	Source       string         `json:"source"`
	Location     map[string]any `json:"location"`
	Backtrace    []string       `json:"backtrace"`
	Context      map[string]any `json:"context"`
	SQLStatement string         `json:"sql_statement"`
	SQLName      string         `json:"sql_name"`
	SQLCached    bool           `json:"sql_cached"`
	DurationMS   float64        `json:"duration_ms"`
}

func Prepare(runDirectory, workingDirectory, currentRubyOpt, currentRubyLib string) (*Adapter, map[string]string, error) {
	bootstrapPath := filepath.Join(runDirectory, "runwitness_rails_bootstrap.rb")
	eventsPath := filepath.Join(runDirectory, "rails-events.jsonl")

	if err := os.WriteFile(bootstrapPath, bootstrapSource, 0o644); err != nil {
		return nil, nil, fmt.Errorf("write Rails bootstrap: %w", err)
	}
	if err := os.WriteFile(eventsPath, nil, 0o644); err != nil {
		_ = os.Remove(bootstrapPath)
		return nil, nil, fmt.Errorf("create Rails event stream: %w", err)
	}

	overrides := map[string]string{
		"RUNWITNESS_RAILS_EVENTS_PATH": eventsPath,
		"RUNWITNESS_WORKING_DIRECTORY": workingDirectory,
		"RUBYOPT":                      appendRubyOpt(currentRubyOpt, "-rrunwitness_rails_bootstrap"),
		"RUBYLIB":                      prependRubyLib(currentRubyLib, runDirectory),
	}

	return &Adapter{
		eventsPath:    eventsPath,
		bootstrapPath: bootstrapPath,
	}, overrides, nil
}

func (a *Adapter) Collect() ([]Evidence, bool, error) {
	if a == nil || a.eventsPath == "" {
		return nil, false, fmt.Errorf("Rails adapter is not prepared")
	}

	file, err := os.Open(a.eventsPath)
	if err != nil {
		return nil, false, fmt.Errorf("open Rails event stream: %w", err)
	}
	defer file.Close()

	evidence := make([]Evidence, 0)
	subscribed := false
	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, 64*1024), 1024*1024)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}

		var item event
		if err := json.Unmarshal([]byte(line), &item); err != nil {
			return nil, subscribed, fmt.Errorf("decode Rails event: %w", err)
		}

		switch item.Type {
		case "subscribed":
			subscribed = true
		case "error":
			observedAt, err := time.Parse(time.RFC3339Nano, item.ObservedAt)
			if err != nil {
				return nil, subscribed, fmt.Errorf("decode Rails error timestamp: %w", err)
			}

			attributes := map[string]any{
				"error.class":    item.ErrorClass,
				"error.message":  item.ErrorMessage,
				"error.handled":  item.Handled,
				"error.severity": item.Severity,
				"error.source":   item.Source,
			}
			if path, ok := item.Location["path"].(string); ok && path != "" {
				attributes["error.location.path"] = path
			}
			if label, ok := item.Location["label"].(string); ok && label != "" {
				attributes["error.location.label"] = label
			}
			if line, ok := item.Location["line"]; ok {
				attributes["error.location.line"] = line
			}

			payload := map[string]any{
				"backtrace": item.Backtrace,
				"context":   item.Context,
				"location":  item.Location,
			}
			evidence = append(evidence, Evidence{
				Kind:       "rails.error",
				ObservedAt: observedAt.UTC(),
				Attributes: attributes,
				Payload:    payload,
			})
		case "sql":
			observedAt, err := time.Parse(time.RFC3339Nano, item.ObservedAt)
			if err != nil {
				return nil, subscribed, fmt.Errorf("decode Rails SQL timestamp: %w", err)
			}
			statement := normalizeSQL(item.SQLStatement)
			name := strings.TrimSpace(item.SQLName)
			if item.SQLCached || statement == "" || isIgnoredSQLName(name) {
				continue
			}
			durationMS := item.DurationMS
			if durationMS < 0 {
				durationMS = 0
			}
			evidence = append(evidence, Evidence{
				Kind:       "rails.sql",
				ObservedAt: observedAt.UTC(),
				Attributes: map[string]any{
					"sql.statement":   statement,
					"sql.name":        name,
					"sql.cached":      false,
					"sql.duration_ms": durationMS,
				},
				Payload: map[string]any{},
			})
		default:
			return nil, subscribed, fmt.Errorf("unsupported Rails event type %q", item.Type)
		}
	}
	if err := scanner.Err(); err != nil {
		return nil, subscribed, fmt.Errorf("read Rails event stream: %w", err)
	}

	return evidence, subscribed, nil
}

func (a *Adapter) Close() error {
	if a == nil {
		return nil
	}
	var firstErr error
	for _, path := range []string{a.bootstrapPath, a.eventsPath} {
		if path == "" {
			continue
		}
		if err := os.Remove(path); err != nil && !os.IsNotExist(err) && firstErr == nil {
			firstErr = err
		}
	}
	if firstErr != nil {
		return fmt.Errorf("clean Rails adapter files: %w", firstErr)
	}
	return nil
}

func normalizeSQL(statement string) string {
	return strings.Join(strings.Fields(statement), " ")
}

func isIgnoredSQLName(name string) bool {
	switch strings.ToUpper(strings.TrimSpace(name)) {
	case "SCHEMA", "TRANSACTION":
		return true
	default:
		return false
	}
}

func appendRubyOpt(current, option string) string {
	current = strings.TrimSpace(current)
	if current == "" {
		return option
	}
	return current + " " + option
}

func prependRubyLib(current, directory string) string {
	if current == "" {
		return directory
	}
	return directory + string(os.PathListSeparator) + current
}
