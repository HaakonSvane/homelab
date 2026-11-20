package components

import (
	"testing"
	"time"
)

func TestGreeting(t *testing.T) {
	oslo, _ := time.LoadLocation("Europe/Oslo")
	for _, c := range []struct {
		hour, min int
		want      string
	}{
		{0, 0, "Sen økt, Kari?"}, {4, 59, "Sen økt, Kari?"},
		{5, 0, "God morgen, Kari"}, {9, 59, "God morgen, Kari"},
		{10, 0, "God formiddag, Kari"}, {11, 59, "God formiddag, Kari"},
		{12, 0, "God ettermiddag, Kari"}, {14, 59, "God ettermiddag, Kari"},
		{15, 0, "Hei, Kari!"}, {17, 59, "Hei, Kari!"},
		{18, 0, "God kveld, Kari"}, {23, 59, "God kveld, Kari"},
	} {
		if got := Greeting(time.Date(2026, 11, 20, c.hour, c.min, 0, 0, oslo), "Kari"); got != c.want {
			t.Errorf("%02d:%02d → %q, want %q", c.hour, c.min, got, c.want)
		}
	}
}
