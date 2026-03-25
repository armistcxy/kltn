package scale

import (
	"context"
	"testing"
	"time"
)

func makeHistory(values []float64, interval time.Duration) []DataPoint {
	now := time.Now()
	pts := make([]DataPoint, len(values))
	for i, v := range values {
		pts[i] = DataPoint{
			Timestamp: now.Add(time.Duration(i) * interval),
			Value:     v,
		}
	}
	return pts
}

// --- EWMA ---

func TestEWMAPredictor_FlatSeries(t *testing.T) {
	p, _ := NewEWMAPredictor(0.3, 10)
	// Flat series: EWMA = constant, slope ≈ 0 → forecast ≈ constant.
	history := makeHistory([]float64{50, 50, 50, 50, 50, 50, 50, 50, 50, 50}, 30*time.Second)
	got, err := p.Predict(context.Background(), history, 5*time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if got < 49 || got > 51 {
		t.Errorf("expected ~50 for flat series, got %.2f", got)
	}
}

func TestEWMAPredictor_RisingSeries(t *testing.T) {
	// Use alpha=0.9 so EWMA tracks closely to the latest values.
	p, _ := NewEWMAPredictor(0.9, 10)
	// Rising series: EWMA ~≈ last value, positive slope → forecast 1 step ahead > last value.
	history := makeHistory([]float64{10, 20, 30, 40, 50, 60, 70, 80, 90, 100}, 30*time.Second)
	got, err := p.Predict(context.Background(), history, 30*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if got <= 100 {
		t.Errorf("expected forecast > 100 for rising series with alpha=0.9, got %.2f", got)
	}
}

func TestEWMAPredictor_EmptyHistory(t *testing.T) {
	p, _ := NewEWMAPredictor(0.5, 5)
	got, err := p.Predict(context.Background(), nil, 5*time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if got != 0 {
		t.Errorf("expected 0 for empty history, got %.2f", got)
	}
}

func TestEWMAPredictor_NegativeForecastFlooredAtZero(t *testing.T) {
	p, _ := NewEWMAPredictor(0.9, 5)
	// Sharply decreasing series — forecast might go negative.
	history := makeHistory([]float64{100, 80, 60, 40, 20, 10, 5, 2, 1, 0}, 30*time.Second)
	got, err := p.Predict(context.Background(), history, 5*time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if got < 0 {
		t.Errorf("forecast must not be negative, got %.2f", got)
	}
}

func TestNewEWMAPredictor_InvalidAlpha(t *testing.T) {
	if _, err := NewEWMAPredictor(0, 5); err == nil {
		t.Error("expected error for alpha=0")
	}
	if _, err := NewEWMAPredictor(1.1, 5); err == nil {
		t.Error("expected error for alpha=1.1")
	}
}

// --- LinReg ---

func TestLinRegPredictor_LinearSeries(t *testing.T) {
	p := NewLinRegPredictor(10)
	// Perfect linear series y = 10*i; slope = 10/30s = 1/3 per second.
	history := makeHistory([]float64{0, 10, 20, 30, 40, 50, 60, 70, 80, 90}, 30*time.Second)
	got, err := p.Predict(context.Background(), history, 30*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	// Next step should be ~100.
	if got < 95 || got > 105 {
		t.Errorf("expected ~100 for linear series, got %.2f", got)
	}
}

func TestLinRegPredictor_FlatSeries(t *testing.T) {
	p := NewLinRegPredictor(10)
	history := makeHistory([]float64{42, 42, 42, 42, 42, 42, 42, 42, 42, 42}, 30*time.Second)
	got, err := p.Predict(context.Background(), history, 5*time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if got < 41 || got > 43 {
		t.Errorf("expected ~42 for flat series, got %.2f", got)
	}
}

func TestLinRegPredictor_EmptyHistory(t *testing.T) {
	p := NewLinRegPredictor(10)
	got, err := p.Predict(context.Background(), nil, 5*time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if got != 0 {
		t.Errorf("expected 0 for empty history, got %.2f", got)
	}
}

func TestLinRegPredictor_NegativeForecastFlooredAtZero(t *testing.T) {
	p := NewLinRegPredictor(10)
	history := makeHistory([]float64{100, 80, 60, 40, 20, 10, 5, 2, 1, 0}, 30*time.Second)
	got, err := p.Predict(context.Background(), history, 5*time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if got < 0 {
		t.Errorf("forecast must not be negative, got %.2f", got)
	}
}

// --- HoltWinters ---

func TestHoltWintersPredictor_FlatSeries(t *testing.T) {
	p := NewHoltWintersPredictor(0.3, 0.1)
	history := makeHistory([]float64{50, 50, 50, 50, 50, 50, 50, 50, 50, 50}, 30*time.Second)
	got, err := p.Predict(context.Background(), history, 5*time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if got < 45 || got > 55 {
		t.Errorf("expected ~50 for flat series, got %.2f", got)
	}
}

func TestHoltWintersPredictor_RisingSeries(t *testing.T) {
	p := NewHoltWintersPredictor(0.4, 0.3)
	history := makeHistory([]float64{10, 20, 30, 40, 50, 60, 70, 80, 90, 100}, 30*time.Second)
	got, err := p.Predict(context.Background(), history, 30*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if got <= 100 {
		t.Errorf("expected forecast > 100 for rising series, got %.2f", got)
	}
}

func TestHoltWintersPredictor_EmptyHistory(t *testing.T) {
	p := NewHoltWintersPredictor(0.3, 0.1)
	got, err := p.Predict(context.Background(), nil, 5*time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if got != 0 {
		t.Errorf("expected 0 for empty history, got %.2f", got)
	}
}

func TestHoltWintersPredictor_NegativeForecastFlooredAtZero(t *testing.T) {
	p := NewHoltWintersPredictor(0.5, 0.3)
	history := makeHistory([]float64{100, 80, 60, 40, 20, 10, 5, 2, 1, 0}, 30*time.Second)
	got, err := p.Predict(context.Background(), history, 5*time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if got < 0 {
		t.Errorf("forecast must not be negative, got %.2f", got)
	}
}

// --- BuildPredictor factory ---

func TestBuildPredictor_EWMA(t *testing.T) {
	cfg := &PredictionConfig{
		Enabled: true,
		Type:    PredictorEWMA,
		EWMA:    &EWMAConfig{Alpha: 0.3, TrendWindow: 30},
	}
	p, err := BuildPredictor(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if p.Name() != "ewma" {
		t.Errorf("expected name 'ewma', got %q", p.Name())
	}
}

func TestBuildPredictor_LinReg(t *testing.T) {
	cfg := &PredictionConfig{Enabled: true, Type: PredictorLinReg}
	p, err := BuildPredictor(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if p.Name() != "linreg" {
		t.Errorf("expected name 'linreg', got %q", p.Name())
	}
}

func TestBuildPredictor_HoltWinters(t *testing.T) {
	cfg := &PredictionConfig{
		Enabled:     true,
		Type:        PredictorHoltWinters,
		HoltWinters: &HoltWintersConfig{Alpha: 0.3, Beta: 0.1},
	}
	p, err := BuildPredictor(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if p.Name() != "holtwinters" {
		t.Errorf("expected name 'holtwinters', got %q", p.Name())
	}
}

func TestBuildPredictor_SMADefault(t *testing.T) {
	cfg := &PredictionConfig{Enabled: true, Type: ""}
	p, err := BuildPredictor(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if p.Name() != "moving_average" {
		t.Errorf("expected 'moving_average', got %q", p.Name())
	}
}

func TestBuildPredictor_Disabled(t *testing.T) {
	p, err := BuildPredictor(&PredictionConfig{Enabled: false})
	if err != nil || p != nil {
		t.Errorf("expected (nil, nil) for disabled prediction, got (%v, %v)", p, err)
	}
}

func TestBuildPredictor_UnknownType(t *testing.T) {
	cfg := &PredictionConfig{Enabled: true, Type: "neural_net"}
	_, err := BuildPredictor(cfg)
	if err == nil {
		t.Error("expected error for unknown predictor type")
	}
}
