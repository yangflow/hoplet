// Command desktop is Hoplet's desktop app: a window over the local
// daemon's sessions, with a menu bar icon and a Dock badge.
package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"sync"
	"time"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"

	"github.com/yangflow/hoplet/desktop/internal/icons"
	"github.com/yangflow/hoplet/desktop/internal/link"
	"github.com/yangflow/hoplet/desktop/internal/shell"
	"github.com/yangflow/hoplet/desktop/internal/theme"
	"github.com/yangflow/hoplet/internal/client"
	"github.com/yangflow/hoplet/proto"
)

// callTimeout bounds one daemon request the window makes.
const callTimeout = 30 * time.Second

// outputTimeout is how long a tool's output box waits for it.
const outputTimeout = 10 * time.Second

func main() {
	if err := run(); err != nil {
		slog.Error("hoplet desktop", "err", err)
		os.Exit(1)
	}
}

func run() error {
	if !mygo.App.RequestSingleInstanceLock() {
		return nil
	}
	set, err := icons.Load()
	if err != nil {
		return err
	}
	if err := theme.RegisterFonts(); err != nil {
		return err
	}
	dial, err := link.DefaultDialer()
	if err != nil {
		return err
	}
	d := &desktop{wake: make(chan struct{})}
	d.app = shell.New(set, d, d.changed)
	mygo.App.SetMenu(d.menu())
	mygo.App.Dock.SetMenu(mygo.NewMenu([]*mygo.MenuItem{
		{Label: "新建会话", Click: d.front(func() { d.app.NewSession() })},
		{Label: "收件箱", Click: d.front(func() { d.app.OpenInbox() })},
	}))
	mygo.App.OnSecondInstance(func([]string, string) { d.show() })
	mygo.App.OnActivate(func(bool) { d.show() })
	mygo.App.OnBeforeQuit(func(*mygo.QuitEvent) { d.quitting = true })
	// While the app is in the background, new results stay unread.
	mygo.App.OnDidResignActive(func() { d.away(true) })
	mygo.App.OnDidBecomeActive(func() { d.away(false) })
	// The app keeps running with its window closed: the menu bar icon stays.
	mygo.App.OnWindowAllClosed(func() {})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	mygo.App.WhenReady(func() {
		d.win = mygo.NewWindow(mygo.WindowOptions{
			Title: "Hoplet", Width: 1280, Height: 800, MinWidth: 760, MinHeight: 480,
			// The traffic lights sit 19 points down, centered in a 52-point
			// title bar the sidebar's top row matches.
			TitleBarStyle: mygo.TitleBarHiddenInset, TrafficLightPosition: &mygo.Point{X: 20, Y: 19},
			Vibrancy: mygo.VibrancyUnderWindow, StateKey: "main",
			Content: ui.View(d.app.View),
		})
		d.win.OnClose(func(e *mygo.CloseEvent) {
			if !d.quitting {
				e.PreventDefault()
				d.win.Hide()
				d.away(true)
			}
		})
		tray, err := mygo.NewTray(mygo.TrayOptions{ID: "main", Icon: icons.Tray(icons.TrayOffline), IconIsTemplate: true, ToolTip: "Hoplet"})
		if err != nil {
			slog.Warn("menu bar icon", "err", err)
		}
		d.tray = tray
		d.changed(shell.State{})
		go link.Watch(ctx, dial, link.Handler{
			Wake: d.wake,
			Connected: func(c *client.Client, host string) {
				d.setClient(c)
				d.win.Update(func() { d.app.Connected(host) })
				d.Harnesses(d.app.SetHarnesses)
			},
			Lost: func(err error) {
				d.setClient(nil)
				d.win.Update(func() { d.app.Lost(err) })
			},
			Item: func(it proto.CatalogItem) { d.win.Update(func() { d.app.Apply(it) }) },
		})
	})
	return mygo.App.Run()
}

// desktop wires the window to the daemon, the menu bar, the menu bar icon
// and the Dock.
type desktop struct {
	app      *shell.App
	win      *mygo.Window
	tray     *mygo.Tray
	quitting bool

	// wake asks the connection to be tried again now.
	wake chan struct{}

	mu sync.Mutex
	c  *client.Client // the current connection, nil while there is none
	// unfollow ends following the session followed now.
	unfollow context.CancelFunc

	// Session menu items, enabled for the selected session.
	pin, unread, rename, reveal, stop, archive *mygo.MenuItem
	back, forward, newSession                  *mygo.MenuItem
}

func (d *desktop) setClient(c *client.Client) {
	d.mu.Lock()
	d.c = c
	d.mu.Unlock()
}

