package audioinput

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// TestProcessorTranscribesAndNormalizesAudio verifies both OpenAI requests and the canonical result.
func TestProcessorTranscribesAndNormalizesAudio(t *testing.T) {
	t.Parallel()

	requestCount := 0
	testServer := httptest.NewServer(http.HandlerFunc(func(responseWriter http.ResponseWriter, request *http.Request) {
		requestCount++
		switch request.URL.Path {
		case "/v1/audio/transcriptions":
			assertTranscriptionRequest(t, request)
			responseWriter.Header().Set("Content-Type", "application/json")
			_, _ = responseWriter.Write([]byte(`{"text":"Vull anar dimarts a Girona... no, dimecres a Tarragona amb dos adults i un nen."}`))
		case "/v1/responses":
			assertNormalizationRequest(t, request)
			responseWriter.Header().Set("Content-Type", "application/json")
			_, _ = responseWriter.Write([]byte(`{"id":"resp_normalized","status":"completed","output":[{"type":"message","content":[{"type":"output_text","text":"{\"canonical_message\":\"Vull anar dimecres a Tarragona amb dos adults i un nen.\",\"confirmation_summary\":\"vols anar dimecres a Tarragona amb dos adults i un nen\"}"}]}]}`))
		default:
			http.NotFound(responseWriter, request)
		}
	}))
	defer testServer.Close()

	processor := NewProcessor("test-api-key", testServer.URL+"/v1", "gpt-transcribe", "gpt-5.5")
	result, err := processor.Process(context.Background(), Audio{
		Content:  []byte("recorded voice"),
		Filename: "voice.ogg",
		MIMEType: "audio/ogg",
	})
	if err != nil {
		t.Fatalf("Process returned an error: %v", err)
	}
	if requestCount != 2 {
		t.Fatalf("request count = %d, want 2", requestCount)
	}
	if result.CanonicalMessage != "Vull anar dimecres a Tarragona amb dos adults i un nen." {
		t.Errorf("CanonicalMessage = %q", result.CanonicalMessage)
	}
	if result.ConfirmationSummary != "vols anar dimecres a Tarragona amb dos adults i un nen" {
		t.Errorf("ConfirmationSummary = %q", result.ConfirmationSummary)
	}
}

// TestProcessorReportsIncompleteNormalization verifies response diagnostics exclude transcript content.
func TestProcessorReportsIncompleteNormalization(t *testing.T) {
	t.Parallel()

	testServer := httptest.NewServer(http.HandlerFunc(func(responseWriter http.ResponseWriter, request *http.Request) {
		if request.URL.Path == "/v1/audio/transcriptions" {
			responseWriter.Header().Set("Content-Type", "application/json")
			_, _ = responseWriter.Write([]byte(`{"text":"Informació privada de l'àudio"}`))
			return
		}
		responseWriter.Header().Set("Content-Type", "application/json")
		_, _ = responseWriter.Write([]byte(`{"id":"resp_incomplete","status":"incomplete","incomplete_details":{"reason":"max_output_tokens"},"output":[]}`))
	}))
	defer testServer.Close()

	processor := NewProcessor("test-api-key", testServer.URL+"/v1", "gpt-transcribe", "gpt-5.5")
	_, err := processor.Process(context.Background(), Audio{Content: []byte("voice"), Filename: "voice.ogg"})
	if err == nil {
		t.Fatal("Process returned nil error for an incomplete normalization response")
	}
	if !strings.Contains(err.Error(), `response_id="resp_incomplete"`) || !strings.Contains(err.Error(), `incomplete_reason="max_output_tokens"`) {
		t.Errorf("normalization error = %q", err)
	}
	if strings.Contains(err.Error(), "Informació privada") {
		t.Errorf("normalization error includes transcript content: %q", err)
	}
}

