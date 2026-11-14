package normalize

import (
	"testing"
	"time"
)

func TestDetectSchema(t *testing.T) {
	modisNRT := []string{"latitude", "longitude", "brightness", "scan", "track", "acq_date", "acq_time", "satellite", "confidence", "version", "bright_t31", "frp", "daynight"}
	s, err := DetectSchema(modisNRT)
	if err != nil || s.Sensor != SensorMODIS || s.ColType != -1 {
		t.Fatalf("unexpected MODIS NRT schema: %+v, err=%v", s, err)
	}

	viirsArch := []string{"\ufefflatitude", "longitude", "bright_ti4", "scan", "track", "acq_date", "acq_time", "satellite", "instrument", "confidence", "version", "bright_ti5", "frp", "daynight", "type"}
	s2, err := DetectSchema(viirsArch)
	if err != nil || s2.Sensor != SensorVIIRS || s2.ColType != 14 {
		t.Fatalf("unexpected VIIRS archive schema: %+v, err=%v", s2, err)
	}

	if _, err := DetectSchema([]string{"latitude", "longitude", "frp"}); err == nil {
		t.Fatal("expected error for unknown header")
	}
}

func TestTimeZeroPaddingAndValidation(t *testing.T) {
	schema, err := DetectSchema([]string{"latitude", "longitude", "bright_ti4", "scan", "track", "acq_date", "acq_time", "satellite", "confidence", "version", "bright_ti5", "frp", "daynight"})
	if err != nil {
		t.Fatal(err)
	}

	good := []struct {
		raw  string
		want time.Time
	}{
		{"0", time.Date(2026, 11, 12, 0, 0, 0, 0, time.UTC)},
		{"6", time.Date(2026, 11, 12, 0, 6, 0, 0, time.UTC)},
		{"34", time.Date(2026, 11, 12, 0, 34, 0, 0, time.UTC)},
		{"2358", time.Date(2026, 11, 12, 23, 58, 0, 0, time.UTC)},
	}
	for _, tc := range good {
		row := []string{"31.5", "74.3", "310.0", "0.4", "0.4", "2026-11-12", tc.raw, "N21", "nominal", "2.0NRT", "290.0", "2.5", "D"}
		det, err := ParseRow(schema, row)
		if err != nil {
			t.Fatalf("ParseRow(acq_time=%q) unexpected error: %v", tc.raw, err)
		}
		if !det.AcqTS.Equal(tc.want) {
			t.Fatalf("acq_time=%q => %v, want %v", tc.raw, det.AcqTS, tc.want)
		}
	}

	for _, bad := range []string{"2460", "1260", "-1", "", "abc"} {
		row := []string{"31.5", "74.3", "310.0", "0.4", "0.4", "2026-11-12", bad, "N21", "nominal", "2.0NRT", "290.0", "2.5", "D"}
		if _, err := ParseRow(schema, row); err == nil {
			t.Fatalf("expected reject for acq_time=%q", bad)
		}
	}
}

func TestConfidenceMapping(t *testing.T) {
	modisCases := []struct {
		in   string
		want int16
	}{
		{"0", 1}, {"29", 1}, {"30", 2}, {"79", 2}, {"80", 3}, {"100", 3},
	}
	for _, tc := range modisCases {
		got, _, err := MapConfidence(SensorMODIS, tc.in)
		if err != nil || got != tc.want {
			t.Fatalf("MODIS MapConfidence(%q) = (%d, %v), want %d", tc.in, got, err, tc.want)
		}
	}
	for _, bad := range []string{"-1", "101", "abc", ""} {
		if _, _, err := MapConfidence(SensorMODIS, bad); err == nil {
			t.Fatalf("MODIS MapConfidence(%q) expected error", bad)
		}
	}

	viirsCases := []struct {
		in   string
		want int16
	}{
		{"l", 1}, {"LOW", 1}, {" nominal ", 2}, {"n", 2}, {"H", 3}, {"high", 3},
	}
	for _, tc := range viirsCases {
		got, _, err := MapConfidence(SensorVIIRS, tc.in)
		if err != nil || got != tc.want {
			t.Fatalf("VIIRS MapConfidence(%q) = (%d, %v), want %d", tc.in, got, err, tc.want)
		}
	}
	for _, bad := range []string{"x", "", "-5", "105"} {
		if _, _, err := MapConfidence(SensorVIIRS, bad); err == nil {
			t.Fatalf("VIIRS MapConfidence(%q) expected error", bad)
		}
	}
}

func TestArchiveStatusAndGuards(t *testing.T) {
	schema, err := DetectSchema([]string{"latitude", "longitude", "brightness", "scan", "track", "acq_date", "acq_time", "satellite", "instrument", "confidence", "version", "bright_t31", "frp", "daynight", "type"})
	if err != nil {
		t.Fatal(err)
	}
	row := []string{"28.3587", "70.1802", "307.9", "2.4", "1.5", "2018-01-01", "638", "Terra", "MODIS", "43", "6.03", "293.4", "0.0", "D", "0"}
	det, err := ParseRow(schema, row)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if det.Satellite != SatTerra || det.Status != StatusSP || det.ConfRank != 2 || det.FRPMW != 0.0 {
		t.Fatalf("unexpected normalized detection: %+v", det)
	}

	// Reject NaN/Inf and out-of-range temperatures
	badRows := [][]string{
		{"NaN", "70.18", "307.9", "2.4", "1.5", "2018-01-01", "638", "Terra", "MODIS", "43", "6.03", "293.4", "10", "D", "0"},
		{"28.35", "70.18", "700.0", "2.4", "1.5", "2018-01-01", "638", "Terra", "MODIS", "43", "6.03", "293.4", "10", "D", "0"},
		{"28.35", "70.18", "307.9", "0.0", "1.5", "2018-01-01", "638", "Terra", "MODIS", "43", "6.03", "293.4", "10", "D", "0"},
		{"28.35", "70.18", "307.9", "2.4", "1.5", "2018-01-01", "638", "Terra", "MODIS", "43", "6.03", "293.4", "-1", "D", "0"},
	}
	for i, b := range badRows {
		if _, err := ParseRow(schema, b); err == nil {
			t.Fatalf("case %d: expected rejection", i)
		}
	}
}