// call runs fn against the daemon off the UI thread; a failure shows in
// the window.
func (d *desktop) call(fn func(ctx context.Context, c *client.Client) error) {
	d.mu.Lock()
	c := d.c
	d.mu.Unlock()
	if c == nil {
		d.app.Fail(errors.New("没有连上守护进程"), nil)
		return
	}
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), callTimeout)
		defer cancel()
		if err := fn(ctx, c); err != nil {
			d.win.Update(func() { d.app.Fail(err, func() { d.call(fn) }) })
		}
	}()
}

// Update implements shell.Host.
func (d *desktop) Update(p proto.UpdateParams) {
	d.call(func(ctx context.Context, c *client.Client) error {
		_, err := c.Update(ctx, p)
		return err
	})
}

// Stop implements shell.Host.
func (d *desktop) Stop(id proto.SessionID) {
	d.call(func(ctx context.Context, c *client.Client) error { return c.Stop(ctx, id) })
}

// Archive implements shell.Host.
func (d *desktop) Archive(id proto.SessionID) {
	d.call(func(ctx context.Context, c *client.Client) error { return c.Archive(ctx, id) })
}

// Reveal implements shell.Host.
func (d *desktop) Reveal(dir string) { mygo.Shell.ShowItemInFolder(dir) }

// ui runs fn on the UI thread.
func (d *desktop) ui(fn func()) { d.win.Update(fn) }

// Create implements shell.Host.
func (d *desktop) Create(p proto.CreateParams, done func(proto.Session)) {
	d.call(func(ctx context.Context, c *client.Client) error {
		s, err := c.Create(ctx, p)
		if err == nil {
			d.ui(func() { done(s) })
		}
		return err
	})
}

// Send implements shell.Host.
func (d *desktop) Send(p proto.SendParams, done func(error)) {
	d.mu.Lock()
	offline := d.c == nil
	d.mu.Unlock()
	if offline && done != nil {
		done(errors.New("没有连上守护进程"))
	}
	d.call(func(ctx context.Context, c *client.Client) error {
		_, err := c.Send(ctx, p)
		if done != nil {
			d.win.Update(func() { done(err) })
		}
		return err
	})
}

// Steer implements shell.Host.
func (d *desktop) Steer(p proto.SteerParams) {
	d.call(func(ctx context.Context, c *client.Client) error { return c.Steer(ctx, p) })
}

// Interrupt implements shell.Host.
func (d *desktop) Interrupt(id proto.SessionID) {
	d.call(func(ctx context.Context, c *client.Client) error { return c.Interrupt(ctx, id) })
}

// Respond implements shell.Host.
func (d *desktop) Respond(p proto.RespondParams) {
	d.call(func(ctx context.Context, c *client.Client) error { return c.Respond(ctx, p) })
}

// Answer implements shell.Host.
func (d *desktop) Answer(p proto.AnswerParams) {
	d.call(func(ctx context.Context, c *client.Client) error { return c.Answer(ctx, p) })
}

// SetPermission implements shell.Host.
func (d *desktop) SetPermission(p proto.SetPermissionParams) {
	d.call(func(ctx context.Context, c *client.Client) error { return c.SetPermission(ctx, p) })
}

// SetPlan implements shell.Host.
func (d *desktop) SetPlan(p proto.SetPlanParams) {
	d.call(func(ctx context.Context, c *client.Client) error { return c.SetPlan(ctx, p) })
}

// SetEffort implements shell.Host.
func (d *desktop) SetEffort(p proto.SetEffortParams) {
	d.call(func(ctx context.Context, c *client.Client) error { return c.SetEffort(ctx, p) })
}

// SetUltracode implements shell.Host.
func (d *desktop) SetUltracode(p proto.SetUltracodeParams) {
	d.call(func(ctx context.Context, c *client.Client) error { return c.SetUltracode(ctx, p) })
}

// SetFast implements shell.Host.
func (d *desktop) SetFast(p proto.SetFastParams) {
	d.call(func(ctx context.Context, c *client.Client) error { return c.SetFast(ctx, p) })
}

// SetModel implements shell.Host.
func (d *desktop) SetModel(p proto.SetModelParams) {
	d.call(func(ctx context.Context, c *client.Client) error { return c.SetModel(ctx, p) })
}

// EditQueued implements shell.Host.
func (d *desktop) EditQueued(p proto.QueueEditParams) {
	d.call(func(ctx context.Context, c *client.Client) error { return c.EditQueued(ctx, p) })
}

// DeleteQueued implements shell.Host.
func (d *desktop) DeleteQueued(p proto.QueueKeyParams) {
	d.call(func(ctx context.Context, c *client.Client) error { return c.DeleteQueued(ctx, p) })
}

// MoveQueued implements shell.Host.
func (d *desktop) MoveQueued(p proto.QueueMoveParams) {
	d.call(func(ctx context.Context, c *client.Client) error { return c.MoveQueued(ctx, p) })
}

