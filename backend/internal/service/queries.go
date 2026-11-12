package service

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/http"
	"time"

	"fourhour/backend/internal/artifacts"
	"fourhour/backend/internal/ids"
	"fourhour/backend/internal/model"
)

// LedgerView is GET /api/ledger.
type LedgerView struct {
	BalanceCents int64               `json:"balance_cents"`
	Entries      []model.LedgerEntry `json:"entries"`
}

// Ledger returns every ledger row, oldest first, and the pool balance.
func (s *Service) Ledger(ctx context.Context) (LedgerView, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT id, at, kind, amount_cents, policy_id, ref_id FROM ledger_entries ORDER BY at, rowid`)
	if err != nil {
		return LedgerView{}, fmt.Errorf("ledger: %w", err)
	}
	v := LedgerView{Entries: []model.LedgerEntry{}}
	for rows.Next() {
		var e model.LedgerEntry
		if err := rows.Scan(&e.ID, &e.At, &e.Kind, &e.AmountCents, &e.PolicyID, &e.RefID); err != nil {
			rows.Close()
			return LedgerView{}, fmt.Errorf("scan ledger: %w", err)
		}
		v.BalanceCents += e.AmountCents
		v.Entries = append(v.Entries, e)
	}
	if err := errors.Join(rows.Err(), rows.Close()); err != nil {
		return LedgerView{}, fmt.Errorf("ledger: %w", err)
	}
	return v, nil
}

// ListPolicies returns every policy, newest first (operator view).
func (s *Service) ListPolicies(ctx context.Context) ([]model.Policy, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT `+policyCols+` FROM policies ORDER BY created_at DESC, id`)
	if err != nil {
		return nil, fmt.Errorf("policies: %w", err)
	}
	out := []model.Policy{}
	for rows.Next() {
		p, err := scanPolicy(rows)
		if err != nil {
			rows.Close()
			return nil, fmt.Errorf("scan policy: %w", err)
		}
		out = append(out, p)
	}
	if err := errors.Join(rows.Err(), rows.Close()); err != nil {
		return nil, fmt.Errorf("policies: %w", err)
	}
	return out, nil
}

// PolicyDetail is GET /api/policies/{id}.
type PolicyDetail struct {
	Policy         model.Policy          `json:"policy"`
	Member         model.Member          `json:"member"`
	PremiumCharges []model.PremiumCharge `json:"premium_charges"`
	Devices        []model.WitnessDevice `json:"devices"`
	Claims         []model.ClaimItem     `json:"claims"`
}

// GetPolicyDetail returns a policy with its member, charges, devices and claims.
func (s *Service) GetPolicyDetail(ctx context.Context, id string) (PolicyDetail, error) {
	p, err := s.GetPolicy(ctx, id)
	if err != nil {
		return PolicyDetail{}, err
	}
	m, err := s.GetMember(ctx, p.MemberID)
	if err != nil {
		return PolicyDetail{}, err
	}
	d := PolicyDetail{Policy: p, Member: m, PremiumCharges: []model.PremiumCharge{}, Devices: []model.WitnessDevice{}, Claims: []model.ClaimItem{}}
	rows, err := s.db.QueryContext(ctx, `SELECT `+chargeCols+` FROM premium_charges WHERE policy_id = ? ORDER BY period, created_at`, id)
	if err != nil {
		return PolicyDetail{}, fmt.Errorf("charges: %w", err)
	}
	for rows.Next() {
		c, err := scanCharge(rows)
		if err != nil {
			rows.Close()
			return PolicyDetail{}, fmt.Errorf("scan charge: %w", err)
		}
		d.PremiumCharges = append(d.PremiumCharges, c)
	}
	if err := errors.Join(rows.Err(), rows.Close()); err != nil {
		return PolicyDetail{}, fmt.Errorf("charges: %w", err)
	}
	devs, err := s.devices(ctx, id)
	if err != nil {
		return PolicyDetail{}, err
	}
	d.Devices = devs
	var claimIDs []string
	crows, err := s.db.QueryContext(ctx, `SELECT id FROM claims WHERE policy_id = ? ORDER BY decided_at DESC, id`, id)
	if err != nil {
		return PolicyDetail{}, fmt.Errorf("claims: %w", err)
	}
	for crows.Next() {
		var cid string
		if err := crows.Scan(&cid); err != nil {
			crows.Close()
			return PolicyDetail{}, fmt.Errorf("scan claim: %w", err)
		}
		claimIDs = append(claimIDs, cid)
	}
	if err := errors.Join(crows.Err(), crows.Close()); err != nil {
		return PolicyDetail{}, fmt.Errorf("claims: %w", err)
	}
	if claimIDs == nil {
		claimIDs = []string{}
	}
	items, err := s.ClaimItems(ctx, claimIDs)
	if err != nil {
		return PolicyDetail{}, err
	}
	d.Claims = items
	return d, nil
}

