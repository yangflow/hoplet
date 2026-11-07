package proto

import "time"

// CompanionID is a companion's Hoplet ID.
type CompanionID string

// Companion is a long-lived Personal assistant: a directory with a persona
// and the main session that talks as it. Its name, harness and archive
// time are its main session's.
type Companion struct {
	ID      CompanionID `json:"id"`
	Name    string      `json:"name,omitempty"`
	Host    string      `json:"host"`
	Harness HarnessRef  `json:"harness"`
	Main    SessionID   `json:"main"`
	// Dir is the companion's directory, holding persona.md.
	Dir        string     `json:"dir"`
	CreatedAt  time.Time  `json:"created_at"`
	ArchivedAt *time.Time `json:"archived_at,omitempty"`
}

// CompanionRef names a companion by its ID or a unique prefix of it.
type CompanionRef struct {
	ID CompanionID `json:"id"`
}

// CompanionsListParams lists companions; All includes those whose main
// session is archived.
type CompanionsListParams struct {
	All bool `json:"all,omitempty"`
}

// CompanionsList is the companions on a host.
type CompanionsList struct {
	Companions []Companion `json:"companions"`
}

// CompanionCreateParams creates a companion and its main session. The
// fields are those of CreateParams; Title becomes the companion's name.
type CompanionCreateParams struct {
	Harness       string         `json:"harness"`
	Permission    PermissionMode `json:"permission,omitempty"`
	Plan          bool           `json:"plan,omitempty"`
	Effort        Effort         `json:"effort,omitempty"`
	Fast          bool           `json:"fast,omitempty"`
	Ultracode     bool           `json:"ultracode,omitempty"`
	Title         string         `json:"title,omitempty"`
	Model         string         `json:"model,omitempty"`
	ModelProvider string         `json:"model_provider,omitempty"`
}

// CompanionCreated is a new companion and its main session.
type CompanionCreated struct {
	Companion Companion `json:"companion"`
	Session   Session   `json:"session"`
}

// Persona is a companion's persona text.
type Persona struct {
	ID   CompanionID `json:"id"`
	Text string      `json:"text"`
}

// PersonaApplies says when a changed persona takes effect.
type PersonaApplies string

// When a persona takes effect.
const (
	// AppliesNextLaunch: the main session's next start reads it.
	AppliesNextLaunch PersonaApplies = "next_launch"
	// AppliesNewMainSession: the harness keeps the old persona in its
	// native session; only a new main session reads it.
	AppliesNewMainSession PersonaApplies = "new_main_session"
)

// SetPersonaResult says when the new persona takes effect.
type SetPersonaResult struct {
	Applies PersonaApplies `json:"applies"`
}
