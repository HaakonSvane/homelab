package money

import (
	"errors"
	"math"
	"strings"
	"testing"
)

func TestFormat(t *testing.T) {
	cases := []struct {
		in   Ore
		want string
	}{
		{0, "0,00 kr"},
		{5, "0,05 kr"},
		{100, "1,00 kr"},
		{99999, "999,99 kr"},
		{100000, "1 000,00 kr"},
		{1234500, "12 345,00 kr"},
		{123456789, "1 234 567,89 kr"},
		{-1, "−0,01 kr"},
		{-1234500, "−12 345,00 kr"},
		{math.MaxInt64, "92 233 720 368 547 758,07 kr"},
		{math.MinInt64, "−92 233 720 368 547 758,08 kr"},
	}
	for _, c := range cases {
		// Test strings use plain spaces for readability; real output uses NBSP.
		want := strings.ReplaceAll(c.want, " ", " ")
		if got := Format(c.in); got != want {
			t.Errorf("Format(%d) = %q, want %q", c.in, got, want)
		}
	}
}

func TestParse(t *testing.T) {
	cases := []struct {
		in   string
		want Ore
		err  error
	}{
		{"0", 0, nil},
		{"12345", 1234500, nil},
		{"12345,5", 1234550, nil},
		{"12345,50", 1234550, nil},
		{"12345,05", 1234505, nil},
		{"12 345,50", 1234550, nil},
		{"12 345,50", 1234550, nil},
		{"12 345,50", 1234550, nil},
		{"12.345,50", 1234550, nil},
		{"1.234.567", 123456700, nil},
		{"12345.50", 1234550, nil},
		{"12.5", 1250, nil},
		{"1.234", 123400, nil}, // three digits after a lone dot: thousands separator
		{"1 500,-", 150000, nil},
		{"1500.-", 150000, nil},
		{"kr 99", 9900, nil},
		{"99 kr", 9900, nil},
		{"99kr", 9900, nil},
		{"99 KR.", 9900, nil},
		{"  250  ", 25000, nil},
		{"007", 700, nil},
		{"100000000", Max, nil},

		{"", 0, ErrSyntax},
		{"kr", 0, ErrSyntax},
		{"abc", 0, ErrSyntax},
		{"12,", 0, ErrSyntax},
		{"12.", 0, ErrSyntax},
		{",50", 0, ErrSyntax},
		{"1,2,3", 0, ErrSyntax},
		{"12 34", 0, ErrSyntax},    // groups after the first must be 3 digits
		{"1234 567", 0, ErrSyntax}, // first group at most 3 digits
		{"1  000", 0, ErrSyntax},   // double separator
		{" .100", 0, ErrSyntax},    // leading separator
		{"12,5.0", 0, ErrSyntax},
		{"1e5", 0, ErrSyntax},
		{"+5", 0, ErrSyntax},
		{"12,345", 0, ErrPrecision},
		{"-5", 0, ErrNegative},
		{"−5", 0, ErrNegative},
		{"100000000,01", 0, ErrTooLarge},
		{"100000001", 0, ErrTooLarge},
		{"99999999999999999999999", 0, ErrTooLarge},
	}
	for _, c := range cases {
		got, err := Parse(c.in)
		if !errors.Is(err, c.err) || got != c.want {
			t.Errorf("Parse(%q) = %d, %v; want %d, %v", c.in, got, err, c.want, c.err)
		}
	}
}

func TestParseRoundTrip(t *testing.T) {
	for _, o := range []Ore{0, 1, 10, 99, 100, 101, 99999, 100000, 1234567, 987654321, Max} {
		if got, err := Parse(Format(o)); err != nil || got != o {
			t.Errorf("Parse(Format(%d)) = %d, %v", o, got, err)
		}
		if got, err := Parse(FormatNumber(o)); err != nil || got != o {
			t.Errorf("Parse(FormatNumber(%d)) = %d, %v", o, got, err)
		}
	}
}

func TestDivRound(t *testing.T) {
	for _, c := range []struct {
		o    Ore
		n    int64
		want Ore
	}{
		{100, 3, 33}, {200, 3, 67}, {5, 2, 2}, {7, 2, 4}, {-5, 2, -2}, {-7, 2, -4}, {0, 30, 0}, {3000000, 31, 96774},
	} {
		if got := DivRound(c.o, c.n); got != c.want {
			t.Errorf("DivRound(%d, %d) = %d, want %d", c.o, c.n, got, c.want)
		}
	}
}
