package main

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"log/slog"
	"math/rand"
	"sync"

	"github.com/actions/scaleset"
	"github.com/actions/scaleset/listener"
	"github.com/aws/aws-sdk-go-v2/service/ec2"
	"github.com/aws/aws-sdk-go-v2/service/ec2/types"
	"github.com/aws/smithy-go"
	"github.com/google/uuid"
	"github.com/studiodwyer/scaleset-ec2-provider/internal/interfaces"
	"github.com/studiodwyer/scaleset-ec2-provider/internal/metrics"
)

type Scaler struct {
	runners            *runnerState
	ec2Client          interfaces.EC2Client
	scalesetClient     interfaces.ScalesetClient
	scaleSetID         int
	ami                string
	instanceTypes      []string
	subnetID           string
	securityGroupIDs   []string
	iamInstanceProfile string
	keyName            string
	useSpot            bool
	minRunners         int
	maxRunners         int
	logger             *slog.Logger
	metrics            *metrics.Metrics
}

type ScalerConfig struct {
	EC2Client          interfaces.EC2Client
	ScalesetClient     interfaces.ScalesetClient
	ScaleSetID         int
	AMI                string
	InstanceTypes      []string
	SubnetID           string
	SecurityGroupIDs   []string
	IAMInstanceProfile string
	KeyName            string
	UseSpot            bool
	MinRunners         int
	MaxRunners         int
	Logger             *slog.Logger
	Metrics            *metrics.Metrics
}

func NewScaler(config ScalerConfig) *Scaler {
	return &Scaler{
		ec2Client:          config.EC2Client,
		scalesetClient:     config.ScalesetClient,
		scaleSetID:         config.ScaleSetID,
		ami:                config.AMI,
		instanceTypes:      config.InstanceTypes,
		subnetID:           config.SubnetID,
		securityGroupIDs:   config.SecurityGroupIDs,
		iamInstanceProfile: config.IAMInstanceProfile,
		keyName:            config.KeyName,
		useSpot:            config.UseSpot,
		minRunners:         config.MinRunners,
		maxRunners:         config.MaxRunners,
		logger:             config.Logger,
		metrics:            config.Metrics,
		runners:            newRunnerState(config.Logger.WithGroup("runner-state"), config.Metrics),
	}
}

func (s *Scaler) HandleDesiredRunnerCount(ctx context.Context, count int) (int, error) {
	currentCount := s.runners.count()
	targetRunnerCount := min(s.maxRunners, s.minRunners+count)

	switch {
	case targetRunnerCount == currentCount:
		return currentCount, nil
	case targetRunnerCount > currentCount:
		scaleUp := targetRunnerCount - currentCount
		s.logger.Info("Scaling up runners",
			slog.Int("currentCount", currentCount),
			slog.Int("desiredCount", targetRunnerCount),
			slog.Int("scaleUp", scaleUp),
		)

		for range scaleUp {
			if _, err := s.startRunner(ctx); err != nil {
				return 0, fmt.Errorf("failed to start runner: %w", err)
			}
		}
		return s.runners.count(), nil
	default:
	}
	return s.runners.count(), nil
}

func (s *Scaler) HandleJobStarted(ctx context.Context, jobInfo *scaleset.JobStarted) error {
	s.logger.Info("Job started",
		slog.Int64("runnerRequestId", jobInfo.RunnerRequestID),
		slog.String("jobId", jobInfo.JobID),
		slog.String("runnerName", jobInfo.RunnerName),
	)
	s.runners.markBusy(jobInfo.RunnerName)
	return nil
}

func (s *Scaler) HandleJobCompleted(ctx context.Context, jobInfo *scaleset.JobCompleted) error {
	s.logger.Info("Job completed",
		slog.Int64("runnerRequestId", jobInfo.RunnerRequestID),
		slog.String("jobId", jobInfo.JobID),
		slog.String("runnerName", jobInfo.RunnerName),
	)

	instanceID := s.runners.markDone(jobInfo.RunnerName)

	if instanceID == "" {
		s.logger.Warn("Runner not found in tracking, instance may have already been terminated or never tracked",
			slog.String("runnerName", jobInfo.RunnerName),
		)
		return nil
	}

	s.logger.Info("Terminating runner instance",
		slog.String("runnerName", jobInfo.RunnerName),
		slog.String("instanceId", instanceID),
	)

	_, err := s.ec2Client.TerminateInstances(ctx, &ec2.TerminateInstancesInput{
		InstanceIds: []string{instanceID},
	})
	if err != nil {
		if s.metrics != nil {
			s.metrics.IncErrors("ec2_terminate")
		}
		return fmt.Errorf("failed to terminate instance %s: %w", instanceID, err)
	}

	if s.metrics != nil {
		s.metrics.IncInstancesTerminated(instanceID)
	}

	return nil
}

