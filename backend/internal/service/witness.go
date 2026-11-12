package service

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"time"

	"fourhour/backend/internal/ids"
	"fourhour/backend/internal/model"
	"fourhour/backend/internal/witness"
)

// MaxEntriesPerUpload bounds one POST /api/witness/entries.
const MaxEntriesPerUpload = 500

const entryCols = `id, device_id, seq, device_time, kind, charging, battery_level, online, prev_hash, hash, sig, received_at, sim_time, valid, reject_reason`

func scanEntry(r scanner) (model.WitnessEntry, error) {
	var e model.WitnessEntry
	var bl sql.NullFloat64
	var sim, reason sql.NullString
	err := r.Scan(&e.ID, &e.DeviceID, &e.Seq, &e.DeviceTime, &e.Kind, &e.Charging, &bl, &e.Online, &e.PrevHash, &e.Hash, &e.Sig, &e.ReceivedAt, &sim, &e.Valid, &reason)
	if bl.Valid {
		v := bl.Float64
		e.BatteryLevel = &v
	}
	e.SimTime, e.RejectReason = strPtr(sim), strPtr(reason)
	return e, err
}

func (s *Service) devices(ctx context.Context, policyID string) ([]model.WitnessDevice, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT id, policy_id, public_key_jwk, registered_at, last_seq, last_hash FROM witness_devices WHERE policy_id = ? ORDER BY registered_at, id`, policyID)
	if err != nil {
		return nil, fmt.Errorf("devices: %w", err)
	}
	out := []model.WitnessDevice{}
	for rows.Next() {
		var d model.WitnessDevice
		var jwk string
		if err := rows.Scan(&d.ID, &d.PolicyID, &jwk, &d.RegisteredAt, &d.LastSeq, &d.LastHash); err != nil {
			rows.Close()
			return nil, fmt.Errorf("scan device: %w", err)
		}
		d.PublicKeyJWK = json.RawMessage(jwk)
		out = append(out, d)
	}
	if err := errors.Join(rows.Err(), rows.Close()); err != nil {
		return nil, fmt.Errorf("devices: %w", err)
	}
	return out, nil
}

// RegisteredDevice is the POST /api/witness/devices response.
type RegisteredDevice struct {
	DeviceID    string `json:"device_id"`
	GenesisHash string `json:"genesis_hash"`
}

// RegisterDevice stores a witness public key for a policy.
func (s *Service) RegisterDevice(ctx context.Context, policyID string, jwk json.RawMessage) (RegisteredDevice, error) {
	if _, err := s.GetPolicy(ctx, policyID); err != nil {
		return RegisteredDevice{}, err
	}
	if _, err := witness.ParseJWK(jwk); err != nil {
		return RegisteredDevice{}, &Error{Status: http.StatusBadRequest, Code: "invalid_public_key", Message: "public_key_jwk must be an EC P-256 public key.", Err: err}
	}
	var compact bytes.Buffer
	if err := json.Compact(&compact, jwk); err != nil {
		return RegisteredDevice{}, newErr(http.StatusBadRequest, "invalid_public_key", "public_key_jwk is not JSON.")
	}
	id := ids.New(ids.Device)
	if _, err := s.db.ExecContext(ctx, `INSERT INTO witness_devices (id, policy_id, public_key_jwk, registered_at, last_seq, last_hash) VALUES (?,?,?,?,0,?)`,
		id, policyID, compact.String(), s.nowStr(), witness.GenesisHash); err != nil {
		return RegisteredDevice{}, fmt.Errorf("insert device: %w", err)
	}
	return RegisteredDevice{DeviceID: id, GenesisHash: witness.GenesisHash}, nil
}

// Rejected is one item of the upload response's rejected list. Seq is null
// when the entry was too malformed to read it.
type Rejected struct {
	Seq    *int64 `json:"seq"`
	Reason string `json:"reason"`
}

// UploadResult is the POST /api/witness/entries response.
type UploadResult struct {
	Accepted int        `json:"accepted"`
	Rejected []Rejected `json:"rejected"`
	LastSeq  int64      `json:"last_seq"`
	LastHash string     `json:"last_hash"`
}

// StaleAfter is how far behind arrival a device_time may be outside a replay.
const StaleAfter = 24 * time.Hour

// AddEntries verifies and stores witness entries (CONTRACTS.md section 6).
// Checks, in order: a resend of an accepted seq with the same hash is
// accepted silently; otherwise seq must be last_seq + 1 (bad_sequence),
// prev_hash must equal last_hash (broken_chain), the hash rebuilt from the
// values must match and its signature verify (bad_signature), device_time
// must not go back (time_went_backwards), and, outside a replay, it must not
// be more than 24 hours behind arrival unless this entry or the previous
// one was written offline (stale). Rejected entries are stored with valid
// = 0; malformed ones are not stored. During a replay each entry is
// stamped with the simulated time (SPEC.md section 7).
func (s *Service) AddEntries(ctx context.Context, deviceID string, raws []json.RawMessage) (UploadResult, error) {
	if len(raws) > MaxEntriesPerUpload {
		return UploadResult{}, newErr(http.StatusRequestEntityTooLarge, "too_many_entries", fmt.Sprintf("At most %d entries per upload.", MaxEntriesPerUpload))
	}
	// Read the replay clock before taking a database connection.
	sim, inReplay := s.SimNow()
	now := s.now().UTC()
	nowStr := ids.FormatTime(now)
	var simStr *string
	if inReplay {
		v := ids.FormatTime(sim)
		simStr = &v
	}

	s.witness.Lock()
	defer s.witness.Unlock()

	res := UploadResult{Rejected: []Rejected{}}
	var policyID string
	var stored []model.WitnessEntry
	err := s.inTx(ctx, func(tx *sql.Tx) error {
		var jwk string
		err := tx.QueryRowContext(ctx, `SELECT policy_id, public_key_jwk, last_seq, last_hash FROM witness_devices WHERE id = ?`, deviceID).Scan(&policyID, &jwk, &res.LastSeq, &res.LastHash)
		if errors.Is(err, sql.ErrNoRows) {
			return newErr(http.StatusNotFound, "device_not_found", "No device "+deviceID+".")
		}
		if err != nil {
			return fmt.Errorf("load device: %w", err)
		}
		pub, err := witness.ParseJWK([]byte(jwk))
		if err != nil {
			return fmt.Errorf("stored key for %s: %w", deviceID, err)
		}
		var prevTime time.Time
		prevOnline := true
		if res.LastSeq > 0 {
			var dt string
			if err := tx.QueryRowContext(ctx, `SELECT device_time, online FROM witness_entries WHERE device_id = ? AND seq = ? AND valid = 1`, deviceID, res.LastSeq).Scan(&dt, &prevOnline); err != nil {
				return fmt.Errorf("load previous entry: %w", err)
			}
			if prevTime, err = ids.ParseTime(dt); err != nil {
				return err
			}
		}
		for _, raw := range raws {
			e, err := witness.ParseEntry(raw)
			if err != nil {
				var pe *witness.ParseError
				r := Rejected{Reason: witness.ReasonMalformed}
				if errors.As(err, &pe) {
					r.Seq = pe.Seq
				}
				res.Rejected = append(res.Rejected, r)
				continue
			}
			reason := ""
			if e.Seq <= res.LastSeq {
				var h string
				err := tx.QueryRowContext(ctx, `SELECT hash FROM witness_entries WHERE device_id = ? AND seq = ? AND valid = 1`, deviceID, e.Seq).Scan(&h)
				if err != nil && !errors.Is(err, sql.ErrNoRows) {
					return fmt.Errorf("load accepted entry: %w", err)
				}
				// Silent only if the values hash to the stored hash: a resend
				// that keeps the old hash but changes a value is a replay.
				if err == nil && h == e.Hash && witness.Hash(e) == h {
					res.Accepted++ // a resend of an accepted entry: accepted silently
					continue
				}
				reason = witness.ReasonBadSequence
			}
			dt, _ := ids.ParseTime(e.DeviceTime) // ParseEntry checked it
			switch {
			case reason != "":
			case e.Seq != res.LastSeq+1:
				reason = witness.ReasonBadSequence
			case e.PrevHash != res.LastHash:
				reason = witness.ReasonBrokenChain
			case !witness.CheckSignature(pub, e):
				reason = witness.ReasonBadSignature
			case res.LastSeq > 0 && dt.Before(prevTime):
				reason = witness.ReasonTimeWentBackwards
			case !inReplay && now.Sub(dt) > StaleAfter && e.Online && prevOnline:
				reason = witness.ReasonStale
			}
			row := model.WitnessEntry{ID: ids.New(ids.WitnessEntry), DeviceID: deviceID, Seq: e.Seq, DeviceTime: e.DeviceTime, Kind: e.Kind,
				Charging: e.Charging, BatteryLevel: e.BatteryLevel, Online: e.Online, PrevHash: e.PrevHash, Hash: e.Hash, Sig: e.Sig,
				ReceivedAt: nowStr, SimTime: simStr, Valid: reason == ""}
			if reason != "" {
				r := reason
				row.RejectReason = &r
				seq := e.Seq
				res.Rejected = append(res.Rejected, Rejected{Seq: &seq, Reason: reason})
			}
			var bl any
			if row.BatteryLevel != nil {
				bl = *row.BatteryLevel
			}
			if _, err := tx.ExecContext(ctx, `INSERT INTO witness_entries (`+entryCols+`) VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
				row.ID, row.DeviceID, row.Seq, row.DeviceTime, row.Kind, row.Charging, bl, row.Online, row.PrevHash, row.Hash, row.Sig,
				row.ReceivedAt, nullStr(row.SimTime), row.Valid, nullStr(row.RejectReason)); err != nil {
				return fmt.Errorf("insert entry: %w", err)
			}
			stored = append(stored, row)
			if row.Valid {
				res.Accepted++
				res.LastSeq, res.LastHash = e.Seq, e.Hash
				prevTime, prevOnline = dt, e.Online
			}
		}
		_, err = tx.ExecContext(ctx, `UPDATE witness_devices SET last_seq = ?, last_hash = ? WHERE id = ?`, res.LastSeq, res.LastHash, deviceID)
		if err != nil {
			return fmt.Errorf("update device: %w", err)
		}
		return nil
	})
	if err != nil {
		return UploadResult{}, err
	}
	for _, row := range stored {
		s.publish(model.EvWitnessEntry, model.WitnessEntryEvent{SimTime: row.SimTime, DeviceID: deviceID, PolicyID: policyID, Kind: row.Kind, Valid: row.Valid})
	}
	return res, nil
}

