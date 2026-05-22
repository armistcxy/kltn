package scale

import (
	"context"
	"time"
)

// DataPoint is a single timestamped observation of a metric value
type DataPoint struct {
	Timestamp time.Time
	Value     float64
}

// Predictor forecasts a future metric value given its historical observations.
//
// Implement this interface to plug in any algorithm, the ScaleController will call Predict on every reconcile loop when prediction is enabled.
type Predictor interface {
	Name() string
	Predict(ctx context.Context, history []DataPoint, horizon time.Duration) (float64, error)
}

type PredictorFunc struct {
	name string
	fn   func(ctx context.Context, history []DataPoint, horizon time.Duration) (float64, error)
}

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

type MovingAveragePredictor struct {
	window int
}

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
