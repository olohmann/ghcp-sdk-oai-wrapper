package metrics

import (
	"strings"
	"testing"

	"github.com/prometheus/client_golang/prometheus"
)

func resetKnownModels(t *testing.T) {
	t.Helper()
	knownModels.Store(nil)
	t.Cleanup(func() { knownModels.Store(nil) })
}

func TestSanitizeModel_KnownModels(t *testing.T) {
	resetKnownModels(t)
	SetKnownModels([]string{"grok-4.7", "mai-code-2", "claude-sonnet-5.5"})

	cases := map[string]string{
		"grok-4.7":                       "grok-4.7",
		"mai-code-2":                     "mai-code-2",
		"claude-sonnet-5.5":              "claude-sonnet-5.5",
		"auto":                           "auto",
		"no-such-model":                  "other",
		"claude-not-in-catalog":          "other",
		"":                               "other",
		strings.Repeat("x", 65):          "other",
		"gpt-" + strings.Repeat("x", 61): "other",
	}
	for in, want := range cases {
		if got := sanitizeModel(in); got != want {
			t.Errorf("sanitizeModel(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestSanitizeModel_PrefixFallbackWithoutCatalog(t *testing.T) {
	resetKnownModels(t)
	for in, want := range map[string]string{
		"claude-sonnet-5.5": "claude-sonnet-5.5",
		"gpt-6-sol":         "gpt-6-sol",
		"grok-4.7":          "other",
		"no-such-model":     "other",
	} {
		if got := sanitizeModel(in); got != want {
			t.Errorf("sanitizeModel(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestSetKnownModels_OverlongIDsNotLabels(t *testing.T) {
	resetKnownModels(t)
	long := strings.Repeat("y", 65)
	SetKnownModels([]string{long})
	if got := sanitizeModel(long); got != "other" {
		t.Errorf("overlong catalog id must not become a label, got %q", got)
	}
}

// completionCounterValues returns the copilot_chat_completions_total samples
// currently exported, keyed by "model|stream|status".
func completionCounterValues(t *testing.T) map[string]float64 {
	t.Helper()
	families, err := prometheus.DefaultGatherer.Gather()
	if err != nil {
		t.Fatal(err)
	}
	out := map[string]float64{}
	for _, f := range families {
		if f.GetName() != "copilot_chat_completions_total" {
			continue
		}
		for _, m := range f.GetMetric() {
			labels := map[string]string{}
			for _, l := range m.GetLabel() {
				labels[l.GetName()] = l.GetValue()
			}
			out[labels["model"]+"|"+labels["stream"]+"|"+labels["status"]] = m.GetCounter().GetValue()
		}
	}
	return out
}

func TestSetKnownModels_ZeroInitialisesCounters(t *testing.T) {
	resetKnownModels(t)
	models := []string{"zero-init-model-a", "zero-init-model-b"}
	SetKnownModels(models)

	values := completionCounterValues(t)
	for _, m := range append(models, "auto", "other") {
		for _, stream := range []string{"true", "false"} {
			for _, status := range []string{"success", "error"} {
				key := m + "|" + stream + "|" + status
				v, ok := values[key]
				if !ok {
					t.Errorf("series %s not pre-created", key)
					continue
				}
				if m != "auto" && m != "other" && v != 0 {
					t.Errorf("series %s = %v, want 0", key, v)
				}
			}
		}
	}

	// The first real request must increment the pre-created series to 1.
	RecordCompletion("zero-init-model-a", true, "success", 0)
	if got := completionCounterValues(t)["zero-init-model-a|true|success"]; got != 1 {
		t.Errorf("first request counted as %v, want 1", got)
	}
}
