package checkdef

import (
	"fmt"
	"strings"

	"github.com/BurntSushi/toml"
)

// ParseCheckDefTOML decodes a TOML check definition and validates it (see
// CheckDef.Validate). Unknown keys are an error, so a typo such as "depend_on"
// cannot silently drop a field.
func ParseCheckDefTOML(data []byte) (CheckDef, error) {
	var def CheckDef
	md, err := toml.Decode(string(data), &def)
	if err != nil {
		return CheckDef{}, err
	}
	if undecoded := md.Undecoded(); len(undecoded) > 0 {
		keys := make([]string, len(undecoded))
		for i, k := range undecoded {
			keys[i] = k.String()
		}
		return CheckDef{}, fmt.Errorf("unknown key(s): %s", strings.Join(keys, ", "))
	}
	if err := def.Validate(); err != nil {
		return CheckDef{}, fmt.Errorf("invalid check definition: %w", err)
	}
	return def, nil
}

// MustParseCheckDefTOML is ParseCheckDefTOML for definitions embedded in the
// program. It panics, prefixed with pkg, on malformed or invalid input —
// check registries are typically built at init time from an embedded file, so
// a bad definition is a build-time bug, not something to recover from at
// runtime.
func MustParseCheckDefTOML(pkg string, data []byte) CheckDef {
	def, err := ParseCheckDefTOML(data)
	if err != nil {
		panic(pkg + ": failed to parse check definition (toml): " + err.Error())
	}
	return def
}
