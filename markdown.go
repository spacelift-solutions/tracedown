package main

import (
	"fmt"
	"os"
	"sort"
	"strings"
	"time"

	"go.opentelemetry.io/collector/pdata/pcommon"
	"go.opentelemetry.io/collector/pdata/ptrace"
)

// WriteMarkdown generates a markdown file from stored traces
func (s *TraceStorage) WriteMarkdown(config *Config) error {
	s.mu.RLock()
	defer s.mu.RUnlock()

	f, err := os.Create(config.OutputFile)
	if err != nil {
		return fmt.Errorf("failed to create file: %w", err)
	}
	defer f.Close()

	// Write header
	fmt.Fprintf(f, "# OpenTelemetry Traces Report\n\n")
	fmt.Fprintf(f, "Generated: %s\n\n", time.Now().Format(time.RFC3339))
	fmt.Fprintf(f, "Total trace batches received: %d\n\n", len(s.traces))

	// Write statistics
	if s.droppedOldest > 0 || s.droppedTraces > 0 {
		fmt.Fprintf(f, "**Note**: ")
		if s.droppedOldest > 0 {
			fmt.Fprintf(f, "%d trace batches were dropped due to limits or expiration. ", s.droppedOldest)
		}
		if s.droppedTraces > 0 {
			fmt.Fprintf(f, "%d trace batches were dropped due to errors. ", s.droppedTraces)
		}
		fmt.Fprintf(f, "\n\n")
	}

	if len(s.traces) == 0 {
		fmt.Fprintf(f, "No traces were collected.\n")
		return nil
	}

	// Collect all spans across all traces for grouping by trace ID
	traceMap := make(map[string]*traceInfo)

	for _, entry := range s.traces {
		traces := entry.traces
		for i := 0; i < traces.ResourceSpans().Len(); i++ {
			rs := traces.ResourceSpans().At(i)
			resource := rs.Resource()

			for j := 0; j < rs.ScopeSpans().Len(); j++ {
				ss := rs.ScopeSpans().At(j)
				scope := ss.Scope()

				for k := 0; k < ss.Spans().Len(); k++ {
					span := ss.Spans().At(k)
					traceID := span.TraceID().String()

					if _, exists := traceMap[traceID]; !exists {
						traceMap[traceID] = &traceInfo{
							traceID: traceID,
							spans:   []spanInfo{},
						}
					}

					traceMap[traceID].spans = append(traceMap[traceID].spans, spanInfo{
						span:     span,
						resource: resource,
						scope:    scope,
					})
				}
			}
		}
	}

	// Sort traces by first span start time
	traces := make([]*traceInfo, 0, len(traceMap))
	for _, ti := range traceMap {
		traces = append(traces, ti)
	}
	sort.Slice(traces, func(i, j int) bool {
		return traces[i].getEarliestTime() < traces[j].getEarliestTime()
	})

	// Write each trace
	fmt.Fprintf(f, "---\n\n")
	for idx, ti := range traces {
		if config.SummaryMode {
			writeTraceSummary(f, idx+1, ti, config)
		} else {
			writeTrace(f, idx+1, ti)
		}
	}

	return nil
}

type traceInfo struct {
	traceID string
	spans   []spanInfo
}

type spanInfo struct {
	span     ptrace.Span
	resource pcommon.Resource
	scope    pcommon.InstrumentationScope
}

func (ti *traceInfo) getEarliestTime() uint64 {
	if len(ti.spans) == 0 {
		return 0
	}
	earliest := ti.spans[0].span.StartTimestamp()
	for _, si := range ti.spans[1:] {
		if si.span.StartTimestamp() < earliest {
			earliest = si.span.StartTimestamp()
		}
	}
	return uint64(earliest)
}

