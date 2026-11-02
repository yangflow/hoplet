package hermes

import (
	"reflect"
	"testing"

	"github.com/yangflow/hoplet/proto"
)

// The shape of Hermes 0.22.5's model.options answer, with made-up
// providers and models.
const modelOptionsAnswer = `{"model":"codex/m2","provider":"local","providers":[
{"slug":"cloud","name":"Cloud","is_current":false,"authenticated":true,"models":["c1"],"capabilities":{"c1":{"fast":true,"reasoning":true}},"total_models":1},
{"slug":"off","name":"Signed out","is_current":false,"authenticated":false,"models":["x"]},
{"slug":"local","name":"local","is_current":true,"authenticated":true,"models":["codex/m1","codex/m2"],"capabilities":{"codex/m1":{"fast":false,"reasoning":true}},"aliases":{}}]}`

func TestParseModelOptions(t *testing.T) {
	got, err := parseModelOptions([]byte(modelOptionsAnswer))
	if err != nil {
		t.Fatal(err)
	}
	want := []proto.ModelInfo{
		{ID: "codex/m1", Provider: "local", Name: "codex/m1", Description: "local"},
		{ID: "codex/m2", Provider: "local", Name: "codex/m2", Description: "local", Default: true},
		{ID: "c1", Provider: "cloud", Name: "c1", Description: "Cloud", Fast: true},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %+v\nwant %+v", got, want)
	}
	if _, err := parseModelOptions([]byte(`[`)); err == nil {
		t.Fatal("a broken answer parsed")
	}
}