func (s *Scaler) startRunner(ctx context.Context) (string, error) {
	name := fmt.Sprintf("runner-%s", uuid.NewString()[:8])

	jit, err := s.scalesetClient.GenerateJitRunnerConfig(ctx,
		&scaleset.RunnerScaleSetJitRunnerSetting{Name: name},
		s.scaleSetID,
	)
	if err != nil {
		if s.metrics != nil {
			s.metrics.IncErrors("jit_config")
		}
		return "", fmt.Errorf("failed to generate JIT config: %w", err)
	}

	userData := base64.StdEncoding.EncodeToString([]byte(s.buildUserData(jit.EncodedJITConfig)))

	var sgIDs []string
	for _, id := range s.securityGroupIDs {
		sgIDs = append(sgIDs, id)
	}

	candidateTypes := make([]string, len(s.instanceTypes))
	copy(candidateTypes, s.instanceTypes)
	rand.Shuffle(len(candidateTypes), func(i, j int) {
		candidateTypes[i], candidateTypes[j] = candidateTypes[j], candidateTypes[i]
	})

	var lastErr error
	for _, instanceType := range candidateTypes {
		input := &ec2.RunInstancesInput{
			ImageId:          &s.ami,
			InstanceType:     types.InstanceType(instanceType),
			MinCount:         int32Ptr(1),
			MaxCount:         int32Ptr(1),
			SubnetId:         &s.subnetID,
			SecurityGroupIds: sgIDs,
			UserData:         &userData,
			TagSpecifications: []types.TagSpecification{
				{
					ResourceType: types.ResourceTypeInstance,
					Tags: []types.Tag{
						{Key: strPtr("Name"), Value: &name},
						{Key: strPtr("github:scaleset-ec2-provider"), Value: strPtr("true")},
					},
				},
			},
			MetadataOptions: &types.InstanceMetadataOptionsRequest{
				HttpTokens:   types.HttpTokensStateRequired,
				HttpEndpoint: types.InstanceMetadataEndpointStateEnabled,
			},
		}

		if s.iamInstanceProfile != "" {
			input.IamInstanceProfile = &types.IamInstanceProfileSpecification{
				Name: &s.iamInstanceProfile,
			}
		}

		if s.keyName != "" {
			input.KeyName = &s.keyName
		}

		if s.useSpot {
			input.InstanceMarketOptions = &types.InstanceMarketOptionsRequest{
				MarketType:  types.MarketTypeSpot,
				SpotOptions: &types.SpotMarketOptions{},
			}
		}

		result, err := s.ec2Client.RunInstances(ctx, input)
		if err != nil {
			if isCapacityError(err) {
				s.logger.Warn("Instance type unavailable, trying next",
					slog.String("instanceType", instanceType),
					slog.String("error", err.Error()),
				)
				if s.metrics != nil {
					s.metrics.IncErrors("ec2_run_capacity")
				}
				lastErr = err
				continue
			}
			if s.metrics != nil {
				s.metrics.IncErrors("ec2_run")
			}
			return "", fmt.Errorf("failed to run instance: %w", err)
		}

		if len(result.Instances) == 0 {
			if s.metrics != nil {
				s.metrics.IncErrors("ec2_run")
			}
			return "", fmt.Errorf("no instances returned from RunInstances")
		}

		instanceID := *result.Instances[0].InstanceId
		marketType := "on-demand"
		if s.useSpot {
			marketType = "spot"
		}
		s.logger.Info("Started EC2 instance",
			slog.String("name", name),
			slog.String("instanceId", instanceID),
			slog.String("instanceType", instanceType),
			slog.String("marketType", marketType),
		)

		if s.metrics != nil {
			s.metrics.IncInstancesCreated(instanceID)
		}

		s.runners.addIdle(name, instanceID)
		return name, nil
	}

	if s.metrics != nil {
		s.metrics.IncErrors("ec2_run")
	}
	return "", fmt.Errorf("failed to start instance: all %d instance types exhausted (last error: %w)", len(s.instanceTypes), lastErr)
}

func isCapacityError(err error) bool {
	var ae smithy.APIError
	if errors.As(err, &ae) {
		switch ae.ErrorCode() {
		case "InsufficientInstanceCapacity",
			"InstanceLimitExceeded",
			"VcpuLimitExceeded",
			"Unsupported",
			"InsufficientFreeAddressesInSubnet",
			"SpotMaxPriceTooLow",
			"SpotLimitExceeded",
			"InsufficientSpotCapacity":
			return true
		}
	}
	return false
}

