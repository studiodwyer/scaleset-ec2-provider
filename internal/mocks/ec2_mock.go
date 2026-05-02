package mocks

import (
	"context"
	"fmt"
	"sync"

	"github.com/aws/aws-sdk-go-v2/service/ec2"
	"github.com/aws/aws-sdk-go-v2/service/ec2/types"
	"github.com/studiodwyer/scaleset-ec2-provider/internal/interfaces"
)

type MockEC2Client struct {
	mu sync.Mutex

	instances             map[string]*types.Instance
	instanceCount         int
	LastMarketOptions     map[string]*types.InstanceMarketOptionsRequest
	LastMarketOptionsOnce *types.InstanceMarketOptionsRequest

	RunInstancesError       error
	RunInstancesErrors      map[string]error
	TerminateInstancesError error
}

func NewMockEC2Client() *MockEC2Client {
	return &MockEC2Client{
		instances: make(map[string]*types.Instance),
	}
}

func (m *MockEC2Client) RunInstances(ctx context.Context, input *ec2.RunInstancesInput, opts ...func(*ec2.Options)) (*ec2.RunInstancesOutput, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	instanceType := string(input.InstanceType)

	if m.RunInstancesErrors != nil {
		if err, ok := m.RunInstancesErrors[instanceType]; ok {
			return nil, err
		}
	}

	if m.RunInstancesError != nil {
		return nil, m.RunInstancesError
	}

	m.instanceCount++
	instanceID := fmt.Sprintf("i-%010d", m.instanceCount)

	if input.InstanceMarketOptions != nil {
		if m.LastMarketOptions == nil {
			m.LastMarketOptions = make(map[string]*types.InstanceMarketOptionsRequest)
		}
		m.LastMarketOptions[instanceID] = input.InstanceMarketOptions
		m.LastMarketOptionsOnce = input.InstanceMarketOptions
	} else {
		m.LastMarketOptionsOnce = nil
	}

	instance := &types.Instance{
		InstanceId:   &instanceID,
		InstanceType: input.InstanceType,
		ImageId:      input.ImageId,
	}

	if len(input.TagSpecifications) > 0 {
		for _, tagSpec := range input.TagSpecifications {
			if tagSpec.ResourceType == types.ResourceTypeInstance {
				instance.Tags = tagSpec.Tags
			}
		}
	}

	m.instances[instanceID] = instance

	return &ec2.RunInstancesOutput{
		Instances: []types.Instance{*instance},
	}, nil
}

func (m *MockEC2Client) TerminateInstances(ctx context.Context, input *ec2.TerminateInstancesInput, opts ...func(*ec2.Options)) (*ec2.TerminateInstancesOutput, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.TerminateInstancesError != nil {
		return nil, m.TerminateInstancesError
	}

	var stateChanges []types.InstanceStateChange
	for _, instanceID := range input.InstanceIds {
		if _, exists := m.instances[instanceID]; exists {
			delete(m.instances, instanceID)
			stateChanges = append(stateChanges, types.InstanceStateChange{
				InstanceId:    &instanceID,
				PreviousState: &types.InstanceState{Name: types.InstanceStateNameRunning},
				CurrentState:  &types.InstanceState{Name: types.InstanceStateNameShuttingDown},
			})
		}
	}

	return &ec2.TerminateInstancesOutput{
		TerminatingInstances: stateChanges,
	}, nil
}

func (m *MockEC2Client) GetInstanceCount() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return len(m.instances)
}

func (m *MockEC2Client) GetInstances() map[string]*types.Instance {
	m.mu.Lock()
	defer m.mu.Unlock()
	result := make(map[string]*types.Instance)
	for k, v := range m.instances {
		result[k] = v
	}
	return result
}

var _ interfaces.EC2Client = (*MockEC2Client)(nil)
