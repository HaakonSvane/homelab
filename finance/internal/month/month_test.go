package month

import (
	"errors"
	"testing"
	"time"
)

func TestParse(t *testing.T) {
	cases := []struct {
		in      string
		year    int
		month   time.Month
		wantErr bool
	}{
		{"2026-01", 2026, time.January, false},
		{"2026-12", 2026, time.December, false},
		{"1999-07", 1999, time.July, false},
		{"2026-00", 0, 0, true},
		{"2026-13", 0, 0, true},
		{"2026-1", 0, 0, true},
		{"26-01", 0, 0, true},
		{"2026/01", 0, 0, true},
		{"0999-01", 0, 0, true},
		{"2026-0a", 0, 0, true},
		{"", 0, 0, true},
	}
	for _, c := range cases {
		m, err := Parse(c.in)
		if c.wantErr {
			if !errors.Is(err, ErrSyntax) {
				t.Errorf("Parse(%q) err = %v, want ErrSyntax", c.in, err)
			}
			continue
		}
		if err != nil || m.Year() != c.year || m.Month() != c.month || m.String() != c.in {
			t.Errorf("Parse(%q) = %v (%d, %v), %v", c.in, m, m.Year(), m.Month(), err)
		}
	}
}

func TestArithmeticAndOrder(t *testing.T) {
	nov := MustParse("2026-11")
	if got := nov.Add(2).String(); got != "2027-01" {
		t.Errorf("2026-11 + 2 = %s", got)
	}
	if got := nov.Add(-11).String(); got != "2025-12" {
		t.Errorf("2026-11 - 11 = %s", got)
	}
	if !(MustParse("2026-09") < MustParse("2026-10") && MustParse("2026-12") < MustParse("2027-01")) {
		t.Error("months must order chronologically")
	}
	oslo, _ := time.LoadLocation("Europe/Oslo")
	// 23:30 UTC on 31 Dec is already January in Oslo.
	if got := Of(time.Date(2026, 12, 31, 23, 30, 0, 0, time.UTC).In(oslo)).String(); got != "2027-01" {
		t.Errorf("Of(new year's eve in Oslo) = %s", got)
	}
	if got := nov.Label(); got != "november 2026" {
		t.Errorf("Label = %q", got)
	}
}

func TestScanValue(t *testing.T) {
	var m Month
	for _, src := range []any{"2026-03", []byte("2026-03")} {
		if err := m.Scan(src); err != nil || m != MustParse("2026-03") {
			t.Errorf("Scan(%v) = %v, %v", src, m, err)
		}
	}
	if err := m.Scan(int64(5)); err == nil {
		t.Error("Scan(int64) should fail")
	}
	if v, _ := MustParse("2026-03").Value(); v != "2026-03" {
		t.Errorf("Value = %v", v)
	}
}

func TestMask(t *testing.T) {
	cases := []struct {
		name     string
		mask     Mask
		count    int
		has      []time.Month
		hasNot   []time.Month
		describe string
	}{
		{"monthly", Monthly, 12, []time.Month{1, 7, 12}, nil, "Månedlig"},
		{"monthly is 4095", 4095, 12, nil, nil, "Månedlig"},
		{"yearly insurance (1 bit)", Yearly(time.March), 1, []time.Month{3}, []time.Month{1, 2, 4, 12}, "Årlig (mars)"},
		{"municipal tax (2 bits)", Semiannual(time.January), 2, []time.Month{1, 7}, []time.Month{2, 6, 8}, "Halvårlig (jan og jul)"},
		{"semiannual wraps the year", Semiannual(time.October), 2, []time.Month{10, 4}, []time.Month{1, 7}, "Halvårlig (apr og okt)"},
		{"kindergarten (11 bits)", ElevenMonths(time.July), 11, []time.Month{1, 6, 8, 12}, []time.Month{7}, "Alle måneder unntatt juli"},
		{"quarterly", MaskOf(1, 4, 7, 10), 4, []time.Month{1, 4, 7, 10}, []time.Month{2, 12}, "jan, apr, jul og okt"},
		{"two months not six apart", MaskOf(2, 5), 2, []time.Month{2, 5}, nil, "feb og mai"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if !c.mask.Valid() {
				t.Fatalf("mask %b not valid", c.mask)
			}
			if got := c.mask.Count(); got != c.count {
				t.Errorf("Count = %d, want %d", got, c.count)
			}
			for _, m := range c.has {
				if !c.mask.Has(m) {
					t.Errorf("should have %v", m)
				}
			}
			for _, m := range c.hasNot {
				if c.mask.Has(m) {
					t.Errorf("should not have %v", m)
				}
			}
			if got := c.mask.Describe(); got != c.describe {
				t.Errorf("Describe = %q, want %q", got, c.describe)
			}
		})
	}
	if MaskOf(time.January) != 1 || MaskOf(time.December) != 1<<11 {
		t.Error("bit 0 must be January and bit 11 December")
	}
	for _, bad := range []Mask{0, 1 << 12, Monthly | 1<<15} {
		if bad.Valid() {
			t.Errorf("mask %b should be invalid", bad)
		}
	}
}
