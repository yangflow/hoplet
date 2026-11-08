package simulator

import (
	"bytes"
	"context"
	"errors"
	"image"
	"image/color"
	"image/png"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func encodePNG(t *testing.T, img image.Image) []byte {
	t.Helper()
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

// shotRunner returns a runner whose screenshot command writes data to the
// path it receives and records that path.
func shotRunner(data []byte, shotErr error) (*fakeRunner, *string) {
	f := listRunner()
	var path string
	f.hook = func(_ string, args []string) ([]byte, error, bool) {
		if len(args) >= 4 && args[1] == "io" && args[3] == "screenshot" {
			path = args[len(args)-1]
			if shotErr != nil {
				return nil, shotErr, true
			}
			return nil, os.WriteFile(path, data, 0o600), true
		}
		return nil, nil, false
	}
	return f, &path
}

func gone(t *testing.T, path string) {
	t.Helper()
	if path == "" {
		t.Fatal("screenshot path was never seen")
	}
	if _, err := os.Stat(filepath.Dir(path)); !os.IsNotExist(err) {
		t.Fatalf("temp dir %s still exists (err = %v)", filepath.Dir(path), err)
	}
}

func TestScreenshotBoxDownscale(t *testing.T) {
	// 3x pixels for a 2x2-point screen: each 3x3 block mixes two values.
	img := image.NewNRGBA(image.Rect(0, 0, 6, 6))
	for y := 0; y < 6; y++ {
		for x := 0; x < 6; x++ {
			v := uint8(0)
			// Block (0,0): 4 pixels of 90, rest 0 -> mean 40. Block (1,1): all 200.
			if x < 3 && y < 3 && (x+y)%2 == 0 && x+y < 4 {
				v = 90
			}
			if x >= 3 && y >= 3 {
				v = 200
			}
			img.SetNRGBA(x, y, color.NRGBA{R: v, G: 255 - v, B: 7, A: 255})
		}
	}
	f, path := shotRunner(encodePNG(t, img), nil)
	f.out["simctl list -j devices"] = `{"devices":{"com.apple.CoreSimulator.SimRuntime.iOS-18-0":[{"name":"P","udid":"U1","state":"Booted","isAvailable":true,"deviceTypeIdentifier":"type.phone-a"}]}}`
	f.out[plutilKey("/types/PhoneA.simdevicetype")] = capabilitiesJSON(3, 6, 6)
	s := newSvc(f)
	shot, err := s.Screenshot(context.Background(), "ses_1", "U1")
	if err != nil {
		t.Fatal(err)
	}
	if shot.Width != 2 || shot.Height != 2 {
		t.Fatalf("shot size = %dx%d", shot.Width, shot.Height)
	}
	out, err := png.Decode(bytes.NewReader(shot.PNG))
	if err != nil {
		t.Fatal(err)
	}
	if b := out.Bounds(); b.Dx() != 2 || b.Dy() != 2 {
		t.Fatalf("png bounds = %v", b)
	}
	r, g, b, _ := out.At(1, 1).RGBA()
	if r>>8 != 200 || g>>8 != 55 || b>>8 != 7 {
		t.Fatalf("pixel(1,1) = %d %d %d", r>>8, g>>8, b>>8)
	}
	r, g, _, _ = out.At(1, 0).RGBA()
	if r>>8 != 0 || g>>8 != 255 {
		t.Fatalf("pixel(1,0) = %d %d", r>>8, g>>8)
	}
	// Block (0,0) has pixels with x+y<4 and even sum: (0,0),(1,1),(2,0),(0,2)
	// = 4 pixels of 90 -> mean 360/9 = 40.
	r, g, _, _ = out.At(0, 0).RGBA()
	if r>>8 != 40 || g>>8 != 255-40 {
		t.Fatalf("pixel(0,0) = %d %d, want 40 215", r>>8, g>>8)
	}
	gone(t, *path)
}

func TestScreenshotScaleOneUnchanged(t *testing.T) {
	img := image.NewNRGBA(image.Rect(0, 0, 4, 2))
	img.SetNRGBA(1, 1, color.NRGBA{R: 9, G: 8, B: 7, A: 255})
	data := encodePNG(t, img)
	f, path := shotRunner(data, nil)
	f.out["simctl list -j devices"] = `{"devices":{"com.apple.CoreSimulator.SimRuntime.iOS-18-0":[{"name":"P","udid":"U1","state":"Booted","isAvailable":true,"deviceTypeIdentifier":"type.phone-a"}]}}`
	f.out[plutilKey("/types/PhoneA.simdevicetype")] = capabilitiesJSON(1, 4, 2)
	shot, err := newSvc(f).Screenshot(context.Background(), "", "U1")
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(shot.PNG, data) || shot.Width != 4 || shot.Height != 2 {
		t.Fatalf("shot = %dx%d, unchanged = %v", shot.Width, shot.Height, bytes.Equal(shot.PNG, data))
	}
	gone(t, *path)
}

func TestScreenshotCommand(t *testing.T) {
	img := image.NewNRGBA(image.Rect(0, 0, 1179, 2556))
	f, path := shotRunner(encodePNG(t, img), nil)
	shot, err := newSvc(f).Screenshot(context.Background(), "", "UDID-A18")
	if err != nil {
		t.Fatal(err)
	}
	if shot.Width != 393 || shot.Height != 852 {
		t.Fatalf("size = %dx%d", shot.Width, shot.Height)
	}
	last := f.calls[len(f.calls)-1]
	if last.name != "xcrun" || strings.Join(last.args[:5], " ") != "simctl io UDID-A18 screenshot --type=png" {
		t.Fatalf("last call = %+v", last)
	}
	if len(last.args) != 6 || last.args[5] != *path {
		t.Fatalf("args = %v", last.args)
	}
}

func TestScreenshotFailures(t *testing.T) {
	good := encodePNG(t, image.NewNRGBA(image.Rect(0, 0, 1179, 2556)))
	notDivisible := encodePNG(t, image.NewNRGBA(image.Rect(0, 0, 1180, 2556)))
	tests := []struct {
		name     string
		device   string
		data     []byte
		shotErr  error
		mutate   func(*fakeRunner)
		wantCode string
		wantShot bool // whether simctl io is expected to run
	}{
		{name: "not booted", device: "UDID-A17", data: good, wantCode: CodeNotBooted},
		{name: "scale unreadable", device: "UDID-A18", data: good, mutate: func(f *fakeRunner) {
			delete(f.out, plutilKey("/types/PhoneA.simdevicetype"))
		}},
		{name: "not divisible", device: "UDID-A18", data: notDivisible, wantShot: true},
		{name: "invalid png", device: "UDID-A18", data: []byte("nope"), wantShot: true},
		{name: "simctl fails", device: "UDID-A18", data: good, shotErr: errors.New("boom"), wantShot: true},
		{name: "unknown device", device: "Nope", data: good, wantCode: CodeNotFound},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f, path := shotRunner(tt.data, tt.shotErr)
			if tt.mutate != nil {
				tt.mutate(f)
			}
			_, err := newSvc(f).Screenshot(context.Background(), "ses_1", tt.device)
			if err == nil {
				t.Fatal("expected error")
			}
			if tt.wantCode != "" {
				var se *Error
				if !errors.As(err, &se) || se.Code != tt.wantCode {
					t.Fatalf("err = %v, want code %s", err, tt.wantCode)
				}
			}
			if !tt.wantShot && *path != "" {
				t.Fatal("simctl io must not run")
			}
			if tt.wantShot {
				gone(t, *path)
			}
		})
	}
}

