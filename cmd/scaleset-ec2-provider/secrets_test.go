package main

import (
	"context"
	"strings"
	"testing"

	"github.com/studiodwyer/scaleset-ec2-provider/internal/mocks"
)

const secretConfig = `
url = "https://github.com/org/repo"
name = "ec2-runners"
ami_id = "ami-1234567890abcdef0"
subnet_id = "subnet-12345678"
security_group_ids = ["sg-aaaaaa"]

[github_app]
client_id = "Iv1.abc"
installation_id = 123456
private_key_secret = "github/app-private-key"
`

const inlineAndSecretConfig = `
url = "https://github.com/org/repo"
name = "ec2-runners"
ami_id = "ami-1234567890abcdef0"
subnet_id = "subnet-12345678"
security_group_ids = ["sg-aaaaaa"]

[github_app]
client_id = "Iv1.abc"
installation_id = 123456
private_key = """-----BEGIN RSA PRIVATE KEY-----
INLINE
-----END RSA PRIVATE KEY-----"""
private_key_secret = "github/app-private-key"
`

func TestLoadConfig_PrivateKeySecret(t *testing.T) {
	path := writeTempConfig(t, secretConfig)

	cfg, err := LoadConfig(path)
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}

	if cfg.PrivateKeySecret != "github/app-private-key" {
		t.Errorf("PrivateKeySecret = %q", cfg.PrivateKeySecret)
	}
	if cfg.GitHubApp.PrivateKey != "" {
		t.Errorf("PrivateKey should be empty before resolution, got %q", cfg.GitHubApp.PrivateKey)
	}
}

func TestResolveSecrets_FetchesKey(t *testing.T) {
	path := writeTempConfig(t, secretConfig)
	cfg, err := LoadConfig(path)
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}

	client := mocks.NewMockSecretsManagerClient()
	client.Secrets["github/app-private-key"] = "-----BEGIN RSA PRIVATE KEY-----\nFROMSECRET\n-----END RSA PRIVATE KEY-----"

	if err := cfg.ResolveSecrets(context.Background(), client); err != nil {
		t.Fatalf("ResolveSecrets: %v", err)
	}

	if !strings.Contains(cfg.GitHubApp.PrivateKey, "FROMSECRET") {
		t.Errorf("PrivateKey = %q, want value fetched from secret", cfg.GitHubApp.PrivateKey)
	}

	if err := cfg.Validate(); err != nil {
		t.Errorf("Validate after resolution: %v", err)
	}
}

func TestResolveSecrets_NoSecretIsNoOp(t *testing.T) {
	path := writeTempConfig(t, githubAppConfig)
	cfg, err := LoadConfig(path)
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}

	before := cfg.GitHubApp.PrivateKey
	if err := cfg.ResolveSecrets(context.Background(), mocks.NewMockSecretsManagerClient()); err != nil {
		t.Fatalf("ResolveSecrets: %v", err)
	}
	if cfg.GitHubApp.PrivateKey != before {
		t.Errorf("PrivateKey changed unexpectedly: %q -> %q", before, cfg.GitHubApp.PrivateKey)
	}
}

func TestResolveSecrets_MutuallyExclusive(t *testing.T) {
	path := writeTempConfig(t, inlineAndSecretConfig)
	cfg, err := LoadConfig(path)
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}

	err = cfg.ResolveSecrets(context.Background(), mocks.NewMockSecretsManagerClient())
	if err == nil {
		t.Fatal("expected error when both private_key and private_key_secret are set, got nil")
	}
	if !strings.Contains(err.Error(), "mutually exclusive") {
		t.Errorf("error = %q, want substring %q", err.Error(), "mutually exclusive")
	}
}

func TestResolveSecrets_FetchError(t *testing.T) {
	path := writeTempConfig(t, secretConfig)
	cfg, err := LoadConfig(path)
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}

	client := mocks.NewMockSecretsManagerClient()
	client.GetSecretValueError = errBoom

	err = cfg.ResolveSecrets(context.Background(), client)
	if err == nil {
		t.Fatal("expected error from Secrets Manager, got nil")
	}
	if !strings.Contains(err.Error(), "github/app-private-key") {
		t.Errorf("error = %q, want the secret id in the message", err.Error())
	}
	if cfg.GitHubApp.PrivateKey != "" {
		t.Errorf("PrivateKey should remain empty on error, got %q", cfg.GitHubApp.PrivateKey)
	}
}

func TestResolveSecrets_EmptySecretValue(t *testing.T) {
	path := writeTempConfig(t, secretConfig)
	cfg, err := LoadConfig(path)
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}

	client := mocks.NewMockSecretsManagerClient()
	// Secret id exists but maps to empty string -> mock returns empty SecretString.

	err = cfg.ResolveSecrets(context.Background(), client)
	if err == nil {
		t.Fatal("expected error for empty secret value, got nil")
	}
	if !strings.Contains(err.Error(), "empty") {
		t.Errorf("error = %q, want substring %q", err.Error(), "empty")
	}
}

var errBoom = &boomErr{}

type boomErr struct{}

func (e *boomErr) Error() string { return "boom" }
