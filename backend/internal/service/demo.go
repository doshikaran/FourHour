package service

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"fourhour/backend/internal/ids"
	"fourhour/backend/internal/model"
	"fourhour/backend/internal/paypal"
)

// The startup demo policy (CONTRACTS.md section 3, "Demo policy").
const (
	DemoFIPS       = "48201"
	DemoCoverCents = int64(5000)
	demoMemberName = "Demo member (simulated)"
)

// DemoInput is what the startup demo policy needs from the environment.
type DemoInput struct {
	MemberEmail string // SANDBOX_MEMBER_EMAIL
	PayoutEmail string // SANDBOX_PAYOUT_EMAIL; paid on claims
	VaultToken  string // DEMO_VAULT_TOKEN
}

// DemoResult says what EnsureDemoPolicy found or did.
type DemoResult struct {
	Member  model.Member
	Policy  model.Policy
	Created bool
	// Charge is the first premium charge, nil if none could be recorded.
	Charge *model.PremiumCharge
}

// EnsureDemoPolicy creates, if none exists, the demo member and an active
// Harris County policy with cover_cents 5000 priced by the normal quote and
// paid from DEMO_VAULT_TOKEN, then makes sure its first premium is charged
// through ChargePremium (PayPal-Request-Id prem-{policy_id}-{period}).
//
// It is idempotent on the same database: the demo_policy table holds one
// row at most, so a restart finds the same policy, and the first premium is
// charged only while the policy has no charge row, or retried with the same
// key while that row's outcome is unknown (status created, no order ID).
// A charge that completed or was refused is never charged again here.
//
// An error after the policy exists is returned with the result filled in,
// so the caller can log it (with any PayPal debug_id, see ErrDebugID) and
// carry on.
func (s *Service) EnsureDemoPolicy(ctx context.Context, in DemoInput) (DemoResult, error) {
	if in.VaultToken == "" || in.MemberEmail == "" {
		return DemoResult{}, errors.New("demo policy: DEMO_VAULT_TOKEN and SANDBOX_MEMBER_EMAIL are both required")
	}
	if in.PayoutEmail == "" {
		in.PayoutEmail = in.MemberEmail
	}
	if !validEmail(in.MemberEmail) || !validEmail(in.PayoutEmail) {
		return DemoResult{}, errors.New("demo policy: SANDBOX_MEMBER_EMAIL and SANDBOX_PAYOUT_EMAIL must be valid addresses")
	}
	res, found, err := s.loadDemo(ctx, s.db)
	if err != nil {
		return DemoResult{}, err
	}
	if !found {
		q, err := s.Quote(ctx, DemoFIPS, DemoCoverCents)
		if err != nil {
			return DemoResult{}, fmt.Errorf("demo policy: quote %s %d: %w", DemoFIPS, DemoCoverCents, err)
		}
		err = s.inTx(ctx, func(tx *sql.Tx) error {
			r, found, err := s.loadDemo(ctx, tx)
			if err != nil || found {
				res = r
				return err
			}
			now := s.nowStr()
			m := model.Member{ID: ids.New(ids.Member), Name: demoMemberName, Email: in.MemberEmail, PayPalEmail: in.PayoutEmail, CreatedAt: now}
			if _, err := tx.ExecContext(ctx, `INSERT INTO members (`+memberCols+`) VALUES (?,?,?,?,?)`, m.ID, m.Name, m.Email, m.PayPalEmail, m.CreatedAt); err != nil {
				return fmt.Errorf("insert demo member: %w", err)
			}
			p := model.Policy{ID: ids.New(ids.Policy), MemberID: m.ID, FIPS: DemoFIPS, CoverCents: DemoCoverCents, PremiumCents: q.PremiumCents,
				Status: model.PolicyActive, ModelVersion: q.ModelVersion, CreatedAt: now}
			if _, err := tx.ExecContext(ctx, `INSERT INTO policies (`+policyCols+`, vault_payment_token) VALUES (?,?,?,?,?,?,?,?,NULL,?)`,
				p.ID, p.MemberID, p.FIPS, p.CoverCents, p.PremiumCents, p.Status, p.ModelVersion, p.CreatedAt, in.VaultToken); err != nil {
				return fmt.Errorf("insert demo policy: %w", err)
			}
			if _, err := tx.ExecContext(ctx, `INSERT INTO demo_policy (slot, policy_id, created_at) VALUES (1, ?, ?)`, p.ID, now); err != nil {
				return fmt.Errorf("insert demo marker: %w", err)
			}
			res = DemoResult{Member: m, Policy: p, Created: true}
			return nil
		})
		if err != nil {
			return DemoResult{}, fmt.Errorf("demo policy: %w", err)
		}
	}

	// First premium. Look at the earliest charge row, if any.
	first, err := scanCharge(s.db.QueryRowContext(ctx, `SELECT `+chargeCols+` FROM premium_charges WHERE policy_id = ? ORDER BY created_at, period LIMIT 1`, res.Policy.ID))
	switch {
	case err == nil && (first.Status != model.ChargeCreated || first.PayPalOrderID != nil):
		res.Charge = &first
		return res, nil
	case err == nil:
		// Outcome unknown: resend with the same key, PayPal returns the original order.
		ch, cerr := s.ChargePremium(ctx, res.Policy.ID, first.Period)
		return s.withCharge(ctx, res, ch, cerr)
	case errors.Is(err, sql.ErrNoRows):
		ch, cerr := s.ChargePremium(ctx, res.Policy.ID, s.CurrentPeriod())
		return s.withCharge(ctx, res, ch, cerr)
	default:
		return res, fmt.Errorf("demo policy: load first charge: %w", err)
	}
}