// Output implements shell.Host.
// The output box says why it has no output, so a failed read is handed
// back rather than shown as a toast.
func (d *desktop) Output(p proto.OutputParams, done func(proto.ToolOutput, error)) {
	d.mu.Lock()
	c := d.c
	d.mu.Unlock()
	if c == nil {
		done(proto.ToolOutput{}, errors.New("没有连上守护进程"))
		return
	}
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), outputTimeout)
		defer cancel()
		o, err := c.Output(ctx, p)
		if errors.Is(err, context.DeadlineExceeded) {
			err = errors.New("等了 10 秒没有回应")
		}
		d.ui(func() { done(o, err) })
	}()
}

// Diff implements shell.Host.
func (d *desktop) Diff(id proto.SessionID, done func(proto.SessionDiff)) {
	d.call(func(ctx context.Context, c *client.Client) error {
		diff, err := c.Diff(ctx, proto.DiffParams{Session: id})
		if err == nil {
			d.ui(func() { done(diff) })
		}
		return err
	})
}

// Reconnect implements shell.Host.
func (d *desktop) Reconnect() {
	select {
	case d.wake <- struct{}{}:
	default: // connected, or already trying
	}
}

// Older implements shell.Host.
func (d *desktop) Older(id proto.SessionID, before uint64, done func(proto.EventsPage)) {
	d.call(func(ctx context.Context, c *client.Client) error {
		page, err := c.EventsBefore(ctx, proto.EventsParams{Session: id, BeforeSeq: before})
		if err == nil {
			d.ui(func() { done(page) })
		}
		return err
	})
}

// Harnesses implements shell.Host.
func (d *desktop) Harnesses(done func([]proto.HarnessInfo)) {
	d.call(func(ctx context.Context, c *client.Client) error {
		hs, err := c.Harnesses(ctx)
		if err == nil {
			d.ui(func() { done(hs) })
		}
		return err
	})
}

// Session implements shell.Host.
func (d *desktop) Session(id proto.SessionID, done func(proto.SessionState)) {
	d.call(func(ctx context.Context, c *client.Client) error {
		st, err := c.Session(ctx, id)
		if err == nil {
			d.ui(func() { done(st) })
		}
		return err
	})
}

// Follow implements shell.Host: one session is followed at a time, until
// another is or the connection ends.
func (d *desktop) Follow(id proto.SessionID, after uint64) {
	d.mu.Lock()
	c := d.c
	if d.unfollow != nil {
		d.unfollow()
	}
	ctx, cancel := context.WithCancel(context.Background())
	d.unfollow = cancel
	d.mu.Unlock()
	if c == nil {
		return
	}
	go func() {
		err := link.Follow(ctx, c, id, after, func(it proto.SessionItem) {
			d.ui(func() { d.app.ApplySession(id, it) })
		})
		if ctx.Err() == nil {
			slog.Warn("follow session", "session", id, "err", err)
		}
	}()
}

// ChooseFolder implements shell.Host.
func (d *desktop) ChooseFolder(done func(string)) {
	go func() {
		paths, err := mygo.Dialog.Open(mygo.OpenDialogOptions{Parent: d.win, Title: "选择工作目录", Directory: true, CreateDirectories: true})
		if err != nil {
			d.ui(func() { d.app.Fail(err, nil) })
			return
		}
		if len(paths) == 1 {
			d.ui(func() { done(paths[0]) })
		}
	}()
}

// show brings the window forward.
func (d *desktop) show() {
	if d.win != nil {
		d.win.Show()
		d.win.Focus()
		d.away(false)
	}
}

// away tells the window whether I can see it.
func (d *desktop) away(away bool) {
	if d.win != nil {
		d.win.Update(func() { d.app.SetAway(away) })
	}
}

// do runs a window command from a menu.
func (d *desktop) do(fn func()) func(*mygo.MenuItem, *mygo.Window) {
	return func(*mygo.MenuItem, *mygo.Window) {
		if d.win != nil {
			d.win.Update(fn)
		}
	}
}

// front brings the window forward and runs fn in it.
func (d *desktop) front(fn func()) func(*mygo.MenuItem, *mygo.Window) {
	return func(*mygo.MenuItem, *mygo.Window) {
		d.show()
		if d.win != nil {
			d.win.Update(fn)
		}
	}
}

