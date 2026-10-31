package proto

import (
	"encoding/json"
	"testing"
)

func TestCapabilitiesJSON(t *testing.T) {
	b, err := json.Marshal(Capabilities{Steer: true, PartialStream: true})
	if err != nil {
		t.Fatal(err)
	}
	if got, want := string(b), `{"steer":true,"partial_stream":true}`; got != want {
		t.Fatalf("got %s, want %s", got, want)
	}
	var back Capabilities
	if err := json.Unmarshal(b, &back); err != nil {
		t.Fatal(err)
	}
	if !back.Steer || !back.PartialStream || back.Resume {
		t.Fatalf("round trip %+v", back)
	}
}

func TestCapabilitiesSetModeModelJSON(t *testing.T) {
	b, err := json.Marshal(Capabilities{SetMode: true, SetModel: true})
	if err != nil {
		t.Fatal(err)
	}
	if got, want := string(b), `{"set_mode":true,"set_model":true}`; got != want {
		t.Fatalf("got %s, want %s", got, want)
	}
}
