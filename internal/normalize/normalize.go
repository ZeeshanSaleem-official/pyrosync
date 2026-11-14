package normalize

import (
	"errors"
	"fmt"
	"math"
	"strconv"
	"strings"
	"time"

	"pyrosync/internal/grid"
)

type SensorFamily string

const (
	SensorMODIS SensorFamily = "MODIS"
	SensorVIIRS SensorFamily = "VIIRS"
)

type SatelliteCode string

const (
	SatTerra  SatelliteCode = "TERRA"
	SatAqua   SatelliteCode = "AQUA"
	SatSNPP   SatelliteCode = "SNPP"
	SatNOAA20 SatelliteCode = "NOAA20"
	SatNOAA21 SatelliteCode = "NOAA21"
)

type ProductStatus string

const (
	StatusNRT ProductStatus = "NRT"
	StatusSP  ProductStatus = "SP"
)

// Schema maps required CSV column indices resolved from the header row.
type Schema struct {
	Sensor   SensorFamily
	MinCols  int
	ColLat   int
	ColLon   int
	ColMWIR  int
	ColLWIR  int
	ColScan  int
	ColTrack int
	ColDate  int
	ColTime  int
	ColSat   int
	ColConf  int
	ColVer   int
	ColFRP   int
	ColDN    int
	ColType  int // optional; -1 when absent
}

// Detection holds one validated, canonical active-fire observation.
type Detection struct {
	Sensor     SensorFamily
	Satellite  SatelliteCode
	LatE5      int32
	LonE5      int32
	BTMWIRK    float32
	BTLWIRK    float32
	ScanKm     float32
	TrackKm    float32
	AcqTS      time.Time
	AcqDate    time.Time
	ConfRank   int16
	ConfRaw    string
	FRPMW      float32
	DayNight   string
	Version    string
	Status     ProductStatus
	SourceType int16
	CellA1     int64
	CellA10    int64
}

// RejectError carries a short categorical reason code for failed rows.
type RejectError struct {
	Reason string
	Detail string
}

func (e *RejectError) Error() string {
	if e.Detail == "" {
		return e.Reason
	}
	return e.Reason + ": " + e.Detail
}

func reject(reason, detail string) error {
	return &RejectError{Reason: reason, Detail: detail}
}

// DetectSchema determines MODIS vs VIIRS format from CSV header column names.
func DetectSchema(header []string) (*Schema, error) {
	if len(header) == 0 {
		return nil, errors.New("empty CSV header")
	}

	cols := make(map[string]int, len(header))
	for i, raw := range header {
		name := strings.TrimSpace(raw)
		if i == 0 {
			name = strings.TrimPrefix(name, "\ufeff")
		}
		name = strings.ToLower(name)
		if name != "" {
			cols[name] = i
		}
	}

	_, hasT31 := cols["bright_t31"]
	_, hasBright := cols["brightness"]
	_, hasTI4 := cols["bright_ti4"]
	_, hasTI5 := cols["bright_ti5"]

	var sensor SensorFamily
	var mwirName, lwirName string
	switch {
	case hasT31 && hasBright && !hasTI4:
		sensor = SensorMODIS
		mwirName = "brightness"
		lwirName = "bright_t31"
	case hasTI4 && hasTI5 && !hasT31:
		sensor = SensorVIIRS
		mwirName = "bright_ti4"
		lwirName = "bright_ti5"
	default:
		return nil, fmt.Errorf("unrecognized FIRMS header: %v", header)
	}

	need := func(name string) (int, error) {
		idx, ok := cols[name]
		if !ok {
			return -1, fmt.Errorf("missing required column %q in %s CSV header", name, sensor)
		}
		return idx, nil
	}

	s := &Schema{Sensor: sensor, ColType: -1}
	var err error
	if s.ColLat, err = need("latitude"); err != nil {
		return nil, err
	}
	if s.ColLon, err = need("longitude"); err != nil {
		return nil, err
	}
	if s.ColMWIR, err = need(mwirName); err != nil {
		return nil, err
	}
	if s.ColLWIR, err = need(lwirName); err != nil {
		return nil, err
	}
	if s.ColScan, err = need("scan"); err != nil {
		return nil, err
	}
	if s.ColTrack, err = need("track"); err != nil {
		return nil, err
	}
	if s.ColDate, err = need("acq_date"); err != nil {
		return nil, err
	}
	if s.ColTime, err = need("acq_time"); err != nil {
		return nil, err
	}
	if s.ColSat, err = need("satellite"); err != nil {
		return nil, err
	}
	if s.ColConf, err = need("confidence"); err != nil {
		return nil, err
	}
	if s.ColVer, err = need("version"); err != nil {
		return nil, err
	}
	if s.ColFRP, err = need("frp"); err != nil {
		return nil, err
	}
	if s.ColDN, err = need("daynight"); err != nil {
		return nil, err
	}
	if idx, ok := cols["type"]; ok {
		s.ColType = idx
	}

	for _, idx := range []int{
		s.ColLat, s.ColLon, s.ColMWIR, s.ColLWIR, s.ColScan, s.ColTrack,
		s.ColDate, s.ColTime, s.ColSat, s.ColConf, s.ColVer, s.ColFRP, s.ColDN,
	} {
		if idx+1 > s.MinCols {
			s.MinCols = idx + 1
		}
	}
	return s, nil
}

