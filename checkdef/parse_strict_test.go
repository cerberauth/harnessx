package checkdef

import (
	"strings"
	"testing"
)

const (
	formatYAML = "yaml"
	formatJSON = "json"
	formatTOML = "toml"
	tagOne     = "one"

	validYAML = "id: a\nname: A\ntags: [x]\ndepends_on: [b]\ncvss_score: 7.5\nextra:\n  anything: goes\n"
	validJSON = `{"id":"a","name":"A","tags":["x"],"depends_on":["b"],"cvss_score":7.5,"extra":{"anything":"goes"}}`
	validTOML = "id = \"a\"\nname = \"A\"\ntags = [\"x\"]\ndepends_on = [\"b\"]\ncvss_score = 7.5\n[extra]\nanything = \"goes\"\n"
)

type parser struct {
	name  string
	parse func([]byte) (CheckDef, error)
	valid string
	typo  string // the valid document with depends_on misspelt
}

func parsers() []parser {
	return []parser{
		{formatYAML, ParseCheckDefYAML, validYAML, strings.Replace(validYAML, "depends_on", "depend_on", 1)},
		{formatJSON, ParseCheckDefJSON, validJSON, strings.Replace(validJSON, "depends_on", "depend_on", 1)},
		{formatTOML, ParseCheckDefTOML, validTOML, strings.Replace(validTOML, "depends_on", "depend_on", 1)},
	}
}

func TestParseCheckDef_Valid(t *testing.T) {
	for _, p := range parsers() {
		def, err := p.parse([]byte(p.valid))
		if err != nil {
			t.Errorf("%s: %v", p.name, err)
			continue
		}
		if def.ID != "a" || len(def.DependsOn) != 1 || def.Extra["anything"] != "goes" {
			t.Errorf("%s: got %+v", p.name, def)
		}
	}
}

func TestParseCheckDef_UnknownKeyIsAnError(t *testing.T) {
	for _, p := range parsers() {
		_, err := p.parse([]byte(p.typo))
		if err == nil || !strings.Contains(err.Error(), "depend_on") {
			t.Errorf("%s: error = %v, want it to name the unknown key depend_on", p.name, err)
		}
	}
}

func TestParseCheckDef_InvalidDefinitionIsAnError(t *testing.T) {
	cases := map[string][2]string{
		formatYAML: {"name: no id\n", "id is required"},
		formatJSON: {`{"id":"a","cvss_score":11}`, "cvss_score"},
		formatTOML: {"id = \"a\"\ndepends_on = [\"a\"]\n", "lists the check itself"},
	}
	byName := map[string]parser{}
	for _, p := range parsers() {
		byName[p.name] = p
	}
	for name, c := range cases {
		_, err := byName[name].parse([]byte(c[0]))
		if err == nil || !strings.Contains(err.Error(), c[1]) {
			t.Errorf("%s: error = %v, want it to mention %q", name, err, c[1])
		}
	}
}

func TestParseCheckDefYAML_EmptyDocumentIsInvalidNotEOF(t *testing.T) {
	_, err := ParseCheckDefYAML(nil)
	if err == nil || !strings.Contains(err.Error(), "id is required") {
		t.Errorf("error = %v, want an id-required validation error", err)
	}
}

func TestParseCheckDefJSON_TrailingDataIsAnError(t *testing.T) {
	if _, err := ParseCheckDefJSON([]byte(validJSON + ` {"id":"b"}`)); err == nil {
		t.Error("want an error for data after the document")
	}
}

func TestMustParseCheckDef_PanicsOnUnknownKey(t *testing.T) {
	for _, p := range parsers() {
		func() {
			defer func() {
				msg, _ := recover().(string)
				if !strings.HasPrefix(msg, "pkg: failed to parse check definition ("+p.name+")") {
					t.Errorf("%s: panic = %q", p.name, msg)
				}
			}()
			switch p.name {
			case formatYAML:
				MustParseCheckDefYAML("pkg", []byte(p.typo))
			case formatJSON:
				MustParseCheckDefJSON("pkg", []byte(p.typo))
			default:
				MustParseCheckDefTOML("pkg", []byte(p.typo))
			}
		}()
	}
}

func TestCheckDef_Validate(t *testing.T) {
	ok := CheckDef{ID: "a", CVSSScore: 10, DependsOn: []string{"b"}, Tags: []string{"t"}}
	if err := ok.Validate(); err != nil {
		t.Errorf("valid definition: %v", err)
	}

	bad := CheckDef{ID: " ", CVSSScore: -1, DependsOn: []string{"", " "}, Tags: []string{""}}
	err := bad.Validate()
	if err == nil {
		t.Fatal("want an error")
	}
	for _, want := range []string{"id is required", "cvss_score", "depends_on has an empty entry", "tags has an empty entry"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q does not mention %q (all problems should be reported)", err, want)
		}
	}
}

func TestNewCheck_DoesNotAliasDefTags(t *testing.T) {
	def := CheckDef{ID: "a", Tags: []string{tagOne, "two"}}
	check := NewCheck(def, nil)

	check.Tags[0] = "changed"
	if def.Tags[0] != tagOne {
		t.Errorf("mutating the check's Tags changed the CheckDef: %v", def.Tags)
	}
	other := NewCheck(def, nil)
	if other.Tags[0] != tagOne {
		t.Errorf("a second check built from the def sees the first one's mutation: %v", other.Tags)
	}
}
