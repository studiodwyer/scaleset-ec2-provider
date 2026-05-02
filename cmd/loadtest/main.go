package main

import (
	"context"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"sync"
	"time"

	"github.com/actions/scaleset"
	"github.com/studiodwyer/scaleset-ec2-provider/internal/metrics"
	"github.com/studiodwyer/scaleset-ec2-provider/internal/mocks"
)

type Config struct {
	Jobs        int
	Rate        int
	Simulation  string
	MetricsPort int
	MaxDuration time.Duration
	Concurrency int
}

func main() {
	cfg := Config{}

	flag.IntVar(&cfg.Jobs, "jobs", 100, "Total number of jobs to simulate")
	flag.IntVar(&cfg.Rate, "rate", 10, "Jobs per second to start")
	flag.StringVar(&cfg.Simulation, "simulation", "instant", "Simulation mode: 'instant' or 'realistic'")
	flag.IntVar(&cfg.MetricsPort, "metrics-port", 0, "Port for Prometheus metrics endpoint (0 = disabled)")
	flag.DurationVar(&cfg.MaxDuration, "max-duration", 10*time.Minute, "Maximum test duration")
	flag.IntVar(&cfg.Concurrency, "concurrency", 10, "Maximum concurrent runners")

	flag.Parse()

	if cfg.Simulation != "instant" && cfg.Simulation != "realistic" {
		fmt.Fprintf(os.Stderr, "Invalid simulation mode: %s (must be 'instant' or 'realistic')\n", cfg.Simulation)
		os.Exit(1)
	}

	if err := runLoadTest(cfg); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
}

func runLoadTest(cfg Config) error {
	logger := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{
		Level: slog.LevelInfo,
	}))

	ec2Mock := mocks.NewMockEC2Client()
	scalesetMock := mocks.NewMockScalesetClient()

	fmt.Println("=== Load Test Configuration ===")
	fmt.Printf("Jobs:          %d\n", cfg.Jobs)
	fmt.Printf("Rate:          %d/sec\n", cfg.Rate)
	fmt.Printf("Simulation:    %s\n", cfg.Simulation)
	fmt.Printf("Concurrency:   %d\n", cfg.Concurrency)
	fmt.Printf("Max Duration:  %v\n", cfg.MaxDuration)
	if cfg.MetricsPort > 0 {
		fmt.Printf("Metrics Port:  %d\n", cfg.MetricsPort)
		fmt.Printf("Prometheus:    http://localhost:%d/metrics\n", cfg.MetricsPort)
	}
	fmt.Println()

	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt)
	defer cancel()

	var metricsServer *metrics.Server
	var m *metrics.Metrics

	if cfg.MetricsPort > 0 {
		m = metrics.New(metrics.MetricsLabels{
			ScaleSetName:  "loadtest",
			InstanceTypes: "t3.medium",
			Region:        "us-east-1",
			AMI:           "ami-loadtest",
		}, logger.WithGroup("metrics"))

		metricsServer = metrics.NewServer(cfg.MetricsPort, m, logger.WithGroup("metrics-server"))
		if err := metricsServer.Start(ctx); err != nil {
			return fmt.Errorf("failed to start metrics server: %w", err)
		}
		defer metricsServer.Stop(context.WithoutCancel(ctx))
	}

	scaler := NewMockScaler(ec2Mock, scalesetMock, m, logger.WithGroup("scaler"), cfg.Concurrency)

	simulator := NewJobSimulator(cfg, scaler, m)

	fmt.Println("Running load test...")
	startTime := time.Now()

	results := simulator.Run(ctx)

	fmt.Println()
	fmt.Println("=== Results ===")
	fmt.Printf("Duration:          %v\n", results.Duration)
	fmt.Printf("Jobs Completed:    %d\n", results.JobsCompleted)
	fmt.Printf("Throughput:        %.2f jobs/sec\n", results.Throughput)
	fmt.Printf("Max Runners:       %d\n", results.MaxRunners)
	fmt.Printf("Total Errors:      %d\n", results.TotalErrors)
	fmt.Printf("Instances Created: %d\n", ec2Mock.GetInstanceCount())
	fmt.Printf("JIT Configs:       %d\n", scalesetMock.GetJitConfigCount())

	if results.TotalErrors > 0 {
		fmt.Printf("\n❌ Load test failed with %d errors\n", results.TotalErrors)
		os.Exit(1)
	}

	if int(results.JobsCompleted) < cfg.Jobs {
		fmt.Printf("\n⚠️  Only completed %d/%d jobs\n", results.JobsCompleted, cfg.Jobs)
	}

	fmt.Printf("\n✓ Load test completed successfully in %v\n", time.Since(startTime))
	return nil
}

type MockScaler struct {
	ec2Mock      *mocks.MockEC2Client
	scalesetMock *mocks.MockScalesetClient
	metrics      *metrics.Metrics
	logger       *slog.Logger
	maxRunners   int
	instanceType string
	ami          string

	runnersMu sync.Mutex
	runners   map[string]string
}

func NewMockScaler(ec2Mock *mocks.MockEC2Client, scalesetMock *mocks.MockScalesetClient, m *metrics.Metrics, logger *slog.Logger, maxRunners int) *MockScaler {
	return &MockScaler{
		ec2Mock:      ec2Mock,
		scalesetMock: scalesetMock,
		metrics:      m,
		logger:       logger,
		maxRunners:   maxRunners,
		runners:      make(map[string]string),
	}
}

func (s *MockScaler) HandleJobStarted(ctx context.Context, jobInfo *scaleset.JobStarted) error {
	s.runnersMu.Lock()
	defer s.runnersMu.Unlock()

	instanceID := fmt.Sprintf("i-%s", jobInfo.RunnerName)
	s.runners[jobInfo.RunnerName] = instanceID

	if s.metrics != nil {
		s.metrics.IncJobsStarted(jobInfo.RunnerName)
		s.metrics.IncBusyRunners()
		s.metrics.IncInstancesCreated(instanceID)
	}

	s.logger.Debug("Job started", "runner", jobInfo.RunnerName, "instance", instanceID)
	return nil
}

func (s *MockScaler) HandleJobCompleted(ctx context.Context, jobInfo *scaleset.JobCompleted) error {
	s.runnersMu.Lock()
	instanceID, exists := s.runners[jobInfo.RunnerName]
	if exists {
		delete(s.runners, jobInfo.RunnerName)
	}
	s.runnersMu.Unlock()

	if s.metrics != nil {
		s.metrics.IncJobsCompleted(jobInfo.RunnerName)
		if exists {
			s.metrics.DecBusyRunners()
			s.metrics.IncInstancesTerminated(instanceID)
		}
	}

	s.logger.Debug("Job completed", "runner", jobInfo.RunnerName, "instance", instanceID)
	return nil
}
