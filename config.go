package main

import (
	"flag"
	"fmt"
	"os"
	"time"
)

// Config holds all configuration for the tracedown server
type Config struct {
	// Server configuration
	Host      string
	GRPCPort  int
	HTTPPort  int
	BindAll   bool

	// Storage limits
	MaxTraces      int
	MaxMemoryMB    int
	TraceExpiration time.Duration

	// Output configuration
	OutputFile     string
	SummaryMode    bool
	MaxSpansPerTrace int
}

// NewConfig creates a configuration from command line flags
func NewConfig() *Config {
	cfg := &Config{}

	// Version flag
	showVersion := flag.Bool("version", false, "Show version information and exit")

	// Server flags
	flag.StringVar(&cfg.Host, "host", "localhost", "Host to bind to (use 0.0.0.0 to bind to all interfaces)")
	flag.IntVar(&cfg.GRPCPort, "grpc-port", 4317, "Port for gRPC OTLP endpoint")
	flag.IntVar(&cfg.HTTPPort, "http-port", 4318, "Port for HTTP OTLP endpoint")
	flag.BoolVar(&cfg.BindAll, "bind-all", false, "Bind to all network interfaces (0.0.0.0) - WARNING: exposes unauthenticated endpoint")

	// Storage flags
	flag.IntVar(&cfg.MaxTraces, "max-traces", 10000, "Maximum number of trace batches to store (0 = unlimited)")
	flag.IntVar(&cfg.MaxMemoryMB, "max-memory-mb", 500, "Approximate maximum memory for traces in MB (0 = unlimited)")
	flag.DurationVar(&cfg.TraceExpiration, "trace-expiration", 1*time.Hour, "Expire traces older than this duration (0 = no expiration)")

	// Output flags
	flag.StringVar(&cfg.OutputFile, "output", "traces.md", "Output markdown file path")
	flag.BoolVar(&cfg.SummaryMode, "summary", false, "Generate summary mode (limited span details)")
	flag.IntVar(&cfg.MaxSpansPerTrace, "max-spans-per-trace", 100, "Maximum spans to show per trace in summary mode (0 = unlimited)")

	flag.Parse()

	// Show version and exit if requested
	if *showVersion {
		fmt.Printf("tracedown version %s\n", version)
		fmt.Printf("  commit: %s\n", commit)
		fmt.Printf("  built:  %s\n", date)
		fmt.Printf("  by:     %s\n", builtBy)
		os.Exit(0)
	}

	// Apply bind-all override
	if cfg.BindAll {
		cfg.Host = "0.0.0.0"
	}

	return cfg
}

// GRPCAddr returns the full gRPC address to bind to
func (c *Config) GRPCAddr() string {
	return fmt.Sprintf("%s:%d", c.Host, c.GRPCPort)
}

// HTTPAddr returns the full HTTP address to bind to
func (c *Config) HTTPAddr() string {
	return fmt.Sprintf("%s:%d", c.Host, c.HTTPPort)
}

// Validate checks if the configuration is valid
func (c *Config) Validate() error {
	if c.GRPCPort < 1 || c.GRPCPort > 65535 {
		return fmt.Errorf("invalid gRPC port: %d", c.GRPCPort)
	}
	if c.HTTPPort < 1 || c.HTTPPort > 65535 {
		return fmt.Errorf("invalid HTTP port: %d", c.HTTPPort)
	}
	if c.GRPCPort == c.HTTPPort {
		return fmt.Errorf("gRPC and HTTP ports cannot be the same: %d", c.GRPCPort)
	}
	if c.MaxMemoryMB < 0 {
		return fmt.Errorf("max memory cannot be negative: %d", c.MaxMemoryMB)
	}
	if c.MaxTraces < 0 {
		return fmt.Errorf("max traces cannot be negative: %d", c.MaxTraces)
	}
	return nil
}

// PrintConfig logs the current configuration
func (c *Config) PrintConfig() {
	fmt.Println("Configuration:")
	fmt.Printf("  Server:\n")
	fmt.Printf("    gRPC endpoint: %s\n", c.GRPCAddr())
	fmt.Printf("    HTTP endpoint: %s\n", c.HTTPAddr())
	if c.Host == "0.0.0.0" {
		fmt.Printf("    ⚠️  WARNING: Binding to all interfaces (unauthenticated)\n")
	}
	fmt.Printf("  Storage Limits:\n")
	if c.MaxTraces > 0 {
		fmt.Printf("    Max traces: %d batches\n", c.MaxTraces)
	} else {
		fmt.Printf("    Max traces: unlimited\n")
	}
	if c.MaxMemoryMB > 0 {
		fmt.Printf("    Max memory: ~%d MB\n", c.MaxMemoryMB)
	} else {
		fmt.Printf("    Max memory: unlimited\n")
	}
	if c.TraceExpiration > 0 {
		fmt.Printf("    Trace expiration: %v\n", c.TraceExpiration)
	} else {
		fmt.Printf("    Trace expiration: disabled\n")
	}
	fmt.Printf("  Output:\n")
	fmt.Printf("    File: %s\n", c.OutputFile)
	fmt.Printf("    Mode: ")
	if c.SummaryMode {
		fmt.Printf("summary (max %d spans per trace)\n", c.MaxSpansPerTrace)
	} else {
		fmt.Println("detailed")
	}
	fmt.Println()
}
