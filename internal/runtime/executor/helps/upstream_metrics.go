package helps

import (
	"fmt"
	"io"
	"net/http"
	"sync"
	"time"

	log "github.com/sirupsen/logrus"
)

var (
	globalUpstreamMetrics     = newUpstreamRequestMetrics()
	globalUpstreamMetricsOnce sync.Once
)

type upstreamRequestMetrics struct {
	mu sync.Mutex

	activeRequests uint64
	completedTotal uint64
	errorsTotal    uint64

	totalDuration time.Duration
	maxDuration   time.Duration

	windowCompleted uint64
	windowDuration  time.Duration
	windowMax       time.Duration
}

type upstreamMetricsSnapshot struct {
	ActiveRequests        uint64
	CompletedTotal        uint64
	ErrorsTotal           uint64
	AverageDuration       time.Duration
	MaxDuration           time.Duration
	WindowCompleted       uint64
	WindowAverageDuration time.Duration
	WindowMaxDuration     time.Duration
}

type upstreamMetricsRoundTripper struct {
	base    http.RoundTripper
	metrics *upstreamRequestMetrics
}

type upstreamMetricsBody struct {
	io.ReadCloser
	once   sync.Once
	finish func()
}

func newUpstreamRequestMetrics() *upstreamRequestMetrics {
	return &upstreamRequestMetrics{}
}

func wrapUpstreamMetrics(rt http.RoundTripper) http.RoundTripper {
	if !log.IsLevelEnabled(log.DebugLevel) {
		return rt
	}
	globalUpstreamMetricsOnce.Do(func() {
		go globalUpstreamMetrics.logEvery(5 * time.Second)
	})
	return globalUpstreamMetrics.wrap(rt)
}

func (m *upstreamRequestMetrics) wrap(rt http.RoundTripper) http.RoundTripper {
	if rt == nil {
		rt = http.DefaultTransport
	}
	if wrapped, ok := rt.(*upstreamMetricsRoundTripper); ok && wrapped.metrics == m {
		return rt
	}
	return &upstreamMetricsRoundTripper{base: rt, metrics: m}
}

func (m *upstreamRequestMetrics) begin() func(bool) {
	start := time.Now()

	m.mu.Lock()
	m.activeRequests++
	m.mu.Unlock()

	var once sync.Once
	return func(failed bool) {
		once.Do(func() {
			duration := time.Since(start)
			if duration <= 0 {
				duration = time.Nanosecond
			}
			m.finish(duration, failed)
		})
	}
}

func (m *upstreamRequestMetrics) finish(duration time.Duration, failed bool) {
	if duration <= 0 {
		duration = time.Nanosecond
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	if m.activeRequests > 0 {
		m.activeRequests--
	}
	m.completedTotal++
	if failed {
		m.errorsTotal++
	}
	m.totalDuration += duration
	if duration > m.maxDuration {
		m.maxDuration = duration
	}
	m.windowCompleted++
	m.windowDuration += duration
	if duration > m.windowMax {
		m.windowMax = duration
	}
}

func (m *upstreamRequestMetrics) snapshot(resetWindow bool) upstreamMetricsSnapshot {
	m.mu.Lock()
	defer m.mu.Unlock()

	var avg time.Duration
	if m.completedTotal > 0 {
		avg = m.totalDuration / time.Duration(m.completedTotal)
	}

	var windowAvg time.Duration
	if m.windowCompleted > 0 {
		windowAvg = m.windowDuration / time.Duration(m.windowCompleted)
	}

	snapshot := upstreamMetricsSnapshot{
		ActiveRequests:        m.activeRequests,
		CompletedTotal:        m.completedTotal,
		ErrorsTotal:           m.errorsTotal,
		AverageDuration:       avg,
		MaxDuration:           m.maxDuration,
		WindowCompleted:       m.windowCompleted,
		WindowAverageDuration: windowAvg,
		WindowMaxDuration:     m.windowMax,
	}

	if resetWindow {
		m.windowCompleted = 0
		m.windowDuration = 0
		m.windowMax = 0
	}

	return snapshot
}

func (m *upstreamRequestMetrics) logEvery(interval time.Duration) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for range ticker.C {
		if !log.IsLevelEnabled(log.DebugLevel) {
			continue
		}
		snapshot := m.snapshot(true)
		log.Debug(fmt.Sprintf(
			"upstream request metrics | active=%d completed=%d errors=%d avg=%s max=%s | window: completed=%d avg=%s max=%s",
			snapshot.ActiveRequests,
			snapshot.CompletedTotal,
			snapshot.ErrorsTotal,
			snapshot.AverageDuration,
			snapshot.MaxDuration,
			snapshot.WindowCompleted,
			snapshot.WindowAverageDuration,
			snapshot.WindowMaxDuration,
		))
	}
}

func (t *upstreamMetricsRoundTripper) RoundTrip(req *http.Request) (*http.Response, error) {
	finish := t.metrics.begin()

	resp, err := t.base.RoundTrip(req)
	if err != nil {
		finish(true)
		return nil, err
	}
	if resp == nil || resp.Body == nil {
		finish(false)
		return resp, nil
	}

	resp.Body = &upstreamMetricsBody{
		ReadCloser: resp.Body,
		finish: func() {
			finish(false)
		},
	}
	return resp, nil
}

func (b *upstreamMetricsBody) Close() error {
	err := b.ReadCloser.Close()
	b.once.Do(b.finish)
	return err
}
