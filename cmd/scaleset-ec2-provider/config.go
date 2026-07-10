package main

import (
	"context"
	"fmt"
	"log/slog"
	"net/url"
	"os"
	"strings"

	"github.com/actions/scaleset"
	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/secretsmanager"
	"github.com/pelletier/go-toml/v2"
	"github.com/studiodwyer/scaleset-ec2-provider/internal/interfaces"
)

type Config struct {
	RegistrationURL    string
	MaxRunners         int
	MinRunners         int
	ScaleSetName       string
	Labels             []string
	RunnerGroup        string
	GitHubApp          scaleset.GitHubAppAuth
	PrivateKeySecret   string
	Token              string
	LogLevel           string
	LogFormat          string
	AMI                string
	InstanceTypes      []string
	SubnetID           string
	SecurityGroupIDs   []string
	IAMInstanceProfile string
	KeyName            string
	UseSpot            bool
	MetricsPort        int
	Region             string
}

func (c *Config) defaults() {
	if c.RunnerGroup == "" {
		c.RunnerGroup = scaleset.DefaultRunnerGroup
	}
	if len(c.InstanceTypes) == 0 {
		c.InstanceTypes = []string{"t3.medium"}
	}
}

func (c *Config) Validate() error {
	c.defaults()

	if _, err := url.ParseRequestURI(c.RegistrationURL); err != nil {
		return fmt.Errorf("invalid registration URL: %w", err)
	}

	appError := c.GitHubApp.Validate()
	if c.Token == "" && appError != nil {
		return fmt.Errorf("no credentials provided: either GitHub App or PAT is required")
	}

	if c.ScaleSetName == "" {
		return fmt.Errorf("scale set name is required")
	}
	if c.AMI == "" {
		return fmt.Errorf("ami-id is required")
	}
	if c.SubnetID == "" {
		return fmt.Errorf("subnet-id is required")
	}
	if len(c.SecurityGroupIDs) == 0 {
		return fmt.Errorf("security-group-ids is required")
	}
	if c.MaxRunners < c.MinRunners {
		return fmt.Errorf("max runners cannot be less than min-runners")
	}

	return nil
}

// ResolveSecrets fetches the GitHub App private key from AWS Secrets Manager
// when private_key_secret is set, populating GitHubApp.PrivateKey. The inline
// private_key and private_key_secret are mutually exclusive.
func (c *Config) ResolveSecrets(ctx context.Context, client interfaces.SecretsManagerClient) error {
	if c.PrivateKeySecret == "" {
		return nil
	}
	if c.GitHubApp.PrivateKey != "" {
		return fmt.Errorf("private_key and private_key_secret are mutually exclusive")
	}

	out, err := client.GetSecretValue(ctx, &secretsmanager.GetSecretValueInput{
		SecretId: aws.String(c.PrivateKeySecret),
	})
	if err != nil {
		return fmt.Errorf("read private key secret %q: %w", c.PrivateKeySecret, err)
	}
	if out.SecretString == nil || *out.SecretString == "" {
		return fmt.Errorf("private key secret %q is empty", c.PrivateKeySecret)
	}

	c.GitHubApp.PrivateKey = *out.SecretString
	return nil
}

func (c *Config) ScalesetClient() (*scaleset.Client, error) {
	if err := c.GitHubApp.Validate(); err == nil {
		return scaleset.NewClientWithGitHubApp(
			scaleset.ClientWithGitHubAppConfig{
				GitHubConfigURL: c.RegistrationURL,
				GitHubAppAuth:   c.GitHubApp,
				SystemInfo:      systemInfo(0),
			},
		)
	}

	return scaleset.NewClientWithPersonalAccessToken(
		scaleset.NewClientWithPersonalAccessTokenConfig{
			GitHubConfigURL:     c.RegistrationURL,
			PersonalAccessToken: c.Token,
			SystemInfo:          systemInfo(0),
		},
	)
}

