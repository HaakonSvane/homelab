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

func TestCategoryIcon(t *testing.T) {
	for _, c := range []struct{ category, name, want string }{
		{"Bolig", "Boliglån", "house"},
		{"Abonnement", "Strømmetjenester", "repeat"},
		{"", "Strøm", "lightning"},
		{"Bolig", "Strøm", "house"}, // category wins over name
		{"Mat", "Mat og husholdning", "basket"},
		{"Barn", "Barnehage", "baby"},
		{"Sparing", "Sparing", "piggy-bank"},
		{"Diverse", "Gave", "tag"},
	} {
		if got := CategoryIcon(c.category, c.name); got != c.want {
			t.Errorf("CategoryIcon(%q, %q) = %q, want %q", c.category, c.name, got, c.want)
		}
	}
}

func TestGenitive(t *testing.T) {
	for in, want := range map[string]string{"Rikke": "Rikkes", "Haakon": "Haakons", "Andreas": "Andreas’", "Max": "Max’"} {
		if got := Genitive(in); got != want {
			t.Errorf("Genitive(%q) = %q, want %q", in, got, want)
		}
	}
}

// Every icon a category can map to, and every icon the templates use, must exist.
func TestIconsExist(t *testing.T) {
	names := []string{"plus", "pencil", "chevron-down", "chevron-left", "chevron-right", "key", "logout", "tag"}
	for _, c := range categoryIcons {
		names = append(names, c.icon)
	}
	for _, n := range names {
		if len(iconPaths[n]) == 0 {
			t.Errorf("icon %q has no paths", n)
		}
	}
}
