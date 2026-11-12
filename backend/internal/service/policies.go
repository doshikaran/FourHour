package service

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/mail"
	"net/url"
	"regexp"
	"strings"

	"fourhour/backend/internal/artifacts"
	"fourhour/backend/internal/ids"
	"fourhour/backend/internal/model"
	"fourhour/backend/internal/money"
	"fourhour/backend/internal/paypal"
)

// AllowedCovers are the cover amounts a member can choose (SPEC.md 3).
// The 100-cent test cover is never accepted here (CONTRACTS.md section 3).
var AllowedCovers = []int64{5000, 15000, 30000}

func coverAllowed(c int64) bool {
	for _, a := range AllowedCovers {
		if a == c {
			return true
		}
	}
	return false
}

var fipsRe = regexp.MustCompile(`^[0-9]{5}$`)

// ---------------------------------------------------------------------------
// Members

const memberCols = `id, name, email, paypal_email, created_at`

func scanMember(r scanner) (model.Member, error) {
	var m model.Member
	err := r.Scan(&m.ID, &m.Name, &m.Email, &m.PayPalEmail, &m.CreatedAt)
	return m, err
}

func validEmail(s string) bool {
	if len(s) > 254 {
		return false
	}
	a, err := mail.ParseAddress(s)
	return err == nil && a.Address == s
}

// CreateMember inserts a member.
func (s *Service) CreateMember(ctx context.Context, name, email, paypalEmail string) (model.Member, error) {
	name = strings.TrimSpace(name)
	if name == "" || len(name) > 200 {
		return model.Member{}, newErr(http.StatusBadRequest, "invalid_name", "name is required (at most 200 characters).")
	}
	if !validEmail(email) {
		return model.Member{}, newErr(http.StatusBadRequest, "invalid_email", "email is not a valid address.")
	}
	if !validEmail(paypalEmail) {
		return model.Member{}, newErr(http.StatusBadRequest, "invalid_paypal_email", "paypal_email is not a valid address.")
	}
	m := model.Member{ID: ids.New(ids.Member), Name: name, Email: email, PayPalEmail: paypalEmail, CreatedAt: s.nowStr()}
	if _, err := s.db.ExecContext(ctx, `INSERT INTO members (`+memberCols+`) VALUES (?,?,?,?,?)`, m.ID, m.Name, m.Email, m.PayPalEmail, m.CreatedAt); err != nil {
		return model.Member{}, fmt.Errorf("insert member: %w", err)
	}
	return m, nil
}