func (s *Scaler) buildUserData(jitConfig string) string {
	return fmt.Sprintf(`#!/bin/bash
set -euo pipefail

# Wait for cloud-init to complete
# cloud-init status --wait || true

# Start the GitHub Actions runner with JIT config
sudo su ubuntu
whoami
id -u
cd /home/ubuntu/actions-runner
export ACTIONS_RUNNER_INPUT_JITCONFIG='%s'
echo ${ACTIONS_RUNNER_INPUT_JITCONFIG} > /tmp/jitcnf
# THIS WORKS -> 
sudo -u ubuntu env ACTIONS_RUNNER_INPUT_JITCONFIG="$(cat /tmp/jitcnf)" ./run.sh 
# DOES NOT WORK -> sudo -u ubuntu env ACTIONS_RUNNER_INPUT_JITCONFIG="%%s" ./run.sh 
# DOES NOT WORK -> sudo -u ubuntu env ACTIONS_RUNNER_INPUT_JITCONFIG='%%s' ./run.sh 

# echo "${ACTIONS_RUNNER_INPUT_JITCONFIG}" > /tmp/jitcnf
# sudo -u ubuntu env ACTIONS_RUNNER_INPUT_JITCONFIG='%%s' ./run.sh &
# sudo -u ubuntu env ACTIONS_RUNNER_INPUT_JITCONFIG="${ACTIONS_RUNNER_JITCONFIG}" ./run.sh

# Wait for the runner process to exit
# RUNNER_PID=$!
# wait $RUNNER_PID || true

# Self-terminate the instance
# shutdown -h now
`, jitConfig)
}

func (s *Scaler) shutdown(ctx context.Context) {
	s.logger.Info("Shutting down runners")
	s.runners.mu.Lock()
	defer s.runners.mu.Unlock()

	var instanceIDs []string
	for _, instanceID := range s.runners.idle {
		instanceIDs = append(instanceIDs, instanceID)
	}
	for _, instanceID := range s.runners.busy {
		instanceIDs = append(instanceIDs, instanceID)
	}

	if len(instanceIDs) == 0 {
		return
	}

	s.logger.Info("Terminating instances", slog.Any("instanceIds", instanceIDs))
	_, err := s.ec2Client.TerminateInstances(ctx, &ec2.TerminateInstancesInput{
		InstanceIds: instanceIDs,
	})
	if err != nil {
		s.logger.Error("Failed to terminate instances", slog.String("error", err.Error()))
	}

	clear(s.runners.idle)
	clear(s.runners.busy)
}

var _ listener.Scaler = (*Scaler)(nil)

type runnerState struct {
	mu      sync.Mutex
	idle    map[string]string
	busy    map[string]string
	logger  *slog.Logger
	metrics *metrics.Metrics
}

func newRunnerState(logger *slog.Logger, metrics *metrics.Metrics) *runnerState {
	return &runnerState{
		idle:    make(map[string]string),
		busy:    make(map[string]string),
		logger:  logger,
		metrics: metrics,
	}
}

func (r *runnerState) count() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.idle) + len(r.busy)
}

func (r *runnerState) addIdle(name, instanceID string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.idle[name] = instanceID
	r.logger.Debug("Added idle runner", slog.String("name", name), slog.String("instanceId", instanceID))
	if r.metrics != nil {
		r.metrics.IncIdleRunners()
	}
}

func (r *runnerState) markBusy(name string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if instanceID, ok := r.idle[name]; ok {
		delete(r.idle, name)
		r.busy[name] = instanceID
		r.logger.Debug("Marked runner busy", slog.String("name", name), slog.String("instanceId", instanceID))
		if r.metrics != nil {
			r.metrics.DecIdleRunners()
			r.metrics.IncBusyRunners()
		}
	} else {
		r.logger.Warn("Runner not found in idle map when marking busy",
			slog.String("name", name),
			slog.Any("currentIdle", getKeys(r.idle)),
			slog.Any("currentBusy", getKeys(r.busy)),
		)
	}
}

func (r *runnerState) markDone(name string) string {
	r.mu.Lock()
	defer r.mu.Unlock()

	if instanceID, ok := r.busy[name]; ok {
		delete(r.busy, name)
		r.logger.Debug("Marked runner done from busy", slog.String("name", name), slog.String("instanceId", instanceID))
		if r.metrics != nil {
			r.metrics.DecBusyRunners()
		}
		return instanceID
	}
	if instanceID, ok := r.idle[name]; ok {
		delete(r.idle, name)
		r.logger.Debug("Marked runner done from idle", slog.String("name", name), slog.String("instanceId", instanceID))
		if r.metrics != nil {
			r.metrics.DecIdleRunners()
		}
		return instanceID
	}
	r.logger.Warn("Runner not found when marking done",
		slog.String("name", name),
		slog.Any("currentIdle", getKeys(r.idle)),
		slog.Any("currentBusy", getKeys(r.busy)),
	)
	return ""
}

func getKeys(m map[string]string) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	return keys
}

func int32Ptr(i int32) *int32 {
	return &i
}

func strPtr(s string) *string {
	return &s
}
