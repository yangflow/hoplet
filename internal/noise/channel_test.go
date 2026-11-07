package noise

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"net"
	"sync"
	"sync/atomic"
	"testing"
)

// pair returns two channels joined by a daily handshake over one pipe.
func pair(t *testing.T) (dial, acc *Channel, dp, ap *pipeEnd) {
	t.Helper()
	d, a, _, _ := configs(t, KindDaily)
	var dEnd, aEnd *pipeEnd
	ds, as := run(t, d, a, func(x, y *pipeEnd) { dEnd, aEnd = x, y })
	if ds.err != nil || as.err != nil {
		t.Fatalf("handshake: %v / %v", ds.err, as.err)
	}
	t.Cleanup(func() { ds.ch.Close(); as.ch.Close() })
	return ds.ch, as.ch, dEnd, aEnd
}

// pattern returns n bytes that differ for every seed and offset.
func pattern(seed, n int) []byte {
	p := make([]byte, n)
	for i := range p {
		p[i] = byte(seed*31 + i*7 + i>>8)
	}
	return p
}

// countConn counts the messages its reader takes.
type countConn struct {
	MsgConn
	reads atomic.Int64
}

func (c *countConn) ReadMessage(ctx context.Context) ([]byte, error) {
	c.reads.Add(1)
	return c.MsgConn.ReadMessage(ctx)
}

func TestFrameSizes(t *testing.T) {
	for _, n := range []int{0, 1, maxPiece, maxPiece + 1, 1 << 20, DefaultMaxFrame} {
		t.Run(fmt.Sprint(n), func(t *testing.T) {
			dial, acc, _, _ := pair(t)
			want := pattern(n, n)
			errc := make(chan error, 1)
			go func() { errc <- dial.WriteFrame(t.Context(), want) }()
			got, err := acc.ReadFrame(t.Context())
			if err != nil {
				t.Fatal(err)
			}
			if err := <-errc; err != nil {
				t.Fatal(err)
			}
			if got == nil || !bytes.Equal(got, want) {
				t.Fatalf("frame of %d bytes came back as %d bytes", n, len(got))
			}
		})
	}
}

func TestFrameTooLarge(t *testing.T) {
	t.Run("default", func(t *testing.T) {
		dial, acc, _, _ := pair(t)
		go func() { _ = dial.WriteFrame(t.Context(), make([]byte, DefaultMaxFrame+1)) }()
		if _, err := acc.ReadFrame(t.Context()); !errors.Is(err, ErrTooLarge) {
			t.Fatalf("got %v, want ErrTooLarge", err)
		}
	})
	t.Run("early", func(t *testing.T) {
		dial, acc, _, _ := pair(t)
		cc := &countConn{MsgConn: acc.c}
		acc.c, acc.maxFrame = cc, maxPiece+1
		go func() { _ = dial.WriteFrame(t.Context(), make([]byte, 5*maxPiece)) }()
		if _, err := acc.ReadFrame(t.Context()); !errors.Is(err, ErrTooLarge) {
			t.Fatalf("got %v, want ErrTooLarge", err)
		}
		if n := cc.reads.Load(); n != 2 {
			t.Fatalf("read %d messages before refusing, want 2", n)
		}
	})
}

func TestConcurrentWriters(t *testing.T) {
	dial, acc, _, _ := pair(t)
	const writers, frames = 8, 6
	var wg sync.WaitGroup
	for w := range writers {
		wg.Go(func() {
			for i := range frames {
				p := pattern(w*frames+i, maxPiece+100*w+i)
				p[0], p[1] = byte(w), byte(i)
				if err := dial.WriteFrame(t.Context(), p); err != nil {
					t.Error(err)
					return
				}
			}
		})
	}
	next := make([]int, writers)
	for range writers * frames {
		got, err := acc.ReadFrame(t.Context())
		if err != nil {
			t.Fatal(err)
		}
		w, i := int(got[0]), int(got[1])
		want := pattern(w*frames+i, maxPiece+100*w+i)
		want[0], want[1] = byte(w), byte(i)
		if !bytes.Equal(got, want) {
			t.Fatalf("frame %d from writer %d is not whole", i, w)
		}
		if i != next[w] {
			t.Fatalf("writer %d: frame %d arrived, want %d", w, i, next[w])
		}
		next[w]++
	}
	wg.Wait()
}

