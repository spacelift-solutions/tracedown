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

	// Write overview table
	fmt.Fprintf(f, "## Overview\n\n")
	fmt.Fprintf(f, "| Metric | Value |\n")
	fmt.Fprintf(f, "|--------|-------|\n")
	fmt.Fprintf(f, "| Generated | %s |\n", time.Now().Format(time.RFC3339))
	fmt.Fprintf(f, "| Total Traces | %d |\n", len(s.traces))

	totalDropped := s.droppedOldest + s.droppedTraces
	if totalDropped > 0 {
		fmt.Fprintf(f, "| Traces Dropped | %d |\n", totalDropped)
	}
	fmt.Fprintf(f, "\n")

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

	// Group traces by status for TOC
	errorTraces := []*traceInfo{}
	successTraces := []*traceInfo{}

	for _, ti := range traces {
		if ti.hasError() {
			errorTraces = append(errorTraces, ti)
		} else {
			successTraces = append(successTraces, ti)
		}
	}

	// Write Table of Contents
	fmt.Fprintf(f, "## Table of Contents\n\n")

	if len(errorTraces) > 0 {
		fmt.Fprintf(f, "### ⚠️ Traces with Errors (%d)\n", len(errorTraces))
		fmt.Fprintf(f, "| Trace | Service | Duration | Spans | Root Operation | Status |\n")
		fmt.Fprintf(f, "|-------|---------|----------|-------|----------------|--------|\n")
		for _, ti := range errorTraces {
			traceNum := findTraceIndex(traces, ti) + 1
			writeTOCRow(f, traceNum, ti)
		}
		fmt.Fprintf(f, "\n")
	}

	if len(successTraces) > 0 {
		fmt.Fprintf(f, "### ✓ Successful Traces (%d)\n", len(successTraces))
		fmt.Fprintf(f, "| Trace | Service | Duration | Spans | Root Operation | Status |\n")
		fmt.Fprintf(f, "|-------|---------|----------|-------|----------------|--------|\n")
		for _, ti := range successTraces {
			traceNum := findTraceIndex(traces, ti) + 1
			writeTOCRow(f, traceNum, ti)
		}
		fmt.Fprintf(f, "\n")
	}

	fmt.Fprintf(f, "---\n\n")

	// Write each trace
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

func (ti *traceInfo) hasError() bool {
	for _, si := range ti.spans {
		if si.span.Status().Code() == ptrace.StatusCodeError {
			return true
		}
	}
	return false
}

func (ti *traceInfo) getDuration() time.Duration {
	if len(ti.spans) == 0 {
		return 0
	}
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
	return time.Duration(latest - earliest)
}

func (ti *traceInfo) getServiceName() string {
	if len(ti.spans) == 0 {
		return "unknown"
	}
	if serviceName, ok := ti.spans[0].resource.Attributes().Get("service.name"); ok {
		return serviceName.AsString()
	}
	return "unknown"
}

func (ti *traceInfo) getRootSpanName() string {
	// Find the span with no parent (root span)
	for _, si := range ti.spans {
		if si.span.ParentSpanID().IsEmpty() {
			return si.span.Name()
		}
	}
	// If no root found, return first span name
	if len(ti.spans) > 0 {
		return ti.spans[0].span.Name()
	}
	return "unknown"
}

func findTraceIndex(traces []*traceInfo, target *traceInfo) int {
	for i, ti := range traces {
		if ti.traceID == target.traceID {
			return i
		}
	}
	return -1
}

func writeTOCRow(f *os.File, traceNum int, ti *traceInfo) {
	duration := ti.getDuration()
	serviceName := ti.getServiceName()
	rootSpan := ti.getRootSpanName()
	status := "✓ OK"
	if ti.hasError() {
		status = "⚠️ ERROR"
	}

	// Create anchor link (markdown anchors are lowercase with hyphens)
	anchor := fmt.Sprintf("trace-%d-%s", traceNum, ti.traceID)

	fmt.Fprintf(f, "| [#%d](#%s) | %s | %v | %d | %s | %s |\n",
		traceNum, anchor, serviceName, duration, len(ti.spans), rootSpan, status)
}

type spanTreeNode struct {
	spanInfo spanInfo
	children []*spanTreeNode
	depth    int
}

func buildSpanTree(ti *traceInfo) *spanTreeNode {
	// Create a map of span ID to spanInfo for quick lookup
	spanMap := make(map[string]spanInfo)
	for _, si := range ti.spans {
		spanMap[si.span.SpanID().String()] = si
	}

	// Find root span (no parent)
	var rootSpan spanInfo
	for _, si := range ti.spans {
		if si.span.ParentSpanID().IsEmpty() {
			rootSpan = si
			break
		}
	}

	// If no root found, use first span
	if rootSpan.span.SpanID().IsEmpty() && len(ti.spans) > 0 {
		rootSpan = ti.spans[0]
	}

	// Build tree recursively
	root := &spanTreeNode{
		spanInfo: rootSpan,
		children: []*spanTreeNode{},
		depth:    0,
	}

	buildChildren(root, spanMap)
	return root
}

