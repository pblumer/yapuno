package skin

import (
	"math"
	"testing"
)

func TestContrastRatioKnownValues(t *testing.T) {
	cases := []struct {
		a, b string
		want float64
	}{
		{"#ffffff", "#000000", 21},
		{"#000000", "#ffffff", 21},
		{"#767676", "#ffffff", 4.54},
		{"#ffffff", "#ffffff", 1},
	}
	for _, c := range cases {
		got, err := ContrastRatio(c.a, c.b)
		if err != nil {
			t.Fatalf("%s/%s: %v", c.a, c.b, err)
		}
		if math.Abs(got-c.want) > 0.01 {
			t.Fatalf("%s/%s: got %.3f, want %.2f", c.a, c.b, got, c.want)
		}
	}
}

func TestContrastRatioRejectsInvalidColors(t *testing.T) {
	for _, bad := range []string{"", "white", "#fff", "#12345g", "#123456;x", "rgb(0,0,0)"} {
		if _, err := ContrastRatio(bad, "#ffffff"); err == nil {
			t.Fatalf("accepted invalid color %q", bad)
		}
	}
}
