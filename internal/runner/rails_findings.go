package runner

import (
	"fmt"

	"github.com/sergii/runwitness/internal/railsadapter"
)

const railsHandledErrorRuleID = "rails.error.handled"

func deriveRailsFindings(runID string, evidence []railsadapter.Evidence, evidenceOffset int) ([]Finding, []Gate) {
	findings := make([]Finding, 0)
	findingIDs := make([]string, 0)

	for index, item := range evidence {
		if item.Kind != "rails.error" {
			continue
		}

		handled, ok := boolAttribute(item.Attributes, "error.handled")
		if !ok || !handled {
			continue
		}

		errorClass, _ := stringAttribute(item.Attributes, "error.class")
		errorMessage, _ := stringAttribute(item.Attributes, "error.message")
		source, _ := stringAttribute(item.Attributes, "error.source")
		path, _ := stringAttribute(item.Attributes, "error.location.path")
		label, _ := stringAttribute(item.Attributes, "error.location.label")

		findingID := stableFindingID(
			"finding.v1",
			"runtime.handled_error",
			railsHandledErrorRuleID,
			errorClass,
			source,
			path,
			label,
		)
		evidenceID := evidenceRecordID(runID, evidenceOffset+index)
		summary := "Rails.error reported a handled error"
		if errorClass != "" {
			summary += ": " + errorClass
		}
		if errorMessage != "" {
			summary += ": " + errorMessage
		}

		findings = append(findings, Finding{
			"finding_id":    findingID,
			"kind":          "runtime.handled_error",
			"severity":      "warning",
			"rule_id":       railsHandledErrorRuleID,
			"sources":       []string{"rails"},
			"summary":       summary,
			"evidence_refs": []string{evidenceID},
		})
		findingIDs = append(findingIDs, findingID)
	}

	if len(findingIDs) == 0 {
		return findings, make([]Gate, 0)
	}

	return findings, []Gate{{
		"rule_id":     runtimeNoErrorsRuleID,
		"action":      "fail",
		"outcome":     "triggered",
		"finding_ids": findingIDs,
		"message":     fmt.Sprintf("%d Rails handled error finding(s) observed", len(findingIDs)),
	}}
}

func boolAttribute(attributes map[string]any, key string) (bool, bool) {
	if attributes == nil {
		return false, false
	}
	value, ok := attributes[key]
	if !ok {
		return false, false
	}
	boolean, ok := value.(bool)
	return boolean, ok
}

func mergeGates(existing, additions []Gate) []Gate {
	result := append([]Gate(nil), existing...)
	for _, addition := range additions {
		ruleID, _ := addition["rule_id"].(string)
		merged := false
		for _, current := range result {
			currentRuleID, _ := current["rule_id"].(string)
			if currentRuleID != ruleID {
				continue
			}

			currentIDs, _ := current["finding_ids"].([]string)
			additionIDs, _ := addition["finding_ids"].([]string)
			current["finding_ids"] = appendUniqueStrings(currentIDs, additionIDs...)
			if ruleID == runtimeNoErrorsRuleID {
				current["message"] = fmt.Sprintf("%d runtime error finding(s) observed", len(current["finding_ids"].([]string)))
			}
			merged = true
			break
		}
		if !merged {
			result = append(result, addition)
		}
	}
	return result
}

func appendUniqueStrings(values []string, additions ...string) []string {
	seen := make(map[string]struct{}, len(values)+len(additions))
	result := make([]string, 0, len(values)+len(additions))
	for _, value := range values {
		if _, ok := seen[value]; ok {
			continue
		}
		seen[value] = struct{}{}
		result = append(result, value)
	}
	for _, value := range additions {
		if _, ok := seen[value]; ok {
			continue
		}
		seen[value] = struct{}{}
		result = append(result, value)
	}
	return result
}
