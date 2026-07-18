package detector

import (
	_ "embed"

	"gopkg.in/yaml.v3"
)

//go:embed builtin.yaml
var builtinData []byte

func Builtin() (Pack, error) {
	var pack Pack
	if err := yaml.Unmarshal(builtinData, &pack); err != nil {
		return Pack{}, err
	}
	if pack.Checksum == "" {
		pack.Checksum = pack.CalculatedChecksum()
	}
	if err := pack.Validate(); err != nil {
		return Pack{}, err
	}
	return pack, nil
}
