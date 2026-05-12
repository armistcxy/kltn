package pattern

import (
	"testing"
	"time"
)

func steps(ss ...Step) *StepPattern {
	var total time.Duration
	for _, s := range ss {
		total += s.Duration
	}
	return &StepPattern{steps: ss, totalDur: total}
}

func TestRPS_flatStep(t *testing.T) {
	p := steps(Step{Duration: 60 * time.Second, RPS: 100})
	if got := p.RPS(0); got != 100 {
		t.Errorf("t=0: want 100, got %f", got)
	}
	if got := p.RPS(30 * time.Second); got != 100 {
		t.Errorf("t=30s: want 100, got %f", got)
	}
}

func TestRPS_linearRamp(t *testing.T) {
	p := steps(Step{Duration: 100 * time.Second, RPS: 0, EndRPS: 1000})

	if got := p.RPS(0); got != 0 {
		t.Errorf("t=0: want 0, got %f", got)
	}
	if got := p.RPS(50 * time.Second); got != 500 {
		t.Errorf("t=50s: want 500, got %f", got)
	}
	if got := p.RPS(100 * time.Second); got != 1000 {
		// at exactly the boundary we fall through to "hold last EndRPS"
		t.Errorf("t=100s: want 1000, got %f", got)
	}
}

func TestRPS_multiStep(t *testing.T) {
	p := steps(
		Step{Duration: 30 * time.Second, RPS: 50},
		Step{Duration: 60 * time.Second, RPS: 50, EndRPS: 1500},
		Step{Duration: 20 * time.Second, RPS: 1500},
	)

	if got := p.RPS(15 * time.Second); got != 50 {
		t.Errorf("phase1: want 50, got %f", got)
	}
	if got := p.RPS(60 * time.Second); got != 775 {
		t.Errorf("phase2 mid: want 775, got %f", got)
	}
	if got := p.RPS(100 * time.Second); got != 1500 {
		t.Errorf("phase3: want 1500, got %f", got)
	}
}

func TestRPS_afterAllSteps(t *testing.T) {
	p := steps(Step{Duration: 10 * time.Second, RPS: 200, EndRPS: 400})
	if got := p.RPS(999 * time.Second); got != 400 {
		t.Errorf("after end: want 400, got %f", got)
	}
}
