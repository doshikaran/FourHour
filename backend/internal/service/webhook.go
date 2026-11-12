package service

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"fourhour/backend/internal/model"
	"fourhour/backend/internal/paypal"
)

// Webhook event types FourHour acts on (developer.paypal.com/api/rest/
// webhooks/event-names, read 2026-11-12). The payments v2 section also
// lists PAYMENT.CAPTURE.DECLINED, handled like DENIED.
const (
	WhCaptureCompleted  = "PAYMENT.CAPTURE.COMPLETED"
	WhCaptureDenied     = "PAYMENT.CAPTURE.DENIED"
	WhCaptureDeclined   = "PAYMENT.CAPTURE.DECLINED"
	WhCaptureRefunded   = "PAYMENT.CAPTURE.REFUNDED"
	WhBatchSuccess      = "PAYMENT.PAYOUTSBATCH.SUCCESS"
	WhBatchDenied       = "PAYMENT.PAYOUTSBATCH.DENIED"
	WhItemSucceeded     = "PAYMENT.PAYOUTS-ITEM.SUCCEEDED"
	WhItemFailed        = "PAYMENT.PAYOUTS-ITEM.FAILED"
	WhItemUnclaimed     = "PAYMENT.PAYOUTS-ITEM.UNCLAIMED"
	whItemPrefix        = "PAYMENT.PAYOUTS-ITEM."
	maxWebhookBodyBytes = 1 << 20
)

// webhookEnvelope is the part of PayPal's "event" schema FourHour reads.
type webhookEnvelope struct {
	ID           string          `json:"id"`
	EventType    string          `json:"event_type"`
	ResourceType string          `json:"resource_type"`
	Resource     json.RawMessage `json:"resource"`
}

// payoutItemResource is the payout_item schema (payouts item events).
type payoutItemResource struct {
	PayoutItemID      string `json:"payout_item_id"`
	PayoutBatchID     string `json:"payout_batch_id"`
	TransactionStatus string `json:"transaction_status"`
	PayoutItem        struct {
		SenderItemID string `json:"sender_item_id"`
	} `json:"payout_item"`
}

// payoutBatchResource is the payout_batch schema (batch events).
type payoutBatchResource struct {
	BatchHeader struct {
		PayoutBatchID string `json:"payout_batch_id"`
		BatchStatus   string `json:"batch_status"`
	} `json:"batch_header"`
}

// idResource is a resource with an id (captures, refunds).
type idResource struct {
	ID string `json:"id"`
}

func resourceID(env webhookEnvelope) string {
	switch {
	case strings.HasPrefix(env.EventType, whItemPrefix):
		var r payoutItemResource
		if json.Unmarshal(env.Resource, &r) == nil {
			return r.PayoutItemID
		}
	case strings.HasPrefix(env.EventType, "PAYMENT.PAYOUTSBATCH."):
		var r payoutBatchResource
		if json.Unmarshal(env.Resource, &r) == nil {
			return r.BatchHeader.PayoutBatchID
		}
	default:
		var r idResource
		if json.Unmarshal(env.Resource, &r) == nil {
			return r.ID
		}
	}
	return ""
}

// HandleWebhook implements CONTRACTS.md section 7, webhook handling: store
// the raw event; skip it if its ID is already stored; verify with P8; if
// verified, update the matching row and the ledger. It returns nil whenever
// the event is stored (the handler then answers 200). If P8 itself cannot
// be reached, the stored row is removed and an error is returned, so the
// handler answers 503 and PayPal retries the delivery.
func (s *Service) HandleWebhook(ctx context.Context, h http.Header, raw []byte) error {
	var env webhookEnvelope
	if err := json.Unmarshal(raw, &env); err != nil || env.ID == "" || env.EventType == "" {
		return newErr(http.StatusBadRequest, "invalid_webhook", "Body is not a PayPal webhook event.")
	}
	res, err := s.db.ExecContext(ctx, `INSERT INTO webhook_events (id, event_type, resource_id, verified, received_at, payload_json)
		VALUES (?,?,?,0,?,?) ON CONFLICT (id) DO NOTHING`, env.ID, env.EventType, resourceID(env), s.nowStr(), string(raw))
	if err != nil {
		return fmt.Errorf("store webhook: %w", err)
	}
	if n, err := res.RowsAffected(); err != nil {
		return fmt.Errorf("store webhook: %w", err)
	} else if n == 0 {
		s.log.Info("webhook already stored; skipped", "event_id", env.ID, "event_type", env.EventType)
		return nil
	}
	if s.pp == nil || s.cfg.PayPalWebhookID == "" {
		s.log.Warn("webhook stored unverified: PayPal client or PAYPAL_WEBHOOK_ID not configured", "event_id", env.ID)
		return nil
	}
	vctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	v, vres, err := s.pp.VerifyWebhookSignature(vctx, paypal.VerifyWebhookRequestFromHeaders(h, s.cfg.PayPalWebhookID, raw))
	if err != nil {
		if isClientError(err) {
			// PayPal refused the verification request (for example, the
			// simulator's events): stored unverified, nothing changes.
			s.log.Warn("webhook verification refused; stored unverified", "event_id", env.ID, "err", err)
			return nil
		}
		s.log.Error("webhook verification unavailable; removing so PayPal retries", "event_id", env.ID, "err", err)
		if _, derr := s.db.ExecContext(ctx, `DELETE FROM webhook_events WHERE id = ? AND verified = 0`, env.ID); derr != nil {
			return errors.Join(paypalErr("webhook verification", err), fmt.Errorf("remove webhook: %w", derr))
		}
		return paypalErr("webhook verification", err)
	}
	s.log.Info("webhook verification", "event_id", env.ID, "event_type", env.EventType, "verification_status", v.VerificationStatus, "paypal_debug_id", vres.DebugID)
	if v.VerificationStatus != paypal.VerificationSuccess {
		return nil
	}
	if _, err := s.db.ExecContext(ctx, `UPDATE webhook_events SET verified = 1 WHERE id = ?`, env.ID); err != nil {
		return fmt.Errorf("mark webhook verified: %w", err)
	}
	if err := s.applyWebhook(ctx, env); err != nil {
		// The event is stored and verified; a failure to apply it is
		// logged, and the polling fallback still confirms payouts.
		s.log.Error("apply webhook", "event_id", env.ID, "event_type", env.EventType, "err", err)
	}
	return nil
}

