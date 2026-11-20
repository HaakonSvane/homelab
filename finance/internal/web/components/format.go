// Package components holds the shared templ components (page layout, masthead) and
// the nb-NO display helpers they use.
package components

import "time"

// Tab is the active section in the masthead; TabNone hides the masthead (login,
// enrollment, errors).
type Tab string

const (
	TabNone     Tab = ""
	TabOverview Tab = "oversikt"
	TabExpenses Tab = "utgifter"
	TabIncome   Tab = "inntekt"
	TabSettings Tab = "innstillinger"
)

// htmxConfig turns off htmx's injected <style> (blocked by our CSP) and makes swaps
// use View Transitions where the browser supports them.
const htmxConfig = `{"includeIndicatorStyles":false,"globalViewTransitions":true}`

// Greeting greets name for the time of day of t (pass local time): God morgen
// (05–10), God formiddag (10–12), God ettermiddag (12–15), a plain "Hei, <name>!"
// in the late afternoon (15–18) where that is what people actually say, God kveld
// (18–24), and after midnight a wry "Sen økt, <name>?" ("God natt" is a goodbye).
func Greeting(t time.Time, name string) string {
	switch h := t.Hour(); {
	case h < 5:
		return "Sen økt, " + name + "?"
	case h < 10:
		return "God morgen, " + name
	case h < 12:
		return "God formiddag, " + name
	case h < 15:
		return "God ettermiddag, " + name
	case h < 18:
		return "Hei, " + name + "!"
	default:
		return "God kveld, " + name
	}
}

// Date formats as 19.11.2026 in local time (TZ=Europe/Oslo).
func Date(t time.Time) string { return t.In(time.Local).Format("02.01.2006") }

// DateTime formats as 19.11.2026 kl. 21:40 in local time.
func DateTime(t time.Time) string { return t.In(time.Local).Format("02.01.2006 kl. 15:04") }
