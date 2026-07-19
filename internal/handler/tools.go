package handler

import (
	"encoding/json"

	copilot "github.com/github/copilot-sdk/go"

	"github.com/olohmann/ghcp-sdk-oai-wrapper/internal/oai"
)

// requestHasTools reports whether the OpenAI request declares any tools.
func requestHasTools(req *oai.ChatCompletionRequest) bool {
	return len(req.Tools) > 0
}

// hasToolResults reports whether the message history already contains tool
// results (role:"tool"), which signals a turn-2 continuation request.
func hasToolResults(messages []oai.Message) bool {
	for _, m := range messages {
		if m.Role == "tool" {
			return true
		}
	}
	return false
}

// toolsToSDK translates OpenAI tool declarations into declaration-only Copilot
// SDK tools (nil handler). The model's calls to a declaration-only tool surface
// as pending ExternalToolRequested events rather than running a handler, which
// is what lets turn-1 emit tool_calls and turn-2 resolve them on the same
// parked session.
func toolsToSDK(tools []oai.Tool) []copilot.Tool {
	out := make([]copilot.Tool, 0, len(tools))
	for _, t := range tools {
		if t.Function.Name == "" {
			continue
		}
		out = append(out, copilot.Tool{
			Name:           t.Function.Name,
			Description:    t.Function.Description,
			Parameters:     t.Function.Parameters,
			SkipPermission: true,
		})
	}
	return out
}

// argumentsToJSON renders tool-call arguments as a JSON object string. A nil or
// unmarshalable value becomes "{}".
func argumentsToJSON(args any) string {
	if args == nil {
		return "{}"
	}
	// If the SDK already handed us a JSON string, pass it through when it is a
	// valid JSON value; otherwise wrap it below via Marshal.
	if s, ok := args.(string); ok {
		if json.Valid([]byte(s)) {
			return s
		}
	}
	b, err := json.Marshal(args)
	if err != nil || len(b) == 0 || string(b) == "null" {
		return "{}"
	}
	return string(b)
}
