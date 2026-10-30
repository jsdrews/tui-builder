package build

import "testing"

// si sorts plain numbers, K/M/B/G/T (B is billions), binary units
// (Ki, MiB) and byte units (kB, MB) by size.
func TestParseSI(t *testing.T) {
	for in, want := range map[string]float64{
		"":       0,
		"42":     42,
		"1,024":  0, // commas are number sort's, not si's
		"1.5K":   1500,
		"3M":     3e6,
		"1.2B":   1.2e9,
		"2G":     2e9,
		"1T":     1e12,
		"12Ki":   12 * 1024,
		"3.5Mi":  3.5 * 1024 * 1024,
		"2GiB":   2 * 1024 * 1024 * 1024,
		"900kB":  900e3,
		"187MB":  187e6,
		"4.2 MB": 4.2e6,
		"-3k":    -3000,
		"lots":   0,
	} {
		if got := parseSI(in); got != want {
			t.Errorf("parseSI(%q) = %v, want %v", in, got, want)
		}
	}
	less := parseLess("si")
	if !less("900kB", "4.2MB") || !less("12Ki", "1Mi") || less("187MB", "4.2MB") {
		t.Error("si doesn't order sizes")
	}
}
