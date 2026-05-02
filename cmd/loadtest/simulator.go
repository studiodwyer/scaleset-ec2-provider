package main

import (
	"context"
	"fmt"
	"math/rand"
	"sync"
	"sync/atomic"
	"time"

	"github.com/actions/scaleset"
	"github.com/studiodwyer/scaleset-ec2-provider/internal/metrics"
)

type JobSimulator struct {
	config  Config
	scaler  *MockScaler
	metrics *metrics.Metrics

	jobsStarted    atomic.Int64
	jobsCompleted  atomic.Int64
	maxRunners     atomic.Int64
	currentRunners atomic.Int64
}

type Results struct {
	Duration      time.Duration
	JobsCompleted int64
	Throughput    float64
	MaxRunners    int64
	TotalErrors   int64
}

func NewJobSimulator(config Config, scaler *MockScaler, m *metrics.Metrics) *JobSimulator {
	return &JobSimulator{
		config:  config,
		scaler:  scaler,
		metrics: m,
	}
}

func (s *JobSimulator) Run(ctx context.Context) Results {
	startTime := time.Now()
	var wg sync.WaitGroup
	var errorCount atomic.Int64

	rateLimiter := time.NewTicker(time.Second / time.Duration(s.config.Rate))
	defer rateLimiter.Stop()

	for i := 0; i < s.config.Jobs; i++ {
		select {
		case <-ctx.Done():
			break
		case <-rateLimiter.C:
			wg.Add(1)
			go func(jobNum int) {
				defer wg.Done()
				s.processJob(ctx, jobNum, &errorCount)
			}(i)
		}
	}

	wg.Wait()

	duration := time.Since(startTime)
	completed := s.jobsCompleted.Load()

	return Results{
		Duration:      duration,
		JobsCompleted: completed,
		Throughput:    float64(completed) / duration.Seconds(),
		MaxRunners:    s.maxRunners.Load(),
		TotalErrors:   errorCount.Load(),
	}
}

func (s *JobSimulator) processJob(ctx context.Context, jobNum int, errorCount *atomic.Int64) {
	runnerName := fmt.Sprintf("runner-%d", jobNum)
	jobID := fmt.Sprintf("job-%d", jobNum)

	jobStarted := &scaleset.JobStarted{
		JobMessageBase: scaleset.JobMessageBase{
			RunnerRequestID: int64(jobNum),
			JobID:           jobID,
		},
		RunnerName: runnerName,
	}

	if err := s.scaler.HandleJobStarted(ctx, jobStarted); err != nil {
		errorCount.Add(1)
		return
	}

	s.jobsStarted.Add(1)
	current := s.currentRunners.Add(1)

	for {
		max := s.maxRunners.Load()
		if current <= max {
			break
		}
		if s.maxRunners.CompareAndSwap(max, current) {
			break
		}
	}

	var jobDuration time.Duration
	switch s.config.Simulation {
	case "instant":
		jobDuration = 0
	case "realistic":
		minDuration := 30 * time.Second
		maxDuration := 5 * time.Minute
		jobDuration = minDuration + time.Duration(rand.Int63n(int64(maxDuration-minDuration)))
	default:
		jobDuration = 0
	}

	select {
	case <-ctx.Done():
		return
	case <-time.After(jobDuration):
	}

	jobCompleted := &scaleset.JobCompleted{
		JobMessageBase: scaleset.JobMessageBase{
			RunnerRequestID: int64(jobNum),
			JobID:           jobID,
		},
		RunnerName: runnerName,
		Result:     "success",
	}

	if err := s.scaler.HandleJobCompleted(ctx, jobCompleted); err != nil {
		errorCount.Add(1)
		return
	}

	s.jobsCompleted.Add(1)
	s.currentRunners.Add(-1)
}
