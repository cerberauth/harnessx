package checkdef

import (
	"strings"
	"testing"
)

// fuzzParse runs a Must* parser and fails the test on any panic other than the
// documented parse-failure one.
func fuzzParse(t *testing.T, parse func(pkg string, data []byte) CheckDef, data []byte) {
	t.Helper()
	defer func() {
		if r := recover(); r != nil {
			msg, ok := r.(string)
			if !ok || !strings.HasPrefix(msg, "fuzz: failed to parse check definition") {
				t.Fatalf("unexpected panic: %v", r)
			}
		}
	}()
	parse("fuzz", data)
}

func FuzzMustParseCheckDefYAML(f *testing.F) {
	f.Add([]byte("id: a\nname: A\ntags: [x]\ndepends_on: [b]\ncvss_score: 7.5\n"))
	f.Add([]byte(""))
	f.Add([]byte("id: [unterminated"))
	f.Fuzz(func(t *testing.T, data []byte) { fuzzParse(t, MustParseCheckDefYAML, data) })
}

func FuzzMustParseCheckDefJSON(f *testing.F) {
	f.Add([]byte(`{"id":"a","name":"A","tags":["x"],"depends_on":["b"],"cvss_score":7.5}`))
	f.Add([]byte(`{}`))
	f.Add([]byte(`{"id":`))
	f.Fuzz(func(t *testing.T, data []byte) { fuzzParse(t, MustParseCheckDefJSON, data) })
}

func FuzzMustParseCheckDefTOML(f *testing.F) {
	f.Add([]byte("id = \"a\"\nname = \"A\"\ntags = [\"x\"]\ncvss_score = 7.5\n"))
	f.Add([]byte(""))
	f.Add([]byte("id = "))
	f.Fuzz(func(t *testing.T, data []byte) { fuzzParse(t, MustParseCheckDefTOML, data) })
}
