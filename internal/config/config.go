package config

import "github.com/spf13/viper"

// Config holds all configuration parameters for the ConfigMap GC.
type Config struct {
	Namespace  string
	AppLabel   string
	NamePrefix string
	KeepLast   int
	KeepDays   int
	DryRun     bool
	LogLevel   string
	LogFormat  string
}

// Load reads configuration from environment variables with fallback to defaults.
// Priority: environment variables > default values.
// A fresh viper.Viper instance is created on every call to avoid global state
// leakage between calls.
func Load() (*Config, error) {
	v := viper.New()

	// Set defaults
	v.SetDefault("NAMESPACE", "mwpcloud")
	v.SetDefault("APP_LABEL", "xzk0-seat")
	v.SetDefault("NAME_PREFIX", "xzk0-seat-config-")
	v.SetDefault("KEEP_LAST", 5)
	v.SetDefault("KEEP_DAYS", 7)
	v.SetDefault("DRY_RUN", true)
	v.SetDefault("LOG_LEVEL", "info")
	v.SetDefault("LOG_FORMAT", "text")

	v.AutomaticEnv()

	return &Config{
		Namespace:  v.GetString("NAMESPACE"),
		AppLabel:   v.GetString("APP_LABEL"),
		NamePrefix: v.GetString("NAME_PREFIX"),
		KeepLast:   v.GetInt("KEEP_LAST"),
		KeepDays:   v.GetInt("KEEP_DAYS"),
		DryRun:     v.GetBool("DRY_RUN"),
		LogLevel:   v.GetString("LOG_LEVEL"),
		LogFormat:  v.GetString("LOG_FORMAT"),
	}, nil
}
