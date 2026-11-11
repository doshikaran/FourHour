import { useApi } from '../api/context';
import { PageHeader } from '../components/Layout';
import { Probe } from '../components/Probe';
import { Link } from '../lib/router';

// K3 empty page. F2 adds the problem statement and the USDA FSIS quote, checked word for word
// against fsis.usda.gov before it is written here (CLAUDE.md, SPEC.md section 1).
export function HomePage() {
  const api = useApi();
  return (
    <>
      <PageHeader title="FourHour" purpose="Problem statement, USDA food-safety guidance with link, and a start button." />
      <p>
        <Link to="/enrol" className="button">
          Get a quote
        </Link>
      </p>
      <Probe
        what="backend health"
        loadKey="health"
        load={() => api.health()}
        summary={(h) => `status ${h.status}, PayPal environment ${h.paypal_env}, ${h.artifacts_loaded.length} artifacts loaded`}
      />
    </>
  );
}
