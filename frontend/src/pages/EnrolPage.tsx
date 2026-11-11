import { useApi } from '../api/context';
import { PageHeader } from '../components/Layout';
import { Probe } from '../components/Probe';
import { useLocation } from '../lib/router';

export function EnrolPage() {
  const api = useApi();
  const { search } = useLocation();
  return (
    <>
      <PageHeader
        title="Enrol"
        purpose="County and cover picker, quote with breakdown, member details, hand-off to PayPal."
      />
      {search.get('cancelled') === '1' && (
        <p role="status" className="notice">
          You left PayPal before saving your wallet. Nothing was charged.
        </p>
      )}
      <Probe
        what="counties"
        loadKey="counties"
        load={() => api.counties()}
        summary={(r) => `${r.counties.length} counties (${r.counties.map((c) => c.name).join(', ')})`}
      />
    </>
  );
}
