package checkdef

import (
	"fmt"
	"reflect"
	"testing"
)

func TestMustParseCheckDefJSON(t *testing.T) {
	data := []byte(fmt.Sprintf(`{
  "id": %q,
  "name": %q,
  "description": %q,
  "link": %q,
  "tags": [%q],
  "depends_on": [%q, %q],
  "cvss_vector": %q,
  "cvss_score": %v,
  "cwe_id": %q,
  "capec_id": %q,
  "owasp": %q,
  "extra": {
    %q: %q
  }
}`, testCheckID, testCheckName, testCheckDescription, testCheckLink, testCheckTag, testCheckDep1, testCheckDep2, testCheckCVSSVector, testCheckCVSSScore, testCheckCWEID, testCheckCAPECID, testCheckOWASP, testCheckExtraKey, testCheckExtraValue))

	want := wantTestCheckDef()

	got := MustParseCheckDefJSON("checkdef_test", data)
	if !reflect.DeepEqual(got, want) {
		t.Errorf("want %#v, got %#v", want, got)
	}
}

func TestMustParseCheckDefJSON_PanicsOnMalformedInput(t *testing.T) {
	defer func() {
		if recover() == nil {
			t.Fatal("expected panic on malformed JSON, got none")
		}
	}()
	MustParseCheckDefJSON("checkdef_test", []byte("{unterminated"))
}
