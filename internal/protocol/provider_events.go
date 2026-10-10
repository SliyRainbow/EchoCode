package protocol

import (
	"errors"
	"fmt"
	"strings"
)

const (
	ProviderNoticeModelReroute      = "model_reroute"
	ProviderNoticeSafetyBuffering   = "safety_buffering"
	ProviderNoticeModelVerification = "model_verification"
	ProviderNoticeModeration        = "moderation"
	ProviderNoticePolicyError       = "policy_error"
	ProviderNoticeProviderError     = "provider_error"
)

// ProviderNotice is provider runtime information that must survive streaming,
// session switches, and app restarts without relying on localized status text.
type ProviderNotice struct {
	Kind           string   `json:"kind"`
	Severity       string   `json:"severity,omitempty"`
	Message        string   `json:"message,omitempty"`
	RequestedModel string   `json:"requestedModel,omitempty"`
	EffectiveModel string   `json:"effectiveModel,omitempty"`
	RetryModel     string   `json:"retryModel,omitempty"`
	UseCases       []string `json:"useCases,omitempty"`
	Reasons        []string `json:"reasons,omitempty"`
	Verifications  []string `json:"verifications,omitempty"`
	MetadataKeys   []string `json:"metadataKeys,omitempty"`
	RequestID      string   `json:"requestId,omitempty"`
}

// ProviderErrorInfo preserves machine-readable upstream failures. In
// particular, cyber_policy must remain a non-retryable policy error instead of
// being flattened into an ordinary string.
type ProviderErrorInfo struct {
	Provider   string `json:"provider,omitempty"`
	HTTPStatus int    `json:"httpStatus,omitempty"`
	Type       string `json:"type,omitempty"`
	Code       string `json:"code,omitempty"`
	Message    string `json:"message"`
	RequestID  string `json:"requestId,omitempty"`
	Retryable  bool   `json:"retryable"`
}

type ProviderError struct {
	Info ProviderErrorInfo
}

type upstreamError struct {
	Message string `json:"message"`
	Type    string `json:"type,omitempty"`
	Code    any    `json:"code,omitempty"`
}

func providerErrorFromUpstream(provider string, payload *upstreamError, status int, requestID string) ProviderErrorInfo {
	info := ProviderErrorInfo{Provider: provider, HTTPStatus: status, RequestID: requestID, Message: "模型服务返回错误"}
	if payload != nil {
		info.Type = strings.TrimSpace(payload.Type)
		if payload.Code != nil {
			info.Code = strings.TrimSpace(fmt.Sprint(payload.Code))
		}
		if strings.TrimSpace(payload.Message) != "" {
			info.Message = strings.TrimSpace(payload.Message)
		}
	}
	info.Retryable = shouldRetryOpenAICompatibleStatus(status) || status == 529
	if status < 400 {
		for _, value := range []string{info.Code, info.Type} {
			switch value {
			case "rate_limit_error", "rate_limit_exceeded", "overloaded_error", "overloaded", "api_error", "server_error", "timeout", "request_timeout":
				info.Retryable = true
			}
		}
	}
	if isNonRetryableProviderErrorCode(info.Code) {
		info.Retryable = false
	}
	return info
}

func (e *ProviderError) Error() string {
	if e == nil {
		return "provider request failed"
	}
	message := strings.TrimSpace(e.Info.Message)
	if message == "" {
		message = "provider request failed"
	}
	if e.Info.HTTPStatus > 0 {
		return fmt.Sprintf("%s (HTTP %d)", message, e.Info.HTTPStatus)
	}
	return message
}

func NewProviderError(info ProviderErrorInfo) error {
	return &ProviderError{Info: info}
}

func ProviderErrorDetails(err error) (ProviderErrorInfo, bool) {
	var providerErr *ProviderError
	if !errors.As(err, &providerErr) || providerErr == nil {
		return ProviderErrorInfo{}, false
	}
	return providerErr.Info, true
}

func IsProviderErrorCode(err error, code string) bool {
	info, ok := ProviderErrorDetails(err)
	return ok && strings.EqualFold(strings.TrimSpace(info.Code), strings.TrimSpace(code))
}
