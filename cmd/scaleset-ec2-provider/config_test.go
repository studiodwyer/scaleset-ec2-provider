package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const githubAppConfig = `
url = "https://github.com/org/repo"
name = "ec2-runners"
labels = ["ec2-runner", "linux"]
max_runners = 5
min_runners = 1
ami_id = "ami-1234567890abcdef0"
instance_types = ["t3.medium", "t3.large"]
subnet_id = "subnet-12345678"
security_group_ids = ["sg-aaaaaa", "sg-bbbbbb"]
iam_instance_profile = "github-actions-runner-instance"
metrics_port = 9090
region = "ap-southeast-2"
log_level = "debug"
log_format = "json"

[github_app]
client_id = "Iv1.abc"
installation_id = 123456
private_key = """-----BEGIN RSA PRIVATE KEY-----
FAKEKEY
-----END RSA PRIVATE KEY-----"""
`

const tokenConfig = `
url = "https://github.com/org/repo"
name = "ec2-runners"
ami_id = "ami-1234567890abcdef0"
subnet_id = "subnet-12345678"
security_group_ids = ["sg-aaaaaa"]
token = "ghp_token"
`

func writeTempConfig(t *testing.T, content string) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "config.toml")
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}
	return path
}

func TestLoadConfig_GitHubApp(t *testing.T) {
	path := writeTempConfig(t, githubAppConfig)

	cfg, err := LoadConfig(path)
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}

	if cfg.RegistrationURL != "https://github.com/org/repo" {
		t.Errorf("RegistrationURL = %q", cfg.RegistrationURL)
	}
	if cfg.ScaleSetName != "ec2-runners" {
		t.Errorf("ScaleSetName = %q", cfg.ScaleSetName)
	}
	if len(cfg.Labels) != 2 || cfg.Labels[0] != "ec2-runner" || cfg.Labels[1] != "linux" {
		t.Errorf("Labels = %v", cfg.Labels)
	}
	if cfg.MaxRunners != 5 || cfg.MinRunners != 1 {
		t.Errorf("runners = max:%d min:%d", cfg.MaxRunners, cfg.MinRunners)
	}
	if cfg.GitHubApp.ClientID != "Iv1.abc" {
		t.Errorf("GitHubApp.ClientID = %q", cfg.GitHubApp.ClientID)
	}
	if cfg.GitHubApp.InstallationID != 123456 {
		t.Errorf("GitHubApp.InstallationID = %d", cfg.GitHubApp.InstallationID)
	}
	if !strings.Contains(cfg.GitHubApp.PrivateKey, "FAKEKEY") {
		t.Errorf("GitHubApp.PrivateKey lost multiline content: %q", cfg.GitHubApp.PrivateKey)
	}
	if len(cfg.InstanceTypes) != 2 {
		t.Errorf("InstanceTypes = %v", cfg.InstanceTypes)
	}
	if len(cfg.SecurityGroupIDs) != 2 {
		t.Errorf("SecurityGroupIDs = %v", cfg.SecurityGroupIDs)
	}
	if cfg.MetricsPort != 9090 {
		t.Errorf("MetricsPort = %d", cfg.MetricsPort)
	}

	if err := cfg.Validate(); err != nil {
		t.Errorf("Validate: %v", err)
	}
}

func TestLoadConfig_Token(t *testing.T) {
	path := writeTempConfig(t, tokenConfig)

	cfg, err := LoadConfig(path)
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}

	if cfg.Token != "ghp_token" {
		t.Errorf("Token = %q", cfg.Token)
	}
	if cfg.GitHubApp.ClientID != "" {
		t.Errorf("GitHubApp.ClientID should be empty, got %q", cfg.GitHubApp.ClientID)
	}

	if err := cfg.Validate(); err != nil {
		t.Errorf("Validate: %v", err)
	}
}

func TestLoadConfig_Defaults(t *testing.T) {
	path := writeTempConfig(t, tokenConfig)

	cfg, err := LoadConfig(path)
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}

	if cfg.RunnerGroup != "" {
		t.Errorf("RunnerGroup should be empty before Validate, got %q", cfg.RunnerGroup)
	}
	if len(cfg.InstanceTypes) != 0 {
		t.Errorf("InstanceTypes should be empty before Validate, got %v", cfg.InstanceTypes)
	}

	if err := cfg.Validate(); err != nil {
		t.Fatalf("Validate: %v", err)
	}

	if cfg.RunnerGroup != "default" {
		t.Errorf("RunnerGroup default = %q", cfg.RunnerGroup)
	}
	if len(cfg.InstanceTypes) != 1 || cfg.InstanceTypes[0] != "t3.medium" {
		t.Errorf("InstanceTypes default = %v", cfg.InstanceTypes)
	}
}

