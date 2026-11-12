// Package service is the backend's business logic: members, policies,
// premiums, claims, payouts, webhooks, witness entries and the ledger. It
// owns every database write. HTTP lives in package api; the replay clock
// lives in package replay.
//
// Money calls follow CONTRACTS.md section 7: insert the local row with its
// idempotency key in a transaction, call PayPal outside any transaction,
// then update the row (and the ledger) in a second transaction.
package service

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"fourhour/backend/internal/config"
	"fourhour/backend/internal/ids"
	"fourhour/backend/internal/paypal"
	"fourhour/backend/internal/stream"
)

// Error is a failure with the contract's error shape (CONTRACTS.md section 1).
type Error struct {
	Status  int
	Code    string
	Message string
	DebugID *string
	Err     error
}

func (e *Error) Error() string {
	if e.Err != nil {
		return fmt.Sprintf("%s: %s: %v", e.Code, e.Message, e.Err)
	}
	return e.Code + ": " + e.Message
}

func (e *Error) Unwrap() error { return e.Err }

func newErr(status int, code, msg string) *Error {
	return &Error{Status: status, Code: code, Message: msg}
}

// paypalErr turns a PayPal client error into an Error carrying debug_id.
func paypalErr(what string, err error) *Error {
	e := &Error{Status: http.StatusBadGateway, Code: "paypal_error", Message: "PayPal " + what + " failed.", Err: err}
	if ae, ok := paypal.AsAPIError(err); ok {
		if ae.DebugID != "" {
			id := ae.DebugID
			e.DebugID = &id
		}
		if ae.Parsed != nil && ae.Parsed.Name != "" {
			e.Message = fmt.Sprintf("PayPal %s failed: HTTP %d %s.", what, ae.StatusCode, ae.Parsed.Name)
		} else {
			e.Message = fmt.Sprintf("PayPal %s failed: HTTP %d.", what, ae.StatusCode)
		}
	}
	return e
}

// isClientError reports whether err is a PayPal 4xx: the request was
// refused, so the outcome is known. Anything else (5xx, timeout, network)
// leaves the outcome unknown, and the row keeps status created so the same
// key can be retried.
func isClientError(err error) bool {
	ae, ok := paypal.AsAPIError(err)
	return ok && ae.StatusCode >= 400 && ae.StatusCode < 500
}

// Options configure a Service.
type Options struct {
	DB           *sql.DB
	PayPal       *paypal.Client // nil when credentials are missing
	Log          *slog.Logger
	Hub          *stream.Hub
	Config       config.Config
	ArtifactsDir string
	Now          func() time.Time
	// Payout polling fallback (CONTRACTS.md section 7): first poll after
	// PollDelay, then every PollInterval, for up to PollTimeout.
	PollDelay    time.Duration
	PollInterval time.Duration
	PollTimeout  time.Duration
}

// Service implements the backend operations. It is safe for concurrent use.
type Service struct {
	db       *sql.DB
	pp       *paypal.Client
	log      *slog.Logger
	hub      *stream.Hub
	cfg      config.Config
	artDir   string
	now      func() time.Time
	pollWait time.Duration
	pollStep time.Duration
	pollMax  time.Duration

	simClock atomic.Pointer[func() (time.Time, bool)]
	witness  sync.Mutex // serialises witness uploads (chain state)

	bgCtx    context.Context
	bgCancel context.CancelFunc
	bg       sync.WaitGroup
}

// New returns a Service. Close stops its background pollers.
func New(o Options) *Service {
	s := &Service{
		db: o.DB, pp: o.PayPal, log: o.Log, hub: o.Hub, cfg: o.Config, artDir: o.ArtifactsDir, now: o.Now,
		pollWait: o.PollDelay, pollStep: o.PollInterval, pollMax: o.PollTimeout,
	}
	if s.now == nil {
		s.now = time.Now
	}
	if s.pollWait <= 0 {
		s.pollWait = 10 * time.Second
	}
	if s.pollStep <= 0 {
		s.pollStep = 5 * time.Second
	}
	if s.pollMax <= 0 {
		s.pollMax = 2 * time.Minute
	}
	if s.hub == nil {
		s.hub = stream.NewHub(256)
	}
	s.bgCtx, s.bgCancel = context.WithCancel(context.Background())
	return s
}

