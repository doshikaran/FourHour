/**
 * F1 shared states and labels. Every page uses these, so loading, empty, error, "not computed",
 * "simulated", "real data", "self-reported" and "test cover" look and read the same everywhere.
 */
import type { ReactNode } from 'react';
import { ApiError } from '../api/client';
import { useApi } from '../api/context';
import type {
  ClaimDecision,
  PayoutStatus,
  Policy,
  PolicyStatus,
  PremiumChargeStatus,
  Provenance,
  ThetaHiStatus,
  WitnessKind,
} from '../api/types';
import { TEST_COVER_CENTS, TEST_COVER_MODEL_VERSION } from '../api/types';
import type { AsyncState } from '../lib/useAsync';

// ---------- loading / error / empty / not computed ----------

export function Loading({ what }: { what: string }) {
  return (
    <div className="state state-loading" role="status">
      <span className="spinner" aria-hidden="true" />
      <span>Loading {what}…</span>
    </div>
  );
}

/** What the user can do about an error, by code. */
export function errorAdvice(e: Error): string {
  if (!(e instanceof ApiError)) return 'Reload the page. If it happens again, the details below help a developer find it.';
  switch (e.code) {
    case 'network_error':
      return 'The backend did not answer. Check that it is running and that this device is online, then try again.';
    case 'not_configured':
      return 'Set VITE_API_BASE_URL in .env, or run with VITE_USE_MOCK=true.';
    case 'artifact_missing':
      return 'The model output for this has not been generated yet. It appears here once ml/ writes it.';
    case 'pricing_not_computed':
      return 'The pricing model has not produced every input this quote needs yet, so no price is shown.';
    case 'database_unavailable':
      return 'The backend cannot reach its database. Try again in a moment.';
    default:
      if (e.status >= 500) return 'The backend hit an error. Try again; if it repeats, check the backend log.';
      if (e.status === 404) return 'Check the address. The record may not exist on this backend.';
      return 'Check the details and try again.';
  }
}

export function ErrorState({ what, error, onRetry }: { what: string; error: Error; onRetry?: () => void }) {
  const api = error instanceof ApiError ? error : null;
  return (
    <div className="state state-error" role="alert">
      <p>
        <strong>Could not load {what}.</strong> {error.message}
      </p>
      <p>{errorAdvice(error)}</p>
      {api && (
        <p className="detail">
          {api.endpoint} · {api.status || 'no response'} · {api.code}
          {api.paypalDebugId ? ` · PayPal debug ID ${api.paypalDebugId}` : ''}
        </p>
      )}
      {onRetry && (
        <button type="button" className="secondary" onClick={onRetry}>
          Try again
        </button>
      )}
    </div>
  );
}

export function Empty({ children }: { children: ReactNode }) {
  return <div className="state state-empty">{children}</div>;
}

/** The only way the UI shows a value that is missing. Never a placeholder number. */
export function NotComputed({ reason }: { reason?: string }) {
  return (
    <span className="not-computed" title={reason ?? 'Not computed yet'}>
      not computed
    </span>
  );
}

/** Renders an AsyncState: loading, error (with retry) or the data. */
export function AsyncView<T>({
  state,
  what,
  children,
}: {
  state: AsyncState<T> & { reload: () => void };
  what: string;
  children: (data: T) => ReactNode;
}) {
  if (state.status === 'loading') return <Loading what={what} />;
  if (state.status === 'error') return <ErrorState what={what} error={state.error} onRetry={state.reload} />;
  return <>{children(state.data)}</>;
}

// ---------- data labels ----------

export function SimulatedChip({ what }: { what?: string }) {
  return (
    <span className="chip chip-sim" title={what ? `${what} is simulated, not a real person or event` : 'Simulated'}>
      Simulated{what ? ` ${what}` : ''}
    </span>
  );
}

/**
 * "Real data" for real data and model results. In mock mode every value is a fixture, so this
 * says "Fixture" instead: the label can never vouch for mock data.
 */
export function RealDataChip({ what }: { what?: string }) {
  const api = useApi();
  if (api.mode === 'mock') {
    return (
      <span className="chip chip-fixture" title="Served by the frontend mock. Not real data.">
        Fixture
      </span>
    );
  }
  return <span className="chip chip-real">Real data{what ? `: ${what}` : ''}</span>;
}

export function SelfReportedChip() {
  return (
    <span className="chip chip-self" title="Entered by the member with a button, not sensed from the charger">
      Self-reported
    </span>
  );
}

export function TestCoverChip() {
  return (
    <span className="chip chip-test" title="Seeded sandbox test policy: $1.00 cover, $1.00 premium, not priced by the model">
      Test cover
    </span>
  );
}

export function FixtureChip() {
  return <span className="chip chip-fixture">Fixture</span>;
}

export function SandboxChip() {
  return (
    <span className="chip chip-sandbox" title="PayPal sandbox. No real money moves.">
      PayPal sandbox
    </span>
  );
}

export function isTestCover(p: Pick<Policy, 'cover_cents' | 'model_version'>): boolean {
  return p.model_version === TEST_COVER_MODEL_VERSION || p.cover_cents === TEST_COVER_CENTS;
}

export function isSelfReported(kind: WitnessKind): boolean {
  return kind === 'attest_lost' || kind === 'attest_restored';
}

