package handler

import (
	"encoding/base64"
	"log/slog"
	"os"
	"strings"
	"testing"

	copilot "github.com/github/copilot-sdk/go"

	"github.com/olohmann/ghcp-sdk-oai-wrapper/internal/oai"
)

// silentLogger keeps tests quiet by routing slog output to /dev/null at error level.
func silentLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))
}

func TestParseDataURI(t *testing.T) {
	pngData := []byte{0x89, 0x50, 0x4E, 0x47}
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
	if _, _, err := parseDataURI("https://example.com/image.png"); err == nil {
		t.Error("expected error for non-data URI")
	}
}

func TestParseDataURI_InvalidBase64(t *testing.T) {
	if _, _, err := parseDataURI("data:image/png;base64,!!!invalid!!!"); err == nil {
		t.Error("expected error for invalid base64")
	}
}

func TestParseDataURI_NoBase64Encoding(t *testing.T) {
	if _, _, err := parseDataURI("data:text/plain,Hello"); err == nil {
		t.Error("expected error for non-base64 encoding")
	}
}

func TestExtractAttachments_NoAttachments(t *testing.T) {
	messages := []oai.Message{
		{Role: "user", Content: oai.NewTextContent("Hello")},
	}
	attachments, err := extractAttachments(messages, silentLogger())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(attachments) != 0 {
		t.Errorf("expected 0 attachments, got %d", len(attachments))
	}
}

func TestExtractAttachments_ImageURL_DataURI(t *testing.T) {
	pngData := []byte{0x89, 0x50, 0x4E, 0x47, 0x0D, 0x0A}
	pngB64 := base64.StdEncoding.EncodeToString(pngData)
	dataURI := "data:image/png;base64," + pngB64

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

	attachments, err := extractAttachments(messages, silentLogger())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(attachments) != 1 {
		t.Fatalf("expected 1 attachment, got %d", len(attachments))
	}
	blob, ok := attachments[0].(*copilot.AttachmentBlob)
	if !ok {
		t.Fatalf("expected *AttachmentBlob, got %T", attachments[0])
	}
	if blob.MIMEType != "image/png" {
		t.Errorf("expected MIMEType=image/png, got %q", blob.MIMEType)
	}
	if blob.Data == nil || *blob.Data != pngB64 {
		t.Errorf("expected Data to round-trip to original base64; want len=%d", len(pngB64))
	}
	if blob.DisplayName != nil {
		t.Errorf("expected DisplayName=nil for unnamed image_url, got %q", *blob.DisplayName)
	}
}

func TestExtractAttachments_ImageURL_Multiple(t *testing.T) {
	imgB64 := base64.StdEncoding.EncodeToString([]byte{0xFF, 0xD8, 0xFF})
	messages := []oai.Message{
		{
			Role: "user",
			Content: oai.MessageContent{
				Parts: []oai.ContentPart{
					{Type: "text", Text: "Compare"},
					{Type: "image_url", ImageURL: &oai.ImageURL{URL: "data:image/jpeg;base64," + imgB64}},
					{Type: "image_url", ImageURL: &oai.ImageURL{URL: "data:image/png;base64," + imgB64}},
				},
			},
		},
	}
	attachments, err := extractAttachments(messages, silentLogger())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(attachments) != 2 {
		t.Errorf("expected 2 attachments, got %d", len(attachments))
	}
}

func TestExtractAttachments_ImageURL_SkipsHTTPURLs(t *testing.T) {
	messages := []oai.Message{
		{
			Role: "user",
			Content: oai.MessageContent{
				Parts: []oai.ContentPart{
					{Type: "image_url", ImageURL: &oai.ImageURL{URL: "https://example.com/image.png"}},
				},
			},
		},
	}
	attachments, err := extractAttachments(messages, silentLogger())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(attachments) != 0 {
		t.Errorf("expected 0 attachments (HTTP URLs skipped), got %d", len(attachments))
	}
}

func TestExtractAttachments_ImageURL_RejectsPDF(t *testing.T) {
	b64 := base64.StdEncoding.EncodeToString([]byte("%PDF-1.4"))
	messages := []oai.Message{
		{
			Role: "user",
			Content: oai.MessageContent{
				Parts: []oai.ContentPart{
					{Type: "image_url", ImageURL: &oai.ImageURL{URL: "data:application/pdf;base64," + b64}},
				},
			},
		},
	}
	_, err := extractAttachments(messages, silentLogger())
	if err == nil {
		t.Fatal("expected error rejecting PDF on image_url, got nil")
	}
	if !strings.Contains(err.Error(), "image/*") || !strings.Contains(err.Error(), "file") {
		t.Errorf("expected error to mention image/* and file part, got: %v", err)
	}
}

