package openai

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/tidwall/gjson"
)

func TestBuildImagesAPIResponseSavesGeneratedImageAndMetadata(t *testing.T) {
	tempDir := t.TempDir()
	oldDir := imagesSaveDir
	imagesSaveDir = tempDir
	t.Cleanup(func() {
		imagesSaveDir = oldDir
	})

	raw := []byte("test-image-bytes")
	out, err := buildImagesAPIResponse([]imageCallResult{
		{
			Result:        base64.StdEncoding.EncodeToString(raw),
			RevisedPrompt: "draw a cat",
			OutputFormat:  "png",
			CallID:        "call_123",
		},
	}, 123, []byte(`{"total_tokens":42}`), imageCallResult{}, "b64_json")
	if err != nil {
		t.Fatalf("buildImagesAPIResponse returned error: %v", err)
	}
	if len(out) == 0 {
		t.Fatal("expected response payload")
	}

	entries, err := os.ReadDir(tempDir)
	if err != nil {
		t.Fatalf("read temp dir failed: %v", err)
	}
	if len(entries) != 2 {
		t.Fatalf("expected 2 saved files, got %d", len(entries))
	}

	var imagePath string
	var infoPath string
	for _, entry := range entries {
		name := entry.Name()
		switch {
		case strings.HasSuffix(name, ".png"):
			imagePath = filepath.Join(tempDir, name)
		case strings.HasSuffix(name, ".info.json"):
			infoPath = filepath.Join(tempDir, name)
		}
	}
	if imagePath == "" {
		t.Fatal("expected saved image file")
	}
	if infoPath == "" {
		t.Fatal("expected saved metadata file")
	}

	savedImage, err := os.ReadFile(imagePath)
	if err != nil {
		t.Fatalf("read saved image failed: %v", err)
	}
	if string(savedImage) != string(raw) {
		t.Fatalf("saved image mismatch: got %q want %q", string(savedImage), string(raw))
	}

	infoBytes, err := os.ReadFile(infoPath)
	if err != nil {
		t.Fatalf("read metadata failed: %v", err)
	}
	var info map[string]interface{}
	if err := json.Unmarshal(infoBytes, &info); err != nil {
		t.Fatalf("unmarshal metadata failed: %v", err)
	}
	if info["revised_prompt"] != "draw a cat" {
		t.Fatalf("unexpected revised_prompt: %v", info["revised_prompt"])
	}
	if info["call_id"] != "call_123" {
		t.Fatalf("unexpected call_id: %v", info["call_id"])
	}
	if info["created"] != float64(123) {
		t.Fatalf("unexpected created: %v", info["created"])
	}
	usage, ok := info["usage"].(map[string]interface{})
	if !ok {
		t.Fatalf("expected usage object, got %T", info["usage"])
	}
	if usage["total_tokens"] != float64(42) {
		t.Fatalf("unexpected usage total_tokens: %v", usage["total_tokens"])
	}
}

func performImagesEndpointRequest(t *testing.T, endpointPath string, contentType string, body io.Reader, handler gin.HandlerFunc) *httptest.ResponseRecorder {
	t.Helper()

	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.POST(endpointPath, handler)

	req := httptest.NewRequest(http.MethodPost, endpointPath, body)
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}
	resp := httptest.NewRecorder()
	router.ServeHTTP(resp, req)
	return resp
}

func assertUnsupportedImagesModelResponse(t *testing.T, resp *httptest.ResponseRecorder, model string) {
	t.Helper()

	if resp.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d: %s", resp.Code, http.StatusBadRequest, resp.Body.String())
	}

	message := gjson.GetBytes(resp.Body.Bytes(), "error.message").String()
	expectedMessage := "Model " + model + " is not supported on " + imagesGenerationsPath + " or " + imagesEditsPath + ". Use " + defaultImagesToolModel + "."
	if message != expectedMessage {
		t.Fatalf("error message = %q, want %q", message, expectedMessage)
	}
	if errorType := gjson.GetBytes(resp.Body.Bytes(), "error.type").String(); errorType != "invalid_request_error" {
		t.Fatalf("error type = %q, want invalid_request_error", errorType)
	}
}

func TestImagesModelValidationAllowsGPTImage2WithOptionalPrefix(t *testing.T) {
	for _, model := range []string{"gpt-image-2", "codex/gpt-image-2"} {
		if !isSupportedImagesModel(model) {
			t.Fatalf("expected %s to be supported", model)
		}
	}
	if isSupportedImagesModel("gpt-5.4-mini") {
		t.Fatal("expected gpt-5.4-mini to be rejected")
	}
}

func TestImagesGenerationsRejectsUnsupportedModel(t *testing.T) {
	handler := &OpenAIAPIHandler{}
	body := strings.NewReader(`{"model":"gpt-5.4-mini","prompt":"draw a square"}`)

	resp := performImagesEndpointRequest(t, imagesGenerationsPath, "application/json", body, handler.ImagesGenerations)

	assertUnsupportedImagesModelResponse(t, resp, "gpt-5.4-mini")
}

func TestImagesEditsJSONRejectsUnsupportedModel(t *testing.T) {
	handler := &OpenAIAPIHandler{}
	body := strings.NewReader(`{"model":"gpt-5.4-mini","prompt":"edit this","images":[{"image_url":"data:image/png;base64,AA=="}]}`)

	resp := performImagesEndpointRequest(t, imagesEditsPath, "application/json", body, handler.ImagesEdits)

	assertUnsupportedImagesModelResponse(t, resp, "gpt-5.4-mini")
}

func TestImagesEditsMultipartRejectsUnsupportedModel(t *testing.T) {
	handler := &OpenAIAPIHandler{}
	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	if err := writer.WriteField("model", "gpt-5.4-mini"); err != nil {
		t.Fatalf("write model field: %v", err)
	}
	if err := writer.WriteField("prompt", "edit this"); err != nil {
		t.Fatalf("write prompt field: %v", err)
	}
	if errClose := writer.Close(); errClose != nil {
		t.Fatalf("close multipart writer: %v", errClose)
	}

	resp := performImagesEndpointRequest(t, imagesEditsPath, writer.FormDataContentType(), &body, handler.ImagesEdits)

	assertUnsupportedImagesModelResponse(t, resp, "gpt-5.4-mini")
}
