package tools

import (
	"net/url"
	"regexp"
	"strings"
)

var transferURLPattern = regexp.MustCompile(`(?i)\b(?:https?|ssh|git)://[^\s"'<>]+`)

func redactTransferURLsForDisplay(value string) string {
	return transferURLPattern.ReplaceAllStringFunc(value, func(candidate string) string {
		parsed, err := url.Parse(candidate)
		if err != nil || parsed.Scheme == "" {
			return "[redacted URL]"
		}
		parsed.User = nil
		parsed.RawQuery = ""
		parsed.Fragment = ""
		return parsed.String()
	})
}

func redactKnownTransferURL(value, rawURL, safeURL string) string {
	if rawURL = strings.TrimSpace(rawURL); rawURL != "" {
		value = strings.ReplaceAll(value, rawURL, safeURL)
	}
	return redactTransferURLsForDisplay(value)
}
