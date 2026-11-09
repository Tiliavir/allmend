package webui

import "testing"

func TestMoney(t *testing.T) {
	for in, want := range map[string]int64{"": 0, "12": 1200, "12.5": 1250, "12,05": 1205, "2'500.00": 250000, "0.01": 1} {
		if got, err := parseMoney(in); err != nil || got != want {
			t.Errorf("parseMoney(%q) = %d, %v", in, got, err)
		}
	}
	for _, bad := range []string{"1.234,50", "12.345", "-3", "abc", "1e3"} {
		if _, err := parseMoney(bad); err == nil {
			t.Errorf("parseMoney(%q) accepted", bad)
		}
	}
	if money(250050, "CHF") != "2'500.50 CHF" || money(123456789, "EUR") != "1.234.567,89 EUR" || moneyInput(1205) != "12.05" {
		t.Error("format")
	}
}
