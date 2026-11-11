package client

import (
	"context"

	"github.com/yangflow/hoplet/proto"
)

// Sessions lists every session, most recently updated first.
func (c *Client) Sessions(ctx context.Context) ([]proto.Session, error) {
	var out []proto.Session
	err := c.Call(ctx, proto.MethodSessionsList, struct{}{}, &out)
	return out, err
}

// Create creates a session.
func (c *Client) Create(ctx context.Context, p proto.CreateParams) (proto.Session, error) {
	var out proto.Session
	err := c.Call(ctx, proto.MethodSessionsCreate, p, &out)
	return out, err
}

// Send sends a message, or queues it behind the running turn.
func (c *Client) Send(ctx context.Context, p proto.SendParams) (proto.SendReply, error) {
	var out proto.SendReply
	err := c.Call(ctx, proto.MethodSessionsSend, p, &out)
	return out, err
}

// Respond answers an approval; the first answer wins and later ones get a
// conflict error.
func (c *Client) Respond(ctx context.Context, p proto.RespondParams) error {
	return c.Call(ctx, proto.MethodSessionsRespond, p, nil)
}

// Interrupt ends the running turn.
func (c *Client) Interrupt(ctx context.Context, id proto.SessionID) error {
	return c.Call(ctx, proto.MethodSessionsInterrupt, proto.SessionRef{Session: id}, nil)
}

// Steer adds text to the running turn.
func (c *Client) Steer(ctx context.Context, p proto.SteerParams) error {
	return c.Call(ctx, proto.MethodSessionsSteer, p, nil)
}

// Answer answers a question.
func (c *Client) Answer(ctx context.Context, p proto.AnswerParams) error {
	return c.Call(ctx, proto.MethodSessionsAnswer, p, nil)
}

// SetPermission changes a session's permission mode.
func (c *Client) SetPermission(ctx context.Context, p proto.SetPermissionParams) error {
	return c.Call(ctx, proto.MethodSessionsSetPerm, p, nil)
}

// SetPlan turns a session's plan mode on or off.
func (c *Client) SetPlan(ctx context.Context, p proto.SetPlanParams) error {
	return c.Call(ctx, proto.MethodSessionsSetPlan, p, nil)
}

// SetUltracode turns a session's Ultracode on or off.
func (c *Client) SetUltracode(ctx context.Context, p proto.SetUltracodeParams) error {
	return c.Call(ctx, proto.MethodSessionsSetUltra, p, nil)
}

// SetFast turns a session's fast mode on or off.
func (c *Client) SetFast(ctx context.Context, p proto.SetFastParams) error {
	return c.Call(ctx, proto.MethodSessionsSetFast, p, nil)
}

// SetEffort changes how hard a session's harness thinks.
func (c *Client) SetEffort(ctx context.Context, p proto.SetEffortParams) error {
	return c.Call(ctx, proto.MethodSessionsSetEffort, p, nil)
}

// Update changes a session's title, pin or unread mark and returns the
// session.
func (c *Client) Update(ctx context.Context, p proto.UpdateParams) (proto.Session, error) {
	var out proto.Session
	err := c.Call(ctx, proto.MethodSessionsUpdate, p, &out)
	return out, err
}

// SetModel changes a session's model from its next turn.
func (c *Client) SetModel(ctx context.Context, p proto.SetModelParams) error {
	return c.Call(ctx, proto.MethodSessionsSetModel, p, nil)
}

// Harnesses describes the installed harnesses and their models.
func (c *Client) Harnesses(ctx context.Context) ([]proto.HarnessInfo, error) {
	var out []proto.HarnessInfo
	err := c.Call(ctx, proto.MethodHarnessesList, struct{}{}, &out)
	return out, err
}

// Output reads a tool call's output.
func (c *Client) Output(ctx context.Context, p proto.OutputParams) (proto.ToolOutput, error) {
	var out proto.ToolOutput
	err := c.Call(ctx, proto.MethodSessionsOutput, p, &out)
	return out, err
}

// Stop stops a session's harness.
func (c *Client) Stop(ctx context.Context, id proto.SessionID) error {
	return c.Call(ctx, proto.MethodSessionsStop, proto.SessionRef{Session: id}, nil)
}

// Archive archives a session.
func (c *Client) Archive(ctx context.Context, id proto.SessionID) error {
	return c.Call(ctx, proto.MethodSessionsArchive, proto.SessionRef{Session: id}, nil)
}

