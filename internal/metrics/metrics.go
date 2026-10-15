// Package metrics collects Workly's metrics and serves them in the
// Prometheus text exposition format.
//
// It implements the small subset of the format Workly needs (counters,
// gauges and histograms with labels) on the standard library, rather than
// pulling in a Prometheus client.
package metrics

import (
	"bufio"
	"context"
	"fmt"
	"math"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"sync"
)

// Registry holds metric families and writes them in registration order.
type Registry struct {
	mu       sync.Mutex
	families []family
	// collect hooks run before each scrape, e.g. to refresh gauges from the
	// database. scrapeMu serializes scrapes so hooks don't interleave.
	collect  []func(context.Context)
	scrapeMu sync.Mutex
}

type family interface {
	write(w *bufio.Writer)
}

func NewRegistry() *Registry { return &Registry{} }

func (r *Registry) register(f family) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.families = append(r.families, f)
}

// OnCollect registers a hook that runs before every scrape.
func (r *Registry) OnCollect(fn func(ctx context.Context)) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.collect = append(r.collect, fn)
}

// Handler serves the metrics.
func (r *Registry) Handler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		w.Header().Set("Content-Type", "text/plain; version=0.0.4; charset=utf-8")
		bw := bufio.NewWriter(w)
		r.Write(req.Context(), bw)
		_ = bw.Flush()
	})
}

// Write runs the collect hooks and writes every family.
func (r *Registry) Write(ctx context.Context, w *bufio.Writer) {
	r.scrapeMu.Lock()
	defer r.scrapeMu.Unlock()
	r.mu.Lock()
	hooks := slices.Clone(r.collect)
	families := slices.Clone(r.families)
	r.mu.Unlock()
	for _, fn := range hooks {
		fn(ctx)
	}
	for _, f := range families {
		f.write(w)
	}
}

// vec is the shared part of a labelled metric family.
type vec struct {
	name, help, typ string
	labels          []string
}

func (v vec) header(w *bufio.Writer) {
	fmt.Fprintf(w, "# HELP %s %s\n# TYPE %s %s\n", v.name, escapeHelp(v.help), v.name, v.typ)
}

// key joins label values into a map key; \xff can't occur in valid UTF-8.
func (v vec) key(values []string) string {
	if len(values) != len(v.labels) {
		panic(fmt.Sprintf("metrics: %s takes %d label values, got %d", v.name, len(v.labels), len(values)))
	}
	return strings.Join(values, "\xff")
}

// labelPairs renders {a="x",b="y"} plus any extra pair (the histogram's le).
func (v vec) labelPairs(key string, extra ...string) string {
	var pairs []string
	if len(v.labels) > 0 {
		for i, value := range strings.Split(key, "\xff") {
			pairs = append(pairs, v.labels[i]+`="`+escapeLabel(value)+`"`)
		}
	}
	for i := 0; i+1 < len(extra); i += 2 {
		pairs = append(pairs, extra[i]+`="`+extra[i+1]+`"`)
	}
	if len(pairs) == 0 {
		return ""
	}
	return "{" + strings.Join(pairs, ",") + "}"
}

// Counter is a monotonically increasing value per label set.
type Counter struct {
	vec
	mu     sync.Mutex
	values map[string]float64
}

func (r *Registry) Counter(name, help string, labels ...string) *Counter {
	c := &Counter{vec: vec{name, help, "counter", labels}, values: map[string]float64{}}
	r.register(c)
	return c
}

func (c *Counter) Inc(labelValues ...string) { c.Add(1, labelValues...) }

func (c *Counter) Add(v float64, labelValues ...string) {
	k := c.key(labelValues)
	c.mu.Lock()
	c.values[k] += v
	c.mu.Unlock()
}

func (c *Counter) write(w *bufio.Writer) {
	c.header(w)
	c.mu.Lock()
	defer c.mu.Unlock()
	for _, k := range sortedKeys(c.values) {
		fmt.Fprintf(w, "%s%s %s\n", c.name, c.labelPairs(k), formatFloat(c.values[k]))
	}
}

