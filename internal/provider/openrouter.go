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

const openRouterAPI = "https://openrouter.ai/api/v1/chat/completions"

// OpenRouter implements Provider using the OpenAI-compatible API.
type OpenRouter struct {
	apiKey    string
	model     string
	maxTokens int
	client    *http.Client
}

func NewOpenRouter(apiKey, model string, maxTokens int) *OpenRouter {
	return &OpenRouter{
		apiKey:    apiKey,
		model:     model,
		maxTokens: maxTokens,
		client:    &http.Client{},
	}
}

// --- Wire types (OpenAI-compatible format) ---

type orRequest struct {
	Model     string     `json:"model"`
	MaxTokens int        `json:"max_tokens"`
	Messages  []orMsg    `json:"messages"`
	Tools     []orTool   `json:"tools,omitempty"`
	Stream    bool       `json:"stream,omitempty"`
}

type orMsg struct {
	Role       string          `json:"role"`
	Content    string          `json:"content,omitempty"`
	ToolCalls  []orToolCall    `json:"tool_calls,omitempty"`
	ToolCallID string          `json:"tool_call_id,omitempty"`
}

type orToolCall struct {
	Index    int        `json:"index"`
	ID       string     `json:"id"`
	Type     string     `json:"type"`
	Function orFunction `json:"function"`
}

type orFunction struct {
	Name      string `json:"name"`
	Arguments string `json:"arguments"`
}

type orTool struct {
	Type     string       `json:"type"`
	Function orToolFunc   `json:"function"`
}

type orToolFunc struct {
	Name        string          `json:"name"`
	Description string          `json:"description"`
	Parameters  json.RawMessage `json:"parameters"`
}

type orResponse struct {
	Choices []orChoice `json:"choices"`
	Usage   orUsage    `json:"usage"`
}

type orChoice struct {
	Message orMsg  `json:"message"`
	Delta   orMsg  `json:"delta"`
}

type orUsage struct {
	PromptTokens     int `json:"prompt_tokens"`
	CompletionTokens int `json:"completion_tokens"`
}

// --- Request translation ---

func (o *OpenRouter) buildRequest(req Request) orRequest {
	r := orRequest{
		Model:     o.model,
		MaxTokens: o.maxTokens,
	}
	if req.MaxTokens > 0 {
		r.MaxTokens = req.MaxTokens
	}
	for _, t := range req.Tools {
		r.Tools = append(r.Tools, orTool{
			Type: "function",
			Function: orToolFunc{
				Name:        t.Name,
				Description: t.Description,
				Parameters:  t.InputSchema,
			},
		})
	}

	// System prompt as first message.
	if req.SystemPrompt != "" {
		r.Messages = append(r.Messages, orMsg{Role: "system", Content: req.SystemPrompt})
	}

	for _, m := range req.Messages {
		r.Messages = append(r.Messages, toORMsg(m))
	}
	return r
}

func toORMsg(m Message) orMsg {
	// Tool result
	if m.ToolResult != nil {
		return orMsg{
			Role:       "tool",
			Content:    m.ToolResult.Content,
			ToolCallID: m.ToolResult.ToolCallID,
		}
	}

	// Assistant with tool calls
	if m.Role == RoleAssistant && len(m.ToolCalls) > 0 {
		msg := orMsg{Role: "assistant", Content: m.Content}
		for _, tc := range m.ToolCalls {
			args, _ := json.Marshal(map[string]string{"command": tc.Input})
			msg.ToolCalls = append(msg.ToolCalls, orToolCall{
				ID:   tc.ID,
				Type: "function",
				Function: orFunction{
					Name:      tc.Name,
					Arguments: string(args),
				},
			})
		}
		return msg
	}

	return orMsg{Role: string(m.Role), Content: m.Content}
}

// --- Chat (non-streaming) ---

func (o *OpenRouter) Chat(ctx context.Context, req Request) (Response, error) {
	r := o.buildRequest(req)
	body, err := json.Marshal(r)
	if err != nil {
		return Response{}, fmt.Errorf("openrouter: marshal: %w", err)
	}

	httpReq, err := http.NewRequestWithContext(ctx, "POST", openRouterAPI, bytes.NewReader(body))
	if err != nil {
		return Response{}, fmt.Errorf("openrouter: request: %w", err)
	}
	o.setHeaders(httpReq)

	httpResp, err := o.client.Do(httpReq)
	if err != nil {
		return Response{}, fmt.Errorf("openrouter: do: %w", err)
	}
	defer httpResp.Body.Close()

	respBody, err := io.ReadAll(httpResp.Body)
	if err != nil {
		return Response{}, fmt.Errorf("openrouter: read: %w", err)
	}

	if httpResp.StatusCode != 200 {
		return Response{}, fmt.Errorf("openrouter: HTTP %d: %s", httpResp.StatusCode, respBody)
	}

	var or orResponse
	if err := json.Unmarshal(respBody, &or); err != nil {
		return Response{}, fmt.Errorf("openrouter: unmarshal: %w", err)
	}

	return parseORResponse(or), nil
}

