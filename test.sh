#!/usr/bin/env bash

set -e

# Tracedown configuration (customize as needed)
# Examples:
#   TRACEDOWN_ARGS="-summary -max-spans-per-trace 20"
#   TRACEDOWN_ARGS="-max-memory-mb 100 -max-traces 1000"
#   TRACEDOWN_ARGS="-grpc-port 14317 -http-port 14318"
TRACEDOWN_ARGS="${TRACEDOWN_ARGS:-}"

# Colors for output
GREEN='\033[0;32m'
BLUE='\033[0;34m'
YELLOW='\033[1;33m'
NC='\033[0m' # No Color

echo -e "${BLUE}=== Tracedown + OpenTofu Integration Test ===${NC}\n"

# Build tracedown if not already built
if [ ! -f "./tracedown" ]; then
    echo -e "${YELLOW}Building tracedown...${NC}"
    go build -o tracedown
fi

# Clean up previous test artifacts
rm -f traces.md
rm -rf test/.terraform test/terraform.tfstate* test/output.txt test/random.txt

# Start tracedown in the background
echo -e "${GREEN}Starting tracedown server...${NC}"
if [ -n "$TRACEDOWN_ARGS" ]; then
    echo -e "${BLUE}Custom arguments: $TRACEDOWN_ARGS${NC}"
fi
./tracedown $TRACEDOWN_ARGS > tracedown.log 2>&1 &
TRACEDOWN_PID=$!

# Ensure tracedown is killed on exit
trap "echo -e '\n${YELLOW}Stopping tracedown server...${NC}'; kill $TRACEDOWN_PID 2>/dev/null || true; wait $TRACEDOWN_PID 2>/dev/null || true" EXIT

# Wait for server to start
sleep 2

# Check if tracedown is still running
if ! kill -0 $TRACEDOWN_PID 2>/dev/null; then
    echo -e "${YELLOW}Error: tracedown failed to start${NC}"
    cat tracedown.log
    exit 1
fi

echo -e "${GREEN}Tracedown server started (PID: $TRACEDOWN_PID)${NC}"
echo -e "  - gRPC endpoint: localhost:4317"
echo -e "  - HTTP endpoint: localhost:4318\n"

# Configure OpenTofu to send traces to tracedown
# See: https://opentofu.org/docs/internals/tracing/#basic-configuration
export OTEL_TRACES_EXPORTER="otlp"
export OTEL_EXPORTER_OTLP_ENDPOINT="http://localhost:4317"
export OTEL_EXPORTER_OTLP_INSECURE="true"
export OTEL_SERVICE_NAME="opentofu-test"

echo -e "${BLUE}OpenTofu Tracing Configuration:${NC}"
echo "  OTEL_TRACES_EXPORTER=$OTEL_TRACES_EXPORTER"
echo "  OTEL_EXPORTER_OTLP_ENDPOINT=$OTEL_EXPORTER_OTLP_ENDPOINT"
echo "  OTEL_EXPORTER_OTLP_INSECURE=$OTEL_EXPORTER_OTLP_INSECURE"
echo "  OTEL_SERVICE_NAME=$OTEL_SERVICE_NAME"
echo ""

cd test

# Check if tofu is available
if ! command -v tofu &> /dev/null; then
    echo -e "${YELLOW}Warning: 'tofu' command not found. Trying 'terraform' instead...${NC}"
    if command -v terraform &> /dev/null; then
        TOFU_CMD="terraform"
    else
        echo -e "${YELLOW}Error: Neither 'tofu' nor 'terraform' found. Please install OpenTofu or Terraform.${NC}"
        exit 1
    fi
else
    TOFU_CMD="tofu"
fi

echo -e "${GREEN}Initializing OpenTofu...${NC}"
$TOFU_CMD init

echo -e "\n${GREEN}Running OpenTofu apply...${NC}"
$TOFU_CMD apply -auto-approve

echo -e "\n${GREEN}OpenTofu apply completed successfully!${NC}"

# Show created files
if [ -f "output.txt" ]; then
    echo -e "\n${BLUE}Created files:${NC}"
    echo "  - output.txt: $(cat output.txt)"
    if [ -f "random.txt" ]; then
        echo "  - random.txt: $(cat random.txt)"
    fi
fi

cd ..

# Give tracedown a moment to process any final traces
sleep 1

# Stop tracedown gracefully
echo -e "\n${YELLOW}Stopping tracedown to generate trace report...${NC}"
kill -TERM $TRACEDOWN_PID 2>/dev/null || true
wait $TRACEDOWN_PID 2>/dev/null || true

# Wait for traces.md to be written
sleep 1

# Display the traces
if [ -f "traces.md" ]; then
    echo -e "\n${GREEN}=== Trace Report Generated ===${NC}\n"
    cat traces.md
    echo -e "\n${GREEN}Trace report saved to: traces.md${NC}"
else
    echo -e "\n${YELLOW}Warning: traces.md was not created (no traces received)${NC}"
    echo -e "This could happen if:"
    echo -e "  - OpenTofu version doesn't support tracing (needs v1.7.0+)"
    echo -e "  - Traces failed to export to the collector"
    echo -e "\n${BLUE}Server logs:${NC}"
    cat tracedown.log
fi

echo -e "\n${GREEN}Test completed!${NC}"
