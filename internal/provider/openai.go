package provider

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
)

const (
	OpenAIResponsesAPI = "https://api.openai.com/v1/responses"
	CodexResponsesAPI  = "https://chatgpt.com/backend-api/codex/responses"
)

// OpenAI implements Provider using the OpenAI Responses API.
type OpenAI struct {
	apiKey  string
	baseURL string
	model   string
	maxTokens int
	client  *http.Client
}

func NewOpenAI(apiKey, baseURL, model string, maxTokens int) *OpenAI {
	return &OpenAI{
		apiKey:    apiKey,
		baseURL:   baseURL,
		model:     model,
		maxTokens: maxTokens,
		client:    &http.Client{},
	}
}

// --- Wire types (Responses API format) ---

type oaiRequest struct {
	Model        string          `json:"model"`
	Instructions string          `json:"instructions,omitempty"`
	Input        json.RawMessage `json:"input"`
	Tools        []oaiTool       `json:"tools,omitempty"`
	MaxTokens    int             `json:"max_output_tokens,omitempty"`
	Stream       bool            `json:"stream,omitempty"`
	Store        *bool           `json:"store,omitempty"`
}

type oaiTool struct {
	Type        string          `json:"type"`
	Name        string          `json:"name,omitempty"`
	Description string          `json:"description,omitempty"`
	Parameters  json.RawMessage `json:"parameters,omitempty"`
}

// oaiInputItem represents an item in the input array.
// Different types use different fields — we use json.RawMessage in buildRequest
// to emit the right shape for each item type.

// oaiResponse is the non-streaming response.
type oaiResponse struct {
	ID     string          `json:"id"`
	Output []oaiOutputItem `json:"output"`
	Usage  oaiUsage        `json:"usage"`
}

type oaiOutputItem struct {
	Type    string           `json:"type"` // "message", "function_call", "web_search_call"
	Role    string           `json:"role,omitempty"`
	Content []oaiContentPart `json:"content,omitempty"`
	// Function call fields (type == "function_call"):
	CallID    string `json:"call_id,omitempty"`
	Name      string `json:"name,omitempty"`
	Arguments string `json:"arguments,omitempty"`
}

type oaiContentPart struct {
	Type string `json:"type"` // "output_text"
	Text string `json:"text,omitempty"`
}

type oaiUsage struct {
	InputTokens  int `json:"input_tokens"`
	OutputTokens int `json:"output_tokens"`
}

// --- Request translation ---

func (o *OpenAI) buildRequest(req Request) oaiRequest {
	r := oaiRequest{
		Model:        o.model,
		Instructions: req.SystemPrompt,
	}
	if o.baseURL == CodexResponsesAPI {
		f := false
		r.Store = &f
	} else {
		r.MaxTokens = o.maxTokens
		if req.MaxTokens > 0 {
			r.MaxTokens = req.MaxTokens
		}
	}

	// Add function tools.
	for _, t := range req.Tools {
		r.Tools = append(r.Tools, oaiTool{
			Type:        "function",
			Name:        t.Name,
			Description: t.Description,
			Parameters:  t.InputSchema,
		})
	}

	// Build input array — items have different shapes per type.
	var input []any
	for _, m := range req.Messages {
		input = append(input, toOAIInput(m)...)
	}
	r.Input, _ = json.Marshal(input)

	return r
}

func toOAIInput(m Message) []any {
	// Tool result → function_call_output
	if m.ToolResult != nil {
		return []any{map[string]string{
			"type":    "function_call_output",
			"call_id": m.ToolResult.ToolCallID,
			"output":  m.ToolResult.Content,
		}}
	}

	// Assistant with tool calls → message + function_call items
	if m.Role == RoleAssistant && len(m.ToolCalls) > 0 {
		var items []any
		if m.Content != "" {
			items = append(items, map[string]string{
				"role": "assistant", "content": m.Content,
			})
		}
		for _, tc := range m.ToolCalls {
			args, _ := json.Marshal(map[string]string{"command": tc.Input})
			items = append(items, map[string]string{
				"type":      "function_call",
				"call_id":   tc.ID,
				"name":      tc.Name,
				"arguments": string(args),
			})
		}
		return items
	}

	return []any{map[string]string{
		"role": string(m.Role), "content": m.Content,
	}}
}