// Gauge is a value per label set that can go up and down.
type Gauge struct {
	vec
	mu     sync.Mutex
	values map[string]float64
}

func (r *Registry) Gauge(name, help string, labels ...string) *Gauge {
	g := &Gauge{vec: vec{name, help, "gauge", labels}, values: map[string]float64{}}
	r.register(g)
	return g
}

func (g *Gauge) Set(v float64, labelValues ...string) {
	k := g.key(labelValues)
	g.mu.Lock()
	g.values[k] = v
	g.mu.Unlock()
}

// Reset removes every label set, e.g. before refreshing per-queue gauges so
// deleted queues disappear.
func (g *Gauge) Reset() {
	g.mu.Lock()
	clear(g.values)
	g.mu.Unlock()
}

func (g *Gauge) write(w *bufio.Writer) {
	g.header(w)
	g.mu.Lock()
	defer g.mu.Unlock()
	for _, k := range sortedKeys(g.values) {
		fmt.Fprintf(w, "%s%s %s\n", g.name, g.labelPairs(k), formatFloat(g.values[k]))
	}
}

// Histogram counts observations into cumulative buckets per label set.
type Histogram struct {
	vec
	buckets []float64
	mu      sync.Mutex
	values  map[string]*histogramValue
}

type histogramValue struct {
	counts []uint64 // per bucket, not cumulative
	count  uint64
	sum    float64
}

func (r *Registry) Histogram(name, help string, buckets []float64, labels ...string) *Histogram {
	h := &Histogram{vec: vec{name, help, "histogram", labels}, buckets: buckets, values: map[string]*histogramValue{}}
	r.register(h)
	return h
}

func (h *Histogram) Observe(v float64, labelValues ...string) {
	k := h.key(labelValues)
	h.mu.Lock()
	defer h.mu.Unlock()
	hv := h.values[k]
	if hv == nil {
		hv = &histogramValue{counts: make([]uint64, len(h.buckets))}
		h.values[k] = hv
	}
	if i, _ := slices.BinarySearch(h.buckets, v); i < len(h.buckets) {
		hv.counts[i]++
	}
	hv.count++
	hv.sum += v
}

func (h *Histogram) write(w *bufio.Writer) {
	h.header(w)
	h.mu.Lock()
	defer h.mu.Unlock()
	for _, k := range sortedKeys(h.values) {
		hv := h.values[k]
		var cumulative uint64
		for i, le := range h.buckets {
			cumulative += hv.counts[i]
			fmt.Fprintf(w, "%s_bucket%s %d\n", h.name, h.labelPairs(k, "le", formatFloat(le)), cumulative)
		}
		fmt.Fprintf(w, "%s_bucket%s %d\n", h.name, h.labelPairs(k, "le", "+Inf"), hv.count)
		fmt.Fprintf(w, "%s_sum%s %s\n", h.name, h.labelPairs(k), formatFloat(hv.sum))
		fmt.Fprintf(w, "%s_count%s %d\n", h.name, h.labelPairs(k), hv.count)
	}
}

func sortedKeys[V any](m map[string]V) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	slices.Sort(keys)
	return keys
}

func formatFloat(v float64) string {
	switch {
	case math.IsInf(v, 1):
		return "+Inf"
	case math.IsInf(v, -1):
		return "-Inf"
	}
	return strconv.FormatFloat(v, 'g', -1, 64)
}

var (
	labelEscaper = strings.NewReplacer(`\`, `\\`, "\n", `\n`, `"`, `\"`)
	helpEscaper  = strings.NewReplacer(`\`, `\\`, "\n", `\n`)
)

func escapeLabel(s string) string { return labelEscaper.Replace(s) }
func escapeHelp(s string) string  { return helpEscaper.Replace(s) }
