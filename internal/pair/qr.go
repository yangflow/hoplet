package pair

import (
	"fmt"
	"strings"

	"rsc.io/qr"
)

// quietZone is the blank margin a scanner needs around a QR code, in modules.
const quietZone = 2

const (
	qrColors = "\x1b[30;107m" // black on bright white, whatever the terminal theme
	qrReset  = "\x1b[0m"
)

// RenderQR draws text as a QR code for a terminal, two modules to a character
// cell, one line per row of cells.
func RenderQR(text string) (string, error) {
	code, err := qr.Encode(text, qr.L)
	if err != nil {
		return "", fmt.Errorf("encode QR code: %w", err)
	}
	black := func(x, y int) bool {
		return x >= 0 && y >= 0 && x < code.Size && y < code.Size && code.Black(x, y)
	}
	var b strings.Builder
	for y := -quietZone; y < code.Size+quietZone; y += 2 {
		b.WriteString(qrColors)
		for x := -quietZone; x < code.Size+quietZone; x++ {
			switch top, bottom := black(x, y), black(x, y+1); {
			case top && bottom:
				b.WriteRune('█')
			case top:
				b.WriteRune('▀')
			case bottom:
				b.WriteRune('▄')
			default:
				b.WriteByte(' ')
			}
		}
		b.WriteString(qrReset)
		b.WriteByte('\n')
	}
	return b.String(), nil
}
