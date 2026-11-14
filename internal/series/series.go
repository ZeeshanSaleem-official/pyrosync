package series

import (
	"math"
	"math/rand/v2"
	"sort"
	"time"
)

const (
	// MinCalibrationDays is the minimum number of full overlap days required for a verified gain factor.
	MinCalibrationDays = 14
	// MaxRelCIWidth caps the relative 95% confidence interval width (ciHigh - ciLow) / k for reliability.
	MaxRelCIWidth = 0.50
	// BootstrapResamples is the number of resamples used to estimate the gain confidence interval.
	BootstrapResamples = 1000
	// AnomalyWindowHalfDays is the circular +/- day-of-year window around a date for seasonal baselines.
	AnomalyWindowHalfDays = 7
	// MinBaselineSamples is the minimum non-partial baseline days required before emitting a Z-score.
	MinBaselineSamples = 10
)

// OverlapSample represents one non-partial day where both sensors recorded fire radiative power.
type OverlapSample struct {
	Date        time.Time
	ModisFRP    float64
	ViirsMeqFRP float64
}

// CalibrationModel holds empirical MODIS/VIIRS gain estimates for one region and scope.
type CalibrationModel struct {
	RegionID      string
	Scope         string
	K             float64
	CILow         float64
	CIHigh        float64
	NDays         int
	Reliable      bool
	ParamsVersion int
	KApplied      float64
}

// RegionDayInput represents one row from region_day.
type RegionDayInput struct {
	Date          time.Time
	ModisFRP      *float64
	ViirsAllFRP   *float64
	ViirsMeqFRP   *float64
	ModisPx       int
	ViirsPx       int
	CellsModis    int
	CellsViirsMeq int
	CellsUnion    int
	SatsModis     int16
	SatsViirs     int16
	IsPartial     bool
}

// DailyPoint holds the computed Stage-D series metrics for one covered date.
type DailyPoint struct {
	Date          time.Time
	ModisFRP      *float64
	ViirsAllFRP   *float64
	ViirsMeqFRP   *float64
	HarmonizedFRP float64
	HasHarmonized bool
	RobustZ       *float64
	ModisPx       int
	ViirsPx       int
	CellsModis    int
	CellsViirsMeq int
	CellsUnion    int
	SatsModis     int16
	SatsViirs     int16
	IsPartial     bool
}

// FitCalibration computes the median MODIS/VIIRS gain k and 1000-resample bootstrap 95% CI.
func FitCalibration(regionID, scope string, paramsVersion int, samples []OverlapSample) CalibrationModel {
	valid := make([]OverlapSample, 0, len(samples))
	for _, s := range samples {
		if !isFinitePos(s.ModisFRP) || !isFinitePos(s.ViirsMeqFRP) {
			continue
		}
		valid = append(valid, s)
	}

	sort.Slice(valid, func(i, j int) bool {
		return valid[i].Date.Before(valid[j].Date)
	})

	ratios := make([]float64, 0, len(valid))
	for _, s := range valid {
		r := s.ModisFRP / s.ViirsMeqFRP
		if isFinitePos(r) {
			ratios = append(ratios, r)
		}
	}

	out := CalibrationModel{
		RegionID:      regionID,
		Scope:         scope,
		K:             1.0,
		CILow:         1.0,
		CIHigh:        1.0,
		NDays:         len(ratios),
		Reliable:      false,
		ParamsVersion: paramsVersion,
		KApplied:      1.0,
	}

	if len(ratios) == 0 {
		return out
	}

	med, ok := Median(ratios)
	if !ok {
		return out
	}
	out.K = med

	if len(ratios) == 1 {
		out.CILow = med
		out.CIHigh = med
		return out
	}

	rng := rand.New(rand.NewPCG(0x5079726F, 0x53796E63))
	bootMedians := make([]float64, BootstrapResamples)
	sampleBuf := make([]float64, len(ratios))
	n := len(ratios)

	for b := 0; b < BootstrapResamples; b++ {
		for i := 0; i < n; i++ {
			sampleBuf[i] = ratios[rng.IntN(n)]
		}
		sort.Float64s(sampleBuf)
		bootMedians[b] = medianSorted(sampleBuf)
	}

	sort.Float64s(bootMedians)
	out.CILow = QuantileSorted(bootMedians, 0.025)
	out.CIHigh = QuantileSorted(bootMedians, 0.975)

	relWidth := (out.CIHigh - out.CILow) / out.K
	if out.NDays >= MinCalibrationDays && out.K > 0 && relWidth <= MaxRelCIWidth {
		out.Reliable = true
		out.KApplied = out.K
	}

	return out
}

// HarmonizedDailyFRP combines regional MODIS and MODIS-equivalent VIIRS daily FRP.
func HarmonizedDailyFRP(modisFRP, viirsMeqFRP *float64, kApplied float64) (float64, bool) {
	hasM := modisFRP != nil && !math.IsNaN(*modisFRP) && !math.IsInf(*modisFRP, 0)
	hasV := viirsMeqFRP != nil && !math.IsNaN(*viirsMeqFRP) && !math.IsInf(*viirsMeqFRP, 0)

	switch {
	case hasM && hasV:
		return 0.5 * (*modisFRP + kApplied**viirsMeqFRP), true
	case hasM:
		return *modisFRP, true
	case hasV:
		return kApplied * *viirsMeqFRP, true
	default:
		return 0, false
	}
}

