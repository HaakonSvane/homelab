// Package month provides a calendar-month type ('YYYY-MM') and the 12-bit mask of
// months in which a bill falls due.
package month

import (
	"database/sql/driver"
	"errors"
	"fmt"
	"math/bits"
	"strings"
	"time"
)

// Month is a calendar month, stored as year*12 + (month-1) so months compare and
// subtract as plain integers. The zero value means "no month" (Parse never returns it).
type Month int32

// ErrSyntax is returned for strings that are not a valid 'YYYY-MM'.
var ErrSyntax = errors.New("month: want YYYY-MM")

// New returns the month for a year and calendar month.
func New(year int, m time.Month) Month { return Month(year*12 + int(m) - 1) }

// Of returns the month t falls in, in t's location.
func Of(t time.Time) Month { return New(t.Year(), t.Month()) }

// Parse reads 'YYYY-MM' (years 1000–9999).
func Parse(s string) (Month, error) {
	if len(s) != 7 || s[4] != '-' {
		return 0, ErrSyntax
	}
	year, m := 0, 0
	for i, c := range []byte(s) {
		switch {
		case i == 4:
		case c < '0' || c > '9':
			return 0, ErrSyntax
		case i < 4:
			year = year*10 + int(c-'0')
		default:
			m = m*10 + int(c-'0')
		}
	}
	if year < 1000 || m < 1 || m > 12 {
		return 0, ErrSyntax
	}
	return New(year, time.Month(m)), nil
}

// MustParse is Parse for constants in tests and seed data; it panics on bad input.
func MustParse(s string) Month {
	m, err := Parse(s)
	if err != nil {
		panic(fmt.Sprintf("month.MustParse(%q): %v", s, err))
	}
	return m
}

func (m Month) Year() int                    { return int(m) / 12 }
func (m Month) Month() time.Month            { return time.Month(int(m)%12 + 1) }
func (m Month) Add(n int) Month              { return m + Month(n) }
func (m Month) IsZero() bool                 { return m == 0 }
func (m Month) String() string               { return fmt.Sprintf("%04d-%02d", m.Year(), int(m.Month())) }
func (m Month) Label() string                { return Name(m.Month()) + " " + fmt.Sprint(m.Year()) }
func (m Month) Value() (driver.Value, error) { return m.String(), nil }

// Scan implements sql.Scanner for TEXT 'YYYY-MM' columns.
func (m *Month) Scan(src any) error {
	var s string
	switch v := src.(type) {
	case string:
		s = v
	case []byte:
		s = string(v)
	default:
		return fmt.Errorf("month: cannot scan %T", src)
	}
	p, err := Parse(s)
	if err != nil {
		return err
	}
	*m = p
	return nil
}

var (
	names      = [12]string{"januar", "februar", "mars", "april", "mai", "juni", "juli", "august", "september", "oktober", "november", "desember"}
	shortNames = [12]string{"jan", "feb", "mar", "apr", "mai", "jun", "jul", "aug", "sep", "okt", "nov", "des"}
)

// Name is the Norwegian month name ("januar").
func Name(m time.Month) string { return names[m-1] }

// ShortName is the abbreviated Norwegian month name ("jan").
func ShortName(m time.Month) string { return shortNames[m-1] }

// Mask is the set of calendar months a bill falls due in: bit 0 = January.
type Mask uint16

// Monthly is every month.
const Monthly Mask = 1<<12 - 1

// MaskOf builds a mask from calendar months.
func MaskOf(months ...time.Month) Mask {
	var k Mask
	for _, m := range months {
		k |= 1 << (m - 1)
	}
	return k
}

// Semiannual is due in anchor and six months later.
func Semiannual(anchor time.Month) Mask { return MaskOf(anchor, (anchor+5)%12+1) }

// Yearly is due once a year, in anchor.
func Yearly(anchor time.Month) Mask { return MaskOf(anchor) }

// ElevenMonths is due every month except one (e.g. kindergarten, free in July).
func ElevenMonths(except time.Month) Mask { return Monthly &^ MaskOf(except) }

// Valid reports whether the mask has at least one month and no bits above December.
func (k Mask) Valid() bool { return k != 0 && k&^Monthly == 0 }

// Has reports whether the bill falls due in calendar month m.
func (k Mask) Has(m time.Month) bool { return k&(1<<(m-1)) != 0 }

// Count is the number of months per year the bill falls due.
func (k Mask) Count() int { return bits.OnesCount16(uint16(k & Monthly)) }

// Months lists the due months in calendar order.
func (k Mask) Months() []time.Month {
	var out []time.Month
	for m := time.January; m <= time.December; m++ {
		if k.Has(m) {
			out = append(out, m)
		}
	}
	return out
}

// Describe is a short Norwegian description: "Månedlig", "Årlig (mars)",
// "Halvårlig (jan og jul)", "Alle måneder unntatt juli", or "jan, apr og okt".
func (k Mask) Describe() string {
	months := k.Months()
	short := make([]string, len(months))
	for i, m := range months {
		short[i] = ShortName(m)
	}
	switch {
	case !k.Valid():
		return "Aldri"
	case k == Monthly:
		return "Månedlig"
	case len(months) == 11:
		return "Alle måneder unntatt " + Name(Monthly.without(k))
	case len(months) == 1:
		return "Årlig (" + Name(months[0]) + ")"
	case len(months) == 2 && months[1]-months[0] == 6:
		return "Halvårlig (" + short[0] + " og " + short[1] + ")"
	default:
		return strings.Join(short[:len(short)-1], ", ") + " og " + short[len(short)-1]
	}
}

// without returns the first month in k that is not in other.
func (k Mask) without(other Mask) time.Month { return (k &^ other).Months()[0] }
