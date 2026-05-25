package helps

import (
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"
)

type testRoundTripper func(*http.Request) (*http.Response, error)

func (f testRoundTripper) RoundTrip(req *http.Request) (*http.Response, error) {
	return f(req)
}

func TestUpstreamMetricsTracksActiveUntilResponseBodyClose(t *testing.T) {
	t.Parallel()

	metrics := newUpstreamRequestMetrics()
	wrapped := metrics.wrap(testRoundTripper(func(*http.Request) (*http.Response, error) {
		return &http.Response{
			StatusCode: http.StatusOK,
			Body:       io.NopCloser(strings.NewReader("ok")),
		}, nil
	}))

	req, errRequest := http.NewRequest(http.MethodGet, "https://example.com", nil)
	if errRequest != nil {
		t.Fatalf("http.NewRequest returned error: %v", errRequest)
	}
	resp, errRoundTrip := wrapped.RoundTrip(req)
	if errRoundTrip != nil {
		t.Fatalf("RoundTrip returned error: %v", errRoundTrip)
	}

	snapshot := metrics.snapshot(false)
	if snapshot.ActiveRequests != 1 {
		t.Fatalf("active requests before body close = %d, want 1", snapshot.ActiveRequests)
	}
	if snapshot.CompletedTotal != 0 {
		t.Fatalf("completed before body close = %d, want 0", snapshot.CompletedTotal)
	}

	if errClose := resp.Body.Close(); errClose != nil {
		t.Fatalf("response body close returned error: %v", errClose)
	}

	snapshot = metrics.snapshot(false)
	if snapshot.ActiveRequests != 0 {
		t.Fatalf("active requests after body close = %d, want 0", snapshot.ActiveRequests)
	}
	if snapshot.CompletedTotal != 1 {
		t.Fatalf("completed after body close = %d, want 1", snapshot.CompletedTotal)
	}
	if snapshot.AverageDuration <= 0 {
		t.Fatalf("average duration = %s, want positive", snapshot.AverageDuration)
	}
	if snapshot.MaxDuration <= 0 {
		t.Fatalf("max duration = %s, want positive", snapshot.MaxDuration)
	}
}

func TestUpstreamMetricsRecordsRoundTripError(t *testing.T) {
	t.Parallel()

	metrics := newUpstreamRequestMetrics()
	wrapped := metrics.wrap(testRoundTripper(func(*http.Request) (*http.Response, error) {
		return nil, errors.New("upstream failed")
	}))

	req, errRequest := http.NewRequest(http.MethodGet, "https://example.com", nil)
	if errRequest != nil {
		t.Fatalf("http.NewRequest returned error: %v", errRequest)
	}
	_, errRoundTrip := wrapped.RoundTrip(req)
	if errRoundTrip == nil {
		t.Fatal("RoundTrip error = nil, want error")
	}

	snapshot := metrics.snapshot(false)
	if snapshot.ActiveRequests != 0 {
		t.Fatalf("active requests after error = %d, want 0", snapshot.ActiveRequests)
	}
	if snapshot.CompletedTotal != 1 {
		t.Fatalf("completed after error = %d, want 1", snapshot.CompletedTotal)
	}
	if snapshot.ErrorsTotal != 1 {
		t.Fatalf("errors after error = %d, want 1", snapshot.ErrorsTotal)
	}
	if snapshot.AverageDuration <= 0 {
		t.Fatalf("average duration = %s, want positive", snapshot.AverageDuration)
	}
}

func TestUpstreamMetricsSnapshotResetWindow(t *testing.T) {
	t.Parallel()

	metrics := newUpstreamRequestMetrics()
	metrics.finish(time.Millisecond, false)
	metrics.finish(3*time.Millisecond, false)

	snapshot := metrics.snapshot(true)
	if snapshot.WindowCompleted != 2 {
		t.Fatalf("window completed = %d, want 2", snapshot.WindowCompleted)
	}
	if snapshot.WindowAverageDuration != 2*time.Millisecond {
		t.Fatalf("window average = %s, want 2ms", snapshot.WindowAverageDuration)
	}
	if snapshot.WindowMaxDuration != 3*time.Millisecond {
		t.Fatalf("window max = %s, want 3ms", snapshot.WindowMaxDuration)
	}

	snapshot = metrics.snapshot(false)
	if snapshot.WindowCompleted != 0 {
		t.Fatalf("window completed after reset = %d, want 0", snapshot.WindowCompleted)
	}
}
