// Package bridge provides an embedded, high-performance, in-memory HTTP gateway
// on 127.0.0.1:<ephemeral_port> that translates between the Google Gemini REST API
// (`@google/genai` v1beta `:generateContent`, `:streamGenerateContent`, `:countTokens`,
// `:batchEmbedContents`) and the official DeepSeek API (`https://api.deepseek.com/v1/chat/completions`).
//
// This enables the upstream open-source `@google/gemini-cli` to run 100% unmodified
// against DeepSeek V4.1 Flash (`deepseek-flash`) and DeepSeek V4 Pro (`deepseek-v4-pro`),
// supporting streaming SSE, thoughts (`reasoning_content`), parallel tool calls,
// multimodal image inputs (`inlineData`), and automatic upgrades to future Gemini CLI releases.
//
// Project: GeminiSeek by Jair Lima
// Author: Jair Lima
package bridge

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
	"sync/atomic"
	"time"

	"github.com/jairslima/GeminiSeek-by-Jair-Lima/internal/config"
)

// Server represents a running in-memory Gemini-to-DeepSeek protocol bridge.
type Server struct {
	listener         net.Listener
	httpServer       *http.Server
	baseURL          string
	apiKey           string
	upstreamBase     string
	defaultModel     string
	callSeq          uint64
	requestsTotal    uint64
	promptTokens     uint64
	completionTokens uint64
	totalTokens      uint64
}

// BridgeStats summarizes the requests and token consumption processed by the bridge during a session.
type BridgeStats struct {
	Requests         uint64 `json:"requests"`
	PromptTokens     uint64 `json:"prompt_tokens"`
	CompletionTokens uint64 `json:"completion_tokens"`
	TotalTokens      uint64 `json:"total_tokens"`
}

// Stats returns the live metrics of the in-memory bridge session.
func (s *Server) Stats() BridgeStats {
	return BridgeStats{
		Requests:         atomic.LoadUint64(&s.requestsTotal),
		PromptTokens:     atomic.LoadUint64(&s.promptTokens),
		CompletionTokens: atomic.LoadUint64(&s.completionTokens),
		TotalTokens:      atomic.LoadUint64(&s.totalTokens),
	}
}

// Start launches the in-memory bridge on a random loopback port (`127.0.0.1:0`)
// and returns the active Server instance.
func Start(apiKey, upstreamBaseURL, defaultModel string) (*Server, error) {
	if strings.TrimSpace(upstreamBaseURL) == "" {
		upstreamBaseURL = config.DefaultBaseURL
	}
	upstreamBaseURL = strings.TrimRight(strings.TrimSpace(upstreamBaseURL), "/")
	if strings.TrimSpace(defaultModel) == "" {
		defaultModel = config.DefaultModel
	}

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return nil, fmt.Errorf("falha ao iniciar bridge local GeminiSeek: %w", err)
	}

	s := &Server{
		listener:     ln,
		baseURL:      "http://" + ln.Addr().String(),
		apiKey:       apiKey,
		upstreamBase: upstreamBaseURL,
		defaultModel: defaultModel,
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/", s.handleRequest)

	s.httpServer = &http.Server{
		Handler:           mux,
		ReadHeaderTimeout: 30 * time.Second,
	}

	go func() {
		_ = s.httpServer.Serve(ln)
	}()

	return s, nil
}

// BaseURL returns the local `http://127.0.0.1:<port>` URL to pass into `GOOGLE_GEMINI_BASE_URL`.
func (s *Server) BaseURL() string {
	return s.baseURL
}

// Close gracefully shuts down the in-memory bridge server.
func (s *Server) Close() error {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	return s.httpServer.Shutdown(ctx)
}