// menu builds the menu bar.
func (d *desktop) menu() *mygo.Menu {
	d.pin = &mygo.MenuItem{Label: "置顶", Accelerator: "Shift+CmdOrCtrl+P", Click: d.do(d.app.TogglePin)}
	d.unread = &mygo.MenuItem{Label: "标为未读", Accelerator: "Shift+CmdOrCtrl+U", Click: d.do(d.app.MarkUnread)}
	d.rename = &mygo.MenuItem{Label: "重命名", Click: d.do(d.app.Rename)}
	d.reveal = &mygo.MenuItem{Label: "在 Finder 中显示", Click: d.do(d.app.Reveal)}
	d.stop = &mygo.MenuItem{Label: "停止", Click: d.do(d.app.Stop)}
	d.archive = &mygo.MenuItem{Label: "归档", Accelerator: "CmdOrCtrl+Backspace", Click: d.do(d.app.Archive)}
	d.newSession = &mygo.MenuItem{Label: "新建会话", Accelerator: "CmdOrCtrl+N", Click: d.do(d.app.NewSession)}
	d.back = &mygo.MenuItem{Label: "后退", Accelerator: "CmdOrCtrl+[", Click: d.do(d.app.Back)}
	d.forward = &mygo.MenuItem{Label: "前进", Accelerator: "CmdOrCtrl+]", Click: d.do(d.app.Forward)}
	return mygo.NewMenu([]*mygo.MenuItem{
		{Role: mygo.RoleAppMenu},
		{Label: "文件", Submenu: []*mygo.MenuItem{d.newSession, mygo.Separator(), {Role: mygo.RoleClose}}},
		{Role: mygo.RoleEditMenu},
		{Label: "会话", Submenu: []*mygo.MenuItem{d.pin, d.unread, d.rename, mygo.Separator(), d.reveal, d.stop, d.archive}},
		{Label: "视图", Submenu: []*mygo.MenuItem{
			{Label: "Personal", Accelerator: "CmdOrCtrl+1", Click: d.do(func() { d.app.ModeKey(proto.ModePersonal) })},
			{Label: "Code", Accelerator: "CmdOrCtrl+2", Click: d.do(func() { d.app.ModeKey(proto.ModeCode) })},
			mygo.Separator(),
			{Label: "收件箱", Accelerator: "Shift+CmdOrCtrl+I", Click: d.do(d.app.OpenInbox)},
			{Label: "搜索", Accelerator: "CmdOrCtrl+K", Click: d.do(d.app.OpenPalette)},
			mygo.Separator(),
			d.back, d.forward,
			mygo.Separator(),
			{Label: "显示或隐藏侧栏", Accelerator: "Ctrl+Cmd+S", Click: d.do(d.app.ToggleSidebar)},
			{Role: mygo.RoleToggleFullScreen},
		}},
		{Role: mygo.RoleWindowMenu},
	})
}

// changed makes the menu bar, the menu bar icon and the Dock follow the
// window's state. It runs on the UI thread.
func (d *desktop) changed(st shell.State) {
	sel := st.Selected
	live := sel.ID != "" && !sel.Archived
	d.pin.SetEnabled(live)
	if sel.Pinned {
		d.pin.SetLabel("取消置顶")
	} else {
		d.pin.SetLabel("置顶")
	}
	d.unread.SetEnabled(sel.ID != "")
	d.rename.SetEnabled(sel.ID != "")
	d.reveal.SetEnabled(sel.HasDir)
	d.stop.SetEnabled(sel.Running)
	d.archive.SetEnabled(live)
	d.back.SetEnabled(st.CanBack)
	d.forward.SetEnabled(st.CanForward)

	mygo.App.SetBadgeCount(st.Counts.NeedsYou)
	if d.tray == nil {
		return
	}
	state := icons.TrayIdle
	switch {
	case !st.Connected:
		state = icons.TrayOffline
	case st.Counts.Running > 0:
		state = icons.TrayRunning
	}
	if err := d.tray.SetIcon(icons.Tray(state), true); err != nil {
		slog.Warn("menu bar icon", "err", err)
	}
	title := ""
	if st.Counts.NeedsYou > 0 {
		title = fmt.Sprint(st.Counts.NeedsYou)
	}
	d.tray.SetTitle(title)
	d.tray.SetMenu(d.trayMenu(st))
}

// trayMenu lists the sessions that need me and those running; choosing
// one opens it.
func (d *desktop) trayMenu(st shell.State) *mygo.Menu {
	var items []*mygo.MenuItem
	section := func(title string, es []shell.Entry) {
		if len(es) == 0 {
			return
		}
		items = append(items, &mygo.MenuItem{Label: title, Disabled: true})
		for _, e := range es {
			items = append(items, &mygo.MenuItem{Label: e.Name, Click: func(*mygo.MenuItem, *mygo.Window) {
				d.show()
				d.win.Update(func() { d.app.Select(e.ID) })
			}})
		}
		items = append(items, mygo.Separator())
	}
	section("需要你", st.NeedsYou)
	section("运行中", st.Running)
	items = append(items,
		&mygo.MenuItem{Label: "打开 Hoplet", Click: func(*mygo.MenuItem, *mygo.Window) { d.show() }},
		mygo.Separator(),
		&mygo.MenuItem{Role: mygo.RoleQuit, Label: "退出 Hoplet"},
	)
	return mygo.NewMenu(items)
}
