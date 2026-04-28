package scale

import (
	"context"
	"fmt"
	"math"
	"time"
)

// HoltWintersPredictor implements Holt-Winters triple exponential smoothing (additive).
// It decomposes the series into three components:
//
//	Level:    L_t = alpha*(y_t - S_{t-m}) + (1-alpha)*(L_{t-1} + T_{t-1})
//	Trend:    T_t = beta*(L_t - L_{t-1}) + (1-beta)*T_{t-1}
//	Seasonal: S_t = gamma*(y_t - L_t) + (1-gamma)*S_{t-m}
//	Forecast h steps ahead: L_n + h*T_n + S[(n-1+h) mod m]
//
// This makes it suitable for periodic workloads where load follows a repeating
// cycle (e.g. daily traffic waves, periodic test scenarios).
//
// Requires at least 2*seasonLength data points to initialise the seasonal
// indices. Falls back to Holt's linear (level+trend only) until then.
type HoltWintersPredictor struct {
	alpha        float64
	beta         float64
	gamma        float64
	seasonLength int
}

// NewHoltWintersPredictor constructs a HoltWintersPredictor.
// alpha, beta, gamma must all be in (0, 1]. seasonLength must be >= 2.
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

	// Not enough history for seasonal init — fall back to Holt's linear.
	if len(history) < 2*m {
		return p.holtLinear(history, steps), nil
	}

	// Initialise using the first two seasons.
	//
	// Level: mean of first season.
	var sumS1 float64
	for i := 0; i < m; i++ {
		sumS1 += history[i].Value
	}
	level := sumS1 / float64(m)

	// Trend: (mean(season2) - mean(season1)) / m — average step-change per point.
	var sumS2 float64
	for i := m; i < 2*m; i++ {
		sumS2 += history[i].Value
	}
	trend := (sumS2/float64(m) - level) / float64(m)

	// Seasonal indices: deviation of each point from the initial level.
	seasonal := make([]float64, m)
	for i := 0; i < m; i++ {
		seasonal[i] = history[i].Value - level
	}

	// Update level, trend, and seasonal for every point from season 2 onward.
	for i := m; i < len(history); i++ {
		y := history[i].Value
		si := i % m

		prevLevel := level
		level = p.alpha*(y-seasonal[si]) + (1-p.alpha)*(level+trend)
		trend = p.beta*(level-prevLevel) + (1-p.beta)*trend
		seasonal[si] = p.gamma*(y-level) + (1-p.gamma)*seasonal[si]
	}

	// Forecast steps ahead. The seasonal index for step h from the end of
	// history is (n-1+h) mod m, where n = len(history).
	n := len(history)
	seasonalIdx := (n - 1 + steps) % m
	predicted := level + float64(steps)*trend + seasonal[seasonalIdx]
	return math.Max(predicted, 0), nil
}

// holtLinear is the level+trend-only fallback used before 2*seasonLength
// data points have been collected.
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
