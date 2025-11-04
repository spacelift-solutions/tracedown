package main

import (
	"context"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"go.opentelemetry.io/collector/pdata/ptrace/ptraceotlp"
	"google.golang.org/grpc"
)

// Version information set by ldflags at build time
var (
	version = "dev"
	commit  = "none"
	date    = "unknown"
	builtBy = "unknown"
)

func main() {
	// Load configuration
	config := NewConfig()
	if err := config.Validate(); err != nil {
		log.Fatalf("Invalid configuration: %v", err)
	}

	config.PrintConfig()

	// Initialize trace storage
	storage := NewTraceStorage(config)

	// Setup gRPC server for OTLP
	grpcServer, grpcListener := setupGRPCServer(storage, config)

	// Setup HTTP server for OTLP
	httpServer := setupHTTPServer(storage, config)

	// Start servers
	go func() {
		log.Printf("Starting gRPC server on %s", config.GRPCAddr())
		if err := grpcServer.Serve(grpcListener); err != nil {
			log.Printf("gRPC server error: %v", err)
		}
	}()

	go func() {
		log.Printf("Starting HTTP server on %s", config.HTTPAddr())
		if err := httpServer.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Printf("HTTP server error: %v", err)
		}
	}()

	// Wait for interrupt signal
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, os.Interrupt, syscall.SIGTERM)
	<-sigChan

	log.Println("\nShutting down gracefully...")

	// Print final statistics
	batches, spans, dropped, expired, memMB := storage.GetStats()
	log.Printf("Final statistics:")
	log.Printf("  Trace batches: %d", batches)
	log.Printf("  Total spans: %d", spans)
	log.Printf("  Memory used: ~%.2f MB", memMB)
	if dropped > 0 {
		log.Printf("  Traces dropped (limit): %d", dropped)
	}
	if expired > 0 {
		log.Printf("  Traces expired (age): %d", expired)
	}

	// Shutdown servers
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	grpcServer.GracefulStop()
	if err := httpServer.Shutdown(ctx); err != nil {
		log.Printf("HTTP server shutdown error: %v", err)
	}

	// Generate markdown from collected traces
	if err := storage.WriteMarkdown(config); err != nil {
		log.Fatalf("Failed to write markdown: %v", err)
	}

	log.Printf("Trace report written to %s", config.OutputFile)
}

func setupGRPCServer(storage *TraceStorage, config *Config) (*grpc.Server, net.Listener) {
	listener, err := net.Listen("tcp", config.GRPCAddr())
	if err != nil {
		log.Fatalf("Failed to listen on %s: %v", config.GRPCAddr(), err)
	}

	server := grpc.NewServer()
	ptraceotlp.RegisterGRPCServer(server, &grpcTraceReceiver{storage: storage})

	return server, listener
}

func setupHTTPServer(storage *TraceStorage, config *Config) *http.Server {
	mux := http.NewServeMux()

	// OTLP/HTTP endpoint
	mux.HandleFunc("/v1/traces", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			log.Printf("HTTP: Method not allowed: %s from %s", r.Method, r.RemoteAddr)
			http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
			return
		}

		receiver := &httpTraceReceiver{storage: storage}
		req := ptraceotlp.NewExportRequest()

		body, err := io.ReadAll(r.Body)
		if err != nil {
			log.Printf("HTTP: Failed to read request body from %s: %v", r.RemoteAddr, err)
			http.Error(w, fmt.Sprintf("Failed to read request body: %v", err), http.StatusBadRequest)
			return
		}

		if err := req.UnmarshalProto(body); err != nil {
			log.Printf("HTTP: Failed to parse OTLP request from %s: %v", r.RemoteAddr, err)
			http.Error(w, fmt.Sprintf("Failed to parse request: %v", err), http.StatusBadRequest)
			return
		}

		resp, err := receiver.Export(r.Context(), req)
		if err != nil {
			log.Printf("HTTP: Failed to export traces from %s: %v", r.RemoteAddr, err)
			http.Error(w, fmt.Sprintf("Failed to export: %v", err), http.StatusInternalServerError)
			return
		}

		w.Header().Set("Content-Type", "application/x-protobuf")
		w.WriteHeader(http.StatusOK)

		data, err := resp.MarshalProto()
		if err != nil {
			log.Printf("HTTP: Failed to marshal response: %v", err)
			return
		}
		w.Write(data)
	})

	return &http.Server{
		Addr:    config.HTTPAddr(),
		Handler: mux,
	}
}

// grpcTraceReceiver implements the gRPC OTLP trace receiver
type grpcTraceReceiver struct {
	ptraceotlp.UnimplementedGRPCServer
	storage *TraceStorage
}

func (r *grpcTraceReceiver) Export(ctx context.Context, req ptraceotlp.ExportRequest) (ptraceotlp.ExportResponse, error) {
	traces := req.Traces()
	r.storage.AddTraces(traces)
	return ptraceotlp.NewExportResponse(), nil
}

func (r *grpcTraceReceiver) MustEmbedUnimplementedGRPCServer() {}

// httpTraceReceiver handles HTTP OTLP trace requests
type httpTraceReceiver struct {
	storage *TraceStorage
}

func (r *httpTraceReceiver) Export(ctx context.Context, req ptraceotlp.ExportRequest) (ptraceotlp.ExportResponse, error) {
	traces := req.Traces()
	r.storage.AddTraces(traces)
	return ptraceotlp.NewExportResponse(), nil
}
