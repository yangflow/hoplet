package proto

import (
	"encoding/json"
	"errors"
	"testing"
	"time"
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

func TestFallback(t *testing.T) {
	all := Capabilities{Modes: []PermissionMode{ModeConfirm, ModeAutoEdit, ModeAutoReview, ModeFullAuto}}
	noReview := Capabilities{Modes: []PermissionMode{ModeConfirm, ModeAutoEdit, ModeFullAuto}}
	confirmOnly := Capabilities{Modes: []PermissionMode{ModeConfirm}}
	for name, tc := range map[string]struct {
		caps     Capabilities
		in, want PermissionMode
	}{
		"has it":             {all, ModeAutoReview, ModeAutoReview},
		"stricter neighbour": {noReview, ModeAutoReview, ModeAutoEdit},
		"full auto kept":     {noReview, ModeFullAuto, ModeFullAuto},
		"only confirm":       {confirmOnly, ModeFullAuto, ModeConfirm},
		"no modes declared":  {Capabilities{}, ModeAutoEdit, ModeConfirm},
		"unknown mode":       {all, "bogus", ModeConfirm},
	} {
		if got := tc.caps.Fallback(tc.in); got != tc.want {
			t.Errorf("%s: Fallback(%s) = %s, want %s", name, tc.in, got, tc.want)
		}
	}
}

func TestModeAndEffortValidity(t *testing.T) {
	for _, m := range []PermissionMode{ModeConfirm, ModeAutoEdit, ModeAutoReview, ModeFullAuto} {
		if !m.Valid() {
			t.Errorf("%s invalid", m)
		}
	}
	for _, m := range []PermissionMode{"", "read_only", "plan"} {
		if m.Valid() {
			t.Errorf("%q valid", m)
		}
	}
	for _, e := range []Effort{EffortLow, EffortMedium, EffortHigh, EffortXHigh, EffortMax, EffortUltra} {
		if !e.Valid() {
			t.Errorf("%s invalid", e)
		}
	}
	if Effort("").Valid() || Effort("huge").Valid() {
		t.Error("an unknown effort is valid")
	}
	c := Capabilities{Efforts: []Effort{EffortLow, EffortHigh}}
	if !c.HasEffort(EffortHigh) || c.HasEffort(EffortMax) {
		t.Errorf("HasEffort on %v", c.Efforts)
	}
}

func TestModeChangedValidates(t *testing.T) {
	if _, err := NewEvent("s", 1, 1, time.Now(), ModeChanged{Permission: "read_only"}); !errors.Is(err, ErrInvalidPayload) {
		t.Fatalf("unknown mode: %v", err)
	}
	if _, err := NewEvent("s", 1, 1, time.Now(), ModeChanged{Permission: ModeAutoEdit, Plan: true}); err != nil {
		t.Fatal(err)
	}
}
