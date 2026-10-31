// Package fake is a scriptable harness for tests: a small binary that
// follows a JSON script and speaks a simple line protocol, plus the adapter
// that drives it. It exercises everything the daemon does with a real
// harness — streaming, tool calls, approvals, questions, steering,
// interrupts, slow output and crashes — without a model.
package fake

// Op is one line Hoplet writes to the fake harness.
type Op struct {
	Op     string `json:"op"` // prompt, steer, interrupt, respond, answer
	Text   string `json:"text,omitempty"`
	ID     string `json:"id,omitempty"`
	Option string `json:"option,omitempty"`
}

// Ev is one line the fake harness writes.
type Ev struct {
	Ev      string   `json:"ev"` // session, turn_started, delta, message, thinking, tool, approval, question, turn_ended, error
	ID      string   `json:"id,omitempty"`
	Text    string   `json:"text,omitempty"`
	Kind    string   `json:"kind,omitempty"`
	Name    string   `json:"name,omitempty"`
	Summary string   `json:"summary,omitempty"`
	Status  string   `json:"status,omitempty"`
	Title   string   `json:"title,omitempty"`
	Reason  string   `json:"reason,omitempty"`
	Options []Option `json:"options,omitempty"`
}

// Option is an approval choice the fake harness offers.
type Option struct {
	ID    string `json:"id"`
	Label string `json:"label"`
	Scope string `json:"scope,omitempty"`
}

// Script drives the fake harness: Session is its native session ID; each
// prompt runs the next turn.
type Script struct {
	Session string `json:"session"`
	Turns   []Turn `json:"turns"`
}

// Turn is the steps of one turn, run between turn_started and turn_ended.
type Turn struct {
	Steps []Step `json:"steps"`
}

// Step is one scripted action; exactly one field is set.
type Step struct {
	Emit          *Ev    `json:"emit,omitempty"`           // write this event
	Raw           string `json:"raw,omitempty"`            // write this line verbatim
	Stderr        string `json:"stderr,omitempty"`         // write this line to stderr
	EchoPrompt    bool   `json:"echo_prompt,omitempty"`    // write a message with the prompt's text
	SleepMS       int    `json:"sleep_ms,omitempty"`       // pause; an interrupt ends the turn early
	AwaitApproval string `json:"await_approval,omitempty"` // block until respond with this ID, then write a message "approved:<option>"
	AwaitAnswer   string `json:"await_answer,omitempty"`   // block until answer with this ID, then write a message "answered:<text>"
	Exit          *int   `json:"exit,omitempty"`           // exit immediately with this code
}
