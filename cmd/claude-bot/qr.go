package main

import (
	"fmt"
	"strings"

	"rsc.io/qr"
)

// qrQuiet is the blank margin around the code, in modules; phones read a
// code drawn on a screen fine with less than the four the standard asks for.
const qrQuiet = 2

// renderQR draws text as a QR code with half-block characters, two modules
// per character cell, and returns it with its width in columns. The colors
// are set explicitly so the code reads the same on dark and light terminals.
func renderQR(text string) (out string, width int, err error) {
	code, err := qr.Encode(text, qr.L)
	if err != nil {
		return "", 0, fmt.Errorf("encode QR code: %w", err)
	}

	black := func(x, y int) bool {
		x, y = x-qrQuiet, y-qrQuiet

		return x >= 0 && y >= 0 && x < code.Size && y < code.Size && code.Black(x, y)
	}

	side := code.Size + 2*qrQuiet

	var b strings.Builder

	for y := 0; y < side; y += 2 {
		b.WriteString("\x1b[30;107m") // black on bright white

		for x := range side {
			switch top, bottom := black(x, y), black(x, y+1); {
			case top && bottom:
				b.WriteString("█")
			case top:
				b.WriteString("▀")
			case bottom:
				b.WriteString("▄")
			default:
				b.WriteString(" ")
			}
		}

		b.WriteString("\x1b[0m\n")
	}

	return b.String(), side, nil
}
