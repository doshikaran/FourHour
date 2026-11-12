package service

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"math/big"
	"strings"
	"time"

	"fourhour/backend/internal/ids"
	"fourhour/backend/internal/model"
	"fourhour/backend/internal/rules"
)

type bigRat = big.Rat

const eventCols = `id, fips, scenario_id, lo_started_at, lo_ended_at, hi_started_at, hi_ended_at, peak_share`

func scanEvent(r scanner) (model.OutageEvent, error) {
	var e model.OutageEvent
	var sc, le, hs, he sql.NullString
	err := r.Scan(&e.ID, &e.FIPS, &sc, &e.LoStartedAt, &le, &hs, &he, &e.PeakShare)
	e.ScenarioID, e.LoEndedAt, e.HiStartedAt, e.HiEndedAt = strPtr(sc), strPtr(le), strPtr(hs), strPtr(he)
	return e, err
}

// EventRow builds the outage_events row for a detector event.
func EventRow(ev rules.Event, fips string, scenarioID *string) model.OutageEvent {
	row := model.OutageEvent{ID: ev.ID, FIPS: fips, ScenarioID: scenarioID, LoStartedAt: ids.FormatTime(ev.LoStart()), PeakShare: ev.PeakShare}
	if le := ev.LoEnd(); le != nil {
		v := ids.FormatTime(*le)
		row.LoEndedAt = &v
	}
	if hi, ok := ev.LongestHi(); ok {
		hs := ids.FormatTime(hi.Start)
		row.HiStartedAt = &hs
		if hi.End != nil {
			he := ids.FormatTime(*hi.End)
			row.HiEndedAt = &he
		}
	}
	return row
}

// UpsertOutageEvent writes an outage_events row.
func (s *Service) UpsertOutageEvent(ctx context.Context, e model.OutageEvent) error {
	_, err := s.db.ExecContext(ctx, `INSERT INTO outage_events (`+eventCols+`) VALUES (?,?,?,?,?,?,?,?)
		ON CONFLICT (id) DO UPDATE SET lo_started_at = excluded.lo_started_at, lo_ended_at = excluded.lo_ended_at,
		hi_started_at = excluded.hi_started_at, hi_ended_at = excluded.hi_ended_at, peak_share = excluded.peak_share`,
		e.ID, e.FIPS, nullStr(e.ScenarioID), e.LoStartedAt, nullStr(e.LoEndedAt), nullStr(e.HiStartedAt), nullStr(e.HiEndedAt), e.PeakShare)
	if err != nil {
		return fmt.Errorf("upsert outage event %s: %w", e.ID, err)
	}
	return nil
}

// DeleteOutageEvent removes a provisional event abandoned by an hours-clause
// merge. Nothing references it: claims exist only after a run qualifies.
func (s *Service) DeleteOutageEvent(ctx context.Context, id string) error {
	if _, err := s.db.ExecContext(ctx, `DELETE FROM outage_events WHERE id = ?`, id); err != nil {
		return fmt.Errorf("delete outage event %s: %w", id, err)
	}
	return nil
}

// GetOutageEvent returns one row.
func (s *Service) GetOutageEvent(ctx context.Context, id string) (model.OutageEvent, error) {
	e, err := scanEvent(s.db.QueryRowContext(ctx, `SELECT `+eventCols+` FROM outage_events WHERE id = ?`, id))
	if err != nil {
		return model.OutageEvent{}, fmt.Errorf("load outage event %s: %w", id, err)
	}
	return e, nil
}

// RulePolicy is an active policy with what the rule engine needs.
type RulePolicy struct {
	Policy    model.Policy
	DeviceIDs []string
}

// ActivePolicies returns the active policies in a county.
func (s *Service) ActivePolicies(ctx context.Context, fips string) ([]RulePolicy, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT `+policyCols+` FROM policies WHERE fips = ? AND status = ? ORDER BY created_at, id`, fips, model.PolicyActive)
	if err != nil {
		return nil, fmt.Errorf("active policies: %w", err)
	}
	var out []RulePolicy
	for rows.Next() {
		p, err := scanPolicy(rows)
		if err != nil {
			rows.Close()
			return nil, fmt.Errorf("scan policy: %w", err)
		}
		out = append(out, RulePolicy{Policy: p})
	}
	if err := errors.Join(rows.Err(), rows.Close()); err != nil {
		return nil, fmt.Errorf("active policies: %w", err)
	}
	for i := range out {
		devs, err := s.deviceIDs(ctx, out[i].Policy.ID)
		if err != nil {
			return nil, err
		}
		out[i].DeviceIDs = devs
	}
	return out, nil
}

func (s *Service) deviceIDs(ctx context.Context, policyID string) ([]string, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT id FROM witness_devices WHERE policy_id = ? ORDER BY registered_at, id`, policyID)
	if err != nil {
		return nil, fmt.Errorf("devices: %w", err)
	}
	var out []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return nil, fmt.Errorf("scan device: %w", err)
		}
		out = append(out, id)
	}
	if err := errors.Join(rows.Err(), rows.Close()); err != nil {
		return nil, fmt.Errorf("devices: %w", err)
	}
	return out, nil
}

