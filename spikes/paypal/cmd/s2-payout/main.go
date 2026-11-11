// Command s2-payout is spike S2: send a $1.00 PayPal payout to a sandbox
// personal account, poll the batch, then resend the same sender_batch_id and
// print exactly what PayPal returns. It prints PASS only if the batch reached
// SUCCESS.
package main

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"net/mail"
	"os"
	"os/signal"
	"syscall"
	"time"

	"fourhour/spikes/paypal/internal/spikeenv"
	"fourhour/spikes/paypal/paypal"
)

const payoutCents int64 = 100 // $1.00

const (
	exitPass   = 0
	exitFail   = 1
	exitConfig = 2
)

type deps struct {
	httpClient *http.Client
}

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	code := run(ctx, os.Args[1:], os.Stdout, os.Stderr, deps{})
	stop()
	os.Exit(code)
}

func usage(fs *flag.FlagSet, w io.Writer) {
	fmt.Fprintln(w, "usage: s2-payout [flags] <recipient-sandbox-personal-email>")
	fs.PrintDefaults()
}

func run(ctx context.Context, args []string, stdout, stderr io.Writer, d deps) int {
	fs := flag.NewFlagSet("s2-payout", flag.ContinueOnError)
	fs.SetOutput(stderr)
	envFile := fs.String("env-file", "", "path to .env (default: .env at the repo root, found by walking up to CONTRACTS.md)")
	senderBatchID := fs.String("sender-batch-id", "", "sender_batch_id to use (default: spike-s2-<time>-<random>)")
	pollEvery := fs.Duration("poll-interval", 5*time.Second, "time between GET payout polls")
	pollFor := fs.Duration("poll-timeout", 2*time.Minute, "give up polling after this long")
	fs.Usage = func() { usage(fs, stderr) }
	if err := fs.Parse(args); err != nil {
		return exitConfig
	}
	if fs.NArg() != 1 {
		usage(fs, stderr)
		return exitConfig
	}
	receiver := fs.Arg(0)
	if addr, err := mail.ParseAddress(receiver); err != nil || addr.Address != receiver {
		fmt.Fprintf(stderr, "s2-payout: %q is not a plain email address\n", receiver)
		return exitConfig
	}
	if *pollEvery <= 0 || *pollFor <= 0 {
		fmt.Fprintln(stderr, "s2-payout: -poll-interval and -poll-timeout must be positive")
		return exitConfig
	}

	cfg, err := loadConfig(*envFile)
	if err != nil {
		fmt.Fprintf(stderr, "s2-payout: %v\n", err)
		return exitConfig
	}
	p := func(format string, a ...any) { fmt.Fprintf(stdout, format+"\n", a...) }
	p("S2 payout. Sandbox: %s. Env file: %s", cfg.BaseURL, cfg.EnvFile)

	client, err := paypal.NewClient(paypal.Config{
		BaseURL: cfg.BaseURL, ClientID: cfg.ClientID, ClientSecret: cfg.ClientSecret,
		HTTPClient: d.httpClient,
		OnResponse: func(ri paypal.ResponseInfo) {
			p("  <- %s %s HTTP %d paypal-debug-id=%s", ri.Method, ri.Path, ri.StatusCode, orNone(ri.DebugID))
		},
	})
	if err != nil {
		fmt.Fprintf(stderr, "s2-payout: %v\n", err)
		return exitConfig
	}

	fail := func(step string, err error) int {
		p("ERROR at %s: %v", step, err)
		if ae, ok := paypal.AsAPIError(err); ok {
			p("  HTTP status: %d", ae.StatusCode)
			p("  paypal-debug-id: %s", orNone(ae.DebugID))
			p("  full error body:\n%s", string(ae.Body))
		}
		p("RESULT: FAIL (%s)", step)
		return exitFail
	}

	if *senderBatchID == "" {
		*senderBatchID = "spike-s2-" + newRunID()
	}
	amount, err := paypal.PayoutUSD(payoutCents)
	if err != nil {
		return fail("build payout", err)
	}
	req := paypal.CreatePayoutRequest{
		SenderBatchHeader: paypal.SenderBatchHeader{
			SenderBatchID: *senderBatchID,
			RecipientType: paypal.RecipientTypeEmail,
			EmailSubject:  "FourHour sandbox spike S2",
		},
		Items: []paypal.PayoutItemRequest{{
			RecipientType:   paypal.RecipientTypeEmail,
			Amount:          amount,
			Note:            "FourHour sandbox spike S2",
			Receiver:        receiver,
			SenderItemID:    "spike-s2-item-1",
			RecipientWallet: paypal.RecipientWalletPayPal,
		}},
	}

	// P5.
	p("\n[P5] POST /v1/payments/payouts  sender_batch_id=%s  %s %s to %s", *senderBatchID, amount.Value, amount.Currency, receiver)
	created, res, err := client.CreatePayout(ctx, req)
	if err != nil {
		return fail("create payout", err)
	}
	batchID := created.BatchHeader.PayoutBatchID
	p("  payout_batch_id=%s batch_status=%s (HTTP %d)", batchID, created.BatchHeader.BatchStatus, res.StatusCode)
	if batchID == "" {
		return fail("create payout", errors.New("response has no payout_batch_id"))
	}

	// P6 polling until the batch is no longer PENDING/PROCESSING or time runs out.
	p("\n[P6] polling GET /v1/payments/payouts/%s every %s for up to %s", batchID, *pollEvery, *pollFor)
	batch, err := poll(ctx, client, batchID, *pollEvery, *pollFor, p)
	if err != nil {
		return fail("poll payout", err)
	}
	p("  final batch_status=%s", batch.BatchHeader.BatchStatus)
	itemOK := true
	for _, it := range batch.Items {
		p("  item payout_item_id=%s transaction_status=%s transaction_id=%s receiver=%s amount=%s %s",
			it.PayoutItemID, orNone(it.TransactionStatus), orNone(it.TransactionID), it.PayoutItem.Receiver, it.PayoutItem.Amount.Value, it.PayoutItem.Amount.Currency)
		if it.Errors != nil {
			p("    item error: name=%s message=%s debug_id=%s", it.Errors.Name, it.Errors.Message, it.Errors.DebugID)
		}
		if it.TransactionStatus != paypal.TransactionStatusSuccess {
			itemOK = false
		}
	}

	// P5 again with the same sender_batch_id: print exactly what comes back.
	p("\n[P5 repeat] POST /v1/payments/payouts  same sender_batch_id=%s", *senderBatchID)
	_, res2, err2 := client.CreatePayout(ctx, req)
	if ae, ok := paypal.AsAPIError(err2); ok {
		p("  HTTP status: %d", ae.StatusCode)
		p("  paypal-debug-id: %s", orNone(ae.DebugID))
		p("  full response body:\n%s", string(ae.Body))
	} else if err2 != nil {
		p("  repeat call failed before a response: %v", err2)
	} else {
		p("  HTTP status: %d", res2.StatusCode)
		p("  paypal-debug-id: %s", orNone(res2.DebugID))
		p("  full response body:\n%s", string(res2.Body))
		p("  WARNING: PayPal accepted a repeated sender_batch_id; check the sandbox for a second payout")
	}

	if batch.BatchHeader.BatchStatus == paypal.BatchStatusSuccess {
		if !itemOK {
			p("\nWARNING: batch is SUCCESS but an item is not SUCCESS (UNCLAIMED usually means the email has no sandbox account)")
		}
		p("\nRESULT: PASS payout_batch_id=%s batch_status=SUCCESS. Now check the recipient's sandbox balance went up by $1.00.", batchID)
		return exitPass
	}
	p("\nRESULT: FAIL payout_batch_id=%s batch_status=%s (PASS needs SUCCESS)", batchID, batch.BatchHeader.BatchStatus)
	return exitFail
}

