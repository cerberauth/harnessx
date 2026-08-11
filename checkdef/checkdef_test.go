package checkdef

import (
	"reflect"
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
