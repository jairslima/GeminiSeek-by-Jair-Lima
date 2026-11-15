package bridge

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestParseModelAndAction(t *testing.T) {
	model, action := parseModelAndAction("/v1beta/models/deepseek-v4-pro:streamGenerateContent", "deepseek-flash")
	if model != "deepseek-v4-pro" || action != "streamGenerateContent" {
		t.Fatalf("unexpected model=%q action=%q", model, action)
	}

	// Internal Gemini utility model should map cleanly to deepseek-flash
	model2, action2 := parseModelAndAction("/v1beta/models/gemini-2.5-flash-lite:generateContent", "deepseek-flash")
	if model2 != "deepseek-flash" || action2 != "generateContent" {
		t.Fatalf("expected gemini-2.5-flash-lite to map to deepseek-flash, got model=%q action=%q", model2, action2)
	}
}

func TestNormalizeJSONSchemaForOpenAI(t *testing.T) {
	raw := map[string]any{
		"type": "OBJECT",
		"properties": map[string]any{
			"file_path": map[string]any{
				"type":        "STRING",
				"nullable":    true,
				"description": "Path to file",
			},
			"lines": map[string]any{
				"type": "ARRAY",
				"items": map[string]any{
					"type": "INTEGER",
				},
			},
		},
		"required": []string{"file_path"},
	}

	cleaned := normalizeJSONSchemaForOpenAI(raw)
	if cleaned["type"] != "object" {
		t.Fatalf("expected root type 'object', got %v", cleaned["type"])
	}
	props := cleaned["properties"].(map[string]any)
	fp := props["file_path"].(map[string]any)
	if fp["type"] != "string" {
		t.Fatalf("expected file_path type 'string', got %v", fp["type"])
	}
	if _, hasNullable := fp["nullable"]; hasNullable {
		t.Fatalf("expected nullable key to be stripped")
	}
}

func TestBridgeStreamAndToolCallingRoundTrip(t *testing.T) {
	// Mock upstream DeepSeek server
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/chat/completions" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)

		chunks := []string{
			`data: {"id":"chatcmpl-1","model":"deepseek-flash","choices":[{"index":0,"delta":{"reasoning_content":"Analisando pedido..."}}]}`,
			`data: {"id":"chatcmpl-1","model":"deepseek-flash","choices":[{"index":0,"delta":{"content":"Olá mundo!"}}]}`,
			`data: {"id":"chatcmpl-1","model":"deepseek-flash","choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"id":"call_abc123","type":"function","function":{"name":"read_file","arguments":"{\"path\":\"README.md\"}"}}]},"finish_reason":"tool_calls"}],"usage":{"prompt_tokens":15,"completion_tokens":20,"total_tokens":35}}`,
			`data: [DONE]`,
		}
		for _, c := range chunks {
			_, _ = w.Write([]byte(c + "\n\n"))
		}
	}))
	defer upstream.Close()

	srv, err := Start("test-key", upstream.URL, "deepseek-flash")
	if err != nil {
		t.Fatalf("failed to start bridge: %v", err)
	}
	defer srv.Close()

	reqPayload := map[string]any{
		"contents": []any{
			map[string]any{
				"role": "user",
				"parts": []any{
					map[string]any{"text": "Leia o README.md"},
				},
			},
		},
	}
	b, _ := json.Marshal(reqPayload)

	resp, err := http.Post(srv.BaseURL()+"/v1beta/models/deepseek-flash:streamGenerateContent?alt=sse", "application/json", bytes.NewReader(b))
	if err != nil {
		t.Fatalf("POST to bridge failed: %v", err)
	}
	defer resp.Body.Close()

	outBytes, _ := io.ReadAll(resp.Body)
	outStr := string(outBytes)

	if !strings.Contains(outStr, `"thought":true`) {
		t.Errorf("expected thought part in SSE output, got:\n%s", outStr)
	}
	if !strings.Contains(outStr, `"Olá mundo!"`) {
		t.Errorf("expected text part in SSE output, got:\n%s", outStr)
	}
	if !strings.Contains(outStr, `"functionCall":{"args":{"path":"README.md"},"id":"call_abc123","name":"read_file"}`) {
		t.Errorf("expected functionCall part in SSE output, got:\n%s", outStr)
	}
}
