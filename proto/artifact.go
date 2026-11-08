package proto

import (
	"fmt"
	"time"
)

// ArtifactID is an artifact's Hoplet ID, the same across its versions.
type ArtifactID string

// ArtifactKind is what an artifact shows, decided by its entry file's
// extension.
type ArtifactKind string

// Artifact kinds.
const (
	ArtifactHTML     ArtifactKind = "html"
	ArtifactMarkdown ArtifactKind = "markdown"
	ArtifactImage    ArtifactKind = "image"
	ArtifactCSV      ArtifactKind = "csv"
	ArtifactText     ArtifactKind = "text"
)

// Valid reports whether k is a known kind.
func (k ArtifactKind) Valid() bool {
	switch k {
	case ArtifactHTML, ArtifactMarkdown, ArtifactImage, ArtifactCSV, ArtifactText:
		return true
	}
	return false
}

// ArtifactSource is who published a version.
type ArtifactSource string

// Artifact sources.
const (
	ArtifactByHarness ArtifactSource = "harness"
	ArtifactByCLI     ArtifactSource = "cli"
)

// Valid reports whether s is a known source.
func (s ArtifactSource) Valid() bool { return s == ArtifactByHarness || s == ArtifactByCLI }

// Artifact is a session's viewable result at one of its versions, the
// latest unless said otherwise. A version never changes once published.
type Artifact struct {
	ID      ArtifactID     `json:"id"`
	Session SessionID      `json:"session"`
	Name    string         `json:"name"`
	Kind    ArtifactKind   `json:"kind"`
	Version int            `json:"version"`
	Entry   string         `json:"entry"`
	By      ArtifactSource `json:"by"`
	// Files counts the version's files, its entry included; Size is their
	// total in bytes.
	Files     int       `json:"files"`
	Size      int64     `json:"size"`
	CreatedAt time.Time `json:"created_at"`
	// UpdatedAt is when this version was published.
	UpdatedAt time.Time `json:"updated_at"`
}

// ArtifactPublished records a new version of an artifact in its session's
// log.
type ArtifactPublished struct {
	Artifact ArtifactID     `json:"artifact"`
	Name     string         `json:"name"`
	Kind     ArtifactKind   `json:"kind"`
	Version  int            `json:"version"`
	By       ArtifactSource `json:"by"`
}

func (a ArtifactPublished) validate() error {
	if a.Artifact == "" || a.Name == "" || a.Version < 1 {
		return fmt.Errorf("%w: artifact publication needs an artifact, a name and a version", ErrInvalidPayload)
	}
	if !a.Kind.Valid() {
		return fmt.Errorf("%w: unknown artifact kind %q", ErrInvalidPayload, a.Kind)
	}
	if !a.By.Valid() {
		return fmt.Errorf("%w: unknown artifact source %q", ErrInvalidPayload, a.By)
	}
	return nil
}

func (ArtifactPublished) eventType() EventType { return TypeArtifactPublished }
func (ArtifactPublished) eventVersion() uint16 { return 1 }

func init() { register[ArtifactPublished]() }

// ArtifactsPublishParams publishes files on the daemon's own machine as a
// new version of the session's artifact named Name. Entry and Files are
// absolute paths; Files go with an HTML entry only, under its directory.
// Only a local connection may call it.
type ArtifactsPublishParams struct {
	Session SessionID `json:"session"`
	Name    string    `json:"name"`
	Entry   string    `json:"entry"`
	Files   []string  `json:"files,omitempty"`
}

// ArtifactsListParams lists a session's artifacts.
type ArtifactsListParams struct {
	Session SessionID `json:"session"`
}

// ArtifactsList is a session's artifacts at their latest versions, the
// most recently published first.
type ArtifactsList struct {
	Artifacts []Artifact `json:"artifacts"`
}

// ArtifactsVersionsParams lists every version of an artifact.
type ArtifactsVersionsParams struct {
	Artifact ArtifactID `json:"artifact"`
}

// ArtifactsVersions is an artifact at each of its versions, the newest
// first.
type ArtifactsVersions struct {
	Versions []Artifact `json:"versions"`
}

// MaxArtifactChunk is the most bytes one artifacts.read returns.
const MaxArtifactChunk = 1 << 20

// ArtifactsReadParams reads a file of an artifact version from Offset.
// Version 0 is the latest; an empty Path is the version's entry.
type ArtifactsReadParams struct {
	Artifact ArtifactID `json:"artifact"`
	Version  int        `json:"version,omitempty"`
	Path     string     `json:"path,omitempty"`
	Offset   int64      `json:"offset,omitempty"`
}

// ArtifactChunk is up to MaxArtifactChunk bytes of a file from the offset
// asked for; Size is the whole file's.
type ArtifactChunk struct {
	Data        []byte `json:"data"`
	Size        int64  `json:"size"`
	ContentType string `json:"content_type"`
}
