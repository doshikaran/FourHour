package service

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"fourhour/backend/internal/ids"
	"fourhour/backend/internal/model"
	"fourhour/backend/internal/paypal"
	"fourhour/backend/internal/rules"
)

const claimCols = `id, policy_id, outage_event_id, tier, rule, decision, amount_cents, reason, decided_at, sim_time, witness_entry_id`

func scanClaim(r scanner) (model.Claim, error) {
	var c model.Claim
	var ev, sim, wen sql.NullString
	err := r.Scan(&c.ID, &c.PolicyID, &ev, &c.Tier, &c.Rule, &c.Decision, &c.AmountCents, &c.Reason, &c.DecidedAt, &sim, &wen)
	c.OutageEventID, c.SimTime, c.WitnessEntryID = strPtr(ev), strPtr(sim), strPtr(wen)
	return c, err
}

const payoutCols = `id, claim_id, sender_batch_id, paypal_batch_id, paypal_item_id, amount_cents, status, created_at, confirmed_at, confirmed_via`

func scanPayout(r scanner) (model.Payout, error) {
	var p model.Payout
	var batch, item, cat, cvia sql.NullString
	err := r.Scan(&p.ID, &p.ClaimID, &p.SenderBatchID, &batch, &item, &p.AmountCents, &p.Status, &p.CreatedAt, &cat, &cvia)
	p.PayPalBatchID, p.PayPalItemID, p.ConfirmedAt, p.ConfirmedVia = strPtr(batch), strPtr(item), strPtr(cat), strPtr(cvia)
	return p, err
}

// SenderBatchID is the P5 idempotency key: clm-{policy_id}-{outage_event_id}-{tier}.
func SenderBatchID(policyID, eventID, tier string) string {
	return "clm-" + policyID + "-" + eventID + "-" + tier
}

// RecordedClaim is the result of RecordDecision.
type RecordedClaim struct {
	Claim   model.Claim
	Payout  *model.Payout
	Created bool // false when the claim already existed
}

// RecordDecision stores a rule decision as a claim and, when it pays, a
// payout row with its sender_batch_id, in one transaction. The database
// unique keys make a repeat return the existing claim (Created false), so
// one outage event can never get two claims or payouts for one tier.
func (s *Service) RecordDecision(ctx context.Context, policyID string, d rules.Decision, simTime *time.Time) (RecordedClaim, error) {
	c := model.Claim{ID: ids.New(ids.Claim), PolicyID: policyID, Tier: d.Tier, Rule: d.Rule, Decision: d.Decision,
		AmountCents: d.AmountCents, Reason: d.Reason, DecidedAt: s.nowStr()}
	if d.EventID != "" {
		v := d.EventID
		c.OutageEventID = &v
	}
	if d.WitnessEntryID != "" {
		v := d.WitnessEntryID
		c.WitnessEntryID = &v
	}
	if simTime != nil {
		v := ids.FormatTime(*simTime)
		c.SimTime = &v
	}
	var po *model.Payout
	if d.Decision == rules.Paid && d.AmountCents > 0 {
		if d.EventID == "" {
			return RecordedClaim{}, errors.New("record decision: a paid claim needs an outage event")
		}
		po = &model.Payout{ID: ids.New(ids.Payout), ClaimID: c.ID, SenderBatchID: SenderBatchID(policyID, d.EventID, d.Tier),
			AmountCents: d.AmountCents, Status: model.PayoutCreated, CreatedAt: c.DecidedAt}
	}
	err := s.inTx(ctx, func(tx *sql.Tx) error {
		if _, err := tx.ExecContext(ctx, `INSERT INTO claims (`+claimCols+`) VALUES (?,?,?,?,?,?,?,?,?,?,?)`,
			c.ID, c.PolicyID, nullStr(c.OutageEventID), c.Tier, c.Rule, c.Decision, c.AmountCents, c.Reason, c.DecidedAt, nullStr(c.SimTime), nullStr(c.WitnessEntryID)); err != nil {
			return fmt.Errorf("insert claim: %w", err)
		}
		if po != nil {
			if _, err := tx.ExecContext(ctx, `INSERT INTO payouts (`+payoutCols+`) VALUES (?,?,?,NULL,NULL,?,?,?,NULL,NULL)`,
				po.ID, po.ClaimID, po.SenderBatchID, po.AmountCents, po.Status, po.CreatedAt); err != nil {
				return fmt.Errorf("insert payout: %w", err)
			}
		}
		return nil
	})
	if isUnique(err) {
		var existing model.Claim
		var qerr error
		if d.EventID != "" {
			existing, qerr = scanClaim(s.db.QueryRowContext(ctx, `SELECT `+claimCols+` FROM claims WHERE policy_id = ? AND outage_event_id = ? AND tier = ?`, policyID, d.EventID, d.Tier))
		} else {
			existing, qerr = scanClaim(s.db.QueryRowContext(ctx, `SELECT `+claimCols+` FROM claims WHERE policy_id = ? AND witness_entry_id = ?`, policyID, d.WitnessEntryID))
		}
		if qerr != nil {
			return RecordedClaim{}, errors.Join(err, fmt.Errorf("load existing claim: %w", qerr))
		}
		p, perr := s.payoutForClaim(ctx, existing.ID)
		if perr != nil {
			return RecordedClaim{}, perr
		}
		return RecordedClaim{Claim: existing, Payout: p}, nil
	}
	if err != nil {
		return RecordedClaim{}, err
	}
	return RecordedClaim{Claim: c, Payout: po, Created: true}, nil
}

