package metrics

import (
	"log/slog"
	"net/http"
	"time"

	"github.com/actions/scaleset"
	"github.com/actions/scaleset/listener"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

type Metrics struct {
	registry *prometheus.Registry

	runnersIdle  prometheus.Gauge
	runnersBusy  prometheus.Gauge
	runnersTotal prometheus.Gauge

	jobsStarted   prometheus.Counter
	jobsCompleted prometheus.Counter

	instancesCreated    prometheus.Counter
	instancesTerminated prometheus.Counter

	errorsTotal *prometheus.CounterVec

	jobDuration      *prometheus.HistogramVec
	instanceLifetime *prometheus.HistogramVec
	instanceStartup  *prometheus.HistogramVec

	info prometheus.Gauge

	desiredRunners          prometheus.Gauge
	githubAvailableJobs     prometheus.Gauge
	githubAcquiredJobs      prometheus.Gauge
	githubAssignedJobs      prometheus.Gauge
	githubRunningJobs       prometheus.Gauge
	githubRegisteredRunners prometheus.Gauge
	githubBusyRunners       prometheus.Gauge
	githubIdleRunners       prometheus.Gauge

	logger *slog.Logger

	jobStartTimes      map[string]time.Time
	instanceStartTimes map[string]time.Time

	idleCount int
	busyCount int
}

type MetricsLabels struct {
	ScaleSetName  string
	InstanceTypes string
	Region        string
	AMI           string
}

func New(labels MetricsLabels, logger *slog.Logger) *Metrics {
	registry := prometheus.NewRegistry()

	constLabels := prometheus.Labels{
		"scale_set_name": labels.ScaleSetName,
		"instance_types": labels.InstanceTypes,
		"region":         labels.Region,
		"ami_id":         labels.AMI,
	}

	m := &Metrics{
		registry: registry,
		logger:   logger,

		runnersIdle: prometheus.NewGauge(prometheus.GaugeOpts{
			Name:        "scaleset_ec2_provider_runners_idle",
			Help:        "Number of idle runners",
			ConstLabels: constLabels,
		}),
		runnersBusy: prometheus.NewGauge(prometheus.GaugeOpts{
			Name:        "scaleset_ec2_provider_runners_busy",
			Help:        "Number of busy runners",
			ConstLabels: constLabels,
		}),
		runnersTotal: prometheus.NewGauge(prometheus.GaugeOpts{
			Name:        "scaleset_ec2_provider_runners_total",
			Help:        "Total number of runners",
			ConstLabels: constLabels,
		}),

		jobsStarted: prometheus.NewCounter(prometheus.CounterOpts{
			Name:        "scaleset_ec2_provider_jobs_started_total",
			Help:        "Total number of jobs started",
			ConstLabels: constLabels,
		}),
		jobsCompleted: prometheus.NewCounter(prometheus.CounterOpts{
			Name:        "scaleset_ec2_provider_jobs_completed_total",
			Help:        "Total number of jobs completed",
			ConstLabels: constLabels,
		}),

		instancesCreated: prometheus.NewCounter(prometheus.CounterOpts{
			Name:        "scaleset_ec2_provider_instances_created_total",
			Help:        "Total number of EC2 instances created",
			ConstLabels: constLabels,
		}),
		instancesTerminated: prometheus.NewCounter(prometheus.CounterOpts{
			Name:        "scaleset_ec2_provider_instances_terminated_total",
			Help:        "Total number of EC2 instances terminated",
			ConstLabels: constLabels,
		}),

		errorsTotal: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name:        "scaleset_ec2_provider_errors_total",
			Help:        "Total number of errors",
			ConstLabels: constLabels,
		}, []string{"error_type"}),

		jobDuration: prometheus.NewHistogramVec(prometheus.HistogramOpts{
			Name:        "scaleset_ec2_provider_job_duration_seconds",
			Help:        "Duration of job execution in seconds",
			ConstLabels: constLabels,
			Buckets:     []float64{10, 30, 60, 120, 300, 600, 1200, 1800, 3600, 7200},
		}, []string{}),

		instanceLifetime: prometheus.NewHistogramVec(prometheus.HistogramOpts{
			Name:        "scaleset_ec2_provider_instance_lifetime_seconds",
			Help:        "Lifetime of EC2 instances from creation to termination in seconds",
			ConstLabels: constLabels,
			Buckets:     []float64{30, 60, 120, 300, 600, 1200, 1800, 3600, 7200, 14400},
		}, []string{}),

		instanceStartup: prometheus.NewHistogramVec(prometheus.HistogramOpts{
			Name:        "scaleset_ec2_provider_instance_startup_seconds",
			Help:        "Time for EC2 instance to start up",
			ConstLabels: constLabels,
			Buckets:     []float64{5, 10, 20, 30, 60, 120, 180, 300},
		}, []string{}),

		info: prometheus.NewGauge(prometheus.GaugeOpts{
			Name:        "scaleset_ec2_provider_info",
			Help:        "Information about the scaleset-ec2-provider instance",
			ConstLabels: constLabels,
		}),

		desiredRunners: prometheus.NewGauge(prometheus.GaugeOpts{
			Name:        "scaleset_ec2_provider_desired_runners",
			Help:        "Desired number of runners as reported by the listener",
			ConstLabels: constLabels,
		}),
		githubAvailableJobs: prometheus.NewGauge(prometheus.GaugeOpts{
			Name:        "scaleset_ec2_provider_github_available_jobs",
			Help:        "Total available jobs reported by GitHub",
			ConstLabels: constLabels,
		}),
		githubAcquiredJobs: prometheus.NewGauge(prometheus.GaugeOpts{
			Name:        "scaleset_ec2_provider_github_acquired_jobs",
			Help:        "Total acquired jobs reported by GitHub",
			ConstLabels: constLabels,
		}),
		githubAssignedJobs: prometheus.NewGauge(prometheus.GaugeOpts{
			Name:        "scaleset_ec2_provider_github_assigned_jobs",
			Help:        "Total assigned jobs reported by GitHub",
			ConstLabels: constLabels,
		}),
		githubRunningJobs: prometheus.NewGauge(prometheus.GaugeOpts{
			Name:        "scaleset_ec2_provider_github_running_jobs",
			Help:        "Total running jobs reported by GitHub",
			ConstLabels: constLabels,
		}),
		githubRegisteredRunners: prometheus.NewGauge(prometheus.GaugeOpts{
			Name:        "scaleset_ec2_provider_github_registered_runners",
			Help:        "Total registered runners reported by GitHub",
			ConstLabels: constLabels,
		}),
		githubBusyRunners: prometheus.NewGauge(prometheus.GaugeOpts{
			Name:        "scaleset_ec2_provider_github_busy_runners",
			Help:        "Total busy runners reported by GitHub",
			ConstLabels: constLabels,
		}),
		githubIdleRunners: prometheus.NewGauge(prometheus.GaugeOpts{
			Name:        "scaleset_ec2_provider_github_idle_runners",
			Help:        "Total idle runners reported by GitHub",
			ConstLabels: constLabels,
		}),

		jobStartTimes:      make(map[string]time.Time),
		instanceStartTimes: make(map[string]time.Time),
	}

	registry.MustRegister(
		m.runnersIdle,
		m.runnersBusy,
		m.runnersTotal,
		m.jobsStarted,
		m.jobsCompleted,
		m.instancesCreated,
		m.instancesTerminated,
		m.errorsTotal,
		m.jobDuration,
		m.instanceLifetime,
		m.instanceStartup,
		m.info,
		m.desiredRunners,
		m.githubAvailableJobs,
		m.githubAcquiredJobs,
		m.githubAssignedJobs,
		m.githubRunningJobs,
		m.githubRegisteredRunners,
		m.githubBusyRunners,
		m.githubIdleRunners,
	)

	m.info.Set(1)

	return m
}

