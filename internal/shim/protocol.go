package shim

// ProtocolVersion is the shim protocol version. Shims outlive daemons, so a
// daemon must speak the version of every shim still running.
const ProtocolVersion = 1

// ChunkSize bounds the data of one spool record. A longer line is split into
// consecutive records of the same kind, all but the last marked More.
const ChunkSize = 1 << 20

// RecordKind says what a spool record holds.
type RecordKind string

// Record kinds.
const (
	KindStart      RecordKind = "start"       // the child started; Pid is set
	KindStdout     RecordKind = "stdout"      // one line the child wrote to stdout, without the newline
	KindStderr     RecordKind = "stderr"      // one line the child wrote to stderr
	KindStdin      RecordKind = "stdin"       // one line written to the child's stdin
	KindStdinError RecordKind = "stdin_error" // writing a stdin line failed; Data is the error text
	KindExit       RecordKind = "exit"        // the child exited; Code or Signal is set
)

// Record is one spool line.
//
// T is the append time in Unix milliseconds. Output lines that are valid UTF-8
// carry their bytes in Data; any other line carries them in B (base64 on the
// wire) and leaves Data empty. A consumer rebuilds a line by concatenating
// the Data or B bytes of a record and its More continuations.
//
// An absent code means 0 (clean exit); -1 means the exit status is unknown.
// Signal holds the protocol name (TERM, KILL, INT, HUP) of the signal that
// ended the child, or "SIG<n>" for any other.
type Record struct {
	Kind   RecordKind `json:"k"`
	T      int64      `json:"t"`
	Data   string     `json:"d,omitempty"`
	B      []byte     `json:"b,omitempty"`
	More   bool       `json:"more,omitempty"` // the line continues in the next record of the same kind
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

// Error codes carried by error frames.
const (
	CodeVersion        = "version"          // the hello's protocol version is not supported
	CodeBadOffset      = "bad_offset"       // the hello's offset is outside the spool or not on a record boundary
	CodeCorrupt        = "corrupt"          // the spool holds an undecodable record at Offset
	CodeRecordTooLarge = "record_too_large" // the record at Offset does not fit in a frame
	CodeTooLarge       = "too_large"        // a stdin line is longer than ChunkSize
	CodeExited         = "exited"           // the child has already exited
	CodeStdinBusy      = "stdin_busy"       // the stdin queue is full
	CodeBadRequest     = "bad_request"      // the frame is malformed or unexpected
)

// Message is one frame. Offset is the spool byte offset: in hello, where to
// resume; in welcome, the spool's size when the client was accepted (a client
// that has consumed up to that offset has caught up with everything the shim
// had recorded by then); in record, the offset just after that record; in
// ack, how far the client has durably consumed; in a corrupt or
// record_too_large error, where the offending record starts.
type Message struct {
	Type    MessageType `json:"type"`
	Version int         `json:"v,omitempty"`
	Offset  int64       `json:"off,omitempty"`
	Data    string      `json:"d,omitempty"`
	Signal  string      `json:"signal,omitempty"`
	Pid     int         `json:"pid,omitempty"`
	Record  *Record     `json:"rec,omitempty"`
	Code    string      `json:"code,omitempty"`
	Error   string      `json:"error,omitempty"`
}
