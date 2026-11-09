package places

import (
	"strings"
	"testing"
)

func TestParseGoogleLocation(t *testing.T) {
	vs, err := ParseGoogleLocation(strings.NewReader(`{"locations":[{"latitudeE7":475596000,"longitudeE7":76012000,"timestamp":"2024-05-03T10:00:00.123Z"}]}`), "o", "f.json")
	if err != nil || len(vs) != 1 || vs[0].Latitude != 47.5596 || vs[0].Timestamp.Year() != 2024 {
		t.Fatalf("%+v %v", vs, err)
	}
	for _, bad := range []string{`[]`, `{}`, `{"locations":[{"latitudeE7":1,"longitudeE7":1}]}`, `{"locations":[{"latitudeE7":1,"longitudeE7":1,"timestamp":"gestern"}]}`, `{"locations":[{"latitudeE7":950000000,"longitudeE7":1,"timestamp":"2024-05-03T10:00:00Z"}]}`} {
		if _, err := ParseGoogleLocation(strings.NewReader(bad), "o", "f"); err == nil {
			t.Errorf("accepted %s", bad)
		}
	}
}
