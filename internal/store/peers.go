package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"
)

// Device is a remote device paired with this host: it may connect over the
// network as the holder of PublicKey.
type Device struct {
	ID        string // host ID of PublicKey
	PublicKey []byte
	Name      string
	PairedAt  time.Time
	RevokedAt *time.Time // nil while the device may connect
}

// Host is another host this one may reach: a pairing code accepted here.
type Host struct {
	ID        string // host ID of PublicKey
	PublicKey []byte
	Name      string // unique among hosts
	Routes    []string
	AddedAt   time.Time
}

func ms(t time.Time) int64 { return t.UTC().Truncate(time.Millisecond).UnixMilli() }

// PairDevice records a paired device. Pairing a known device again updates
// its key, name and pairing time and lifts any revocation.
func (s *Store) PairDevice(ctx context.Context, d Device) error {
	if d.ID == "" || len(d.PublicKey) == 0 {
		return fmt.Errorf("%w: device needs an id and a public key", ErrInvalid)
	}
	return s.tx(ctx, true, func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `INSERT INTO devices (id, public_key, name, paired_at) VALUES (?, ?, ?, ?)
			ON CONFLICT (id) DO UPDATE SET public_key = excluded.public_key, name = excluded.name,
				paired_at = excluded.paired_at, revoked_at = NULL`,
			d.ID, d.PublicKey, d.Name, ms(d.PairedAt))
		if err != nil {
			return fmt.Errorf("store: pair device: %w", err)
		}
		return nil
	})
}

// RevokeDevice marks a device revoked at t. Revoking it again keeps the
// first time.
func (s *Store) RevokeDevice(ctx context.Context, id string, t time.Time) error {
	return s.tx(ctx, true, func(tx *sql.Tx) error {
		res, err := tx.ExecContext(ctx, `UPDATE devices SET revoked_at = coalesce(revoked_at, ?) WHERE id = ?`, ms(t), id)
		if err != nil {
			return fmt.Errorf("store: revoke device: %w", err)
		}
		if n, err := res.RowsAffected(); err != nil {
			return fmt.Errorf("store: revoke device: %w", err)
		} else if n == 0 {
			return ErrNotFound
		}
		return nil
	})
}

const deviceCols = `id, public_key, name, paired_at, revoked_at`

func scanDevice(sc interface{ Scan(...any) error }) (Device, error) {
	var d Device
	var paired int64
	var revoked sql.NullInt64
	if err := sc.Scan(&d.ID, &d.PublicKey, &d.Name, &paired, &revoked); err != nil {
		return Device{}, err
	}
	d.PairedAt = time.UnixMilli(paired).UTC()
	if revoked.Valid {
		t := time.UnixMilli(revoked.Int64).UTC()
		d.RevokedAt = &t
	}
	return d, nil
}

// Device returns the device with id, revoked or not.
func (s *Store) Device(ctx context.Context, id string) (Device, error) {
	d, err := scanDevice(s.rdb.QueryRowContext(ctx, `SELECT `+deviceCols+` FROM devices WHERE id = ?`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return Device{}, ErrNotFound
	}
	if err != nil {
		return Device{}, fmt.Errorf("store: device: %w", err)
	}
	return d, nil
}

// Devices lists the devices whose ID starts with prefix, revoked ones
// included, oldest pairing first. An empty prefix lists them all.
func (s *Store) Devices(ctx context.Context, prefix string) ([]Device, error) {
	rows, err := s.rdb.QueryContext(ctx, `SELECT `+deviceCols+` FROM devices
		WHERE substr(id, 1, length(?1)) = ?1 ORDER BY paired_at, id`, prefix)
	if err != nil {
		return nil, fmt.Errorf("store: devices: %w", err)
	}
	defer rows.Close()
	var out []Device
	for rows.Next() {
		d, err := scanDevice(rows)
		if err != nil {
			return nil, fmt.Errorf("store: devices: %w", err)
		}
		out = append(out, d)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("store: devices: %w", err)
	}
	return out, nil
}

// AddHost records a host. A host with the same ID is replaced; another
// host with the same name is ErrExists.
func (s *Store) AddHost(ctx context.Context, h Host) error {
	if h.ID == "" || len(h.PublicKey) == 0 || h.Name == "" {
		return fmt.Errorf("%w: host needs an id, a public key and a name", ErrInvalid)
	}
	routes, err := json.Marshal(h.Routes)
	if err != nil {
		return fmt.Errorf("store: add host: %w", err)
	}
	if h.Routes == nil {
		routes = []byte("[]")
	}
	return s.tx(ctx, true, func(tx *sql.Tx) error {
		var other string
		err := tx.QueryRowContext(ctx, `SELECT id FROM hosts WHERE name = ? AND id != ?`, h.Name, h.ID).Scan(&other)
		if err == nil {
			return fmt.Errorf("%w: host name %q is taken", ErrExists, h.Name)
		}
		if !errors.Is(err, sql.ErrNoRows) {
			return fmt.Errorf("store: add host: %w", err)
		}
		_, err = tx.ExecContext(ctx, `INSERT INTO hosts (id, public_key, name, routes, added_at) VALUES (?, ?, ?, ?, ?)
			ON CONFLICT (id) DO UPDATE SET public_key = excluded.public_key, name = excluded.name,
				routes = excluded.routes, added_at = excluded.added_at`,
			h.ID, h.PublicKey, h.Name, string(routes), ms(h.AddedAt))
		if err != nil {
			return fmt.Errorf("store: add host: %w", err)
		}
		return nil
	})
}

// Hosts lists the hosts by name.
func (s *Store) Hosts(ctx context.Context) ([]Host, error) {
	rows, err := s.rdb.QueryContext(ctx, `SELECT id, public_key, name, routes, added_at FROM hosts ORDER BY name`)
	if err != nil {
		return nil, fmt.Errorf("store: hosts: %w", err)
	}
	defer rows.Close()
	var out []Host
	for rows.Next() {
		var h Host
		var routes string
		var added int64
		if err := rows.Scan(&h.ID, &h.PublicKey, &h.Name, &routes, &added); err != nil {
			return nil, fmt.Errorf("store: hosts: %w", err)
		}
		if err := json.Unmarshal([]byte(routes), &h.Routes); err != nil {
			return nil, fmt.Errorf("store: hosts: routes of %s: %w", h.ID, err)
		}
		h.AddedAt = time.UnixMilli(added).UTC()
		out = append(out, h)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("store: hosts: %w", err)
	}
	return out, nil
}

// RemoveHost removes the host named, or with the ID, nameOrID.
func (s *Store) RemoveHost(ctx context.Context, nameOrID string) error {
	return s.tx(ctx, true, func(tx *sql.Tx) error {
		res, err := tx.ExecContext(ctx, `DELETE FROM hosts WHERE name = ?1 OR id = ?1`, nameOrID)
		if err != nil {
			return fmt.Errorf("store: remove host: %w", err)
		}
		if n, err := res.RowsAffected(); err != nil {
			return fmt.Errorf("store: remove host: %w", err)
		} else if n == 0 {
			return ErrNotFound
		}
		return nil
	})
}
