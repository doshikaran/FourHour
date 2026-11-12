/**
 * Live-page guide for the demo policy (CONTRACTS 1.8, GET /api/demo). Shown only when the backend
 * has a demo policy. Thresholds come from the trigger artifact and the peak from the replay stream;
 * nothing here is typed in.
 */
import { useApi } from "../api/context";
import { NotComputed, SandboxChip } from "../components/ui";
import { formatCents } from "../lib/money";
import { formatShare } from "../lib/format";
import { Link } from "../lib/router";
import { useAsync } from "../lib/useAsync";

export const DERECHO_SCENARIO_ID = "derecho-harris-2024";

export interface DemoGuideProps {
  thetaLo: number | null;
  thetaHi: number | null;
  /** Title of the derecho scenario from GET /api/replay/scenarios, if listed. */
  derechoTitle: string | null;
  /** Highest index share seen in the current replay, or null before any reading. */
  peakShare: number | null;
  /** True when the current replay is the derecho scenario. */
  derechoRunning: boolean;
  replayEnded: boolean;
}

/** Peak against the index-only line, worded from the numbers only. Exported for tests. */
export function peakSentence(
  peak: number | null,
  thetaHi: number | null,
  ended: boolean,
): string | null {
  if (peak === null || thetaHi === null) return null;
  const rel = peak >= thetaHi ? "at or above" : "below";
  return `${ended ? "This replay peaked" : "Highest so far in this replay"}: ${formatShare(peak)} of county customers, ${rel} the ${formatShare(thetaHi)} index-only line.`;
}

export function DemoGuide(props: DemoGuideProps) {
  const api = useApi();
  const demo = useAsync(() => api.demo(), "demo");
  // The guide is a helper: while loading, on error, or with no demo policy it stays out of the way.
  if (demo.status !== "ok" || demo.data.policy_id === null) return null;
  const d = demo.data;
  const lo =
    props.thetaLo === null ? (
      <NotComputed reason="Read from artifacts/trigger_params.json" />
    ) : (
      formatShare(props.thetaLo)
    );
  const hi =
    props.thetaHi === null ? (
      <NotComputed reason="Read from artifacts/trigger_params.json" />
    ) : (
      formatShare(props.thetaHi)
    );
  const peak = props.derechoRunning
    ? peakSentence(props.peakShare, props.thetaHi, props.replayEnded)
    : null;
  return (
    <section
      className="card"
      aria-labelledby="demo-guide-h"
      style={{ marginBottom: 16 }}
    >
      <div className="card-title">
        <h2 id="demo-guide-h">Run the demo</h2>
        <span className="spacer" />
        <SandboxChip />
      </div>
      <p className="small muted" style={{ marginBottom: 0 }}>
        Demo policy for {d.member_name} (simulated member),{" "}
        {formatCents(d.cover_cents)} cover, {formatCents(d.premium_cents)} a
        month.
      </p>
      <ol className="guide-steps">
        <li>
          Open the witness on this device:{" "}
          <Link to={`/witness/${d.policy_id}`}>/witness/{d.policy_id}</Link>
        </li>
        <li>
          Start the{" "}
          {props.derechoTitle ? (
            <strong>{props.derechoTitle}</strong>
          ) : (
            "derecho"
          )}{" "}
          replay using the controls.
        </li>
        <li>
          When the index crosses the lower line ({lo}), unplug the charger or
          press <strong>Power is out</strong> on the witness.
        </li>
        <li>Watch the claim and its payout status update.</li>
      </ol>
      <p className="small" style={{ marginTop: 10, marginBottom: 4 }}>
        <strong>With no witness, nothing is paid.</strong> Without a device
        confirming the outage, a payment needs the county index at or above {hi}{" "}
        for 4 hours (the index-only line). For a county-wide index that is rare:
        if the derecho index stays below it, only a witness confirming the
        outage at the premises leads to a payment.{" "}
        {peak ?? "The replay’s peak is shown here once it starts."}
      </p>
      <p className="small" style={{ margin: 0 }}>
        Every replay with a confirmed witness sends a real PayPal sandbox
        payout, drawn from a finite sandbox balance. Run it when you mean to.
      </p>
    </section>
  );
}
