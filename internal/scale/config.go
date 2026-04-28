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
	MinInstances                 int             `yaml:"minInstances"`
	MaxInstances                 int             `yaml:"maxInstances"`
	Metrics                      []MetricSpec    `yaml:"metrics"`
	Aggregation                  AggregationType `yaml:"aggregation"`
	Cooldown                     string          `yaml:"cooldown"`
	PollInterval                 string          `yaml:"pollInterval"`
	Prediction                   *predictionFile `yaml:"prediction,omitempty"`
	ScalingMode                  string          `yaml:"scalingMode"`
	ScaleDownStabilizationWindow string          `yaml:"scaleDownStabilizationWindow"`
}

type predictionFile struct {
	Enabled            bool                 `yaml:"enabled"`
	Type               string               `yaml:"type"`
	MetricName         string               `yaml:"metricName"`
	Horizon            string               `yaml:"horizon"`
	MinHistoryDuration string               `yaml:"minHistoryDuration"`
	SMA                *smaConfigFile       `yaml:"sma,omitempty"`
	EWMA               *ewmaConfigFile      `yaml:"ewma,omitempty"`
	LinReg             *linRegConfigFile    `yaml:"linreg,omitempty"`
	HoltWinters        *holtWintersFile     `yaml:"holtwinters,omitempty"`
}

type smaConfigFile struct {
	Window int `yaml:"window"`
}

type ewmaConfigFile struct {
	Alpha       float64 `yaml:"alpha"`
	TrendWindow int     `yaml:"trendWindow"`
}

type linRegConfigFile struct {
	Window int `yaml:"window"`
}

type holtWintersFile struct {
	Alpha        float64 `yaml:"alpha"`
	Beta         float64 `yaml:"beta"`
	Gamma        float64 `yaml:"gamma"`
	SeasonLength int     `yaml:"seasonLength"`
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
	mode := ScalingMode(raw.ScalingMode)
	if mode == "" {
		mode = ScalingModeHybrid
	}

	cfg := Config{
		MinInstances: raw.MinInstances,
		MaxInstances: raw.MaxInstances,
		Metrics:      raw.Metrics,
		Aggregation:  raw.Aggregation,
		ScalingMode:  mode,
	}

	if raw.PollInterval == "" {
		return Config{}, fmt.Errorf("pollInterval is required")
	}
	pollInterval, err := time.ParseDuration(raw.PollInterval)
	if err != nil {
		return Config{}, fmt.Errorf("pollInterval %q: %w", raw.PollInterval, err)
	}
	cfg.PollInterval = pollInterval

	if raw.ScaleDownStabilizationWindow != "" {
		w, err := time.ParseDuration(raw.ScaleDownStabilizationWindow)
		if err != nil {
			return Config{}, fmt.Errorf("scaleDownStabilizationWindow %q: %w", raw.ScaleDownStabilizationWindow, err)
		}
		cfg.ScaleDownStabilizationWindow = w
	}

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
			Type:       PredictorType(raw.Prediction.Type),
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
		if raw.Prediction.SMA != nil {
			p.SMA = &SMAConfig{Window: raw.Prediction.SMA.Window}
		}
		if raw.Prediction.EWMA != nil {
			p.EWMA = &EWMAConfig{
				Alpha:       raw.Prediction.EWMA.Alpha,
				TrendWindow: raw.Prediction.EWMA.TrendWindow,
			}
		}
		if raw.Prediction.LinReg != nil {
			p.LinReg = &LinRegConfig{Window: raw.Prediction.LinReg.Window}
		}
		if raw.Prediction.HoltWinters != nil {
			p.HoltWinters = &HoltWintersConfig{
				Alpha:        raw.Prediction.HoltWinters.Alpha,
				Beta:         raw.Prediction.HoltWinters.Beta,
				Gamma:        raw.Prediction.HoltWinters.Gamma,
				SeasonLength: raw.Prediction.HoltWinters.SeasonLength,
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
	switch cfg.ScalingMode {
	case ScalingModeReactive, ScalingModePredictive, ScalingModeHybrid:
		// valid
	default:
		return fmt.Errorf("scalingMode %q is not supported (valid: reactive, predictive, hybrid)", cfg.ScalingMode)
	}
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
		switch cfg.Prediction.Type {
		case "", PredictorSMA, PredictorEWMA, PredictorLinReg, PredictorHoltWinters:
			// valid
		default:
			return fmt.Errorf("prediction.type %q is not supported (valid: sma, ewma, linreg, holtwinters)", cfg.Prediction.Type)
		}
		if cfg.Prediction.Type == PredictorEWMA {
			if cfg.Prediction.EWMA == nil {
				return fmt.Errorf("prediction.ewma config block is required when type is %q", PredictorEWMA)
			}
			if cfg.Prediction.EWMA.Alpha <= 0 || cfg.Prediction.EWMA.Alpha > 1 {
				return fmt.Errorf("prediction.ewma.alpha must be in (0, 1], got %v", cfg.Prediction.EWMA.Alpha)
			}
		}
		if cfg.Prediction.Type == PredictorHoltWinters {
			if cfg.Prediction.HoltWinters == nil {
				return fmt.Errorf("prediction.holtwinters config block is required when type is %q", PredictorHoltWinters)
			}
			if cfg.Prediction.HoltWinters.Alpha <= 0 || cfg.Prediction.HoltWinters.Alpha > 1 {
				return fmt.Errorf("prediction.holtwinters.alpha must be in (0, 1], got %v", cfg.Prediction.HoltWinters.Alpha)
			}
			if cfg.Prediction.HoltWinters.Beta <= 0 || cfg.Prediction.HoltWinters.Beta > 1 {
				return fmt.Errorf("prediction.holtwinters.beta must be in (0, 1], got %v", cfg.Prediction.HoltWinters.Beta)
			}
		}
	}
	return nil
}
