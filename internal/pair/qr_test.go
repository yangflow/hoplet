package pair

import (
	"strings"
	"testing"

	"rsc.io/qr"
)

func TestRenderQRDrawsEveryModule(t *testing.T) {
	const text = "hoplet1:example"
	out, err := RenderQR(text)
	if err != nil {
		t.Fatal(err)
	}
	code, err := qr.Encode(text, qr.L)
	if err != nil {
		t.Fatal(err)
	}
	side := code.Size + 2*quietZone
	lines := strings.Split(strings.TrimSuffix(out, "\n"), "\n")
	if want := (side + 1) / 2; len(lines) != want {
		t.Fatalf("lines = %d, want %d", len(lines), want)
	}
	for row, line := range lines {
		if !strings.HasPrefix(line, qrColors) || !strings.HasSuffix(line, qrReset) {
			t.Fatalf("line %d does not set its colors: %q", row, line)
		}
		cells := []rune(strings.TrimSuffix(strings.TrimPrefix(line, qrColors), qrReset))
		if len(cells) != side {
			t.Fatalf("line %d has %d cells, want %d", row, len(cells), side)
		}
		for column, cell := range cells {
			x, y := column-quietZone, row*2-quietZone
			inCode := func(x, y int) bool {
				return x >= 0 && y >= 0 && x < code.Size && y < code.Size && code.Black(x, y)
			}
			top, bottom := cell == '█' || cell == '▀', cell == '█' || cell == '▄'
			if top != inCode(x, y) || bottom != inCode(x, y+1) {
				t.Fatalf("cell (%d,%d) = %q", column, row, cell)
			}
		}
	}
}

func TestRenderQRRejectsTextThatDoesNotFit(t *testing.T) {
	if _, err := RenderQR(strings.Repeat("x", 4000)); err == nil {
		t.Fatal("RenderQR accepted text too long for a QR code")
	}
}