// --- Response translation ---

func parseOAIResponse(r oaiResponse) Response {
	resp := Response{
		Usage: Usage{
			InputTokens:  r.Usage.InputTokens,
			OutputTokens: r.Usage.OutputTokens,
		},
	}
	for _, item := range r.Output {
		switch item.Type {
		case "message":
			for _, part := range item.Content {
				if part.Type == "output_text" {
					resp.Text += part.Text
				}
			}
		case "function_call":
			tc := ToolCall{ID: item.CallID, Name: item.Name}
			var args map[string]string
			if json.Unmarshal([]byte(item.Arguments), &args) == nil {
				tc.Input = args["command"]
			}
			resp.ToolCalls = append(resp.ToolCalls, tc)
		}
	}
	return resp
}

func parseOpenAIError(statusCode int, body []byte) error {
	var envelope struct {
		Error struct {
			Message string `json:"message"`
		} `json:"error"`
		Detail string `json:"detail"`
	}
	if json.Unmarshal(body, &envelope) == nil {
		if envelope.Error.Message != "" {
			return fmt.Errorf("openai: %s (HTTP %d)", envelope.Error.Message, statusCode)
		}
		if envelope.Detail != "" {
			return fmt.Errorf("openai: %s (HTTP %d)", envelope.Detail, statusCode)
		}
	}
	return fmt.Errorf("openai: HTTP %d: %s", statusCode, body)
}

// --- Chat (non-streaming) ---

func (o *OpenAI) Chat(ctx context.Context, req Request) (Response, error) {
	r := o.buildRequest(req)
	body, err := json.Marshal(r)
	if err != nil {
		return Response{}, fmt.Errorf("openai: marshal: %w", err)
	}

	httpReq, err := http.NewRequestWithContext(ctx, "POST", o.baseURL, bytes.NewReader(body))
	if err != nil {
		return Response{}, fmt.Errorf("openai: request: %w", err)
	}
	o.setHeaders(httpReq)

	httpResp, err := o.client.Do(httpReq)
	if err != nil {
		return Response{}, fmt.Errorf("openai: do: %w", err)
	}
	defer httpResp.Body.Close()

	respBody, err := io.ReadAll(httpResp.Body)
	if err != nil {
		return Response{}, fmt.Errorf("openai: read: %w", err)
	}

	if httpResp.StatusCode != 200 {
		return Response{}, parseOpenAIError(httpResp.StatusCode, respBody)
	}

	var oaiResp oaiResponse
	if err := json.Unmarshal(respBody, &oaiResp); err != nil {
		return Response{}, fmt.Errorf("openai: unmarshal: %w", err)
	}

	return parseOAIResponse(oaiResp), nil
}

// --- ChatStream (SSE streaming) ---

func (o *OpenAI) ChatStream(ctx context.Context, req Request) (<-chan StreamChunk, error) {
	r := o.buildRequest(req)
	r.Stream = true
	body, err := json.Marshal(r)
	if err != nil {
		return nil, fmt.Errorf("openai: marshal: %w", err)
	}

	httpReq, err := http.NewRequestWithContext(ctx, "POST", o.baseURL, bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("openai: request: %w", err)
	}
	o.setHeaders(httpReq)

	httpResp, err := o.client.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("openai: do: %w", err)
	}

	if httpResp.StatusCode != 200 {
		defer httpResp.Body.Close()
		respBody, _ := io.ReadAll(httpResp.Body)
		return nil, parseOpenAIError(httpResp.StatusCode, respBody)
	}

	ch := make(chan StreamChunk, 16)
	go o.readStream(ctx, httpResp.Body, ch)
	return ch, nil
}