func (s *Service) payoutForClaim(ctx context.Context, claimID string) (*model.Payout, error) {
	p, err := scanPayout(s.db.QueryRowContext(ctx, `SELECT `+payoutCols+` FROM payouts WHERE claim_id = ?`, claimID))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("load payout: %w", err)
	}
	return &p, nil
}

// GetPayout returns one payout row.
func (s *Service) GetPayout(ctx context.Context, id string) (model.Payout, error) {
	p, err := scanPayout(s.db.QueryRowContext(ctx, `SELECT `+payoutCols+` FROM payouts WHERE id = ?`, id))
	if err != nil {
		return model.Payout{}, fmt.Errorf("load payout %s: %w", id, err)
	}
	return p, nil
}

// SendPayoutAsync sends a payout in the background (it outlives the
// request or replay that decided the claim).
func (s *Service) SendPayoutAsync(payoutID string) {
	s.bg.Add(1)
	go func() {
		defer s.bg.Done()
		ctx, cancel := context.WithTimeout(s.bgCtx, time.Minute)
		defer cancel()
		if _, err := s.SendPayout(ctx, payoutID); err != nil {
			s.log.Error("send payout", "payout_id", payoutID, "err", err)
		}
	}()
}

// SendPayout makes the P5 call for a payout row still "created", updates
// the row, emits payout_sent and starts the polling fallback.
//
// A repeated sender_batch_id (decision D7) is not a failure: the original
// batch ID is read from the error's link, fetched with P6, and the row
// continues from that batch's status. Any other 4xx marks the payout
// failed. A 5xx or network error leaves it "created": the outcome is
// unknown and the same sender_batch_id is safe to send again.
func (s *Service) SendPayout(ctx context.Context, payoutID string) (model.Payout, error) {
	if err := s.requirePayPal(); err != nil {
		return model.Payout{}, err
	}
	var po model.Payout
	var receiver, claimID string
	row := s.db.QueryRowContext(ctx, `SELECT `+prefixCols("p.", payoutCols)+`, m.paypal_email, c.id
		FROM payouts p JOIN claims c ON c.id = p.claim_id JOIN policies pol ON pol.id = c.policy_id JOIN members m ON m.id = pol.member_id
		WHERE p.id = ?`, payoutID)
	var batch, item, cat, cvia sql.NullString
	if err := row.Scan(&po.ID, &po.ClaimID, &po.SenderBatchID, &batch, &item, &po.AmountCents, &po.Status, &po.CreatedAt, &cat, &cvia, &receiver, &claimID); err != nil {
		return model.Payout{}, fmt.Errorf("load payout %s: %w", payoutID, err)
	}
	po.PayPalBatchID, po.PayPalItemID, po.ConfirmedAt, po.ConfirmedVia = strPtr(batch), strPtr(item), strPtr(cat), strPtr(cvia)
	if po.Status != model.PayoutCreated {
		return po, nil
	}
	amount, err := paypal.PayoutUSD(po.AmountCents)
	if err != nil {
		return po, fmt.Errorf("format amount: %w", err)
	}
	// Same body shape as spike S2, which reached SUCCESS in the sandbox.
	req := paypal.CreatePayoutRequest{
		SenderBatchHeader: paypal.SenderBatchHeader{
			SenderBatchID: po.SenderBatchID,
			RecipientType: paypal.RecipientTypeEmail,
			EmailSubject:  "FourHour outage cover payout (sandbox)",
		},
		Items: []paypal.PayoutItemRequest{{
			RecipientType:   paypal.RecipientTypeEmail,
			Amount:          amount,
			Note:            "FourHour claim " + claimID + " (sandbox prototype)",
			Receiver:        receiver,
			SenderItemID:    claimID,
			RecipientWallet: paypal.RecipientWalletPayPal,
		}},
	}
	out, res, err := s.pp.CreatePayout(ctx, req)
	var batchID string
	switch {
	case err == nil:
		batchID = out.BatchHeader.PayoutBatchID
		s.log.Info("payout sent", "payout_id", po.ID, "sender_batch_id", po.SenderBatchID, "payout_batch_id", batchID, "batch_status", out.BatchHeader.BatchStatus, "paypal_debug_id", res.DebugID)
	default:
		if dup, ok := paypal.DuplicateBatchID(err); ok {
			batchID = dup
			s.log.Info("payout already sent (duplicate sender_batch_id); continuing from the original batch", "payout_id", po.ID, "payout_batch_id", dup, "err", err)
		} else {
			body := ""
			if ae, ok := paypal.AsAPIError(err); ok {
				body = string(ae.Body)
			}
			s.log.Error("payout call failed", "payout_id", po.ID, "sender_batch_id", po.SenderBatchID, "err", err, "paypal_error_body", body)
			if isClientError(err) {
				if _, uerr := s.db.ExecContext(ctx, `UPDATE payouts SET status = ? WHERE id = ? AND status = ?`, model.PayoutFailed, po.ID, model.PayoutCreated); uerr != nil {
					return po, errors.Join(paypalErr("payout", err), fmt.Errorf("mark payout failed: %w", uerr))
				}
				if p, gerr := s.GetPayout(ctx, po.ID); gerr == nil {
					s.publish(model.EvPayoutSent, model.PayoutEvent{SimTime: s.simTimePtr(), Payout: p})
				}
			}
			return po, paypalErr("payout", err)
		}
	}
	if batchID == "" {
		return po, errors.New("payout response has no payout_batch_id")
	}
	if _, err := s.db.ExecContext(ctx, `UPDATE payouts SET paypal_batch_id = ?, status = ? WHERE id = ? AND status = ?`, batchID, model.PayoutPending, po.ID, model.PayoutCreated); err != nil {
		return po, fmt.Errorf("update payout: %w", err)
	}
	sent, err := s.GetPayout(ctx, po.ID)
	if err != nil {
		return po, err
	}
	s.publish(model.EvPayoutSent, model.PayoutEvent{SimTime: s.simTimePtr(), Payout: sent})
	s.startPolling(po.ID)
	return sent, nil
}

