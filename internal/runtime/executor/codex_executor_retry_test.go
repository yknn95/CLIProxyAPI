package executor

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/router-for-me/CLIProxyAPI/v7/internal/config"
	"github.com/router-for-me/CLIProxyAPI/v7/internal/runtime/executor/helps"
	sdktranslator "github.com/router-for-me/CLIProxyAPI/v7/sdk/translator"
)

func TestParseCodexRetryAfter(t *testing.T) {
	now := time.Unix(1_700_000_000, 0)

	t.Run("resets_in_seconds", func(t *testing.T) {
		body := []byte(`{"error":{"type":"usage_limit_reached","resets_in_seconds":123}}`)
		retryAfter := parseCodexRetryAfter(http.StatusTooManyRequests, body, now)
		if retryAfter == nil {
			t.Fatalf("expected retryAfter, got nil")
		}
		if *retryAfter != 123*time.Second {
			t.Fatalf("retryAfter = %v, want %v", *retryAfter, 123*time.Second)
		}
	})

	t.Run("prefers resets_at", func(t *testing.T) {
		resetAt := now.Add(5 * time.Minute).Unix()
		body := []byte(`{"error":{"type":"usage_limit_reached","resets_at":` + itoa(resetAt) + `,"resets_in_seconds":1}}`)
		retryAfter := parseCodexRetryAfter(http.StatusTooManyRequests, body, now)
		if retryAfter == nil {
			t.Fatalf("expected retryAfter, got nil")
		}
		if *retryAfter != 5*time.Minute {
			t.Fatalf("retryAfter = %v, want %v", *retryAfter, 5*time.Minute)
		}
	})

	t.Run("fallback when resets_at is past", func(t *testing.T) {
		resetAt := now.Add(-1 * time.Minute).Unix()
		body := []byte(`{"error":{"type":"usage_limit_reached","resets_at":` + itoa(resetAt) + `,"resets_in_seconds":77}}`)
		retryAfter := parseCodexRetryAfter(http.StatusTooManyRequests, body, now)
		if retryAfter == nil {
			t.Fatalf("expected retryAfter, got nil")
		}
		if *retryAfter != 77*time.Second {
			t.Fatalf("retryAfter = %v, want %v", *retryAfter, 77*time.Second)
		}
	})

	t.Run("non-429 status code", func(t *testing.T) {
		body := []byte(`{"error":{"type":"usage_limit_reached","resets_in_seconds":30}}`)
		if got := parseCodexRetryAfter(http.StatusBadRequest, body, now); got != nil {
			t.Fatalf("expected nil for non-429, got %v", *got)
		}
	})

	t.Run("non usage_limit_reached error type", func(t *testing.T) {
		body := []byte(`{"error":{"type":"server_error","resets_in_seconds":30}}`)
		if got := parseCodexRetryAfter(http.StatusTooManyRequests, body, now); got != nil {
			t.Fatalf("expected nil for non-usage_limit_reached, got %v", *got)
		}
	})
}

func TestNewCodexStatusErrTreatsCapacityAsRetryableRateLimit(t *testing.T) {
	body := []byte(`{"error":{"message":"Selected model is at capacity. Please try a different model."}}`)

	err := newCodexStatusErr(http.StatusBadRequest, body)

	if got := err.StatusCode(); got != http.StatusTooManyRequests {
		t.Fatalf("status code = %d, want %d", got, http.StatusTooManyRequests)
	}
	if err.RetryAfter() != nil {
		t.Fatalf("expected nil explicit retryAfter for capacity fallback, got %v", *err.RetryAfter())
	}
}

