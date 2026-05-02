package metrics

import (
	"log/slog"
	"os"
	"strings"
	"testing"

	"github.com/actions/scaleset"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/testutil"
)

func TestNewMetrics(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(os.Stdout, nil))
	labels := MetricsLabels{
		ScaleSetName:  "test-scale-set",
		InstanceTypes: "t3.medium",
		Region:        "us-east-1",
		AMI:           "ami-12345",
	}

	metrics := New(labels, logger)
	if metrics == nil {
		t.Fatal("Expected metrics to be created, got nil")
	}

	if metrics.registry == nil {
		t.Error("Expected registry to be initialized")
	}

	if metrics.runnersIdle == nil {
		t.Error("Expected runnersIdle gauge to be initialized")
	}

	if metrics.runnersBusy == nil {
		t.Error("Expected runnersBusy gauge to be initialized")
	}

	if metrics.jobStartTimes == nil {
		t.Error("Expected jobStartTimes map to be initialized")
	}

	if metrics.instanceStartTimes == nil {
		t.Error("Expected instanceStartTimes map to be initialized")
	}
}

func TestMetricsRunnerCounts(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(os.Stdout, nil))
	labels := MetricsLabels{
		ScaleSetName:  "test",
		InstanceTypes: "t3.medium",
		Region:        "us-east-1",
		AMI:           "ami-123",
	}

	m := New(labels, logger)

	m.IncIdleRunners()
	if m.idleCount != 1 {
		t.Errorf("Expected idleCount=1, got %d", m.idleCount)
	}

	m.IncIdleRunners()
	if m.idleCount != 2 {
		t.Errorf("Expected idleCount=2, got %d", m.idleCount)
	}

	m.DecIdleRunners()
	if m.idleCount != 1 {
		t.Errorf("Expected idleCount=1, got %d", m.idleCount)
	}

	m.IncBusyRunners()
	if m.busyCount != 1 {
		t.Errorf("Expected busyCount=1, got %d", m.busyCount)
	}

	m.DecBusyRunners()
	if m.busyCount != 0 {
		t.Errorf("Expected busyCount=0, got %d", m.busyCount)
	}
}

func TestMetricsJobTracking(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(os.Stdout, nil))
	labels := MetricsLabels{
		ScaleSetName:  "test",
		InstanceTypes: "t3.medium",
		Region:        "us-east-1",
		AMI:           "ami-123",
	}

	m := New(labels, logger)

	runnerName := "runner-test-123"

	m.IncJobsStarted(runnerName)

	if _, exists := m.jobStartTimes[runnerName]; !exists {
		t.Error("Expected job start time to be recorded")
	}

	beforeComplete := testutil.ToFloat64(m.jobsCompleted)
	m.IncJobsCompleted(runnerName)
	afterComplete := testutil.ToFloat64(m.jobsCompleted)

	if afterComplete != beforeComplete+1 {
		t.Errorf("Expected jobs_completed to increment by 1, got before=%v after=%v", beforeComplete, afterComplete)
	}

	if _, exists := m.jobStartTimes[runnerName]; exists {
		t.Error("Expected job start time to be cleared after completion")
	}

	jobDurationCount := testutil.CollectAndCount(m.jobDuration)
	if jobDurationCount == 0 {
		t.Error("Expected job duration to be recorded")
	}
}

func TestMetricsInstanceTracking(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(os.Stdout, nil))
	labels := MetricsLabels{
		ScaleSetName:  "test",
		InstanceTypes: "t3.medium",
		Region:        "us-east-1",
		AMI:           "ami-123",
	}

	m := New(labels, logger)

	instanceID := "i-1234567890"

	beforeCreate := testutil.ToFloat64(m.instancesCreated)
	m.IncInstancesCreated(instanceID)
	afterCreate := testutil.ToFloat64(m.instancesCreated)

	if afterCreate != beforeCreate+1 {
		t.Errorf("Expected instances_created to increment by 1, got before=%v after=%v", beforeCreate, afterCreate)
	}

	if _, exists := m.instanceStartTimes[instanceID]; !exists {
		t.Error("Expected instance start time to be recorded")
	}

	beforeTerminate := testutil.ToFloat64(m.instancesTerminated)
	m.IncInstancesTerminated(instanceID)
	afterTerminate := testutil.ToFloat64(m.instancesTerminated)

	if afterTerminate != beforeTerminate+1 {
		t.Errorf("Expected instances_terminated to increment by 1, got before=%v after=%v", beforeTerminate, afterTerminate)
	}

	if _, exists := m.instanceStartTimes[instanceID]; exists {
		t.Error("Expected instance start time to be cleared after termination")
	}

	lifetimeCount := testutil.CollectAndCount(m.instanceLifetime)
	if lifetimeCount == 0 {
		t.Error("Expected instance lifetime to be recorded")
	}
}

