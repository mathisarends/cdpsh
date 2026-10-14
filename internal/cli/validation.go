package cli

import (
	"fmt"
	"math"
	"strconv"
	"strings"

	"github.com/mathisarends/cdpsh/internal/protocol"
)

// The parser and the published schemas use the same flag types and bounds.
func validateFlagValue(name, value string, spec Flag) error {
	if len(spec.Choices) > 0 {
		for _, allowed := range spec.Choices {
			if value == allowed {
				return nil
			}
		}
		return protocol.Fail("INVALID_ARGUMENT", "--"+name+" must be one of: "+strings.Join(spec.Choices, ", "))
	}
	var n float64
	switch spec.Type {
	case "integer":
		integer, err := strconv.ParseInt(value, 10, 64)
		if err != nil {
			return protocol.Fail("INVALID_ARGUMENT", "--"+name+" must be an integer")
		}
		n = float64(integer)
	case "number":
		var err error
		n, err = strconv.ParseFloat(value, 64)
		if err != nil || math.IsNaN(n) || math.IsInf(n, 0) {
			return protocol.Fail("INVALID_ARGUMENT", "--"+name+" must be a finite number")
		}
	default:
		return nil
	}
	if spec.Minimum != nil && n < *spec.Minimum || spec.Maximum != nil && n > *spec.Maximum {
		if spec.Minimum != nil && spec.Maximum != nil {
			return protocol.Fail("INVALID_ARGUMENT", fmt.Sprintf("--%s must be in %s..%s", name, strconv.FormatFloat(*spec.Minimum, 'f', -1, 64), strconv.FormatFloat(*spec.Maximum, 'f', -1, 64)))
		}
		return protocol.Fail("INVALID_ARGUMENT", fmt.Sprintf("--%s is outside its allowed range; see help or schema", name))
	}
	return nil
}
