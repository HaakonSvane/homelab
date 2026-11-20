package budget

import "time"

// Easter returns Easter Sunday for year (Gregorian calendar; Meeus/Jones/Butcher).
func Easter(year int) Date {
	a := year % 19
	b, c := year/100, year%100
	d, e := b/4, b%4
	f := (b + 8) / 25
	g := (b - f + 1) / 3
	h := (19*a + b - d - g + 15) % 30
	i, k := c/4, c%4
	l := (32 + 2*e + 2*i - h - k) % 7
	m := (a + 11*h + 22*l) / 451
	month := (h + l - 7*m + 114) / 31
	day := (h+l-7*m+114)%31 + 1
	return Date{year, time.Month(month), day}
}

// IsBankHoliday reports whether Norwegian banks are closed on d for a holiday:
// New Year's Day, Maundy Thursday, Good Friday, Easter Monday, 1 May, 17 May,
// Ascension Day, Whit Monday, Christmas Eve, Christmas Day, Boxing Day and New Year's
// Eve. (Easter Sunday and Whit Sunday are Sundays anyway.)
func IsBankHoliday(d Date) bool {
	switch {
	case d.Month == time.January && d.Day == 1,
		d.Month == time.May && (d.Day == 1 || d.Day == 17),
		d.Month == time.December && (d.Day == 24 || d.Day == 25 || d.Day == 26 || d.Day == 31):
		return true
	}
	easter := Easter(d.Year)
	for _, offset := range []int{-3, -2, 1, 39, 50} {
		if easter.AddDays(offset) == d {
			return true
		}
	}
	return false
}

// IsWorkingDay reports whether d is a weekday that is not a bank holiday.
func IsWorkingDay(d Date) bool {
	wd := d.Weekday()
	return wd != time.Saturday && wd != time.Sunday && !IsBankHoliday(d)
}
