package config

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

// allEnvKeys lists every env key Load() reads.
// Used to guarantee a clean slate before each table-driven case.
var allEnvKeys = []string{
	"NAMESPACE", "APP_LABEL", "NAME_PREFIX",
	"KEEP_LAST", "KEEP_DAYS", "DRY_RUN",
	"LOG_LEVEL", "LOG_FORMAT",
}

func TestLoad(t *testing.T) {
	tests := []struct {
		name     string
		envVars  map[string]string
		expected Config
	}{
		{
			name:    "all defaults when no env vars set",
			envVars: map[string]string{},
			expected: Config{
				Namespace:  "mwpcloud",
				AppLabel:   "xzk0-seat",
				NamePrefix: "xzk0-seat-config-",
				KeepLast:   5,
				KeepDays:   7,
				DryRun:     true,
				LogLevel:   "info",
				LogFormat:  "text",
			},
		},
		{
			name: "all fields overridden by env vars",
			envVars: map[string]string{
				"NAMESPACE":   "production",
				"APP_LABEL":   "my-app",
				"NAME_PREFIX": "my-app-config-",
				"KEEP_LAST":   "3",
				"KEEP_DAYS":   "14",
				"DRY_RUN":     "false",
				"LOG_LEVEL":   "debug",
				"LOG_FORMAT":  "json",
			},
			expected: Config{
				Namespace:  "production",
				AppLabel:   "my-app",
				NamePrefix: "my-app-config-",
				KeepLast:   3,
				KeepDays:   14,
				DryRun:     false,
				LogLevel:   "debug",
				LogFormat:  "json",
			},
		},
		{
			name: "partial override: only NAMESPACE set, rest are defaults",
			envVars: map[string]string{
				"NAMESPACE": "staging",
			},
			expected: Config{
				Namespace:  "staging",
				AppLabel:   "xzk0-seat",
				NamePrefix: "xzk0-seat-config-",
				KeepLast:   5,
				KeepDays:   7,
				DryRun:     true,
				LogLevel:   "info",
				LogFormat:  "text",
			},
		},
		{
			name: "DRY_RUN explicitly true",
			envVars: map[string]string{
				"DRY_RUN": "true",
			},
			expected: Config{
				Namespace:  "mwpcloud",
				AppLabel:   "xzk0-seat",
				NamePrefix: "xzk0-seat-config-",
				KeepLast:   5,
				KeepDays:   7,
				DryRun:     true,
				LogLevel:   "info",
				LogFormat:  "text",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Clear all env keys first to avoid cross-case pollution from
			for _, key := range allEnvKeys {
				t.Setenv(key, "")
			}
			// Apply this case's env vars
			for k, v := range tt.envVars {
				t.Setenv(k, v)
			}

			cfg, err := Load()

			assert.NoError(t, err)
			assert.Equal(t, tt.expected, *cfg)
		})
	}
}
