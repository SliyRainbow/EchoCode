package mcp

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"sort"
	"strings"
)

type ToolDescriptor struct {
	Name            string `json:"name"`
	InputSchemaHash string `json:"inputSchemaHash"`
	OutputPolicy    string `json:"outputPolicy"`
}

type ServerSnapshot struct {
	Server    string           `json:"server"`
	ToolsHash string           `json:"toolsHash"`
	Tools     []ToolDescriptor `json:"tools"`
}

func NewServerSnapshot(server string, tools []ToolDescriptor) ServerSnapshot {
	ordered := make([]ToolDescriptor, len(tools))
	copy(ordered, tools)
	sort.Slice(ordered, func(i, j int) bool {
		return ordered[i].Name < ordered[j].Name
	})
	return ServerSnapshot{
		Server:    server,
		ToolsHash: hashJSON(ordered),
		Tools:     ordered,
	}
}

func HashSchema(schema string) string {
	sum := sha256.Sum256([]byte(strings.TrimSpace(schema)))
	return "sha256:" + hex.EncodeToString(sum[:])
}

func FormatSnapshots(snapshots []ServerSnapshot) string {
	if len(snapshots) == 0 {
		return "[]"
	}
	encoded, err := json.Marshal(snapshots)
	if err != nil {
		return "[]"
	}
	return string(encoded)
}

func hashJSON(value any) string {
	encoded, err := json.Marshal(value)
	if err != nil {
		sum := sha256.Sum256([]byte("unhashable"))
		return "sha256:" + hex.EncodeToString(sum[:])
	}
	sum := sha256.Sum256(encoded)
	return "sha256:" + hex.EncodeToString(sum[:])
}
