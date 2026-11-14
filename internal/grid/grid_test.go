package grid

import (
	"math"
	"math/rand"
	"testing"
)

func TestReferenceVectors(t *testing.T) {
	cases := []struct {
		name string
		lat  float64
		lon  float64
		want int64
	}{
		{name: "origin", lat: 0.005, lon: 0.005, want: 360018000},
		{name: "antimeridian_east", lat: 61.0, lon: 179.99, want: 604017449},
		{name: "antimeridian_west", lat: 61.0, lon: -179.99, want: 604000000},
		{name: "south_pole", lat: -89.999, lon: 0.0, want: 1},
		{name: "north_pole", lat: 89.999, lon: 179.999, want: 719960002},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := CellID(tc.lat, tc.lon, ResA1)
			if got != tc.want {
				t.Fatalf("CellID(%v, %v, %v) = %d, want %d", tc.lat, tc.lon, ResA1, got, tc.want)
			}
		})
	}
}

func TestRandomContainmentAndEqualArea(t *testing.T) {
	rng := rand.New(rand.NewSource(20261114))
	const eps = 1e-9

	for i := 0; i < 10000; i++ {
		lat := rng.Float64()*179.998 - 89.999
		lon := rng.Float64()*359.998 - 179.999
		for _, res := range []float64{ResA1, ResA10} {
			id := CellID(lat, lon, res)
			lat0, lon0, lat1, lon1 := Bounds(id, res)
			if lat < lat0-eps || lat > lat1+eps {
				t.Fatalf("point (%v, %v) outside lat bounds [%v, %v] for res=%v", lat, lon, lat0, lat1, res)
			}
			if lon < lon0-eps || lon > lon1+eps {
				t.Fatalf("point (%v, %v) outside lon bounds [%v, %v] for res=%v", lon, lat, lon0, lon1, res)
			}
		}
	}

	area0 := AreaKm2(CellID(0.005, 0.005, ResA1), ResA1)
	area45 := AreaKm2(CellID(45.005, 10.005, ResA1), ResA1)
	area70 := AreaKm2(CellID(70.005, 10.005, ResA1), ResA1)

	for _, pair := range []struct {
		name string
		area float64
	}{
		{"45deg", area45},
		{"70deg", area70},
	} {
		relDiff := math.Abs(pair.area-area0) / area0
		if relDiff > 0.01 {
			t.Fatalf("area at %s = %.6f km2 differs by %.2f%% from equator %.6f km2", pair.name, pair.area, relDiff*100, area0)
		}
	}
}