func writeTrace(f *os.File, index int, ti *traceInfo) {
	fmt.Fprintf(f, "## Trace %d: `%s`\n\n", index, ti.traceID)

	// Sort spans by start time
	sort.Slice(ti.spans, func(i, j int) bool {
		return ti.spans[i].span.StartTimestamp() < ti.spans[j].span.StartTimestamp()
	})

	// Calculate trace duration
	if len(ti.spans) > 0 {
		earliest := ti.spans[0].span.StartTimestamp()
		latest := ti.spans[0].span.EndTimestamp()
		for _, si := range ti.spans {
			if si.span.StartTimestamp() < earliest {
				earliest = si.span.StartTimestamp()
			}
			if si.span.EndTimestamp() > latest {
				latest = si.span.EndTimestamp()
			}
		}
		duration := time.Duration(latest - earliest)
		fmt.Fprintf(f, "**Duration**: %v\n\n", duration)
	}

	fmt.Fprintf(f, "**Spans**: %d\n\n", len(ti.spans))

	// Write spans
	for i, si := range ti.spans {
		writeSpan(f, i+1, si)
	}

	fmt.Fprintf(f, "---\n\n")
}

func writeTraceSummary(f *os.File, index int, ti *traceInfo, config *Config) {
	fmt.Fprintf(f, "## Trace %d: `%s`\n\n", index, ti.traceID)

	// Sort spans by start time
	sort.Slice(ti.spans, func(i, j int) bool {
		return ti.spans[i].span.StartTimestamp() < ti.spans[j].span.StartTimestamp()
	})

	// Calculate trace duration
	if len(ti.spans) > 0 {
		earliest := ti.spans[0].span.StartTimestamp()
		latest := ti.spans[0].span.EndTimestamp()
		for _, si := range ti.spans {
			if si.span.StartTimestamp() < earliest {
				earliest = si.span.StartTimestamp()
			}
			if si.span.EndTimestamp() > latest {
				latest = si.span.EndTimestamp()
			}
		}
		duration := time.Duration(latest - earliest)
		fmt.Fprintf(f, "**Duration**: %v\n\n", duration)
	}

	totalSpans := len(ti.spans)
	fmt.Fprintf(f, "**Total Spans**: %d\n\n", totalSpans)

	// Determine how many spans to show
	maxSpans := config.MaxSpansPerTrace
	if maxSpans == 0 || maxSpans > totalSpans {
		maxSpans = totalSpans
	}

	if maxSpans < totalSpans {
		fmt.Fprintf(f, "*Showing first %d of %d spans*\n\n", maxSpans, totalSpans)
	}

	// Write span summary table
	fmt.Fprintf(f, "| # | Span Name | Duration | Status |\n")
	fmt.Fprintf(f, "|---|-----------|----------|--------|\n")

	for i := 0; i < maxSpans; i++ {
		si := ti.spans[i]
		span := si.span
		duration := time.Duration(span.EndTimestamp() - span.StartTimestamp())
		status := span.Status().Code().String()
		fmt.Fprintf(f, "| %d | %s | %v | %s |\n", i+1, span.Name(), duration, status)
	}

	fmt.Fprintf(f, "\n")

	// Show service information from first span's resource
	if len(ti.spans) > 0 {
		fmt.Fprintf(f, "**Service Information**:\n\n")
		resource := ti.spans[0].resource
		if serviceName, ok := resource.Attributes().Get("service.name"); ok {
			fmt.Fprintf(f, "- Service: %s\n", serviceName.AsString())
		}
		if serviceVersion, ok := resource.Attributes().Get("service.version"); ok {
			fmt.Fprintf(f, "- Version: %s\n", serviceVersion.AsString())
		}
		fmt.Fprintf(f, "\n")
	}

	fmt.Fprintf(f, "---\n\n")
}

