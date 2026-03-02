package oai

import "time"

// ChatCompletionRequest represents the OpenAI chat completion request body.
type ChatCompletionRequest struct {
	Model            string         `json:"model"`
	Messages         []Message      `json:"messages"`
	Temperature      *float64       `json:"temperature,omitempty"`
	TopP             *float64       `json:"top_p,omitempty"`
	N                *int           `json:"n,omitempty"`
	Stream           bool           `json:"stream,omitempty"`
	Stop             any            `json:"stop,omitempty"`
	MaxTokens        *int           `json:"max_tokens,omitempty"`
	PresencePenalty  *float64       `json:"presence_penalty,omitempty"`
	FrequencyPenalty *float64       `json:"frequency_penalty,omitempty"`
	User             string         `json:"user,omitempty"`
	StreamOptions    *StreamOptions `json:"stream_options,omitempty"`
}

// StreamOptions controls streaming behavior.
type StreamOptions struct {
	IncludeUsage bool `json:"include_usage,omitempty"`
}

// Message represents a chat message in OpenAI format.
type Message struct {
	Role    string `json:"role"`
	Content string `json:"content"`
	Name    string `json:"name,omitempty"`
}

// ChatCompletionResponse represents the OpenAI non-streaming response.
type ChatCompletionResponse struct {
	ID                string   `json:"id"`
	Object            string   `json:"object"`
	Created           int64    `json:"created"`
	Model             string   `json:"model"`
	Choices           []Choice `json:"choices"`
	Usage             *Usage   `json:"usage,omitempty"`
	SystemFingerprint string   `json:"system_fingerprint,omitempty"`
}

// Choice represents a single completion choice.
type Choice struct {
	Index        int      `json:"index"`
	Message      *Message `json:"message,omitempty"`
	Delta        *Message `json:"delta,omitempty"`
	FinishReason *string  `json:"finish_reason"`
}

// Usage tracks token usage.
type Usage struct {
	PromptTokens     int `json:"prompt_tokens"`
	CompletionTokens int `json:"completion_tokens"`
	TotalTokens      int `json:"total_tokens"`
}

// ChatCompletionChunk represents a single SSE chunk in streaming mode.
type ChatCompletionChunk struct {
	ID                string   `json:"id"`
	Object            string   `json:"object"`
	Created           int64    `json:"created"`
	Model             string   `json:"model"`
	Choices           []Choice `json:"choices"`
	Usage             *Usage   `json:"usage,omitempty"`
	SystemFingerprint string   `json:"system_fingerprint,omitempty"`
}

// ModelObject represents a model in the OpenAI models list response.
type ModelObject struct {
	ID      string `json:"id"`
	Object  string `json:"object"`
	Created int64  `json:"created"`
	OwnedBy string `json:"owned_by"`
}

// ModelListResponse represents the GET /v1/models response.
type ModelListResponse struct {
	Object string        `json:"object"`
	Data   []ModelObject `json:"data"`
}

// ErrorResponse represents an OpenAI API error.
type ErrorResponse struct {
	Error ErrorDetail `json:"error"`
}

// ErrorDetail contains error information.
type ErrorDetail struct {
	Message string  `json:"message"`
	Type    string  `json:"type"`
	Param   *string `json:"param"`
	Code    *string `json:"code"`
}

// NewCompletionID generates a chat completion ID.
func NewCompletionID() string {
	return "chatcmpl-" + randomID()
}

// NowUnix returns the current Unix timestamp.
func NowUnix() int64 {
	return time.Now().Unix()
}

// StringPtr returns a pointer to a string.
func StringPtr(s string) *string {
	return &s
}

// randomID produces a simple unique ID suffix.
func randomID() string {
	return time.Now().Format("20060102150405.000000000")
}
