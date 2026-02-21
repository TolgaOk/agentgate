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

const anthropicAPI = "https://api.anthropic.com/v1/messages"

type Anthropic struct {
	apiKey    string
	useBearer bool // true = Authorization: Bearer, false = X-API-Key
	model     string
	maxTokens int
	client    *http.Client
}

func NewAnthropic(apiKey, model string, maxTokens int) *Anthropic {
	return &Anthropic{
		apiKey:    apiKey,
		model:     model,
		maxTokens: maxTokens,
		client:    &http.Client{},
	}
}

// --- Wire types (Anthropic API JSON format) ---

type anthropicRequest struct {
	Model     string           `json:"model"`
	MaxTokens int              `json:"max_tokens"`
	System    string           `json:"system,omitempty"`
	Messages  []anthropicMsg   `json:"messages"`
	Tools     []anthropicTool  `json:"tools,omitempty"`
	Stream    bool             `json:"stream,omitempty"`
}

type anthropicMsg struct {
	Role    string            `json:"role"`
	Content json.RawMessage   `json:"content"`
}

type anthropicContentBlock struct {
	Type      string          `json:"type"`
	Text      string          `json:"text,omitempty"`
	ID        string          `json:"id,omitempty"`
	Name      string          `json:"name,omitempty"`
	Input     json.RawMessage `json:"input,omitempty"`
	ToolUseID string          `json:"tool_use_id,omitempty"`
	Content   string          `json:"content,omitempty"`
	IsError   bool            `json:"is_error,omitempty"`
}

type anthropicTool struct {
	Name        string          `json:"name"`
	Description string          `json:"description"`
	InputSchema json.RawMessage `json:"input_schema"`
}

type anthropicResponse struct {
	ID         string                  `json:"id"`
	Content    []anthropicContentBlock `json:"content"`
	StopReason string                  `json:"stop_reason"`
	Usage      anthropicUsage          `json:"usage"`
}

type anthropicUsage struct {
	InputTokens  int `json:"input_tokens"`
	OutputTokens int `json:"output_tokens"`
}

// --- Request translation ---

func (a *Anthropic) buildRequest(req Request) anthropicRequest {
	ar := anthropicRequest{
		Model:     a.model,
		MaxTokens: a.maxTokens,
		System:    req.SystemPrompt,
	}
	if req.MaxTokens > 0 {
		ar.MaxTokens = req.MaxTokens
	}
	for _, t := range req.Tools {
		ar.Tools = append(ar.Tools, anthropicTool{
			Name:        t.Name,
			Description: t.Description,
			InputSchema: t.InputSchema,
		})
	}
	for _, m := range req.Messages {
		ar.Messages = append(ar.Messages, toAnthropicMsg(m))
	}
	return ar
}

func toAnthropicMsg(m Message) anthropicMsg {
	// Tool result message
	if m.ToolResult != nil {
		block := anthropicContentBlock{
			Type:      "tool_result",
			ToolUseID: m.ToolResult.ToolCallID,
			Content:   m.ToolResult.Content,
			IsError:   m.ToolResult.IsError,
		}
		content, _ := json.Marshal([]anthropicContentBlock{block})
		return anthropicMsg{Role: "user", Content: content}
	}

	// Assistant message with tool calls
	if m.Role == RoleAssistant && len(m.ToolCalls) > 0 {
		var blocks []anthropicContentBlock
		if m.Content != "" {
			blocks = append(blocks, anthropicContentBlock{Type: "text", Text: m.Content})
		}
		for _, tc := range m.ToolCalls {
			// Parse the command string into the expected JSON input format.
			input, _ := json.Marshal(map[string]string{"command": tc.Input})
			blocks = append(blocks, anthropicContentBlock{
				Type:  "tool_use",
				ID:    tc.ID,
				Name:  tc.Name,
				Input: input,
			})
		}
		content, _ := json.Marshal(blocks)
		return anthropicMsg{Role: "assistant", Content: content}
	}

	// Simple text message
	content, _ := json.Marshal(m.Content)
	return anthropicMsg{Role: string(m.Role), Content: content}
}

