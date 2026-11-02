package daemon

import (
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/yangflow/hoplet/internal/harness/fake"
	"github.com/yangflow/hoplet/proto"
)

func TestModelsAndToolOutput(t *testing.T) {
	// A long output: 100-byte lines, more than proto.MaxOutput in all.
	long := strings.Repeat(strings.Repeat("x", 99)+"\n", proto.MaxOutput/100+50)
	h, script := newHome(t, fake.Script{Session: "n1", Turns: []fake.Turn{{Steps: []fake.Step{
		{Emit: &fake.Ev{Ev: "tool", ID: "t1", Kind: "exec", Name: "Bash", Summary: "ls", Status: "running"}},
		{Emit: &fake.Ev{Ev: "tool", ID: "t1", Kind: "exec", Name: "Bash", Summary: "ls", Status: "done", Text: "a.go\nb.go\n"}},
		{Emit: &fake.Ev{Ev: "tool", ID: "t2", Kind: "exec", Name: "Bash", Summary: "cat big", Status: "done", Text: long}},
		{EchoPrompt: true},
	}}}})
	cfg := testConfig(h, script)
	models := []proto.ModelInfo{{ID: "big", Name: "Big", Default: true}, {ID: "small", Name: "Small"}}
	cfg.Harnesses["fake"].(*fake.Harness).Models = models
	start(t, cfg)
	c := dial(t, h, "device:a")
	ctx := timeout(t)

	hs, err := c.Harnesses(ctx)
	if err != nil || len(hs) != 1 || hs[0].Kind != "fake" || !reflect.DeepEqual(hs[0].Models, models) || !hs[0].Capabilities.SetModel {
		t.Fatalf("harnesses %+v %v", hs, err)
	}

	sess, err := c.Create(ctx, proto.CreateParams{Mode: proto.ModeCode, Harness: "fake", Directory: filepath.Join(h, "work"), Model: "small"})
	if err != nil || sess.Model != "small" {
		t.Fatalf("create %+v %v", sess, err)
	}
	w := watch(t, c, sess.ID, 0)
	if _, err := c.Send(ctx, proto.SendParams{Session: sess.ID, Text: "go", Key: "k1"}); err != nil {
		t.Fatal(err)
	}
	w.until(turnEnded(1))

	out, err := c.Output(ctx, proto.OutputParams{Session: sess.ID, ToolCall: "t1"})
	if err != nil || out.Text != "a.go\nb.go\n" || out.Size != 10 || out.Truncated {
		t.Fatalf("output %+v %v", out, err)
	}
	big, err := c.Output(ctx, proto.OutputParams{Session: sess.ID, ToolCall: "t2"})
	if err != nil || !big.Truncated || big.Size != int64(len(long)) || len(big.Text) != proto.MaxOutput/100*100 || !strings.HasSuffix(big.Text, "x\n") {
		t.Fatalf("long output: %d bytes, size %d, truncated %v, %v", len(big.Text), big.Size, big.Truncated, err)
	}
	if _, err := c.Output(ctx, proto.OutputParams{Session: sess.ID, ToolCall: "nope"}); apiCode(err) != proto.CodeNotFound {
		t.Fatalf("unknown tool call: %v", err)
	}

	if err := c.SetModel(ctx, proto.SetModelParams{Session: sess.ID, Model: "big"}); err != nil {
		t.Fatal(err)
	}
	if st, err := c.Session(ctx, sess.ID); err != nil || st.Session.Model != "big" {
		t.Fatalf("after set_model %+v %v", st.Session, err)
	}
	if err := c.SetModel(ctx, proto.SetModelParams{Session: sess.ID}); apiCode(err) != proto.CodeInvalid {
		t.Fatalf("no model: %v", err)
	}
}
