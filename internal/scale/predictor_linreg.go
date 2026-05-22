package scale

import (
	"context"
	"math"
	"time"
)

// LinRegPredictor fits an OLS line to the last Window data points and extrapolates to now+horizon.
type LinRegPredictor struct {
	window int
}

func NewLinRegPredictor(window int) *LinRegPredictor {
	if window <= 1 {
		window = 30
	}
	return &LinRegPredictor{window: window}
}

func (p *LinRegPredictor) Name() string { return "linreg" }

func (p *LinRegPredictor) Predict(_ context.Context, history []DataPoint, horizon time.Duration) (float64, error) {
	if len(history) == 0 {
		return 0, nil
	}
	if len(history) == 1 {
		return history[0].Value, nil
	}

	start := len(history) - p.window
	if start < 0 {
		start = 0
	}
	pts := history[start:]

	n := float64(len(pts))
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
	var slope, intercept float64
	if math.Abs(denom) < 1e-9 {
		intercept = sumY / n
	} else {
		slope = (n*sumTY - sumT*sumY) / denom
		intercept = (sumY - slope*sumT) / n
	}

	tForecast := pts[len(pts)-1].Timestamp.Sub(t0).Seconds() + horizon.Seconds()
	predicted := intercept + slope*tForecast

	return math.Max(predicted, 0), nil
}