// --- Response translation ---

func parseAnthropicResponse(ar anthropicResponse) Response {
	var resp Response
	resp.StopReason = ar.StopReason
	resp.Usage = Usage{
		InputTokens:  ar.Usage.InputTokens,
		OutputTokens: ar.Usage.OutputTokens,
	}
	for _, block := range ar.Content {
		switch block.Type {
		case "text":
			resp.Text += block.Text
		case "tool_use":
			tc := ToolCall{ID: block.ID, Name: block.Name}
			// Extract "command" from input JSON.
			var input map[string]string
			if json.Unmarshal(block.Input, &input) == nil {
				tc.Input = input["command"]
			}
			resp.ToolCalls = append(resp.ToolCalls, tc)
		}
	}
	return resp
}

// --- Chat (non-streaming) ---

func (a *Anthropic) Chat(ctx context.Context, req Request) (Response, error) {
	ar := a.buildRequest(req)
	body, err := json.Marshal(ar)
	if err != nil {
		return Response{}, fmt.Errorf("anthropic: marshal: %w", err)
	}

	httpReq, err := http.NewRequestWithContext(ctx, "POST", anthropicAPI, bytes.NewReader(body))
	if err != nil {
		return Response{}, fmt.Errorf("anthropic: request: %w", err)
	}
	a.setHeaders(httpReq)

	httpResp, err := a.client.Do(httpReq)
	if err != nil {
		return Response{}, fmt.Errorf("anthropic: do: %w", err)
	}
	defer httpResp.Body.Close()

	respBody, err := io.ReadAll(httpResp.Body)
	if err != nil {
		return Response{}, fmt.Errorf("anthropic: read body: %w", err)
	}

	if httpResp.StatusCode != 200 {
		return Response{}, fmt.Errorf("anthropic: HTTP %d: %s", httpResp.StatusCode, respBody)
	}

	var ar2 anthropicResponse
	if err := json.Unmarshal(respBody, &ar2); err != nil {
		return Response{}, fmt.Errorf("anthropic: unmarshal: %w", err)
	}

	return parseAnthropicResponse(ar2), nil
}

// --- ChatStream (SSE streaming) ---

func (a *Anthropic) ChatStream(ctx context.Context, req Request) (<-chan StreamChunk, error) {
	ar := a.buildRequest(req)
	ar.Stream = true
	body, err := json.Marshal(ar)
	if err != nil {
		return nil, fmt.Errorf("anthropic: marshal: %w", err)
	}

	httpReq, err := http.NewRequestWithContext(ctx, "POST", anthropicAPI, bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("anthropic: request: %w", err)
	}
	a.setHeaders(httpReq)

	httpResp, err := a.client.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("anthropic: do: %w", err)
	}

	if httpResp.StatusCode != 200 {
		defer httpResp.Body.Close()
		respBody, _ := io.ReadAll(httpResp.Body)
		return nil, fmt.Errorf("anthropic: HTTP %d: %s", httpResp.StatusCode, respBody)
	}

	ch := make(chan StreamChunk, 16)
	go a.readStream(ctx, httpResp.Body, ch)
	return ch, nil
}

