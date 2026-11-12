/**
 * The payout moment on /live. The sandbox takes about 20–25 s to confirm a payout (CONTRACTS
 * section 7), so "sending" is a visible state for that whole time, with a running count of seconds,
 * and "confirmed" replaces it with a large unmistakable card. Icons and words carry the state;
 * colour is only a third cue. The panel has a reserved height, so nothing below it moves.
 */
import { useEffect, useState } from 'react';
import { Link } from '../lib/router';
import { formatCents } from '../lib/money';
import { DecisionPill, SandboxChip, SimulatedChip, TIER_LABEL } from '../components/ui';
import type { LiveClaim } from './liveState';

function useNow(active: boolean): number {
  const [now, setNow] = useState(() => Date.now());
  useEffect(() => {
    if (!active) return;
    setNow(Date.now());
    const id = setInterval(() => setNow(Date.now()), 500);
    return () => clearInterval(id);
  }, [active]);
  return now;
}

type Stage = 'decided' | 'sending' | 'confirmed' | 'failed' | 'unclaimed' | 'declined';

export function stageOf(c: LiveClaim): Stage {
  if (c.claim.decision !== 'paid') return 'declined';
  const s = c.payout?.status;
  if (!s) return 'decided';
  if (s === 'success') return 'confirmed';
  if (s === 'failed') return 'failed';
  if (s === 'unclaimed') return 'unclaimed';
  return 'sending';
}

export function PayoutStage({ focus }: { focus: LiveClaim | null }) {
  const stage = focus ? stageOf(focus) : null;
  const now = useNow(stage === 'sending' || stage === 'decided');
  if (!focus || !stage) {
    return (
      <div className="payout-stage">
        <div className="payout-card" data-testid="payout-card">
          <p className="payout-headline">
            <span className="big-icon status-neutral" aria-hidden="true">
              ◷
            </span>
            No claim yet
          </p>
          <p className="muted">
            When the index has been above a threshold for 4 hours and the rules agree, the claim and its PayPal payout
            appear here.
          </p>
        </div>
      </div>
    );
  }
  const { claim, payout } = focus;
  const who = focus.member_name ?? claim.policy_id;
  const sentSecs = focus.payoutSeenAt ? Math.max(0, Math.floor(((focus.settledSeenAt ?? now) - focus.payoutSeenAt) / 1000)) : 0;

  return (
    <div className="payout-stage" aria-live="polite">
      <div className={`payout-card ${stage}`} data-testid="payout-card" data-stage={stage}>
        {stage === 'declined' ? (
          <>
            <p className="payout-headline">
              <span className={`big-icon ${claim.decision === 'review' ? 'status-warn' : 'status-neutral'}`} aria-hidden="true">
                {claim.decision === 'review' ? '!' : '✕'}
              </span>
              {claim.decision === 'review' ? 'No payout: flagged for review' : 'No payout: claim declined'}
            </p>
            <p style={{ marginTop: 8 }}>
              <strong>{claim.rule}.</strong> {claim.reason}
            </p>
          </>
        ) : (
          <>
            <p className="payout-headline">
              {stage === 'confirmed' ? (
                <span className="big-icon status-good" aria-hidden="true">
                  ✓
                </span>
              ) : stage === 'failed' || stage === 'unclaimed' ? (
                <span className="big-icon status-bad" aria-hidden="true">
                  !
                </span>
              ) : (
                <span className="spinner" aria-hidden="true" style={{ width: 40, height: 40, borderWidth: 5 }} />
              )}
              {stage === 'confirmed' && 'Payout confirmed'}
              {stage === 'sending' && 'Sending payout via PayPal…'}
              {stage === 'decided' && 'Claim approved. Sending to PayPal…'}
              {stage === 'failed' && 'Payout failed'}
              {stage === 'unclaimed' && 'Payout unclaimed'}
            </p>
            <p className="payout-amount num">{formatCents(claim.amount_cents)}</p>
            <p>
              to {who} <SimulatedChip what="member" /> <SandboxChip />
            </p>
            <p className="small">
              {TIER_LABEL[claim.tier]} · rule {claim.rule}
              {stage === 'sending' && payout?.status && ` · PayPal status ${payout.status} · ${sentSecs} s so far (the sandbox takes about 20–25 s)`}
              {stage === 'confirmed' &&
                ` · confirmed by ${payout?.confirmed_via ?? 'PayPal'}${focus.payoutSeenAt ? `, ${sentSecs} s after it was sent (measured in this browser)` : ''}`}
            </p>
          </>
        )}
        <ol className="payout-steps" aria-label="Payout progress">
          <Step label="Claim decided" state="done" />
          {claim.decision === 'paid' && (
            <>
              <Step label="Sent to PayPal" state={stage === 'decided' ? 'current' : 'done'} />
              <Step
                label={stage === 'failed' ? 'Failed' : stage === 'unclaimed' ? 'Unclaimed' : 'Confirmed'}
                state={stage === 'sending' ? 'current' : stage === 'decided' ? 'todo' : 'done'}
              />
            </>
          )}
          <li style={{ marginLeft: 'auto' }}>
            <Link to={`/claims/${claim.id}`}>Explain this claim</Link>
          </li>
        </ol>
      </div>
    </div>
  );
}

function Step({ label, state }: { label: string; state: 'done' | 'current' | 'todo' }) {
  return (
    <li className={state === 'todo' ? '' : state}>
      <span aria-hidden="true">{state === 'done' ? '✓' : state === 'current' ? '●' : '○'}</span>
      {label}
      <span className="visually-hidden">{state === 'done' ? ' (done)' : state === 'current' ? ' (in progress)' : ' (not yet)'}</span>
    </li>
  );
}

export function ClaimRow({ c }: { c: LiveClaim }) {
  return (
    <li>
      <DecisionPill decision={c.claim.decision} />
      <span className="num">
        <strong>{formatCents(c.claim.amount_cents)}</strong>
      </span>
      <span>
        {TIER_LABEL[c.claim.tier]} · {c.claim.rule}
      </span>
      <Link to={`/claims/${c.claim.id}`} className="small">
        Explain
      </Link>
    </li>
  );
}
