# tracedown

A lightweight, configurable OpenTelemetry trace collector that captures OTLP traces and generates markdown reports on shutdown.

## Overview

`tracedown` runs an OTLP-compatible server that receives traces via gRPC or HTTP, stores them in memory with configurable limits, and generates markdown reports when terminated. Perfect for debugging, documentation, and understanding trace flows in development and testing environments.

## Features

- **Dual Protocol Support**: Accepts traces via both gRPC (default: port 4317) and HTTP (default: port 4318)
- **OTLP Compatible**: Fully compatible with OpenTelemetry OTLP exporters (gRPC and HTTP)
- **Configurable Limits**: Control memory usage, trace count, and expiration
- **Secure by Default**: Binds to localhost only (explicit flag required for external access)
- **Smart Storage Management**: Automatic eviction of old traces when limits are reached
- **Flexible Output**: Choose between detailed or summary markdown reports
- **Comprehensive Logging**: Real-time visibility into trace reception and storage
- **Graceful Shutdown**: Ensures all traces are captured before generating the report

## Requirements

- **Go**: 1.21 or later (for building from source)
- **OpenTofu**: 1.7.0 or later (if using with OpenTofu tracing)

## Installation

### Pre-built Binaries (Recommended)

Download the latest release for your platform from the [releases page](https://github.com/spacelift-solutions/tracedown/releases).

**macOS/Linux:**
```bash
# Example for macOS (ARM64)
curl -L https://github.com/spacelift-solutions/tracedown/releases/latest/download/tracedown_<VERSION>_darwin_arm64.tar.gz | tar xz
sudo mv tracedown /usr/local/bin/

# Example for Linux (x86_64)
curl -L https://github.com/spacelift-solutions/tracedown/releases/latest/download/tracedown_<VERSION>_linux_x86_64.tar.gz | tar xz
sudo mv tracedown /usr/local/bin/
```

**Windows:**
Download the `.zip` file from releases, extract, and add to your PATH.

### Go Install

```bash
go install github.com/spacelift-solutions/tracedown@latest
```

### Build from Source

```bash
git clone https://github.com/spacelift-solutions/tracedown.git
cd tracedown
go build -o tracedown
```

## Usage

### Basic Usage

Start the trace collector with default settings:

```bash
./tracedown
```

The server will start listening on:
- gRPC: `localhost:4317`
- HTTP: `localhost:4318`

### Configuration Options

All options can be configured via command-line flags:

#### Server Configuration

```bash
-host string         # Host to bind to (default "localhost")
-grpc-port int       # Port for gRPC OTLP endpoint (default 4317)
-http-port int       # Port for HTTP OTLP endpoint (default 4318)
-bind-all            # Bind to all interfaces (0.0.0.0) - WARNING: exposes unauthenticated endpoint
```

#### Storage Limits

```bash
-max-traces int         # Maximum trace batches to store (default 10000, 0 = unlimited)
-max-memory-mb int      # Approximate max memory for traces in MB (default 500, 0 = unlimited)
-trace-expiration duration  # Expire traces older than this (default 1h, 0 = no expiration)
```

#### Output Configuration

```bash
-output string              # Output markdown file path (default "traces.md")
-summary                    # Generate summary mode with limited details
-max-spans-per-trace int    # Max spans per trace in summary mode (default 100, 0 = unlimited)
```

### Examples

**Basic usage with custom ports:**
```bash
./tracedown -grpc-port 14317 -http-port 14318
```

**High-volume scenario with memory limits:**
```bash
./tracedown -max-traces 50000 -max-memory-mb 1024 -trace-expiration 30m
```

**Summary mode for large traces:**
```bash
./tracedown -summary -max-spans-per-trace 50 -output summary.md
```

**Expose on network (use with caution):**
```bash
./tracedown -bind-all  # Binds to 0.0.0.0, accessible from network
```

### Connecting Your Application

Configure your application to send traces to tracedown:

**For OpenTofu (requires v1.7.0+):**
```bash
export OTEL_TRACES_EXPORTER=otlp
export OTEL_EXPORTER_OTLP_ENDPOINT=http://localhost:4317
export OTEL_EXPORTER_OTLP_INSECURE=true
export OTEL_SERVICE_NAME=my-tofu-project

tofu apply
```

**For general OTLP exporters:**
```bash
# HTTP
export OTEL_EXPORTER_OTLP_ENDPOINT=http://localhost:4318
export OTEL_EXPORTER_OTLP_PROTOCOL=http/protobuf

# gRPC
export OTEL_EXPORTER_OTLP_ENDPOINT=http://localhost:4317
export OTEL_EXPORTER_OTLP_PROTOCOL=grpc
```

### Stopping and Generating Report

When you're done collecting traces, stop the process:

```bash
# Press Ctrl+C or send SIGTERM
kill -TERM <pid>
```

Tracedown will gracefully shut down and generate the markdown report with statistics:

```
Final statistics:
  Trace batches: 150
  Total spans: 1,234
  Memory used: ~45.67 MB
Trace report written to traces.md
```

## Output Format

### Detailed Mode (Default)

The generated markdown file includes comprehensive trace information:

- **Report Header**: Generation timestamp, total batches, dropped/expired counts
- **Traces Grouped by Trace ID**: All spans belonging to the same trace are grouped together
- **Full Span Details**:
  - Span and parent span IDs
  - Span kind (client, server, internal, etc.)
  - Status and status message
  - Start/end times and duration
  - Resource attributes (service name, version, host, etc.)
  - Instrumentation scope information
  - Span attributes
  - Events with timestamps and attributes
  - Links to other traces

### Summary Mode (`-summary` flag)

Optimized for traces with many spans:

- **Trace Overview**: Trace ID, total duration, span count
- **Span Summary Table**: Condensed table showing span name, duration, and status
- **Limit Control**: Use `-max-spans-per-trace` to cap displayed spans
- **Service Information**: Key metadata from resource attributes

Summary mode is recommended when:
- Traces contain 100+ spans
- You need quick overview rather than deep details
- Generating reports for documentation or presentations

## Example Output

```markdown
# OpenTelemetry Traces Report

Generated: 2024-01-15T10:30:00Z

Total trace batches received: 3

---

## Trace 1: `a1b2c3d4e5f6g7h8i9j0k1l2m3n4o5p6`

**Duration**: 245.3ms

**Spans**: 5

### Span 1: GET /api/users
- **Span ID**: `abc123def456`
- **Parent Span ID**: ``
- **Kind**: SPAN_KIND_SERVER
- **Status**: STATUS_CODE_OK
...
```

## Security Considerations

- **Secure by Default**: The server binds to `localhost` only, preventing external network access
- **Network Exposure**: Use `-bind-all` flag cautiously - it exposes an unauthenticated endpoint to your network
- **No Authentication**: This tool does not implement authentication or authorization
- **Memory Protection**: Built-in limits prevent unbounded memory growth:
  - Default max: 10,000 trace batches or ~500MB (configurable)
  - Automatic eviction of oldest traces when limits are reached
  - Trace expiration after 1 hour by default
- **Development Focus**: Designed for local development and testing, not production observability

### Best Practices

1. **Never expose to internet**: Only use `-bind-all` in trusted networks
2. **Set appropriate limits**: Adjust `-max-memory-mb` and `-max-traces` for your workload
3. **Monitor logs**: Watch for "dropped" or "expired" warnings in output
4. **Clean shutdown**: Always use Ctrl+C or SIGTERM to ensure report generation

## Use Cases

- **Development Debugging**: Capture and analyze traces during local development
- **CI/CD Testing**: Collect traces from integration tests and generate reports
- **Documentation**: Generate visual trace documentation from real application flows
- **Trace Analysis**: Understand span relationships and timing in complex distributed systems

## Contributing

Contributions are welcome! Please feel free to submit issues or pull requests.

## License

MIT License - see LICENSE file for details