// EventsBefore returns a page of a session's events older than
// p.BeforeSeq, oldest first; More says older events remain.
func (c *Client) EventsBefore(ctx context.Context, p proto.EventsParams) (proto.EventsPage, error) {
	var out proto.EventsPage
	err := c.Call(ctx, proto.MethodSessionsEvents, p, &out)
	return out, err
}

// Queue returns a session's queued messages, next first.
func (c *Client) Queue(ctx context.Context, id proto.SessionID) ([]proto.QueuedMessage, error) {
	var out proto.Queue
	err := c.Call(ctx, proto.MethodQueueList, proto.SessionRef{Session: id}, &out)
	return out.Messages, err
}

// EditQueued changes a queued message's text.
func (c *Client) EditQueued(ctx context.Context, p proto.QueueEditParams) error {
	return c.Call(ctx, proto.MethodQueueEdit, p, nil)
}

// DeleteQueued removes a queued message.
func (c *Client) DeleteQueued(ctx context.Context, p proto.QueueKeyParams) error {
	return c.Call(ctx, proto.MethodQueueDelete, p, nil)
}

// MoveQueued moves a queued message; position 0 is next.
func (c *Client) MoveQueued(ctx context.Context, p proto.QueueMoveParams) error {
	return c.Call(ctx, proto.MethodQueueMove, p, nil)
}

// SubscribeCatalog subscribes to the session list; items are
// proto.CatalogItem.
func (c *Client) SubscribeCatalog(ctx context.Context, afterVersion uint64) (*Subscription, error) {
	return c.Subscribe(ctx, proto.MethodSubscribeCatalog, proto.CatalogParams{AfterVersion: afterVersion})
}

// SubscribeSession subscribes to a session's events; items are
// proto.SessionItem.
func (c *Client) SubscribeSession(ctx context.Context, p proto.SessionParams) (*Subscription, error) {
	return c.Subscribe(ctx, proto.MethodSubscribeSession, p)
}

// Session returns a session's record, pending approvals and queue.
func (c *Client) Session(ctx context.Context, id proto.SessionID) (proto.SessionState, error) {
	var out proto.SessionState
	err := c.Call(ctx, proto.MethodSessionsGet, proto.SessionRef{Session: id}, &out)
	return out, err
}

// Diff returns what a session changed in the git repositories under its
// working directory since the session began.
func (c *Client) Diff(ctx context.Context, p proto.DiffParams) (proto.SessionDiff, error) {
	var out proto.SessionDiff
	err := c.Call(ctx, proto.MethodSessionsDiff, p, &out)
	return out, err
}

// Commits lists the commits a session's repositories offer for a commit
// range.
func (c *Client) Commits(ctx context.Context, p proto.CommitsParams) (proto.SessionCommits, error) {
	var out proto.SessionCommits
	err := c.Call(ctx, proto.MethodSessionsCommits, p, &out)
	return out, err
}

// Files lists a directory of a session's working directory, or with All
// every file under it.
func (c *Client) Files(ctx context.Context, p proto.FilesListParams) (proto.FileList, error) {
	var out proto.FileList
	err := c.Call(ctx, proto.MethodFilesList, p, &out)
	return out, err
}

// ReadFile reads a file of a session's working directory, now or as the
// session's baseline recorded it.
func (c *Client) ReadFile(ctx context.Context, p proto.FilesReadParams) (proto.FileContent, error) {
	var out proto.FileContent
	err := c.Call(ctx, proto.MethodFilesRead, p, &out)
	return out, err
}

// RefreshEnv makes the daemon read the login shell's environment again for
// harnesses started from now on.
func (c *Client) RefreshEnv(ctx context.Context) (proto.EnvInfo, error) {
	var out proto.EnvInfo
	err := c.Call(ctx, proto.MethodEnvRefresh, struct{}{}, &out)
	return out, err
}

// Doctor returns the daemon's health checks.
func (c *Client) Doctor(ctx context.Context) (proto.DoctorReport, error) {
	var out proto.DoctorReport
	err := c.Call(ctx, proto.MethodSystemDoctor, struct{}{}, &out)
	return out, err
}

// StartPairing opens a pairing and returns its one-time code; local
// connections only.
func (c *Client) StartPairing(ctx context.Context) (proto.PairingCode, error) {
	var out proto.PairingCode
	err := c.Call(ctx, proto.MethodPairingStart, struct{}{}, &out)
	return out, err
}