func TestChannelTampering(t *testing.T) {
	tests := []struct {
		name   string
		filter func() func(msg []byte) [][]byte
	}{
		{"replay", func() func([]byte) [][]byte {
			return func(m []byte) [][]byte { return [][]byte{m, m} }
		}},
		{"swap", func() func([]byte) [][]byte {
			var held []byte
			return func(m []byte) [][]byte {
				if held == nil {
					held = m
					return nil
				}
				return [][]byte{m, held}
			}
		}},
		{"drop", func() func([]byte) [][]byte {
			dropped := false
			return func(m []byte) [][]byte {
				if !dropped {
					dropped = true
					return nil
				}
				return [][]byte{m}
			}
		}},
		{"alter", func() func([]byte) [][]byte {
			return func(m []byte) [][]byte {
				m[len(m)/2] ^= 1
				return [][]byte{m}
			}
		}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dial, acc, dp, _ := pair(t)
			dp.setFilter(tt.filter())
			for _, f := range []string{"one", "two"} {
				if err := dial.WriteFrame(t.Context(), []byte(f)); err != nil {
					t.Fatal(err)
				}
			}
			var err error
			for err == nil {
				_, err = acc.ReadFrame(t.Context())
			}
			if !errors.Is(err, ErrDecrypt) {
				t.Fatalf("got %v, want ErrDecrypt", err)
			}
			if _, again := acc.ReadFrame(t.Context()); !errors.Is(again, ErrDecrypt) {
				t.Fatalf("after failure got %v, want the same error", again)
			}
		})
	}
}

// writeRaw sends one transport message with the given plaintext.
func writeRaw(t *testing.T, ch *Channel, pt []byte) {
	t.Helper()
	ch.wmu.Lock()
	defer ch.wmu.Unlock()
	ct, err := ch.send.Encrypt(nil, nil, pt)
	if err != nil {
		t.Fatal(err)
	}
	if err := ch.c.WriteMessage(t.Context(), ct); err != nil {
		t.Fatal(err)
	}
}

