package runner

import (
	"fmt"
	"strings"

	"github.com/sergii/runwitness/internal/oteladapter"
)

const (
	otelSpanErrorRuleID   = "otel.span.error"
	runtimeNoErrorsRuleID = "runtime.no_errors"
)

func deriveRuntimeFindings(runID string, evidence []oteladapter.Evidence) ([]Finding, []Gate) {
	findings := make([]Finding, 0)
	findingIDs := make([]string, 0)

	for index, item := range evidence {
		if item.Kind != "otel.span" {
			continue
		}

		status, ok := stringAttribute(item.Attributes, "span.status")
		if !ok || !strings.EqualFold(status, "ERROR") {
			continue
		}

		evidenceID := evidenceRecordID(runID, index)
		findingID := "rwf_otel_span_error_" + evidenceID
		summary := "OpenTelemetry span reported ERROR"
		if spanName, ok := stringAttribute(item.Attributes, "span.name"); ok && spanName != "" {
			summary += ": " + spanName
		}

		findings = append(findings, Finding{
			"finding_id":    findingID,
			"kind":          "runtime.error",
			"severity":      "error",
			"rule_id":       otelSpanErrorRuleID,
			"sources":       []string{"otel"},
			"summary":       summary,
			"evidence_refs": []string{evidenceID},
		})
		findingIDs = append(findingIDs, findingID)
	}

	if len(findingIDs) == 0 {
		return findings, make([]Gate, 0)
	}

	gates := []Gate{{
		"rule_id":     runtimeNoErrorsRuleID,
		"action":      "fail",
		"outcome":     "triggered",
		"finding_ids": findingIDs,
		"message":     fmt.Sprintf("%d runtime error finding(s) observed", len(findingIDs)),
	}}
	return findings, gates
}

func evidenceRecordID(runID string, index int) string {
	return fmt.Sprintf("ev_%s_%06d", strings.ReplaceAll(runID, "-", ""), index+1)
}

func stringAttribute(attributes map[string]any, key string) (string, bool) {
	if attributes == nil {
		return "", false
	}
	value, ok := attributes[key]
	if !ok {
		return "", false
	}
	text, ok := value.(string)
	return text, ok
}
