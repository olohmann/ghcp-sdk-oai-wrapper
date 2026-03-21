package ollama

import "time"

// ChatRequest is the Ollama POST /api/chat request body.
type ChatRequest struct {
	Model    string    `json:"model"`
	Messages []Message `json:"messages"`
	Stream   *bool     `json:"stream,omitempty"`
	Options  *Options  `json:"options,omitempty"`
	Format   string    `json:"format,omitempty"`
}

// IsStreaming returns true if streaming is requested (default: true).
func (r *ChatRequest) IsStreaming() bool {
	if r.Stream == nil {
		return true
	}
	return *r.Stream
}

// GenerateRequest is the Ollama POST /api/generate request body.
type GenerateRequest struct {
	Model   string   `json:"model"`
	Prompt  string   `json:"prompt"`
	System  string   `json:"system,omitempty"`
	Stream  *bool    `json:"stream,omitempty"`
	Options *Options `json:"options,omitempty"`
	Format  string   `json:"format,omitempty"`
}

// IsStreaming returns true if streaming is requested (default: true).
func (r *GenerateRequest) IsStreaming() bool {
	if r.Stream == nil {
		return true
	}
	return *r.Stream
}

// Message represents a chat message in Ollama format.
type Message struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

// Options holds model parameters.
type Options struct {
	Temperature *float64 `json:"temperature,omitempty"`
	TopP        *float64 `json:"top_p,omitempty"`
	NumPredict  *int     `json:"num_predict,omitempty"`
	Stop        []string `json:"stop,omitempty"`
}

// ChatResponse is the non-streaming POST /api/chat response.
type ChatResponse struct {
	Model              string  `json:"model"`
	CreatedAt          string  `json:"created_at"`
	Message            Message `json:"message"`
	Done               bool    `json:"done"`
	DoneReason         string  `json:"done_reason,omitempty"`
	TotalDuration      int64   `json:"total_duration,omitempty"`
	LoadDuration       int64   `json:"load_duration,omitempty"`
	PromptEvalCount    int     `json:"prompt_eval_count,omitempty"`
	PromptEvalDuration int64   `json:"prompt_eval_duration,omitempty"`
	EvalCount          int     `json:"eval_count,omitempty"`
	EvalDuration       int64   `json:"eval_duration,omitempty"`
}

// ChatStreamChunk is a single NDJSON chunk in streaming chat mode.
type ChatStreamChunk struct {
	Model              string  `json:"model"`
	CreatedAt          string  `json:"created_at"`
	Message            Message `json:"message"`
	Done               bool    `json:"done"`
	DoneReason         string  `json:"done_reason,omitempty"`
	TotalDuration      int64   `json:"total_duration,omitempty"`
	LoadDuration       int64   `json:"load_duration,omitempty"`
	PromptEvalCount    int     `json:"prompt_eval_count,omitempty"`
	PromptEvalDuration int64   `json:"prompt_eval_duration,omitempty"`
	EvalCount          int     `json:"eval_count,omitempty"`
	EvalDuration       int64   `json:"eval_duration,omitempty"`
}

// GenerateResponse is the non-streaming POST /api/generate response.
type GenerateResponse struct {
	Model              string `json:"model"`
	CreatedAt          string `json:"created_at"`
	Response           string `json:"response"`
	Done               bool   `json:"done"`
	DoneReason         string `json:"done_reason,omitempty"`
	TotalDuration      int64  `json:"total_duration,omitempty"`
	LoadDuration       int64  `json:"load_duration,omitempty"`
	PromptEvalCount    int    `json:"prompt_eval_count,omitempty"`
	PromptEvalDuration int64  `json:"prompt_eval_duration,omitempty"`
	EvalCount          int    `json:"eval_count,omitempty"`
	EvalDuration       int64  `json:"eval_duration,omitempty"`
}

// GenerateStreamChunk is a single NDJSON chunk in streaming generate mode.
type GenerateStreamChunk struct {
	Model         string `json:"model"`
	CreatedAt     string `json:"created_at"`
	Response      string `json:"response"`
	Done          bool   `json:"done"`
	DoneReason    string `json:"done_reason,omitempty"`
	TotalDuration int64  `json:"total_duration,omitempty"`
}

// TagsResponse is the GET /api/tags response.
type TagsResponse struct {
	Models []ModelInfo `json:"models"`
}

// ModelInfo describes a model in Ollama's tag list format.
type ModelInfo struct {
	Name       string       `json:"name"`
	Model      string       `json:"model"`
	ModifiedAt string       `json:"modified_at"`
	Size       int64        `json:"size"`
	Digest     string       `json:"digest"`
	Details    ModelDetails `json:"details"`
}

// ModelDetails contains model metadata.
type ModelDetails struct {
	Format            string `json:"format"`
	Family            string `json:"family"`
	ParameterSize     string `json:"parameter_size"`
	QuantizationLevel string `json:"quantization_level"`
}

// ShowRequest is the POST /api/show request body.
type ShowRequest struct {
	Model string `json:"model"`
}

// ShowResponse is the POST /api/show response.
type ShowResponse struct {
	Modelfile  string       `json:"modelfile"`
	Parameters string       `json:"parameters"`
	Template   string       `json:"template"`
	Details    ModelDetails `json:"details"`
}

// VersionResponse is the GET /api/version response.
type VersionResponse struct {
	Version string `json:"version"`
}

// NowRFC3339Milli returns the current time in the format Ollama uses.
func NowRFC3339Milli() string {
	return time.Now().UTC().Format("2006-01-02T15:04:05.000Z")
}

// BoolPtr returns a pointer to a bool.
func BoolPtr(b bool) *bool {
	return &b
}
