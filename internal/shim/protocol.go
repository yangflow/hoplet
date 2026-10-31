package shim

// ProtocolVersion is the shim protocol version. Shims outlive daemons, so a
// daemon must speak the version of every shim still running.
const ProtocolVersion = 1

// RecordKind says what a spool record holds.
type RecordKind string

// Record kinds.
const (
	KindStart  RecordKind = "start"  // the child started; Pid is set
	KindStdout RecordKind = "stdout" // one line the child wrote to stdout, without the newline
	KindStderr RecordKind = "stderr" // one line the child wrote to stderr
	KindStdin  RecordKind = "stdin"  // one line written to the child's stdin
	KindExit   RecordKind = "exit"   // the child exited; Code or Signal is set
)

// Record is one spool line.
type Record struct {
	Kind   RecordKind `json:"k"`
	Data   string     `json:"d,omitempty"`
	Pid    int        `json:"pid,omitempty"`
	Code   int        `json:"code,omitempty"`
	Signal string     `json:"signal,omitempty"`
}

// MessageType names a frame.
type MessageType string

// Frames. Client → shim: hello, stdin, close_stdin, signal, ack.
// Shim → client: welcome, record, error.
const (
	MsgHello      MessageType = "hello"
	MsgStdin      MessageType = "stdin"
	MsgCloseStdin MessageType = "close_stdin"
	MsgSignal     MessageType = "signal"
	MsgAck        MessageType = "ack"
	MsgWelcome    MessageType = "welcome"
	MsgRecord     MessageType = "record"
	MsgError      MessageType = "error"
)

// Message is one frame. Offset is the spool byte offset: in hello, where to
// resume; in record, the offset just after that record; in ack, how far the
// client has durably consumed.
type Message struct {
	Type    MessageType `json:"type"`
	Version int         `json:"v,omitempty"`
	Offset  int64       `json:"off,omitempty"`
	Data    string      `json:"d,omitempty"`
	Signal  string      `json:"signal,omitempty"`
	Pid     int         `json:"pid,omitempty"`
	Record  *Record     `json:"rec,omitempty"`
	Error   string      `json:"error,omitempty"`
}
