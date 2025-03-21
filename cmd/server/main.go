package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/socialgouv/crossplane-skyhook/pkg/config"
	"github.com/socialgouv/crossplane-skyhook/pkg/grpc"
	"github.com/socialgouv/crossplane-skyhook/pkg/http"
	"github.com/socialgouv/crossplane-skyhook/pkg/logger"
	"github.com/socialgouv/crossplane-skyhook/pkg/node"
)

func main() {
	// Load configuration from environment variables
	cfg, err := config.LoadConfig()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Failed to load configuration: %v\n", err)
		os.Exit(1)
	}

	// Define command line flags with current config values as defaults
	grpcAddr := flag.String("grpc-addr", cfg.GRPCAddress, "gRPC server address")
	httpAddr := flag.String("http-addr", ":8080", "HTTP server address for health checks")
	tempDir := flag.String("temp-dir", cfg.TempDir, "Temporary directory for code files")
	gcInterval := flag.Duration("gc-interval", cfg.GCInterval, "Garbage collection interval")
	idleTimeout := flag.Duration("idle-timeout", cfg.IdleTimeout, "Idle process timeout")
	logLevel := flag.String("log-level", cfg.LogLevel, "Log level (debug, info, warn, error)")
	logFormat := flag.String("log-format", cfg.LogFormat, "Log format (auto, text, json). Auto uses text for TTY, JSON otherwise")
	nodeServerPort := flag.Int("node-server-port", cfg.NodeServerPort, "Port for the Node.js HTTP server")
	healthCheckWait := flag.Duration("health-check-wait", cfg.HealthCheckWait, "Timeout for health check")
	healthCheckInterval := flag.Duration("health-check-interval", cfg.HealthCheckInterval, "Interval for health check polling")
	requestTimeout := flag.Duration("request-timeout", cfg.NodeRequestTimeout, "Timeout for requests")
	tlsEnabled := flag.Bool("tls-enabled", cfg.TLSEnabled, "Enable TLS")
	tlsCertFile := flag.String("tls-cert-file", cfg.TLSCertFile, "Path to TLS certificate file")
	tlsKeyFile := flag.String("tls-key-file", cfg.TLSKeyFile, "Path to TLS key file")
	flag.Parse()

	// Override config with command line flags (highest priority)
	cfg.GRPCAddress = *grpcAddr
	if *tempDir != "" {
		cfg.TempDir = *tempDir
	}
	cfg.GCInterval = *gcInterval
	cfg.IdleTimeout = *idleTimeout
	cfg.LogLevel = *logLevel
	cfg.LogFormat = *logFormat
	cfg.NodeServerPort = *nodeServerPort
	cfg.HealthCheckWait = *healthCheckWait
	cfg.HealthCheckInterval = *healthCheckInterval
	cfg.NodeRequestTimeout = *requestTimeout
	cfg.TLSEnabled = *tlsEnabled
	if *tlsCertFile != "" {
		cfg.TLSCertFile = *tlsCertFile
	}
	if *tlsKeyFile != "" {
		cfg.TLSKeyFile = *tlsKeyFile
	}

	// Create logger
	log := logger.NewLogrusLogger(cfg.LogLevel, cfg.LogFormat)

	// Log configuration source information
	log.Info("Configuration loaded from environment variables and command line flags")

	// Validate configuration
	if err := cfg.Validate(); err != nil {
		log.Fatalf("Invalid configuration: %v", err)
	}

	// Create process manager with all configuration options
	processManager, err := node.NewProcessManager(
		cfg.GCInterval,
		cfg.IdleTimeout,
		cfg.TempDir,
		log,
		node.WithNodeServerPort(cfg.NodeServerPort),
		node.WithHealthCheckWait(cfg.HealthCheckWait),
		node.WithHealthCheckInterval(cfg.HealthCheckInterval),
		node.WithRequestTimeout(cfg.NodeRequestTimeout),
	)
	if err != nil {
		log.Fatalf("Failed to create process manager: %v", err)
	}

	// Create gRPC server
	grpcServer := grpc.NewServer(processManager, log)

	// Create HTTP server for health checks
	httpServer := http.NewServer(processManager, log)

	// Start gRPC server
	go func() {
		if err := grpcServer.Start(cfg.GRPCAddress, cfg.TLSEnabled, cfg.TLSCertFile, cfg.TLSKeyFile); err != nil {
			log.Fatalf("Failed to start gRPC server: %v", err)
		}
	}()

	// Start HTTP server for health checks
	go func() {
		if err := httpServer.Start(*httpAddr); err != nil && err != http.ErrServerClosed {
			log.Fatalf("Failed to start HTTP server: %v", err)
		}
	}()

	log.Infof("gRPC server started on %s", cfg.GRPCAddress)
	log.Infof("HTTP server started on %s", *httpAddr)

	// Wait for termination signal
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	<-sigCh

	// Stop the servers
	log.Info("Shutting down...")

	// Create a context with timeout for graceful shutdown
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	// Stop the HTTP server
	if err := httpServer.Stop(ctx); err != nil {
		log.Errorf("Error stopping HTTP server: %v", err)
	}

	// Stop the gRPC server
	grpcServer.Stop()
}