func TestMetricsErrors(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(os.Stdout, nil))
	labels := MetricsLabels{
		ScaleSetName:  "test",
		InstanceTypes: "t3.medium",
		Region:        "us-east-1",
		AMI:           "ami-123",
	}

	m := New(labels, logger)

	errorTypes := []string{"ec2_run", "ec2_terminate", "jit_config"}

	for _, errorType := range errorTypes {
		before := testutil.ToFloat64(m.errorsTotal.WithLabelValues(errorType))
		m.IncErrors(errorType)
		after := testutil.ToFloat64(m.errorsTotal.WithLabelValues(errorType))

		if after != before+1 {
			t.Errorf("Expected errors_total[%s] to increment by 1, got before=%v after=%v", errorType, before, after)
		}
	}
}

func TestMetricsHandler(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(os.Stdout, nil))
	labels := MetricsLabels{
		ScaleSetName:  "test",
		InstanceTypes: "t3.medium",
		Region:        "us-east-1",
		AMI:           "ami-123",
	}

	m := New(labels, logger)

	handler := m.Handler()
	if handler == nil {
		t.Fatal("Expected handler to be created, got nil")
	}
}

func TestMetricsLabels(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(os.Stdout, nil))
	expectedLabels := MetricsLabels{
		ScaleSetName:  "my-scale-set",
		InstanceTypes: "t3.large",
		Region:        "us-west-2",
		AMI:           "ami-abcdef",
	}

	m := New(expectedLabels, logger)

	expectedMetrics := []string{
		"scaleset_ec2_provider_runners_idle",
		"scaleset_ec2_provider_runners_busy",
		"scaleset_ec2_provider_runners_total",
		"scaleset_ec2_provider_jobs_started_total",
		"scaleset_ec2_provider_jobs_completed_total",
		"scaleset_ec2_provider_instances_created_total",
		"scaleset_ec2_provider_instances_terminated_total",
	}

	metricFamilies, err := m.registry.Gather()
	if err != nil {
		t.Fatalf("Failed to gather metrics: %v", err)
	}

	foundMetrics := make(map[string]bool)
	for _, mf := range metricFamilies {
		foundMetrics[mf.GetName()] = true
	}

	for _, expected := range expectedMetrics {
		if !foundMetrics[expected] {
			t.Errorf("Expected metric %s to be registered", expected)
		}
	}

	for _, mf := range metricFamilies {
		if mf.GetName() == "scaleset_ec2_provider_runners_idle" {
			for _, m := range mf.GetMetric() {
				labels := m.GetLabel()
				labelMap := make(map[string]string)
				for _, l := range labels {
					labelMap[l.GetName()] = l.GetValue()
				}

				if labelMap["scale_set_name"] != expectedLabels.ScaleSetName {
					t.Errorf("Expected scale_set_name=%s, got %s", expectedLabels.ScaleSetName, labelMap["scale_set_name"])
				}
				if labelMap["instance_types"] != expectedLabels.InstanceTypes {
					t.Errorf("Expected instance_types=%s, got %s", expectedLabels.InstanceTypes, labelMap["instance_types"])
				}
				if labelMap["region"] != expectedLabels.Region {
					t.Errorf("Expected region=%s, got %s", expectedLabels.Region, labelMap["region"])
				}
				if labelMap["ami_id"] != expectedLabels.AMI {
					t.Errorf("Expected ami_id=%s, got %s", expectedLabels.AMI, labelMap["ami_id"])
				}
			}
		}
	}
}

