package checkdef

import "github.com/BurntSushi/toml"

// MustParseCheckDefTOML unmarshals a TOML-encoded check definition into a
// CheckDef. It panics (with pkg-prefixed context) on malformed input —
// check registries are typically built at init time from an embedded file,
// so a bad definition is a build-time bug, not something to recover from at
// runtime.
func MustParseCheckDefTOML(pkg string, data []byte) CheckDef {
	var def CheckDef
	if err := toml.Unmarshal(data, &def); err != nil {
		panic(pkg + ": failed to parse check definition (toml): " + err.Error())
	}
	return def
}