// BuildDailySeries converts region_day rows into Stage-D harmonized daily series points
// with seasonal robust Z-score anomalies computed over non-partial days in DOY +/- 7 windows.
func BuildDailySeries(rows []RegionDayInput, kApplied float64, computeAnomalies bool) []DailyPoint {
	ordered := make([]RegionDayInput, len(rows))
	copy(ordered, rows)
	sort.Slice(ordered, func(i, j int) bool {
		return ordered[i].Date.Before(ordered[j].Date)
	})

	points := make([]DailyPoint, len(ordered))
	type baseItem struct {
		doy365 int
		val    float64
	}
	var baseline []baseItem

	for i, row := range ordered {
		h, ok := HarmonizedDailyFRP(row.ModisFRP, row.ViirsMeqFRP, kApplied)
		points[i] = DailyPoint{
			Date:          row.Date,
			ModisFRP:      copyFloatPtr(row.ModisFRP),
			ViirsAllFRP:   copyFloatPtr(row.ViirsAllFRP),
			ViirsMeqFRP:   copyFloatPtr(row.ViirsMeqFRP),
			HarmonizedFRP: h,
			HasHarmonized: ok,
			ModisPx:       row.ModisPx,
			ViirsPx:       row.ViirsPx,
			CellsModis:    row.CellsModis,
			CellsViirsMeq: row.CellsViirsMeq,
			CellsUnion:    row.CellsUnion,
			SatsModis:     row.SatsModis,
			SatsViirs:     row.SatsViirs,
			IsPartial:     row.IsPartial,
		}
		if computeAnomalies && ok && !row.IsPartial {
			baseline = append(baseline, baseItem{
				doy365: dayOfYear365(row.Date),
				val:    h,
			})
		}
	}

	if !computeAnomalies || len(baseline) < MinBaselineSamples {
		return points
	}

	var windowVals []float64
	var absDevs []float64

	for i := range points {
		if !points[i].HasHarmonized {
			continue
		}
		targetDOY := dayOfYear365(points[i].Date)
		windowVals = windowVals[:0]
		for _, item := range baseline {
			if circularDayDiff365(targetDOY, item.doy365) <= AnomalyWindowHalfDays {
				windowVals = append(windowVals, item.val)
			}
		}
		if len(windowVals) < MinBaselineSamples {
			continue
		}

		sort.Float64s(windowVals)
		med := medianSorted(windowVals)

		absDevs = absDevs[:0]
		for _, v := range windowVals {
			absDevs = append(absDevs, math.Abs(v-med))
		}
		sort.Float64s(absDevs)
		mad := medianSorted(absDevs)

		scale := math.Max(1.4826*mad, math.Max(0.05*med, 1.0))
		z := (points[i].HarmonizedFRP - med) / scale
		points[i].RobustZ = &z
	}

	return points
}

// Median computes the median of vals without mutating the input slice.
func Median(vals []float64) (float64, bool) {
	if len(vals) == 0 {
		return 0, false
	}
	cp := make([]float64, len(vals))
	copy(cp, vals)
	sort.Float64s(cp)
	return medianSorted(cp), true
}

// QuantileSorted returns the q-th quantile (q in [0, 1]) of an already sorted ascending slice.
func QuantileSorted(sorted []float64, q float64) float64 {
	if len(sorted) == 0 {
		return 0
	}
	if len(sorted) == 1 || q <= 0 {
		return sorted[0]
	}
	if q >= 1 {
		return sorted[len(sorted)-1]
	}
	pos := q * float64(len(sorted)-1)
	lo := int(math.Floor(pos))
	hi := int(math.Ceil(pos))
	if lo == hi {
		return sorted[lo]
	}
	frac := pos - float64(lo)
	return sorted[lo]*(1-frac) + sorted[hi]*frac
}

func medianSorted(sorted []float64) float64 {
	n := len(sorted)
	mid := n / 2
	if n%2 == 1 {
		return sorted[mid]
	}
	return 0.5 * (sorted[mid-1] + sorted[mid])
}

func dayOfYear365(t time.Time) int {
	u := t.UTC()
	m, d := u.Month(), u.Day()
	if m == time.February && d == 29 {
		d = 28
	}
	return time.Date(2001, m, d, 0, 0, 0, 0, time.UTC).YearDay()
}

func circularDayDiff365(a, b int) int {
	diff := a - b
	if diff < 0 {
		diff = -diff
	}
	if diff > 365-diff {
		return 365 - diff
	}
	return diff
}

func isFinitePos(v float64) bool {
	return !math.IsNaN(v) && !math.IsInf(v, 0) && v > 0
}

func copyFloatPtr(p *float64) *float64 {
	if p == nil {
		return nil
	}
	v := *p
	return &v
}