func TestLoadConfig_MissingFile(t *testing.T) {
	_, err := LoadConfig(filepath.Join(t.TempDir(), "does-not-exist.toml"))
	if err == nil {
		t.Fatal("expected error for missing file, got nil")
	}
}

func TestLoadConfig_Malformed(t *testing.T) {
	path := writeTempConfig(t, "this is = = not valid toml [[[")
	if _, err := LoadConfig(path); err == nil {
		t.Fatal("expected error for malformed toml, got nil")
	}
}

func TestValidate_RequiredFields(t *testing.T) {
	cases := []struct {
		name string
		mut  func(*Config)
		want string
	}{
		{"missing url", func(c *Config) { c.RegistrationURL = "" }, "invalid registration URL"},
		{"missing name", func(c *Config) { c.ScaleSetName = "" }, "scale set name is required"},
		{"missing ami", func(c *Config) { c.AMI = "" }, "ami-id is required"},
		{"missing subnet", func(c *Config) { c.SubnetID = "" }, "subnet-id is required"},
		{"missing security groups", func(c *Config) { c.SecurityGroupIDs = nil }, "security-group-ids is required"},
		{"no credentials", func(c *Config) { c.Token = "" }, "no credentials provided"},
		{"max < min", func(c *Config) {
			c.MinRunners = 5
			c.MaxRunners = 1
		}, "max runners cannot be less than min-runners"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			c := validConfig()
			tc.mut(&c)
			err := c.Validate()
			if err == nil {
				t.Fatal("expected error, got nil")
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Errorf("error = %q, want substring %q", err.Error(), tc.want)
			}
		})
	}
}

func validConfig() Config {
	return Config{
		RegistrationURL:  "https://github.com/org/repo",
		ScaleSetName:     "ec2-runners",
		AMI:              "ami-test",
		SubnetID:         "subnet-test",
		SecurityGroupIDs: []string{"sg-test"},
		Token:            "ghp_token",
		MaxRunners:       10,
		MinRunners:       0,
	}
}

func TestValidateReloadable_NoCredentialsOK(t *testing.T) {
	// Mirrors a Secrets Manager setup: the private key is not in the config
	// file, and auth/url/name are absent. Reload must not require them.
	c := Config{
		AMI:              "ami-test",
		SubnetID:         "subnet-test",
		SecurityGroupIDs: []string{"sg-test"},
		MaxRunners:       10,
		MinRunners:       0,
	}
	if err := c.ValidateReloadable(); err != nil {
		t.Fatalf("ValidateReloadable should ignore auth, got: %v", err)
	}
}

func TestValidateReloadable_DefaultsInstanceTypes(t *testing.T) {
	c := Config{
		AMI:              "ami-test",
		SubnetID:         "subnet-test",
		SecurityGroupIDs: []string{"sg-test"},
	}
	if err := c.ValidateReloadable(); err != nil {
		t.Fatalf("ValidateReloadable: %v", err)
	}
	if len(c.InstanceTypes) != 1 || c.InstanceTypes[0] != "t3.medium" {
		t.Errorf("expected default t3.medium, got %v", c.InstanceTypes)
	}
}

func TestValidateReloadable_InvalidFields(t *testing.T) {
	cases := []struct {
		name string
		mut  func(*Config)
		want string
	}{
		{"missing ami", func(c *Config) { c.AMI = "" }, "ami-id is required"},
		{"missing subnet", func(c *Config) { c.SubnetID = "" }, "subnet-id is required"},
		{"missing security groups", func(c *Config) { c.SecurityGroupIDs = nil }, "security-group-ids is required"},
		{"max < min", func(c *Config) {
			c.MinRunners = 5
			c.MaxRunners = 1
		}, "max runners cannot be less than min-runners"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			c := Config{
				AMI:              "ami-test",
				SubnetID:         "subnet-test",
				SecurityGroupIDs: []string{"sg-test"},
				MaxRunners:       10,
				MinRunners:       0,
			}
			tc.mut(&c)
			err := c.ValidateReloadable()
			if err == nil {
				t.Fatal("expected error, got nil")
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Errorf("error = %q, want substring %q", err.Error(), tc.want)
			}
		})
	}
}