/** Provenance block from CONTRACTS section 8, behind a disclosure. */
export function ProvenanceDetails({ provenance, label = 'Where this comes from' }: { provenance: Provenance; label?: string }) {
  return (
    <details className="provenance">
      <summary>
        <RealDataChip /> {label}
      </summary>
      <dl>
        <dt>Generated</dt>
        <dd>{provenance.generated_at}</dd>
        <dt>Code</dt>
        <dd>
          <code>{provenance.code}</code> at commit <code>{provenance.git_commit}</code>
        </dd>
        <dt>Fitted on</dt>
        <dd>
          {provenance.train_years[0]}–{provenance.train_years[1]}
        </dd>
        <dt>Evaluated on</dt>
        <dd>
          {provenance.test_years[0]}–{provenance.test_years[1]}
        </dd>
        <dt>Seed</dt>
        <dd>{provenance.seed}</dd>
        {provenance.data_sources.map((d) => (
          <FragmentSource key={d.name + d.sha256} name={d.name} url={d.url} rows={d.rows_used} sha={d.sha256} />
        ))}
        {provenance.notes && (
          <>
            <dt>Notes</dt>
            <dd>{provenance.notes}</dd>
          </>
        )}
      </dl>
    </details>
  );
}

function FragmentSource({ name, url, rows, sha }: { name: string; url: string; rows: number; sha: string }) {
  return (
    <>
      <dt>Data</dt>
      <dd>
        <a href={url} target="_blank" rel="noreferrer">
          {name}
        </a>
        , {rows.toLocaleString('en-US')} rows, sha256 <code>{sha.slice(0, 12)}…</code>
      </dd>
    </>
  );
}

// ---------- status pills: icon + word, colour is only a third cue ----------

type Tone = 'good' | 'warn' | 'bad' | 'info' | 'neutral';

export function StatusPill({ tone, icon, children }: { tone: Tone; icon: string; children: ReactNode }) {
  return (
    <span className={`status status-${tone}`}>
      <span className="status-icon" aria-hidden="true">
        {icon}
      </span>
      {children}
    </span>
  );
}

export function PayoutStatusPill({ status }: { status: PayoutStatus | null }) {
  switch (status) {
    case null:
      return <StatusPill tone="neutral" icon="–">No payout</StatusPill>;
    case 'created':
    case 'pending':
      return (
        <StatusPill tone="info" icon="…">
          Sending
        </StatusPill>
      );
    case 'success':
      return (
        <StatusPill tone="good" icon="✓">
          Confirmed
        </StatusPill>
      );
    case 'failed':
      return (
        <StatusPill tone="bad" icon="✕">
          Failed
        </StatusPill>
      );
    case 'unclaimed':
      return (
        <StatusPill tone="warn" icon="!">
          Unclaimed
        </StatusPill>
      );
  }
}

export function DecisionPill({ decision }: { decision: ClaimDecision }) {
  switch (decision) {
    case 'paid':
      return (
        <StatusPill tone="good" icon="✓">
          Paid
        </StatusPill>
      );
    case 'declined':
      return (
        <StatusPill tone="neutral" icon="✕">
          Declined
        </StatusPill>
      );
    case 'review':
      return (
        <StatusPill tone="warn" icon="!">
          Flagged for review
        </StatusPill>
      );
  }
}

export function PolicyStatusPill({ status }: { status: PolicyStatus }) {
  switch (status) {
    case 'active':
      return (
        <StatusPill tone="good" icon="✓">
          Active
        </StatusPill>
      );
    case 'pending_vault':
      return (
        <StatusPill tone="warn" icon="…">
          Waiting for PayPal
        </StatusPill>
      );
    case 'cancelled':
      return (
        <StatusPill tone="neutral" icon="✕">
          Cancelled
        </StatusPill>
      );
  }
}

export function ChargeStatusPill({ status }: { status: PremiumChargeStatus }) {
  switch (status) {
    case 'completed':
      return (
        <StatusPill tone="good" icon="✓">
          Completed
        </StatusPill>
      );
    case 'created':
      return (
        <StatusPill tone="info" icon="…">
          In progress
        </StatusPill>
      );
    case 'failed':
      return (
        <StatusPill tone="bad" icon="✕">
          Failed
        </StatusPill>
      );
    case 'refunded':
      return (
        <StatusPill tone="neutral" icon="↩">
          Refunded
        </StatusPill>
      );
  }
}

/**
 * How to describe theta_hi (CONTRACTS 8.1, 1.7): at the grid edge it is never called an optimum.
 * `short` goes on the chart line; `long` goes next to it.
 */
export function thetaHiWording(status: ThetaHiStatus | null | undefined): { short: string; long: string } {
  if (status === 'grid_edge_no_interior_optimum') {
    return {
      short: 'grid edge',
      long: 'The strong threshold is the edge of the search grid: the search found no interior optimum for it, and the objective is nearly flat near the edge. Read it as "index-only payment only in extreme events", not as a fitted value.',
    };
  }
  return { short: '', long: 'The strong threshold was chosen by the trigger-design search on 2014–2022 data.' };
}

export const TIER_LABEL: Record<'t4' | 't24', string> = {
  t4: '4-hour tier',
  t24: '24-hour tier (not offered)',
};

/** Rule meanings, from SPEC section 4. Text only; no numbers. */
export const RULE_TEXT: Record<'R1' | 'R2' | 'R3' | 'R4' | 'R5', string> = {
  R1: 'Index at or above the strong threshold for 4 hours, and the witness confirms 4 hours overlapping it. Pays the full schedule.',
  R2: 'Index at or above the strong threshold for 4 hours, and no witness device or no log. Pays 50% of the schedule.',
  R3: 'Index at or above the lenient threshold (but below the strong one) for 4 hours, and the witness confirms 4 hours overlapping it. Pays the full schedule.',
  R4: 'Index at or above the strong threshold for 4 hours, but the witness log shows power stayed on. No payout.',
  R5: 'The witness reports an outage while the index stays below the lenient threshold. No payout; flagged for review.',
};
