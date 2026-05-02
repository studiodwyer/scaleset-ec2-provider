package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"strings"

	"github.com/actions/scaleset"
	"github.com/actions/scaleset/listener"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/ec2"
	"github.com/google/uuid"
	"github.com/studiodwyer/scaleset-ec2-provider/internal/interfaces"
	"github.com/studiodwyer/scaleset-ec2-provider/internal/metrics"
)

var (
	version = "dev"
	commit  = "none"
	date    = "unknown"
)

func main() {
	if len(os.Args) < 2 {
		printUsage()
		os.Exit(1)
	}

	command := os.Args[1]
	switch command {
	case "run", "-run", "--run":
		runCommand(os.Args[2:])
	case "delete":
		deleteCommand(os.Args[2:])
	case "version", "-version", "--version":
		fmt.Printf("%s (commit: %s, built: %s)\n", version, commit, date)
		os.Exit(0)
	case "help", "-h", "--help":
		printUsage()
		os.Exit(0)
	default:
		if strings.HasPrefix(command, "-") {
			runCommand(os.Args[1:])
		} else {
			fmt.Fprintf(os.Stderr, "Unknown command: %s\n\n", command)
			printUsage()
			os.Exit(1)
		}
	}
}

func printUsage() {
	fmt.Fprintf(os.Stderr, "Usage: scaleset-ec2-provider <command> [options]\n\n")
	fmt.Fprintf(os.Stderr, "Commands:\n")
	fmt.Fprintf(os.Stderr, "  run      Run the scaler (default if no command specified)\n")
	fmt.Fprintf(os.Stderr, "  delete   Delete a scale set\n")
	fmt.Fprintf(os.Stderr, "  version  Print version information\n")
	fmt.Fprintf(os.Stderr, "  help     Print this help message\n\n")
	fmt.Fprintf(os.Stderr, "Use \"scaleset-ec2-provider <command> -help\" for more information about a command.\n")
}

func runCommand(args []string) {
	var cfg Config
	var labels, securityGroupIDs, instanceTypes string
	var showVersion bool

	fs := flag.NewFlagSet("run", flag.ExitOnError)
	fs.StringVar(&cfg.RegistrationURL, "url", "", "REQUIRED: URL where to register your scale set (e.g. https://github.com/org/repo)")
	fs.IntVar(&cfg.MaxRunners, "max-runners", 10, "Maximum number of runners")
	fs.IntVar(&cfg.MinRunners, "min-runners", 0, "Minimum number of runners")
	fs.StringVar(&cfg.ScaleSetName, "name", "", "REQUIRED: Name of your scale set")
	fs.StringVar(&labels, "labels", "", "Labels for workflow targeting (comma-separated). Defaults to --name if not provided.")
	fs.StringVar(&cfg.RunnerGroup, "runner-group", scaleset.DefaultRunnerGroup, "Name of the runner group your scale set should belong to")
	fs.StringVar(&cfg.GitHubApp.ClientID, "app-client-id", "", "GitHub App client id")
	fs.Int64Var(&cfg.GitHubApp.InstallationID, "app-installation-id", 0, "GitHub App installation ID")
	fs.StringVar(&cfg.GitHubApp.PrivateKey, "app-private-key", "", "GitHub App private key")
	fs.StringVar(&cfg.Token, "token", "", "Personal access token (can be used in place of a GitHub App)")
	fs.StringVar(&cfg.LogLevel, "log-level", "info", "Logging level (debug, info, warn, error)")
	fs.StringVar(&cfg.LogFormat, "log-format", "text", "Logging format (text, json)")
	fs.StringVar(&cfg.AMI, "ami-id", "", "REQUIRED: AMI ID with GitHub Actions runner pre-installed")
	fs.StringVar(&instanceTypes, "instance-types", "t3.medium", "EC2 instance types (comma-separated, shuffled on each launch)")
	fs.StringVar(&cfg.SubnetID, "subnet-id", "", "REQUIRED: Subnet ID for EC2 instances")
	fs.StringVar(&securityGroupIDs, "security-group-ids", "", "REQUIRED: Security group IDs (comma-separated)")
	fs.StringVar(&cfg.IAMInstanceProfile, "iam-instance-profile", "", "IAM instance profile name for EC2 instances")
	fs.StringVar(&cfg.KeyName, "key-name", "", "SSH key pair name for runner instances")
	fs.BoolVar(&cfg.UseSpot, "spot", false, "Use EC2 Spot instances instead of on-demand")
	fs.IntVar(&cfg.MetricsPort, "metrics-port", 0, "Port for Prometheus metrics endpoint (0 = disabled)")
	fs.StringVar(&cfg.Region, "region", "", "AWS region for metrics labels (auto-detected if not specified)")
	fs.BoolVar(&showVersion, "version", false, "Print version information")

	fs.Usage = func() {
		fmt.Fprintf(fs.Output(), "Usage: scaleset-ec2-provider run [options]\n\n")
		fmt.Fprintf(fs.Output(), "Scale GitHub Actions runners using ephemeral EC2 instances.\n\nOptions:\n")
		fs.PrintDefaults()
	}

	fs.Parse(args)

	if showVersion {
		fmt.Printf("%s (commit: %s, built: %s)\n", version, commit, date)
		os.Exit(0)
	}

	if labels != "" {
		cfg.Labels = splitAndTrim(labels)
	}
	if securityGroupIDs != "" {
		cfg.SecurityGroupIDs = splitAndTrim(securityGroupIDs)
	}
	if instanceTypes != "" {
		cfg.InstanceTypes = splitAndTrim(instanceTypes)
	}

	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt)
	defer cancel()

	if err := cfg.Validate(); err != nil {
		fmt.Fprintf(os.Stderr, "invalid configuration: %v\n", err)
		os.Exit(1)
	}

	if err := run(ctx, cfg); err != nil {
		fmt.Fprintf(os.Stderr, "%v\n", err)
		os.Exit(1)
	}
}

