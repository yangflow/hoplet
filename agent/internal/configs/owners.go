package configs

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	gosync "sync"
)

const ownersFile = "sessions-config.json"

// Owners records which configuration started each session the agent started (ADR 0028), in
// <hoplet_home>/sessions-config.json. A session it does not list belongs to its vendor's built-in
// configuration, unless it lives in the session source of another configuration.
type Owners struct {
	home string
	mu   gosync.Mutex
	m    map[string]string
}

// LoadOwners reads the file; a missing file is an empty record.
func LoadOwners(home string) (*Owners, error) {
	o := &Owners{home: home, m: map[string]string{}}
	raw, err := os.ReadFile(filepath.Join(home, ownersFile))
	if err != nil {
		if os.IsNotExist(err) {
			return o, nil
		}
		return nil, fmt.Errorf("read session configurations: %w", err)
	}
	var disk struct {
		Sessions map[string]string `json:"sessions"`
	}
	if err := json.Unmarshal(raw, &disk); err != nil {
		return nil, fmt.Errorf("parse session configurations: %w", err)
	}
	for k, v := range disk.Sessions {
		o.m[k] = v
	}
	return o, nil
}

// Get is the configuration recorded for the session.
func (o *Owners) Get(sessionID string) (string, bool) {
	o.mu.Lock()
	defer o.mu.Unlock()
	id, ok := o.m[sessionID]
	return id, ok
}

// Set records the configuration of a session and saves the file (atomically, mode 0600).
func (o *Owners) Set(sessionID, configuration string) error {
	o.mu.Lock()
	defer o.mu.Unlock()
	if o.m[sessionID] == configuration {
		return nil
	}
	o.m[sessionID] = configuration
	return o.save()
}

// Forget drops a session from the record.
func (o *Owners) Forget(sessionID string) error {
	o.mu.Lock()
	defer o.mu.Unlock()
	if _, ok := o.m[sessionID]; !ok {
		return nil
	}
	delete(o.m, sessionID)
	return o.save()
}

func (o *Owners) save() error {
	raw, err := json.MarshalIndent(struct {
		Sessions map[string]string `json:"sessions"`
	}{o.m}, "", "  ")
	if err != nil {
		return err
	}
	return writeAtomic(o.home, ownersFile, append(raw, '\n'))
}

// NewOwners is an empty record that saves under home.
func NewOwners(home string) *Owners { return &Owners{home: home, m: map[string]string{}} }
