package scale

import (
	"context"
	"time"
)

// DataPoint is a single timestamped observation of a metric value.
type DataPoint struct {
	Timestamp time.Time
	Value     float64
}

// Predictor forecasts a future metric value given its historical observations.
//
// Implement this interface to plug in any algorithm: ARIMA, Prophet,
// Holt-Winters, LSTM, or a simple heuristic. The ScaleController will
// call Predict on every reconcile loop when prediction is enabled.
//
// Example usage:
//
//	ctrl.WithPredictor(scale.NewPredictorFunc("my_algo", func(ctx context.Context, history []scale.DataPoint, horizon time.Duration) (float64, error) {
//	    // your algorithm here
//	    return forecast, nil
//	}))
type Predictor interface {
	// Name returns the algorithm identifier (used in logs).
	Name() string

	// Predict returns the forecasted metric value at now+horizon.
	// history is provided oldest-first and contains all recorded observations.
	Predict(ctx context.Context, history []DataPoint, horizon time.Duration) (float64, error)
}

// PredictorFunc is a function adapter that satisfies Predictor.
// Use it to wrap an inline function without defining a new type.
type PredictorFunc struct {
	name string
	fn   func(ctx context.Context, history []DataPoint, horizon time.Duration) (float64, error)
}

// NewPredictorFunc wraps a plain function as a Predictor.
func NewPredictorFunc(
	name string,
	fn func(ctx context.Context, history []DataPoint, horizon time.Duration) (float64, error),
) *PredictorFunc {
	return &PredictorFunc{name: name, fn: fn}
}

func (p *PredictorFunc) Name() string { return p.name }

func (p *PredictorFunc) Predict(ctx context.Context, history []DataPoint, horizon time.Duration) (float64, error) {
	return p.fn(ctx, history, horizon)
}

// MovingAveragePredictor is a simple baseline predictor.
//
// It forecasts the average of the last N data points, ignoring the horizon.
// Use this as a functional placeholder until a real algorithm is implemented.
// It will still provide useful signal by smoothing out short-term noise.
type MovingAveragePredictor struct {
	// window is the number of most-recent points to average.
	window int
}

// NewMovingAveragePredictor creates a moving-average predictor.
// window controls how many recent observations are averaged (default 10 if ≤ 0).
func NewMovingAveragePredictor(window int) *MovingAveragePredictor {
	if window <= 0 {
		window = 10
	}
	return &MovingAveragePredictor{window: window}
}

func (p *MovingAveragePredictor) Name() string { return "moving_average" }

func (p *MovingAveragePredictor) Predict(_ context.Context, history []DataPoint, _ time.Duration) (float64, error) {
	if len(history) == 0 {
		return 0, nil
	}

	start := len(history) - p.window
	if start < 0 {
		start = 0
	}

	window := history[start:]
	sum := 0.0
	for _, dp := range window {
		sum += dp.Value
	}
	return sum / float64(len(window)), nil
}