// Close cancels background work and waits for it.
func (s *Service) Close() {
	s.bgCancel()
	s.bg.Wait()
}

// Hub returns the event hub.
func (s *Service) Hub() *stream.Hub { return s.hub }

// ArtifactsDir returns the artifacts directory.
func (s *Service) ArtifactsDir() string { return s.artDir }

// PayPalReady reports whether a PayPal client is configured.
func (s *Service) PayPalReady() bool { return s.pp != nil }

// SetSimClock installs the replay clock. f returns the simulated time and
// true while a replay runs.
func (s *Service) SetSimClock(f func() (time.Time, bool)) { s.simClock.Store(&f) }

// SimNow returns the replay clock, if a replay is running.
func (s *Service) SimNow() (time.Time, bool) {
	f := s.simClock.Load()
	if f == nil {
		return time.Time{}, false
	}
	return (*f)()
}

func (s *Service) simTimePtr() *string {
	if t, ok := s.SimNow(); ok {
		v := ids.FormatTime(t)
		return &v
	}
	return nil
}

func (s *Service) nowStr() string { return ids.FormatTime(s.now()) }

func (s *Service) publish(event string, body any) {
	if err := s.hub.Publish(event, body); err != nil {
		s.log.Error("stream publish", "event", event, "err", err)
	}
}

func (s *Service) requirePayPal() error {
	if s.pp == nil {
		return newErr(http.StatusServiceUnavailable, "paypal_unavailable", "PayPal credentials are not configured.")
	}
	return nil
}

// LogPayPalResponse is the client's OnResponse hook: every PayPal response's
// paypal-debug-id is logged (CONTRACTS.md section 7).
func LogPayPalResponse(log *slog.Logger) func(paypal.ResponseInfo) {
	return func(r paypal.ResponseInfo) {
		log.Info("paypal response", "method", r.Method, "path", r.Path, "status", r.StatusCode, "paypal_debug_id", r.DebugID)
	}
}

// inTx runs fn in a transaction.
func (s *Service) inTx(ctx context.Context, fn func(*sql.Tx) error) (err error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin: %w", err)
	}
	defer func() {
		if err != nil {
			if rb := tx.Rollback(); rb != nil && !errors.Is(rb, sql.ErrTxDone) {
				err = errors.Join(err, fmt.Errorf("rollback: %w", rb))
			}
		}
	}()
	if err = fn(tx); err != nil {
		return err
	}
	if err = tx.Commit(); err != nil {
		return fmt.Errorf("commit: %w", err)
	}
	return nil
}

func isUnique(err error) bool {
	return err != nil && strings.Contains(err.Error(), "UNIQUE constraint failed")
}

// appendLedger adds one ledger row. The (kind, ref_id) unique key makes a
// repeat a no-op, so a money movement is never appended twice.
func appendLedger(ctx context.Context, tx *sql.Tx, at, kind string, amount int64, policyID, refID string) error {
	_, err := tx.ExecContext(ctx, `INSERT INTO ledger_entries (id, at, kind, amount_cents, policy_id, ref_id) VALUES (?,?,?,?,?,?)
		ON CONFLICT (kind, ref_id) DO NOTHING`, ids.New(ids.LedgerEntry), at, kind, amount, policyID, refID)
	if err != nil {
		return fmt.Errorf("ledger %s %s: %w", kind, refID, err)
	}
	return nil
}

type scanner interface{ Scan(dest ...any) error }

func strPtr(ns sql.NullString) *string {
	if !ns.Valid {
		return nil
	}
	v := ns.String
	return &v
}

func nullStr(p *string) any {
	if p == nil {
		return nil
	}
	return *p
}
