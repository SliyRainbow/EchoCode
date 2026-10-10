package main

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/http/httputil"
	"net/url"
	"strings"
	"testing"
)

// Optional loopback observer for synthetic acceptance traffic only. It reports
// field names/lengths, never headers, credentials, reasoning or image content.
func observeLiveWireShapes(t *testing.T, endpoint string) string {
	t.Helper()
	target, err := url.Parse(endpoint)
	if err != nil {
		t.Fatal("invalid scoped endpoint")
	}
	proxy := httputil.NewSingleHostReverseProxy(target)
	direct := proxy.Director
	proxy.Director = func(request *http.Request) { direct(request); request.Host = target.Host }
	proxy.FlushInterval = -1
	proxy.ModifyResponse = func(response *http.Response) error {
		if strings.Contains(response.Header.Get("Content-Type"), "event-stream") {
			response.Body = &shapeReader{ReadCloser: response.Body, t: t, fields: map[string]int{}}
		}
		return nil
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		data, err := io.ReadAll(r.Body)
		if err != nil {
			http.Error(w, "fixture body read failed", 500)
			return
		}
		_ = r.Body.Close()
		r.Body = io.NopCloser(bytes.NewReader(data))
		var body struct {
			Messages []map[string]json.RawMessage `json:"messages"`
		}
		if json.Unmarshal(data, &body) == nil {
			lengths := make([]int, 0)
			for _, message := range body.Messages {
				if string(message["role"]) != `"assistant"` {
					continue
				}
				var reasoning string
				if raw, exists := message["reasoning_content"]; exists {
					_ = json.Unmarshal(raw, &reasoning)
					lengths = append(lengths, len(reasoning))
				} else {
					lengths = append(lengths, -1)
				}
			}
			t.Logf("wire request assistant reasoning lengths (-1=absent): %v", lengths)
		}
		proxy.ServeHTTP(w, r)
	}))
	t.Cleanup(server.Close)
	return server.URL
}

type shapeReader struct {
	io.ReadCloser
	t       *testing.T
	pending string
	fields  map[string]int
}

func (s *shapeReader) Read(p []byte) (int, error) {
	n, err := s.ReadCloser.Read(p)
	s.pending += string(p[:n])
	for {
		index := strings.IndexByte(s.pending, '\n')
		if index < 0 {
			break
		}
		line := strings.TrimSpace(s.pending[:index])
		s.pending = s.pending[index+1:]
		if !strings.HasPrefix(line, "data:") {
			continue
		}
		var chunk struct {
			Choices []struct {
				Delta map[string]json.RawMessage `json:"delta"`
			} `json:"choices"`
		}
		if json.Unmarshal([]byte(strings.TrimSpace(strings.TrimPrefix(line, "data:"))), &chunk) != nil {
			continue
		}
		for _, choice := range chunk.Choices {
			for key := range choice.Delta {
				s.fields[key]++
			}
		}
	}
	return n, err
}

func (s *shapeReader) Close() error {
	s.t.Logf("wire response delta field counts: %v", s.fields)
	return s.ReadCloser.Close()
}
