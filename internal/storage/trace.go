package storage

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"
)

type traceContextKey struct{}

// Trace collects request-local storage timings for the Server-Timing header.
// It intentionally contains no object paths or user data.
type Trace struct {
	mu      sync.Mutex
	timings map[string]time.Duration
	desc    map[string]string
}

func NewTrace() *Trace {
	return &Trace{timings: make(map[string]time.Duration), desc: make(map[string]string)}
}

func WithTrace(ctx context.Context, trace *Trace) context.Context {
	return context.WithValue(ctx, traceContextKey{}, trace)
}

func Time(ctx context.Context, name string) func() {
	started := time.Now()
	return func() {
		if trace, ok := ctx.Value(traceContextKey{}).(*Trace); ok && trace != nil {
			trace.add(name, time.Since(started))
		}
	}
}

func Describe(ctx context.Context, name, description string) {
	if trace, ok := ctx.Value(traceContextKey{}).(*Trace); ok && trace != nil {
		trace.mu.Lock()
		trace.desc[name] = description
		trace.mu.Unlock()
	}
}

func (t *Trace) add(name string, elapsed time.Duration) {
	t.mu.Lock()
	t.timings[name] += elapsed
	t.mu.Unlock()
}

func (t *Trace) ServerTiming() string {
	if t == nil {
		return ""
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	names := make([]string, 0, len(t.timings)+len(t.desc))
	seen := make(map[string]bool)
	for name := range t.timings {
		names = append(names, name)
		seen[name] = true
	}
	for name := range t.desc {
		if !seen[name] {
			names = append(names, name)
		}
	}
	sort.Strings(names)
	parts := make([]string, 0, len(names))
	for _, name := range names {
		part := name
		if description := strings.ReplaceAll(t.desc[name], `"`, `'`); description != "" {
			part += `;desc="` + description + `"`
		}
		if elapsed, ok := t.timings[name]; ok {
			part += fmt.Sprintf(";dur=%.1f", float64(elapsed.Microseconds())/1000)
		}
		parts = append(parts, part)
	}
	return strings.Join(parts, ", ")
}
