package series

import (
	"math"
	"testing"
	"time"
)

func f64Ptr(v float64) *float64 { return &v }

func utcDay(y int, m time.Month, d int) time.Time {
	return time.Date(y, m, d, 0, 0, 0, 0, time.UTC)
}

func TestFitCalibrationEdgeCasesAndDeterminism(t *testing.T) {
	empty := FitCalibration("global", "live", 1, nil)
	if empty.NDays != 0 || empty.Reliable || empty.K != 1.0 || empty.KApplied != 1.0 {
		t.Fatalf("unexpected empty calibration: %+v", empty)
	}

	single := FitCalibration("global", "live", 1, []OverlapSample{
		{Date: utcDay(2026, 11, 12), ModisFRP: 300, ViirsMeqFRP: 400},
		{Date: utcDay(2026, 11, 13), ModisFRP: 0, ViirsMeqFRP: 200}, // zero MODIS ignored
	})
	if single.NDays != 1 || math.Abs(single.K-0.75) > 1e-9 || single.Reliable || single.KApplied != 1.0 {
		t.Fatalf("unexpected single sample calibration: %+v", single)
	}

	var few []OverlapSample
	for i := 0; i < 13; i++ {
		few = append(few, OverlapSample{
			Date:        utcDay(2024, 3, 1+i),
			ModisFRP:    120,
			ViirsMeqFRP: 100,
		})
	}
	fewModel := FitCalibration("california", "live", 1, few)
	if fewModel.NDays != 13 || fewModel.Reliable || fewModel.KApplied != 1.0 {
		t.Fatalf("expected <14 days to stay unverified: %+v", fewModel)
	}

	// Wide relative CI (>50%) should stay unverified even with n >= 14.
	var noisy []OverlapSample
	for i := 0; i < 16; i++ {
		ratio := 0.25
		if i%2 == 1 {
			ratio = 2.50
		}
		noisy = append(noisy, OverlapSample{
			Date:        utcDay(2024, 4, 1+i),
			ModisFRP:    100 * ratio,
			ViirsMeqFRP: 100,
		})
	}
	noisyModel := FitCalibration("amazon", "history", 1, noisy)
	if noisyModel.NDays != 16 || noisyModel.Reliable || noisyModel.KApplied != 1.0 {
		t.Fatalf("expected wide CI model to be unreliable: %+v", noisyModel)
	}

	// Tight ratios with >= 14 days should verify and yield reproducible estimates.
	var steady []OverlapSample
	for i := 0; i < 30; i++ {
		ratio := 1.24 + float64(i%5)*0.01
		steady = append(steady, OverlapSample{
			Date:        utcDay(2024, 5, 1+i),
			ModisFRP:    100 * ratio,
			ViirsMeqFRP: 100,
		})
	}
	m1 := FitCalibration("pakistan-indus", "history", 1, steady)
	m2 := FitCalibration("pakistan-indus", "history", 1, steady)
	if !m1.Reliable || m1.NDays != 30 {
		t.Fatalf("expected reliable calibration: %+v", m1)
	}
	if m1.K < 1.24 || m1.K > 1.28 || m1.KApplied != m1.K {
		t.Fatalf("expected k near 1.26 and applied: %+v", m1)
	}
	if m1.CILow != m2.CILow || m1.CIHigh != m2.CIHigh || m1.K != m2.K {
		t.Fatalf("calibration bootstrap is not deterministic: %+v vs %+v", m1, m2)
	}
}

func TestBuildDailySeriesAndSeasonalRobustZ(t *testing.T) {
	rows := []RegionDayInput{
		{Date: utcDay(2010, 1, 2), ModisFRP: f64Ptr(100), ViirsMeqFRP: nil},                       // MODIS only
		{Date: utcDay(2026, 1, 2), ModisFRP: nil, ViirsMeqFRP: f64Ptr(100)},                       // VIIRS only
		{Date: utcDay(2024, 2, 29), ModisFRP: f64Ptr(100), ViirsMeqFRP: f64Ptr(100)},              // Leap day
		{Date: utcDay(2024, 12, 31), ModisFRP: f64Ptr(100), ViirsMeqFRP: f64Ptr(100)},             // Year boundary
		{Date: utcDay(2025, 1, 2), ModisFRP: f64Ptr(100), ViirsMeqFRP: f64Ptr(100)},               // Jan 2 in window around Dec 31
		{Date: utcDay(2025, 1, 3), ModisFRP: f64Ptr(500), ViirsMeqFRP: f64Ptr(500), IsPartial: true}, // Partial outlier excluded from baseline
	}

	// Add multi-year Dec 30 - Jan 4 baseline around ~100 MW.
	for y := 2013; y <= 2023; y++ {
		if y%2 == 0 {
			rows = append(rows,
				RegionDayInput{Date: utcDay(y, 12, 30), ModisFRP: f64Ptr(95), ViirsMeqFRP: f64Ptr(105)},
				RegionDayInput{Date: utcDay(y+1, 1, 3), ModisFRP: f64Ptr(102), ViirsMeqFRP: f64Ptr(98)},
			)
		}
	}

	pts := BuildDailySeries(rows, 1.5, true)
	byDate := make(map[string]DailyPoint, len(pts))
	for _, p := range pts {
		byDate[p.Date.Format("2006-01-02")] = p
	}

	if got := byDate["2010-01-02"]; !got.HasHarmonized || got.HarmonizedFRP != 100 {
		t.Fatalf("unexpected MODIS-only point: %+v", got)
	}
	if got := byDate["2026-01-02"]; !got.HasHarmonized || got.HarmonizedFRP != 150 {
		t.Fatalf("unexpected VIIRS-only scaled point: %+v", got)
	}
	if got := byDate["2024-12-31"]; !got.HasHarmonized || got.HarmonizedFRP != 125 || got.RobustZ == nil {
		t.Fatalf("expected circular DOY baseline across year boundary: %+v", got)
	}
	if got := byDate["2024-02-29"]; got.RobustZ != nil {
		t.Fatalf("expected sparse leap-day window to omit baseline Z-score, got %v", *got.RobustZ)
	}

	// Partial day outlier should not inflate baseline near 100-125, and itself stays high Z.
	part := byDate["2025-01-03"]
	if part.RobustZ == nil || *part.RobustZ < 5.0 {
		t.Fatalf("expected strong positive anomaly on spike day: %+v", part)
	}

	// Zero-value constant baseline must not divide by zero (minimum scale floor = 1.0).
	var zeroRows []RegionDayInput
	for i := 0; i < 12; i++ {
		zeroRows = append(zeroRows, RegionDayInput{
			Date:        utcDay(2015+i, 6, 15),
			ModisFRP:    f64Ptr(0),
			ViirsMeqFRP: f64Ptr(0),
		})
	}
	zeroPts := BuildDailySeries(zeroRows, 1.0, true)
	for _, p := range zeroPts {
		if p.RobustZ == nil || math.IsNaN(*p.RobustZ) || *p.RobustZ != 0 {
			t.Fatalf("expected zero Z-score on flat zero baseline, got %+v", p)
		}
	}
}