func deleteCommand(args []string) {
	var (
		url           string
		scaleSetName  string
		runnerGroup   string
		appClientID   string
		appInstallID  int64
		appPrivateKey string
		token         string
		logLevel      string
		logFormat     string
	)

	fs := flag.NewFlagSet("delete", flag.ExitOnError)
	fs.StringVar(&url, "url", "", "REQUIRED: URL where the scale set is registered (e.g. https://github.com/org/repo)")
	fs.StringVar(&scaleSetName, "name", "", "REQUIRED: Name of the scale set to delete")
	fs.StringVar(&runnerGroup, "runner-group", scaleset.DefaultRunnerGroup, "Name of the runner group the scale set belongs to")
	fs.StringVar(&appClientID, "app-client-id", "", "GitHub App client id")
	fs.Int64Var(&appInstallID, "app-installation-id", 0, "GitHub App installation ID")
	fs.StringVar(&appPrivateKey, "app-private-key", "", "GitHub App private key")
	fs.StringVar(&token, "token", "", "Personal access token (can be used in place of a GitHub App)")
	fs.StringVar(&logLevel, "log-level", "info", "Logging level (debug, info, warn, error)")
	fs.StringVar(&logFormat, "log-format", "text", "Logging format (text, json)")

	fs.Usage = func() {
		fmt.Fprintf(fs.Output(), "Usage: scaleset-ec2-provider delete [options]\n\n")
		fmt.Fprintf(fs.Output(), "Delete a runner scale set from GitHub.\n\nOptions:\n")
		fs.PrintDefaults()
	}

	fs.Parse(args)

	logger := setupLogger(logLevel, logFormat)

	if url == "" {
		fmt.Fprintf(os.Stderr, "error: url is required\n")
		fs.Usage()
		os.Exit(1)
	}

	if scaleSetName == "" {
		fmt.Fprintf(os.Stderr, "error: name is required\n")
		fs.Usage()
		os.Exit(1)
	}

	var githubApp scaleset.GitHubAppAuth
	if appClientID != "" {
		githubApp = scaleset.GitHubAppAuth{
			ClientID:       appClientID,
			InstallationID: appInstallID,
			PrivateKey:     appPrivateKey,
		}
		if err := githubApp.Validate(); err != nil {
			fmt.Fprintf(os.Stderr, "invalid GitHub App credentials: %v\n", err)
			os.Exit(1)
		}
	}

	if githubApp.ClientID == "" && token == "" {
		fmt.Fprintf(os.Stderr, "error: either GitHub App credentials or token is required\n")
		fs.Usage()
		os.Exit(1)
	}

	ctx := context.Background()

	var scalesetClient *scaleset.Client
	var err error

	if githubApp.ClientID != "" {
		scalesetClient, err = scaleset.NewClientWithGitHubApp(
			scaleset.ClientWithGitHubAppConfig{
				GitHubConfigURL: url,
				GitHubAppAuth:   githubApp,
				SystemInfo:      systemInfo(0),
			},
		)
	} else {
		scalesetClient, err = scaleset.NewClientWithPersonalAccessToken(
			scaleset.NewClientWithPersonalAccessTokenConfig{
				GitHubConfigURL:     url,
				PersonalAccessToken: token,
				SystemInfo:          systemInfo(0),
			},
		)
	}

	if err != nil {
		fmt.Fprintf(os.Stderr, "failed to create scaleset client: %v\n", err)
		os.Exit(1)
	}

	var runnerGroupID int
	switch runnerGroup {
	case scaleset.DefaultRunnerGroup:
		runnerGroupID = 1
	default:
		rg, err := scalesetClient.GetRunnerGroupByName(ctx, runnerGroup)
		if err != nil {
			fmt.Fprintf(os.Stderr, "failed to get runner group: %v\n", err)
			os.Exit(1)
		}
		runnerGroupID = rg.ID
	}

	logger.Info("Finding scale set", slog.String("name", scaleSetName), slog.String("runnerGroup", runnerGroup))

	scaleSet, err := scalesetClient.GetRunnerScaleSet(ctx, runnerGroupID, scaleSetName)
	if err != nil {
		fmt.Fprintf(os.Stderr, "failed to get scale set: %v\n", err)
		os.Exit(1)
	}
	if scaleSet == nil {
		fmt.Fprintf(os.Stderr, "scale set %q not found\n", scaleSetName)
		os.Exit(1)
	}

	logger.Info("Deleting scale set", slog.String("name", scaleSetName), slog.Int("id", scaleSet.ID))

	if err := scalesetClient.DeleteRunnerScaleSet(ctx, scaleSet.ID); err != nil {
		fmt.Fprintf(os.Stderr, "failed to delete scale set: %v\n", err)
		os.Exit(1)
	}

	logger.Info("Successfully deleted scale set", slog.String("name", scaleSetName))
}

