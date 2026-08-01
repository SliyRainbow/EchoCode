package tools

import (
	"context"
	"encoding/json"
	"testing"
)

func TestUpdatePlanToolReturnsStructuredProgress(t *testing.T) {
	result, err := (UpdatePlanTool{}).Execute(context.Background(), json.RawMessage(`{
		"steps":[
			{"title":"Inspect","status":"completed"},
			{"title":"Implement","status":"in_progress"},
			{"title":"Verify","status":"pending"}
		]
	}`))
	if err != nil || result.IsError || len(result.Parts) != 1 {
		t.Fatalf("result=%+v err=%v", result, err)
	}
	part := result.Parts[0]
	if part.Kind != PartProgress || part.TaskStatus != "running" || len(part.Steps) != 3 {
		t.Fatalf("progress part=%+v", part)
	}
}

func TestUpdatePlanToolRejectsMultipleActiveSteps(t *testing.T) {
	result, err := (UpdatePlanTool{}).Execute(context.Background(), json.RawMessage(`{
		"steps":[
			{"title":"One","status":"in_progress"},
			{"title":"Two","status":"in_progress"}
		]
	}`))
	if err != nil {
		t.Fatal(err)
	}
	if !result.IsError {
		t.Fatalf("expected validation error, got %+v", result)
	}
}

func TestSummarizeFileChangesUsesFinalStatePerPath(t *testing.T) {
	files, adds, dels := SummarizeFileChanges([]FileChange{
		{Path: "a.txt", Before: "one\n", After: "one\ntwo\n"},
		{Path: "a.txt", Before: "one\ntwo\n", After: "one\nthree\n"},
		{Path: "unchanged.txt", Before: "same\n", After: "same\n"},
	})
	if files != 1 || adds != 1 || dels != 0 {
		t.Fatalf("stats = files:%d +%d -%d", files, adds, dels)
	}
}
