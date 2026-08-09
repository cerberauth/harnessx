package checkdef

import (
	"fmt"
	"reflect"
	"testing"
)

func TestMustParseCheckDefTOML(t *testing.T) {
	data := []byte(fmt.Sprintf(`
id = %q
name = %q
description = %q
link = %q
tags = [%q]
depends_on = [%q, %q]
`, testCheckID, testCheckName, testCheckDescription, testCheckLink, testCheckTag, testCheckDep1, testCheckDep2))

	want := wantTestCheckDef()

	got := MustParseCheckDefTOML("checkdef_test", data)
	if !reflect.DeepEqual(got, want) {
		t.Errorf("want %#v, got %#v", want, got)
	}
}

func TestMustParseCheckDefTOML_PanicsOnMalformedInput(t *testing.T) {
	defer func() {
		if recover() == nil {
			t.Fatal("expected panic on malformed TOML, got none")
		}
	}()
	MustParseCheckDefTOML("checkdef_test", []byte("id = [unterminated"))
}
