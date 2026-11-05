package shell

import (
	"math"
	"testing"
	"time"

	"github.com/yangflow/hoplet/desktop/internal/theme"
	"github.com/yangflow/hoplet/proto"
)

// TestLayoutFromTokens measures the window as the canvas draws it: the
// sidebar's width, a session row, the title bar and how far the composer
// stands from the bottom all come out of the tokens.
func TestLayoutFromTokens(t *testing.T) {
	a, _, tt, _ := newApp(t)
	tt.SetSize(theme.WindowWidth, theme.WindowHeight)
	selectRunning(t, a, tt)
	tt.Advance(time.Second)
	tt.Frame()
	near := func(name string, got, want float32) {
		t.Helper()
		if math.Abs(float64(got-want)) > 0.5 {
			t.Errorf("%s: %v, want %v", name, got, want)
		}
	}
	near("sidebar width", a.side, theme.SidebarWidth)
	row, ok := tt.Find("Fix sync")
	if !ok {
		t.Fatalf("no session row in %q", tt.Texts())
	}
	near("session row height", row.H, theme.Row)
	details, ok := tt.Find("会话详情 ⌘I")
	if !ok {
		t.Fatal("no details button")
	}
	near("title bar button", details.H, theme.Row)
	near("title bar middle", details.Y+details.H/2, theme.TitleBar/2)
	stop, ok := tt.Find("停止 ⎋")
	if !ok {
		t.Fatal("no stop button")
	}
	// The composer stands space-4 off the bottom; its controls sit
	// space-2 above its edge, inside its line.
	near("composer from the bottom", theme.WindowHeight-(stop.Y+stop.H), theme.Space4+theme.Space2+theme.LineWidth)
}

// TestSidebarOpensByWidth closes and opens the sidebar: on the way the
// conversation's composer moves with its edge, and at rest the sidebar is
// gone or back at its width, its edge a divider again.
func TestSidebarOpensByWidth(t *testing.T) {
	a, _, tt, _ := newApp(t)
	tt.SetSize(theme.WindowWidth, theme.WindowHeight)
	a.SetHarnesses([]proto.HarnessInfo{claudeHarness()})
	a.Select("s1")
	tt.Frame()
	composer := func() float32 {
		r, ok := tt.Find("消息")
		if !ok {
			t.Fatalf("no composer in %q", tt.Texts())
		}
		return r.X
	}
	open := composer()
	a.ToggleSidebar()
	tt.Frame()
	tt.Advance(theme.MotionSmooth.Spring.Duration / 4)
	tt.Frame()
	mid := composer()
	tt.Advance(time.Second)
	closed := composer()
	if !(closed < mid && mid < open) {
		t.Fatalf("the composer at %v, %v, %v while the sidebar closes", open, mid, closed)
	}
	if tt.HasText("全部机器") {
		t.Fatal("the sidebar stays after closing")
	}
	a.ToggleSidebar()
	tt.Advance(time.Second)
	if x := composer(); x != open {
		t.Fatalf("opened with the composer at %v, want %v", x, open)
	}
	if _, ok := tt.Find("Divider"); !ok {
		t.Fatal("the open sidebar has no divider to drag")
	}
}
