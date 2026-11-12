import Link from "next/link";
import { RouteStrip } from "@/components/RouteStrip";

const credentials = [
  { role: "Dispatcher", email: "dispatcher@northwind.test", note: "orders, shipments, agents" },
  { role: "Owner", email: "owner@northwind.test", note: "full tenant control" },
  { role: "Warehouse", email: "warehouse@northwind.test", note: "stock and fulfilment" },
  { role: "Delivery agent", email: "agent@northwind.test", note: "mobile route console" },
];

const entries = [
  {
    href: "/console",
    title: "Dispatch console",
    body: "Orders, reservations and shipments on one board. Confirm an order and stock is reserved in the same transaction.",
    cta: "Open console",
  },
  {
    href: "/agent",
    title: "Agent console",
    body: "A phone-sized surface for the courier: what is assigned, and one tap to advance the route.",
    cta: "Open agent view",
  },
  {
    href: "/track",
    title: "Track a shipment",
    body: "The customer-facing page. Public tracking number, live status over WebSocket, no account required.",
    cta: "Track a parcel",
  },
];

export default function LandingPage() {
  return (
    <div className="landing">
      <header className="topbar">
        <span className="wordmark">
          <span className="beacon" />
          Waypoint
        </span>
        <span className="spacer" />
        <Link className="btn btn-sm" href="/login">
          Sign in
        </Link>
      </header>

      <main className="landing-main">
        <p className="eyebrow">Logistics control tower</p>
        <h1>Every order knows where it is.</h1>
        <p className="thesis">
          Waypoint is a multi-tenant order and shipment platform. Orders reserve stock
          atomically, <strong>every state change is an event</strong>, and couriers move
          parcels along a route the customer can watch live.
        </p>

        <div className="panel" style={{ marginTop: 34 }}>
          <div className="panel-head">
            <h2>Route SHP-20261112-63A848AD</h2>
            <span className="eyebrow">Live strip</span>
          </div>
          <div className="panel-body">
            <RouteStrip status="in_transit" />
          </div>
        </div>

        <div className="entry-grid">
          {entries.map((entry) => (
            <Link className="entry" href={entry.href} key={entry.href}>
              <h3>{entry.title}</h3>
              <p>{entry.body}</p>
              <span className="go">{entry.cta} →</span>
            </Link>
          ))}
        </div>

        <div style={{ marginTop: 46 }}>
          <p className="eyebrow">Demo accounts · password Passw0rd!</p>
          <div className="credential-grid">
            {credentials.map((credential) => (
              <div className="credential" key={credential.email}>
                <div className="who">{credential.role}</div>
                <div className="email">{credential.email}</div>
                <p className="muted" style={{ margin: "6px 0 0", fontSize: 12.5 }}>
                  {credential.note}
                </p>
              </div>
            ))}
          </div>
        </div>
      </main>
    </div>
  );
}
