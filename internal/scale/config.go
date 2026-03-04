package scale

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"time"

	"sigs.k8s.io/yaml"
)

// configFile is the YAML-shaped struct used only for parsing.
// Duration fields are kept as strings (e.g. "30s", "5m") because
// sigs.k8s.io/yaml goes through JSON, which cannot unmarshal duration strings
// into time.Duration (int64) directly.
type configFile struct {
	MinInstances int             `yaml:"minInstances"`
	MaxInstances int             `yaml:"maxInstances"`
	Metrics      []MetricSpec    `yaml:"metrics"`
	Aggregation  AggregationType `yaml:"aggregation"`
	Cooldown     string          `yaml:"cooldown"`
	PollInterval string          `yaml:"pollInterval"`
	Prediction   *predictionFile `yaml:"prediction,omitempty"`
}

type predictionFile struct {
	Enabled            bool   `yaml:"enabled"`
	MetricName         string `yaml:"metricName"`
	Horizon            string `yaml:"horizon"`
	MinHistoryDuration string `yaml:"minHistoryDuration"`
}

// LoadConfig reads and parses a YAML config file into Config.
func LoadConfig(path string) (Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return Config{}, fmt.Errorf("read config file: %w", err)
	}

	var raw configFile
	if err := yaml.Unmarshal(data, &raw); err != nil {
		return Config{}, fmt.Errorf("parse config file: %w", err)
	}

	cfg, err := convertConfig(raw)
	if err != nil {
		return Config{}, fmt.Errorf("invalid config: %w", err)
	}

	return cfg, nil
}

// convertConfig translates configFile into Config, parsing duration strings.
func convertConfig(raw configFile) (Config, error) {
	cfg := Config{
		MinInstances: raw.MinInstances,
		MaxInstances: raw.MaxInstances,
		Metrics:      raw.Metrics,
		Aggregation:  raw.Aggregation,
	}

	if raw.PollInterval == "" {
		return Config{}, fmt.Errorf("pollInterval is required")
	}
	pollInterval, err := time.ParseDuration(raw.PollInterval)
	if err != nil {
		return Config{}, fmt.Errorf("pollInterval %q: %w", raw.PollInterval, err)
	}
	cfg.PollInterval = pollInterval

	if raw.Cooldown == "" {
		return Config{}, fmt.Errorf("cooldown is required")
	}
	cooldown, err := time.ParseDuration(raw.Cooldown)
	if err != nil {
		return Config{}, fmt.Errorf("cooldown %q: %w", raw.Cooldown, err)
	}
	cfg.Cooldown = cooldown

	if raw.Prediction != nil {
		p := &PredictionConfig{
			Enabled:    raw.Prediction.Enabled,
			MetricName: raw.Prediction.MetricName,
		}
		if raw.Prediction.Horizon != "" {
			if p.Horizon, err = time.ParseDuration(raw.Prediction.Horizon); err != nil {
				return Config{}, fmt.Errorf("prediction.horizon %q: %w", raw.Prediction.Horizon, err)
			}
		}
		if raw.Prediction.MinHistoryDuration != "" {
			if p.MinHistoryDuration, err = time.ParseDuration(raw.Prediction.MinHistoryDuration); err != nil {
				return Config{}, fmt.Errorf("prediction.minHistoryDuration %q: %w", raw.Prediction.MinHistoryDuration, err)
			}
		}
		cfg.Prediction = p
	}

	return cfg, validateConfig(cfg)
}

// WatchConfig polls the config file every interval and calls onChange when it changes.
// Blocks until ctx is cancelled.
func WatchConfig(ctx context.Context, path string, interval time.Duration, onChange func(Config)) {
	var lastMod time.Time

	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			info, err := os.Stat(path)
			if err != nil {
				slog.Warn("config watch: stat failed", "path", path, "err", err)
				continue
			}

			if info.ModTime().Equal(lastMod) {
				continue
			}

			cfg, err := LoadConfig(path)
			if err != nil {
				slog.Warn("config watch: reload failed", "path", path, "err", err)
				continue
			}

			lastMod = info.ModTime()
			slog.Info("config reloaded", "path", path)
			onChange(cfg)
		}
	}
}

func validateConfig(cfg Config) error {
	if cfg.MinInstances < 1 {
		return fmt.Errorf("minInstances must be >= 1, got %d", cfg.MinInstances)
	}
	if cfg.MaxInstances < cfg.MinInstances {
		return fmt.Errorf("maxInstances (%d) must be >= minInstances (%d)", cfg.MaxInstances, cfg.MinInstances)
	}
	if cfg.PollInterval <= 0 {
		return fmt.Errorf("pollInterval must be > 0")
	}
	for i, m := range cfg.Metrics {
		if m.Name == "" {
			return fmt.Errorf("metrics[%d]: name is required", i)
		}
		if m.Query == "" {
			return fmt.Errorf("metrics[%d] %q: query is required", i, m.Name)
		}
	}
	if cfg.Prediction != nil && cfg.Prediction.Enabled {
		if cfg.Prediction.MetricName == "" {
			return fmt.Errorf("prediction.metricName is required when prediction is enabled")
		}
		if cfg.Prediction.Horizon <= 0 {
			return fmt.Errorf("prediction.horizon must be > 0")
		}
	}
	return nil
}