func TestMetricsGaugesUpdateCorrectly(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(os.Stdout, nil))
	labels := MetricsLabels{
		ScaleSetName:  "test",
		InstanceTypes: "t3.medium",
		Region:        "us-east-1",
		AMI:           "ami-123",
	}

	m := New(labels, logger)

	m.IncIdleRunners()
	m.IncIdleRunners()
	m.IncBusyRunners()

	idleValue := testutil.ToFloat64(m.runnersIdle)
	busyValue := testutil.ToFloat64(m.runnersBusy)
	totalValue := testutil.ToFloat64(m.runnersTotal)

	if idleValue != 2 {
		t.Errorf("Expected runners_idle=2, got %v", idleValue)
	}

	if busyValue != 1 {
		t.Errorf("Expected runners_busy=1, got %v", busyValue)
	}

	if totalValue != 3 {
		t.Errorf("Expected runners_total=3, got %v", totalValue)
	}
}

func TestMetricsHistogramBuckets(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(os.Stdout, nil))
	labels := MetricsLabels{
		ScaleSetName:  "test",
		InstanceTypes: "t3.medium",
		Region:        "us-east-1",
		AMI:           "ami-123",
	}

	m := New(labels, logger)

	expectedJobDurationBuckets := []float64{10, 30, 60, 120, 300, 600, 1200, 1800, 3600, 7200}
	expectedLifetimeBuckets := []float64{30, 60, 120, 300, 600, 1200, 1800, 3600, 7200, 14400}
	expectedStartupBuckets := []float64{5, 10, 20, 30, 60, 120, 180, 300}

	metricFamilies, err := m.registry.Gather()
	if err != nil {
		t.Fatalf("Failed to gather metrics: %v", err)
	}

	for _, mf := range metricFamilies {
		switch mf.GetName() {
		case "scaleset_ec2_provider_job_duration_seconds":
			histogram := mf.GetMetric()[0].GetHistogram()
			buckets := histogram.GetBucket()
			if len(buckets) != len(expectedJobDurationBuckets) {
				t.Errorf("Expected %d job duration buckets, got %d", len(expectedJobDurationBuckets), len(buckets))
			}
		case "scaleset_ec2_provider_instance_lifetime_seconds":
			histogram := mf.GetMetric()[0].GetHistogram()
			buckets := histogram.GetBucket()
			if len(buckets) != len(expectedLifetimeBuckets) {
				t.Errorf("Expected %d instance lifetime buckets, got %d", len(expectedLifetimeBuckets), len(buckets))
			}
		case "scaleset_ec2_provider_instance_startup_seconds":
			histogram := mf.GetMetric()[0].GetHistogram()
			buckets := histogram.GetBucket()
			if len(buckets) != len(expectedStartupBuckets) {
				t.Errorf("Expected %d instance startup buckets, got %d", len(expectedStartupBuckets), len(buckets))
			}
		}
	}
}

func TestMetricsCounterIncrement(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(os.Stdout, nil))
	labels := MetricsLabels{
		ScaleSetName:  "test",
		InstanceTypes: "t3.medium",
		Region:        "us-east-1",
		AMI:           "ami-123",
	}

	m := New(labels, logger)

	for i := 0; i < 5; i++ {
		m.IncJobsStarted("runner-" + string(rune(i)))
	}

	startedValue := testutil.ToFloat64(m.jobsStarted)
	if startedValue != 5 {
		t.Errorf("Expected jobs_started_total=5, got %v", startedValue)
	}

	for i := 0; i < 3; i++ {
		m.IncInstancesCreated("i-" + string(rune(i)))
	}

	createdValue := testutil.ToFloat64(m.instancesCreated)
	if createdValue != 3 {
		t.Errorf("Expected instances_created_total=3, got %v", createdValue)
	}
}

