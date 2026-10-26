package checkdef

import (
	"bytes"
	"errors"
	"fmt"
	"io"

	"gopkg.in/yaml.v3"
)

// ParseCheckDefYAML decodes a YAML check definition and validates it (see
// CheckDef.Validate). Unknown keys are an error, so a typo such as "depend_on"
// cannot silently drop a field.
func ParseCheckDefYAML(data []byte) (CheckDef, error) {
	var def CheckDef
	dec := yaml.NewDecoder(bytes.NewReader(data))
	dec.KnownFields(true)
	if err := dec.Decode(&def); err != nil && !errors.Is(err, io.EOF) {
		return CheckDef{}, err
	}
	if err := def.Validate(); err != nil {
		return CheckDef{}, fmt.Errorf("invalid check definition: %w", err)
	}
	return def, nil
}

// MustParseCheckDefYAML is ParseCheckDefYAML for definitions embedded in the
// program. It panics, prefixed with pkg, on malformed or invalid input —
// check registries are typically built at init time from an embedded file, so
// a bad definition is a build-time bug, not something to recover from at
// runtime.
func MustParseCheckDefYAML(pkg string, data []byte) CheckDef {
	def, err := ParseCheckDefYAML(data)
	if err != nil {
		panic(pkg + ": failed to parse check definition (yaml): " + err.Error())
	}
	return def
}
