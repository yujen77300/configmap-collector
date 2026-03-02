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
	"strings"
	"sync"
	"sync/atomic"
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
	namespace string
	appLabel  string
	keepLast  int
	keepDays  int
	dryRun    bool
	logLevel  string
	logFormat string
}

func main() {
	flags := &cliFlags{}

	rootCmd := &cobra.Command{
		Use:   "cm-gc",
		Short: "ConfigMap garbage collector for Argo Rollouts Helm checksum versioning",
		Long: `cm-gc removes stale versioned ConfigMaps that accumulate when Helm generates
immutable ConfigMaps with pattern {app}-config-{hash8}.

Dry-run is enabled by default. Set --dry-run=false (or DRY_RUN=false) to perform
actual deletions.

Multiple namespaces can be specified as a comma-separated string:
  --namespace=mwpcloud,staging-ns,prod-ns
or via the NAMESPACE environment variable.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			return run(cmd, flags)
		},
		SilenceUsage: true,
	}

	// Register flags; all have env-var equivalents loaded via Viper in config.Load().
	// --namespace accepts a comma-separated list: "mwpcloud,staging-ns,prod-ns"
	rootCmd.Flags().StringVar(&flags.namespace, "namespace", "", "Comma-separated target namespaces (env: NAMESPACE, default: mwpcloud)")
	rootCmd.Flags().StringVar(&flags.appLabel, "app-label", "", "App label value to match (env: APP_LABEL, default: xzk0-seat)")
	rootCmd.Flags().IntVar(&flags.keepLast, "keep-last", 0, "Keep N newest ConfigMaps regardless of age (env: KEEP_LAST, default: 5)")
	rootCmd.Flags().IntVar(&flags.keepDays, "keep-days", 0, "Keep ConfigMaps newer than N days (env: KEEP_DAYS, default: 7)")
	rootCmd.Flags().BoolVar(&flags.dryRun, "dry-run", true, "Log actions without deleting (env: DRY_RUN, default: true)")
	rootCmd.Flags().StringVar(&flags.logLevel, "log-level", "", "Log level: debug|info|warn|error (env: LOG_LEVEL, default: info)")
	rootCmd.Flags().StringVar(&flags.logFormat, "log-format", "", "Log format: text|json (env: LOG_FORMAT, default: text)")

	if err := rootCmd.Execute(); err != nil {
		os.Exit(1)
	}
}

// run is the main execution logic, separated from main() for testability.
func run(cmd *cobra.Command, flags *cliFlags) error {
	// 1. Load config from env/defaults, then override with CLI flags.
	cfg, err := config.Load()
	if err != nil {
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
	logger.Info("starting cm-gc",
		zap.Strings("namespaces", cfg.Namespaces),
		zap.String("app_label", cfg.AppLabel),
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
	cmClient := k8s.NewKubeConfigMapClient(clients.Kube)
	rsClient := k8s.NewKubeReplicaSetClient(clients.Kube)
	rolloutClient := k8s.NewKubeRolloutClient(clients.Rollout)

	// 5. Process each namespace concurrently.
	// anyFailed is set to 1 if any namespace encounters a partial deletion error.
	var anyFailed atomic.Int32
	var wg sync.WaitGroup

	for _, ns := range cfg.Namespaces {
		wg.Add(1)
		go func(ns string) {
			defer wg.Done()
			nsLogger := logger.With(zap.String("namespace", ns))
			if failed := runForNamespace(ctx, ns, cfg, cmClient, rsClient, rolloutClient, nsLogger); failed {
				anyFailed.Store(1)
			}
		}(ns)
	}

	wg.Wait()

	if anyFailed.Load() != 0 {
		os.Exit(2)
	}
	return nil
}

// runForNamespace executes the full GC cycle for a single namespace:
//  1. List all Rollouts → auto-derive ConfigMap prefix per Rollout
//  2. For each Rollout: list prefix-matched CMs → resolve in-use checksums →
//     plan → delete (or dry-run log).
//
// Returns true if any deletion failed (caller should exit with code 2).
func runForNamespace(
	ctx context.Context,
	ns string,
	cfg *config.Config,
	cmClient k8s.ConfigMapClient,
	rsClient *k8s.KubeReplicaSetClient,
	rolloutClient *k8s.KubeRolloutClient,
	logger *zap.Logger,
) (anyFailed bool) {
	// 5. Discover all Rollout names in the namespace.
	// Each Rollout "foo" manages ConfigMaps with prefix "foo-config-".
	rolloutNames, err := rolloutClient.ListRolloutNames(ctx, ns)
	if err != nil {
		logger.Error("failed to list rollouts", zap.Error(err))
		return true
	}
	logger.Info("discovered rollouts",
		zap.Strings("rollouts", rolloutNames),
	)

	if len(rolloutNames) == 0 {
		logger.Info("no rollouts found in namespace — nothing to do")
		return false
	}

	// 6. Resolve in-use checksums once for all Rollout-owned ReplicaSets
	// in the namespace (a single API call covers all Rollouts).
	resolver := k8s.NewInUseResolver(rsClient)
	checksums, err := resolver.Resolve(ctx, ns)
	if err != nil {
		logger.Error("failed to resolve in-use checksums", zap.Error(err))
		return true
	}
	logger.Info("resolved in-use checksums from rollout replicasets",
		zap.Int("count", len(checksums)),
		zap.Strings("checksums", mapKeys(checksums)),
	)

	// 7. Process each Rollout independently using its auto-derived prefix.
	for _, rolloutName := range rolloutNames {
		prefix := rolloutName + "-config-"
		rolloutLogger := logger.With(zap.String("rollout", rolloutName), zap.String("prefix", prefix))
		if failed := runForRollout(ctx, ns, prefix, cfg, cmClient, checksums, rolloutLogger); failed {
			anyFailed = true
		}
	}
	return anyFailed
}

// runForRollout runs the GC cycle for a single Rollout within a namespace,
// using the pre-resolved checksum set shared across all Rollouts.
func runForRollout(
	ctx context.Context,
	ns string,
	prefix string,
	cfg *config.Config,
	cmClient k8s.ConfigMapClient,
	checksums map[string]bool,
	logger *zap.Logger,
) (anyFailed bool) {
	// List only ConfigMaps matching this Rollout's auto-derived prefix.
	candidateCMs, err := cmClient.ListConfigMaps(ctx, ns, prefix)
	if err != nil {
		logger.Error("failed to list configmaps")
		return true
	}
	logger.Info("discovered configmaps matching prefix",
		zap.Int("count", len(candidateCMs)),
	)

	if len(candidateCMs) == 0 {
		logger.Info("no configmaps found matching prefix — nothing to do")
		return false
	}

	// Build the inUse set (keyed by full CM name) for this Rollout's candidates.
	inUse := make(map[string]bool)
	for _, cm := range candidateCMs {
		for checksum := range checksums {
			if strings.Contains(cm.Name, checksum) {
				inUse[cm.Name] = true
				break
			}
		}
	}
	logger.Debug("in-use configmaps (referenced by replicasets)",
		zap.Strings("configmaps", mapKeys(inUse)),
	)

	// Build planner candidates.
	candidates := make([]planner.ConfigMapCandidate, 0, len(candidateCMs))
	for _, cm := range candidateCMs {
		candidates = append(candidates, planner.ConfigMapCandidate{
			Name:              cm.Name,
			CreationTimestamp: cm.CreationTimestamp.Time,
			Annotations:       cm.Annotations,
		})
	}

	toDelete := planner.Plan(candidates, inUse, cfg.KeepLast, cfg.KeepDays, time.Now())
	logger.Info("planner result",
		zap.Int("candidates_for_deletion", len(toDelete)),
	)

	if len(toDelete) == 0 {
		logger.Info("no configmaps eligible for deletion — done")
		return false
	}

	// Log all candidates before acting.
	now := time.Now()
	for _, name := range toDelete {
		var age time.Duration
		for _, cm := range candidateCMs {
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

	// Execute deletions (skipped in dry-run mode).
	if cfg.DryRun {
		logger.Info("[DRY-RUN] completed — no deletions performed",
			zap.Int("would_delete", len(toDelete)),
		)
		return false
	}

	deleted := 0
	for _, name := range toDelete {
		if err := cmClient.DeleteConfigMap(ctx, ns, name); err != nil {
			logger.Error("failed to delete configmap",
				zap.String("configmap", name),
				zap.Error(err),
			)
			anyFailed = true
			continue
		}
		logger.Info("deleted configmap", zap.String("configmap", name))
		deleted++
	}

	logger.Info("gc completed",
		zap.Int("deleted", deleted),
		zap.Int("failed", len(toDelete)-deleted),
	)
	return anyFailed
}

// applyFlagOverrides replaces cfg values with any CLI flags that were explicitly
// set (non-zero / non-empty), so that flags always win over env vars / defaults.
func applyFlagOverrides(cmd *cobra.Command, flags *cliFlags, cfg *config.Config) {
	if cmd.Flags().Changed("namespace") {
		// Re-parse the comma-separated string through the same logic as config.Load().
		cfg.Namespaces = config.ParseNamespaces(flags.namespace, cfg.Namespaces[0])
	}
	if cmd.Flags().Changed("app-label") {
		cfg.AppLabel = flags.appLabel
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
