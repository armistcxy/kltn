package scale

import (
	"context"
	"fmt"
	"math"
	"time"
)

// EWMAPredictor forecasts using Exponential Weighted Moving Average with OLS trend projection.
//
// Algorithm:
//  1. Compute a single running EWMA over all history values.
//  2. Estimate the current trend slope via OLS linear regression on the last
//     min(trendWindow, len(history)) raw data points.
//  3. Extrapolate: predicted = ewma + slope * (horizon / avgInterval).
//  4. Floor result at 0.
type EWMAPredictor struct {
	alpha       float64
	trendWindow int
}

// NewEWMAPredictor constructs an EWMAPredictor.
// alpha must be in (0, 1]. trendWindow <= 1 disables trend extrapolation.
func NewEWMAPredictor(alpha float64, trendWindow int) (*EWMAPredictor, error) {
	if alpha <= 0 || alpha > 1 {
		return nil, fmt.Errorf("ewma: alpha must be in (0, 1], got %v", alpha)
	}
	return &EWMAPredictor{alpha: alpha, trendWindow: trendWindow}, nil
}

func (p *EWMAPredictor) Name() string { return "ewma" }

func (p *EWMAPredictor) Predict(_ context.Context, history []DataPoint, horizon time.Duration) (float64, error) {
	if len(history) == 0 {
		return 0, nil
	}
	if len(history) == 1 {
		return history[0].Value, nil
	}

	// Step 1: compute running EWMA over all values
	ewma := history[0].Value
	for i := 1; i < len(history); i++ {
		ewma = p.alpha*history[i].Value + (1-p.alpha)*ewma
	}

	// Step 2: compute OLS slope from last min(trendWindow, len) raw points
	tw := p.trendWindow
	if tw <= 0 {
		tw = 30
	}
	start := len(history) - tw
	if start < 0 {
		start = 0
	}
	recent := history[start:]

	// Step 3: extrapolate: slope is value/second, so multiply directly by horizon
	// equivalent to (slope/interval * steps) where steps = horizon/avgInterval
	slope := olsSlope(recent)
	predicted := ewma + slope*horizon.Seconds()

	// Step 4: floor at 0
	return math.Max(predicted, 0), nil
}

// olsSlope fits y = a + b*t to the data points using OLS and returns slope b
// t is seconds since the first point in pts
func olsSlope(pts []DataPoint) float64 {
	n := float64(len(pts))
	if n < 2 {
		return 0
	}
	t0 := pts[0].Timestamp
	var sumT, sumY, sumTT, sumTY float64
	for _, dp := range pts {
		t := dp.Timestamp.Sub(t0).Seconds()
		sumT += t
		sumY += dp.Value
		sumTT += t * t
		sumTY += t * dp.Value
	}
	denom := n*sumTT - sumT*sumT
	if math.Abs(denom) < 1e-9 {
		return 0
	}
	return (n*sumTY - sumT*sumY) / denom
}

// avgPointInterval returns the average duration between consecutive data points
func avgPointInterval(pts []DataPoint) time.Duration {
	if len(pts) < 2 {
		return 0
	}
	total := pts[len(pts)-1].Timestamp.Sub(pts[0].Timestamp)
	return total / time.Duration(len(pts)-1)
}
