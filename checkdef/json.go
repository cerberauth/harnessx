package checkdef

import "encoding/json"

// MustParseCheckDefJSON unmarshals a JSON-encoded check definition into a
// CheckDef. It panics (with pkg-prefixed context) on malformed input —
// check registries are typically built at init time from an embedded file,
// so a bad definition is a build-time bug, not something to recover from at
// runtime.
func MustParseCheckDefJSON(pkg string, data []byte) CheckDef {
	var def CheckDef
	if err := json.Unmarshal(data, &def); err != nil {
		panic(pkg + ": failed to parse check definition (json): " + err.Error())
	}
	return def
}