func buildChildren(node *spanTreeNode, spanMap map[string]spanInfo) {
	parentID := node.spanInfo.span.SpanID().String()

	for _, si := range spanMap {
		if si.span.ParentSpanID().String() == parentID {
			child := &spanTreeNode{
				spanInfo: si,
				children: []*spanTreeNode{},
				depth:    node.depth + 1,
			}
			node.children = append(node.children, child)
			buildChildren(child, spanMap)
		}
	}

	// Sort children by start time
	sort.Slice(node.children, func(i, j int) bool {
		return node.children[i].spanInfo.span.StartTimestamp() < node.children[j].spanInfo.span.StartTimestamp()
	})
}

func writeSpanTree(f *os.File, node *spanTreeNode, traceDuration time.Duration, prefix string, isLast bool) {
	span := node.spanInfo.span
	duration := time.Duration(span.EndTimestamp() - span.StartTimestamp())

	// Calculate duration bar (max 24 chars)
	barLength := 24
	if traceDuration > 0 {
		barLength = int(float64(duration) / float64(traceDuration) * 24)
		if barLength < 1 {
			barLength = 1
		}
		if barLength > 24 {
			barLength = 24
		}
	}

	bar := strings.Repeat("█", barLength)

	// Format duration with proper width
	durationStr := fmt.Sprintf("[%6s]", formatDuration(duration))

	// Add error indicator if needed
	statusIndicator := ""
	if span.Status().Code() == ptrace.StatusCodeError {
		statusIndicator = " ⚠️ ERROR"
	}

	// Determine tree characters
	connector := "├─"
	if isLast {
		connector = "└─"
	}
	if node.depth == 0 {
		connector = ""
	}

	// Calculate padding to align duration and bars
	nameMaxLen := 50
	name := span.Name()
	if len(name) > nameMaxLen {
		name = name[:nameMaxLen-3] + "..."
	}

	fmt.Fprintf(f, "%s%s %-50s %s %s%s\n", prefix, connector, name, durationStr, bar, statusIndicator)

	// Write children
	for i, child := range node.children {
		childIsLast := i == len(node.children)-1
		childPrefix := prefix
		if node.depth > 0 {
			if isLast {
				childPrefix += "   "
			} else {
				childPrefix += "│  "
			}
		}
		writeSpanTree(f, child, traceDuration, childPrefix, childIsLast)
	}
}

func formatDuration(d time.Duration) string {
	if d < time.Microsecond {
		return fmt.Sprintf("%dns", d.Nanoseconds())
	} else if d < time.Millisecond {
		return fmt.Sprintf("%.1fµs", float64(d.Nanoseconds())/1000)
	} else if d < time.Second {
		return fmt.Sprintf("%.1fms", float64(d.Microseconds())/1000)
	}
	return fmt.Sprintf("%.2fs", d.Seconds())
}

func writeTrace(f *os.File, index int, ti *traceInfo) {
	fmt.Fprintf(f, "## Trace %d: `%s`\n\n", index, ti.traceID)

	// Sort spans by start time for processing
	sort.Slice(ti.spans, func(i, j int) bool {
		return ti.spans[i].span.StartTimestamp() < ti.spans[j].span.StartTimestamp()
	})

	// Calculate trace duration and status
	duration := ti.getDuration()
	status := "✓ OK"
	if ti.hasError() {
		status = "⚠️ ERROR"
	}

	fmt.Fprintf(f, "**Duration:** %v | **Spans:** %d | **Status:** %s\n\n", duration, len(ti.spans), status)

	// Write service info table
	fmt.Fprintf(f, "### Service Info\n")
	fmt.Fprintf(f, "| Property | Value |\n")
	fmt.Fprintf(f, "|----------|-------|\n")

	if len(ti.spans) > 0 {
		resource := ti.spans[0].resource
		if serviceName, ok := resource.Attributes().Get("service.name"); ok {
			fmt.Fprintf(f, "| Service | %s |\n", serviceName.AsString())
		}
		if serviceVersion, ok := resource.Attributes().Get("service.version"); ok {
			fmt.Fprintf(f, "| Version | %s |\n", serviceVersion.AsString())
		}
		if env, ok := resource.Attributes().Get("deployment.environment"); ok {
			fmt.Fprintf(f, "| Environment | %s |\n", env.AsString())
		}
	}
	fmt.Fprintf(f, "\n")

	// Write ASCII timeline
	fmt.Fprintf(f, "### Span Timeline\n")
	fmt.Fprintf(f, "```\n")
	tree := buildSpanTree(ti)
	writeSpanTree(f, tree, duration, "", true)
	fmt.Fprintf(f, "```\n\n")

	// Write span summary table with inline collapsible details
	fmt.Fprintf(f, "### Span Summary\n")
	fmt.Fprintf(f, "| # | Name | Duration | Status | Kind | Details |\n")
	fmt.Fprintf(f, "|---|------|----------|--------|------|----------|\n")

	for i, si := range ti.spans {
		span := si.span
		spanDuration := time.Duration(span.EndTimestamp() - span.StartTimestamp())
		status := span.Status().Code().String()
		kind := span.Kind().String()

		// Build collapsible details inline
		detailsHTML := buildInlineSpanDetails(i+1, si)

		fmt.Fprintf(f, "| %d | %s | %v | %s | %s | %s |\n", i+1, span.Name(), spanDuration, status, kind, detailsHTML)
	}

	fmt.Fprintf(f, "\n---\n\n")
}

