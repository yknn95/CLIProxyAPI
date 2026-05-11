package openai

import (
	"bytes"
	"context"
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
	internalconfig "github.com/router-for-me/CLIProxyAPI/v7/internal/config"
	"github.com/router-for-me/CLIProxyAPI/v7/sdk/api/handlers"
	sdkconfig "github.com/router-for-me/CLIProxyAPI/v7/sdk/config"
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

func TestImagesGenerations_DisableImageGeneration_Returns404(t *testing.T) {
	base := handlers.NewBaseAPIHandlers(&sdkconfig.SDKConfig{DisableImageGeneration: internalconfig.DisableImageGenerationAll}, nil)
	handler := NewOpenAIAPIHandler(base)
	body := strings.NewReader(`{"prompt":"draw a square"}`)

	resp := performImagesEndpointRequest(t, imagesGenerationsPath, "application/json", body, handler.ImagesGenerations)

	if resp.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want %d: %s", resp.Code, http.StatusNotFound, resp.Body.String())
	}
}

func TestImagesEdits_DisableImageGeneration_Returns404(t *testing.T) {
	base := handlers.NewBaseAPIHandlers(&sdkconfig.SDKConfig{DisableImageGeneration: internalconfig.DisableImageGenerationAll}, nil)
	handler := NewOpenAIAPIHandler(base)
	body := strings.NewReader(`{"prompt":"edit this","images":[{"image_url":"data:image/png;base64,AA=="}]}`)

	resp := performImagesEndpointRequest(t, imagesEditsPath, "application/json", body, handler.ImagesEdits)

	if resp.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want %d: %s", resp.Code, http.StatusNotFound, resp.Body.String())
	}
}

func TestImagesGenerations_DisableImageGenerationChat_DoesNotReturn404(t *testing.T) {
	base := handlers.NewBaseAPIHandlers(&sdkconfig.SDKConfig{DisableImageGeneration: internalconfig.DisableImageGenerationChat}, nil)
	handler := NewOpenAIAPIHandler(base)
	body := strings.NewReader(`{"model":"gpt-5.4-mini","prompt":"draw a square"}`)

	resp := performImagesEndpointRequest(t, imagesGenerationsPath, "application/json", body, handler.ImagesGenerations)

	if resp.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d: %s", resp.Code, http.StatusBadRequest, resp.Body.String())
	}
}

func TestImagesEdits_DisableImageGenerationChat_DoesNotReturn404(t *testing.T) {
	base := handlers.NewBaseAPIHandlers(&sdkconfig.SDKConfig{DisableImageGeneration: internalconfig.DisableImageGenerationChat}, nil)
	handler := NewOpenAIAPIHandler(base)
	body := strings.NewReader(`{"model":"gpt-5.4-mini","prompt":"edit this","images":[{"image_url":"data:image/png;base64,AA=="}]}`)

	resp := performImagesEndpointRequest(t, imagesEditsPath, "application/json", body, handler.ImagesEdits)

	if resp.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d: %s", resp.Code, http.StatusBadRequest, resp.Body.String())
	}
}

func TestCollectImagesFromResponsesStream_ReturnsResponseFailedError(t *testing.T) {
	ctx := context.Background()
	dataCh := make(chan []byte, 1)
	errCh := make(chan *interfaces.ErrorMessage)

	dataCh <- []byte("data: " + `{"type":"response.failed","response":{"status":"failed","error":{"code":"rate_limit_exceeded","message":"please retry later"}}}` + "\n\n")
	close(dataCh)
	close(errCh)

	out, errMsg := collectImagesFromResponsesStream(ctx, dataCh, errCh, "b64_json")
	if out != nil {
		t.Fatalf("expected no output, got %s", string(out))
	}
	if errMsg == nil {
		t.Fatal("expected error message")
	}
	if errMsg.StatusCode != http.StatusTooManyRequests {
		t.Fatalf("status = %d, want %d", errMsg.StatusCode, http.StatusTooManyRequests)
	}
	if got := gjson.Get(errMsg.Error.Error(), "error.message").String(); got != "please retry later" {
		t.Fatalf("message = %q, want %q", got, "please retry later")
	}
	if got := gjson.Get(errMsg.Error.Error(), "error.code").String(); got != "rate_limit_exceeded" {
		t.Fatalf("code = %q, want %q", got, "rate_limit_exceeded")
	}
	if got := gjson.Get(errMsg.Error.Error(), "error.type").String(); got != "rate_limit_error" {
		t.Fatalf("type = %q, want %q", got, "rate_limit_error")
	}
}

func TestCollectImagesFromResponsesStream_ReturnsErrorEvent(t *testing.T) {
	ctx := context.Background()
	dataCh := make(chan []byte, 1)
	errCh := make(chan *interfaces.ErrorMessage)

	dataCh <- []byte("data: " + `{"type":"error","status":500,"error":{"type":"server_error","code":"internal_server_error","message":"upstream exploded"}}` + "\n\n")
	close(dataCh)
	close(errCh)

	out, errMsg := collectImagesFromResponsesStream(ctx, dataCh, errCh, "b64_json")
	if out != nil {
		t.Fatalf("expected no output, got %s", string(out))
	}
	if errMsg == nil {
		t.Fatal("expected error message")
	}
	if errMsg.StatusCode != http.StatusInternalServerError {
		t.Fatalf("status = %d, want %d", errMsg.StatusCode, http.StatusInternalServerError)
	}
	if got := gjson.Get(errMsg.Error.Error(), "error.message").String(); got != "upstream exploded" {
		t.Fatalf("message = %q, want %q", got, "upstream exploded")
	}
	if got := gjson.Get(errMsg.Error.Error(), "error.code").String(); got != "internal_server_error" {
		t.Fatalf("code = %q, want %q", got, "internal_server_error")
	}
	if got := gjson.Get(errMsg.Error.Error(), "error.type").String(); got != "server_error" {
		t.Fatalf("type = %q, want %q", got, "server_error")
	}
}
