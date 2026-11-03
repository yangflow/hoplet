package icons

import (
	"bytes"
	"flag"
	"image"
	"image/png"
	"os"
	"strings"
	"testing"

	"github.com/egoist/mygo/ui"
)

var update = flag.Bool("update", false, "rewrite the rendered images")

// render draws svg at size×size, inset by margin on every side, on a
// transparent canvas.
func render(t *testing.T, svg string, size, margin float32) *image.RGBA {
	t.Helper()
	icon, err := ui.ParseSVG([]byte(svg))
	if err != nil {
		t.Fatal(err)
	}
	tt := ui.NewTester(func(c *ui.Context) {
		c.Root().Background(ui.Transparent)
		ui.Box(c).Padding(margin).Children(func() {
			ui.Image(c, icon).Size(size-2*margin, size-2*margin)
		})
	}, int(size), int(size))
	tt.Frame()
	return tt.Image()
}

// TestRenderedImages checks the PNGs made from the brand SVGs: the menu bar
// template images and the app icon. Run with -update after changing an SVG.
func TestRenderedImages(t *testing.T) {
	mark, err := os.ReadFile("brand/mark.svg")
	if err != nil {
		t.Fatal(err)
	}
	app, err := os.ReadFile("brand/app-icon.svg")
	if err != nil {
		t.Fatal(err)
	}
	const piece = `opacity=".42"`
	if !strings.Contains(string(mark), piece) {
		t.Fatalf("brand/mark.svg lost its %s square", piece)
	}
	for _, out := range []struct {
		path string
		img  *image.RGBA
	}{
		{trayFile(TrayIdle), render(t, string(mark), 36, 0)},
		{trayFile(TrayRunning), render(t, strings.Replace(string(mark), piece, `opacity="1"`, 1), 36, 0)},
		{trayFile(TrayOffline), render(t, strings.Replace(string(mark), piece, `opacity=".12"`, 1), 36, 0)},
		// The macOS icon grid: an 824-point tile centered on 1024.
		{"../../resources/icon.png", render(t, string(app), 1024, 100)},
	} {
		if *update {
			var b bytes.Buffer
			if err := png.Encode(&b, out.img); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(out.path, b.Bytes(), 0o644); err != nil {
				t.Fatal(err)
			}
			continue
		}
		f, err := os.Open(out.path)
		if err != nil {
			t.Fatalf("%v (run go test -update)", err)
		}
		want, err := png.Decode(f)
		f.Close()
		if err != nil {
			t.Fatal(err)
		}
		if d := maxDiff(out.img, want); d > 2 {
			t.Errorf("%s differs from its SVG by %d; run go test -update", out.path, d)
		}
	}
}

// maxDiff is the largest difference of a channel between two images of
// the same size, or 255 when the sizes differ.
func maxDiff(a *image.RGBA, b image.Image) int {
	if a.Bounds() != b.Bounds() {
		return 255
	}
	worst := 0
	for y := a.Rect.Min.Y; y < a.Rect.Max.Y; y++ {
		for x := a.Rect.Min.X; x < a.Rect.Max.X; x++ {
			r1, g1, b1, a1 := a.At(x, y).RGBA()
			r2, g2, b2, a2 := b.At(x, y).RGBA()
			for _, d := range []int{int(r1>>8) - int(r2>>8), int(g1>>8) - int(g2>>8), int(b1>>8) - int(b2>>8), int(a1>>8) - int(a2>>8)} {
				worst = max(worst, d, -d)
			}
		}
	}
	return worst
}

func TestLoad(t *testing.T) {
	s, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if s.Mark == nil || s.Claude == nil || s.Pin == nil {
		t.Fatalf("set %+v", s)
	}
	for _, st := range []TrayState{TrayIdle, TrayRunning, TrayOffline} {
		if _, err := png.Decode(bytes.NewReader(Tray(st))); err != nil {
			t.Errorf("tray %d: %v", st, err)
		}
	}
}

func TestFileType(t *testing.T) {
	s, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct{ name, want string }{
		{"cmd/main.go", "go"}, {"go.mod", "go-mod"}, {"Makefile", "makefile"}, {"web/App.TSX", "react_ts"}, {"notes", "document"},
	} {
		if got := s.FileType(tc.name); got == nil || got != s.files[tc.want] {
			t.Errorf("%s: not the %s icon", tc.name, tc.want)
		}
	}
	for _, name := range strings.Fields("a.go a.swift a.json a.toml a.html a.css a.xml a.lock a.java a.php a.vue a.svelte a.proto a.ts a.js a.jsx a.py a.rs a.md a.yml a.sh a.scss a.png a.sql a.c a.cpp a.kt a.rb a.ini Dockerfile .gitignore") {
		if s.FileType(name) == nil {
			t.Errorf("%s has no icon file", name)
		}
	}
	if s.FolderType() == nil {
		t.Error("no folder icon")
	}
}
