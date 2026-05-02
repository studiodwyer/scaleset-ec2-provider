package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"testing"

	"github.com/actions/scaleset"
	"github.com/aws/aws-sdk-go-v2/service/ec2/types"
	"github.com/aws/smithy-go"
	"github.com/studiodwyer/scaleset-ec2-provider/internal/mocks"
)

func newTestScaler(ec2Client *mocks.MockEC2Client, instanceTypes []string) *Scaler {
	return NewScaler(ScalerConfig{
		EC2Client:        ec2Client,
		ScalesetClient:   mocks.NewMockScalesetClient(),
		ScaleSetID:       1,
		AMI:              "ami-test",
		InstanceTypes:    instanceTypes,
		SubnetID:         "subnet-test",
		SecurityGroupIDs: []string{"sg-test"},
		MinRunners:       0,
		MaxRunners:       10,
		Logger:           slog.New(slog.NewTextHandler(os.Stdout, nil)),
	})
}

func newTestScalerWithSpot(ec2Client *mocks.MockEC2Client, instanceTypes []string) *Scaler {
	return NewScaler(ScalerConfig{
		EC2Client:        ec2Client,
		ScalesetClient:   mocks.NewMockScalesetClient(),
		ScaleSetID:       1,
		AMI:              "ami-test",
		InstanceTypes:    instanceTypes,
		SubnetID:         "subnet-test",
		SecurityGroupIDs: []string{"sg-test"},
		UseSpot:          true,
		MinRunners:       0,
		MaxRunners:       10,
		Logger:           slog.New(slog.NewTextHandler(os.Stdout, nil)),
	})
}

type capacityError struct {
	code string
}

func (e *capacityError) Error() string {
	return fmt.Sprintf("API error %s: capacity error", e.code)
}

func (e *capacityError) ErrorCode() string             { return e.code }
func (e *capacityError) ErrorMessage() string          { return "capacity error" }
func (e *capacityError) ErrorFault() smithy.ErrorFault { return smithy.FaultServer }

type nonCapacityError struct{}

func (e *nonCapacityError) Error() string { return "some other error" }

func TestStartRunner_SingleType_Success(t *testing.T) {
	ec2Client := mocks.NewMockEC2Client()
	scaler := newTestScaler(ec2Client, []string{"t3.medium"})

	name, err := scaler.startRunner(context.Background())
	if err != nil {
		t.Fatalf("Expected no error, got: %v", err)
	}
	if name == "" {
		t.Fatal("Expected runner name, got empty string")
	}
	if ec2Client.GetInstanceCount() != 1 {
		t.Errorf("Expected 1 instance, got %d", ec2Client.GetInstanceCount())
	}
}

func TestStartRunner_FallbackOnCapacityError(t *testing.T) {
	ec2Client := mocks.NewMockEC2Client()
	ec2Client.RunInstancesErrors = map[string]error{
		"c5.large": &capacityError{code: "InsufficientInstanceCapacity"},
	}

	scaler := newTestScaler(ec2Client, []string{"c5.large", "t3.medium"})

	name, err := scaler.startRunner(context.Background())
	if err != nil {
		t.Fatalf("Expected no error, got: %v", err)
	}
	if name == "" {
		t.Fatal("Expected runner name, got empty string")
	}
	if ec2Client.GetInstanceCount() != 1 {
		t.Errorf("Expected 1 instance, got %d", ec2Client.GetInstanceCount())
	}
}

func TestStartRunner_AllTypesExhausted(t *testing.T) {
	ec2Client := mocks.NewMockEC2Client()
	ec2Client.RunInstancesErrors = map[string]error{
		"c5.large":  &capacityError{code: "InsufficientInstanceCapacity"},
		"c5a.large": &capacityError{code: "InsufficientInstanceCapacity"},
		"t3.medium": &capacityError{code: "InstanceLimitExceeded"},
	}

	scaler := newTestScaler(ec2Client, []string{"c5.large", "c5a.large", "t3.medium"})

	_, err := scaler.startRunner(context.Background())
	if err == nil {
		t.Fatal("Expected error when all types exhausted, got nil")
	}
	if ec2Client.GetInstanceCount() != 0 {
		t.Errorf("Expected 0 instances, got %d", ec2Client.GetInstanceCount())
	}
}

