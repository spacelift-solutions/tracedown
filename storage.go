package main

import (
	"log"
	"sync"
	"time"

	"go.opentelemetry.io/collector/pdata/ptrace"
)

// traceEntry holds a trace batch with metadata
type traceEntry struct {
	traces    ptrace.Traces
	timestamp time.Time
	sizeBytes int64
}

// TraceStorage holds collected traces in memory with limits
type TraceStorage struct {
	mu              sync.RWMutex
	traces          []traceEntry
	config          *Config
	totalSizeBytes  int64
	totalSpanCount  int
	droppedTraces   int
	droppedOldest   int
}

// NewTraceStorage creates a new trace storage instance
func NewTraceStorage(config *Config) *TraceStorage {
	return &TraceStorage{
		traces: make([]traceEntry, 0),
		config: config,
	}
}

// AddTraces stores incoming traces with memory and count limits
func (s *TraceStorage) AddTraces(traces ptrace.Traces) {
	s.mu.Lock()
	defer s.mu.Unlock()

	// Clone the traces to avoid any mutation issues
	cloned := ptrace.NewTraces()
	traces.CopyTo(cloned)

	// Calculate approximate size
	spanCount := s.countSpans(cloned)
	estimatedSize := s.estimateSize(cloned, spanCount)

	entry := traceEntry{
		traces:    cloned,
		timestamp: time.Now(),
		sizeBytes: estimatedSize,
	}

	// Check memory limit before adding
	if s.config.MaxMemoryMB > 0 {
		maxBytes := int64(s.config.MaxMemoryMB) * 1024 * 1024
		if s.totalSizeBytes+estimatedSize > maxBytes {
			log.Printf("Warning: Memory limit reached (%d MB), dropping oldest traces", s.config.MaxMemoryMB)
			s.evictOldestUntilRoom(estimatedSize)
		}
	}

	// Check trace count limit
	if s.config.MaxTraces > 0 && len(s.traces) >= s.config.MaxTraces {
		log.Printf("Warning: Max trace count reached (%d), dropping oldest trace", s.config.MaxTraces)
		s.removeOldest()
	}

	s.traces = append(s.traces, entry)
	s.totalSizeBytes += estimatedSize
	s.totalSpanCount += spanCount

	log.Printf("Received trace batch: %d spans, ~%d KB (total: %d batches, %d spans, ~%.2f MB)",
		spanCount, estimatedSize/1024, len(s.traces), s.totalSpanCount, float64(s.totalSizeBytes)/(1024*1024))
}

// GetTraces returns all stored traces, applying expiration
func (s *TraceStorage) GetTraces() []ptrace.Traces {
	s.mu.RLock()
	defer s.mu.RUnlock()

	s.expireOldTracesLocked()

	result := make([]ptrace.Traces, len(s.traces))
	for i, entry := range s.traces {
		result[i] = entry.traces
	}
	return result
}

// GetStats returns storage statistics
func (s *TraceStorage) GetStats() (batches, spans, droppedTraces, droppedOldest int, memoryMB float64) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return len(s.traces), s.totalSpanCount, s.droppedTraces, s.droppedOldest, float64(s.totalSizeBytes) / (1024 * 1024)
}

// expireOldTracesLocked removes traces older than the configured expiration time
// Must be called with lock held
func (s *TraceStorage) expireOldTracesLocked() {
	if s.config.TraceExpiration <= 0 {
		return
	}

	cutoff := time.Now().Add(-s.config.TraceExpiration)
	newTraces := make([]traceEntry, 0, len(s.traces))

	for _, entry := range s.traces {
		if entry.timestamp.After(cutoff) {
			newTraces = append(newTraces, entry)
		} else {
			spanCount := s.countSpans(entry.traces)
			s.totalSizeBytes -= entry.sizeBytes
			s.totalSpanCount -= spanCount
			s.droppedOldest++
		}
	}

	if len(newTraces) < len(s.traces) {
		expired := len(s.traces) - len(newTraces)
		log.Printf("Expired %d old trace batches (older than %v)", expired, s.config.TraceExpiration)
		s.traces = newTraces
	}
}

// evictOldestUntilRoom removes oldest traces until there's room for newSize
// Must be called with lock held
func (s *TraceStorage) evictOldestUntilRoom(newSize int64) {
	maxBytes := int64(s.config.MaxMemoryMB) * 1024 * 1024

	for len(s.traces) > 0 && s.totalSizeBytes+newSize > maxBytes {
		s.removeOldest()
	}
}

// removeOldest removes the oldest trace
// Must be called with lock held
func (s *TraceStorage) removeOldest() {
	if len(s.traces) == 0 {
		return
	}

	oldest := s.traces[0]
	spanCount := s.countSpans(oldest.traces)
	s.totalSizeBytes -= oldest.sizeBytes
	s.totalSpanCount -= spanCount
	s.droppedOldest++
	s.traces = s.traces[1:]
}

// countSpans counts total spans in a trace batch
func (s *TraceStorage) countSpans(traces ptrace.Traces) int {
	count := 0
	for i := 0; i < traces.ResourceSpans().Len(); i++ {
		rs := traces.ResourceSpans().At(i)
		for j := 0; j < rs.ScopeSpans().Len(); j++ {
			ss := rs.ScopeSpans().At(j)
			count += ss.Spans().Len()
		}
	}
	return count
}

// estimateSize provides a rough estimate of memory usage for a trace batch
func (s *TraceStorage) estimateSize(traces ptrace.Traces, spanCount int) int64 {
	// Rough estimates based on typical span data
	// Average span: ~1KB (name, attributes, events, etc.)
	// Resource attributes: ~500 bytes
	// Base overhead: ~100 bytes

	baseSize := int64(100)
	resourceSize := int64(traces.ResourceSpans().Len() * 500)
	spanSize := int64(spanCount * 1024)

	return baseSize + resourceSize + spanSize
}
