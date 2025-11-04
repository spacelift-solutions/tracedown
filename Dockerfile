# Multi-stage build for minimal image size
FROM alpine:latest

# Install ca-certificates for HTTPS support
RUN apk --no-cache add ca-certificates tzdata

# Create non-root user
RUN addgroup -g 1000 tracedown && \
    adduser -D -u 1000 -G tracedown tracedown

# Copy the binary from goreleaser
COPY tracedown /usr/local/bin/tracedown

# Create directory for output files
RUN mkdir -p /data && chown tracedown:tracedown /data

USER tracedown
WORKDIR /data

# Expose OTLP ports
EXPOSE 4317 4318

# Default command with bind-all flag for container usage
ENTRYPOINT ["/usr/local/bin/tracedown"]
CMD ["-bind-all", "-output", "/data/traces.md"]