func writeTraceSummary(f *os.File, index int, ti *traceInfo, config *Config) {
	fmt.Fprintf(f, "## Trace %d: `%s`\n\n", index, ti.traceID)

	// Sort spans by start time for processing
	sort.Slice(ti.spans, func(i, j int) bool {
		return ti.spans[i].span.StartTimestamp() < ti.spans[j].span.StartTimestamp()
	})

	// Calculate trace duration and status
	duration := ti.getDuration()
	status := "✓ OK"
	if ti.hasError() {
		status = "⚠️ ERROR"
	}

	totalSpans := len(ti.spans)
	fmt.Fprintf(f, "**Duration:** %v | **Spans:** %d | **Status:** %s\n\n", duration, totalSpans, status)

	// Write service info table
	fmt.Fprintf(f, "### Service Info\n")
	fmt.Fprintf(f, "| Property | Value |\n")
	fmt.Fprintf(f, "|----------|-------|\n")

	if len(ti.spans) > 0 {
		resource := ti.spans[0].resource
		if serviceName, ok := resource.Attributes().Get("service.name"); ok {
			fmt.Fprintf(f, "| Service | %s |\n", serviceName.AsString())
		}
		if serviceVersion, ok := resource.Attributes().Get("service.version"); ok {
			fmt.Fprintf(f, "| Version | %s |\n", serviceVersion.AsString())
		}
		if env, ok := resource.Attributes().Get("deployment.environment"); ok {
			fmt.Fprintf(f, "| Environment | %s |\n", env.AsString())
		}
	}
	fmt.Fprintf(f, "\n")

	// Write ASCII timeline
	fmt.Fprintf(f, "### Span Timeline\n")
	fmt.Fprintf(f, "```\n")
	tree := buildSpanTree(ti)
	writeSpanTree(f, tree, duration, "", true)
	fmt.Fprintf(f, "```\n\n")

	// Determine how many spans to show
	maxSpans := config.MaxSpansPerTrace
	if maxSpans == 0 || maxSpans > totalSpans {
		maxSpans = totalSpans
	}

	// Write span summary table with inline collapsible details
	if maxSpans < totalSpans {
		fmt.Fprintf(f, "### Span Summary (showing first %d of %d)\n", maxSpans, totalSpans)
	} else {
		fmt.Fprintf(f, "### Span Summary\n")
	}
	fmt.Fprintf(f, "| # | Name | Duration | Status | Kind | Details |\n")
	fmt.Fprintf(f, "|---|------|----------|--------|------|----------|\n")

	for i := 0; i < maxSpans; i++ {
		si := ti.spans[i]
		span := si.span
		spanDuration := time.Duration(span.EndTimestamp() - span.StartTimestamp())
		spanStatus := span.Status().Code().String()
		kind := span.Kind().String()

		// Build collapsible details inline
		detailsHTML := buildInlineSpanDetails(i+1, si)

		fmt.Fprintf(f, "| %d | %s | %v | %s | %s | %s |\n", i+1, span.Name(), spanDuration, spanStatus, kind, detailsHTML)
	}

	if maxSpans < totalSpans {
		fmt.Fprintf(f, "\n*... %d more spans not shown*\n", totalSpans-maxSpans)
	}

	fmt.Fprintf(f, "\n---\n\n")
}