func TestNewCodexStatusErrClassifiesKnownCodexFailures(t *testing.T) {
	tests := []struct {
		name       string
		statusCode int
		body       []byte
		wantStatus int
		wantType   string
		wantCode   string
	}{
		{
			name:       "context length status",
			statusCode: http.StatusRequestEntityTooLarge,
			body:       []byte(`{"error":{"message":"context length exceeded","type":"invalid_request_error","code":"context_length_exceeded"}}`),
			wantStatus: http.StatusRequestEntityTooLarge,
			wantType:   "invalid_request_error",
			wantCode:   "context_too_large",
		},
		{
			name:       "thinking signature",
			statusCode: http.StatusBadRequest,
			body:       []byte(`{"error":{"message":"Invalid signature in thinking block","type":"invalid_request_error","code":"invalid_request_error"}}`),
			wantStatus: http.StatusBadRequest,
			wantType:   "invalid_request_error",
			wantCode:   "thinking_signature_invalid",
		},
		{
			name:       "previous response missing",
			statusCode: http.StatusBadRequest,
			body:       []byte(`{"error":{"message":"No response found for previous_response_id resp_123","type":"invalid_request_error","code":"previous_response_not_found"}}`),
			wantStatus: http.StatusBadRequest,
			wantType:   "invalid_request_error",
			wantCode:   "previous_response_not_found",
		},
		{
			name:       "auth unavailable",
			statusCode: http.StatusUnauthorized,
			body:       []byte(`{"error":{"message":"invalid or expired token","type":"authentication_error","code":"invalid_api_key"}}`),
			wantStatus: http.StatusUnauthorized,
			wantType:   "authentication_error",
			wantCode:   "auth_unavailable",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			err := newCodexStatusErr(tc.statusCode, tc.body)

			if got := err.StatusCode(); got != tc.wantStatus {
				t.Fatalf("status code = %d, want %d", got, tc.wantStatus)
			}
			assertCodexErrorCode(t, err.Error(), tc.wantType, tc.wantCode)
		})
	}
}

func TestNewCodexStatusErrPreservesUnclassifiedErrors(t *testing.T) {
	body := []byte(`{"error":{"message":"documentation mentions too many tokens, but this is a billing configuration failure","type":"server_error","code":"billing_config_error"}}`)

	err := newCodexStatusErr(http.StatusBadGateway, body)

	if got := err.StatusCode(); got != http.StatusBadGateway {
		t.Fatalf("status code = %d, want %d", got, http.StatusBadGateway)
	}
	if got := err.Error(); got != string(body) {
		t.Fatalf("error body = %s, want original %s", got, string(body))
	}
}

func TestShouldRetryCodexTransportError(t *testing.T) {
	if !shouldRetryCodexTransportError(io.EOF) {
		t.Fatal("expected io.EOF to be retryable")
	}
	if !shouldRetryCodexTransportError(io.ErrUnexpectedEOF) {
		t.Fatal("expected io.ErrUnexpectedEOF to be retryable")
	}
	if !shouldRetryCodexTransportError(errors.New(`Post "https://chatgpt.com/backend-api/codex/responses": EOF`)) {
		t.Fatal("expected wrapped EOF text to be retryable")
	}
	if shouldRetryCodexTransportError(statusErr{code: http.StatusBadGateway, msg: "upstream error"}) {
		t.Fatal("expected HTTP status error to be non-retryable here")
	}
}

func TestCodexRetryAttemptCountForProxy(t *testing.T) {
	if got := codexRetryAttemptCountForProxy(false); got != 1 {
		t.Fatalf("attempts without proxy = %d, want 1", got)
	}
	if got := codexRetryAttemptCountForProxy(true); got != 4 {
		t.Fatalf("attempts with proxy = %d, want 4", got)
	}
	if codexProxyRetryDelay != time.Second {
		t.Fatalf("retry delay = %v, want %v", codexProxyRetryDelay, time.Second)
	}
}

func TestCodexHTTPClientWithProxyAllowsHTTP2(t *testing.T) {
	executor := NewCodexExecutor(&config.Config{SDKConfig: config.SDKConfig{ProxyURL: "http://proxy.local:8080"}})
	client := executor.newCodexHTTPClient(context.Background(), nil)
	transport, ok := client.Transport.(*http.Transport)
	if !ok {
		t.Fatalf("transport = %T, want *http.Transport", client.Transport)
	}
	if !transport.ForceAttemptHTTP2 {
		t.Fatal("expected proxied Codex HTTP client to allow HTTP/2")
	}
	if len(transport.TLSNextProto) != 0 {
		t.Fatalf("TLSNextProto should not disable HTTP/2, got len=%d", len(transport.TLSNextProto))
	}
	if transport.TLSClientConfig != nil && len(transport.TLSClientConfig.NextProtos) == 1 && transport.TLSClientConfig.NextProtos[0] == "http/1.1" {
		t.Fatal("TLS ALPN should not be restricted to HTTP/1.1")
	}
}