func parseORResponse(or orResponse) Response {
	resp := Response{
		Usage: Usage{
			InputTokens:  or.Usage.PromptTokens,
			OutputTokens: or.Usage.CompletionTokens,
		},
	}
	if len(or.Choices) > 0 {
		msg := or.Choices[0].Message
		resp.Text = msg.Content
		for _, tc := range msg.ToolCalls {
			call := ToolCall{ID: tc.ID, Name: tc.Function.Name}
			var args map[string]string
			if json.Unmarshal([]byte(tc.Function.Arguments), &args) == nil {
				call.Input = args["command"]
			}
			resp.ToolCalls = append(resp.ToolCalls, call)
		}
	}
	return resp
}

// --- ChatStream (SSE streaming) ---

func (o *OpenRouter) ChatStream(ctx context.Context, req Request) (<-chan StreamChunk, error) {
	r := o.buildRequest(req)
	r.Stream = true
	body, err := json.Marshal(r)
	if err != nil {
		return nil, fmt.Errorf("openrouter: marshal: %w", err)
	}

	httpReq, err := http.NewRequestWithContext(ctx, "POST", openRouterAPI, bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("openrouter: request: %w", err)
	}
	o.setHeaders(httpReq)

	httpResp, err := o.client.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("openrouter: do: %w", err)
	}

	if httpResp.StatusCode != 200 {
		defer httpResp.Body.Close()
		respBody, _ := io.ReadAll(httpResp.Body)
		return nil, fmt.Errorf("openrouter: HTTP %d: %s", httpResp.StatusCode, respBody)
	}

	ch := make(chan StreamChunk, 16)
	go o.readStream(ctx, httpResp.Body, ch)
	return ch, nil
}

func (o *OpenRouter) readStream(ctx context.Context, body io.ReadCloser, ch chan<- StreamChunk) {
	defer close(ch)
	defer body.Close()

	// Track in-progress tool calls by index.
	type toolState struct {
		id   string
		name string
		args strings.Builder
	}
	tools := map[int]*toolState{}

	scanner := bufio.NewScanner(body)
	for scanner.Scan() {
		line := scanner.Text()
		if !strings.HasPrefix(line, "data: ") {
			continue
		}
		data := strings.TrimPrefix(line, "data: ")
		if data == "[DONE]" {
			// Emit any accumulated tool calls.
			for _, ts := range tools {
				tc := ToolCall{ID: ts.id, Name: ts.name}
				var args map[string]string
				if json.Unmarshal([]byte(ts.args.String()), &args) == nil {
					tc.Input = args["command"]
				}
				if !sendChunk(ctx, ch, StreamChunk{Kind: ChunkToolUse, Tool: &tc}) {
					return
				}
			}
			sendChunk(ctx, ch, StreamChunk{Kind: ChunkDone})
			return
		}

		var event struct {
			Choices []struct {
				Delta orMsg `json:"delta"`
			} `json:"choices"`
			Usage *orUsage `json:"usage"`
		}
		if json.Unmarshal([]byte(data), &event) != nil {
			continue
		}

		if len(event.Choices) > 0 {
			delta := event.Choices[0].Delta

			// Text delta.
			if delta.Content != "" {
				if !sendChunk(ctx, ch, StreamChunk{Kind: ChunkText, Text: delta.Content}) {
					return
				}
			}

			// Tool call deltas.
			for _, tc := range delta.ToolCalls {
				idx := tc.Index
				if _, ok := tools[idx]; !ok {
					tools[idx] = &toolState{id: tc.ID, name: tc.Function.Name}
				}
				ts := tools[idx]
				if tc.ID != "" {
					ts.id = tc.ID
				}
				if tc.Function.Name != "" {
					ts.name = tc.Function.Name
				}
				ts.args.WriteString(tc.Function.Arguments)
			}
		}

		// Usage (some providers send it in the stream).
		if event.Usage != nil && (event.Usage.PromptTokens > 0 || event.Usage.CompletionTokens > 0) {
			if !sendChunk(ctx, ch, StreamChunk{
				Kind: ChunkUsage,
				Usage: &Usage{
					InputTokens:  event.Usage.PromptTokens,
					OutputTokens: event.Usage.CompletionTokens,
				},
			}) {
				return
			}
		}
	}
}

func (o *OpenRouter) setHeaders(req *http.Request) {
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+o.apiKey)
}
