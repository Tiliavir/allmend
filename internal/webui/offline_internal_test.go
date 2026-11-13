package webui

import (
	"net/http/httptest"
	"regexp"
	"strings"
	"testing"
)

// TestOfflineWritesAreRoutes: every offline form is a registered route, and
// the worker's path expression matches exactly that route.
func TestOfflineWritesAreRoutes(t *testing.T) {
	mux := (&Server{}).routes()
	res := writeRegexps()
	for i, p := range offlineWrites {
		path := patternParam.ReplaceAllString(strings.TrimPrefix(p, "POST "), "x1")
		if _, got := mux.Handler(httptest.NewRequest("POST", path, nil)); got != p {
			t.Errorf("%s: %s routes to %q", p, path, got)
		}
		re := regexp.MustCompile(res[i])
		if !re.MatchString(path) || re.MatchString(path+"/x") || re.MatchString("/a"+path) {
			t.Errorf("%s: expression %s", p, res[i])
		}
	}
}