func TestDoCodexRequestWithRetryRetriesTransientProxyFailure(t *testing.T) {
	var attempts atomic.Int32
	resp, err := doCodexRequestWithRetry(context.Background(), true, func() (*http.Request, error) {
		req := httptest.NewRequest(http.MethodPost, "https://chatgpt.com/backend-api/codex/responses", nil)
		return req, nil
	}, func(req *http.Request) (*http.Response, error) {
		if req == nil {
			t.Fatal("request must not be nil")
		}
		if attempts.Add(1) < 4 {
			return nil, io.EOF
		}
		return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader("ok"))}, nil
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp == nil || resp.StatusCode != http.StatusOK {
		t.Fatalf("unexpected response: %#v", resp)
	}
	if got := attempts.Load(); got != 4 {
		t.Fatalf("attempt count = %d, want 4", got)
	}
}

func TestFinishCodexNonStreamResponseRetriesUntilCompleted(t *testing.T) {
	executor := NewCodexExecutor(&config.Config{SDKConfig: config.SDKConfig{ProxyURL: "http://proxy.local:8080"}})
	reporter := helps.NewUsageReporter(context.Background(), executor.Identifier(), "gpt-5.5", nil)
	incomplete := "data: {\"type\":\"response.output_item.done\",\"item\":{\"type\":\"message\",\"role\":\"assistant\",\"content\":[{\"type\":\"output_text\",\"text\":\"partial\"}]},\"output_index\":0}\n"
	complete := incomplete + "data: {\"type\":\"response.completed\",\"response\":{\"id\":\"resp_1\",\"object\":\"response\",\"created_at\":1775555723,\"status\":\"completed\",\"model\":\"gpt-5.5\",\"output\":[],\"usage\":{\"input_tokens\":8,\"output_tokens\":28,\"total_tokens\":36}}}\n\n"

	resp, err, retryable := executor.finishCodexNonStreamResponse(
		context.Background(),
		reporter,
		&http.Response{StatusCode: http.StatusOK, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(incomplete))},
		"gpt-5.5",
		sdktranslator.FromString("openai"),
		sdktranslator.FromString("codex"),
		[]byte(`{"messages":[{"role":"user","content":"hi"}]}`),
		[]byte(`{"model":"gpt-5.5","stream":true}`),
		codexIdentityConfuseState{},
	)
	if err == nil || !retryable {
		t.Fatalf("expected retryable completion error, got err=%v retryable=%v", err, retryable)
	}
	if resp.Payload != nil {
		t.Fatalf("expected empty payload on incomplete response, got %s", string(resp.Payload))
	}

	resp, err, retryable = executor.finishCodexNonStreamResponse(
		context.Background(),
		reporter,
		&http.Response{StatusCode: http.StatusOK, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(complete))},
		"gpt-5.5",
		sdktranslator.FromString("openai"),
		sdktranslator.FromString("codex"),
		[]byte(`{"messages":[{"role":"user","content":"hi"}]}`),
		[]byte(`{"model":"gpt-5.5","stream":true}`),
		codexIdentityConfuseState{},
	)
	if err != nil || retryable {
		t.Fatalf("expected success, got err=%v retryable=%v", err, retryable)
	}
	if len(resp.Payload) == 0 {
		t.Fatal("expected translated payload")
	}
}

func TestDoCodexRequestWithRetryDoesNotRetryWithoutProxy(t *testing.T) {
	var attempts atomic.Int32
	_, err := doCodexRequestWithRetry(context.Background(), false, func() (*http.Request, error) {
		return httptest.NewRequest(http.MethodPost, "https://chatgpt.com/backend-api/codex/responses", nil), nil
	}, func(req *http.Request) (*http.Response, error) {
		attempts.Add(1)
		return nil, io.EOF
	})
	if !errors.Is(err, io.EOF) {
		t.Fatalf("expected io.EOF, got %v", err)
	}
	if got := attempts.Load(); got != 1 {
		t.Fatalf("attempt count = %d, want 1", got)
	}
}

func assertCodexErrorCode(t *testing.T, raw string, wantType string, wantCode string) {
	t.Helper()

	var payload struct {
		Error struct {
			Type string `json:"type"`
			Code string `json:"code"`
		} `json:"error"`
	}
	if err := json.Unmarshal([]byte(raw), &payload); err != nil {
		t.Fatalf("error body is not valid JSON: %v; body=%s", err, raw)
	}
	if payload.Error.Type != wantType {
		t.Fatalf("error.type = %q, want %q; body=%s", payload.Error.Type, wantType, raw)
	}
	if payload.Error.Code != wantCode {
		t.Fatalf("error.code = %q, want %q; body=%s", payload.Error.Code, wantCode, raw)
	}
}

func itoa(v int64) string {
	return strconv.FormatInt(v, 10)
}