func TestStartRunner_NonCapacityErrorFailsImmediately(t *testing.T) {
	ec2Client := mocks.NewMockEC2Client()
	ec2Client.RunInstancesError = &nonCapacityError{}

	scaler := newTestScaler(ec2Client, []string{"c5.large", "t3.medium"})

	_, err := scaler.startRunner(context.Background())
	if err == nil {
		t.Fatal("Expected error, got nil")
	}

	var ae smithy.APIError
	if errors.As(err, &ae) {
		t.Fatalf("Non-capacity error should not be wrapped as APIError")
	}
}

func TestStartRunner_VcpuLimitExceeded_IsCapacityError(t *testing.T) {
	ec2Client := mocks.NewMockEC2Client()
	ec2Client.RunInstancesErrors = map[string]error{
		"c5.large": &capacityError{code: "VcpuLimitExceeded"},
	}

	scaler := newTestScaler(ec2Client, []string{"c5.large", "t3.medium"})

	name, err := scaler.startRunner(context.Background())
	if err != nil {
		t.Fatalf("Expected fallback to succeed, got: %v", err)
	}
	if name == "" {
		t.Fatal("Expected runner name, got empty string")
	}
}

func TestStartRunner_Unsupported_IsCapacityError(t *testing.T) {
	ec2Client := mocks.NewMockEC2Client()
	ec2Client.RunInstancesErrors = map[string]error{
		"c5.large": &capacityError{code: "Unsupported"},
	}

	scaler := newTestScaler(ec2Client, []string{"c5.large", "t3.medium"})

	name, err := scaler.startRunner(context.Background())
	if err != nil {
		t.Fatalf("Expected fallback to succeed, got: %v", err)
	}
	if name == "" {
		t.Fatal("Expected runner name, got empty string")
	}
}

func TestStartRunner_InsufficientFreeAddressesInSubnet_IsCapacityError(t *testing.T) {
	ec2Client := mocks.NewMockEC2Client()
	ec2Client.RunInstancesErrors = map[string]error{
		"c5.large": &capacityError{code: "InsufficientFreeAddressesInSubnet"},
	}

	scaler := newTestScaler(ec2Client, []string{"c5.large", "t3.medium"})

	name, err := scaler.startRunner(context.Background())
	if err != nil {
		t.Fatalf("Expected fallback to succeed, got: %v", err)
	}
	if name == "" {
		t.Fatal("Expected runner name, got empty string")
	}
}

func TestStartRunner_JitConfigError(t *testing.T) {
	ec2Client := mocks.NewMockEC2Client()
	scalesetClient := mocks.NewMockScalesetClient()
	scalesetClient.GenerateJitRunnerConfigError = fmt.Errorf("jit config failed")

	scaler := NewScaler(ScalerConfig{
		EC2Client:        ec2Client,
		ScalesetClient:   scalesetClient,
		ScaleSetID:       1,
		AMI:              "ami-test",
		InstanceTypes:    []string{"t3.medium"},
		SubnetID:         "subnet-test",
		SecurityGroupIDs: []string{"sg-test"},
		MinRunners:       0,
		MaxRunners:       10,
		Logger:           slog.New(slog.NewTextHandler(os.Stdout, nil)),
	})

	_, err := scaler.startRunner(context.Background())
	if err == nil {
		t.Fatal("Expected error, got nil")
	}
}