func TestDevicesGeometryFailureIsPerType(t *testing.T) {
	f := listRunner()
	delete(f.out, plutilKey("/types/PhoneTwin.simdevicetype"))
	got, err := newSvc(f).Devices(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	for _, d := range got {
		switch d.Name {
		case "Phone A":
			if d.Scale != 3 || d.Width != 393 || d.Height != 852 {
				t.Fatalf("%+v", d)
			}
		case "Phone Twin":
			if d.Scale != 0 || d.Width != 0 || d.Height != 0 {
				t.Fatalf("%+v", d)
			}
		}
	}
}

func TestGeometryCache(t *testing.T) {
	f := listRunner()
	s := newSvc(f)
	for i := 0; i < 2; i++ {
		if _, err := s.Devices(context.Background()); err != nil {
			t.Fatal(err)
		}
	}
	plutils := 0
	for _, c := range f.calls {
		if c.name == "plutil" {
			plutils++
		}
	}
	if plutils != 2 {
		t.Fatalf("plutil calls = %d, want 2 (one per device type)", plutils)
	}
	// A failed read is not cached: it is retried on the next listing.
	delete(f.out, plutilKey("/types/PhoneTwin.simdevicetype"))
	s2 := newSvc(f)
	_, _ = s2.Devices(context.Background())
	f.out[plutilKey("/types/PhoneTwin.simdevicetype")] = capabilitiesJSON(2, 750, 1334)
	got, _ := s2.Devices(context.Background())
	for _, d := range got {
		if d.Name == "Phone Twin" && d.Scale != 2 {
			t.Fatalf("failure was cached: %+v", d)
		}
	}
}

func TestDevicesIOSOnly(t *testing.T) {
	got, err := newSvc(listRunner()).Devices(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	for _, d := range got {
		if !strings.HasPrefix(d.Runtime, "iOS-") || d.Name == "Watch" {
			t.Fatalf("non-iOS device listed: %+v", d)
		}
	}
	if len(got) != 4 {
		t.Fatalf("devices = %d", len(got))
	}
}
