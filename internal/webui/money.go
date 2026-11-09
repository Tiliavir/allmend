package webui

import (
	"errors"
	"regexp"
	"strconv"
	"strings"
)

var moneyRe = regexp.MustCompile(`^\d{1,10}(?:[.,]\d{1,2})?$`)

// parseMoney reads "1234.5", "1234,50" or "" (= 0) into minor units, exactly.
func parseMoney(s string) (int64, error) {
	s = strings.ReplaceAll(strings.ReplaceAll(strings.TrimSpace(s), "'", ""), " ", "")
	if s == "" {
		return 0, nil
	}
	if !moneyRe.MatchString(s) {
		return 0, errors.New("kein gültiger Betrag")
	}
	whole, frac, _ := strings.Cut(strings.ReplaceAll(s, ",", "."), ".")
	for len(frac) < 2 {
		frac += "0"
	}
	w, err := strconv.ParseInt(whole, 10, 64)
	if err != nil {
		return 0, err
	}
	f, _ := strconv.ParseInt(frac, 10, 64)
	return w*100 + f, nil
}

// moneyInput renders minor units for an input field ("" for 0).
func moneyInput(c int64) string {
	if c == 0 {
		return ""
	}
	return strconv.FormatInt(c/100, 10) + "." + pad2(c%100)
}

func pad2(n int64) string {
	if n < 10 {
		return "0" + strconv.FormatInt(n, 10)
	}
	return strconv.FormatInt(n, 10)
}

// money formats an amount: Swiss style for CHF (1'234.50 CHF), German
// otherwise (1.234,50 EUR).
func money(c int64, cur string) string {
	neg := c < 0
	if neg {
		c = -c
	}
	sep, dec := ".", ","
	if cur == "CHF" {
		sep, dec = "'", "."
	}
	whole := strconv.FormatInt(c/100, 10)
	var b strings.Builder
	for i, r := range whole {
		if i > 0 && (len(whole)-i)%3 == 0 {
			b.WriteString(sep)
		}
		b.WriteRune(r)
	}
	out := b.String() + dec + pad2(c%100) + " " + cur
	if neg {
		return "−" + out
	}
	return out
}
