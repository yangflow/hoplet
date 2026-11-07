package frame

import (
	"errors"

	"github.com/yangflow/hoplet/internal/noise"
)

// Shutdowner ends a transport in order.
type Shutdowner interface {
	Shutdown() error
}

// Noise carries frames in a Noise channel; under is the channel's
// transport, shut down in order by Shutdown.
func Noise(ch *noise.Channel, under Shutdowner) Conn { return noiseConn{ch, under} }

type noiseConn struct {
	*noise.Channel
	under Shutdowner
}

func (c noiseConn) Shutdown() error { return errors.Join(c.under.Shutdown(), c.Close()) }