// readRaw takes one transport message and returns its plaintext.
func readRaw(t *testing.T, ch *Channel) []byte {
	t.Helper()
	ct, err := ch.c.ReadMessage(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	pt, err := ch.recv.Decrypt(nil, nil, ct)
	if err != nil {
		t.Fatal(err)
	}
	return pt
}

func TestBadMessages(t *testing.T) {
	for _, tt := range []struct {
		name string
		pt   []byte
	}{
		{"empty", nil},
		{"unknown flag", []byte{4, 'x'}},
		{"short ping", []byte{flagPing, 1, 2, 3}},
		{"long pong", append([]byte{flagPong}, make([]byte, nonceSize+1)...)},
	} {
		t.Run(tt.name, func(t *testing.T) {
			dial, acc, _, _ := pair(t)
			writeRaw(t, dial, tt.pt)
			if _, err := acc.ReadFrame(t.Context()); !errors.Is(err, ErrFrame) {
				t.Fatalf("got %v, want ErrFrame", err)
			}
		})
	}
}

func TestPingInsideFrame(t *testing.T) {
	dial, acc, _, _ := pair(t)
	nonce := [nonceSize]byte{1, 2, 3, 4, 5, 6, 7, 8}
	writeRaw(t, dial, []byte{flagMore, 'a', 'b'})
	writeRaw(t, dial, append([]byte{flagPing}, nonce[:]...))
	writeRaw(t, dial, []byte{flagLast, 'c', 'd'})
	got, err := acc.ReadFrame(t.Context())
	if err != nil || string(got) != "abcd" {
		t.Fatalf("got %q, %v", got, err)
	}
	if pt := readRaw(t, dial); !bytes.Equal(pt, append([]byte{flagPong}, nonce[:]...)) {
		t.Fatalf("pong is %x", pt)
	}
}

func TestPingRoundTrip(t *testing.T) {
	dial, acc, _, _ := pair(t)
	go func() { _, _ = acc.ReadFrame(t.Context()) }()
	go func() { _, _ = dial.ReadFrame(t.Context()) }()
	for range 3 {
		if err := dial.Ping(t.Context()); err != nil {
			t.Fatal(err)
		}
	}
}

func TestPingWrongNonce(t *testing.T) {
	dial, acc, _, _ := pair(t)
	frames := make(chan string, 1)
	go func() {
		for {
			f, err := dial.ReadFrame(t.Context())
			if err != nil {
				return
			}
			frames <- string(f)
		}
	}()
	pinged := make(chan error, 1)
	go func() { pinged <- dial.Ping(t.Context()) }()

	pt := readRaw(t, acc)
	if pt[0] != flagPing {
		t.Fatalf("flag %d, want ping", pt[0])
	}
	nonce := [nonceSize]byte(pt[1:])
	wrong := nonce
	wrong[0] ^= 1
	writeRaw(t, acc, append([]byte{flagPong}, wrong[:]...))
	writeRaw(t, acc, []byte{flagLast, 'm'})
	<-frames // the reader has seen the wrong pong
	select {
	case err := <-pinged:
		t.Fatalf("ping returned %v on a wrong nonce", err)
	default:
	}
	writeRaw(t, acc, append([]byte{flagPong}, nonce[:]...))
	if err := <-pinged; err != nil {
		t.Fatal(err)
	}
}

func TestPingEnds(t *testing.T) {
	stop := errors.New("stop")
	for _, tt := range []struct {
		name string
		end  func(ch *Channel, cancel context.CancelCauseFunc)
		want error
	}{
		{"context", func(_ *Channel, cancel context.CancelCauseFunc) { cancel(stop) }, stop},
		{"close", func(ch *Channel, _ context.CancelCauseFunc) { ch.Close() }, net.ErrClosed},
	} {
		t.Run(tt.name, func(t *testing.T) {
			dial, acc, _, _ := pair(t)
			ctx, cancel := context.WithCancelCause(t.Context())
			defer cancel(nil)
			pinged := make(chan error, 1)
			go func() { pinged <- dial.Ping(ctx) }()
			readRaw(t, acc) // the ping is out; nobody answers
			tt.end(dial, cancel)
			if err := <-pinged; !errors.Is(err, tt.want) {
				t.Fatalf("got %v, want %v", err, tt.want)
			}
		})
	}
}

func TestTooManyPings(t *testing.T) {
	dial, acc, _, _ := pair(t)
	// Hold the acceptor's writer so its pongs pile up.
	acc.wmu.Lock()
	defer acc.wmu.Unlock()
	go func() {
		for i := range maxPongs + 2 {
			var nonce [nonceSize]byte
			nonce[0] = byte(i)
			writeRaw(t, dial, append([]byte{flagPing}, nonce[:]...))
		}
	}()
	if _, err := acc.ReadFrame(t.Context()); !errors.Is(err, ErrFrame) {
		t.Fatalf("got %v, want ErrFrame", err)
	}
}

func TestClose(t *testing.T) {
	dial, acc, _, _ := pair(t)
	for range 2 {
		if err := dial.Close(); err != nil {
			t.Fatal(err)
		}
	}
	if err := dial.WriteFrame(t.Context(), []byte("x")); !errors.Is(err, net.ErrClosed) {
		t.Fatalf("write after close: %v", err)
	}
	if _, err := dial.ReadFrame(t.Context()); !errors.Is(err, net.ErrClosed) {
		t.Fatalf("read after close: %v", err)
	}
	if err := dial.Ping(t.Context()); !errors.Is(err, net.ErrClosed) {
		t.Fatalf("ping after close: %v", err)
	}
	if _, err := acc.ReadFrame(t.Context()); !errors.Is(err, net.ErrClosed) {
		t.Fatalf("peer read after close: %v", err)
	}
}

func TestChannelRace(t *testing.T) {
	dial, acc, _, _ := pair(t)
	ctx := t.Context()
	var wg sync.WaitGroup
	for _, ch := range []*Channel{dial, acc} {
		wg.Go(func() {
			for {
				if _, err := ch.ReadFrame(ctx); err != nil {
					return
				}
			}
		})
	}
	var sent atomic.Int64
	for w := range 4 {
		for _, ch := range []*Channel{dial, acc} {
			wg.Go(func() {
				for i := 0; ; i++ {
					if ch.WriteFrame(ctx, pattern(w+i, (i%3)*maxPiece/2)) != nil {
						return
					}
					sent.Add(1)
				}
			})
			wg.Go(func() {
				for ch.Ping(ctx) == nil {
				}
			})
		}
	}
	for sent.Load() < 200 {
		if err := dial.Ping(ctx); err != nil {
			t.Fatal(err)
		}
	}
	dial.Close()
	acc.Close()
	wg.Wait()
}
