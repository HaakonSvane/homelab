package components

import "strings"

// iconPaths holds each icon as SVG path data on a 24×24 grid.
var iconPaths = map[string][]string{
	"plus":          {"M12 5v14M5 12h14"},
	"pencil":        {"M4 20h4L19 9l-4-4L4 16z", "M13.5 6.5l4 4"},
	"chevron-down":  {"M6 9l6 6 6-6"},
	"chevron-left":  {"M15 6l-6 6 6 6"},
	"chevron-right": {"M9 6l6 6-6 6"},
	"key":           {"M8 19a4 4 0 1 0 0-8 4 4 0 0 0 0 8z", "M11 12l9-9", "M17 6l2 2", "M14.5 8.5l2 2"},
	"logout":        {"M15 4h4v16h-4", "M10 8l-4 4 4 4", "M6 12h10"},
	"home":          {"M3 11l9-7 9 7", "M5 10v10h14V10", "M10 20v-6h4v6"},
	"basket":        {"M4 9h16l-1.6 10.2a1 1 0 0 1-1 .8H6.6a1 1 0 0 1-1-.8z", "M9 9l3-5 3 5", "M9.5 13v3M14.5 13v3"},
	"car":           {"M5.5 16.5H3v-4L5.5 8h9l3.5 4.5H21v4h-2.5", "M9.5 16.5h5", "M3 12.5h15", "M7.5 18.5a2 2 0 1 0 0-4 2 2 0 0 0 0 4z", "M16.5 18.5a2 2 0 1 0 0-4 2 2 0 0 0 0 4z"},
	"shield":        {"M12 3l8 3v6c0 4.8-3.4 8-8 9-4.6-1-8-4.2-8-9V6z"},
	"repeat":        {"M4 11a8 8 0 0 1 14-5.3L20 8", "M20 4v4h-4", "M20 13a8 8 0 0 1-14 5.3L4 16", "M4 20v-4h4"},
	"sprout":        {"M12 21v-9", "M12 12C12 8 9.5 5.5 5 5.5 5 10 7.5 12 12 12z", "M12 14.5c0-3.3 2.2-5.5 6-5.5 0 3.3-2.2 5.5-6 5.5z"},
	"bolt":          {"M13 3L5 14h6l-1 7 8-11h-6z"},
	"smile":         {"M12 21a9 9 0 1 0 0-18 9 9 0 0 0 0 18z", "M8.5 14.5a4 4 0 0 0 7 0", "M9 9.5h.01M15 9.5h.01"},
	"activity":      {"M3 12h4l3-8 4 16 3-8h4"},
	"wifi":          {"M4.5 10.5a11 11 0 0 1 15 0", "M8 14a6 6 0 0 1 8 0", "M12 18h.01"},
	"tag":           {"M3 12V4h8l10 10-8 8z", "M7.5 9a1.5 1.5 0 1 0 0-3 1.5 1.5 0 0 0 0 3z"},
}

// categoryIcons maps words found in a category (or, failing that, the expense name)
// to an icon. Order matters: "strømmetjenester" is a subscription, not electricity.
var categoryIcons = []struct{ words, icon string }{
	{"abonnement strømme streaming", "repeat"},
	{"internett bredbånd mobil", "wifi"},
	{"bolig hus husleie lån felleskost kommunal", "home"},
	{"mat dagligvar husholdning", "basket"},
	{"bil transport drivstoff parkering bom", "car"},
	{"forsikring", "shield"},
	{"sparing buffer fond", "sprout"},
	{"strøm energi fjernvarme", "bolt"},
	{"barn barnehage sfo", "smile"},
	{"fritid trening sport hobby", "activity"},
}

// CategoryIcon picks an icon for an expense from its category, falling back to its
// name, and to a plain tag when nothing matches.
func CategoryIcon(category, name string) string {
	for _, text := range []string{category, name} {
		lower := strings.ToLower(text)
		if lower == "" {
			continue
		}
		for _, c := range categoryIcons {
			for _, w := range strings.Fields(c.words) {
				if strings.Contains(lower, w) {
					return c.icon
				}
			}
		}
	}
	return "tag"
}

// Genitive is the Norwegian possessive of a name: Rikkes, Haakons, Andreas’.
func Genitive(name string) string {
	if l := strings.ToLower(name); strings.HasSuffix(l, "s") || strings.HasSuffix(l, "x") || strings.HasSuffix(l, "z") {
		return name + "’"
	}
	return name + "s"
}
