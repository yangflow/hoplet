package shell

import (
	"fmt"
	"time"

	"github.com/yangflow/hoplet/proto"
)

// name is how a session is shown: its title, or 新会话 while it has none.
func name(s proto.Session) string {
	if s.Title != "" {
		return s.Title
	}
	return "新会话"
}

// ago is a short relative time: 刚刚, 5 分, 3 小时, 2 天, then the date.
func ago(now, t time.Time) string {
	d := now.Sub(t)
	switch {
	case d < time.Minute:
		return "刚刚"
	case d < time.Hour:
		return fmt.Sprintf("%d 分", int(d/time.Minute))
	case d < 24*time.Hour:
		return fmt.Sprintf("%d 小时", int(d/time.Hour))
	case d < 30*24*time.Hour:
		return fmt.Sprintf("%d 天", int(d/(24*time.Hour)))
	}
	t = t.Local()
	return fmt.Sprintf("%d月%d日", t.Month(), t.Day())
}
