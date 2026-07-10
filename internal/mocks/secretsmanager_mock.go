package mocks

import (
	"context"
	"sync"

	"github.com/aws/aws-sdk-go-v2/service/secretsmanager"
	"github.com/studiodwyer/scaleset-ec2-provider/internal/interfaces"
)

type MockSecretsManagerClient struct {
	mu sync.Mutex

	Secrets map[string]string

	GetSecretValueError error
}

func NewMockSecretsManagerClient() *MockSecretsManagerClient {
	return &MockSecretsManagerClient{
		Secrets: make(map[string]string),
	}
}

func (m *MockSecretsManagerClient) GetSecretValue(ctx context.Context, input *secretsmanager.GetSecretValueInput, opts ...func(*secretsmanager.Options)) (*secretsmanager.GetSecretValueOutput, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.GetSecretValueError != nil {
		return nil, m.GetSecretValueError
	}

	if input == nil || input.SecretId == nil {
		return &secretsmanager.GetSecretValueOutput{}, nil
	}

	id := *input.SecretId
	value, ok := m.Secrets[id]
	if !ok {
		return &secretsmanager.GetSecretValueOutput{}, nil
	}

	return &secretsmanager.GetSecretValueOutput{
		SecretString: &value,
	}, nil
}

var _ interfaces.SecretsManagerClient = (*MockSecretsManagerClient)(nil)