func (m *Metrics) IncIdleRunners() {
	m.idleCount++
	m.runnersIdle.Inc()
	m.runnersTotal.Set(float64(m.idleCount + m.busyCount))
}

func (m *Metrics) DecIdleRunners() {
	m.idleCount--
	m.runnersIdle.Dec()
	m.runnersTotal.Set(float64(m.idleCount + m.busyCount))
}

func (m *Metrics) IncBusyRunners() {
	m.busyCount++
	m.runnersBusy.Inc()
	m.runnersTotal.Set(float64(m.idleCount + m.busyCount))
}

func (m *Metrics) DecBusyRunners() {
	m.busyCount--
	m.runnersBusy.Dec()
	m.runnersTotal.Set(float64(m.idleCount + m.busyCount))
}

func (m *Metrics) IncJobsStarted(runnerName string) {
	m.jobsStarted.Inc()
	m.jobStartTimes[runnerName] = time.Now()
}

func (m *Metrics) IncJobsCompleted(runnerName string) {
	m.jobsCompleted.Inc()
	if startTime, ok := m.jobStartTimes[runnerName]; ok {
		duration := time.Since(startTime).Seconds()
		m.jobDuration.WithLabelValues().Observe(duration)
		delete(m.jobStartTimes, runnerName)
	}
}

func (m *Metrics) IncInstancesCreated(instanceID string) {
	m.instancesCreated.Inc()
	m.instanceStartTimes[instanceID] = time.Now()
}

func (m *Metrics) IncInstancesTerminated(instanceID string) {
	m.instancesTerminated.Inc()
	if startTime, ok := m.instanceStartTimes[instanceID]; ok {
		lifetime := time.Since(startTime).Seconds()
		m.instanceLifetime.WithLabelValues().Observe(lifetime)
		delete(m.instanceStartTimes, instanceID)
	}
}

func (m *Metrics) RecordInstanceStartup(instanceID string) {
	if startTime, ok := m.instanceStartTimes[instanceID]; ok {
		startupTime := time.Since(startTime).Seconds()
		m.instanceStartup.WithLabelValues().Observe(startupTime)
	}
}

func (m *Metrics) IncErrors(errorType string) {
	m.errorsTotal.WithLabelValues(errorType).Inc()
}

func (m *Metrics) Handler() http.Handler {
	return promhttp.HandlerFor(m.registry, promhttp.HandlerOpts{})
}

var _ listener.MetricsRecorder = (*Metrics)(nil)

func (m *Metrics) RecordStatistics(statistics *scaleset.RunnerScaleSetStatistic) {
	m.githubAvailableJobs.Set(float64(statistics.TotalAvailableJobs))
	m.githubAcquiredJobs.Set(float64(statistics.TotalAcquiredJobs))
	m.githubAssignedJobs.Set(float64(statistics.TotalAssignedJobs))
	m.githubRunningJobs.Set(float64(statistics.TotalRunningJobs))
	m.githubRegisteredRunners.Set(float64(statistics.TotalRegisteredRunners))
	m.githubBusyRunners.Set(float64(statistics.TotalBusyRunners))
	m.githubIdleRunners.Set(float64(statistics.TotalIdleRunners))
}

func (m *Metrics) RecordJobStarted(msg *scaleset.JobStarted) {
	m.IncJobsStarted(msg.RunnerName)
}

func (m *Metrics) RecordJobCompleted(msg *scaleset.JobCompleted) {
	m.IncJobsCompleted(msg.RunnerName)
}

func (m *Metrics) RecordDesiredRunners(count int) {
	m.desiredRunners.Set(float64(count))
}
