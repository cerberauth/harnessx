package checkdef

import (
	"reflect"
	"strings"
	"testing"

	"github.com/cerberauth/harnessx"
)

const testCheckID1 = "check-1"

func TestCheckDef_DependsOnIDs(t *testing.T) {
	tests := []struct {
		name      string
		dependsOn []string
		expected  []harnessx.CheckID
	}{
		{
			name:      "empty depends on",
			dependsOn: []string{},
			expected:  []harnessx.CheckID{},
		},
		{
			name:      "nil depends on",
			dependsOn: nil,
			expected:  []harnessx.CheckID{},
		},
		{
			name:      "multiple items",
			dependsOn: []string{testCheckID1, "check-2"},
			expected:  []harnessx.CheckID{"check-1", "check-2"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			d := CheckDef{DependsOn: tt.dependsOn}
			got := d.DependsOnIDs()
			if !reflect.DeepEqual(got, tt.expected) {
				t.Errorf("want %#v, got %#v", tt.expected, got)
			}
		})
	}
}

func TestCheckDef_WithSetters(t *testing.T) {
	orig := CheckDef{
		Name: "orig",
		Link: "https://orig.example.com",
	}

	got := orig.
		WithName("new name").
		WithDescription("new description").
		WithLink("https://new.example.com").
		WithTags("a", "b").
		WithDependsOn(testCheckID1).
		WithCVSSVector("CVSS:3.1/AV:N").
		WithCVSSScore(9.1).
		WithCWEID("CWE-1").
		WithCAPECID("CAPEC-1").
		WithOWASP("A01:2021").
		WithExtra(map[string]any{"k": "v"})

	want := CheckDef{
		Name:        "new name",
		Description: "new description",
		Link:        "https://new.example.com",
		Tags:        []string{"a", "b"},
		DependsOn:   []string{testCheckID1},
		CVSSVector:  "CVSS:3.1/AV:N",
		CVSSScore:   9.1,
		CWEID:       "CWE-1",
		CAPECID:     "CAPEC-1",
		OWASP:       "A01:2021",
		Extra:       map[string]any{"k": "v"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("want %#v, got %#v", want, got)
	}

	if orig.Name != "orig" || orig.Link != "https://orig.example.com" {
		t.Errorf("WithX must not mutate the receiver, orig changed: %#v", orig)
	}
}

func TestCheckDef_ValidateCVSS(t *testing.T) {
	const vector = "CVSS:4.0/AV:N/AC:L/AT:N/PR:L/UI:N/VC:H/VI:H/VA:N/SC:N/SI:N/SA:N"
	testCases := []struct {
		name string
		def  CheckDef
		err  string
	}{
		{"no vector", CheckDef{ID: "a"}, ""},
		{"matching score", CheckDef{ID: "a", CVSSVector: vector, CVSSScore: 8.6}, ""},
		{"wrong score", CheckDef{ID: "a", CVSSVector: vector, CVSSScore: 8.2}, "which scores 8.6"},
		{"invalid vector", CheckDef{ID: "a", CVSSVector: "CVSS:4.0/AV:N", CVSSScore: 1}, "invalid CVSS v4.0 vector"},
	}
	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			err := tc.def.ValidateCVSS()

			switch {
			case tc.err == "" && err != nil:
				t.Fatalf("unexpected error: %v", err)
			case tc.err != "" && (err == nil || !strings.Contains(err.Error(), tc.err)):
				t.Fatalf("got %v, want error containing %q", err, tc.err)
			}
		})
	}
}