func TestMetricsMultipleErrorTypes(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(os.Stdout, nil))
	labels := MetricsLabels{
		ScaleSetName:  "test",
		InstanceTypes: "t3.medium",
		Region:        "us-east-1",
		AMI:           "ami-123",
	}

	m := New(labels, logger)

	m.IncErrors("ec2_run")
	m.IncErrors("ec2_run")
	m.IncErrors("ec2_terminate")

	ec2RunValue := testutil.ToFloat64(m.errorsTotal.WithLabelValues("ec2_run"))
	ec2TerminateValue := testutil.ToFloat64(m.errorsTotal.WithLabelValues("ec2_terminate"))

	if ec2RunValue != 2 {
		t.Errorf("Expected errors_total[ec2_run]=2, got %v", ec2RunValue)
	}

	if ec2TerminateValue != 1 {
		t.Errorf("Expected errors_total[ec2_terminate]=1, got %v", ec2TerminateValue)
	}
}

func TestMetricsNilSafety(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(os.Stdout, nil))
	labels := MetricsLabels{
		ScaleSetName:  "test",
		InstanceTypes: "t3.medium",
		Region:        "us-east-1",
		AMI:           "ami-123",
	}

	m := New(labels, logger)

	defer func() {
		if r := recover(); r != nil {
			t.Errorf("Metrics methods should not panic: %v", r)
		}
	}()

	m.IncIdleRunners()
	m.DecIdleRunners()
	m.IncBusyRunners()
	m.DecBusyRunners()
	m.IncJobsStarted("test")
	m.IncJobsCompleted("test")
	m.IncInstancesCreated("test")
	m.IncInstancesTerminated("test")
	m.IncErrors("test")
}

func TestMetricsOutput(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(os.Stdout, nil))
	labels := MetricsLabels{
		ScaleSetName:  "test",
		InstanceTypes: "t3.medium",
		Region:        "us-east-1",
		AMI:           "ami-123",
	}

	m := New(labels, logger)

	m.IncIdleRunners()
	m.IncBusyRunners()
	m.IncJobsStarted("runner-1")
	m.IncJobsCompleted("runner-1")
	m.IncInstancesCreated("i-123")
	m.IncErrors("test_error")

	handler := m.Handler()
	if handler == nil {
		t.Fatal("Expected handler to be created")
	}
}

func TestMetricsRecordStatistics(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(os.Stdout, nil))
	labels := MetricsLabels{
		ScaleSetName:  "test",
		InstanceTypes: "t3.medium",
		Region:        "us-east-1",
		AMI:           "ami-123",
	}

	m := New(labels, logger)

	stats := &scaleset.RunnerScaleSetStatistic{
		TotalAvailableJobs:     10,
		TotalAcquiredJobs:      8,
		TotalAssignedJobs:      6,
		TotalRunningJobs:       4,
		TotalRegisteredRunners: 12,
		TotalBusyRunners:       5,
		TotalIdleRunners:       7,
	}

	m.RecordStatistics(stats)

	tests := []struct {
		name     string
		gauge    prometheus.Gauge
		expected float64
	}{
		{"available_jobs", m.githubAvailableJobs, 10},
		{"acquired_jobs", m.githubAcquiredJobs, 8},
		{"assigned_jobs", m.githubAssignedJobs, 6},
		{"running_jobs", m.githubRunningJobs, 4},
		{"registered_runners", m.githubRegisteredRunners, 12},
		{"busy_runners", m.githubBusyRunners, 5},
		{"idle_runners", m.githubIdleRunners, 7},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if v := testutil.ToFloat64(tt.gauge); v != tt.expected {
				t.Errorf("Expected %s=%v, got %v", tt.name, tt.expected, v)
			}
		})
	}
}

func TestMetricsRecordStatisticsOverwrite(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(os.Stdout, nil))
	labels := MetricsLabels{
		ScaleSetName:  "test",
		InstanceTypes: "t3.medium",
		Region:        "us-east-1",
		AMI:           "ami-123",
	}

	m := New(labels, logger)

	m.RecordStatistics(&scaleset.RunnerScaleSetStatistic{
		TotalAvailableJobs: 5,
	})
	if v := testutil.ToFloat64(m.githubAvailableJobs); v != 5 {
		t.Errorf("Expected available_jobs=5, got %v", v)
	}

	m.RecordStatistics(&scaleset.RunnerScaleSetStatistic{
		TotalAvailableJobs: 15,
	})
	if v := testutil.ToFloat64(m.githubAvailableJobs); v != 15 {
		t.Errorf("Expected available_jobs=15, got %v", v)
	}
}

