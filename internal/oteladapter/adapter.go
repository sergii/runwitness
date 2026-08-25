package oteladapter

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

const defaultBinary = "otlp-mcp"

type Evidence struct {
	Kind       string
	ObservedAt time.Time
	Attributes map[string]any
	Payload    map[string]any
}

type Adapter struct {
	session         *mcp.ClientSession
	environmentVars map[string]string
}

type endpointOutput struct {
	Endpoint        string            `json:"endpoint"`
	Protocol        string            `json:"protocol"`
	EnvironmentVars map[string]string `json:"environment_vars"`
}

type snapshotOutput struct {
	Name string `json:"name"`
}

type snapshotDataOutput struct {
	Traces  []traceSummary  `json:"traces"`
	Logs    []logSummary    `json:"logs"`
	Metrics []metricSummary `json:"metrics"`
}

type traceSummary struct {
	TraceID      string         `json:"trace_id"`
	SpanID       string         `json:"span_id"`
	ParentSpanID string         `json:"parent_span_id,omitempty"`
	ServiceName  string         `json:"service_name"`
	SpanName     string         `json:"span_name"`
	StartTime    uint64         `json:"start_time_unix_nano"`
	EndTime      uint64         `json:"end_time_unix_nano"`
	Status       string         `json:"status,omitempty"`
	Attributes   map[string]any `json:"attributes,omitempty"`
}

type logSummary struct {
	TraceID     string         `json:"trace_id,omitempty"`
	SpanID      string         `json:"span_id,omitempty"`
	ServiceName string         `json:"service_name"`
	Severity    string         `json:"severity"`
	SeverityNum int32          `json:"severity_number"`
	Body        string         `json:"body"`
	Timestamp   uint64         `json:"timestamp_unix_nano"`
	Attributes  map[string]any `json:"attributes,omitempty"`
}

type metricSummary struct {
	MetricName  string   `json:"metric_name"`
	ServiceName string   `json:"service_name"`
	MetricType  string   `json:"metric_type"`
	Timestamp   uint64   `json:"timestamp_unix_nano"`
	Value       *float64 `json:"value,omitempty"`
	Count       *uint64  `json:"count,omitempty"`
	Sum         *float64 `json:"sum,omitempty"`
	DataPoints  int      `json:"data_point_count"`
}

func ResolveBinary() (string, error) {
	if configured := os.Getenv("RUNWITNESS_OTLP_MCP_BIN"); configured != "" {
		path, err := exec.LookPath(configured)
		if err != nil {
			return "", fmt.Errorf("resolve configured otlp-mcp executable %q: %w", configured, err)
		}
		return path, nil
	}

	path, err := exec.LookPath(defaultBinary)
	if err != nil {
		return "", fmt.Errorf("resolve %s on PATH: %w", defaultBinary, err)
	}
	return path, nil
}

func Start(ctx context.Context, binary string) (*Adapter, error) {
	command := exec.Command(binary, "serve", "--transport", "stdio", "--otlp-port", "0")
	client := mcp.NewClient(&mcp.Implementation{Name: "runwitness", Version: "v0.0.2"}, nil)
	session, err := client.Connect(ctx, &mcp.CommandTransport{Command: command}, nil)
	if err != nil {
		return nil, fmt.Errorf("connect to otlp-mcp: %w", err)
	}

	adapter := &Adapter{session: session}
	endpoint, err := callTool[endpointOutput](ctx, session, "get_otlp_endpoint", nil)
	if err != nil {
		_ = session.Close()
		return nil, fmt.Errorf("get OTLP endpoint: %w", err)
	}
	if endpoint.Endpoint == "" {
		_ = session.Close()
		return nil, fmt.Errorf("get OTLP endpoint: backend returned an empty endpoint")
	}

	adapter.environmentVars = cloneStringMap(endpoint.EnvironmentVars)
	if adapter.environmentVars == nil {
		adapter.environmentVars = make(map[string]string)
	}
	if _, ok := adapter.environmentVars["OTEL_EXPORTER_OTLP_ENDPOINT"]; !ok {
		adapter.environmentVars["OTEL_EXPORTER_OTLP_ENDPOINT"] = endpoint.Endpoint
	}
	if endpoint.Protocol != "" {
		if _, ok := adapter.environmentVars["OTEL_EXPORTER_OTLP_PROTOCOL"]; !ok {
			adapter.environmentVars["OTEL_EXPORTER_OTLP_PROTOCOL"] = endpoint.Protocol
		}
	}

	return adapter, nil
}

func (a *Adapter) EnvironmentVars() map[string]string {
	if a == nil {
		return nil
	}
	return cloneStringMap(a.environmentVars)
}