func (a *Anthropic) readStream(ctx context.Context, body io.ReadCloser, ch chan<- StreamChunk) {
	defer close(ch)
	defer body.Close()

	// Track in-progress tool_use blocks.
	type toolState struct {
		id        string
		name      string
		inputJSON strings.Builder
	}
	var currentTool *toolState

	scanner := bufio.NewScanner(body)
	for scanner.Scan() {
		line := scanner.Text()

		if !strings.HasPrefix(line, "data: ") {
			continue
		}
		data := strings.TrimPrefix(line, "data: ")

		var event struct {
			Type         string                `json:"type"`
			Index        int                   `json:"index"`
			ContentBlock anthropicContentBlock `json:"content_block"`
			Delta        json.RawMessage       `json:"delta"`
			Message      json.RawMessage       `json:"message"`
			Usage        *anthropicUsage       `json:"usage"`
		}
		if err := json.Unmarshal([]byte(data), &event); err != nil {
			continue
		}

		switch event.Type {
		case "message_start":
			// Extract input token usage from the initial message.
			var msg struct {
				Usage anthropicUsage `json:"usage"`
			}
			if json.Unmarshal(event.Message, &msg) == nil && msg.Usage.InputTokens > 0 {
				if !sendChunk(ctx, ch, StreamChunk{
					Kind:  ChunkUsage,
					Usage: &Usage{InputTokens: msg.Usage.InputTokens},
				}) {
					return
				}
			}

		case "content_block_start":
			if event.ContentBlock.Type == "tool_use" {
				currentTool = &toolState{
					id:   event.ContentBlock.ID,
					name: event.ContentBlock.Name,
				}
			}

		case "content_block_delta":
			var delta struct {
				Type        string `json:"type"`
				Text        string `json:"text"`
				PartialJSON string `json:"partial_json"`
			}
			if json.Unmarshal(event.Delta, &delta) != nil {
				continue
			}
			switch delta.Type {
			case "text_delta":
				if !sendChunk(ctx, ch, StreamChunk{Kind: ChunkText, Text: delta.Text}) {
					return
				}
			case "input_json_delta":
				if currentTool != nil {
					currentTool.inputJSON.WriteString(delta.PartialJSON)
				}
			}

		case "content_block_stop":
			if currentTool != nil {
				// Parse accumulated JSON to extract command.
				var input map[string]string
				cmd := currentTool.inputJSON.String()
				if json.Unmarshal([]byte(cmd), &input) == nil {
					cmd = input["command"]
				}
				if !sendChunk(ctx, ch, StreamChunk{
					Kind: ChunkToolUse,
					Tool: &ToolCall{
						ID:    currentTool.id,
						Name:  currentTool.name,
						Input: cmd,
					},
				}) {
					return
				}
				currentTool = nil
			}

		case "message_delta":
			if event.Usage != nil {
				if !sendChunk(ctx, ch, StreamChunk{
					Kind:  ChunkUsage,
					Usage: &Usage{OutputTokens: event.Usage.OutputTokens},
				}) {
					return
				}
			}

		case "message_stop":
			sendChunk(ctx, ch, StreamChunk{Kind: ChunkDone})
			return
		}
	}

	if err := scanner.Err(); err != nil && ctx.Err() == nil {
		sendChunk(ctx, ch, StreamChunk{Kind: ChunkError, Err: fmt.Errorf("anthropic: stream read: %w", err)})
	}
}

// sendChunk sends a chunk on the channel, respecting context cancellation.
// Returns false if context is done (caller should return).
func sendChunk(ctx context.Context, ch chan<- StreamChunk, chunk StreamChunk) bool {
	select {
	case ch <- chunk:
		return true
	case <-ctx.Done():
		return false
	}
}

// NewAnthropicBearer creates a provider using Bearer token auth (OAuth).
func NewAnthropicBearer(token, model string, maxTokens int) *Anthropic {
	return &Anthropic{
		apiKey:    token,
		useBearer: true,
		model:     model,
		maxTokens: maxTokens,
		client:    &http.Client{},
	}
}

func (a *Anthropic) setHeaders(req *http.Request) {
	req.Header.Set("Content-Type", "application/json")
	if a.useBearer {
		req.Header.Set("Authorization", "Bearer "+a.apiKey)
	} else {
		req.Header.Set("X-API-Key", a.apiKey)
	}
	req.Header.Set("Anthropic-Version", "2023-06-01")
}