func (o *OpenAI) readStream(ctx context.Context, body io.ReadCloser, ch chan<- StreamChunk) {
	defer close(ch)
	defer body.Close()

	// Track function call argument deltas by item_id.
	type toolState struct {
		callID string
		name   string
		args   strings.Builder
	}
	tools := map[string]*toolState{} // keyed by item_id

	scanner := bufio.NewScanner(body)
	// Increase scanner buffer for potentially large events.
	scanner.Buffer(make([]byte, 0, 256*1024), 256*1024)

	var eventType string
	for scanner.Scan() {
		line := scanner.Text()

		// Parse SSE event type.
		if strings.HasPrefix(line, "event: ") {
			eventType = strings.TrimPrefix(line, "event: ")
			continue
		}

		if !strings.HasPrefix(line, "data: ") {
			continue
		}
		data := strings.TrimPrefix(line, "data: ")

		switch eventType {
		case "response.output_text.delta":
			var ev struct {
				Delta string `json:"delta"`
			}
			if json.Unmarshal([]byte(data), &ev) == nil && ev.Delta != "" {
				if !sendChunk(ctx, ch, StreamChunk{Kind: ChunkText, Text: ev.Delta}) {
					return
				}
			}

		case "response.output_item.added":
			// Track new function_call items.
			var ev struct {
				Item struct {
					ID     string `json:"id"`
					Type   string `json:"type"`
					CallID string `json:"call_id"`
					Name   string `json:"name"`
				} `json:"item"`
			}
			if json.Unmarshal([]byte(data), &ev) == nil && ev.Item.Type == "function_call" {
				tools[ev.Item.ID] = &toolState{
					callID: ev.Item.CallID,
					name:   ev.Item.Name,
				}
			}

		case "response.function_call_arguments.delta":
			var ev struct {
				ItemID string `json:"item_id"`
				Delta  string `json:"delta"`
			}
			if json.Unmarshal([]byte(data), &ev) == nil {
				if ts, ok := tools[ev.ItemID]; ok {
					ts.args.WriteString(ev.Delta)
				}
			}

		case "response.function_call_arguments.done":
			var ev struct {
				ItemID    string `json:"item_id"`
				Name      string `json:"name"`
				Arguments string `json:"arguments"`
			}
			if json.Unmarshal([]byte(data), &ev) == nil {
				var tc ToolCall
				if ts, ok := tools[ev.ItemID]; ok {
					tc.ID = ts.callID
					tc.Name = ts.name
				}
				if ev.Name != "" {
					tc.Name = ev.Name
				}
				var args map[string]string
				if json.Unmarshal([]byte(ev.Arguments), &args) == nil {
					tc.Input = args["command"]
				}
				if !sendChunk(ctx, ch, StreamChunk{Kind: ChunkToolUse, Tool: &tc}) {
					return
				}
			}

		case "response.completed":
			// Extract usage from the completed response.
			var ev struct {
				Response struct {
					Usage oaiUsage `json:"usage"`
				} `json:"response"`
			}
			if json.Unmarshal([]byte(data), &ev) == nil {
				u := ev.Response.Usage
				if u.InputTokens > 0 || u.OutputTokens > 0 {
					sendChunk(ctx, ch, StreamChunk{
						Kind: ChunkUsage,
						Usage: &Usage{
							InputTokens:  u.InputTokens,
							OutputTokens: u.OutputTokens,
						},
					})
				}
			}
			sendChunk(ctx, ch, StreamChunk{Kind: ChunkDone})
			return

		case "response.failed":
			var ev struct {
				Response struct {
					Error struct {
						Message string `json:"message"`
					} `json:"error"`
				} `json:"response"`
			}
			if json.Unmarshal([]byte(data), &ev) == nil && ev.Response.Error.Message != "" {
				sendChunk(ctx, ch, StreamChunk{Kind: ChunkError, Err: fmt.Errorf("openai: %s", ev.Response.Error.Message)})
			}
			return
		}
	}

	if err := scanner.Err(); err != nil && ctx.Err() == nil {
		sendChunk(ctx, ch, StreamChunk{Kind: ChunkError, Err: fmt.Errorf("openai: stream read: %w", err)})
	}
}

func (o *OpenAI) setHeaders(req *http.Request) {
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+o.apiKey)
}
