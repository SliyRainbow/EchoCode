package agent

import (
	"errors"
	"fmt"
)

var (
	errEmptyModelResponse           = errors.New("模型没有返回可用正文")
	errProviderIgnoredDisabledTools = errors.New("上游模型在工具已禁用后仍请求调用工具")
)

type UnknownReasoningLevelError struct {
	Level ReasoningLevel
}

func (e *UnknownReasoningLevelError) Error() string {
	return fmt.Sprintf("unknown reasoning level %q", e.Level)
}