// ParseRow validates and normalizes one CSV data record against schema.
func ParseRow(schema *Schema, fields []string) (Detection, error) {
	if len(fields) < schema.MinCols {
		return Detection{}, reject("short_row", fmt.Sprintf("got %d columns, need >= %d", len(fields), schema.MinCols))
	}

	lat, err := parseFiniteFloat(fields[schema.ColLat])
	if err != nil || lat < -90.0 || lat > 90.0 {
		return Detection{}, reject("invalid_latitude", fields[schema.ColLat])
	}
	lon, err := parseFiniteFloat(fields[schema.ColLon])
	if err != nil || lon < -180.0 || lon > 180.0 {
		return Detection{}, reject("invalid_longitude", fields[schema.ColLon])
	}

	mwir, err := parseFiniteFloat(fields[schema.ColMWIR])
	if err != nil || mwir < 150.0 || mwir > 650.0 {
		return Detection{}, reject("invalid_bt_mwir", fields[schema.ColMWIR])
	}
	lwir, err := parseFiniteFloat(fields[schema.ColLWIR])
	if err != nil || lwir < 150.0 || lwir > 650.0 {
		return Detection{}, reject("invalid_bt_lwir", fields[schema.ColLWIR])
	}

	scan, err := parseFiniteFloat(fields[schema.ColScan])
	if err != nil || scan <= 0.0 || scan > 10.0 {
		return Detection{}, reject("invalid_scan", fields[schema.ColScan])
	}
	track, err := parseFiniteFloat(fields[schema.ColTrack])
	if err != nil || track <= 0.0 || track > 10.0 {
		return Detection{}, reject("invalid_track", fields[schema.ColTrack])
	}

	acqTS, acqDate, err := parseUTCTimestamp(fields[schema.ColDate], fields[schema.ColTime])
	if err != nil {
		return Detection{}, err
	}

	sat, err := MapSatellite(schema.Sensor, fields[schema.ColSat])
	if err != nil {
		return Detection{}, err
	}

	confRank, confRaw, err := MapConfidence(schema.Sensor, fields[schema.ColConf])
	if err != nil {
		return Detection{}, err
	}

	frp, err := parseFiniteFloat(fields[schema.ColFRP])
	if err != nil || frp < 0.0 {
		return Detection{}, reject("invalid_frp", fields[schema.ColFRP])
	}

	dn := strings.ToUpper(strings.TrimSpace(fields[schema.ColDN]))
	if dn != "D" && dn != "N" {
		return Detection{}, reject("invalid_daynight", fields[schema.ColDN])
	}

	ver := strings.TrimSpace(fields[schema.ColVer])
	if ver == "" {
		return Detection{}, reject("invalid_version", "empty")
	}
	status := StatusSP
	if strings.HasSuffix(strings.ToUpper(ver), "NRT") {
		status = StatusNRT
	}

	var srcType int16
	if schema.ColType >= 0 && schema.ColType < len(fields) {
		rawType := strings.TrimSpace(fields[schema.ColType])
		if rawType != "" {
			tVal, err := strconv.Atoi(rawType)
			if err != nil || tVal < 0 || tVal > 9 {
				return Detection{}, reject("invalid_source_type", rawType)
			}
			srcType = int16(tVal)
		}
	}

	return Detection{
		Sensor:     schema.Sensor,
		Satellite:  sat,
		LatE5:      int32(math.Round(lat * 1e5)),
		LonE5:      int32(math.Round(lon * 1e5)),
		BTMWIRK:    float32(mwir),
		BTLWIRK:    float32(lwir),
		ScanKm:     float32(scan),
		TrackKm:    float32(track),
		AcqTS:      acqTS,
		AcqDate:    acqDate,
		ConfRank:   confRank,
		ConfRaw:    confRaw,
		FRPMW:      float32(frp),
		DayNight:   dn,
		Version:    ver,
		Status:     status,
		SourceType: srcType,
		CellA1:     grid.CellID(lat, lon, grid.ResA1),
		CellA10:    grid.CellID(lat, lon, grid.ResA10),
	}, nil
}

