package pages

import "testing"

func TestTaskText(t *testing.T) {
	cases := map[string]string{
		"- [ ] Milch":     "Milch",
		"  * [x] Brot ":   "Brot",
		"1. [X] Äpfel":    "Äpfel",
		"> - [ ] zitiert": "zitiert",
		"- [ ]":           "",
	}
	for in, want := range cases {
		if got, ok := TaskText(in); !ok || got != want {
			t.Errorf("TaskText(%q) = %q, %v", in, got, ok)
		}
	}
	for _, in := range []string{"Milch", "- Milch", "-[ ] x", "- [y] x", "- [ ]x", "[ ] x"} {
		if _, ok := TaskText(in); ok {
			t.Errorf("TaskText(%q) accepted", in)
		}
	}
	if toggleLine("- [ ] a") != "- [x] a" || toggleLine("1. [X] a") != "1. [ ] a" {
		t.Error("toggle")
	}
}

func TestSlugify(t *testing.T) {
	for in, want := range map[string]string{"Netzwerk & NAS": "netzwerk-nas", "Einkaufsliste": "einkaufsliste", "Grüße!": "gruesse", "  ": "seite", "日本": "seite"} {
		if got := Slugify(in); got != want {
			t.Errorf("Slugify(%q) = %q", in, got)
		}
	}
}