// Devices lists the devices paired with the host, revoked ones included.
func (c *Client) Devices(ctx context.Context) ([]proto.Device, error) {
	var out []proto.Device
	err := c.Call(ctx, proto.MethodDevicesList, struct{}{}, &out)
	return out, err
}

// RevokeDevice revokes a device by its ID or a unique prefix of at least
// four characters and ends its connections.
func (c *Client) RevokeDevice(ctx context.Context, id string) error {
	return c.Call(ctx, proto.MethodDevicesRevoke, proto.DeviceRef{ID: id}, nil)
}

// AddHost records a host this one reaches; local connections only.
func (c *Client) AddHost(ctx context.Context, h proto.Host) error {
	return c.Call(ctx, proto.MethodHostsAdd, h, nil)
}

// Hosts lists the hosts this one reaches; local connections only.
func (c *Client) Hosts(ctx context.Context) ([]proto.Host, error) {
	var out []proto.Host
	err := c.Call(ctx, proto.MethodHostsList, struct{}{}, &out)
	return out, err
}

// RemoveHost forgets a host by name or ID; local connections only.
func (c *Client) RemoveHost(ctx context.Context, host string) error {
	return c.Call(ctx, proto.MethodHostsRemove, proto.HostRef{Host: host}, nil)
}

// Companions lists the companions; all includes those whose main session
// is archived.
func (c *Client) Companions(ctx context.Context, all bool) ([]proto.Companion, error) {
	var out proto.CompanionsList
	err := c.Call(ctx, proto.MethodCompanionsList, proto.CompanionsListParams{All: all}, &out)
	return out.Companions, err
}

// Companion returns a companion by its ID or a unique prefix of at least 4
// characters.
func (c *Client) Companion(ctx context.Context, id string) (proto.Companion, error) {
	var out proto.Companion
	err := c.Call(ctx, proto.MethodCompanionsGet, proto.CompanionRef{ID: proto.CompanionID(id)}, &out)
	return out, err
}

// CreateCompanion creates a companion and its main session.
func (c *Client) CreateCompanion(ctx context.Context, p proto.CompanionCreateParams) (proto.CompanionCreated, error) {
	var out proto.CompanionCreated
	err := c.Call(ctx, proto.MethodCompanionsCreate, p, &out)
	return out, err
}

// Persona returns a companion's persona.
func (c *Client) Persona(ctx context.Context, id string) (proto.Persona, error) {
	var out proto.Persona
	err := c.Call(ctx, proto.MethodCompanionsPersona, proto.CompanionRef{ID: proto.CompanionID(id)}, &out)
	return out, err
}

// SetPersona replaces a companion's persona.
func (c *Client) SetPersona(ctx context.Context, id, text string) (proto.SetPersonaResult, error) {
	var out proto.SetPersonaResult
	err := c.Call(ctx, proto.MethodCompanionsSetPers, proto.Persona{ID: proto.CompanionID(id), Text: text}, &out)
	return out, err
}

// PublishArtifact publishes files on the daemon's machine as a new version
// of a session's artifact; local connections only.
func (c *Client) PublishArtifact(ctx context.Context, p proto.ArtifactsPublishParams) (proto.Artifact, error) {
	var out proto.Artifact
	err := c.Call(ctx, proto.MethodArtifactsPublish, p, &out)
	return out, err
}

// Artifacts lists a session's artifacts at their latest versions, the most
// recently published first.
func (c *Client) Artifacts(ctx context.Context, session proto.SessionID) ([]proto.Artifact, error) {
	var out proto.ArtifactsList
	err := c.Call(ctx, proto.MethodArtifactsList, proto.ArtifactsListParams{Session: session}, &out)
	return out.Artifacts, err
}

// ArtifactVersions lists an artifact at each of its versions, the newest
// first.
func (c *Client) ArtifactVersions(ctx context.Context, id proto.ArtifactID) ([]proto.Artifact, error) {
	var out proto.ArtifactsVersions
	err := c.Call(ctx, proto.MethodArtifactsVersions, proto.ArtifactsVersionsParams{Artifact: id}, &out)
	return out.Versions, err
}

