package handler

import (
	"context"
	"errors"
	"fmt"
	"slices"

	"github.com/olohmann/ghcp-sdk-oai-wrapper/internal/oai"
)

var errInvalidReasoningEffort = errors.New("invalid reasoning_effort")

func requestedReasoningEffort(req *oai.ChatCompletionRequest) string {
	if req.ReasoningEffort == nil {
		return ""
	}
	return *req.ReasoningEffort
}

func validateReasoningEffort(ctx context.Context, client modelLister, req *oai.ChatCompletionRequest) error {
	if req.ReasoningEffort == nil {
		return nil
	}
	effort := *req.ReasoningEffort
	if effort == "" {
		return fmt.Errorf("%w: must not be empty", errInvalidReasoningEffort)
	}
	models, err := client.ListModels(ctx)
	if err != nil {
		return fmt.Errorf("list models for reasoning effort: %w", err)
	}
	for _, model := range models {
		if model.ID != req.Model {
			continue
		}
		if !model.Capabilities.Supports.ReasoningEffort {
			return fmt.Errorf("%w: model %q does not support reasoning effort", errInvalidReasoningEffort, req.Model)
		}
		if len(model.SupportedReasoningEfforts) == 0 {
			return fmt.Errorf("%w: model %q does not report supported reasoning levels", errInvalidReasoningEffort, req.Model)
		}
		if !slices.Contains(model.SupportedReasoningEfforts, effort) {
			return fmt.Errorf("%w: model %q supports %v", errInvalidReasoningEffort, req.Model, model.SupportedReasoningEfforts)
		}
		return nil
	}
	return fmt.Errorf("%w: model %q is not available", errInvalidReasoningEffort, req.Model)
}
