// Package pattern defines load patterns (ramp, spike, steady-state).
// Full implementation in WP3.
package pattern

import (
	"fmt"
	"os"
	"time"

	"gopkg.in/yaml.v3"
)

// Pattern drives the rate limiter over time
type Pattern interface {
	RPS(t time.Duration) float64
	Done(t time.Duration) bool
	TotalDuration() time.Duration
}

// Step is one phase in a multi-step scenario
type Step struct {
	Duration time.Duration `yaml:"duration"`
	RPS      float64       `yaml:"rps"`
	EndRPS   float64       `yaml:"end_rps"`
}

// ScenarioFile is the top-level YAML structure for scenario files.
type ScenarioFile struct {
	Name  string `yaml:"name"`
	Steps []struct {
		Duration string  `yaml:"duration"`
		RPS      float64 `yaml:"rps"`
		EndRPS   float64 `yaml:"end_rps"`
	} `yaml:"steps"`
}

// StepPattern runs a sequence of (duration, rps) steps, then holds the last RPS
type StepPattern struct {
	steps    []Step
	totalDur time.Duration
}

// LoadFile parses a YAML scenario file into a Pattern
func LoadFile(path string) (*StepPattern, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read scenario: %w", err)
	}

	var sf ScenarioFile
	if err := yaml.Unmarshal(raw, &sf); err != nil {
		return nil, fmt.Errorf("parse scenario: %w", err)
	}
	if len(sf.Steps) == 0 {
		return nil, fmt.Errorf("scenario %q has no steps", sf.Name)
	}

	steps := make([]Step, len(sf.Steps))
	var total time.Duration
	for i, s := range sf.Steps {
		d, err := time.ParseDuration(s.Duration)
		if err != nil {
			return nil, fmt.Errorf("step %d duration: %w", i, err)
		}
		steps[i] = Step{Duration: d, RPS: s.RPS, EndRPS: s.EndRPS}
		total += d
	}
	return &StepPattern{steps: steps, totalDur: total}, nil
}

func (p *StepPattern) RPS(t time.Duration) float64 {
	var cumul time.Duration
	for _, s := range p.steps {
		start := cumul
		cumul += s.Duration
		if t < cumul {
			if s.EndRPS == 0 {
				return s.RPS
			}
			// Linear interpolation within the step.
			progress := float64(t-start) / float64(s.Duration)
			return s.RPS + progress*(s.EndRPS-s.RPS)
		}
	}
	last := p.steps[len(p.steps)-1]
	if last.EndRPS != 0 {
		return last.EndRPS
	}
	return last.RPS
}

func (p *StepPattern) Done(t time.Duration) bool    { return t >= p.totalDur }
func (p *StepPattern) TotalDuration() time.Duration { return p.totalDur }