func prefixCols(prefix, cols string) string {
	out := ""
	start := 0
	for i := 0; i <= len(cols); i++ {
		if i == len(cols) || cols[i] == ',' {
			name := cols[start:i]
			for len(name) > 0 && name[0] == ' ' {
				name = name[1:]
			}
			if out != "" {
				out += ", "
			}
			out += prefix + name
			start = i + 1
		}
	}
	return out
}

// payoutStatus maps a P6 item transaction_status (or a batch status when no
// item is present) to the payouts.status enum. PROCESSING and PENDING are
// still pending (CONTRACTS.md section 7).
func payoutStatus(itemStatus, batchStatus string) string {
	switch itemStatus {
	case paypal.TransactionStatusSuccess:
		return model.PayoutSuccess
	case paypal.TransactionStatusFailed, paypal.TransactionStatusReturned, paypal.TransactionStatusBlocked,
		paypal.TransactionStatusRefunded, paypal.TransactionStatusReversed, "CANCELED":
		return model.PayoutFailed
	case paypal.TransactionStatusUnclaimed:
		return model.PayoutUnclaimed
	case "":
		if batchStatus == paypal.BatchStatusDenied || batchStatus == paypal.BatchStatusCanceled {
			return model.PayoutFailed
		}
	}
	return model.PayoutPending
}

