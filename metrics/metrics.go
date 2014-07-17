// Package metrics provides an HTTP handler which registers expvar counters for
// the number of requests received and responses sent as well as quantiles of
// the latency of responses.
package metrics

import (
	"expvar"
	"net/http"
	"sync"
	"sync/atomic"
	"time"

	"github.com/codahale/hdrhistogram/hdr"
)

// Wrap returns a handler which records the number of requests received and
// responses sent to the given handler, as well as latency quantiles for
// responses over a five-minute window.
//
// These counters are published as the "http" object in expvars.
//
// By tracking incoming requests and outgoing responses, one can monitor not
// only the requests per second, but also the number of requests being processed
// at any given point in time.
func Wrap(h http.Handler) http.Handler {
	// a five-minute window tracking 1ms-3min
	stats := handlerStats{histogram: newHistogram(5, 1, 1000*60*3, 3)}

	expvar.Publish("http", expvar.Func(func() interface{} { return stats.snapshot() }))

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		stats.requests(1)
		defer stats.responses(1)
		defer stats.record(time.Now())
		h.ServeHTTP(w, r)
	})
}

// handlerStats is a container type for several HTTP handler statistics
// NOTE(tsenart): Memory must be 64bit aligned on ARM and x86-32
// or a runtime panic will occur in some platforms when using atomic operations.
// See: http://golang.org/src/pkg/sync/atomic/doc.go#L52
type handlerStats struct {
	reqs, resps uint64
	*histogram
}

// snapshot returns a snapshot of this handlerStats
func (s *handlerStats) snapshot() map[string]interface{} {
	s.Lock()
	defer s.Unlock()

	m := s.Merge()

	return map[string]interface{}{
		"Requests":  s.reqs,
		"Responses": s.responses,
		"P50":       m.ValueAtQuantile(50),
		"P75":       m.ValueAtQuantile(75),
		"P90":       m.ValueAtQuantile(90),
		"P95":       m.ValueAtQuantile(95),
		"P99":       m.ValueAtQuantile(99),
		"P999":      m.ValueAtQuantile(999),
	}
}

// reqs atomically adds n to the internal requests counter
func (s *handlerStats) requests(n uint64) uint64 {
	return atomic.AddUint64(&s.reqs, n)
}

// resps atomically adds n to the internal responses counter
func (s *handlerStats) responses(n uint64) uint64 {
	return atomic.AddUint64(&s.resps, n)
}

// histogram is an utility type wrapping an hdr.WindowedHistogram
type histogram struct {
	sync.Mutex
	*hdr.WindowedHistogram
}

func newHistogram(n int, minValue, maxValue int64, sigfigs int) *histogram {
	h := &histogram{
		WindowedHistogram: hdr.NewWindowedHistogram(n, minValue, maxValue, sigfigs),
	}

	go h.rotate(1 * time.Minute)

	return h
}

// record safely updates the histogram's state with a new entry
func (h *histogram) record(start time.Time) {
	h.Lock()
	elapsedMS := time.Now().Sub(start).Seconds() * 1000.0
	h.Current.RecordValue(int64(elapsedMS))
	h.Unlock()
}

// rotate safely rotates the histogram every period amount of time
func (h *histogram) rotate(period time.Duration) {
	for _ = range time.Tick(period) {
		h.Lock()
		h.Rotate()
		h.Unlock()
	}
}
