package runner

import "encoding/json"

func (document Document) MarshalJSON() ([]byte, error) {
	type documentAlias Document
	normalized := documentAlias(document)
	if normalized.Adapters == nil {
		normalized.Adapters = make([]Adapter, 0)
	}
	if normalized.Findings == nil {
		normalized.Findings = make([]Finding, 0)
	}
	if normalized.Verdict.Gates == nil {
		normalized.Verdict.Gates = make([]Gate, 0)
	}
	return json.Marshal(normalized)
}