func terminal(status string) bool {
	return status == model.PayoutSuccess || status == model.PayoutFailed || status == model.PayoutUnclaimed
}

// applyBatch updates a payout from a P6 response.
func (s *Service) applyBatch(ctx context.Context, payoutID string, b paypal.PayoutBatch, via string) (model.Payout, error) {
	po, err := s.GetPayout(ctx, payoutID)
	if err != nil {
		return model.Payout{}, err
	}
	var item *paypal.PayoutBatchItem
	for i := range b.Items {
		if b.Items[i].PayoutItem.SenderItemID == po.ClaimID || len(b.Items) == 1 {
			item = &b.Items[i]
			break
		}
	}
	itemID, itemStatus := "", ""
	if item != nil {
		itemID, itemStatus = item.PayoutItemID, item.TransactionStatus
	}
	return s.applyStatus(ctx, payoutID, b.BatchHeader.PayoutBatchID, itemID, payoutStatus(itemStatus, b.BatchHeader.BatchStatus), via)
}

// applyStatus moves a payout to status. Only created or pending rows change,
// so a late or repeated report never undoes a final state. The first
// confirmation of success sets confirmed_at and confirmed_via and appends
// the ledger row in the same transaction; payout_confirmed is emitted for
// every move to a final state.
func (s *Service) applyStatus(ctx context.Context, payoutID, batchID, itemID, status, via string) (model.Payout, error) {
	moved := false
	err := s.inTx(ctx, func(tx *sql.Tx) error {
		po, err := scanPayout(tx.QueryRowContext(ctx, `SELECT `+payoutCols+` FROM payouts WHERE id = ?`, payoutID))
		if err != nil {
			return fmt.Errorf("load payout: %w", err)
		}
		if terminal(po.Status) {
			return nil
		}
		var batchArg, itemArg any = nullStr(po.PayPalBatchID), nullStr(po.PayPalItemID)
		if batchID != "" {
			batchArg = batchID
		}
		if itemID != "" {
			itemArg = itemID
		}
		if status == model.PayoutSuccess {
			now := s.nowStr()
			if _, err := tx.ExecContext(ctx, `UPDATE payouts SET paypal_batch_id = ?, paypal_item_id = ?, status = ?, confirmed_at = ?, confirmed_via = ? WHERE id = ?`,
				batchArg, itemArg, status, now, via, payoutID); err != nil {
				return fmt.Errorf("confirm payout: %w", err)
			}
			var policyID string
			if err := tx.QueryRowContext(ctx, `SELECT c.policy_id FROM claims c WHERE c.id = ?`, po.ClaimID).Scan(&policyID); err != nil {
				return fmt.Errorf("load claim policy: %w", err)
			}
			moved = true
			return appendLedger(ctx, tx, now, "payout", -po.AmountCents, policyID, po.ID)
		}
		if _, err := tx.ExecContext(ctx, `UPDATE payouts SET paypal_batch_id = ?, paypal_item_id = ?, status = ? WHERE id = ?`, batchArg, itemArg, status, payoutID); err != nil {
			return fmt.Errorf("update payout: %w", err)
		}
		moved = terminal(status)
		return nil
	})
	if err != nil {
		return model.Payout{}, err
	}
	po, err := s.GetPayout(ctx, payoutID)
	if err != nil {
		return model.Payout{}, err
	}
	if moved {
		s.log.Info("payout final", "payout_id", po.ID, "status", po.Status, "via", via, "payout_batch_id", nullStr(po.PayPalBatchID), "payout_item_id", nullStr(po.PayPalItemID))
		s.publish(model.EvPayoutConfirmed, model.PayoutEvent{SimTime: s.simTimePtr(), Payout: po})
	}
	return po, nil
}