func writeSpan(f *os.File, index int, si spanInfo) {
	span := si.span

	fmt.Fprintf(f, "### Span %d: %s\n\n", index, span.Name())

	// Basic info
	fmt.Fprintf(f, "- **Span ID**: `%s`\n", span.SpanID().String())
	fmt.Fprintf(f, "- **Parent Span ID**: `%s`\n", span.ParentSpanID().String())
	fmt.Fprintf(f, "- **Kind**: %s\n", span.Kind().String())
	fmt.Fprintf(f, "- **Status**: %s", span.Status().Code().String())

	if span.Status().Message() != "" {
		fmt.Fprintf(f, " - %s", span.Status().Message())
	}
	fmt.Fprintf(f, "\n")

	// Timing
	startTime := time.Unix(0, int64(span.StartTimestamp()))
	endTime := time.Unix(0, int64(span.EndTimestamp()))
	duration := endTime.Sub(startTime)

	fmt.Fprintf(f, "- **Start Time**: %s\n", startTime.Format(time.RFC3339Nano))
	fmt.Fprintf(f, "- **End Time**: %s\n", endTime.Format(time.RFC3339Nano))
	fmt.Fprintf(f, "- **Duration**: %v\n", duration)

	// Resource attributes
	if si.resource.Attributes().Len() > 0 {
		fmt.Fprintf(f, "\n#### Resource Attributes\n\n")
		writeAttributes(f, si.resource.Attributes())
	}

	// Scope info
	if si.scope.Name() != "" {
		fmt.Fprintf(f, "\n#### Instrumentation Scope\n\n")
		fmt.Fprintf(f, "- **Name**: %s\n", si.scope.Name())
		if si.scope.Version() != "" {
			fmt.Fprintf(f, "- **Version**: %s\n", si.scope.Version())
		}
	}

	// Span attributes
	if span.Attributes().Len() > 0 {
		fmt.Fprintf(f, "\n#### Span Attributes\n\n")
		writeAttributes(f, span.Attributes())
	}

	// Events
	if span.Events().Len() > 0 {
		fmt.Fprintf(f, "\n#### Events\n\n")
		for i := 0; i < span.Events().Len(); i++ {
			event := span.Events().At(i)
			eventTime := time.Unix(0, int64(event.Timestamp()))
			fmt.Fprintf(f, "**%d. %s** (%s)\n\n", i+1, event.Name(), eventTime.Format(time.RFC3339Nano))
			if event.Attributes().Len() > 0 {
				writeAttributes(f, event.Attributes())
				fmt.Fprintf(f, "\n")
			}
		}
	}

	// Links
	if span.Links().Len() > 0 {
		fmt.Fprintf(f, "\n#### Links\n\n")
		for i := 0; i < span.Links().Len(); i++ {
			link := span.Links().At(i)
			fmt.Fprintf(f, "**%d. Linked Trace**\n\n", i+1)
			fmt.Fprintf(f, "- **Trace ID**: `%s`\n", link.TraceID().String())
			fmt.Fprintf(f, "- **Span ID**: `%s`\n", link.SpanID().String())
			if link.Attributes().Len() > 0 {
				writeAttributes(f, link.Attributes())
			}
			fmt.Fprintf(f, "\n")
		}
	}

	fmt.Fprintf(f, "\n")
}

func writeAttributes(f *os.File, attrs pcommon.Map) {
	// Sort attributes by key for consistent output
	keys := make([]string, 0, attrs.Len())
	attrs.Range(func(k string, v pcommon.Value) bool {
		keys = append(keys, k)
		return true
	})
	sort.Strings(keys)

	for _, key := range keys {
		val, _ := attrs.Get(key)
		fmt.Fprintf(f, "- **%s**: %s\n", key, formatValue(val))
	}
}

func formatValue(val pcommon.Value) string {
	switch val.Type() {
	case pcommon.ValueTypeStr:
		return fmt.Sprintf("`%s`", val.Str())
	case pcommon.ValueTypeInt:
		return fmt.Sprintf("`%d`", val.Int())
	case pcommon.ValueTypeDouble:
		return fmt.Sprintf("`%f`", val.Double())
	case pcommon.ValueTypeBool:
		return fmt.Sprintf("`%t`", val.Bool())
	case pcommon.ValueTypeBytes:
		return fmt.Sprintf("`%x`", val.Bytes().AsRaw())
	case pcommon.ValueTypeSlice:
		var items []string
		slice := val.Slice()
		for i := 0; i < slice.Len(); i++ {
			items = append(items, formatValue(slice.At(i)))
		}
		return fmt.Sprintf("[%s]", strings.Join(items, ", "))
	case pcommon.ValueTypeMap:
		var pairs []string
		val.Map().Range(func(k string, v pcommon.Value) bool {
			pairs = append(pairs, fmt.Sprintf("%s: %s", k, formatValue(v)))
			return true
		})
		return fmt.Sprintf("{%s}", strings.Join(pairs, ", "))
	default:
		return "`<unknown>`"
	}
}