// ReadArtifact reads one chunk of a file of an artifact version.
func (c *Client) ReadArtifact(ctx context.Context, p proto.ArtifactsReadParams) (proto.ArtifactChunk, error) {
	var out proto.ArtifactChunk
	err := c.Call(ctx, proto.MethodArtifactsRead, p, &out)
	return out, err
}

// ProfileList returns the memory entries that match p.
func (c *Client) ProfileList(ctx context.Context, p proto.ProfileListParams) (proto.ProfileList, error) {
	var out proto.ProfileList
	err := c.Call(ctx, proto.MethodProfileList, p, &out)
	return out, err
}

// ProfileGet returns a memory entry and its history.
func (c *Client) ProfileGet(ctx context.Context, id string) (proto.ProfileDetail, error) {
	var out proto.ProfileDetail
	err := c.Call(ctx, proto.MethodProfileGet, proto.ProfileRef{ID: id}, &out)
	return out, err
}

// ProfileUpdate replaces a memory entry's body as the user.
func (c *Client) ProfileUpdate(ctx context.Context, p proto.ProfileUpdateParams) (proto.ProfileWriteResult, error) {
	var out proto.ProfileWriteResult
	err := c.Call(ctx, proto.MethodProfileUpdate, p, &out)
	return out, err
}

// ProfileWithdraw withdraws a memory entry as the user.
func (c *Client) ProfileWithdraw(ctx context.Context, p proto.ProfileWithdrawParams) (proto.ProfileWriteResult, error) {
	var out proto.ProfileWriteResult
	err := c.Call(ctx, proto.MethodProfileWithdraw, p, &out)
	return out, err
}

// ProfileCandidates returns the changes waiting for the user.
func (c *Client) ProfileCandidates(ctx context.Context) (proto.ProfileCandidates, error) {
	var out proto.ProfileCandidates
	err := c.Call(ctx, proto.MethodProfileCandidates, struct{}{}, &out)
	return out, err
}

// ProfileExtraction says how background extraction stands.
func (c *Client) ProfileExtraction(ctx context.Context) (proto.ProfileExtractionStatus, error) {
	var out proto.ProfileExtractionStatus
	err := c.Call(ctx, proto.MethodProfileExtraction, struct{}{}, &out)
	return out, err
}

// SetProfileExtraction turns background extraction on or off.
func (c *Client) SetProfileExtraction(ctx context.Context, on bool) (proto.ProfileExtractionStatus, error) {
	var out proto.ProfileExtractionStatus
	err := c.Call(ctx, proto.MethodProfileExtractionSet, proto.ProfileExtractionSetParams{Enabled: on}, &out)
	return out, err
}

// ProfileAccept carries out a candidate.
func (c *Client) ProfileAccept(ctx context.Context, p proto.ProfileAcceptParams) (proto.ProfileEntry, error) {
	var out proto.ProfileEntry
	err := c.Call(ctx, proto.MethodProfileAccept, p, &out)
	return out, err
}

// ProfileReject withdraws a candidate.
func (c *Client) ProfileReject(ctx context.Context, p proto.ProfileRejectParams) (proto.ProfileEntry, error) {
	var out proto.ProfileEntry
	err := c.Call(ctx, proto.MethodProfileReject, p, &out)
	return out, err
}

// ProfileSettings returns the memory settings.
func (c *Client) ProfileSettings(ctx context.Context) (proto.ProfileSettings, error) {
	var out proto.ProfileSettings
	err := c.Call(ctx, proto.MethodProfileSettings, struct{}{}, &out)
	return out, err
}

// SetProfileSettings replaces the memory settings and returns them as saved.
func (c *Client) SetProfileSettings(ctx context.Context, s proto.ProfileSettings) (proto.ProfileSettings, error) {
	var out proto.ProfileSettings
	err := c.Call(ctx, proto.MethodProfileSettingsSet, s, &out)
	return out, err
}

// ProfileUnlink removes the link between two memory entries.
func (c *Client) ProfileUnlink(ctx context.Context, p proto.ProfileUnlinkParams) error {
	return c.Call(ctx, proto.MethodProfileUnlink, p, nil)
}

// ProfileUsage says which memory entries a session used.
func (c *Client) ProfileUsage(ctx context.Context, id proto.SessionID) (proto.ProfileUsage, error) {
	var out proto.ProfileUsage
	err := c.Call(ctx, proto.MethodProfileUsage, proto.ProfileUsageParams{Session: id}, &out)
	return out, err
}