// ClaimDetail is GET /api/claims/{id}.
type ClaimDetail struct {
	Claim          model.ClaimItem      `json:"claim"`
	OutageEvent    *model.OutageEvent   `json:"outage_event"`
	IndexSeries    []artifacts.Point    `json:"index_series"`
	WitnessEntries []model.WitnessEntry `json:"witness_entries"`
}

// GetClaimDetail returns one claim explained: its outage event (null for
// R5), the index series over the event window (over the witness interval
// for R5), and the valid witness entries inside that window.
func (s *Service) GetClaimDetail(ctx context.Context, id string) (ClaimDetail, error) {
	items, err := s.ClaimItems(ctx, []string{id})
	if err != nil {
		return ClaimDetail{}, err
	}
	if len(items) == 0 {
		return ClaimDetail{}, newErr(http.StatusNotFound, "claim_not_found", "No claim "+id+".")
	}
	d := ClaimDetail{Claim: items[0], IndexSeries: []artifacts.Point{}, WitnessEntries: []model.WitnessEntry{}}
	c := d.Claim
	var start, end time.Time
	var scenarioID string
	if c.OutageEventID != nil {
		ev, err := s.GetOutageEvent(ctx, *c.OutageEventID)
		if err != nil {
			return ClaimDetail{}, err
		}
		d.OutageEvent = &ev
		if start, err = ids.ParseTime(ev.LoStartedAt); err != nil {
			return ClaimDetail{}, err
		}
		end = start
		if ev.LoEndedAt != nil {
			if end, err = ids.ParseTime(*ev.LoEndedAt); err != nil {
				return ClaimDetail{}, err
			}
		} else if c.SimTime != nil {
			if end, err = ids.ParseTime(*c.SimTime); err != nil {
				return ClaimDetail{}, err
			}
		}
		if ev.ScenarioID != nil {
			scenarioID = *ev.ScenarioID
		}
	} else if c.WitnessEntryID != nil {
		var sim sql.NullString
		var dev string
		var seq int64
		if err := s.db.QueryRowContext(ctx, `SELECT sim_time, device_id, seq FROM witness_entries WHERE id = ?`, *c.WitnessEntryID).Scan(&sim, &dev, &seq); err != nil {
			return ClaimDetail{}, fmt.Errorf("load witness entry: %w", err)
		}
		if !sim.Valid {
			return d, nil
		}
		if start, err = ids.ParseTime(sim.String); err != nil {
			return ClaimDetail{}, err
		}
		end = start
		var restored sql.NullString
		err := s.db.QueryRowContext(ctx, `SELECT sim_time FROM witness_entries WHERE device_id = ? AND seq > ? AND valid = 1
			AND kind IN ('power_restored','attest_restored') AND sim_time IS NOT NULL ORDER BY seq LIMIT 1`, dev, seq).Scan(&restored)
		switch {
		case err == nil && restored.Valid:
			if end, err = ids.ParseTime(restored.String); err != nil {
				return ClaimDetail{}, err
			}
		case err != nil && !errors.Is(err, sql.ErrNoRows):
			return ClaimDetail{}, fmt.Errorf("find restored entry: %w", err)
		default:
			if c.SimTime != nil {
				if end, err = ids.ParseTime(*c.SimTime); err != nil {
					return ClaimDetail{}, err
				}
			}
		}
		// The R5 row has no event, so the scenario is the one for this
		// county whose window contains the interval start.
		all, _ := artifacts.LoadScenarios(s.artDir)
		for _, sc := range all {
			if sc.FIPS == c.FIPS && len(sc.Times) > 0 && !start.Before(sc.Times[0]) && !start.After(sc.Times[len(sc.Times)-1]) {
				scenarioID = sc.ID
				break
			}
		}
	}
	if scenarioID != "" {
		if sc, err := artifacts.FindScenario(s.artDir, scenarioID); err == nil {
			for i, p := range sc.Series {
				if !sc.Times[i].Before(start) && !sc.Times[i].After(end) {
					d.IndexSeries = append(d.IndexSeries, p)
				}
			}
		}
	}
	rows, err := s.db.QueryContext(ctx, `SELECT `+prefixCols("e.", entryCols)+` FROM witness_entries e JOIN witness_devices dv ON dv.id = e.device_id
		WHERE dv.policy_id = ? AND e.valid = 1 AND e.sim_time IS NOT NULL AND e.sim_time >= ? AND e.sim_time <= ? ORDER BY e.device_id, e.seq`,
		c.PolicyID, ids.FormatTime(start), ids.FormatTime(end))
	if err != nil {
		return ClaimDetail{}, fmt.Errorf("claim witness entries: %w", err)
	}
	for rows.Next() {
		e, err := scanEntry(rows)
		if err != nil {
			rows.Close()
			return ClaimDetail{}, fmt.Errorf("scan entry: %w", err)
		}
		d.WitnessEntries = append(d.WitnessEntries, e)
	}
	if err := errors.Join(rows.Err(), rows.Close()); err != nil {
		return ClaimDetail{}, fmt.Errorf("claim witness entries: %w", err)
	}
	return d, nil
}
