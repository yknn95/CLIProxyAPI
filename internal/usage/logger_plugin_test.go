package usage

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	coreusage "github.com/router-for-me/CLIProxyAPI/v6/sdk/cliproxy/usage"
)

func TestRequestStatisticsRecordIncludesLatency(t *testing.T) {
	stats := NewRequestStatistics()
	stats.Record(context.Background(), coreusage.Record{
		APIKey:      "test-key",
		Model:       "gpt-5.4",
		RequestedAt: time.Date(2026, 3, 20, 12, 0, 0, 0, time.UTC),
		Latency:     1500 * time.Millisecond,
		Detail: coreusage.Detail{
			InputTokens:  10,
			OutputTokens: 20,
			TotalTokens:  30,
		},
	})

	snapshot := stats.Snapshot()
	details := snapshot.APIs["test-key"].Models["gpt-5.4"].Details
	if len(details) != 1 {
		t.Fatalf("details len = %d, want 1", len(details))
	}
	if details[0].LatencyMs != 1500 {
		t.Fatalf("latency_ms = %d, want 1500", details[0].LatencyMs)
	}
}

func TestRequestStatisticsMergeSnapshotDedupIgnoresLatency(t *testing.T) {
	stats := NewRequestStatistics()
	timestamp := time.Date(2026, 3, 20, 12, 0, 0, 0, time.UTC)
	first := StatisticsSnapshot{
		APIs: map[string]APISnapshot{
			"test-key": {
				Models: map[string]ModelSnapshot{
					"gpt-5.4": {
						Details: []RequestDetail{{
							Timestamp: timestamp,
							LatencyMs: 0,
							Source:    "user@example.com",
							AuthIndex: "0",
							Tokens: TokenStats{
								InputTokens:  10,
								OutputTokens: 20,
								TotalTokens:  30,
							},
						}},
					},
				},
			},
		},
	}
	second := StatisticsSnapshot{
		APIs: map[string]APISnapshot{
			"test-key": {
				Models: map[string]ModelSnapshot{
					"gpt-5.4": {
						Details: []RequestDetail{{
							Timestamp: timestamp,
							LatencyMs: 2500,
							Source:    "user@example.com",
							AuthIndex: "0",
							Tokens: TokenStats{
								InputTokens:  10,
								OutputTokens: 20,
								TotalTokens:  30,
							},
						}},
					},
				},
			},
		},
	}

	result := stats.MergeSnapshot(first)
	if result.Added != 1 || result.Skipped != 0 {
		t.Fatalf("first merge = %+v, want added=1 skipped=0", result)
	}

	result = stats.MergeSnapshot(second)
	if result.Added != 0 || result.Skipped != 1 {
		t.Fatalf("second merge = %+v, want added=0 skipped=1", result)
	}

	snapshot := stats.Snapshot()
	details := snapshot.APIs["test-key"].Models["gpt-5.4"].Details
	if len(details) != 1 {
		t.Fatalf("details len = %d, want 1", len(details))
	}
}

func TestBuildUsageLogPayloadIncludesRequestMetadata(t *testing.T) {
	gin.SetMode(gin.TestMode)

	recorder := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(recorder)
	req := httptest.NewRequest(http.MethodPost, "/v1/responses", nil)
	req.RemoteAddr = "10.65.0.164:12345"
	req.Header.Set("X-Forwarded-For", "10.65.0.164")
	ctx.Request = req

	payload, err := buildUsageLogPayload(context.WithValue(context.Background(), "gin", ctx), coreusage.Record{
		Latency: 12 * time.Millisecond,
		Detail: coreusage.Detail{
			TotalTokens: 12348,
		},
	})
	if err != nil {
		t.Fatalf("buildUsageLogPayload error: %v", err)
	}

	var got usageLogEntry
	if err := json.Unmarshal(payload, &got); err != nil {
		t.Fatalf("json.Unmarshal error: %v", err)
	}
	if got.IP != "10.65.0.164" {
		t.Fatalf("IP = %q, want %q", got.IP, "10.65.0.164")
	}
	if got.URI != "/v1/responses" {
		t.Fatalf("URI = %q, want %q", got.URI, "/v1/responses")
	}
	if got.Timestamp == 0 {
		t.Fatalf("Timestamp = %d, want non-zero", got.Timestamp)
	}
	if got.TimestampText == "" {
		t.Fatal("TimestampText = empty, want non-empty")
	}
	if got.Tokens != 12348 {
		t.Fatalf("Tokens = %d, want %d", got.Tokens, 12348)
	}
	if got.CostTime != 12 {
		t.Fatalf("CostTime = %d, want %d", got.CostTime, 12)
	}
}

