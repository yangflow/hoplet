package proto

// IsVoice reports whether c says why a voice call cannot start or has ended.
func (c ErrorCode) IsVoice() bool {
	switch c {
	case CodeVoiceNoHarness, CodeVoiceNotSubscribed, CodeVoiceNoComponent, CodeVoiceBusy,
		CodeVoiceMedia, CodeVoiceQuota, CodeVoiceNotEnabled, CodeVoiceEnded:
		return true
	}
	return false
}

// VoiceCallID identifies one voice call.
type VoiceCallID string

// VoiceProbeResult says whether this host can take a call now.
type VoiceProbeResult struct {
	// Provider names the voice service; empty when the host has none.
	Provider  string `json:"provider,omitempty"`
	Available bool   `json:"available"`
	// Reason is one of the voice error codes when Available is false.
	Reason ErrorCode `json:"reason,omitempty"`
	// BusyDevice is the device holding the call when Reason is
	// CodeVoiceBusy.
	BusyDevice string `json:"busy_device,omitempty"`
}

// VoiceStartParams starts a call with the client's media offer. The offer
// is never logged or stored.
type VoiceStartParams struct {
	Offer string `json:"offer"`
	// Voice is the provider's voice name; empty means its default.
	Voice string `json:"voice,omitempty"`
}

// VoiceStartResult carries the media answer for the client. The answer is
// never logged or stored.
type VoiceStartResult struct {
	Call   VoiceCallID `json:"call"`
	Answer string      `json:"answer"`
}

// VoiceCallRef names a call in voice.confirm, voice.heartbeat and
// voice.stop.
type VoiceCallRef struct {
	Call VoiceCallID `json:"call"`
}
