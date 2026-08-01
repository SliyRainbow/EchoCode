package protocol

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
)

func applyExtraHeaders(req *http.Request, raw string) error {
	for _, line := range strings.Split(raw, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		key, value, ok := strings.Cut(line, ":")
		if !ok {
			key, value, ok = strings.Cut(line, "=")
		}
		key = strings.TrimSpace(key)
		value = strings.TrimSpace(value)
		if !ok || key == "" {
			return fmt.Errorf("invalid extra header line %q, expected Header: value", line)
		}
		if strings.ContainsAny(key, "\r\n") || strings.ContainsAny(value, "\r\n") {
			return fmt.Errorf("invalid extra header %q", key)
		}
		req.Header.Set(key, value)
	}
	return nil
}

func mergeExtraJSONBody(encoded []byte, raw string, protected map[string]bool) ([]byte, error) {
	if strings.TrimSpace(raw) == "" {
		return encoded, nil
	}
	var base map[string]any
	if err := json.Unmarshal(encoded, &base); err != nil {
		return nil, err
	}
	var extra map[string]any
	if err := json.Unmarshal([]byte(raw), &extra); err != nil {
		return nil, fmt.Errorf("invalid extra request body JSON: %w", err)
	}
	if extra == nil {
		return nil, fmt.Errorf("extra request body must be a JSON object")
	}
	for key, value := range extra {
		cleanKey := strings.TrimSpace(key)
		if cleanKey == "" || protected[strings.ToLower(cleanKey)] {
			continue
		}
		base[cleanKey] = value
	}
	return json.Marshal(base)
}

func protectedBodyKeys(keys ...string) map[string]bool {
	protected := make(map[string]bool, len(keys))
	for _, key := range keys {
		protected[strings.ToLower(key)] = true
	}
	return protected
}
