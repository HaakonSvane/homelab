// Package budget is the time-based side of personal spending: pay dates (Norwegian
// working days), the current pay period, how much should be left of the month's free
// money, and whether a purchase fits. Pure functions only, no database or HTTP.
package budget

import (
	"fmt"
	"time"

	"github.com/HaakonSvane/homelab/finance/internal/month"
)

// Date is a calendar date without time or zone, so day counts never trip over
// daylight saving time.
type Date struct {
	Year  int
	Month time.Month
	Day   int
}

// DateOf returns the calendar date of t in t's location.
func DateOf(t time.Time) Date { return Date{t.Year(), t.Month(), t.Day()} }

func (d Date) utc() time.Time { return time.Date(d.Year, d.Month, d.Day, 0, 0, 0, 0, time.UTC) }

// AddDays returns the date n days later (or earlier, for negative n).
func (d Date) AddDays(n int) Date { return DateOf(d.utc().AddDate(0, 0, n)) }

// DaysUntil is the number of days from d to e (negative if e is earlier).
func (d Date) DaysUntil(e Date) int { return int(e.utc().Sub(d.utc()).Hours() / 24) }

func (d Date) Before(e Date) bool    { return d.utc().Before(e.utc()) }
func (d Date) After(e Date) bool     { return d.utc().After(e.utc()) }
func (d Date) Weekday() time.Weekday { return d.utc().Weekday() }
func (d Date) MonthOf() month.Month  { return month.New(d.Year, d.Month) }
func (d Date) String() string        { return fmt.Sprintf("%04d-%02d-%02d", d.Year, int(d.Month), d.Day) }
func daysIn(m month.Month) int {
	return Date{m.Year(), m.Month(), 1}.DaysUntil(Date{m.Add(1).Year(), m.Add(1).Month(), 1})
}
