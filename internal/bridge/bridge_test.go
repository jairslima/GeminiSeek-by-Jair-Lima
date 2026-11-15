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

func TestBridgeRetryAndEditorialMode(t *testing.T) {
	calls := 0
	var capturedSystem string
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		raw, _ := io.ReadAll(r.Body)
		var req map[string]any
		_ = json.Unmarshal(raw, &req)
		if msgs, ok := req["messages"].([]any); ok && len(msgs) > 0 {
			if m0, ok := msgs[0].(map[string]any); ok && m0["role"] == "system" {
				capturedSystem, _ = m0["content"].(string)
			}
		}
		if calls == 1 {
			w.WriteHeader(http.StatusServiceUnavailable)
			_, _ = w.Write([]byte(`{"error":"temporary overload"}`))
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"id":"chatcmpl-2","model":"deepseek-v4-pro","choices":[{"index":0,"message":{"role":"assistant","content":"Capítulo revisado."},"finish_reason":"stop"}],"usage":{"prompt_tokens":100,"completion_tokens":50,"total_tokens":150,"prompt_cache_hit_tokens":80,"prompt_cache_miss_tokens":20}}`))
	}))
	defer upstream.Close()

	srv, err := Start("test-key", upstream.URL, "deepseek-v4-pro")
	if err != nil {
		t.Fatalf("failed to start bridge: %v", err)
	}
	srv.SetEditorialMode(true)
	defer srv.Close()

	reqPayload := map[string]any{
		"contents": []any{
			map[string]any{
				"role":  "user",
				"parts": []any{map[string]any{"text": "Escreva um parágrafo."}},
			},
		},
	}
	b, _ := json.Marshal(reqPayload)
	resp, err := http.Post(srv.BaseURL()+"/v1beta/models/deepseek-v4-pro:generateContent", "application/json", bytes.NewReader(b))
	if err != nil {
		t.Fatalf("POST failed: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected HTTP 200 after retry, got %d", resp.StatusCode)
	}
	if !strings.Contains(capturedSystem, "CONTRATO EDITORIAL OBRIGATÓRIO") {
		t.Fatalf("expected editorial mode contract in system message, got: %s", capturedSystem)
	}
	st := srv.Stats()
	if st.Retries != 1 || st.CacheHitTokens != 80 || st.CacheMissTokens != 20 {
		t.Fatalf("unexpected bridge stats: %+v", st)
	}
}
