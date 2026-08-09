package checkdef

import (
	"reflect"
	"testing"

	"github.com/cerberauth/harnessx"
)

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
			dependsOn: []string{"check-1", "check-2"},
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
