import { useApi } from '../api/context';
import type { PolicyId } from '../api/types';
import { PageHeader } from '../components/Layout';
import { Probe } from '../components/Probe';

// K3 empty page. F5 adds key generation, the signed hash chain, the local queue, upload and the
// manual fallback. The capability lines below only report what this browser offers.
export function WitnessPage({ policyId }: { policyId: string }) {
  const api = useApi();
  const hasBattery = typeof navigator !== 'undefined' && 'getBattery' in navigator;
  const hasSubtle = typeof crypto !== 'undefined' && typeof crypto.subtle !== 'undefined';
  return (
    <>
      <PageHeader title="Witness" purpose="The phone page: logs charger state in a signed, hash-chained log for this policy." />
      <ul className="facts">
        <li>Battery Status API: {hasBattery ? 'available' : 'not available in this browser (manual buttons will be used)'}</li>
        <li>WebCrypto: {hasSubtle ? 'available' : 'not available (needs HTTPS)'}</li>
      </ul>
      <Probe
        what={`policy ${policyId}`}
        loadKey={`witness:${policyId}`}
        load={() => api.policy(policyId as PolicyId)}
        summary={(r) => `${r.devices.length} registered device(s)`}
      />
    </>
  );
}