func TestIsCapacityError(t *testing.T) {
	tests := []struct {
		name     string
		err      error
		expected bool
	}{
		{
			name:     "InsufficientInstanceCapacity",
			err:      &capacityError{code: "InsufficientInstanceCapacity"},
			expected: true,
		},
		{
			name:     "InstanceLimitExceeded",
			err:      &capacityError{code: "InstanceLimitExceeded"},
			expected: true,
		},
		{
			name:     "VcpuLimitExceeded",
			err:      &capacityError{code: "VcpuLimitExceeded"},
			expected: true,
		},
		{
			name:     "Unsupported",
			err:      &capacityError{code: "Unsupported"},
			expected: true,
		},
		{
			name:     "InsufficientFreeAddressesInSubnet",
			err:      &capacityError{code: "InsufficientFreeAddressesInSubnet"},
			expected: true,
		},
		{
			name:     "non-API error",
			err:      fmt.Errorf("some random error"),
			expected: false,
		},
		{
			name:     "nonCapacityError",
			err:      &nonCapacityError{},
			expected: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := isCapacityError(tt.err)
			if result != tt.expected {
				t.Errorf("isCapacityError(%v) = %v, want %v", tt.err, result, tt.expected)
			}
		})
	}
}

func TestHandleDesiredRunnerCount_ScaleUp(t *testing.T) {
	ec2Client := mocks.NewMockEC2Client()
	scaler := newTestScaler(ec2Client, []string{"t3.medium"})

	count, err := scaler.HandleDesiredRunnerCount(context.Background(), 3)
	if err != nil {
		t.Fatalf("Expected no error, got: %v", err)
	}
	if count != 3 {
		t.Errorf("Expected count=3, got %d", count)
	}
	if ec2Client.GetInstanceCount() != 3 {
		t.Errorf("Expected 3 instances, got %d", ec2Client.GetInstanceCount())
	}
}

func TestHandleDesiredRunnerCount_RespectsMax(t *testing.T) {
	ec2Client := mocks.NewMockEC2Client()
	scaler := newTestScaler(ec2Client, []string{"t3.medium"})

	count, err := scaler.HandleDesiredRunnerCount(context.Background(), 100)
	if err != nil {
		t.Fatalf("Expected no error, got: %v", err)
	}
	if count != 10 {
		t.Errorf("Expected count=10 (max), got %d", count)
	}
}

func TestHandleJobCompleted_TerminatesInstance(t *testing.T) {
	ec2Client := mocks.NewMockEC2Client()
	scaler := newTestScaler(ec2Client, []string{"t3.medium"})

	name, err := scaler.startRunner(context.Background())
	if err != nil {
		t.Fatalf("startRunner failed: %v", err)
	}

	instances := ec2Client.GetInstances()
	if len(instances) != 1 {
		t.Fatalf("Expected 1 instance, got %d", len(instances))
	}

	err = scaler.HandleJobCompleted(context.Background(), &scaleset.JobCompleted{
		RunnerName: name,
	})
	if err != nil {
		t.Fatalf("HandleJobCompleted failed: %v", err)
	}

	if ec2Client.GetInstanceCount() != 0 {
		t.Errorf("Expected 0 instances after termination, got %d", ec2Client.GetInstanceCount())
	}
}

func TestHandleDesiredRunnerCount_WithInstanceTypeFallback(t *testing.T) {
	ec2Client := mocks.NewMockEC2Client()
	ec2Client.RunInstancesErrors = map[string]error{
		"c5.large": &capacityError{code: "InsufficientInstanceCapacity"},
	}

	scaler := newTestScaler(ec2Client, []string{"c5.large", "t3.medium"})

	count, err := scaler.HandleDesiredRunnerCount(context.Background(), 2)
	if err != nil {
		t.Fatalf("Expected no error, got: %v", err)
	}
	if count != 2 {
		t.Errorf("Expected count=2, got %d", count)
	}
	if ec2Client.GetInstanceCount() != 2 {
		t.Errorf("Expected 2 instances, got %d", ec2Client.GetInstanceCount())
	}

	for _, inst := range ec2Client.GetInstances() {
		if inst.InstanceType == types.InstanceTypeC5Large {
			t.Error("Should not have launched c5.large due to capacity error")
		}
	}
}

