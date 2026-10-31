package proto

import (
	"encoding/json"
	"errors"
	"fmt"
	"time"
)

// SessionID identifies a session across hosts and restarts.
type SessionID string

// EventType names the kind of an event's payload.
type EventType string

// BlobRef points at a content-addressed blob stored outside the event log.
type BlobRef struct {
	SHA256 string `json:"sha256"`
	Size   int64  `json:"size"`
}

// Event is one entry of a session's event log. Seq increases by one per
// session and never resets. Timestamps are UTC with millisecond precision.
// Payload holds the JSON encoding of a Payload whose type and version are
// Type and V.
type Event struct {
	Session SessionID `json:"session"`
	Seq     uint64    `json:"seq"`
	Time    time.Time `json:"ts"`
	// Turn numbers turns from 1; 0 (omitted) means the event belongs to no turn.
	Turn    uint32          `json:"turn,omitempty"`
	Type    EventType       `json:"type"`
	V       uint16          `json:"v"`
	Payload json.RawMessage `json:"payload"`
	Raw     *BlobRef        `json:"raw,omitempty"`
}

// Payload is implemented by every event payload type. Its methods are
// unexported so that payloads are defined only in this package.
type Payload interface {
	eventType() EventType
	eventVersion() uint16
}

// ErrUnknownEvent is returned when decoding an event whose type and version
// have no registered decoder.
var ErrUnknownEvent = errors.New("proto: unknown event type or version")

type decoderKey struct {
	t EventType
	v uint16
}

// ErrNilPayload is returned by NewEvent when the payload is nil.
var ErrNilPayload = errors.New("proto: nil payload")

// decoders is written only during package init and read-only afterwards.
var decoders = map[decoderKey]func(json.RawMessage) (Payload, error){}

func addDecoder(k decoderKey, dec func(json.RawMessage) (Payload, error)) {
	if _, dup := decoders[k]; dup {
		panic(fmt.Sprintf("proto: duplicate decoder for %s v%d", k.t, k.v))
	}
	decoders[k] = dec
}

// registerLegacy registers a decoder for an older version of an event type.
// upgrade converts the raw payload of that version to the current payload
// type, so Decode always returns the current type. Init-time only; it panics
// on a duplicate key.
func registerLegacy(t EventType, v uint16, upgrade func(json.RawMessage) (Payload, error)) {
	addDecoder(decoderKey{t, v}, upgrade)
}

// register makes payload type P decodable at its current version.
func register[P Payload]() {
	var zero P
	k := decoderKey{zero.eventType(), zero.eventVersion()}
	addDecoder(k, func(b json.RawMessage) (Payload, error) {
		var p P
		if err := json.Unmarshal(b, &p); err != nil {
			return nil, err
		}
		return p, nil
	})
}

// NewEvent builds an event carrying p.
func NewEvent(session SessionID, seq uint64, turn uint32, t time.Time, p Payload) (Event, error) {
	if p == nil {
		return Event{}, ErrNilPayload
	}
	b, err := json.Marshal(p)
	if err != nil {
		return Event{}, fmt.Errorf("proto: encode %s: %w", p.eventType(), err)
	}
	return Event{
		Session: session,
		Seq:     seq,
		Time:    t.UTC().Truncate(time.Millisecond),
		Turn:    turn,
		Type:    p.eventType(),
		V:       p.eventVersion(),
		Payload: b,
	}, nil
}

// Decode returns the event's payload as its registered Go type.
func (e Event) Decode() (Payload, error) {
	dec, ok := decoders[decoderKey{e.Type, e.V}]
	if !ok {
		return nil, fmt.Errorf("%w: %s v%d", ErrUnknownEvent, e.Type, e.V)
	}
	p, err := dec(e.Payload)
	if err != nil {
		return nil, fmt.Errorf("proto: decode %s v%d: %w", e.Type, e.V, err)
	}
	return p, nil
}