func TestExtractAttachments_FilePart_PDF(t *testing.T) {
	pdfData := []byte("%PDF-1.4 fake content for test")
	pdfB64 := base64.StdEncoding.EncodeToString(pdfData)
	dataURI := "data:application/pdf;base64," + pdfB64

	messages := []oai.Message{
		{
			Role: "user",
			Content: oai.MessageContent{
				Parts: []oai.ContentPart{
					{Type: "text", Text: "Summarize this PDF"},
					{Type: "file", File: &oai.FilePart{FileData: dataURI, Filename: "report.pdf"}},
				},
			},
		},
	}

	attachments, err := extractAttachments(messages, silentLogger())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(attachments) != 1 {
		t.Fatalf("expected 1 attachment, got %d", len(attachments))
	}
	blob, ok := attachments[0].(*copilot.AttachmentBlob)
	if !ok {
		t.Fatalf("expected *AttachmentBlob, got %T", attachments[0])
	}
	if blob.MIMEType != "application/pdf" {
		t.Errorf("expected MIMEType=application/pdf, got %q", blob.MIMEType)
	}
	if blob.Data == nil || *blob.Data != pdfB64 {
		t.Errorf("expected Data to round-trip to original base64")
	}
	if blob.DisplayName == nil || *blob.DisplayName != "report.pdf" {
		t.Errorf("expected DisplayName=report.pdf, got %v", blob.DisplayName)
	}
}

func TestExtractAttachments_FilePart_FilenameSanitized(t *testing.T) {
	b64 := base64.StdEncoding.EncodeToString([]byte("x"))
	messages := []oai.Message{
		{
			Role: "user",
			Content: oai.MessageContent{
				Parts: []oai.ContentPart{
					{Type: "file", File: &oai.FilePart{
						FileData: "data:application/pdf;base64," + b64,
						Filename: "/etc/passwd",
					}},
				},
			},
		},
	}
	attachments, err := extractAttachments(messages, silentLogger())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	blob := attachments[0].(*copilot.AttachmentBlob)
	if blob.DisplayName == nil || *blob.DisplayName != "passwd" {
		t.Errorf("expected sanitized DisplayName=passwd, got %v", blob.DisplayName)
	}
}

func TestExtractAttachments_FilePart_FileIDRejected(t *testing.T) {
	messages := []oai.Message{
		{
			Role: "user",
			Content: oai.MessageContent{
				Parts: []oai.ContentPart{
					{Type: "file", File: &oai.FilePart{FileID: "file-abc123"}},
				},
			},
		},
	}
	_, err := extractAttachments(messages, silentLogger())
	if err == nil {
		t.Fatal("expected error for file_id, got nil")
	}
	if !strings.Contains(err.Error(), "file_id is not supported") {
		t.Errorf("unexpected error message: %v", err)
	}
}

func TestExtractAttachments_FilePart_MissingFileData(t *testing.T) {
	messages := []oai.Message{
		{
			Role: "user",
			Content: oai.MessageContent{
				Parts: []oai.ContentPart{
					{Type: "file", File: &oai.FilePart{Filename: "empty.pdf"}},
				},
			},
		},
	}
	_, err := extractAttachments(messages, silentLogger())
	if err == nil {
		t.Fatal("expected error for missing file_data, got nil")
	}
	if !strings.Contains(err.Error(), "file_data") {
		t.Errorf("unexpected error message: %v", err)
	}
}

func TestExtractAttachments_FilePart_NonDataURI(t *testing.T) {
	messages := []oai.Message{
		{
			Role: "user",
			Content: oai.MessageContent{
				Parts: []oai.ContentPart{
					{Type: "file", File: &oai.FilePart{FileData: "https://example.com/doc.pdf"}},
				},
			},
		},
	}
	_, err := extractAttachments(messages, silentLogger())
	if err == nil {
		t.Fatal("expected error for non-data URI, got nil")
	}
}

func TestExtractAttachments_FilePart_NoFilenamePassThrough(t *testing.T) {
	// An unnamed file part should still produce a Blob with the MIME type
	// preserved; DisplayName stays nil.
	b64 := base64.StdEncoding.EncodeToString([]byte("hello"))
	messages := []oai.Message{
		{
			Role: "user",
			Content: oai.MessageContent{
				Parts: []oai.ContentPart{
					{Type: "file", File: &oai.FilePart{
						FileData: "data:application/octet-stream;base64," + b64,
					}},
				},
			},
		},
	}
	attachments, err := extractAttachments(messages, silentLogger())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	blob := attachments[0].(*copilot.AttachmentBlob)
	if blob.MIMEType != "application/octet-stream" {
		t.Errorf("expected MIME passed through, got %q", blob.MIMEType)
	}
	if blob.DisplayName != nil {
		t.Errorf("expected DisplayName=nil for unnamed file part, got %q", *blob.DisplayName)
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
