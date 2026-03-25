package scale

import (
	"context"
	"math"
	"time"
)

// HoltWintersPredictor implements Holt's linear method (double exponential smoothing).
// It decomposes the series into a level component and a trend component, then
// projects h steps ahead:
//
//	Level:   L_t = alpha*y_t + (1-alpha)*(L_{t-1} + T_{t-1})
//	Trend:   T_t = beta*(L_t - L_{t-1}) + (1-beta)*T_{t-1}
//	Forecast h steps ahead: L_n + h * T_n
//
// Suitable for series with a linear trend but no seasonality.
type HoltWintersPredictor struct {
	alpha float64
	beta  float64
}

// NewHoltWintersPredictor constructs a HoltWintersPredictor.
// Both alpha and beta must be in (0, 1].
func NewHoltWintersPredictor(alpha, beta float64) *HoltWintersPredictor {
	return &HoltWintersPredictor{alpha: alpha, beta: beta}
}

func (p *HoltWintersPredictor) Name() string { return "holtwinters" }

func (p *HoltWintersPredictor) Predict(_ context.Context, history []DataPoint, horizon time.Duration) (float64, error) {
	if len(history) == 0 {
		return 0, nil
	}
	if len(history) == 1 {
		return history[0].Value, nil
	}

	// Initialization: level = first value, trend = y[1] - y[0].
	level := history[0].Value
	trend := history[1].Value - history[0].Value

	for i := 1; i < len(history); i++ {
		prevLevel := level
		level = p.alpha*history[i].Value + (1-p.alpha)*(level+trend)
		trend = p.beta*(level-prevLevel) + (1-p.beta)*trend
	}

	// Steps ahead = horizon / avgInterval.
	avgInterval := avgPointInterval(history)
	steps := 0.0
	if avgInterval > 0 {
		steps = horizon.Seconds() / avgInterval.Seconds()
	}

	predicted := level + steps*trend
	return math.Max(predicted, 0), nil
}
