package handler

import (
	"encoding/base64"
	"log/slog"
	"os"
	"testing"

	"github.com/olohmann/ghcp-sdk-oai-wrapper/internal/oai"
)

func TestParseDataURI(t *testing.T) {
	pngData := []byte{0x89, 0x50, 0x4E, 0x47} // PNG magic bytes
	b64 := base64.StdEncoding.EncodeToString(pngData)
	uri := "data:image/png;base64," + b64

	mime, data, err := parseDataURI(uri)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if mime != "image/png" {
		t.Errorf("expected image/png, got %q", mime)
	}
	if len(data) != len(pngData) {
		t.Errorf("expected %d bytes, got %d", len(pngData), len(data))
	}
}

func TestParseDataURI_NotDataURI(t *testing.T) {
	_, _, err := parseDataURI("https://example.com/image.png")
	if err == nil {
		t.Error("expected error for non-data URI")
	}
}

func TestParseDataURI_InvalidBase64(t *testing.T) {
	_, _, err := parseDataURI("data:image/png;base64,!!!invalid!!!")
	if err == nil {
		t.Error("expected error for invalid base64")
	}
}

func TestParseDataURI_NoBase64Encoding(t *testing.T) {
	_, _, err := parseDataURI("data:text/plain,Hello")
	if err == nil {
		t.Error("expected error for non-base64 encoding")
	}
}

func TestExtractImageAttachments_NoImages(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))
	messages := []oai.Message{
		{Role: "user", Content: oai.NewTextContent("Hello")},
	}

	attachments, cleanup, err := extractImageAttachments(messages, logger)
	defer cleanup()

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(attachments) != 0 {
		t.Errorf("expected 0 attachments, got %d", len(attachments))
	}
}

func TestExtractImageAttachments_WithDataURI(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))

	pngData := []byte{0x89, 0x50, 0x4E, 0x47, 0x0D, 0x0A}
	b64 := base64.StdEncoding.EncodeToString(pngData)
	dataURI := "data:image/png;base64," + b64

	messages := []oai.Message{
		{
			Role: "user",
			Content: oai.MessageContent{
				Parts: []oai.ContentPart{
					{Type: "text", Text: "What is this?"},
					{Type: "image_url", ImageURL: &oai.ImageURL{URL: dataURI}},
				},
			},
		},
	}

	attachments, cleanup, err := extractImageAttachments(messages, logger)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	defer cleanup()

	if len(attachments) != 1 {
		t.Fatalf("expected 1 attachment, got %d", len(attachments))
	}
	if attachments[0].Path == nil {
		t.Fatal("expected attachment Path to be set")
	}

	// Verify the temp file exists and contains the right data
	content, err := os.ReadFile(*attachments[0].Path)
	if err != nil {
		t.Fatalf("failed to read temp file: %v", err)
	}
	if len(content) != len(pngData) {
		t.Errorf("expected %d bytes in file, got %d", len(pngData), len(content))
	}

	// Run cleanup and verify file is removed
	cleanup()
	if _, err := os.Stat(*attachments[0].Path); !os.IsNotExist(err) {
		t.Error("expected temp file to be removed after cleanup")
	}
}

func TestExtractImageAttachments_MultipleImages(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))

	imgData := []byte{0xFF, 0xD8, 0xFF}
	b64 := base64.StdEncoding.EncodeToString(imgData)

	messages := []oai.Message{
		{
			Role: "user",
			Content: oai.MessageContent{
				Parts: []oai.ContentPart{
					{Type: "text", Text: "Compare these images"},
					{Type: "image_url", ImageURL: &oai.ImageURL{URL: "data:image/jpeg;base64," + b64}},
					{Type: "image_url", ImageURL: &oai.ImageURL{URL: "data:image/png;base64," + b64}},
				},
			},
		},
	}

	attachments, cleanup, err := extractImageAttachments(messages, logger)
	defer cleanup()

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(attachments) != 2 {
		t.Errorf("expected 2 attachments, got %d", len(attachments))
	}
}

func TestExtractImageAttachments_SkipsHTTPURLs(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))

	messages := []oai.Message{
		{
			Role: "user",
			Content: oai.MessageContent{
				Parts: []oai.ContentPart{
					{Type: "text", Text: "What is this?"},
					{Type: "image_url", ImageURL: &oai.ImageURL{URL: "https://example.com/image.png"}},
				},
			},
		},
	}

	attachments, cleanup, err := extractImageAttachments(messages, logger)
	defer cleanup()

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(attachments) != 0 {
		t.Errorf("expected 0 attachments (HTTP URLs skipped), got %d", len(attachments))
	}
}

func TestBuildPrompt_MultimodalContent(t *testing.T) {
	messages := []oai.Message{
		{Role: "system", Content: oai.NewTextContent("You are helpful.")},
		{
			Role: "user",
			Content: oai.MessageContent{
				Parts: []oai.ContentPart{
					{Type: "text", Text: "Describe this image"},
					{Type: "image_url", ImageURL: &oai.ImageURL{URL: "data:image/png;base64,abc"}},
				},
			},
		},
	}

	prompt := buildPrompt(messages)
	if prompt != "Describe this image" {
		t.Errorf("expected 'Describe this image', got %q", prompt)
	}
}

func TestExtractSystemMessage_MultimodalContent(t *testing.T) {
	messages := []oai.Message{
		{
			Role: "system",
			Content: oai.MessageContent{
				Parts: []oai.ContentPart{
					{Type: "text", Text: "System instruction"},
				},
			},
		},
		{Role: "user", Content: oai.NewTextContent("Hello")},
	}

	sysMsg := extractSystemMessage(messages)
	if sysMsg != "System instruction" {
		t.Errorf("expected 'System instruction', got %q", sysMsg)
	}
}