// withCharge records the charge row as stored (ChargePremium's returned
// row can predate its failure update) and wraps err.
func (s *Service) withCharge(ctx context.Context, res DemoResult, ch model.PremiumCharge, err error) (DemoResult, error) {
	if ch.ID != "" {
		stored, rerr := scanCharge(s.db.QueryRowContext(ctx, `SELECT `+chargeCols+` FROM premium_charges WHERE id = ?`, ch.ID))
		if rerr != nil {
			err = errors.Join(err, fmt.Errorf("reload charge: %w", rerr))
		} else {
			res.Charge = &stored
		}
	}
	if err != nil {
		return res, fmt.Errorf("demo policy: first premium: %w", err)
	}
	return res, nil
}

type queryRower interface {
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
}

func (s *Service) loadDemo(ctx context.Context, q queryRower) (DemoResult, bool, error) {
	var pid string
	err := q.QueryRowContext(ctx, `SELECT policy_id FROM demo_policy WHERE slot = 1`).Scan(&pid)
	if errors.Is(err, sql.ErrNoRows) {
		return DemoResult{}, false, nil
	}
	if err != nil {
		return DemoResult{}, false, fmt.Errorf("find demo policy: %w", err)
	}
	p, err := scanPolicy(q.QueryRowContext(ctx, `SELECT `+policyCols+` FROM policies WHERE id = ?`, pid))
	if err != nil {
		return DemoResult{}, false, fmt.Errorf("load demo policy: %w", err)
	}
	m, err := scanMember(q.QueryRowContext(ctx, `SELECT `+memberCols+` FROM members WHERE id = ?`, p.MemberID))
	if err != nil {
		return DemoResult{}, false, fmt.Errorf("load demo member: %w", err)
	}
	return DemoResult{Member: m, Policy: p}, true, nil
}

// DemoInfo is the GET /api/demo response. Every field but PolicyID is
// omitted when there is no demo policy, giving { "policy_id": null }.
type DemoInfo struct {
	PolicyID     *string `json:"policy_id"`
	MemberName   *string `json:"member_name,omitempty"`
	FIPS         *string `json:"fips,omitempty"`
	CoverCents   *int64  `json:"cover_cents,omitempty"`
	PremiumCents *int64  `json:"premium_cents,omitempty"`
}

// DemoPolicy returns the startup demo policy, if one exists.
func (s *Service) DemoPolicy(ctx context.Context) (DemoInfo, error) {
	r, found, err := s.loadDemo(ctx, s.db)
	if err != nil {
		return DemoInfo{}, err
	}
	if !found {
		return DemoInfo{}, nil
	}
	return DemoInfo{PolicyID: &r.Policy.ID, MemberName: &r.Member.Name, FIPS: &r.Policy.FIPS, CoverCents: &r.Policy.CoverCents, PremiumCents: &r.Policy.PremiumCents}, nil
}

// ErrDebugID returns the PayPal debug_id carried by err, or "".
func ErrDebugID(err error) string {
	var se *Error
	if errors.As(err, &se) && se.DebugID != nil {
		return *se.DebugID
	}
	if ae, ok := paypal.AsAPIError(err); ok {
		return ae.DebugID
	}
	return ""
}
