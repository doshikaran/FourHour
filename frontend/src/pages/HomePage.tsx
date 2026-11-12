import { Link } from '../lib/router';
import { RealDataChip, SandboxChip, SimulatedChip } from '../components/ui';

/** USDA FSIS page the 4-hour line comes from (SPEC section 1). */
export const USDA_URL =
  'https://www.fsis.usda.gov/food-safety/safe-food-handling-and-preparation/emergencies/keep-your-food-safe-during-emergencies';

// The USDA wording is deliberately not quoted here: the FSIS page could not be fetched to check it
// word for word (it refuses automated clients). A human must check the quote before any is added.
export function HomePage() {
  return (
    <>
      <section className="hero">
        <div>
          <h1>Power out past four hours? The payout starts itself.</h1>
          <p className="lede">
            Automatic outage cover for small food sellers: food trucks, home bakers, corner grocers. No claim form.
          </p>
          <div className="row" style={{ marginTop: 24 }}>
            <Link to="/enrol" className="button big">
              Get a quote
            </Link>
            <Link to="/live" className="button big secondary">
              Watch a replay
            </Link>
          </div>
        </div>
        <aside className="card" aria-labelledby="usda-h">
          <h2 id="usda-h">Why four hours</h2>
          <p>
            The four-hour line comes from US Department of Agriculture food-safety guidance on refrigerated food during a
            power outage.
          </p>
          <p>
            <a href={USDA_URL} target="_blank" rel="noreferrer">
              Read the guidance: “Keep Your Food Safe During Emergencies”, USDA Food Safety and Inspection Service
            </a>
          </p>
        </aside>
      </section>

      <section className="grid-2" style={{ marginTop: 40 }}>
        <div className="card">
          <h2>How it works</h2>
          <ol className="steps">
            <li>Pick your county and how much stock you want covered. The pricing model shows how your premium is built.</li>
            <li>Save your PayPal wallet. The monthly premium is charged from it.</li>
            <li>Leave a laptop or phone on a charger at your premises. It keeps a signed log of when its charger loses power.</li>
            <li>
              When the county outage index stays high for four hours and your device agrees, a PayPal payout is sent
              automatically.
            </li>
          </ol>
        </div>
        <div className="card">
          <h2>What is real and what is simulated</h2>
          <ul>
            <li>
              <RealDataChip /> County outage data comes from the US Department of Energy EAGLE-I records. Replays play
              real past outages on a simulated clock.
            </li>
            <li>
              <RealDataChip /> Prices and thresholds come from models fitted on 2014–2022 and tested on 2023–2024. Each
              shows where it came from.
            </li>
            <li>
              <SimulatedChip /> Members, their losses and the timing of replayed outages are simulated.
            </li>
            <li>
              <SandboxChip /> Every premium and payout runs in the PayPal sandbox. No real money moves.
            </li>
          </ul>
          <p className="small muted">
            <Link to="/evidence">See the evidence: backtest, pricing, solvency and basis risk</Link>
          </p>
        </div>
      </section>
    </>
  );
}
