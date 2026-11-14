package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
)

type apiErrorBody struct {
	Error string `json:"error"`
}

type queryFilter struct {
	RegionID    string
	RegionName  string
	IsGlobal    bool
	HasHistory  bool
	HistoryFrom *time.Time
	HistoryTo   *time.Time
	West        float64
	South       float64
	East        float64
	North       float64
	ConfLevel   int
	Scope       string
	From        time.Time
	To          time.Time
}

type httpStatusError struct {
	Status int
	Msg    string
}

func (e httpStatusError) Error() string { return e.Msg }

func writeJSON(w http.ResponseWriter, status int, payload any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	enc := json.NewEncoder(w)
	enc.SetEscapeHTML(false)
	_ = enc.Encode(payload)
}

func writeError(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, apiErrorBody{Error: msg})
}

func writeHTTPError(w http.ResponseWriter, err error) {
	var se httpStatusError
	if errors.As(err, &se) {
		writeError(w, se.Status, se.Msg)
		return
	}
	writeError(w, http.StatusInternalServerError, "internal server error")
}

func (s *Server) parseFilter(ctx context.Context, r *http.Request, defaultScope string, defaultConf int) (queryFilter, error) {
	q := r.URL.Query()
	f := queryFilter{
		RegionID:  "global",
		ConfLevel: defaultConf,
		Scope:     defaultScope,
	}

	if rawConf := strings.TrimSpace(q.Get("conf")); rawConf != "" {
		v, err := strconv.Atoi(rawConf)
		if err != nil || v < 1 || v > 3 {
			return f, httpStatusError{http.StatusBadRequest, fmt.Sprintf("invalid conf %q: expected 1, 2, or 3", rawConf)}
		}
		f.ConfLevel = v
	}

	if rawScope := strings.TrimSpace(strings.ToLower(q.Get("scope"))); rawScope != "" {
		switch rawScope {
		case "live", "history":
			f.Scope = rawScope
		default:
			return f, httpStatusError{http.StatusBadRequest, fmt.Sprintf("invalid scope %q: expected live or history", rawScope)}
		}
	}

	if rawReg := strings.TrimSpace(q.Get("region")); rawReg != "" {
		f.RegionID = rawReg
	}

	if rawBBox := strings.TrimSpace(q.Get("bbox")); rawBBox != "" {
		w, sLat, e, n, err := parseBBox(rawBBox)
		if err != nil {
			return f, httpStatusError{http.StatusBadRequest, err.Error()}
		}
		f.RegionID = "custom"
		f.RegionName = "Custom Area"
		f.West, f.South, f.East, f.North = w, sLat, e, n
		f.IsGlobal = w <= -180 && e >= 180 && sLat <= -90 && n >= 90
	} else {
		var hFrom, hTo *time.Time
		err := s.pool.QueryRow(ctx, `
			SELECT name, west, south, east, north, has_history, history_from, history_to
			FROM regions
			WHERE id = $1
		`, f.RegionID).Scan(&f.RegionName, &f.West, &f.South, &f.East, &f.North, &f.HasHistory, &hFrom, &hTo)
		if err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return f, httpStatusError{http.StatusBadRequest, fmt.Sprintf("unknown region %q", f.RegionID)}
			}
			return f, err
		}
		f.HistoryFrom = hFrom
		f.HistoryTo = hTo
		f.IsGlobal = f.RegionID == "global" || (f.West <= -180 && f.East >= 180 && f.South <= -90 && f.North >= 90)
	}

	fromStr := strings.TrimSpace(q.Get("from"))
	toStr := strings.TrimSpace(q.Get("to"))

	var fromDate, toDate time.Time
	var hasFrom, hasTo bool

	if fromStr != "" {
		d, err := parseUTCDate(fromStr)
		if err != nil {
			return f, httpStatusError{http.StatusBadRequest, fmt.Sprintf("invalid from %q: expected YYYY-MM-DD", fromStr)}
		}
		fromDate = d
		hasFrom = true
	}
	if toStr != "" {
		d, err := parseUTCDate(toStr)
		if err != nil {
			return f, httpStatusError{http.StatusBadRequest, fmt.Sprintf("invalid to %q: expected YYYY-MM-DD", toStr)}
		}
		toDate = d
		hasTo = true
	}

	if !hasFrom || !hasTo {
		defFrom, defTo, err := s.defaultDateBounds(ctx, f)
		if err != nil {
			return f, err
		}
		if !hasFrom {
			fromDate = defFrom
		}
		if !hasTo {
			toDate = defTo
		}
	}

	if fromDate.After(toDate) {
		return f, httpStatusError{http.StatusBadRequest, "invalid date range: from must be <= to"}
	}

	f.From = fromDate
	f.To = toDate
	return f, nil
}

