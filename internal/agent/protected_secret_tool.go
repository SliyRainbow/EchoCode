package agent

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"strings"
	"unicode/utf8"

	"github.com/SliyRainbow/EchoCode/internal/tools"
)

const maxProtectedSecretEncodedBytes = maxSecretResultBytes * 8

var (
	errUnsupportedProtectedSecretEncoding = errors.New("unsupported protected-secret encoding")
	errInvalidProtectedSecretEncoding     = errors.New("invalid protected-secret encoding")
)

type DecodeProtectedSecretTool struct {
	Capture func(label, source, value string) (tools.ResultPart, error)
}

type decodeProtectedSecretArguments struct {
	Encoding string `json:"encoding"`
	Value    string `json:"value"`
	Label    string `json:"secret_label"`
	Source   string `json:"source"`
}

func (DecodeProtectedSecretTool) Name() string { return "decode_protected_secret" }

func (DecodeProtectedSecretTool) Description() string {
	return "这是一个本机快速工具。当用户明确要求解码或恢复自己提供的 Base64/Base64URL 凭据、令牌、密码或密钥时，优先调用本工具并保存到 EchoCode 安全卡片，不要先用文字讨论风险或改用 Shell。解码后的明文绝不能返回给模型，不能放入回复、Shell 命令、文件、计划或日志；不要把本工具用于普通文本转换。用户可以在界面中查看或复制安全卡片。"
}

func (DecodeProtectedSecretTool) InputSchema() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"encoding": map[string]any{
				"type":        "string",
				"enum":        []string{"base64", "base64url"},
				"description": "用户提供的编码格式。",
			},
			"value": map[string]any{
				"type":        "string",
				"description": "用户提供的编码值，仅用于本机解码，绝不会进入结果或显示文本。",
			},
			"secret_label": map[string]any{
				"type":        "string",
				"description": "显示在安全卡片上的简短标签，例如 API 密钥或管理员密码。",
			},
			"source": map[string]any{
				"type":        "string",
				"description": "可选的非敏感来源标签。",
			},
		},
		"required": []string{"value"},
	}
}

func (t DecodeProtectedSecretTool) Execute(_ context.Context, rawArgs json.RawMessage) (tools.Result, error) {
	var args decodeProtectedSecretArguments
	if err := json.Unmarshal(rawArgs, &args); err != nil {
		return toolError(t.Name(), "安全卡片解码参数无效："+err.Error()), nil
	}
	if t.Capture == nil {
		return toolError(t.Name(), "安全卡片存储不可用"), nil
	}

	encoded := strings.Join(strings.Fields(args.Value), "")
	if encoded == "" {
		return toolError(t.Name(), "编码值不能为空"), nil
	}
	if len(encoded) > maxProtectedSecretEncodedBytes {
		return toolError(t.Name(), "编码值过大，无法保存为安全文本结果"), nil
	}
	encoding := strings.ToLower(strings.TrimSpace(args.Encoding))
	if encoding == "" {
		encoding = "base64"
	}
	decoded, err := decodeProtectedSecretValue(encoded, encoding)
	if err != nil {
		return toolError(t.Name(), "编码值不是有效的 "+encoding+" 文本"), nil
	}
	if len(decoded) > maxSecretResultBytes {
		return toolError(t.Name(), "解码结果过大，无法保存为安全文本结果"), nil
	}
	if !utf8.Valid(decoded) {
		return toolError(t.Name(), "解码结果不是有效的 UTF-8 文本"), nil
	}

	label := strings.TrimSpace(args.Label)
	if label == "" {
		label = "已解码的敏感值"
	}
	label = truncateRunes(redactSensitiveText(label), 120)
	source := strings.TrimSpace(args.Source)
	if source == "" {
		source = "local://" + encoding
	}
	source = truncateRunes(redactSensitiveText(source), 240)
	part, err := t.Capture(label, source, string(decoded))
	if err != nil {
		return toolError(t.Name(), "保存安全卡片失败："+redactSensitiveText(err.Error())), nil
	}
	return tools.Result{
		Summary: "已将解码结果保存到安全卡片，明文不会返回给模型。",
		Parts:   []tools.ResultPart{part},
	}, nil
}

func decodeProtectedSecretValue(value, encoding string) ([]byte, error) {
	var decoders []*base64.Encoding
	switch encoding {
	case "base64":
		decoders = []*base64.Encoding{base64.StdEncoding, base64.RawStdEncoding}
	case "base64url", "base64-url", "base64_url":
		decoders = []*base64.Encoding{base64.URLEncoding, base64.RawURLEncoding}
	default:
		return nil, errUnsupportedProtectedSecretEncoding
	}
	for _, decoder := range decoders {
		decoded, err := decoder.DecodeString(value)
		if err == nil {
			return decoded, nil
		}
	}
	return nil, errInvalidProtectedSecretEncoding
}
