package openai

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/router-for-me/CLIProxyAPI/v7/internal/registry"
	"github.com/router-for-me/CLIProxyAPI/v7/sdk/api/handlers"
	coreauth "github.com/router-for-me/CLIProxyAPI/v7/sdk/cliproxy/auth"
	coreexecutor "github.com/router-for-me/CLIProxyAPI/v7/sdk/cliproxy/executor"
	sdkconfig "github.com/router-for-me/CLIProxyAPI/v7/sdk/config"
	"github.com/tidwall/gjson"
)

type codexChatRoutingCaptureExecutor struct {
	sourceFormat        string
	requestSourceFormat string
	payload             []byte
}

func (e *codexChatRoutingCaptureExecutor) Identifier() string { return "codex" }

func (e *codexChatRoutingCaptureExecutor) Execute(_ context.Context, _ *coreauth.Auth, req coreexecutor.Request, opts coreexecutor.Options) (coreexecutor.Response, error) {
	e.sourceFormat = opts.SourceFormat.String()
	if opts.Metadata != nil {
		if raw, ok := opts.Metadata[coreexecutor.RequestSourceFormatMetadataKey].(string); ok {
			e.requestSourceFormat = raw
		}
	}
	e.payload = append([]byte(nil), req.Payload...)
	return coreexecutor.Response{Payload: []byte(`{"id":"chatcmpl_test","object":"chat.completion","choices":[]}`)}, nil
}

func (e *codexChatRoutingCaptureExecutor) ExecuteStream(context.Context, *coreauth.Auth, coreexecutor.Request, coreexecutor.Options) (*coreexecutor.StreamResult, error) {
	return nil, errors.New("not implemented")
}

func (e *codexChatRoutingCaptureExecutor) Refresh(_ context.Context, auth *coreauth.Auth) (*coreauth.Auth, error) {
	return auth, nil
}

func (e *codexChatRoutingCaptureExecutor) CountTokens(context.Context, *coreauth.Auth, coreexecutor.Request, coreexecutor.Options) (coreexecutor.Response, error) {
	return coreexecutor.Response{}, errors.New("not implemented")
}

func (e *codexChatRoutingCaptureExecutor) HttpRequest(context.Context, *coreauth.Auth, *http.Request) (*http.Response, error) {
	return nil, errors.New("not implemented")
}

func TestOpenAIChatCompletionsRoutesCodexThroughResponsesRequestFormat(t *testing.T) {
	gin.SetMode(gin.TestMode)
	executor := &codexChatRoutingCaptureExecutor{}
	manager := coreauth.NewManager(nil, nil, nil)
	manager.RegisterExecutor(executor)

	auth := &coreauth.Auth{ID: "codex-auth", Provider: "codex", Status: coreauth.StatusActive}
	if _, err := manager.Register(context.Background(), auth); err != nil {
		t.Fatalf("Register auth: %v", err)
	}
	registry.GetGlobalRegistry().RegisterClient(auth.ID, auth.Provider, []*registry.ModelInfo{{ID: "gpt-5.4"}})
	t.Cleanup(func() {
		registry.GetGlobalRegistry().UnregisterClient(auth.ID)
	})

	base := handlers.NewBaseAPIHandlers(&sdkconfig.SDKConfig{}, manager)
	h := NewOpenAIAPIHandler(base)
	router := gin.New()
	router.POST("/v1/chat/completions", h.ChatCompletions)

	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(`{"model":"gpt-5.4","messages":[{"role":"system","content":"rules"},{"role":"user","content":"hello"}]}`))
	req.Header.Set("Content-Type", "application/json")
	resp := httptest.NewRecorder()
	router.ServeHTTP(resp, req)

	if resp.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d: %s", resp.Code, http.StatusOK, resp.Body.String())
	}
	if executor.sourceFormat != "openai" {
		t.Fatalf("source format = %q, want openai", executor.sourceFormat)
	}
	if executor.requestSourceFormat != "openai-response" {
		t.Fatalf("request source format = %q, want openai-response", executor.requestSourceFormat)
	}
	if got := gjson.GetBytes(executor.payload, "instructions").String(); got != "rules" {
		t.Fatalf("instructions = %q, want rules", got)
	}
	if got := gjson.GetBytes(executor.payload, "input.0.role").String(); got != "user" {
		t.Fatalf("input.0.role = %q, want user", got)
	}
	if gjson.GetBytes(executor.payload, "messages").Exists() {
		t.Fatalf("payload should not retain chat messages: %s", string(executor.payload))
	}
}