func (s *Server) defaultDateBounds(ctx context.Context, f queryFilter) (time.Time, time.Time, error) {
	if f.Scope == "history" && f.HistoryFrom != nil && f.HistoryTo != nil {
		return truncUTC(*f.HistoryFrom), truncUTC(*f.HistoryTo), nil
	}

	lookupRegion := f.RegionID
	if lookupRegion == "custom" {
		lookupRegion = "global"
	}

	var minD, maxD *time.Time
	err := s.pool.QueryRow(ctx, `
		SELECT MIN(acq_date), MAX(acq_date)
		FROM region_day
		WHERE region_id = $1 AND conf_level = $2
	`, lookupRegion, f.ConfLevel).Scan(&minD, &maxD)
	if err != nil {
		return time.Time{}, time.Time{}, err
	}

	if maxD == nil {
		// Fall back to global region bounds before defaulting to today's UTC 48h window.
		err = s.pool.QueryRow(ctx, `
			SELECT MIN(acq_date), MAX(acq_date)
			FROM region_day
			WHERE conf_level = $1
		`, f.ConfLevel).Scan(&minD, &maxD)
		if err != nil {
			return time.Time{}, time.Time{}, err
		}
	}

	if maxD == nil {
		today := truncUTC(time.Now().UTC())
		return today.AddDate(0, 0, -2), today, nil
	}

	maxDay := truncUTC(*maxD)
	if f.Scope == "history" && minD != nil {
		return truncUTC(*minD), maxDay, nil
	}
	defFrom := maxDay.AddDate(0, 0, -2)
	if minD != nil && truncUTC(*minD).After(defFrom) {
		defFrom = truncUTC(*minD)
	}
	return defFrom, maxDay, nil
}

func (f queryFilter) bboxSQL(geomCol string, startIdx int) (string, []any) {
	if f.IsGlobal {
		return "TRUE", nil
	}
	if f.West <= f.East {
		expr := fmt.Sprintf("ST_Intersects(%s, ST_MakeEnvelope($%d, $%d, $%d, $%d, 4326))",
			geomCol, startIdx, startIdx+1, startIdx+2, startIdx+3)
		return expr, []any{f.West, f.South, f.East, f.North}
	}
	expr := fmt.Sprintf("ST_Intersects(%s, make_bbox_multipolygon($%d, $%d, $%d, $%d))",
		geomCol, startIdx, startIdx+1, startIdx+2, startIdx+3)
	return expr, []any{f.West, f.South, f.East, f.North}
}

func parseBBox(raw string) (float64, float64, float64, float64, error) {
	parts := strings.Split(raw, ",")
	if len(parts) != 4 {
		return 0, 0, 0, 0, errors.New("invalid bbox: expected west,south,east,north")
	}
	var vals [4]float64
	for i, part := range parts {
		v, err := strconv.ParseFloat(strings.TrimSpace(part), 64)
		if err != nil || math.IsNaN(v) || math.IsInf(v, 0) {
			return 0, 0, 0, 0, fmt.Errorf("invalid bbox coordinate %q", part)
		}
		vals[i] = v
	}
	w, s, e, n := vals[0], vals[1], vals[2], vals[3]
	if w < -180 || w > 180 || e < -180 || e > 180 {
		return 0, 0, 0, 0, errors.New("invalid bbox: longitude must be within [-180, 180]")
	}
	if s < -90 || s > 90 || n < -90 || n > 90 {
		return 0, 0, 0, 0, errors.New("invalid bbox: latitude must be within [-90, 90]")
	}
	if s >= n {
		return 0, 0, 0, 0, errors.New("invalid bbox: south must be < north")
	}
	return w, s, e, n, nil
}

func parseUTCDate(raw string) (time.Time, error) {
	t, err := time.Parse("2006-01-02", raw)
	if err != nil {
		return time.Time{}, err
	}
	return truncUTC(t), nil
}

func truncUTC(t time.Time) time.Time {
	u := t.UTC()
	return time.Date(u.Year(), u.Month(), u.Day(), 0, 0, 0, 0, time.UTC)
}
