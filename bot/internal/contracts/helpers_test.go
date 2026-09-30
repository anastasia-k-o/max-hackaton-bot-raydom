package contracts

import (
	"io"
	"strings"
)

// newStringReader keeps the decoding test readable.
func newStringReader(s string) io.Reader { return strings.NewReader(s) }