// TestProcessorRejectsUnnormalizedTranscript verifies raw speech never enters conversation context.
func TestProcessorRejectsUnnormalizedTranscript(t *testing.T) {
	t.Parallel()

	testServer := httptest.NewServer(http.HandlerFunc(func(responseWriter http.ResponseWriter, request *http.Request) {
		if request.URL.Path == "/v1/audio/transcriptions" {
			responseWriter.Header().Set("Content-Type", "application/json")
			_, _ = responseWriter.Write([]byte(`{"text":"Missatge literal"}`))
			return
		}
		http.Error(responseWriter, "normalizer unavailable", http.StatusServiceUnavailable)
	}))
	defer testServer.Close()

	processor := NewProcessor("test-api-key", testServer.URL+"/v1", "gpt-transcribe", "gpt-5.5")
	if _, err := processor.Process(context.Background(), Audio{Content: []byte("voice"), Filename: "voice.ogg"}); err == nil {
		t.Fatal("Process returned nil error when normalization failed")
	}
}

// assertTranscriptionRequest verifies the multipart request retains audio metadata.
func assertTranscriptionRequest(t *testing.T, request *http.Request) {
	t.Helper()
	if request.Header.Get("Authorization") != "Bearer test-api-key" {
		t.Errorf("authorization header = %q", request.Header.Get("Authorization"))
	}
	if err := request.ParseMultipartForm(1 << 20); err != nil {
		t.Fatalf("parse transcription multipart form: %v", err)
	}
	if request.FormValue("model") != "gpt-transcribe" {
		t.Errorf("transcription model = %q", request.FormValue("model"))
	}
	uploadedFile, uploadedFileHeader, err := request.FormFile("file")
	if err != nil {
		t.Fatalf("read transcription file: %v", err)
	}
	defer uploadedFile.Close()
	if uploadedFileHeader.Filename != "voice.ogg" {
		t.Errorf("filename = %q", uploadedFileHeader.Filename)
	}
	if uploadedFileHeader.Header.Get("Content-Type") != "audio/ogg" {
		t.Errorf("content type = %q", uploadedFileHeader.Header.Get("Content-Type"))
	}
	uploadedContent, err := io.ReadAll(uploadedFile)
	if err != nil {
		t.Fatalf("read uploaded content: %v", err)
	}
	if string(uploadedContent) != "recorded voice" {
		t.Errorf("uploaded content = %q", uploadedContent)
	}
}

// assertNormalizationRequest verifies normalization uses strict structured output.
func assertNormalizationRequest(t *testing.T, request *http.Request) {
	t.Helper()
	if !strings.HasPrefix(request.Header.Get("Content-Type"), "application/json") {
		t.Errorf("normalization content type = %q", request.Header.Get("Content-Type"))
	}
	var requestPayload struct {
		Model           string `json:"model"`
		Instructions    string `json:"instructions"`
		Input           string `json:"input"`
		MaxOutputTokens int    `json:"max_output_tokens"`
		Text            struct {
			Format struct {
				Type   string `json:"type"`
				Name   string `json:"name"`
				Strict bool   `json:"strict"`
			} `json:"format"`
		} `json:"text"`
	}
	if err := json.NewDecoder(request.Body).Decode(&requestPayload); err != nil {
		t.Fatalf("decode normalization request: %v", err)
	}
	if requestPayload.Model != "gpt-5.5" {
		t.Errorf("normalization model = %q", requestPayload.Model)
	}
	if !strings.Contains(requestPayload.Instructions, "explicit self-corrections") {
		t.Errorf("normalization instructions = %q", requestPayload.Instructions)
	}
	if !strings.Contains(requestPayload.Input, "dimecres a Tarragona") {
		t.Errorf("normalization input = %q", requestPayload.Input)
	}
	if requestPayload.Text.Format.Type != "json_schema" || requestPayload.Text.Format.Name != "normalized_audio_message" || !requestPayload.Text.Format.Strict {
		t.Errorf("response format = %#v", requestPayload.Text.Format)
	}
	if requestPayload.MaxOutputTokens != normalizationMaxOutputTokens {
		t.Errorf("max output tokens = %d, want %d", requestPayload.MaxOutputTokens, normalizationMaxOutputTokens)
	}
}
