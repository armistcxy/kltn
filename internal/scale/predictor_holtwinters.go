package scale

import (
	"context"
	"fmt"
	"math"
	"time"
)

// HoltWintersPredictor implements Holt-Winters triple exponential smoothing
type HoltWintersPredictor struct {
	alpha        float64
	beta         float64
	gamma        float64
	seasonLength int
}

func NewHoltWintersPredictor(alpha, beta, gamma float64, seasonLength int) (*HoltWintersPredictor, error) {
	if alpha <= 0 || alpha > 1 {
		return nil, fmt.Errorf("holtwinters: alpha must be in (0,1], got %v", alpha)
	}
	if beta <= 0 || beta > 1 {
		return nil, fmt.Errorf("holtwinters: beta must be in (0,1], got %v", beta)
	}
	if gamma <= 0 || gamma > 1 {
		return nil, fmt.Errorf("holtwinters: gamma must be in (0,1], got %v", gamma)
	}
	if seasonLength < 2 {
		return nil, fmt.Errorf("holtwinters: seasonLength must be >= 2, got %v", seasonLength)
	}
	return &HoltWintersPredictor{
		alpha:        alpha,
		beta:         beta,
		gamma:        gamma,
		seasonLength: seasonLength,
	}, nil
}

func (p *HoltWintersPredictor) Name() string { return "holtwinters" }

func (p *HoltWintersPredictor) Predict(_ context.Context, history []DataPoint, horizon time.Duration) (float64, error) {
	if len(history) == 0 {
		return 0, nil
	}
	if len(history) == 1 {
		return history[0].Value, nil
	}

	avgInterval := avgPointInterval(history)
	steps := 1
	if avgInterval > 0 {
		steps = int(math.Round(horizon.Seconds() / avgInterval.Seconds()))
		if steps < 1 {
			steps = 1
		}
	}

	m := p.seasonLength

	if len(history) < 2*m {
		return p.holtLinear(history, steps), nil
	}

	var sumS1 float64
	for i := 0; i < m; i++ {
		sumS1 += history[i].Value
	}
	level := sumS1 / float64(m)

	var sumS2 float64
	for i := m; i < 2*m; i++ {
		sumS2 += history[i].Value
	}
	trend := (sumS2/float64(m) - level) / float64(m)

	seasonal := make([]float64, m)
	for i := 0; i < m; i++ {
		seasonal[i] = history[i].Value - level
	}

	for i := m; i < len(history); i++ {
		y := history[i].Value
		si := i % m

		prevLevel := level
		level = p.alpha*(y-seasonal[si]) + (1-p.alpha)*(level+trend)
		trend = p.beta*(level-prevLevel) + (1-p.beta)*trend
		seasonal[si] = p.gamma*(y-level) + (1-p.gamma)*seasonal[si]
	}

	n := len(history)
	seasonalIdx := (n - 1 + steps) % m
	predicted := level + float64(steps)*trend + seasonal[seasonalIdx]
	return math.Max(predicted, 0), nil
}

func (p *HoltWintersPredictor) holtLinear(history []DataPoint, steps int) float64 {
	level := history[0].Value
	trend := history[1].Value - history[0].Value

	for i := 1; i < len(history); i++ {
		prevLevel := level
		level = p.alpha*history[i].Value + (1-p.alpha)*(level+trend)
		trend = p.beta*(level-prevLevel) + (1-p.beta)*trend
	}

	predicted := level + float64(steps)*trend
	return math.Max(predicted, 0)
}
