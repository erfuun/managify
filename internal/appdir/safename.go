package appdir

import (
	"fmt"
	"strings"
)

var reservedNames = map[string]bool{"CON": true, "PRN": true, "AUX": true, "NUL": true}

func init() {
	for i := 1; i <= 9; i++ {
		reservedNames[fmt.Sprintf("COM%d", i)] = true
		reservedNames[fmt.Sprintf("LPT%d", i)] = true
	}
}

// SafeName makes name usable as a file or folder name on every OS. An
// empty result becomes fallback.
func SafeName(name, fallback string) string {
	s := strings.Map(func(r rune) rune {
		if r < 0x20 || r == 0x7f || strings.ContainsRune(`<>:"/\|?*`, r) {
			return '_'
		}
		return r
	}, name)
	s = strings.TrimRight(s, ". ")
	if s == "" {
		return fallback
	}
	// Windows reserves these names even with an extension ("nul.txt" is NUL),
	// so the suffix goes on the part before the first dot.
	base, ext, dotted := strings.Cut(s, ".")
	if reservedNames[strings.ToUpper(base)] {
		if dotted {
			return base + "_." + ext
		}
		return s + "_"
	}
	return s
}