func (s *Service) applyWebhook(ctx context.Context, env webhookEnvelope) error {
	switch {
	case strings.HasPrefix(env.EventType, whItemPrefix):
		var r payoutItemResource
		if err := json.Unmarshal(env.Resource, &r); err != nil {
			return fmt.Errorf("payout item resource: %w", err)
		}
		var payoutID string
		err := s.db.QueryRowContext(ctx, `SELECT id FROM payouts WHERE claim_id = ? OR (paypal_item_id IS NOT NULL AND paypal_item_id = ?) LIMIT 1`,
			r.PayoutItem.SenderItemID, r.PayoutItemID).Scan(&payoutID)
		if errors.Is(err, sql.ErrNoRows) {
			s.log.Warn("webhook for unknown payout item", "payout_item_id", r.PayoutItemID, "sender_item_id", r.PayoutItem.SenderItemID)
			return nil
		}
		if err != nil {
			return fmt.Errorf("find payout: %w", err)
		}
		status := r.TransactionStatus
		if status == "" {
			switch env.EventType {
			case WhItemSucceeded:
				status = paypal.TransactionStatusSuccess
			case WhItemFailed:
				status = paypal.TransactionStatusFailed
			case WhItemUnclaimed:
				status = paypal.TransactionStatusUnclaimed
			}
		}
		_, err = s.applyStatus(ctx, payoutID, r.PayoutBatchID, r.PayoutItemID, payoutStatus(status, ""), model.ViaWebhook)
		return err
	case env.EventType == WhBatchSuccess || env.EventType == WhBatchDenied:
		var r payoutBatchResource
		if err := json.Unmarshal(env.Resource, &r); err != nil {
			return fmt.Errorf("payout batch resource: %w", err)
		}
		var payoutID string
		err := s.db.QueryRowContext(ctx, `SELECT id FROM payouts WHERE paypal_batch_id = ?`, r.BatchHeader.PayoutBatchID).Scan(&payoutID)
		if errors.Is(err, sql.ErrNoRows) {
			return nil
		}
		if err != nil {
			return fmt.Errorf("find payout: %w", err)
		}
		if env.EventType == WhBatchDenied {
			_, err = s.applyStatus(ctx, payoutID, r.BatchHeader.PayoutBatchID, "", model.PayoutFailed, model.ViaWebhook)
			return err
		}
		// Batch webhooks carry no item status; read it (P6, read-only).
		b, _, err := s.pp.GetPayoutBatch(ctx, r.BatchHeader.PayoutBatchID)
		if err != nil {
			return paypalErr("payout status", err)
		}
		_, err = s.applyBatch(ctx, payoutID, b, model.ViaWebhook)
		return err
	case env.EventType == WhCaptureCompleted, env.EventType == WhCaptureDenied, env.EventType == WhCaptureDeclined:
		var r idResource
		if err := json.Unmarshal(env.Resource, &r); err != nil || r.ID == "" {
			return fmt.Errorf("capture resource without id")
		}
		return s.inTx(ctx, func(tx *sql.Tx) error {
			var id, policyID, status string
			var amount int64
			err := tx.QueryRowContext(ctx, `SELECT id, policy_id, status, amount_cents FROM premium_charges WHERE paypal_capture_id = ?`, r.ID).Scan(&id, &policyID, &status, &amount)
			if errors.Is(err, sql.ErrNoRows) {
				return nil
			}
			if err != nil {
				return fmt.Errorf("find charge: %w", err)
			}
			if status != model.ChargeCreated {
				return nil
			}
			if env.EventType != WhCaptureCompleted {
				_, err := tx.ExecContext(ctx, `UPDATE premium_charges SET status = ? WHERE id = ?`, model.ChargeFailed, id)
				return err
			}
			if _, err := tx.ExecContext(ctx, `UPDATE premium_charges SET status = ? WHERE id = ?`, model.ChargeCompleted, id); err != nil {
				return fmt.Errorf("complete charge: %w", err)
			}
			return appendLedger(ctx, tx, s.nowStr(), "premium", amount, policyID, id)
		})
	default:
		// Stored and verified; FourHour takes no action on this type
		// (refund events arrive with cancel and refund, a stretch item).
		return nil
	}
}