// ListEntries returns a device's most recent entries (valid and rejected),
// in seq order, at most limit.
func (s *Service) ListEntries(ctx context.Context, deviceID string, limit int) ([]model.WitnessEntry, error) {
	if limit <= 0 || limit > 1000 {
		limit = 200
	}
	var n int
	if err := s.db.QueryRowContext(ctx, `SELECT count(*) FROM witness_devices WHERE id = ?`, deviceID).Scan(&n); err != nil {
		return nil, fmt.Errorf("device: %w", err)
	}
	if n == 0 {
		return nil, newErr(http.StatusNotFound, "device_not_found", "No device "+deviceID+".")
	}
	rows, err := s.db.QueryContext(ctx, `SELECT `+entryCols+` FROM witness_entries WHERE rowid IN (SELECT rowid FROM witness_entries WHERE device_id = ? ORDER BY rowid DESC LIMIT ?) ORDER BY seq, rowid`, deviceID, limit)
	if err != nil {
		return nil, fmt.Errorf("entries: %w", err)
	}
	out := []model.WitnessEntry{}
	for rows.Next() {
		e, err := scanEntry(rows)
		if err != nil {
			rows.Close()
			return nil, fmt.Errorf("scan entry: %w", err)
		}
		out = append(out, e)
	}
	if err := errors.Join(rows.Err(), rows.Close()); err != nil {
		return nil, fmt.Errorf("entries: %w", err)
	}
	return out, nil
}