func TestLoggerPluginHandleUsageWritesDedicatedUsageLog(t *testing.T) {
	sink := &memoryUsageLogWriter{}
	prevSink := usageLogSink
	usageLogSink = sink
	defer func() { usageLogSink = prevSink }()

	plugin := &LoggerPlugin{stats: NewRequestStatistics()}
	plugin.HandleUsage(context.Background(), coreusage.Record{
		Latency: 8 * time.Millisecond,
		Detail: coreusage.Detail{
			TotalTokens: 42,
		},
	})

	if len(sink.lines) != 1 {
		t.Fatalf("logged lines = %d, want 1", len(sink.lines))
	}
	if !bytes.Contains(sink.lines[0], []byte('{')) {
		t.Fatalf("log output = %q, want JSON payload", string(sink.lines[0]))
	}
	if !bytes.Contains(sink.lines[0], []byte('"Tokens":42')) {
		t.Fatalf("log output = %q, want token count", string(sink.lines[0]))
	}
	if !bytes.Contains(sink.lines[0], []byte('"CostTime":8')) {
		t.Fatalf("log output = %q, want latency", string(sink.lines[0]))
	}
}

func TestLoggerPluginHandleUsageStillWritesLogWhenStatisticsDisabled(t *testing.T) {
	sink := &memoryUsageLogWriter{}
	prevSink := usageLogSink
	prevEnabled := StatisticsEnabled()
	usageLogSink = sink
	SetStatisticsEnabled(false)
	defer func() {
		usageLogSink = prevSink
		SetStatisticsEnabled(prevEnabled)
	}()

	plugin := &LoggerPlugin{stats: NewRequestStatistics()}
	plugin.HandleUsage(context.Background(), coreusage.Record{
		Latency: 5 * time.Millisecond,
		Detail: coreusage.Detail{
			TotalTokens: 7,
		},
	})

	if len(sink.lines) != 1 {
		t.Fatalf("logged lines = %d, want 1", len(sink.lines))
	}
}

func TestBuildUsageLogPayloadFallsBackToCurrentTimeWhenRequestedAtMissing(t *testing.T) {
	payload, err := buildUsageLogPayload(context.Background(), coreusage.Record{
		Latency: 6325 * time.Millisecond,
		Detail: coreusage.Detail{
			TotalTokens: 25953,
		},
	})
	if err != nil {
		t.Fatalf("buildUsageLogPayload error: %v", err)
	}

	var got usageLogEntry
	if err := json.Unmarshal(payload, &got); err != nil {
		t.Fatalf("json.Unmarshal error: %v", err)
	}
	if got.Timestamp == 0 {
		t.Fatalf("Timestamp = %d, want non-zero", got.Timestamp)
	}
	if got.TimestampText == "" {
		t.Fatal("TimestampText = empty, want non-empty")
	}
	if got.Tokens != 25953 {
		t.Fatalf("Tokens = %d, want %d", got.Tokens, 25953)
	}
	if got.CostTime != 6325 {
		t.Fatalf("CostTime = %d, want %d", got.CostTime, 6325)
	}
}


type memoryUsageLogWriter struct {
	lines [][]byte
}

func (w *memoryUsageLogWriter) WriteLine(payload []byte) error {
	copied := append([]byte(nil), payload...)
	w.lines = append(w.lines, copied)
	return nil
}