// startPolling is the fallback for when no webhook arrives: first poll
// after pollWait, then every pollStep, for up to pollMax.
func (s *Service) startPolling(payoutID string) {
	s.bg.Add(1)
	go func() {
		defer s.bg.Done()
		deadline := time.NewTimer(s.pollWait + s.pollMax)
		defer deadline.Stop()
		wait := time.NewTimer(s.pollWait)
		defer wait.Stop()
		for {
			select {
			case <-s.bgCtx.Done():
				return
			case <-deadline.C:
				s.log.Warn("payout polling gave up; still pending", "payout_id", payoutID)
				return
			case <-wait.C:
			}
			done, err := s.PollPayout(s.bgCtx, payoutID)
			if err != nil {
				s.log.Error("payout poll", "payout_id", payoutID, "err", err)
			}
			if done {
				return
			}
			wait.Reset(s.pollStep)
		}
	}()
}

// PollPayout reads a pending payout's batch once (P6) and applies it. It
// returns true when there is nothing more to poll.
func (s *Service) PollPayout(ctx context.Context, payoutID string) (bool, error) {
	po, err := s.GetPayout(ctx, payoutID)
	if err != nil {
		return true, err
	}
	if po.Status != model.PayoutPending || po.PayPalBatchID == nil {
		return true, nil
	}
	cctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	b, res, err := s.pp.GetPayoutBatch(cctx, *po.PayPalBatchID)
	if err != nil {
		return false, paypalErr("payout status", err)
	}
	item := ""
	if len(b.Items) > 0 {
		item = b.Items[0].TransactionStatus
	}
	s.log.Info("payout poll", "payout_id", po.ID, "payout_batch_id", *po.PayPalBatchID, "batch_status", b.BatchHeader.BatchStatus, "item_status", item, "paypal_debug_id", res.DebugID)
	np, err := s.applyBatch(ctx, payoutID, b, model.ViaPoll)
	if err != nil {
		return false, err
	}
	return terminal(np.Status), nil
}

// ResumePayouts restarts work left by a previous process: payouts still
// "created" are sent again with the same key, and "pending" ones are polled.
func (s *Service) ResumePayouts(ctx context.Context) error {
	rows, err := s.db.QueryContext(ctx, `SELECT id, status FROM payouts WHERE status IN (?, ?)`, model.PayoutCreated, model.PayoutPending)
	if err != nil {
		return fmt.Errorf("list unfinished payouts: %w", err)
	}
	type pr struct{ id, status string }
	var list []pr
	for rows.Next() {
		var p pr
		if err := rows.Scan(&p.id, &p.status); err != nil {
			rows.Close()
			return fmt.Errorf("scan payout: %w", err)
		}
		list = append(list, p)
	}
	if err := errors.Join(rows.Err(), rows.Close()); err != nil {
		return fmt.Errorf("list unfinished payouts: %w", err)
	}
	if s.pp == nil {
		if len(list) > 0 {
			s.log.Warn("unfinished payouts not resumed: PayPal not configured", "count", len(list))
		}
		return nil
	}
	for _, p := range list {
		if p.status == model.PayoutCreated {
			s.SendPayoutAsync(p.id)
		} else {
			s.startPolling(p.id)
		}
	}
	return nil
}
