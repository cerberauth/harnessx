package checkdef

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
)

// ParseCheckDefJSON decodes a JSON check definition and validates it (see
// CheckDef.Validate). Unknown keys and trailing data are an error, so a typo
// such as "depend_on" cannot silently drop a field.
func ParseCheckDefJSON(data []byte) (CheckDef, error) {
	var def CheckDef
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&def); err != nil {
		return CheckDef{}, err
	}
	if _, err := dec.Token(); !errors.Is(err, io.EOF) {
		return CheckDef{}, errors.New("unexpected data after the JSON document")
	}
	if err := def.Validate(); err != nil {
		return CheckDef{}, fmt.Errorf("invalid check definition: %w", err)
	}
	return def, nil
}

// MustParseCheckDefJSON is ParseCheckDefJSON for definitions embedded in the
// program. It panics, prefixed with pkg, on malformed or invalid input —
// check registries are typically built at init time from an embedded file, so
// a bad definition is a build-time bug, not something to recover from at
// runtime.
func MustParseCheckDefJSON(pkg string, data []byte) CheckDef {
	def, err := ParseCheckDefJSON(data)
	if err != nil {
		panic(pkg + ": failed to parse check definition (json): " + err.Error())
	}
	return def
}