func buildInlineSpanDetails(index int, si spanInfo) string {
	span := si.span
	var builder strings.Builder

	builder.WriteString("<details><summary>View</summary><br><br>")

	// Basic properties
	builder.WriteString(fmt.Sprintf("<b>Span ID:</b> <code>%s</code><br>", span.SpanID().String()))
	builder.WriteString(fmt.Sprintf("<b>Parent ID:</b> <code>%s</code><br>", span.ParentSpanID().String()))

	if span.Status().Message() != "" {
		builder.WriteString(fmt.Sprintf("<b>Status Message:</b> %s<br>", span.Status().Message()))
	}

	// Attributes
	if span.Attributes().Len() > 0 {
		builder.WriteString("<br><b>Attributes:</b><br>")
		keys := make([]string, 0, span.Attributes().Len())
		span.Attributes().Range(func(k string, v pcommon.Value) bool {
			keys = append(keys, k)
			return true
		})
		sort.Strings(keys)

		for _, key := range keys {
			val, _ := span.Attributes().Get(key)
			builder.WriteString(fmt.Sprintf("• <code>%s</code>: %s<br>", key, formatValue(val)))
		}
	}

	// Events
	if span.Events().Len() > 0 {
		builder.WriteString("<br><b>Events:</b><br>")
		for i := 0; i < span.Events().Len(); i++ {
			event := span.Events().At(i)
			eventTime := time.Unix(0, int64(event.Timestamp()))
			builder.WriteString(fmt.Sprintf("• <code>%s</code> - %s<br>", eventTime.Format("15:04:05.000"), event.Name()))
		}
	}

	// Links
	if span.Links().Len() > 0 {
		builder.WriteString("<br><b>Links:</b><br>")
		for i := 0; i < span.Links().Len(); i++ {
			link := span.Links().At(i)
			builder.WriteString(fmt.Sprintf("• Trace: <code>%s</code><br>", link.TraceID().String()))
		}
	}

	builder.WriteString("</details>")
	return builder.String()
}

func writeSpanDetailed(f *os.File, index int, si spanInfo) {
	span := si.span

	fmt.Fprintf(f, "### Span %d: %s\n", index, span.Name())
	fmt.Fprintf(f, "| Property | Value |\n")
	fmt.Fprintf(f, "|----------|-------|\n")
	fmt.Fprintf(f, "| Span ID | `%s` |\n", span.SpanID().String())
	fmt.Fprintf(f, "| Parent ID | `%s` |\n", span.ParentSpanID().String())
	fmt.Fprintf(f, "| Kind | %s |\n", span.Kind().String())

	duration := time.Duration(span.EndTimestamp() - span.StartTimestamp())
	fmt.Fprintf(f, "| Duration | %v |\n", duration)
	fmt.Fprintf(f, "| Status | %s |\n", span.Status().Code().String())

	if span.Status().Message() != "" {
		fmt.Fprintf(f, "| Status Message | %s |\n", span.Status().Message())
	}
	fmt.Fprintf(f, "\n")

	// Span attributes in table
	if span.Attributes().Len() > 0 {
		fmt.Fprintf(f, "**Key Attributes**\n")
		fmt.Fprintf(f, "| Attribute | Value |\n")
		fmt.Fprintf(f, "|-----------|-------|\n")
		writeAttributesTable(f, span.Attributes())
		fmt.Fprintf(f, "\n")
	}

	// Events in table
	if span.Events().Len() > 0 {
		fmt.Fprintf(f, "**Events**\n")
		fmt.Fprintf(f, "| Time | Event | Details |\n")
		fmt.Fprintf(f, "|------|-------|----------|\n")
		for i := 0; i < span.Events().Len(); i++ {
			event := span.Events().At(i)
			eventTime := time.Unix(0, int64(event.Timestamp()))
			details := "-"
			if event.Attributes().Len() > 0 {
				// Get first attribute as preview
				var firstAttr string
				event.Attributes().Range(func(k string, v pcommon.Value) bool {
					firstAttr = fmt.Sprintf("`%s: %s`", k, formatValue(v))
					return false // stop after first
				})
				if event.Attributes().Len() > 1 {
					details = fmt.Sprintf("%s, ...", firstAttr)
				} else {
					details = firstAttr
				}
			}
			fmt.Fprintf(f, "| %s | %s | %s |\n", eventTime.Format("15:04:05.000"), event.Name(), details)
		}
		fmt.Fprintf(f, "\n")
	}

	// Links
	if span.Links().Len() > 0 {
		fmt.Fprintf(f, "**Links**\n")
		fmt.Fprintf(f, "| Trace ID | Span ID |\n")
		fmt.Fprintf(f, "|----------|----------|\n")
		for i := 0; i < span.Links().Len(); i++ {
			link := span.Links().At(i)
			fmt.Fprintf(f, "| `%s` | `%s` |\n", link.TraceID().String(), link.SpanID().String())
		}
		fmt.Fprintf(f, "\n")
	}
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

func writeAttributesTable(f *os.File, attrs pcommon.Map) {
	// Sort attributes by key for consistent output
	keys := make([]string, 0, attrs.Len())
	attrs.Range(func(k string, v pcommon.Value) bool {
		keys = append(keys, k)
		return true
	})
	sort.Strings(keys)

	for _, key := range keys {
		val, _ := attrs.Get(key)
		fmt.Fprintf(f, "| %s | %s |\n", key, formatValue(val))
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
