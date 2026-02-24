package main

// cmd/cm-gc/main.go — CLI entry point for the ConfigMap GC tool.
// Wires configuration, Kubernetes clients, in-use resolver, planner, and
// structured logging (uber-go/zap) together via a Cobra root command.
//
// Flags override environment variables; environment variables override defaults.
// See internal/config/config.go for default values.

import (
	"context"
	"os"
	"time"

	"github.com/spf13/cobra"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"

	"github.com/yujen77300/configmap-collector/internal/config"
	"github.com/yujen77300/configmap-collector/internal/k8s"
	"github.com/yujen77300/configmap-collector/internal/planner"
)

// cliFlags mirrors config.Config so that Cobra flag values can override the
// values that Viper loads from the environment.
type cliFlags struct {
	namespace   string
	appLabel    string
	namePrefix  string
	keepLast    int
	keepDays    int
	dryRun      bool
	logLevel    string
	logFormat   string
	rolloutName string
}

func main() {
	flags := &cliFlags{}

	rootCmd := &cobra.Command{
		Use:   "cm-gc",
		Short: "ConfigMap garbage collector for Argo Rollouts Helm checksum versioning",
		Long: `cm-gc removes stale versioned ConfigMaps that accumulate when Helm generates
immutable ConfigMaps with pattern {app}-config-{hash8}.

Dry-run is enabled by default. Set --dry-run=false (or DRY_RUN=false) to perform
actual deletions.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			return run(cmd, flags)
		},
		SilenceUsage: true,
	}

	// Register flags; all have env-var equivalents loaded via Viper in config.Load().
	rootCmd.Flags().StringVar(&flags.namespace, "namespace", "", "Target namespace (env: NAMESPACE, default: mwpcloud)")
	rootCmd.Flags().StringVar(&flags.appLabel, "app-label", "", "App label value to match (env: APP_LABEL, default: xzk0-seat)")
	rootCmd.Flags().StringVar(&flags.namePrefix, "name-prefix", "", "ConfigMap name prefix to filter (env: NAME_PREFIX, default: xzk0-seat-config-)")
	rootCmd.Flags().IntVar(&flags.keepLast, "keep-last", 0, "Keep N newest ConfigMaps regardless of age (env: KEEP_LAST, default: 5)")
	rootCmd.Flags().IntVar(&flags.keepDays, "keep-days", 0, "Keep ConfigMaps newer than N days (env: KEEP_DAYS, default: 7)")
	rootCmd.Flags().BoolVar(&flags.dryRun, "dry-run", true, "Log actions without deleting (env: DRY_RUN, default: true)")
	rootCmd.Flags().StringVar(&flags.logLevel, "log-level", "", "Log level: debug|info|warn|error (env: LOG_LEVEL, default: info)")
	rootCmd.Flags().StringVar(&flags.logFormat, "log-format", "", "Log format: text|json (env: LOG_FORMAT, default: text)")
	rootCmd.Flags().StringVar(&flags.rolloutName, "rollout-name", "", "Argo Rollout name (env: ROLLOUT_NAME, default: same as app-label)")

	if err := rootCmd.Execute(); err != nil {
		os.Exit(1)
	}
}

// run is the main execution logic, separated from main() for testability.
func run(cmd *cobra.Command, flags *cliFlags) error {
	// 1. Load config from env/defaults, then override with CLI flags.
	cfg, err := config.Load()
	if err != nil {
		// Logger not ready yet — use stderr directly.
		cmd.PrintErrf("failed to load config: %v\n", err)
		os.Exit(1)
	}
	applyFlagOverrides(cmd, flags, cfg)

	// 2. Build structured logger.
	logger, err := buildLogger(cfg.LogLevel, cfg.LogFormat)
	if err != nil {
		cmd.PrintErrf("failed to build logger: %v\n", err)
		os.Exit(1)
	}
	defer logger.Sync() //nolint:errcheck

	// 3. Log startup configuration.
	rolloutName := cfg.AppLabel // default: rollout name == app label
	if flags.rolloutName != "" {
		rolloutName = flags.rolloutName
	}

	logger.Info("starting cm-gc",
		zap.String("namespace", cfg.Namespace),
		zap.String("app_label", cfg.AppLabel),
		zap.String("name_prefix", cfg.NamePrefix),
		zap.String("rollout_name", rolloutName),
		zap.Int("keep_last", cfg.KeepLast),
		zap.Int("keep_days", cfg.KeepDays),
		zap.Bool("dry_run", cfg.DryRun),
		zap.String("log_level", cfg.LogLevel),
		zap.String("log_format", cfg.LogFormat),
	)

	if cfg.DryRun {
		logger.Info("[DRY-RUN] mode enabled — no ConfigMaps will be deleted")
	}

	// 4. Initialise Kubernetes clients.
	clients, err := k8s.NewClients()
	if err != nil {
		logger.Error("failed to initialise kubernetes clients", zap.Error(err))
		os.Exit(1)
	}

	ctx := context.Background()

	// 5. Discover ConfigMaps matching the name prefix.
	cmClient := k8s.NewKubeConfigMapClient(clients.Kube)
	allCMs, err := cmClient.ListConfigMaps(ctx, cfg.Namespace, cfg.NamePrefix)
	if err != nil {
		logger.Error("failed to list configmaps", zap.Error(err))
		os.Exit(1)
	}
	logger.Info("discovered configmaps",
		zap.Int("total", len(allCMs)),
		zap.String("prefix", cfg.NamePrefix),
	)

	if len(allCMs) == 0 {
		logger.Info("no configmaps found matching prefix — nothing to do")
		return nil
	}

	// 6. Resolve in-use ConfigMaps from ReplicaSets owned by the Rollout.
	rsClient := k8s.NewKubeReplicaSetClient(clients.Kube)
	resolver := k8s.NewInUseResolver(rsClient, cfg.NamePrefix)
	inUse, err := resolver.Resolve(ctx, cfg.Namespace, rolloutName)
	if err != nil {
		logger.Error("failed to resolve in-use configmaps", zap.Error(err))
		os.Exit(1)
	}
	logger.Info("resolved in-use configmaps",
		zap.Int("count", len(inUse)),
		zap.Strings("names", mapKeys(inUse)),
	)

	// 7. Build planner candidates from the discovered ConfigMaps.
	candidates := make([]planner.ConfigMapCandidate, 0, len(allCMs))
	for _, cm := range allCMs {
		candidates = append(candidates, planner.ConfigMapCandidate{
			Name:              cm.Name,
			CreationTimestamp: cm.CreationTimestamp.Time,
			Annotations:       cm.Annotations,
		})
	}

	// 8. Run the deletion planner.
	toDelete := planner.Plan(candidates, inUse, cfg.KeepLast, cfg.KeepDays, time.Now())
	logger.Info("planner result",
		zap.Int("candidates_for_deletion", len(toDelete)),
	)

	if len(toDelete) == 0 {
		logger.Info("no configmaps eligible for deletion — done")
		return nil
	}

	// 9. Log all candidates before acting.
	now := time.Now()
	for _, name := range toDelete {
		var age time.Duration
		for _, cm := range allCMs {
			if cm.Name == name {
				age = now.Sub(cm.CreationTimestamp.Time)
				break
			}
		}
		ageDays := int(age.Hours() / 24)
		if cfg.DryRun {
			logger.Info("[DRY-RUN] would delete configmap",
				zap.String("configmap", name),
				zap.Int("age_days", ageDays),
				zap.String("reason", "not in-use, not in keep-last, older than keep-days"),
			)
		} else {
			logger.Info("deleting configmap",
				zap.String("configmap", name),
				zap.Int("age_days", ageDays),
			)
		}
	}

	// 10. Execute deletions (skipped in dry-run mode).
	if cfg.DryRun {
		logger.Info("[DRY-RUN] completed — no deletions performed",
			zap.Int("would_delete", len(toDelete)),
		)
		return nil
	}

	exitCode := 0
	deleted := 0
	for _, name := range toDelete {
		if err := cmClient.DeleteConfigMap(ctx, cfg.Namespace, name); err != nil {
			logger.Error("failed to delete configmap",
				zap.String("configmap", name),
				zap.Error(err),
			)
			exitCode = 2
			continue
		}
		logger.Info("deleted configmap", zap.String("configmap", name))
		deleted++
	}

	logger.Info("gc completed",
		zap.Int("deleted", deleted),
		zap.Int("failed", len(toDelete)-deleted),
	)

	if exitCode != 0 {
		os.Exit(exitCode)
	}
	return nil
}

// applyFlagOverrides replaces cfg values with any CLI flags that were explicitly
// set (non-zero / non-empty), so that flags always win over env vars / defaults.
func applyFlagOverrides(cmd *cobra.Command, flags *cliFlags, cfg *config.Config) {
	if cmd.Flags().Changed("namespace") {
		cfg.Namespace = flags.namespace
	}
	if cmd.Flags().Changed("app-label") {
		cfg.AppLabel = flags.appLabel
	}
	if cmd.Flags().Changed("name-prefix") {
		cfg.NamePrefix = flags.namePrefix
	}
	if cmd.Flags().Changed("keep-last") {
		cfg.KeepLast = flags.keepLast
	}
	if cmd.Flags().Changed("keep-days") {
		cfg.KeepDays = flags.keepDays
	}
	if cmd.Flags().Changed("dry-run") {
		cfg.DryRun = flags.dryRun
	}
	if cmd.Flags().Changed("log-level") {
		cfg.LogLevel = flags.logLevel
	}
	if cmd.Flags().Changed("log-format") {
		cfg.LogFormat = flags.logFormat
	}
}

// buildLogger creates a zap.Logger configured for the given level and format.
// format="json" produces structured JSON; anything else uses the human-friendly
// console encoder.
func buildLogger(level, format string) (*zap.Logger, error) {
	var zapLevel zapcore.Level
	if err := zapLevel.UnmarshalText([]byte(level)); err != nil {
		zapLevel = zapcore.InfoLevel
	}

	var zapCfg zap.Config
	if format == "json" {
		zapCfg = zap.NewProductionConfig()
	} else {
		zapCfg = zap.NewDevelopmentConfig()
		zapCfg.EncoderConfig.EncodeLevel = zapcore.CapitalColorLevelEncoder
	}
	zapCfg.Level = zap.NewAtomicLevelAt(zapLevel)

	return zapCfg.Build()
}

// mapKeys returns the keys of a map[string]bool as a sorted slice — used for
// deterministic log output.
func mapKeys(m map[string]bool) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	return keys
}
