package skin

import (
	"fmt"
	"math"
	"strconv"
)

// Minimum WCAG 2.x contrast ratios enforced for every skin. Text includes
// small labels, so the stricter normal-text threshold applies throughout.
const (
	MinTextContrast    = 4.5
	MinNonTextContrast = 3.0
)

// ContrastRatio returns the WCAG 2.x contrast ratio between two #rrggbb colors.
func ContrastRatio(a, b string) (float64, error) {
	la, err := relativeLuminance(a)
	if err != nil {
		return 0, err
	}
	lb, err := relativeLuminance(b)
	if err != nil {
		return 0, err
	}
	if la < lb {
		la, lb = lb, la
	}
	return (la + 0.05) / (lb + 0.05), nil
}

func relativeLuminance(hex string) (float64, error) {
	r, g, b, err := parseHex(hex)
	if err != nil {
		return 0, err
	}
	channel := func(v uint8) float64 {
		c := float64(v) / 255
		if c <= 0.03928 {
			return c / 12.92
		}
		return math.Pow((c+0.055)/1.055, 2.4)
	}
	return 0.2126*channel(r) + 0.7152*channel(g) + 0.0722*channel(b), nil
}

func parseHex(hex string) (r, g, b uint8, err error) {
	if !hexColor.MatchString(hex) {
		return 0, 0, 0, fmt.Errorf("%w: color %q is not #rrggbb", ErrInvalidSkin, hex)
	}
	v, err := strconv.ParseUint(hex[1:], 16, 32)
	if err != nil {
		return 0, 0, 0, fmt.Errorf("%w: color %q", ErrInvalidSkin, hex)
	}
	return uint8(v >> 16), uint8(v >> 8), uint8(v), nil
}
