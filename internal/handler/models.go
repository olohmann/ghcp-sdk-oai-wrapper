package handler

import (
	"context"
	"log/slog"
	"net/http"

	copilot "github.com/github/copilot-sdk/go"

	"github.com/olohmann/ghcp-sdk-oai-wrapper/internal/oai"
)

type modelLister interface {
	ListModels(context.Context) ([]copilot.ModelInfo, error)
}

// Models returns the handler for GET /v1/models.
func Models(client modelLister, logger *slog.Logger) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			oai.WriteError(w, http.StatusMethodNotAllowed, "method not allowed", "invalid_request_error")
			return
		}

		models, err := client.ListModels(r.Context())
		if err != nil {
			logger.Error("failed to list models", "error", err)
			oai.WriteError(w, http.StatusInternalServerError, "failed to list models", "server_error")
			return
		}

		data := make([]oai.ModelObject, 0, len(models))
		for _, m := range models {
			data = append(data, oai.ModelObject{
				ID:                        m.ID,
				Object:                    "model",
				Created:                   oai.NowUnix(),
				OwnedBy:                   "github-copilot",
				ContextWindow:             m.Capabilities.Limits.MaxContextWindowTokens,
				SupportsVision:            m.Capabilities.Supports.Vision,
				SupportsReasoningEffort:   m.Capabilities.Supports.ReasoningEffort,
				SupportedReasoningEfforts: m.SupportedReasoningEfforts,
				DefaultReasoningEffort:    m.DefaultReasoningEffort,
			})
		}

		oai.WriteJSON(w, http.StatusOK, oai.ModelListResponse{
			Object: "list",
			Data:   data,
		})
	}
}