// RuleEntries returns a policy's valid witness entries that count for the
// current replay: those received since the replay started, stamped with
// sim_time (SPEC.md section 7, clocks in demo mode). The time each entry
// counts at is its sim_time.
func (s *Service) RuleEntries(ctx context.Context, policyID string, since time.Time) ([]rules.WitnessEntry, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT e.id, e.seq, e.kind, e.sim_time, e.device_id FROM witness_entries e
		JOIN witness_devices d ON d.id = e.device_id
		WHERE d.policy_id = ? AND e.valid = 1 AND e.sim_time IS NOT NULL AND e.received_at >= ?
		ORDER BY e.device_id, e.seq`, policyID, ids.FormatTime(since))
	if err != nil {
		return nil, fmt.Errorf("rule entries: %w", err)
	}
	var out []rules.WitnessEntry
	var firstDev string
	for rows.Next() {
		var e rules.WitnessEntry
		var sim, dev string
		if err := rows.Scan(&e.ID, &e.Seq, &e.Kind, &sim, &dev); err != nil {
			rows.Close()
			return nil, fmt.Errorf("scan entry: %w", err)
		}
		// One device per policy is what the product registers; if several
		// exist, only the first device's chain is used, so seq order holds.
		if firstDev == "" {
			firstDev = dev
		}
		if dev != firstDev {
			continue
		}
		t, err := ids.ParseTime(sim)
		if err != nil {
			rows.Close()
			return nil, fmt.Errorf("entry %s sim_time: %w", e.ID, err)
		}
		e.At = t
		out = append(out, e)
	}
	if err := errors.Join(rows.Err(), rows.Close()); err != nil {
		return nil, fmt.Errorf("rule entries: %w", err)
	}
	return out, nil
}

// ClaimItems returns GET /api/claims items for the given claim IDs, in the
// given order. An empty list returns every claim, newest first.
func (s *Service) ClaimItems(ctx context.Context, claimIDs []string) ([]model.ClaimItem, error) {
	q := `SELECT ` + prefixCols("c.", claimCols) + `, m.name, p.fips FROM claims c
		JOIN policies p ON p.id = c.policy_id JOIN members m ON m.id = p.member_id`
	var args []any
	if claimIDs != nil {
		if len(claimIDs) == 0 {
			return []model.ClaimItem{}, nil
		}
		q += ` WHERE c.id IN (?` + strings.Repeat(",?", len(claimIDs)-1) + `)`
		for _, id := range claimIDs {
			args = append(args, id)
		}
	}
	q += ` ORDER BY c.decided_at DESC, c.id`
	rows, err := s.db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, fmt.Errorf("claims: %w", err)
	}
	out := []model.ClaimItem{}
	for rows.Next() {
		var it model.ClaimItem
		var ev, sim, wen sql.NullString
		c := &it.Claim
		if err := rows.Scan(&c.ID, &c.PolicyID, &ev, &c.Tier, &c.Rule, &c.Decision, &c.AmountCents, &c.Reason, &c.DecidedAt, &sim, &wen, &it.MemberName, &it.FIPS); err != nil {
			rows.Close()
			return nil, fmt.Errorf("scan claim: %w", err)
		}
		c.OutageEventID, c.SimTime, c.WitnessEntryID = strPtr(ev), strPtr(sim), strPtr(wen)
		out = append(out, it)
	}
	if err := errors.Join(rows.Err(), rows.Close()); err != nil {
		return nil, fmt.Errorf("claims: %w", err)
	}
	for i := range out {
		p, err := s.payoutForClaim(ctx, out[i].ID)
		if err != nil {
			return nil, err
		}
		out[i].Payout = p
	}
	if claimIDs != nil {
		pos := map[string]int{}
		for i, id := range claimIDs {
			pos[id] = i
		}
		ordered := make([]model.ClaimItem, len(out))
		n := 0
		for _, it := range out {
			if i, ok := pos[it.ID]; ok && i < len(ordered) {
				ordered[i] = it
				n++
			}
		}
		if n == len(out) && n == len(claimIDs) {
			return ordered, nil
		}
	}
	return out, nil
}
