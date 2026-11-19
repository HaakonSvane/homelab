// Package money represents amounts as integer øre and formats/parses them the
// Norwegian (nb-NO) way: "12 345,00 kr". There are no floats anywhere.
package money

import (
	"errors"
	"strings"
)

// Ore is an amount in øre (1/100 krone).
type Ore int64

// Max is the largest amount accepted from input: 100 000 000 kr. Keeping amounts
// below this leaves the ledger's exact intermediate sums far from int64 overflow.
const Max Ore = 100_000_000_00

// Kr returns whole kroner as Ore. Convenient in tests and seed data.
func Kr(kroner int64) Ore { return Ore(kroner * 100) }

const (
	nbsp  = " " // thousands separator and before "kr", as nb-NO Intl does
	minus = "−" // nb-NO uses the real minus sign
)

// String formats as "12 345,00 kr".
func (o Ore) String() string { return Format(o) }

// Format formats as "12 345,00 kr" (non-breaking spaces, U+2212 for negatives).
func Format(o Ore) string { return FormatNumber(o) + nbsp + "kr" }

// FormatNumber formats without the currency: "12 345,00". Parse accepts the result,
// so it is also what form fields are pre-filled with.
func FormatNumber(o Ore) string {
	neg := o < 0
	u := uint64(o)
	if neg {
		u = -u // two's complement; correct even for math.MinInt64
	}
	kroner, ore := u/100, u%100

	digits := []byte(uitoa(kroner))
	var b strings.Builder
	if neg {
		b.WriteString(minus)
	}
	for i, d := range digits {
		if i > 0 && (len(digits)-i)%3 == 0 {
			b.WriteString(nbsp)
		}
		b.WriteByte(d)
	}
	b.WriteByte(',')
	b.WriteByte(byte('0' + ore/10))
	b.WriteByte(byte('0' + ore%10))
	return b.String()
}

func uitoa(u uint64) string {
	if u == 0 {
		return "0"
	}
	var buf [20]byte
	i := len(buf)
	for u > 0 {
		i--
		buf[i] = byte('0' + u%10)
		u /= 10
	}
	return string(buf[i:])
}

// Parse errors. The web layer maps these to Norwegian messages.
var (
	ErrSyntax    = errors.New("money: not a valid amount")
	ErrNegative  = errors.New("money: amount cannot be negative")
	ErrPrecision = errors.New("money: more than two decimals")
	ErrTooLarge  = errors.New("money: amount too large")
)

// Parse reads a non-negative amount as typed by a person. It accepts
//
//	12345   12345,5   12 345,50   12.345,50   12345.50   1 500,-   kr 99   99 kr
//
// Comma is the decimal separator. A dot is a decimal separator only when it is the
// only one, there is no comma, and 1–2 digits follow it ("12.50"); otherwise dots,
// spaces, NBSP and narrow NBSP are thousands separators, and groups after the first
// must have exactly three digits.
func Parse(s string) (Ore, error) {
	s = strings.TrimSpace(s)
	lower := strings.ToLower(s)
	for _, cur := range []string{"kr.", "kr", "nok"} {
		if strings.HasSuffix(lower, cur) {
			s, lower = s[:len(s)-len(cur)], lower[:len(lower)-len(cur)]
			break
		}
	}
	for _, cur := range []string{"kr", "nok"} {
		if strings.HasPrefix(lower, cur) {
			s = s[len(cur):]
			break
		}
	}
	s = strings.TrimSpace(s)
	if s == "" {
		return 0, ErrSyntax
	}
	if strings.HasPrefix(s, "-") || strings.HasPrefix(s, minus) {
		return 0, ErrNegative
	}

	intPart, frac := s, ""
	switch {
	case strings.HasSuffix(s, ",-") || strings.HasSuffix(s, ".-"):
		intPart = s[:len(s)-2]
	case strings.Count(s, ",") > 1:
		return 0, ErrSyntax
	case strings.Contains(s, ","):
		intPart, frac, _ = strings.Cut(s, ",")
	case strings.Count(s, ".") == 1:
		if i := strings.LastIndexByte(s, '.'); len(s)-i-1 >= 1 && len(s)-i-1 <= 2 {
			intPart, frac = s[:i], s[i+1:]
		}
	}
	if strings.Contains(frac, ".") {
		return 0, ErrSyntax
	}

	kroner, err := parseGrouped(intPart)
	if err != nil {
		return 0, err
	}
	if !allDigits(frac) {
		return 0, ErrSyntax
	}
	if len(frac) > 2 {
		return 0, ErrPrecision
	}
	if strings.Contains(s, ",") && !strings.HasSuffix(s, ",-") && frac == "" {
		return 0, ErrSyntax // "12,"
	}
	var ore int64
	switch len(frac) {
	case 1:
		ore = int64(frac[0]-'0') * 10
	case 2:
		ore = int64(frac[0]-'0')*10 + int64(frac[1]-'0')
	}
	total := Ore(kroner*100 + ore)
	if total > Max {
		return 0, ErrTooLarge
	}
	return total, nil
}

// parseGrouped parses whole kroner with optional thousands separators.
func parseGrouped(s string) (int64, error) {
	groups := strings.FieldsFunc(s, isGroupSep)
	// FieldsFunc drops empty fields, so compare lengths to catch "1  000" or "1.".
	seps := 0
	for _, r := range s {
		if isGroupSep(r) {
			seps++
		}
	}
	if len(groups) == 0 || seps != len(groups)-1 || isGroupSep(firstRune(s)) {
		return 0, ErrSyntax
	}
	var n int64
	for i, g := range groups {
		if !allDigits(g) || g == "" {
			return 0, ErrSyntax
		}
		if len(groups) > 1 && ((i == 0 && len(g) > 3) || (i > 0 && len(g) != 3)) {
			return 0, ErrSyntax
		}
		for _, c := range g {
			n = n*10 + int64(c-'0')
			if n > int64(Max/100) {
				return 0, ErrTooLarge
			}
		}
	}
	return n, nil
}

func isGroupSep(r rune) bool {
	return r == ' ' || r == '.' || r == ' ' || r == ' '
}

func firstRune(s string) rune {
	for _, r := range s {
		return r
	}
	return 0
}

func allDigits(s string) bool {
	for i := 0; i < len(s); i++ {
		if s[i] < '0' || s[i] > '9' {
			return false
		}
	}
	return true
}