func (a *Adapter) CreateSnapshot(ctx context.Context, name string) error {
	if a == nil || a.session == nil {
		return fmt.Errorf("OTEL adapter is not connected")
	}
	if name == "" {
		return fmt.Errorf("snapshot name cannot be empty")
	}

	output, err := callTool[snapshotOutput](ctx, a.session, "create_snapshot", map[string]any{"name": name})
	if err != nil {
		return err
	}
	if output.Name == "" {
		return fmt.Errorf("backend returned an empty snapshot name")
	}
	return nil
}

func (a *Adapter) Collect(ctx context.Context, startSnapshot, endSnapshot string) ([]Evidence, error) {
	if a == nil || a.session == nil {
		return nil, fmt.Errorf("OTEL adapter is not connected")
	}

	data, err := callTool[snapshotDataOutput](ctx, a.session, "get_snapshot_data", map[string]any{
		"start_snapshot": startSnapshot,
		"end_snapshot":   endSnapshot,
	})
	if err != nil {
		return nil, err
	}

	evidence := make([]Evidence, 0, len(data.Traces)+len(data.Logs)+len(data.Metrics))
	for _, span := range data.Traces {
		payload, err := objectPayload(span)
		if err != nil {
			return nil, fmt.Errorf("encode span payload: %w", err)
		}
		attributes := cloneAnyMap(span.Attributes)
		attributes["service.name"] = span.ServiceName
		attributes["trace.id"] = span.TraceID
		attributes["span.id"] = span.SpanID
		attributes["span.name"] = span.SpanName
		if span.ParentSpanID != "" {
			attributes["span.parent_id"] = span.ParentSpanID
		}
		if span.Status != "" {
			attributes["span.status"] = span.Status
		}
		evidence = append(evidence, Evidence{
			Kind:       "otel.span",
			ObservedAt: unixNanoTime(span.StartTime),
			Attributes: attributes,
			Payload:    payload,
		})
	}

	for _, log := range data.Logs {
		payload, err := objectPayload(log)
		if err != nil {
			return nil, fmt.Errorf("encode log payload: %w", err)
		}
		attributes := cloneAnyMap(log.Attributes)
		attributes["service.name"] = log.ServiceName
		attributes["log.severity"] = log.Severity
		attributes["log.severity_number"] = log.SeverityNum
		if log.TraceID != "" {
			attributes["trace.id"] = log.TraceID
		}
		if log.SpanID != "" {
			attributes["span.id"] = log.SpanID
		}
		evidence = append(evidence, Evidence{
			Kind:       "otel.log",
			ObservedAt: unixNanoTime(log.Timestamp),
			Attributes: attributes,
			Payload:    payload,
		})
	}

	for _, metric := range data.Metrics {
		payload, err := objectPayload(metric)
		if err != nil {
			return nil, fmt.Errorf("encode metric payload: %w", err)
		}
		attributes := map[string]any{
			"service.name": metric.ServiceName,
			"metric.name":  metric.MetricName,
			"metric.type":  metric.MetricType,
		}
		evidence = append(evidence, Evidence{
			Kind:       "otel.metric",
			ObservedAt: unixNanoTime(metric.Timestamp),
			Attributes: attributes,
			Payload:    payload,
		})
	}

	return evidence, nil
}

func (a *Adapter) Close() error {
	if a == nil || a.session == nil {
		return nil
	}
	return a.session.Close()
}

func callTool[T any](ctx context.Context, session *mcp.ClientSession, name string, arguments map[string]any) (T, error) {
	var zero T
	result, err := session.CallTool(ctx, &mcp.CallToolParams{Name: name, Arguments: arguments})
	if err != nil {
		return zero, err
	}
	if result.IsError {
		return zero, fmt.Errorf("tool %s returned an error", name)
	}
	if result.StructuredContent == nil {
		return zero, fmt.Errorf("tool %s returned no structured content", name)
	}

	payload, err := json.Marshal(result.StructuredContent)
	if err != nil {
		return zero, fmt.Errorf("encode %s structured content: %w", name, err)
	}
	var output T
	if err := json.Unmarshal(payload, &output); err != nil {
		return zero, fmt.Errorf("decode %s structured content: %w", name, err)
	}
	return output, nil
}

func objectPayload(value any) (map[string]any, error) {
	payload, err := json.Marshal(value)
	if err != nil {
		return nil, err
	}
	result := make(map[string]any)
	if err := json.Unmarshal(payload, &result); err != nil {
		return nil, err
	}
	return result, nil
}

func cloneStringMap(source map[string]string) map[string]string {
	if source == nil {
		return nil
	}
	result := make(map[string]string, len(source))
	for key, value := range source {
		result[key] = value
	}
	return result
}

func cloneAnyMap(source map[string]any) map[string]any {
	result := make(map[string]any, len(source)+6)
	for key, value := range source {
		result[key] = value
	}
	return result
}

func unixNanoTime(value uint64) time.Time {
	seconds := int64(value / uint64(time.Second))
	nanoseconds := int64(value % uint64(time.Second))
	return time.Unix(seconds, nanoseconds).UTC()
}
