package mocks

import (
	"context"
	"encoding/base64"
	"fmt"

	"github.com/actions/scaleset"
	"github.com/studiodwyer/scaleset-ec2-provider/internal/interfaces"
)

type MockScalesetClient struct {
	GenerateJitRunnerConfigError error
	jitConfigCount               int
}

func NewMockScalesetClient() *MockScalesetClient {
	return &MockScalesetClient{}
}

func (m *MockScalesetClient) GenerateJitRunnerConfig(ctx context.Context, setting *scaleset.RunnerScaleSetJitRunnerSetting, scaleSetID int) (*scaleset.RunnerScaleSetJitRunnerConfig, error) {
	if m.GenerateJitRunnerConfigError != nil {
		return nil, m.GenerateJitRunnerConfigError
	}

	m.jitConfigCount++

	fakeConfig := fmt.Sprintf("fake-jit-config-%s-%d", setting.Name, m.jitConfigCount)
	encodedConfig := base64.StdEncoding.EncodeToString([]byte(fakeConfig))

	return &scaleset.RunnerScaleSetJitRunnerConfig{
		EncodedJITConfig: encodedConfig,
	}, nil
}

func (m *MockScalesetClient) GetJitConfigCount() int {
	return m.jitConfigCount
}

var _ interfaces.ScalesetClient = (*MockScalesetClient)(nil)