func TestMetricsRecordDesiredRunners(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(os.Stdout, nil))
	labels := MetricsLabels{
		ScaleSetName:  "test",
		InstanceTypes: "t3.medium",
		Region:        "us-east-1",
		AMI:           "ami-123",
	}

	m := New(labels, logger)

	m.RecordDesiredRunners(7)
	if v := testutil.ToFloat64(m.desiredRunners); v != 7 {
		t.Errorf("Expected desired_runners=7, got %v", v)
	}

	m.RecordDesiredRunners(0)
	if v := testutil.ToFloat64(m.desiredRunners); v != 0 {
		t.Errorf("Expected desired_runners=0, got %v", v)
	}
}

func TestMetricsRecordJobStarted(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(os.Stdout, nil))
	labels := MetricsLabels{
		ScaleSetName:  "test",
		InstanceTypes: "t3.medium",
		Region:        "us-east-1",
		AMI:           "ami-123",
	}

	m := New(labels, logger)

	m.RecordJobStarted(&scaleset.JobStarted{
		RunnerName: "runner-abc",
		JobMessageBase: scaleset.JobMessageBase{
			RunnerRequestID: 1,
			JobID:           "job-1",
		},
	})

	if v := testutil.ToFloat64(m.jobsStarted); v != 1 {
		t.Errorf("Expected jobs_started_total=1, got %v", v)
	}
	if _, ok := m.jobStartTimes["runner-abc"]; !ok {
		t.Error("Expected job start time to be recorded")
	}
}

func TestMetricsRecordJobCompleted(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(os.Stdout, nil))
	labels := MetricsLabels{
		ScaleSetName:  "test",
		InstanceTypes: "t3.medium",
		Region:        "us-east-1",
		AMI:           "ami-123",
	}

	m := New(labels, logger)

	m.IncJobsStarted("runner-xyz")

	m.RecordJobCompleted(&scaleset.JobCompleted{
		RunnerName: "runner-xyz",
		JobMessageBase: scaleset.JobMessageBase{
			RunnerRequestID: 2,
			JobID:           "job-2",
		},
	})

	if v := testutil.ToFloat64(m.jobsCompleted); v != 1 {
		t.Errorf("Expected jobs_completed_total=1, got %v", v)
	}
	if _, ok := m.jobStartTimes["runner-xyz"]; ok {
		t.Error("Expected job start time to be cleared")
	}
}

func TestMetricsRecorderNilSafety(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(os.Stdout, nil))
	labels := MetricsLabels{
		ScaleSetName:  "test",
		InstanceTypes: "t3.medium",
		Region:        "us-east-1",
		AMI:           "ami-123",
	}

	m := New(labels, logger)

	defer func() {
		if r := recover(); r != nil {
			t.Errorf("MetricsRecorder methods should not panic: %v", r)
		}
	}()

	m.RecordStatistics(&scaleset.RunnerScaleSetStatistic{})
	m.RecordJobStarted(&scaleset.JobStarted{
		RunnerName: "r1",
	})
	m.RecordJobCompleted(&scaleset.JobCompleted{
		RunnerName: "r1",
	})
	m.RecordDesiredRunners(3)
}

func BenchmarkMetricsIncIdle(b *testing.B) {
	logger := slog.New(slog.NewTextHandler(os.Stdout, nil))
	labels := MetricsLabels{
		ScaleSetName:  "test",
		InstanceTypes: "t3.medium",
		Region:        "us-east-1",
		AMI:           "ami-123",
	}

	m := New(labels, logger)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		m.IncIdleRunners()
		m.DecIdleRunners()
	}
}

func BenchmarkMetricsJobTracking(b *testing.B) {
	logger := slog.New(slog.NewTextHandler(os.Stdout, nil))
	labels := MetricsLabels{
		ScaleSetName:  "test",
		InstanceTypes: "t3.medium",
		Region:        "us-east-1",
		AMI:           "ami-123",
	}

	m := New(labels, logger)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		runner := "runner-" + string(rune(i))
		m.IncJobsStarted(runner)
		m.IncJobsCompleted(runner)
	}
}

func contains(s, substr string) bool {
	return strings.Contains(s, substr)
}
