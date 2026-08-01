package mcp

import "encoding/json"

type ResultRef struct {
	Kind string `json:"kind"`
	Path string `json:"path,omitempty"`
	Line int    `json:"line,omitempty"`
	ID   string `json:"id,omitempty"`
}

type ToolResultSummary struct {
	Tool        string      `json:"tool"`
	Status      string      `json:"status"`
	Summary     string      `json:"summary"`
	Refs        []ResultRef `json:"refs"`
	RawResultID string      `json:"rawResultId"`
}

func FormatToolResults(results []ToolResultSummary) string {
	if len(results) == 0 {
		return "[]"
	}
	encoded, err := json.Marshal(results)
	if err != nil {
		return "[]"
	}
	return string(encoded)
}
