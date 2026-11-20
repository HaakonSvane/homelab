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
		{0, 0, "God natt"}, {4, 59, "God natt"},
		{5, 0, "God morgen"}, {9, 59, "God morgen"},
		{10, 0, "God formiddag"}, {11, 59, "God formiddag"},
		{12, 0, "God ettermiddag"}, {17, 59, "God ettermiddag"},
		{18, 0, "God kveld"}, {23, 59, "God kveld"},
	} {
		if got := Greeting(time.Date(2026, 11, 20, c.hour, c.min, 0, 0, oslo)); got != c.want {
			t.Errorf("%02d:%02d → %q, want %q", c.hour, c.min, got, c.want)
		}
	}
}
