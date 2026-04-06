package storage

import (
	"fmt"
	"os"
	"time"

	"sigs.k8s.io/yaml"
)

// Config is the full configuration for the storage autoscaling controller.
type Config struct {
	Enabled      bool
	PollInterval time.Duration
	Namespace    string
	Cluster      string

	PGData       PGDataConfig
	WAL          WALConfig
	SafetyGuards SafetyGuardsConfig
}

// PGDataConfig configures autoscaling for the main PostgreSQL data volume.
type PGDataConfig struct {
	// ScaleUpThresholdPercent triggers a resize when PVC usage exceeds this value (e.g. 80).
	ScaleUpThresholdPercent float64

	// CriticalThresholdPercent triggers an immediate resize, bypassing cooldown (e.g. 90).
	CriticalThresholdPercent float64

	// StepPercent is the percentage by which to increase the current size each resize (e.g. 50 → 10Gi → 15Gi).
	StepPercent float64

	// MaxSizeGi is the hard upper limit in GiB (e.g. 100).
	MaxSizeGi int

	// Cooldown is the minimum time between two resize operations.
	Cooldown time.Duration

	// PreemptiveResizeIfFullInHours triggers a preemptive resize when the worst-case estimated
	// time-to-full (based on p95/p99 historical growth rate) falls below this many hours.
	// 0 disables preemptive resizing.
	PreemptiveResizeIfFullInHours float64
}

// WALConfig configures autoscaling for the WAL volume (spec.walStorage).
// Only applies when the CNPG cluster has a dedicated walStorage volume.
type WALConfig struct {
	// Enabled controls whether WAL storage scaling is active.
	Enabled bool

	// ScaleUpThresholdPercent triggers a resize when WAL usage ratio exceeds this value (e.g. 70).
	ScaleUpThresholdPercent float64

	// CriticalThresholdPercent triggers an immediate resize, bypassing cooldown (e.g. 85).
	CriticalThresholdPercent float64

	// StepPercent is the percentage by which to increase the current WAL volume size per resize.
	StepPercent float64

	// MaxSizeGi is the hard upper limit in GiB.
	MaxSizeGi int

	// Cooldown is the minimum time between two WAL resize operations.
	Cooldown time.Duration
}

// SafetyGuardsConfig defines conditions that block a storage resize.
type SafetyGuardsConfig struct {
	// BlockIfWALArchivePending blocks resize when WAL archive pending files exceed this count.
	// 0 disables this guard.
	BlockIfWALArchivePending int

	// BlockIfReplicationLagSeconds blocks resize when max replica lag exceeds this threshold.
	// 0 disables this guard.
	BlockIfReplicationLagSeconds float64
}

// ---- YAML parsing types ----

type configFile struct {
	StorageScaling storageScalingFile `yaml:"storageScaling"`
}

type storageScalingFile struct {
	Enabled      bool              `yaml:"enabled"`
	PollInterval string            `yaml:"pollInterval"`
	Namespace    string            `yaml:"namespace"`
	Cluster      string            `yaml:"cluster"`
	PGData       pgDataConfigFile  `yaml:"pgdataStorage"`
	WAL          walConfigFile     `yaml:"walStorage"`
	SafetyGuards safetyGuardsFile  `yaml:"safetyGuards"`
}

type pgDataConfigFile struct {
	ScaleUpThresholdPercent       float64 `yaml:"scaleUpThresholdPercent"`
	CriticalThresholdPercent      float64 `yaml:"criticalThresholdPercent"`
	StepPercent                   float64 `yaml:"stepPercent"`
	MaxSizeGi                     int     `yaml:"maxSizeGi"`
	CooldownMinutes               int     `yaml:"cooldownMinutes"`
	PreemptiveResizeIfFullInHours float64 `yaml:"preemptiveResizeIfFullInHours"`
}

type walConfigFile struct {
	Enabled                  bool    `yaml:"enabled"`
	ScaleUpThresholdPercent  float64 `yaml:"scaleUpThresholdPercent"`
	CriticalThresholdPercent float64 `yaml:"criticalThresholdPercent"`
	StepPercent              float64 `yaml:"stepPercent"`
	MaxSizeGi                int     `yaml:"maxSizeGi"`
	CooldownMinutes          int     `yaml:"cooldownMinutes"`
}

type safetyGuardsFile struct {
	BlockIfWALArchivePending     int     `yaml:"blockIfWalArchivePending"`
	BlockIfReplicationLagSeconds float64 `yaml:"blockIfReplicationLagSeconds"`
}