func setupLogger(logLevel, logFormat string) *slog.Logger {
	var lvl slog.Level
	switch strings.ToLower(logLevel) {
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

	switch logFormat {
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

func splitAndTrim(s string) []string {
	parts := strings.Split(s, ",")
	result := make([]string, 0, len(parts))
	for _, p := range parts {
		if trimmed := strings.TrimSpace(p); trimmed != "" {
			result = append(result, trimmed)
		}
	}
	return result
}

func run(ctx context.Context, c Config) error {
	logger := c.Logger()

	scalesetClient, err := c.ScalesetClient()
	if err != nil {
		return fmt.Errorf("failed to create scaleset client: %w", err)
	}

	var runnerGroupID int
	switch c.RunnerGroup {
	case scaleset.DefaultRunnerGroup:
		runnerGroupID = 1
	default:
		runnerGroup, err := scalesetClient.GetRunnerGroupByName(ctx, c.RunnerGroup)
		if err != nil {
			return fmt.Errorf("failed to get runner group ID: %w", err)
		}
		runnerGroupID = runnerGroup.ID
	}

	scaleSet, err := scalesetClient.CreateRunnerScaleSet(ctx, &scaleset.RunnerScaleSet{
		Name:          c.ScaleSetName,
		RunnerGroupID: runnerGroupID,
		Labels:        c.BuildLabels(),
		RunnerSetting: scaleset.RunnerSetting{
			DisableUpdate: true,
		},
	})
	if err != nil {
		return fmt.Errorf("failed to create runner scale set: %w", err)
	}

	scalesetClient.SetSystemInfo(systemInfo(scaleSet.ID))

	defer func() {
		logger.Info("Deleting runner scale set", slog.Int("scaleSetID", scaleSet.ID))
		if err := scalesetClient.DeleteRunnerScaleSet(context.WithoutCancel(ctx), scaleSet.ID); err != nil {
			logger.Error("Failed to delete runner scale set", slog.Int("scaleSetID", scaleSet.ID), slog.String("error", err.Error()))
		}
	}()

	awsCfg, err := config.LoadDefaultConfig(ctx)
	if err != nil {
		return fmt.Errorf("failed to load AWS config: %w", err)
	}
	ec2Client := &interfaces.RealEC2Client{Client: ec2.NewFromConfig(awsCfg)}

	if c.Region == "" {
		c.Region = awsCfg.Region
	}

	var metricsServer *metrics.Server
	var m *metrics.Metrics
	if c.MetricsPort > 0 {
		m = metrics.New(metrics.MetricsLabels{
			ScaleSetName:  c.ScaleSetName,
			InstanceTypes: strings.Join(c.InstanceTypes, ","),
			Region:        c.Region,
			AMI:           c.AMI,
		}, logger.WithGroup("metrics"))

		metricsServer = metrics.NewServer(c.MetricsPort, m, logger.WithGroup("metrics-server"))
		if err := metricsServer.Start(ctx); err != nil {
			logger.Error("Failed to start metrics server", slog.String("error", err.Error()))
		} else {
			defer metricsServer.Stop(context.WithoutCancel(ctx))
		}
	}

	hostname, err := os.Hostname()
	if err != nil {
		hostname = uuid.NewString()
		logger.Info("Failed to get hostname, fallback to uuid", "uuid", hostname, "error", err)
	}

	sessionClient, err := scalesetClient.MessageSessionClient(ctx, scaleSet.ID, hostname)
	if err != nil {
		return fmt.Errorf("failed to create message session client: %w", err)
	}
	defer sessionClient.Close(context.Background())

	var listenerOpts []listener.Option
	if m != nil {
		listenerOpts = append(listenerOpts, listener.WithMetricsRecorder(m))
	}

	listener, err := listener.New(sessionClient, listener.Config{
		ScaleSetID: scaleSet.ID,
		MaxRunners: c.MaxRunners,
		Logger:     logger.WithGroup("listener"),
	}, listenerOpts...)
	if err != nil {
		return fmt.Errorf("failed to create listener: %w", err)
	}

	scaler := NewScaler(ScalerConfig{
		EC2Client:          ec2Client,
		ScalesetClient:     scalesetClient,
		ScaleSetID:         scaleSet.ID,
		AMI:                c.AMI,
		InstanceTypes:      c.InstanceTypes,
		SubnetID:           c.SubnetID,
		SecurityGroupIDs:   c.SecurityGroupIDs,
		IAMInstanceProfile: c.IAMInstanceProfile,
		KeyName:            c.KeyName,
		UseSpot:            c.UseSpot,
		MinRunners:         c.MinRunners,
		MaxRunners:         c.MaxRunners,
		Logger:             logger.WithGroup("scaler"),
		Metrics:            m,
	})

	defer scaler.shutdown(context.WithoutCancel(ctx))

	logger.Info("Starting listener")
	if err := listener.Run(ctx, scaler); !errors.Is(err, context.Canceled) {
		return fmt.Errorf("listener run failed: %w", err)
	}
	return nil
}

func systemInfo(scaleSetID int) scaleset.SystemInfo {
	return scaleset.SystemInfo{
		System:     "scaleset-ec2-provider",
		Subsystem:  "scaleset-ec2-provider",
		CommitSHA:  "NA",
		Version:    version,
		ScaleSetID: scaleSetID,
	}
}
