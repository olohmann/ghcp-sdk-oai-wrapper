package oai

import (
	"encoding/json"
	"strings"
	"time"
)

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

// ImageURL represents an image URL reference in a content part.
type ImageURL struct {
	URL    string `json:"url"`
	Detail string `json:"detail,omitempty"`
}

// ContentPart represents a single part of multimodal content.
type ContentPart struct {
	Type     string    `json:"type"`
	Text     string    `json:"text,omitempty"`
	ImageURL *ImageURL `json:"image_url,omitempty"`
}

// MessageContent handles the polymorphic "content" field in OpenAI messages.
// It can be either a plain string or an array of ContentPart objects.
type MessageContent struct {
	Text  string        // set when content is a plain string
	Parts []ContentPart // set when content is an array of parts
}

// NewTextContent creates a MessageContent from a plain string.
func NewTextContent(s string) MessageContent {
	return MessageContent{Text: s}
}

// TextContent returns all text from this content, concatenating text parts if multimodal.
func (mc MessageContent) TextContent() string {
	if len(mc.Parts) == 0 {
		return mc.Text
	}
	var parts []string
	for _, p := range mc.Parts {
		if p.Type == "text" && p.Text != "" {
			parts = append(parts, p.Text)
		}
	}
	return strings.Join(parts, "\n")
}

// ImageParts returns only the image_url content parts.
func (mc MessageContent) ImageParts() []ContentPart {
	var imgs []ContentPart
	for _, p := range mc.Parts {
		if p.Type == "image_url" && p.ImageURL != nil {
			imgs = append(imgs, p)
		}
	}
	return imgs
}

// IsMultimodal returns true if the content contains non-text parts.
func (mc MessageContent) IsMultimodal() bool {
	return len(mc.ImageParts()) > 0
}

func (mc MessageContent) MarshalJSON() ([]byte, error) {
	if len(mc.Parts) > 0 {
		return json.Marshal(mc.Parts)
	}
	return json.Marshal(mc.Text)
}

func (mc *MessageContent) UnmarshalJSON(data []byte) error {
	// Try string first (most common case).
	var s string
	if err := json.Unmarshal(data, &s); err == nil {
		mc.Text = s
		mc.Parts = nil
		return nil
	}

	// Try array of content parts.
	var parts []ContentPart
	if err := json.Unmarshal(data, &parts); err != nil {
		return err
	}
	mc.Parts = parts
	mc.Text = ""
	return nil
}

// Message represents a chat message in OpenAI format.
type Message struct {
	Role    string         `json:"role"`
	Content MessageContent `json:"content"`
	Name    string         `json:"name,omitempty"`
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