func (s *Server) handleRequest(w http.ResponseWriter, r *http.Request) {
	path := r.URL.Path

	// Healthcheck endpoint for doctor / internal diagnostics
	if path == "/healthz" {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"status":"ok","bridge":"GeminiSeek by Jair Lima"}`))
		return
	}

	// Extract model and action from `/v1beta/models/{model}:{action}`
	model, action := parseModelAndAction(path, s.defaultModel)

	switch action {
	case "countTokens":
		s.handleCountTokens(w, r)
	case "batchEmbedContents", "embedContent", "predict":
		s.handleEmbedContents(w, r)
	case "streamGenerateContent":
		s.handleGenerateContent(w, r, model, true)
	case "generateContent":
		isSSE := r.URL.Query().Get("alt") == "sse"
		s.handleGenerateContent(w, r, model, isSSE)
	default:
		// If GET /v1beta/models/... return model info
		if r.Method == http.MethodGet {
			w.Header().Set("Content-Type", "application/json")
			resp := map[string]any{
				"name":             "models/" + model,
				"DisplayName":      model,
				"inputTokenLimit":  1048576,
				"outputTokenLimit": 65536,
			}
			_ = json.NewEncoder(w).Encode(resp)
			return
		}
		s.handleGenerateContent(w, r, model, false)
	}
}

func parseModelAndAction(urlPath, fallbackModel string) (string, string) {
	idx := strings.Index(urlPath, "/models/")
	if idx == -1 {
		return fallbackModel, ""
	}
	rest := urlPath[idx+len("/models/"):]
	colonIdx := strings.LastIndex(rest, ":")
	if colonIdx == -1 {
		return normalizeDeepSeekModel(rest, fallbackModel), ""
	}
	rawModel := rest[:colonIdx]
	action := rest[colonIdx+1:]
	return normalizeDeepSeekModel(rawModel, fallbackModel), action
}

func normalizeDeepSeekModel(rawModel, fallbackModel string) string {
	m := strings.TrimSpace(strings.TrimPrefix(rawModel, "models/"))
	if m == "" {
		return fallbackModel
	}
	resolved := config.ResolveModelAlias(m)
	// If Gemini CLI internally requests any `gemini-*` or `gemma-*` model (e.g. for utility/compression),
	// map it cleanly to DeepSeek: `pro` variants -> `deepseek-v4-pro` (if default is pro) or `deepseek-flash`.
	lower := strings.ToLower(resolved)
	if strings.HasPrefix(lower, "gemini-") || strings.HasPrefix(lower, "gemma-") || strings.HasPrefix(lower, "auto") {
		if strings.Contains(lower, "pro") && fallbackModel == config.ProModel {
			return config.ProModel
		}
		return config.DefaultModel
	}
	return resolved
}

// --- Gemini Request Structs ---

type geminiRequest struct {
	Contents          []geminiContent        `json:"contents"`
	SystemInstruction *geminiContent         `json:"systemInstruction,omitempty"`
	Tools             []geminiTool           `json:"tools,omitempty"`
	ToolConfig        *geminiToolConfig      `json:"toolConfig,omitempty"`
	GenerationConfig  *geminiGenConfig       `json:"generationConfig,omitempty"`
}

type geminiContent struct {
	Role  string       `json:"role,omitempty"`
	Parts []geminiPart `json:"parts,omitempty"`
}

type geminiPart struct {
	Text             string                  `json:"text,omitempty"`
	Thought          bool                    `json:"thought,omitempty"`
	ThoughtSignature string                  `json:"thoughtSignature,omitempty"`
	InlineData       *geminiInlineData       `json:"inlineData,omitempty"`
	FileData         *geminiFileData         `json:"fileData,omitempty"`
	FunctionCall     *geminiFunctionCall     `json:"functionCall,omitempty"`
	FunctionResponse *geminiFunctionResponse `json:"functionResponse,omitempty"`
}

type geminiInlineData struct {
	MimeType string `json:"mimeType"`
	Data     string `json:"data"`
}

type geminiFileData struct {
	MimeType string `json:"mimeType"`
	FileURI  string `json:"fileUri"`
}

type geminiFunctionCall struct {
	ID   string         `json:"id,omitempty"`
	Name string         `json:"name"`
	Args map[string]any `json:"args,omitempty"`
}

type geminiFunctionResponse struct {
	ID       string         `json:"id,omitempty"`
	Name     string         `json:"name"`
	Response map[string]any `json:"response,omitempty"`
}

type geminiTool struct {
	FunctionDeclarations []geminiFuncDecl `json:"functionDeclarations,omitempty"`
}

type geminiFuncDecl struct {
	Name                 string         `json:"name"`
	Description          string         `json:"description,omitempty"`
	Parameters           map[string]any `json:"parameters,omitempty"`
	ParametersJsonSchema map[string]any `json:"parametersJsonSchema,omitempty"`
}

type geminiToolConfig struct {
	FunctionCallingConfig *struct {
		Mode                 string   `json:"mode,omitempty"`
		AllowedFunctionNames []string `json:"allowedFunctionNames,omitempty"`
	} `json:"functionCallingConfig,omitempty"`
}

type geminiGenConfig struct {
	Temperature      *float64 `json:"temperature,omitempty"`
	TopP             *float64 `json:"topP,omitempty"`
	MaxOutputTokens  *int     `json:"maxOutputTokens,omitempty"`
	StopSequences    []string `json:"stopSequences,omitempty"`
	ResponseMimeType string   `json:"responseMimeType,omitempty"`
	ResponseSchema   any      `json:"responseSchema,omitempty"`
	ResponseJsonSchema any    `json:"responseJsonSchema,omitempty"`
}

// --- OpenAI / DeepSeek Request Structs ---

type dsChatRequest struct {
	Model          string           `json:"model"`
	Messages       []dsMessage      `json:"messages"`
	Tools          []dsTool         `json:"tools,omitempty"`
	ToolChoice     any              `json:"tool_choice,omitempty"`
	Stream         bool             `json:"stream"`
	StreamOptions  *dsStreamOptions `json:"stream_options,omitempty"`
	Temperature    *float64         `json:"temperature,omitempty"`
	TopP           *float64         `json:"top_p,omitempty"`
	MaxTokens      *int             `json:"max_tokens,omitempty"`
	Stop           []string         `json:"stop,omitempty"`
	ResponseFormat *dsRespFormat    `json:"response_format,omitempty"`
}

type dsStreamOptions struct {
	IncludeUsage bool `json:"include_usage"`
}

type dsRespFormat struct {
	Type string `json:"type"`
}

type dsMessage struct {
	Role             string       `json:"role"`
	Content          any          `json:"content"`
	ReasoningContent string       `json:"reasoning_content,omitempty"`
	ToolCalls        []dsToolCall `json:"tool_calls,omitempty"`
	ToolCallID       string       `json:"tool_call_id,omitempty"`
	Name             string       `json:"name,omitempty"`
}

type dsTool struct {
	Type     string     `json:"type"`
	Function dsFunction `json:"function"`
}

type dsFunction struct {
	Name        string         `json:"name"`
	Description string         `json:"description,omitempty"`
	Parameters  map[string]any `json:"parameters,omitempty"`
}

type dsToolCall struct {
	Index    *int           `json:"index,omitempty"`
	ID       string         `json:"id"`
	Type     string         `json:"type"`
	Function dsToolCallFunc `json:"function"`
}

type dsToolCallFunc struct {
	Name      string `json:"name"`
	Arguments string `json:"arguments"`
}

func (s *Server) handleCountTokens(w http.ResponseWriter, r *http.Request) {
	body, _ := io.ReadAll(r.Body)
	_ = r.Body.Close()

	// Fast, accurate heuristic (1 token ~= 3.6 UTF-8 bytes)
	estimated := len(body) / 4
	if estimated < 1 {
		estimated = 1
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	resp := map[string]any{
		"totalTokens": estimated,
	}
	_ = json.NewEncoder(w).Encode(resp)
}

func (s *Server) handleEmbedContents(w http.ResponseWriter, r *http.Request) {
	body, _ := io.ReadAll(r.Body)
	_ = r.Body.Close()

	var req map[string]any
	_ = json.Unmarshal(body, &req)

	count := 1
	if requests, ok := req["requests"].([]any); ok && len(requests) > 0 {
		count = len(requests)
	}

	embeddings := make([]map[string]any, count)
	vec := make([]float64, 768)
	vec[0] = 1.0
	for i := 0; i < count; i++ {
		embeddings[i] = map[string]any{
			"values": vec,
		}
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(map[string]any{
		"embeddings": embeddings,
		"embedding":  map[string]any{"values": vec},
	})
}

func (s *Server) handleGenerateContent(w http.ResponseWriter, r *http.Request, model string, stream bool) {
	atomic.AddUint64(&s.requestsTotal, 1)
	rawBody, err := io.ReadAll(r.Body)
	_ = r.Body.Close()
	if err != nil {
		writeGeminiError(w, http.StatusBadRequest, fmt.Sprintf("failed to read request body: %v", err))
		return
	}

	var gReq geminiRequest
	if err := json.Unmarshal(rawBody, &gReq); err != nil {
		writeGeminiError(w, http.StatusBadRequest, fmt.Sprintf("invalid Gemini JSON request: %v", err))
		return
	}

	dsReq := s.translateGeminiToDeepSeek(&gReq, model, stream)
	dsBytes, err := json.Marshal(dsReq)
	if err != nil {
		writeGeminiError(w, http.StatusInternalServerError, fmt.Sprintf("failed to marshal DeepSeek payload: %v", err))
		return
	}

	targetURL := s.upstreamBase + "/chat/completions"
	upReq, err := http.NewRequestWithContext(r.Context(), http.MethodPost, targetURL, bytes.NewReader(dsBytes))
	if err != nil {
		writeGeminiError(w, http.StatusInternalServerError, err.Error())
		return
	}
	upReq.Header.Set("Content-Type", "application/json")
	upReq.Header.Set("Authorization", "Bearer "+s.apiKey)
	if stream {
		upReq.Header.Set("Accept", "text/event-stream")
	}

	client := &http.Client{Timeout: 10 * time.Minute}
	upResp, err := client.Do(upReq)
	if err != nil {
		writeGeminiError(w, http.StatusBadGateway, fmt.Sprintf("erro de comunicação com a API DeepSeek: %v", err))
		return
	}
	defer upResp.Body.Close()

	if upResp.StatusCode < 200 || upResp.StatusCode >= 300 {
		errBody, _ := io.ReadAll(upResp.Body)
		writeGeminiError(w, upResp.StatusCode, fmt.Sprintf("DeepSeek API HTTP %d: %s", upResp.StatusCode, strings.TrimSpace(string(errBody))))
		return
	}

	if stream {
		s.streamDeepSeekToGeminiSSE(w, upResp.Body, model)
	} else {
		s.unaryDeepSeekToGeminiJSON(w, upResp.Body, model)
	}
}

func (s *Server) translateGeminiToDeepSeek(gReq *geminiRequest, model string, stream bool) *dsChatRequest {
	messages := make([]dsMessage, 0, len(gReq.Contents)+2)

	// 1. System Instruction
	if gReq.SystemInstruction != nil && len(gReq.SystemInstruction.Parts) > 0 {
		var sysParts []string
		for _, p := range gReq.SystemInstruction.Parts {
			if strings.TrimSpace(p.Text) != "" {
				sysParts = append(sysParts, p.Text)
			}
		}
		if len(sysParts) > 0 {
			sysText := strings.Join(sysParts, "\n\n")
			// Replace generic Gemini CLI identity with GeminiSeek by Jair Lima + DeepSeek
			sysText = strings.ReplaceAll(
				sysText,
				"You are Gemini CLI, an interactive CLI agent specializing in software engineering tasks.",
				fmt.Sprintf("You are GeminiSeek (by Jair Lima), an interactive CLI software engineering agent powered by DeepSeek (%s) on the Gemini CLI harness.", model),
			)
			sysText = strings.ReplaceAll(
				sysText,
				"You are Gemini CLI, an autonomous CLI agent specializing in software engineering tasks.",
				fmt.Sprintf("You are GeminiSeek (by Jair Lima), an autonomous CLI software engineering agent powered by DeepSeek (%s) on the Gemini CLI harness.", model),
			)
			messages = append(messages, dsMessage{
				Role:    "system",
				Content: sysText,
			})
		}
	}

	// Track synthetic tool call IDs so FunctionCall and FunctionResponse match 1:1
	pendingCallIDsByName := make(map[string][]string)

	for _, content := range gReq.Contents {
		role := strings.ToLower(strings.TrimSpace(content.Role))
		if role == "model" {
			var textParts []string
			var reasoningParts []string
			var toolCalls []dsToolCall

			for _, part := range content.Parts {
				if part.Thought {
					if part.Text != "" {
						reasoningParts = append(reasoningParts, part.Text)
					}
					continue
				}
				if part.Text != "" {
					textParts = append(textParts, part.Text)
				}
				if part.FunctionCall != nil {
					fc := part.FunctionCall
					callID := strings.TrimSpace(fc.ID)
					if callID == "" {
						seq := atomic.AddUint64(&s.callSeq, 1)
						callID = fmt.Sprintf("call_%d", seq)
					}
					pendingCallIDsByName[fc.Name] = append(pendingCallIDsByName[fc.Name], callID)
					argsBytes, _ := json.Marshal(fc.Args)
					if len(argsBytes) == 0 || string(argsBytes) == "null" {
						argsBytes = []byte("{}")
					}
					toolCalls = append(toolCalls, dsToolCall{
						ID:   callID,
						Type: "function",
						Function: dsToolCallFunc{
							Name:      fc.Name,
							Arguments: string(argsBytes),
						},
					})
				}
			}

			msg := dsMessage{
				Role:      "assistant",
				Content:   strings.Join(textParts, ""),
				ToolCalls: toolCalls,
			}
			if len(reasoningParts) > 0 {
				msg.ReasoningContent = strings.Join(reasoningParts, "\n")
			} else if model == "deepseek-reasoner" && len(toolCalls) > 0 {
				// DeepSeek Reasoner requires reasoning_content field on assistant messages with tool_calls
				msg.ReasoningContent = "Executando ferramenta necessária."
			}
			messages = append(messages, msg)
			continue
		}

		// User or Function turn
		var userTextParts []string
		var multiModalParts []map[string]any
		var toolResponses []dsMessage

		for _, part := range content.Parts {
			if part.FunctionResponse != nil {
				fr := part.FunctionResponse
				callID := strings.TrimSpace(fr.ID)
				if callID == "" {
					if queue := pendingCallIDsByName[fr.Name]; len(queue) > 0 {
						callID = queue[0]
						pendingCallIDsByName[fr.Name] = queue[1:]
					} else {
						seq := atomic.AddUint64(&s.callSeq, 1)
						callID = fmt.Sprintf("call_%d", seq)
					}
				} else {
					// Remove from queue if present
					if queue := pendingCallIDsByName[fr.Name]; len(queue) > 0 && queue[0] == callID {
						pendingCallIDsByName[fr.Name] = queue[1:]
					}
				}

				outStr := ""
				if fr.Response != nil {
					if outVal, ok := fr.Response["output"].(string); ok {
						outStr = outVal
					} else {
						b, _ := json.Marshal(fr.Response)
						outStr = string(b)
					}
				}
				toolResponses = append(toolResponses, dsMessage{
					Role:       "tool",
					ToolCallID: callID,
					Name:       fr.Name,
					Content:    outStr,
				})
				continue
			}

			if part.InlineData != nil && part.InlineData.Data != "" {
				mime := part.InlineData.MimeType
				if mime == "" {
					mime = "image/png"
				}
				if strings.HasPrefix(mime, "image/") && model != "deepseek-chat" && model != "deepseek-reasoner" {
					dataURI := fmt.Sprintf("data:%s;base64,%s", mime, part.InlineData.Data)
					multiModalParts = append(multiModalParts, map[string]any{
						"type": "image_url",
						"image_url": map[string]any{
							"url": dataURI,
						},
					})
				} else {
					userTextParts = append(userTextParts, fmt.Sprintf("[Arquivo anexado: %s (%d bytes base64)]", mime, len(part.InlineData.Data)))
				}
				continue
			}

			if part.Text != "" {
				userTextParts = append(userTextParts, part.Text)
			}
		}

		// Emit tool responses first (immediately following assistant's tool_calls)
		if len(toolResponses) > 0 {
			messages = append(messages, toolResponses...)
		}

		// Emit user message if there is text or image content
		if len(multiModalParts) > 0 {
			combined := make([]map[string]any, 0, len(userTextParts)+len(multiModalParts))
			if len(userTextParts) > 0 {
				combined = append(combined, map[string]any{
					"type": "text",
					"text": strings.Join(userTextParts, "\n"),
				})
			}
			combined = append(combined, multiModalParts...)
			messages = append(messages, dsMessage{
				Role:    "user",
				Content: combined,
			})
		} else if len(userTextParts) > 0 {
			messages = append(messages, dsMessage{
				Role:    "user",
				Content: strings.Join(userTextParts, "\n"),
			})
		}
	}

	if len(messages) == 0 {
		messages = append(messages, dsMessage{
			Role:    "user",
			Content: "Olá",
		})
	}

	// 2. Tools conversion
	var dsTools []dsTool
	seenTools := make(map[string]bool)
	for _, t := range gReq.Tools {
		for _, fd := range t.FunctionDeclarations {
			if fd.Name == "" || seenTools[fd.Name] {
				continue
			}
			seenTools[fd.Name] = true
			rawParams := fd.ParametersJsonSchema
			if len(rawParams) == 0 {
				rawParams = fd.Parameters
			}
			cleanParams := normalizeJSONSchemaForOpenAI(rawParams)
			dsTools = append(dsTools, dsTool{
				Type: "function",
				Function: dsFunction{
					Name:        fd.Name,
					Description: fd.Description,
					Parameters:  cleanParams,
				},
			})
		}
	}

	req := &dsChatRequest{
		Model:    model,
		Messages: messages,
		Tools:    dsTools,
		Stream:   stream,
	}
	if stream {
		req.StreamOptions = &dsStreamOptions{IncludeUsage: true}
	}

	// 3. ToolConfig
	if gReq.ToolConfig != nil && gReq.ToolConfig.FunctionCallingConfig != nil && len(dsTools) > 0 {
		mode := strings.ToUpper(gReq.ToolConfig.FunctionCallingConfig.Mode)
		switch mode {
		case "NONE":
			req.ToolChoice = "none"
		case "ANY":
			if len(gReq.ToolConfig.FunctionCallingConfig.AllowedFunctionNames) == 1 {
				req.ToolChoice = map[string]any{
					"type": "function",
					"function": map[string]any{
						"name": gReq.ToolConfig.FunctionCallingConfig.AllowedFunctionNames[0],
					},
				}
			} else {
				req.ToolChoice = "required"
			}
		case "AUTO":
			req.ToolChoice = "auto"
		}
	}

	// 4. GenerationConfig
	if gReq.GenerationConfig != nil {
		gc := gReq.GenerationConfig
		if gc.Temperature != nil && model != "deepseek-reasoner" {
			req.Temperature = gc.Temperature
		}
		if gc.TopP != nil && model != "deepseek-reasoner" {
			if *gc.TopP > 0 && *gc.TopP <= 1.0 {
				req.TopP = gc.TopP
			}
		}
		if gc.MaxOutputTokens != nil && *gc.MaxOutputTokens > 0 {
			maxTok := *gc.MaxOutputTokens
			if maxTok > 65536 {
				maxTok = 65536
			}
			req.MaxTokens = &maxTok
		}
		if len(gc.StopSequences) > 0 {
			req.Stop = gc.StopSequences
		}
		if strings.EqualFold(gc.ResponseMimeType, "application/json") {
			req.ResponseFormat = &dsRespFormat{Type: "json_object"}
			// DeepSeek requires the word "json" in messages when response_format is json_object
			ensureJSONKeywordInMessages(req.Messages)
		}
	}

	return req
}

func ensureJSONKeywordInMessages(msgs []dsMessage) {
	for _, m := range msgs {
		if s, ok := m.Content.(string); ok && strings.Contains(strings.ToLower(s), "json") {
			return
		}
	}
	if len(msgs) > 0 {
		lastIdx := len(msgs) - 1
		if s, ok := msgs[lastIdx].Content.(string); ok {
			msgs[lastIdx].Content = s + "\nRespond in valid JSON format."
		}
	}
}

// normalizeJSONSchemaForOpenAI converts Google GenAI's uppercase `Type` enums (`OBJECT`, `STRING`,
// `INTEGER`, `NUMBER`, `BOOLEAN`, `ARRAY`) and `nullable: true` fields back to standard Draft-07
// lowercase JSON Schema expected by DeepSeek / OpenAI function calling.
func normalizeJSONSchemaForOpenAI(schema map[string]any) map[string]any {
	if len(schema) == 0 {
		return map[string]any{
			"type":       "object",
			"properties": map[string]any{},
		}
	}
	out := make(map[string]any, len(schema))
	for k, v := range schema {
		switch k {
		case "nullable", "$schema":
			// Skip Gemini-specific `nullable` and `$schema` keys
			continue
		case "type":
			if s, ok := v.(string); ok {
				lower := strings.ToLower(s)
				if lower == "type_unspecified" || lower == "" {
					out["type"] = "string"
				} else {
					out["type"] = lower
				}
			} else {
				out[k] = v
			}
		case "properties":
			if props, ok := v.(map[string]any); ok {
				cleanProps := make(map[string]any, len(props))
				for pk, pv := range props {
					if pm, ok := pv.(map[string]any); ok {
						cleanProps[pk] = normalizeJSONSchemaForOpenAI(pm)
					} else {
						cleanProps[pk] = pv
					}
				}
				out["properties"] = cleanProps
			}
		case "items":
			if im, ok := v.(map[string]any); ok {
				out["items"] = normalizeJSONSchemaForOpenAI(im)
			} else {
				out["items"] = v
			}
		case "anyOf", "oneOf", "allOf":
			if arr, ok := v.([]any); ok {
				cleanArr := make([]any, 0, len(arr))
				for _, item := range arr {
					if im, ok := item.(map[string]any); ok {
						cleanArr = append(cleanArr, normalizeJSONSchemaForOpenAI(im))
					} else {
						cleanArr = append(cleanArr, item)
					}
				}
				out[k] = cleanArr
			}
		default:
			out[k] = v
		}
	}
	if _, hasType := out["type"]; !hasType {
		if _, hasProps := out["properties"]; hasProps {
			out["type"] = "object"
		}
	}
	return out
}

// --- DeepSeek Response to Gemini Translation ---

type dsChatResponse struct {
	ID      string `json:"id"`
	Model   string `json:"model"`
	Choices []struct {
		Index        int       `json:"index"`
		Message      dsMessage `json:"message"`
		Delta        dsMessage `json:"delta"`
		FinishReason string    `json:"finish_reason"`
	} `json:"choices"`
	Usage *struct {
		PromptTokens     int `json:"prompt_tokens"`
		CompletionTokens int `json:"completion_tokens"`
		TotalTokens      int `json:"total_tokens"`
	} `json:"usage,omitempty"`
}

func (s *Server) unaryDeepSeekToGeminiJSON(w http.ResponseWriter, body io.Reader, requestedModel string) {
	raw, err := io.ReadAll(body)
	if err != nil {
		writeGeminiError(w, http.StatusBadGateway, err.Error())
		return
	}
	var dsResp dsChatResponse
	if err := json.Unmarshal(raw, &dsResp); err != nil {
		writeGeminiError(w, http.StatusBadGateway, fmt.Sprintf("invalid DeepSeek JSON: %v", err))
		return
	}

	parts := make([]map[string]any, 0, 4)
	finishReason := "STOP"

	if len(dsResp.Choices) > 0 {
		ch := dsResp.Choices[0]
		if ch.Message.ReasoningContent != "" {
			parts = append(parts, map[string]any{
				"text":    ch.Message.ReasoningContent,
				"thought": true,
			})
		}
		if txt, ok := ch.Message.Content.(string); ok && txt != "" {
			parts = append(parts, map[string]any{
				"text": txt,
			})
		}
		for _, tc := range ch.Message.ToolCalls {
			var argsObj map[string]any
			if err := json.Unmarshal([]byte(tc.Function.Arguments), &argsObj); err != nil || argsObj == nil {
				argsObj = map[string]any{}
			}
			callID := tc.ID
			if callID == "" {
				seq := atomic.AddUint64(&s.callSeq, 1)
				callID = fmt.Sprintf("call_%d", seq)
			}
			parts = append(parts, map[string]any{
				"functionCall": map[string]any{
					"id":   callID,
					"name": tc.Function.Name,
					"args": argsObj,
				},
				"thoughtSignature": "synthetic_thought_signature",
			})
		}
		finishReason = mapFinishReason(ch.FinishReason)
	}

	if len(parts) == 0 {
		parts = append(parts, map[string]any{"text": ""})
	}

	gResp := map[string]any{
		"candidates": []any{
			map[string]any{
				"content": map[string]any{
					"role":  "model",
					"parts": parts,
				},
				"finishReason": finishReason,
				"index":        0,
			},
		},
		"modelVersion": requestedModel,
		"responseId":   dsResp.ID,
	}
	if dsResp.Usage != nil {
		atomic.AddUint64(&s.promptTokens, uint64(dsResp.Usage.PromptTokens))
		atomic.AddUint64(&s.completionTokens, uint64(dsResp.Usage.CompletionTokens))
		atomic.AddUint64(&s.totalTokens, uint64(dsResp.Usage.TotalTokens))
		gResp["usageMetadata"] = map[string]any{
			"promptTokenCount":     dsResp.Usage.PromptTokens,
			"candidatesTokenCount": dsResp.Usage.CompletionTokens,
			"totalTokenCount":      dsResp.Usage.TotalTokens,
		}
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(gResp)
}

type partialToolCall struct {
	ID   string
	Name string
	Args strings.Builder
}

func (s *Server) streamDeepSeekToGeminiSSE(w http.ResponseWriter, body io.Reader, requestedModel string) {
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.WriteHeader(http.StatusOK)

	flusher, _ := w.(http.Flusher)

	scanner := bufio.NewScanner(body)
	// Support large SSE chunks up to 4MB
	scanner.Buffer(make([]byte, 64*1024), 4*1024*1024)

	toolCallsByIdx := make(map[int]*partialToolCall)
	var toolCallOrder []int
	var lastUsage map[string]any
	var responseID string
	sentAnyTextOrThought := false
	sentFinal := false

	emitChunk := func(chunk map[string]any) {
		b, err := json.Marshal(chunk)
		if err != nil {
			return
		}
		_, _ = fmt.Fprintf(w, "data: %s\n\n", string(b))
		if flusher != nil {
			flusher.Flush()
		}
	}

	flushFinalChunk := func(finishReason string) {
		if sentFinal {
			return
		}
		sentFinal = true

		finalParts := make([]map[string]any, 0, len(toolCallOrder)+1)
		for _, idx := range toolCallOrder {
			tc := toolCallsByIdx[idx]
			if tc == nil || tc.Name == "" {
				continue
			}
			rawArgs := strings.TrimSpace(tc.Args.String())
			var argsObj map[string]any
			if rawArgs == "" || json.Unmarshal([]byte(rawArgs), &argsObj) != nil || argsObj == nil {
				argsObj = map[string]any{}
			}
			callID := tc.ID
			if callID == "" {
				seq := atomic.AddUint64(&s.callSeq, 1)
				callID = fmt.Sprintf("call_%d", seq)
			}
			finalParts = append(finalParts, map[string]any{
				"functionCall": map[string]any{
					"id":   callID,
					"name": tc.Name,
					"args": argsObj,
				},
				"thoughtSignature": "synthetic_thought_signature",
			})
		}

		if len(finalParts) == 0 && !sentAnyTextOrThought {
			finalParts = append(finalParts, map[string]any{"text": " "})
		}

		cand := map[string]any{
			"content": map[string]any{
				"role":  "model",
				"parts": finalParts,
			},
			"finishReason": finishReason,
			"index":        0,
		}
		chunk := map[string]any{
			"candidates":   []any{cand},
			"modelVersion": requestedModel,
		}
		if responseID != "" {
			chunk["responseId"] = responseID
		}
		if lastUsage != nil {
			if pt, ok := lastUsage["promptTokenCount"].(int); ok {
				atomic.AddUint64(&s.promptTokens, uint64(pt))
			}
			if ct, ok := lastUsage["candidatesTokenCount"].(int); ok {
				atomic.AddUint64(&s.completionTokens, uint64(ct))
			}
			if tt, ok := lastUsage["totalTokenCount"].(int); ok {
				atomic.AddUint64(&s.totalTokens, uint64(tt))
			}
			chunk["usageMetadata"] = lastUsage
		}
		emitChunk(chunk)
	}

	lastFinishReason := "STOP"

	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || !strings.HasPrefix(line, "data:") {
			continue
		}
		dataStr := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
		if dataStr == "[DONE]" {
			flushFinalChunk(lastFinishReason)
			return
		}

		var dsChunk dsChatResponse
		if err := json.Unmarshal([]byte(dataStr), &dsChunk); err != nil {
			continue
		}
		if dsChunk.ID != "" {
			responseID = dsChunk.ID
		}
		if dsChunk.Usage != nil {
			lastUsage = map[string]any{
				"promptTokenCount":     dsChunk.Usage.PromptTokens,
				"candidatesTokenCount": dsChunk.Usage.CompletionTokens,
				"totalTokenCount":      dsChunk.Usage.TotalTokens,
			}
		}

		if len(dsChunk.Choices) == 0 {
			continue
		}
		ch := dsChunk.Choices[0]
		if ch.FinishReason != "" {
			lastFinishReason = mapFinishReason(ch.FinishReason)
		}

		// Accumulate tool calls across stream deltas
		for i, tc := range ch.Delta.ToolCalls {
			idx := i
			if tc.Index != nil {
				idx = *tc.Index
			}
			ptc, exists := toolCallsByIdx[idx]
			if !exists {
				ptc = &partialToolCall{}
				toolCallsByIdx[idx] = ptc
				toolCallOrder = append(toolCallOrder, idx)
			}
			if tc.ID != "" {
				ptc.ID = tc.ID
			}
			if tc.Function.Name != "" {
				ptc.Name += tc.Function.Name
			}
			if tc.Function.Arguments != "" {
				ptc.Args.WriteString(tc.Function.Arguments)
			}
		}

		// Emit live text / reasoning delta chunks immediately
		var deltaParts []map[string]any
		if ch.Delta.ReasoningContent != "" {
			deltaParts = append(deltaParts, map[string]any{
				"text":    ch.Delta.ReasoningContent,
				"thought": true,
			})
			sentAnyTextOrThought = true
		}
		if txt, ok := ch.Delta.Content.(string); ok && txt != "" {
			deltaParts = append(deltaParts, map[string]any{
				"text": txt,
			})
			sentAnyTextOrThought = true
		}

		if len(deltaParts) > 0 {
			chunk := map[string]any{
				"candidates": []any{
					map[string]any{
						"content": map[string]any{
							"role":  "model",
							"parts": deltaParts,
						},
						"index": 0,
					},
				},
				"modelVersion": requestedModel,
			}
			if responseID != "" {
				chunk["responseId"] = responseID
			}
			emitChunk(chunk)
		}
	}

	if !sentFinal {
		flushFinalChunk(lastFinishReason)
	}
}

func mapFinishReason(dsReason string) string {
	switch strings.ToLower(strings.TrimSpace(dsReason)) {
	case "length", "max_tokens":
		return "MAX_TOKENS"
	case "content_filter":
		return "SAFETY"
	default:
		return "STOP"
	}
}

func writeGeminiError(w http.ResponseWriter, statusCode int, message string) {
	if statusCode < 400 || statusCode >= 600 {
		statusCode = http.StatusInternalServerError
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(statusCode)
	errPayload := map[string]any{
		"error": map[string]any{
			"code":    statusCode,
			"message": message,
			"status":  "INTERNAL",
		},
	}
	_ = json.NewEncoder(w).Encode(errPayload)
}
