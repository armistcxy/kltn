package scale

import "fmt"

// BuildPredictor constructs the Predictor described by cfg.
// Returns (nil, nil) when cfg is nil or prediction is disabled.
func BuildPredictor(cfg *PredictionConfig) (Predictor, error) {
	if cfg == nil || !cfg.Enabled {
		return nil, nil
	}

	switch cfg.Type {
	case "", PredictorSMA:
		window := 10
		if cfg.SMA != nil && cfg.SMA.Window > 0 {
			window = cfg.SMA.Window
		}
		return NewMovingAveragePredictor(window), nil

	case PredictorEWMA:
		// Validated by config loader, but guard defensively.
		if cfg.EWMA == nil {
			return nil, fmt.Errorf("predictor type %q requires an ewma config block", cfg.Type)
		}
		return NewEWMAPredictor(cfg.EWMA.Alpha, cfg.EWMA.TrendWindow)

	case PredictorLinReg:
		window := 30
		if cfg.LinReg != nil && cfg.LinReg.Window > 0 {
			window = cfg.LinReg.Window
		}
		return NewLinRegPredictor(window), nil

	case PredictorHoltWinters:
		if cfg.HoltWinters == nil {
			return nil, fmt.Errorf("predictor type %q requires a holtwinters config block", cfg.Type)
		}
		return NewHoltWintersPredictor(cfg.HoltWinters.Alpha, cfg.HoltWinters.Beta), nil

	default:
		return nil, fmt.Errorf("unknown predictor type %q (valid: sma, ewma, linreg, holtwinters)", cfg.Type)
	}
}