// LoadConfig reads and parses a YAML config file into Config.
// The YAML file must have a top-level "storageScaling" key.
func LoadConfig(path string) (Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return Config{}, fmt.Errorf("read config file: %w", err)
	}

	var raw configFile
	if err := yaml.Unmarshal(data, &raw); err != nil {
		return Config{}, fmt.Errorf("parse config file: %w", err)
	}

	return convertConfig(raw.StorageScaling)
}

func convertConfig(raw storageScalingFile) (Config, error) {
	pollInterval := 60 * time.Second
	if raw.PollInterval != "" {
		var err error
		if pollInterval, err = time.ParseDuration(raw.PollInterval); err != nil {
			return Config{}, fmt.Errorf("pollInterval %q: %w", raw.PollInterval, err)
		}
	}

	cfg := Config{
		Enabled:      raw.Enabled,
		PollInterval: pollInterval,
		Namespace:    raw.Namespace,
		Cluster:      raw.Cluster,
		PGData: PGDataConfig{
			ScaleUpThresholdPercent:       raw.PGData.ScaleUpThresholdPercent,
			CriticalThresholdPercent:      raw.PGData.CriticalThresholdPercent,
			StepPercent:                   raw.PGData.StepPercent,
			MaxSizeGi:                     raw.PGData.MaxSizeGi,
			Cooldown:                      time.Duration(raw.PGData.CooldownMinutes) * time.Minute,
			PreemptiveResizeIfFullInHours: raw.PGData.PreemptiveResizeIfFullInHours,
		},
		WAL: WALConfig{
			Enabled:                  raw.WAL.Enabled,
			ScaleUpThresholdPercent:  raw.WAL.ScaleUpThresholdPercent,
			CriticalThresholdPercent: raw.WAL.CriticalThresholdPercent,
			StepPercent:              raw.WAL.StepPercent,
			MaxSizeGi:                raw.WAL.MaxSizeGi,
			Cooldown:                 time.Duration(raw.WAL.CooldownMinutes) * time.Minute,
		},
		SafetyGuards: SafetyGuardsConfig{
			BlockIfWALArchivePending:     raw.SafetyGuards.BlockIfWALArchivePending,
			BlockIfReplicationLagSeconds: raw.SafetyGuards.BlockIfReplicationLagSeconds,
		},
	}

	return cfg, validateConfig(cfg)
}

func validateConfig(cfg Config) error {
	if cfg.Namespace == "" {
		return fmt.Errorf("storageScaling.namespace is required")
	}
	if cfg.Cluster == "" {
		return fmt.Errorf("storageScaling.cluster is required")
	}
	if cfg.PollInterval <= 0 {
		return fmt.Errorf("storageScaling.pollInterval must be > 0")
	}
	if cfg.PGData.ScaleUpThresholdPercent <= 0 || cfg.PGData.ScaleUpThresholdPercent >= 100 {
		return fmt.Errorf("pgdataStorage.scaleUpThresholdPercent must be in (0, 100)")
	}
	if cfg.PGData.CriticalThresholdPercent <= cfg.PGData.ScaleUpThresholdPercent {
		return fmt.Errorf("pgdataStorage.criticalThresholdPercent must be > scaleUpThresholdPercent")
	}
	if cfg.PGData.StepPercent <= 0 {
		return fmt.Errorf("pgdataStorage.stepPercent must be > 0")
	}
	if cfg.PGData.MaxSizeGi <= 0 {
		return fmt.Errorf("pgdataStorage.maxSizeGi must be > 0")
	}
	if cfg.WAL.Enabled {
		if cfg.WAL.ScaleUpThresholdPercent <= 0 || cfg.WAL.ScaleUpThresholdPercent >= 100 {
			return fmt.Errorf("walStorage.scaleUpThresholdPercent must be in (0, 100)")
		}
		if cfg.WAL.CriticalThresholdPercent <= cfg.WAL.ScaleUpThresholdPercent {
			return fmt.Errorf("walStorage.criticalThresholdPercent must be > scaleUpThresholdPercent")
		}
		if cfg.WAL.StepPercent <= 0 {
			return fmt.Errorf("walStorage.stepPercent must be > 0")
		}
		if cfg.WAL.MaxSizeGi <= 0 {
			return fmt.Errorf("walStorage.maxSizeGi must be > 0")
		}
	}
	return nil
}
