package grid

import "math"

const (
	// ResA1 is the fine analysis grid latitude step (~1.11 km height, ~1.23 km^2 area).
	ResA1 = 0.01
	// ResA10 is the coarse regional grid latitude step (~11.1 km height, ~123 km^2 area).
	ResA10 = 0.10

	// MaxCols is the row stride used to pack (row, col) into an int64 cell ID.
	MaxCols = 40000

	earthRadiusKm = 6371.0088
)

func numRows(res float64) int {
	return int(math.Round(180.0 / res))
}

func rowOf(lat, res float64) int {
	r := int(math.Floor((lat+90.0)/res + 1e-9))
	if r < 0 {
		return 0
	}
	maxRow := numRows(res) - 1
	if r > maxRow {
		return maxRow
	}
	return r
}

func colsInRow(row int, res float64) int {
	latC := (float64(row)+0.5)*res - 90.0
	n := int(math.Floor(360.0*math.Cos(latC*math.Pi/180.0)/res + 0.5))
	if n < 1 {
		return 1
	}
	return n
}

// CellID maps WGS84 (lat, lon) to a deterministic equal-area cosine-grid cell ID.
func CellID(lat, lon, res float64) int64 {
	row := rowOf(lat, res)
	n := colsInRow(row, res)
	step := 360.0 / float64(n)
	col := int(math.Floor((lon + 180.0) / step))
	if col < 0 {
		col = 0
	}
	if col > n-1 {
		col = n - 1
	}
	return int64(row)*MaxCols + int64(col)
}

// Bounds returns the (lat0, lon0, lat1, lon1) WGS84 bounding box for cell id.
func Bounds(id int64, res float64) (lat0, lon0, lat1, lon1 float64) {
	row := int(id / MaxCols)
	col := int(id % MaxCols)
	n := colsInRow(row, res)
	step := 360.0 / float64(n)
	lat0 = float64(row)*res - 90.0
	lat1 = lat0 + res
	lon0 = float64(col)*step - 180.0
	lon1 = lon0 + step
	return lat0, lon0, lat1, lon1
}

// Center returns the geographic center (clat, clon) of cell id.
func Center(id int64, res float64) (clat, clon float64) {
	lat0, lon0, lat1, lon1 := Bounds(id, res)
	return (lat0 + lat1) / 2.0, (lon0 + lon1) / 2.0
}

// AreaKm2 computes the spherical quadrangle surface area in km^2 for cell id.
func AreaKm2(id int64, res float64) float64 {
	lat0, lon0, lat1, lon1 := Bounds(id, res)
	dlon := (lon1 - lon0) * math.Pi / 180.0
	return earthRadiusKm * earthRadiusKm * dlon *
		(math.Sin(lat1*math.Pi/180.0) - math.Sin(lat0*math.Pi/180.0))
}