func (c *Config) Logger() *slog.Logger {
	var lvl slog.Level
	switch strings.ToLower(c.LogLevel) {
	case "debug":
		lvl = slog.LevelDebug
	case "info":
		lvl = slog.LevelInfo
	case "warn":
		lvl = slog.LevelWarn
	case "error":
		lvl = slog.LevelError
	default:
		lvl = slog.LevelInfo
	}

	switch c.LogFormat {
	case "json":
		return slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{
			AddSource: true,
			Level:     lvl,
		}))
	case "text":
		return slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{
			AddSource: true,
			Level:     lvl,
		}))
	default:
		return slog.New(slog.DiscardHandler)
	}
}

func (c *Config) BuildLabels() []scaleset.Label {
	if len(c.Labels) > 0 {
		labels := make([]scaleset.Label, len(c.Labels))
		for i, name := range c.Labels {
			labels[i] = scaleset.Label{Name: strings.TrimSpace(name)}
		}
		return labels
	}
	return []scaleset.Label{{Name: c.ScaleSetName}}
}

type tomlGitHubApp struct {
	ClientID         string `toml:"client_id"`
	InstallationID   int64  `toml:"installation_id"`
	PrivateKey       string `toml:"private_key"`
	PrivateKeySecret string `toml:"private_key_secret"`
}

type tomlConfig struct {
	RegistrationURL    string        `toml:"url"`
	MaxRunners         int           `toml:"max_runners"`
	MinRunners         int           `toml:"min_runners"`
	ScaleSetName       string        `toml:"name"`
	Labels             []string      `toml:"labels"`
	RunnerGroup        string        `toml:"runner_group"`
	GitHubApp          tomlGitHubApp `toml:"github_app"`
	Token              string        `toml:"token"`
	LogLevel           string        `toml:"log_level"`
	LogFormat          string        `toml:"log_format"`
	AMI                string        `toml:"ami_id"`
	InstanceTypes      []string      `toml:"instance_types"`
	SubnetID           string        `toml:"subnet_id"`
	SecurityGroupIDs   []string      `toml:"security_group_ids"`
	IAMInstanceProfile string        `toml:"iam_instance_profile"`
	KeyName            string        `toml:"key_name"`
	UseSpot            bool          `toml:"spot"`
	MetricsPort        int           `toml:"metrics_port"`
	Region             string        `toml:"region"`
}

func LoadConfig(path string) (Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return Config{}, fmt.Errorf("read config %q: %w", path, err)
	}

	var tc tomlConfig
	if err := toml.Unmarshal(data, &tc); err != nil {
		return Config{}, fmt.Errorf("parse config %q: %w", path, err)
	}

	return Config{
		RegistrationURL: tc.RegistrationURL,
		MaxRunners:      tc.MaxRunners,
		MinRunners:      tc.MinRunners,
		ScaleSetName:    tc.ScaleSetName,
		Labels:          tc.Labels,
		RunnerGroup:     tc.RunnerGroup,
		GitHubApp: scaleset.GitHubAppAuth{
			ClientID:       tc.GitHubApp.ClientID,
			InstallationID: tc.GitHubApp.InstallationID,
			PrivateKey:     tc.GitHubApp.PrivateKey,
		},
		PrivateKeySecret:   tc.GitHubApp.PrivateKeySecret,
		Token:              tc.Token,
		LogLevel:           tc.LogLevel,
		LogFormat:          tc.LogFormat,
		AMI:                tc.AMI,
		InstanceTypes:      tc.InstanceTypes,
		SubnetID:           tc.SubnetID,
		SecurityGroupIDs:   tc.SecurityGroupIDs,
		IAMInstanceProfile: tc.IAMInstanceProfile,
		KeyName:            tc.KeyName,
		UseSpot:            tc.UseSpot,
		MetricsPort:        tc.MetricsPort,
		Region:             tc.Region,
	}, nil
}