func TestStartRunner_Spot_SetsMarketOptions(t *testing.T) {
	ec2Client := mocks.NewMockEC2Client()
	scaler := newTestScalerWithSpot(ec2Client, []string{"t3.medium"})

	name, err := scaler.startRunner(context.Background())
	if err != nil {
		t.Fatalf("Expected no error, got: %v", err)
	}
	if name == "" {
		t.Fatal("Expected runner name, got empty string")
	}

	if ec2Client.LastMarketOptionsOnce == nil {
		t.Fatal("Expected InstanceMarketOptions to be set for spot instance")
	}
	if ec2Client.LastMarketOptionsOnce.MarketType != types.MarketTypeSpot {
		t.Errorf("Expected MarketType spot, got %v", ec2Client.LastMarketOptionsOnce.MarketType)
	}
	if ec2Client.LastMarketOptionsOnce.SpotOptions == nil {
		t.Fatal("Expected SpotOptions to be set")
	}
}

func TestStartRunner_OnDemand_NoMarketOptions(t *testing.T) {
	ec2Client := mocks.NewMockEC2Client()
	scaler := newTestScaler(ec2Client, []string{"t3.medium"})

	name, err := scaler.startRunner(context.Background())
	if err != nil {
		t.Fatalf("Expected no error, got: %v", err)
	}
	if name == "" {
		t.Fatal("Expected runner name, got empty string")
	}

	if ec2Client.LastMarketOptionsOnce != nil {
		t.Fatal("Expected no InstanceMarketOptions for on-demand instance")
	}
}

func TestStartRunner_Spot_CapacityErrorFallback(t *testing.T) {
	ec2Client := mocks.NewMockEC2Client()
	ec2Client.RunInstancesErrors = map[string]error{
		"c5.large": &capacityError{code: "InsufficientSpotCapacity"},
	}

	scaler := newTestScalerWithSpot(ec2Client, []string{"c5.large", "t3.medium"})

	name, err := scaler.startRunner(context.Background())
	if err != nil {
		t.Fatalf("Expected fallback to succeed, got: %v", err)
	}
	if name == "" {
		t.Fatal("Expected runner name, got empty string")
	}
	if ec2Client.GetInstanceCount() != 1 {
		t.Errorf("Expected 1 instance, got %d", ec2Client.GetInstanceCount())
	}

	if ec2Client.LastMarketOptionsOnce == nil {
		t.Fatal("Expected spot market options on the fallback instance")
	}
}

func TestStartRunner_SpotMaxPriceTooLow_IsCapacityError(t *testing.T) {
	ec2Client := mocks.NewMockEC2Client()
	ec2Client.RunInstancesErrors = map[string]error{
		"c5.large": &capacityError{code: "SpotMaxPriceTooLow"},
	}

	scaler := newTestScalerWithSpot(ec2Client, []string{"c5.large", "t3.medium"})

	name, err := scaler.startRunner(context.Background())
	if err != nil {
		t.Fatalf("Expected fallback to succeed, got: %v", err)
	}
	if name == "" {
		t.Fatal("Expected runner name, got empty string")
	}
}

func TestStartRunner_SpotLimitExceeded_IsCapacityError(t *testing.T) {
	ec2Client := mocks.NewMockEC2Client()
	ec2Client.RunInstancesErrors = map[string]error{
		"c5.large": &capacityError{code: "SpotLimitExceeded"},
	}

	scaler := newTestScalerWithSpot(ec2Client, []string{"c5.large", "t3.medium"})

	name, err := scaler.startRunner(context.Background())
	if err != nil {
		t.Fatalf("Expected fallback to succeed, got: %v", err)
	}
	if name == "" {
		t.Fatal("Expected runner name, got empty string")
	}
}
