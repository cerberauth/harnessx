package checkdef

import "gopkg.in/yaml.v3"

// MustParseCheckDefYAML unmarshals a YAML-encoded check definition into a
// CheckDef. It panics (with pkg-prefixed context) on malformed input —
// check registries are typically built at init time from an embedded file,
// so a bad definition is a build-time bug, not something to recover from at
// runtime.
func MustParseCheckDefYAML(pkg string, data []byte) CheckDef {
	var def CheckDef
	if err := yaml.Unmarshal(data, &def); err != nil {
		panic(pkg + ": failed to parse check definition (yaml): " + err.Error())
	}
	return def
}
