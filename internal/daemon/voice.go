package daemon

import (
	"context"

	"github.com/yangflow/hoplet/proto"
)

// voiceCall serves the voice.* methods for the connection's device.
func (c *conn) voiceCall(ctx context.Context, f proto.ClientFrame) (any, error) {
	v := c.s.voice
	if f.Method == proto.MethodVoiceProbe {
		return v.Probe(ctx), nil
	}
	if f.Method == proto.MethodVoiceStart {
		var p proto.VoiceStartParams
		if err := decode(f, &p); err != nil {
			return nil, err
		}
		return v.Start(ctx, c.device, p)
	}
	var p proto.VoiceCallRef
	if err := decode(f, &p); err != nil {
		return nil, err
	}
	op := v.Stop
	switch f.Method {
	case proto.MethodVoiceConfirm:
		op = v.Confirm
	case proto.MethodVoiceHeartbeat:
		op = v.Heartbeat
	}
	return struct{}{}, op(c.device, p.Call)
}