// poll reads the batch until it leaves PENDING and PROCESSING, or the timeout
// passes. It returns the last batch read.
func poll(ctx context.Context, c *paypal.Client, batchID string, every, limit time.Duration, p func(string, ...any)) (paypal.PayoutBatch, error) {
	deadline := time.Now().Add(limit)
	for {
		b, _, err := c.GetPayoutBatch(ctx, batchID)
		if err != nil {
			return b, err
		}
		status := b.BatchHeader.BatchStatus
		p("  batch_status=%s", status)
		if status != paypal.BatchStatusPending && status != paypal.BatchStatusProcessing {
			return b, nil
		}
		if time.Now().Add(every).After(deadline) {
			p("  still %s after %s; giving up", status, limit)
			return b, nil
		}
		select {
		case <-time.After(every):
		case <-ctx.Done():
			return b, ctx.Err()
		}
	}
}

func loadConfig(envFile string) (spikeenv.Config, error) {
	if envFile == "" {
		wd, err := os.Getwd()
		if err != nil {
			return spikeenv.Config{}, fmt.Errorf("working directory: %w", err)
		}
		envFile, err = spikeenv.FindEnvFile(wd)
		if err != nil {
			return spikeenv.Config{}, err
		}
	}
	return spikeenv.Load(envFile)
}

func newRunID() string {
	b := make([]byte, 4)
	if _, err := rand.Read(b); err != nil {
		return time.Now().UTC().Format("20060102T150405")
	}
	return time.Now().UTC().Format("20060102T150405") + "-" + hex.EncodeToString(b)
}

func orNone(s string) string {
	if s == "" {
		return "(none)"
	}
	return s
}
