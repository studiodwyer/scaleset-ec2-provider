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
	var (
		configPath  string
		logLevel    string
		logFormat   string
		showVersion bool
	)

	fs := flag.NewFlagSet("run", flag.ExitOnError)
	fs.StringVar(&configPath, "config", "", "REQUIRED: Path to TOML config file")
	fs.StringVar(&logLevel, "log-level", "", "Override config log_level (debug, info, warn, error)")
	fs.StringVar(&logFormat, "log-format", "", "Override config log_format (text, json)")
	fs.BoolVar(&showVersion, "version", false, "Print version information")

	fs.Usage = func() {
		fmt.Fprintf(fs.Output(), "Usage: scaleset-ec2-provider run --config <path> [options]\n\n")
		fmt.Fprintf(fs.Output(), "Scale GitHub Actions runners using ephemeral EC2 instances.\n\n")
		fmt.Fprintf(fs.Output(), "Configuration is read from the TOML file specified by --config.\n")
		fmt.Fprintf(fs.Output(), "Flags override their counterparts in the config file.\n\nOptions:\n")
		fs.PrintDefaults()
	}

	fs.Parse(args)

	if showVersion {
		fmt.Printf("%s (commit: %s, built: %s)\n", version, commit, date)
		os.Exit(0)
	}

	if configPath == "" {
		fmt.Fprintln(os.Stderr, "error: --config is required")
		fs.Usage()
		os.Exit(1)
	}

	cfg, err := LoadConfig(configPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "failed to load config: %v\n", err)
		os.Exit(1)
	}

	if logLevel != "" {
		cfg.LogLevel = logLevel
	}
	if logFormat != "" {
		cfg.LogFormat = logFormat
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
		configPath    string
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
	fs.StringVar(&configPath, "config", "", "Path to TOML config file (optional; provides defaults for the values below)")
	fs.StringVar(&url, "url", "", "URL where the scale set is registered (e.g. https://github.com/org/repo)")
	fs.StringVar(&scaleSetName, "name", "", "Name of the scale set to delete")
	fs.StringVar(&runnerGroup, "runner-group", "", "Name of the runner group the scale set belongs to (default: default runner group)")
	fs.StringVar(&appClientID, "app-client-id", "", "GitHub App client id")
	fs.Int64Var(&appInstallID, "app-installation-id", 0, "GitHub App installation ID")
	fs.StringVar(&appPrivateKey, "app-private-key", "", "GitHub App private key")
	fs.StringVar(&token, "token", "", "Personal access token (can be used in place of a GitHub App)")
	fs.StringVar(&logLevel, "log-level", "", "Logging level (debug, info, warn, error)")
	fs.StringVar(&logFormat, "log-format", "", "Logging format (text, json)")

	fs.Usage = func() {
		fmt.Fprintf(fs.Output(), "Usage: scaleset-ec2-provider delete [--config <path>] [options]\n\n")
		fmt.Fprintf(fs.Output(), "Delete a runner scale set from GitHub.\n\n")
		fmt.Fprintf(fs.Output(), "When --config is provided, its values are used as defaults;\n")
		fmt.Fprintf(fs.Output(), "explicitly-set flags override the config file.\n\nOptions:\n")
		fs.PrintDefaults()
	}

	fs.Parse(args)

	setFlags := map[string]struct{}{}
	fs.Visit(func(f *flag.Flag) { setFlags[f.Name] = struct{}{} })

	var urlVal, nameVal, runnerGroupVal, appClientIDVal, appPrivateKeyVal, tokenVal, logLevelVal, logFormatVal string
	var appInstallIDVal int64

	if configPath != "" {
		cfg, err := LoadConfig(configPath)
		if err != nil {
			fmt.Fprintf(os.Stderr, "failed to load config: %v\n", err)
			os.Exit(1)
		}
		urlVal = cfg.RegistrationURL
		nameVal = cfg.ScaleSetName
		runnerGroupVal = cfg.RunnerGroup
		appClientIDVal = cfg.GitHubApp.ClientID
		appInstallIDVal = cfg.GitHubApp.InstallationID
		appPrivateKeyVal = cfg.GitHubApp.PrivateKey
		tokenVal = cfg.Token
		logLevelVal = cfg.LogLevel
		logFormatVal = cfg.LogFormat
	}

	if _, ok := setFlags["url"]; ok {
		urlVal = url
	}
	if _, ok := setFlags["name"]; ok {
		nameVal = scaleSetName
	}
	if _, ok := setFlags["runner-group"]; ok {
		runnerGroupVal = runnerGroup
	}
	if _, ok := setFlags["app-client-id"]; ok {
		appClientIDVal = appClientID
	}
	if _, ok := setFlags["app-installation-id"]; ok {
		appInstallIDVal = appInstallID
	}
	if _, ok := setFlags["app-private-key"]; ok {
		appPrivateKeyVal = appPrivateKey
	}
	if _, ok := setFlags["token"]; ok {
		tokenVal = token
	}
	if _, ok := setFlags["log-level"]; ok {
		logLevelVal = logLevel
	}
	if _, ok := setFlags["log-format"]; ok {
		logFormatVal = logFormat
	}

	if runnerGroupVal == "" {
		runnerGroupVal = scaleset.DefaultRunnerGroup
	}
	if logLevelVal == "" {
		logLevelVal = "info"
	}
	if logFormatVal == "" {
		logFormatVal = "text"
	}

	logger := setupLogger(logLevelVal, logFormatVal)

	if urlVal == "" {
		fmt.Fprintf(os.Stderr, "error: url is required\n")
		fs.Usage()
		os.Exit(1)
	}

	if nameVal == "" {
		fmt.Fprintf(os.Stderr, "error: name is required\n")
		fs.Usage()
		os.Exit(1)
	}

	var githubApp scaleset.GitHubAppAuth
	if appClientIDVal != "" {
		githubApp = scaleset.GitHubAppAuth{
			ClientID:       appClientIDVal,
			InstallationID: appInstallIDVal,
			PrivateKey:     appPrivateKeyVal,
		}
		if err := githubApp.Validate(); err != nil {
			fmt.Fprintf(os.Stderr, "invalid GitHub App credentials: %v\n", err)
			os.Exit(1)
		}
	}

	if githubApp.ClientID == "" && tokenVal == "" {
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
				GitHubConfigURL: urlVal,
				GitHubAppAuth:   githubApp,
				SystemInfo:      systemInfo(0),
			},
		)
	} else {
		scalesetClient, err = scaleset.NewClientWithPersonalAccessToken(
			scaleset.NewClientWithPersonalAccessTokenConfig{
				GitHubConfigURL:     urlVal,
				PersonalAccessToken: tokenVal,
				SystemInfo:          systemInfo(0),
			},
		)
	}

	if err != nil {
		fmt.Fprintf(os.Stderr, "failed to create scaleset client: %v\n", err)
		os.Exit(1)
	}

	var runnerGroupID int
	switch runnerGroupVal {
	case scaleset.DefaultRunnerGroup:
		runnerGroupID = 1
	default:
		rg, err := scalesetClient.GetRunnerGroupByName(ctx, runnerGroupVal)
		if err != nil {
			fmt.Fprintf(os.Stderr, "failed to get runner group: %v\n", err)
			os.Exit(1)
		}
		runnerGroupID = rg.ID
	}

	logger.Info("Finding scale set", slog.String("name", nameVal), slog.String("runnerGroup", runnerGroupVal))

	scaleSet, err := scalesetClient.GetRunnerScaleSet(ctx, runnerGroupID, nameVal)
	if err != nil {
		fmt.Fprintf(os.Stderr, "failed to get scale set: %v\n", err)
		os.Exit(1)
	}
	if scaleSet == nil {
		fmt.Fprintf(os.Stderr, "scale set %q not found\n", nameVal)
		os.Exit(1)
	}

	logger.Info("Deleting scale set", slog.String("name", nameVal), slog.Int("id", scaleSet.ID))

	if err := scalesetClient.DeleteRunnerScaleSet(ctx, scaleSet.ID); err != nil {
		fmt.Fprintf(os.Stderr, "failed to delete scale set: %v\n", err)
		os.Exit(1)
	}

	logger.Info("Successfully deleted scale set", slog.String("name", nameVal))
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
