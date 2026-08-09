package checkdef

import (
	"fmt"
	"reflect"
	"testing"
)

func TestMustParseCheckDefYAML(t *testing.T) {
	data := []byte(fmt.Sprintf(`
id: %s
name: %q
description: %q
link: %q
tags:
  - %s
depends_on:
  - %s
  - %s
cvss_vector: %q
cvss_score: %v
cwe_id: %q
capec_id: %q
owasp: %q
extra:
  %s: %q
`, testCheckID, testCheckName, testCheckDescription, testCheckLink, testCheckTag, testCheckDep1, testCheckDep2, testCheckCVSSVector, testCheckCVSSScore, testCheckCWEID, testCheckCAPECID, testCheckOWASP, testCheckExtraKey, testCheckExtraValue))

	want := wantTestCheckDef()

	got := MustParseCheckDefYAML("checkdef_test", data)
	if !reflect.DeepEqual(got, want) {
		t.Errorf("want %#v, got %#v", want, got)
	}
}

func TestMustParseCheckDefYAML_PanicsOnMalformedInput(t *testing.T) {
	defer func() {
		if recover() == nil {
			t.Fatal("expected panic on malformed YAML, got none")
		}
	}()
	MustParseCheckDefYAML("checkdef_test", []byte("id: [unterminated"))
}