// MapSatellite resolves FIRMS satellite codes (both NRT short codes and SP archive names).
func MapSatellite(sensor SensorFamily, raw string) (SatelliteCode, error) {
	norm := strings.ToUpper(strings.TrimSpace(raw))
	switch norm {
	case "T", "TERRA":
		if sensor != SensorMODIS {
			return "", reject("satellite_sensor_mismatch", raw)
		}
		return SatTerra, nil
	case "A", "AQUA":
		if sensor != SensorMODIS {
			return "", reject("satellite_sensor_mismatch", raw)
		}
		return SatAqua, nil
	case "N", "NP", "SNPP", "S-NPP", "NPP":
		if sensor != SensorVIIRS {
			return "", reject("satellite_sensor_mismatch", raw)
		}
		return SatSNPP, nil
	case "N20", "J1", "JPSS1", "JPSS-1", "NOAA20", "NOAA-20":
		if sensor != SensorVIIRS {
			return "", reject("satellite_sensor_mismatch", raw)
		}
		return SatNOAA20, nil
	case "N21", "J2", "JPSS2", "JPSS-2", "NOAA21", "NOAA-21":
		if sensor != SensorVIIRS {
			return "", reject("satellite_sensor_mismatch", raw)
		}
		return SatNOAA21, nil
	default:
		return "", reject("unknown_satellite", raw)
	}
}

// MapConfidence normalizes MODIS 0-100 scores and VIIRS categorical strings into ranks 1..3.
func MapConfidence(sensor SensorFamily, raw string) (int16, string, error) {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return 0, "", reject("invalid_confidence", "empty")
	}

	if sensor == SensorMODIS {
		val, err := strconv.Atoi(trimmed)
		if err != nil || val < 0 || val > 100 {
			return 0, "", reject("invalid_confidence", raw)
		}
		switch {
		case val < 30:
			return 1, trimmed, nil
		case val < 80:
			return 2, trimmed, nil
		default:
			return 3, trimmed, nil
		}
	}

	lower := strings.ToLower(trimmed)
	switch lower {
	case "l", "low":
		return 1, lower, nil
	case "n", "nominal":
		return 2, lower, nil
	case "h", "high":
		return 3, lower, nil
	default:
		// Some older experimental VIIRS products report numeric confidence 0-100.
		if val, err := strconv.Atoi(trimmed); err == nil && val >= 0 && val <= 100 {
			switch {
			case val < 30:
				return 1, trimmed, nil
			case val < 80:
				return 2, trimmed, nil
			default:
				return 3, trimmed, nil
			}
		}
		return 0, "", reject("invalid_confidence", raw)
	}
}

func parseUTCTimestamp(rawDate, rawTime string) (time.Time, time.Time, error) {
	dStr := strings.TrimSpace(rawDate)
	d, err := time.Parse("2006-01-02", dStr)
	if err != nil {
		return time.Time{}, time.Time{}, reject("invalid_acq_date", rawDate)
	}

	tStr := strings.TrimSpace(rawTime)
	if tStr == "" {
		return time.Time{}, time.Time{}, reject("invalid_acq_time", "empty")
	}
	tVal, err := strconv.Atoi(tStr)
	if err != nil || tVal < 0 || tVal > 2359 {
		return time.Time{}, time.Time{}, reject("invalid_acq_time", rawTime)
	}
	padded := fmt.Sprintf("%04d", tVal)
	hh, _ := strconv.Atoi(padded[:2])
	mm, _ := strconv.Atoi(padded[2:])
	if hh > 23 || mm > 59 {
		return time.Time{}, time.Time{}, reject("invalid_acq_time", rawTime)
	}

	ts := time.Date(d.Year(), d.Month(), d.Day(), hh, mm, 0, 0, time.UTC)
	dateOnly := time.Date(d.Year(), d.Month(), d.Day(), 0, 0, 0, 0, time.UTC)
	return ts, dateOnly, nil
}

func parseFiniteFloat(raw string) (float64, error) {
	v, err := strconv.ParseFloat(strings.TrimSpace(raw), 64)
	if err != nil || math.IsNaN(v) || math.IsInf(v, 0) {
		return 0, errors.New("non-finite float")
	}
	return v, nil
}