// GetMember returns one member.
func (s *Service) GetMember(ctx context.Context, id string) (model.Member, error) {
	m, err := scanMember(s.db.QueryRowContext(ctx, `SELECT `+memberCols+` FROM members WHERE id = ?`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return model.Member{}, newErr(http.StatusNotFound, "member_not_found", "No member "+id+".")
	}
	if err != nil {
		return model.Member{}, fmt.Errorf("get member: %w", err)
	}
	return m, nil
}

// ---------------------------------------------------------------------------
// Quotes

// Breakdown is the quote breakdown (CONTRACTS.md section 4).
type Breakdown struct {
	ExpectedEventsPerYear     json.Number  `json:"expected_events_per_year"`
	ExpectedPayoutShare       json.Number  `json:"expected_payout_share"`
	ProbPremisesAffected      json.Number  `json:"prob_premises_affected"`
	ExpectedAnnualPayoutCents int64        `json:"expected_annual_payout_cents"`
	CredibilityWeight         *json.Number `json:"credibility_weight"`
	RiskMarginRate            json.Number  `json:"risk_margin_rate"`
	ExpenseLoadCents          json.Number  `json:"expense_load_cents"`
}

// Quote is the POST /api/quotes response.
type Quote struct {
	FIPS         string          `json:"fips"`
	CoverCents   int64           `json:"cover_cents"`
	PremiumCents int64           `json:"premium_cents"`
	ModelVersion string          `json:"model_version"`
	Breakdown    Breakdown       `json:"breakdown"`
	Provenance   json.RawMessage `json:"provenance"`
}

// Quote prices a cover from pricing_table.json with the CONTRACTS.md
// section 4 formula, in exact decimal arithmetic. The expected payout share
// is payout_schedule.t4, the only tier paid. A county that is not offered
// (or not in the table) is 422 county_not_offered with its reason.
func (s *Service) Quote(ctx context.Context, fips string, cover int64) (Quote, error) {
	if !fipsRe.MatchString(fips) {
		return Quote{}, newErr(http.StatusBadRequest, "invalid_fips", "fips must be a 5-digit county code.")
	}
	if !coverAllowed(cover) {
		return Quote{}, newErr(http.StatusBadRequest, "invalid_cover", "cover_cents must be 5000, 15000 or 30000.")
	}
	pt, err := artifacts.LoadPricingTable(s.artDir)
	if errors.Is(err, artifacts.ErrMissing) {
		return Quote{}, newErr(http.StatusNotFound, "artifact_missing", "pricing_table.json is not available.")
	}
	if err != nil {
		return Quote{}, &Error{Status: http.StatusServiceUnavailable, Code: "artifact_invalid", Message: "pricing_table.json could not be read.", Err: err}
	}
	notComputed := func(what string) error {
		return newErr(http.StatusUnprocessableEntity, "pricing_not_computed", what+" is not computed in pricing_table.json.")
	}
	if pt.ModelVersion == nil {
		return Quote{}, notComputed("model_version")
	}
	if off := pt.OfferFor(fips); !off.Offered {
		return Quote{}, newErr(http.StatusUnprocessableEntity, "county_not_offered", *off.Reason)
	}
	c := pt.Counties[fips]
	if c == nil || pt.Rates == nil || pt.PayoutSchedule == nil {
		return Quote{}, notComputed("A pricing input for " + fips)
	}
	in := money.PremiumInputs{CoverCents: cover}
	for _, f := range []struct {
		dst  **bigRat
		n    *json.Number
		name string
	}{
		{&in.ExpectedEventsPerYear, c.ExpectedEventsPerYear, "expected_events_per_year"},
		{&in.ProbPremisesAffected, c.ProbPremisesAffected, "prob_premises_affected"},
		{&in.RiskMarginRate, pt.Rates.RiskMarginRate, "risk_margin_rate"},
		{&in.ExpenseLoadCents, pt.Rates.ExpenseLoadCents, "expense_load_cents"},
		{&in.T4, pt.PayoutSchedule.T4, "payout_schedule.t4"},
	} {
		r, err := artifacts.Rat(f.n, f.name)
		if errors.Is(err, artifacts.ErrNotComputed) {
			return Quote{}, notComputed(f.name)
		}
		if err != nil {
			return Quote{}, &Error{Status: http.StatusServiceUnavailable, Code: "artifact_invalid", Message: f.name + " is not a number.", Err: err}
		}
		*f.dst = r
	}
	p, err := money.ComputePremium(in)
	if err != nil {
		return Quote{}, &Error{Status: http.StatusServiceUnavailable, Code: "artifact_invalid", Message: "The premium could not be computed.", Err: err}
	}
	return Quote{
		FIPS: fips, CoverCents: cover, PremiumCents: p.PremiumCents, ModelVersion: *pt.ModelVersion,
		Breakdown: Breakdown{
			ExpectedEventsPerYear: *c.ExpectedEventsPerYear, ExpectedPayoutShare: *pt.PayoutSchedule.T4,
			ProbPremisesAffected: *c.ProbPremisesAffected, ExpectedAnnualPayoutCents: p.ExpectedAnnualPayoutCents,
			CredibilityWeight: c.CredibilityWeight, RiskMarginRate: *pt.Rates.RiskMarginRate, ExpenseLoadCents: *pt.Rates.ExpenseLoadCents,
		},
		Provenance: pt.Provenance,
	}, nil
}

// ---------------------------------------------------------------------------
// Policies

const policyCols = `id, member_id, fips, cover_cents, premium_cents, status, model_version, created_at, cancelled_at`

func scanPolicy(r scanner) (model.Policy, error) {
	var p model.Policy
	var cancelled sql.NullString
	err := r.Scan(&p.ID, &p.MemberID, &p.FIPS, &p.CoverCents, &p.PremiumCents, &p.Status, &p.ModelVersion, &p.CreatedAt, &cancelled)
	p.CancelledAt = strPtr(cancelled)
	return p, err
}

// GetPolicy returns one policy (without vault tokens).
func (s *Service) GetPolicy(ctx context.Context, id string) (model.Policy, error) {
	p, err := scanPolicy(s.db.QueryRowContext(ctx, `SELECT `+policyCols+` FROM policies WHERE id = ?`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return model.Policy{}, newErr(http.StatusNotFound, "policy_not_found", "No policy "+id+".")
	}
	if err != nil {
		return model.Policy{}, fmt.Errorf("get policy: %w", err)
	}
	return p, nil
}

// CreatedPolicy is the POST /api/policies response.
type CreatedPolicy struct {
	Policy     model.Policy `json:"policy"`
	ApproveURL string       `json:"approve_url"`
}

// CreatePolicy prices the cover, inserts a pending_vault policy, creates a
// vault setup token (P2, PayPal-Request-Id vst-{policy_id}) and returns the
// approval link.
func (s *Service) CreatePolicy(ctx context.Context, memberID, fips string, cover int64) (CreatedPolicy, error) {
	if s.cfg.AppBaseURL == "" {
		return CreatedPolicy{}, newErr(http.StatusServiceUnavailable, "config_missing", "APP_BASE_URL is not set, so PayPal has no return URL.")
	}
	if err := s.requirePayPal(); err != nil {
		return CreatedPolicy{}, err
	}
	if _, err := s.GetMember(ctx, memberID); err != nil {
		return CreatedPolicy{}, err
	}
	q, err := s.Quote(ctx, fips, cover)
	if err != nil {
		return CreatedPolicy{}, err
	}
	p := model.Policy{ID: ids.New(ids.Policy), MemberID: memberID, FIPS: fips, CoverCents: cover, PremiumCents: q.PremiumCents,
		Status: model.PolicyPendingVault, ModelVersion: q.ModelVersion, CreatedAt: s.nowStr()}
	if _, err := s.db.ExecContext(ctx, `INSERT INTO policies (`+policyCols+`) VALUES (?,?,?,?,?,?,?,?,NULL)`,
		p.ID, p.MemberID, p.FIPS, p.CoverCents, p.PremiumCents, p.Status, p.ModelVersion, p.CreatedAt); err != nil {
		return CreatedPolicy{}, fmt.Errorf("insert policy: %w", err)
	}
	base := strings.TrimRight(s.cfg.AppBaseURL, "/")
	q2 := url.Values{"policy_id": {p.ID}}.Encode()
	st, _, err := s.pp.CreateSetupToken(ctx, "vst-"+p.ID, paypal.SetupTokenRequest{
		PaymentSource: paypal.SetupTokenPaymentSource{PayPal: &paypal.VaultPayPalWalletRequest{
			UsageType: paypal.UsageTypeMerchant,
			ExperienceContext: &paypal.VaultExperienceContext{
				ReturnURL: base + "/api/paypal/vault/return?" + q2,
				CancelURL: base + "/api/paypal/vault/cancel?" + q2,
			},
		}},
	})
	if err != nil {
		return CreatedPolicy{}, paypalErr("vault setup token", err)
	}
	approve, ok := st.ApproveURL()
	if !ok || st.ID == "" {
		return CreatedPolicy{}, newErr(http.StatusBadGateway, "paypal_error", "PayPal setup token response has no id or approve link.")
	}
	if _, err := s.db.ExecContext(ctx, `UPDATE policies SET vault_setup_token = ? WHERE id = ?`, st.ID, p.ID); err != nil {
		return CreatedPolicy{}, fmt.Errorf("store setup token: %w", err)
	}
	return CreatedPolicy{Policy: p, ApproveURL: approve}, nil
}

// CurrentPeriod is the UTC month of now, as YYYY-MM.
func (s *Service) CurrentPeriod() string { return s.now().UTC().Format("2006-01") }

// VaultReturn finishes enrolment after PayPal approval: P3 (PayPal-Request-Id
// vpt-{policy_id}), store the payment token, set the policy active, charge
// the first premium. It returns the frontend URL to redirect to.
func (s *Service) VaultReturn(ctx context.Context, policyID string) string {
	front := strings.TrimRight(s.cfg.FrontendURL, "/")
	fail := func(code string) string {
		return front + "/policies/" + url.PathEscape(policyID) + "?enrolled=0&error=" + url.QueryEscape(code)
	}
	ok := front + "/policies/" + url.PathEscape(policyID) + "?enrolled=1"
	var status string
	var setup, payment sql.NullString
	err := s.db.QueryRowContext(ctx, `SELECT status, vault_setup_token, vault_payment_token FROM policies WHERE id = ?`, policyID).Scan(&status, &setup, &payment)
	if errors.Is(err, sql.ErrNoRows) {
		return fail("policy_not_found")
	}
	if err != nil {
		s.log.Error("vault return: load policy", "policy_id", policyID, "err", err)
		return fail("internal_error")
	}
	if status == model.PolicyCancelled {
		return fail("policy_cancelled")
	}
	if !payment.Valid {
		if !setup.Valid {
			return fail("no_setup_token")
		}
		if s.pp == nil {
			return fail("paypal_unavailable")
		}
		pt, _, err := s.pp.CreatePaymentToken(ctx, "vpt-"+policyID, paypal.PaymentTokenRequest{
			PaymentSource: paypal.PaymentTokenPaymentSource{Token: &paypal.TokenIDRequest{ID: setup.String, Type: paypal.TokenTypeSetupToken}},
		})
		if err != nil {
			s.log.Error("vault return: payment token", "policy_id", policyID, "err", err)
			return fail("paypal_error")
		}
		if pt.ID == "" {
			return fail("paypal_error")
		}
		if _, err := s.db.ExecContext(ctx, `UPDATE policies SET vault_payment_token = ?, status = ? WHERE id = ? AND status = ?`,
			pt.ID, model.PolicyActive, policyID, model.PolicyPendingVault); err != nil {
			s.log.Error("vault return: store payment token", "policy_id", policyID, "err", err)
			return fail("internal_error")
		}
	}
	if _, err := s.ChargePremium(ctx, policyID, s.CurrentPeriod()); err != nil {
		code := "charge_failed"
		var se *Error
		if errors.As(err, &se) {
			code = se.Code
		}
		s.log.Error("vault return: first premium", "policy_id", policyID, "err", err)
		return fail(code)
	}
	return ok
}

// ---------------------------------------------------------------------------
// Premium charges

const chargeCols = `id, policy_id, period, amount_cents, request_id, paypal_order_id, paypal_capture_id, status, created_at`

func scanCharge(r scanner) (model.PremiumCharge, error) {
	var c model.PremiumCharge
	var order, capture sql.NullString
	err := r.Scan(&c.ID, &c.PolicyID, &c.Period, &c.AmountCents, &c.RequestID, &order, &capture, &c.Status, &c.CreatedAt)
	c.PayPalOrderID, c.PayPalCaptureID = strPtr(order), strPtr(capture)
	return c, err
}

var periodRe = regexp.MustCompile(`^[0-9]{4}-(0[1-9]|1[0-2])$`)

// ChargePremium charges one period's premium (P4, PayPal-Request-Id
// prem-{policy_id}-{period}). A second call for the same period returns the
// existing row and makes no PayPal call. The only exception is a row still
// "created" with no order ID: the earlier call's outcome is unknown, so the
// same key is sent again, and PayPal returns the original order instead of
// charging twice (spike S1).
func (s *Service) ChargePremium(ctx context.Context, policyID, period string) (model.PremiumCharge, error) {
	if !periodRe.MatchString(period) {
		return model.PremiumCharge{}, newErr(http.StatusBadRequest, "invalid_period", "period must be YYYY-MM.")
	}
	var status string
	var premium int64
	var token sql.NullString
	err := s.db.QueryRowContext(ctx, `SELECT status, premium_cents, vault_payment_token FROM policies WHERE id = ?`, policyID).Scan(&status, &premium, &token)
	if errors.Is(err, sql.ErrNoRows) {
		return model.PremiumCharge{}, newErr(http.StatusNotFound, "policy_not_found", "No policy "+policyID+".")
	}
	if err != nil {
		return model.PremiumCharge{}, fmt.Errorf("load policy: %w", err)
	}
	if status != model.PolicyActive || !token.Valid {
		return model.PremiumCharge{}, newErr(http.StatusConflict, "policy_not_active", "Policy "+policyID+" is not active with a saved wallet.")
	}

	// Step 1: the local row with its key, in a transaction.
	var ch model.PremiumCharge
	err = s.inTx(ctx, func(tx *sql.Tx) error {
		c, err := scanCharge(tx.QueryRowContext(ctx, `SELECT `+chargeCols+` FROM premium_charges WHERE policy_id = ? AND period = ?`, policyID, period))
		if err == nil {
			ch = c
			return nil
		}
		if !errors.Is(err, sql.ErrNoRows) {
			return fmt.Errorf("load charge: %w", err)
		}
		ch = model.PremiumCharge{ID: ids.New(ids.Charge), PolicyID: policyID, Period: period, AmountCents: premium,
			RequestID: "prem-" + policyID + "-" + period, Status: model.ChargeCreated, CreatedAt: s.nowStr()}
		_, err = tx.ExecContext(ctx, `INSERT INTO premium_charges (`+chargeCols+`) VALUES (?,?,?,?,?,NULL,NULL,?,?)`,
			ch.ID, ch.PolicyID, ch.Period, ch.AmountCents, ch.RequestID, ch.Status, ch.CreatedAt)
		if err != nil {
			return fmt.Errorf("insert charge: %w", err)
		}
		return nil
	})
	if isUnique(err) {
		// A concurrent call inserted the row first: return it.
		return scanCharge(s.db.QueryRowContext(ctx, `SELECT `+chargeCols+` FROM premium_charges WHERE policy_id = ? AND period = ?`, policyID, period))
	}
	if err != nil {
		return model.PremiumCharge{}, err
	}
	if ch.Status != model.ChargeCreated || ch.PayPalOrderID != nil {
		return ch, nil
	}
	if err := s.requirePayPal(); err != nil {
		return ch, err
	}

	// Step 2: the PayPal call, outside any transaction. Same body shape as
	// spike S1, which completed in the sandbox with no buyer present.
	amount, err := paypal.USD(ch.AmountCents)
	if err != nil {
		return ch, fmt.Errorf("format amount: %w", err)
	}
	order, res, err := s.pp.CreateOrder(ctx, ch.RequestID, paypal.OrderRequest{
		Intent:        paypal.IntentCapture,
		PurchaseUnits: []paypal.PurchaseUnitRequest{{Amount: amount}},
		PaymentSource: &paypal.OrderPaymentSource{PayPal: &paypal.OrderPayPalWallet{VaultID: token.String}},
	})
	if err != nil {
		s.log.Error("premium charge failed", "charge_id", ch.ID, "request_id", ch.RequestID, "err", err)
		if isClientError(err) {
			if _, uerr := s.db.ExecContext(ctx, `UPDATE premium_charges SET status = ? WHERE id = ? AND status = ?`, model.ChargeFailed, ch.ID, model.ChargeCreated); uerr != nil {
				return ch, errors.Join(paypalErr("order", err), fmt.Errorf("mark charge failed: %w", uerr))
			}
		}
		return ch, paypalErr("order", err)
	}

	// Step 3: update the row, and the ledger when the capture completed.
	capture, hasCapture := order.FirstCapture()
	newStatus := model.ChargeCreated
	switch {
	case order.Status == paypal.OrderStatusCompleted && hasCapture && capture.Status == paypal.CaptureStatusCompleted:
		newStatus = model.ChargeCompleted
	case hasCapture && (capture.Status == paypal.CaptureStatusDeclined || capture.Status == paypal.CaptureStatusFailed):
		newStatus = model.ChargeFailed
	}
	s.log.Info("premium charge", "charge_id", ch.ID, "order_id", order.ID, "order_status", order.Status, "capture_id", capture.ID, "capture_status", capture.Status, "paypal_debug_id", res.DebugID)
	err = s.inTx(ctx, func(tx *sql.Tx) error {
		var capID any
		if hasCapture && capture.ID != "" {
			capID = capture.ID
		}
		if _, err := tx.ExecContext(ctx, `UPDATE premium_charges SET paypal_order_id = ?, paypal_capture_id = ?, status = ? WHERE id = ?`, order.ID, capID, newStatus, ch.ID); err != nil {
			return fmt.Errorf("update charge: %w", err)
		}
		if newStatus == model.ChargeCompleted {
			return appendLedger(ctx, tx, s.nowStr(), "premium", ch.AmountCents, policyID, ch.ID)
		}
		return nil
	})
	if err != nil {
		return ch, err
	}
	return scanCharge(s.db.QueryRowContext(ctx, `SELECT `+chargeCols+` FROM premium_charges WHERE id = ?`, ch.ID))
}

// ---------------------------------------------------------------------------
// Seed

// SeedInput is the seed command's input.
type SeedInput struct {
	MemberEmail string // SANDBOX_MEMBER_EMAIL
	PayoutEmail string // SANDBOX_PAYOUT_EMAIL; paid on claims
	VaultToken  string // DEMO_VAULT_TOKEN
	FIPS        string
}

// SeedResult says what Seed did.
type SeedResult struct {
	Member  model.Member
	Policy  model.Policy
	Created bool
}

// Seed creates a demo member and an active test-cover policy (cover 100,
// premium 100, model_version test-cover) using a payment token saved in
// spike S1, so the loop runs with no browser step. It is idempotent: if an
// active test-cover policy for this member email and county exists, it is
// returned and nothing is written. It calls no PayPal API.
func (s *Service) Seed(ctx context.Context, in SeedInput) (SeedResult, error) {
	if in.VaultToken == "" || in.MemberEmail == "" {
		return SeedResult{}, errors.New("seed: DEMO_VAULT_TOKEN and SANDBOX_MEMBER_EMAIL are both required")
	}
	if !validEmail(in.MemberEmail) || !validEmail(in.PayoutEmail) {
		return SeedResult{}, errors.New("seed: SANDBOX_MEMBER_EMAIL and SANDBOX_PAYOUT_EMAIL must be valid addresses")
	}
	if !fipsRe.MatchString(in.FIPS) {
		return SeedResult{}, fmt.Errorf("seed: fips %q is not a 5-digit code", in.FIPS)
	}
	var res SeedResult
	err := s.inTx(ctx, func(tx *sql.Tx) error {
		row := tx.QueryRowContext(ctx, `SELECT p.id FROM policies p JOIN members m ON m.id = p.member_id
			WHERE m.email = ? AND p.fips = ? AND p.model_version = ? AND p.status = ? ORDER BY p.created_at LIMIT 1`,
			in.MemberEmail, in.FIPS, model.TestCoverModelVersion, model.PolicyActive)
		var pid string
		switch err := row.Scan(&pid); {
		case err == nil:
			p, err := scanPolicy(tx.QueryRowContext(ctx, `SELECT `+policyCols+` FROM policies WHERE id = ?`, pid))
			if err != nil {
				return fmt.Errorf("load seeded policy: %w", err)
			}
			m, err := scanMember(tx.QueryRowContext(ctx, `SELECT `+memberCols+` FROM members WHERE id = ?`, p.MemberID))
			if err != nil {
				return fmt.Errorf("load seeded member: %w", err)
			}
			res = SeedResult{Member: m, Policy: p}
			return nil
		case !errors.Is(err, sql.ErrNoRows):
			return fmt.Errorf("find seeded policy: %w", err)
		}
		now := s.nowStr()
		m := model.Member{ID: ids.New(ids.Member), Name: "Demo member (sandbox test cover)", Email: in.MemberEmail, PayPalEmail: in.PayoutEmail, CreatedAt: now}
		if _, err := tx.ExecContext(ctx, `INSERT INTO members (`+memberCols+`) VALUES (?,?,?,?,?)`, m.ID, m.Name, m.Email, m.PayPalEmail, m.CreatedAt); err != nil {
			return fmt.Errorf("insert member: %w", err)
		}
		p := model.Policy{ID: ids.New(ids.Policy), MemberID: m.ID, FIPS: in.FIPS, CoverCents: 100, PremiumCents: 100,
			Status: model.PolicyActive, ModelVersion: model.TestCoverModelVersion, CreatedAt: now}
		if _, err := tx.ExecContext(ctx, `INSERT INTO policies (`+policyCols+`, vault_payment_token) VALUES (?,?,?,?,?,?,?,?,NULL,?)`,
			p.ID, p.MemberID, p.FIPS, p.CoverCents, p.PremiumCents, p.Status, p.ModelVersion, p.CreatedAt, in.VaultToken); err != nil {
			return fmt.Errorf("insert policy: %w", err)
		}
		res = SeedResult{Member: m, Policy: p, Created: true}
		return nil
	})
	return res, err
}
