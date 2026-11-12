"use client";

import { useCallback, useEffect, useState } from "react";
import { useRouter } from "next/navigation";
import { RouteStrip } from "@/components/RouteStrip";
import { SignInForm } from "@/components/SignInForm";
import { StatusChip } from "@/components/StatusChip";
import { ApiError, listShipments, logout, transitionShipment } from "@/lib/api";
import { shipmentColor } from "@/lib/format";
import { useSession } from "@/lib/useSession";
import type { ShipmentStatus, ShipmentSummary } from "@/lib/types";

const NEXT_STEPS: Partial<Record<ShipmentStatus, { status: string; label: string }[]>> = {
  assigned: [{ status: "picked_up", label: "Collected from the warehouse" }],
  picked_up: [{ status: "in_transit", label: "On the road" }],
  in_transit: [{ status: "out_for_delivery", label: "Out for delivery" }],
  out_for_delivery: [
    { status: "delivered", label: "Delivered" },
    { status: "failed", label: "Attempt failed" },
  ],
  failed: [{ status: "out_for_delivery", label: "Try again" }],
};

export default function AgentPage() {
  const { session, loading, reload } = useSession();
  const router = useRouter();
  const [shipments, setShipments] = useState<ShipmentSummary[]>([]);
  const [busyId, setBusyId] = useState<string | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [signingOut, setSigningOut] = useState(false);

  async function signOut() {
    setSigningOut(true);
    try {
      await logout();
    } catch {
      // the cookie is cleared regardless
    }
    router.push("/");
    router.refresh();
  }

  const load = useCallback(async () => {
    if (!session) return;
    try {
      const body = await listShipments({});
      setShipments(body.items);
    } catch (err) {
      setError(err instanceof ApiError ? err.message : "could not load the route");
    }
  }, [session]);

  useEffect(() => {
    void load();
  }, [load]);

  if (loading) {
    return (
      <div className="agent-shell">
        <div className="agent-main">
          <span className="spinner" />
        </div>
      </div>
    );
  }

  if (!session) {
    return (
      <div className="login-wrap">
        <SignInForm
          nextPath="/agent"
          title="Courier console"
          lede="Sign in with a delivery agent account to open your route."
          onSignedIn={reload}
        />
      </div>
    );
  }

  const mine = shipments.filter(
    (shipment) =>
      shipment.agent_id === session.user.id &&
      shipment.status !== "delivered" &&
      shipment.status !== "cancelled",
  );
  const done = shipments.filter(
    (shipment) =>
      shipment.agent_id === session.user.id &&
      (shipment.status === "delivered" || shipment.status === "cancelled"),
  );

  async function advance(shipment: ShipmentSummary, status: string) {
    setBusyId(shipment.id);
    setError(null);
    try {
      await transitionShipment(shipment.id, { status, note: `marked ${status} from the route` });
      await load();
    } catch (err) {
      setError(err instanceof ApiError ? err.message : "could not update the shipment");
    } finally {
      setBusyId(null);
    }
  }

  return (
    <div className="agent-shell">
      <header className="topbar">
        <span className="wordmark">
          <span className="beacon" />
          Waypoint
        </span>
        <span className="spacer" />
        <span className="mono faint" style={{ fontSize: 12 }}>
          {session.user.name}
        </span>
        <button className="btn btn-sm" onClick={signOut} disabled={signingOut}>
          Sign out
        </button>
      </header>

      <main className="agent-main">
        <div className="page-head">
          <div>
            <p className="eyebrow">Your route</p>
            <h1>{mine.length} to deliver</h1>
          </div>
        </div>

        {error ? (
          <p className="notice bad" style={{ marginBottom: 16 }}>
            {error}
          </p>
        ) : null}

        {mine.length === 0 ? (
          <div className="stop-card">
            <p className="muted" style={{ margin: 0 }}>
              Nothing assigned right now. New parcels appear here the moment a dispatcher
              puts them on your route.
            </p>
          </div>
        ) : null}

        {mine.map((shipment) => {
          const steps = NEXT_STEPS[shipment.status] ?? [];
          return (
            <div className="stop-card" key={shipment.id}>
              <div className="stop-head">
                <div>
                  <div className="order-ref">{shipment.order_number}</div>
                  <div style={{ fontSize: 17, fontWeight: 500, marginTop: 2 }}>
                    {shipment.customer_name}
                  </div>
                </div>
                <StatusChip status={shipment.status} color={shipmentColor(shipment.status)} />
              </div>

              <div style={{ marginTop: 16 }}>
                <RouteStrip status={shipment.status} />
              </div>

              <div className="mono faint" style={{ fontSize: 12, marginTop: 12 }}>
                {shipment.carrier.toUpperCase()} · {shipment.service_level.toUpperCase()} ·{" "}
                {shipment.tracking_number}
              </div>

              {steps.map((step, index) => (
                <button
                  key={step.status}
                  className={`btn big-action ${index === 0 ? "primary" : ""} ${step.status === "failed" ? "danger" : ""}`}
                  disabled={busyId === shipment.id}
                  onClick={() => void advance(shipment, step.status)}
                >
                  {step.label}
                </button>
              ))}
            </div>
          );
        })}

        {done.length > 0 ? (
          <div className="panel" style={{ marginTop: 22 }}>
            <div className="panel-head">
              <h2>Finished</h2>
              <span className="eyebrow">{done.length}</span>
            </div>
            <ul className="timeline" style={{ padding: "4px 16px 14px" }}>
              {done.map((shipment) => (
                <li key={shipment.id}>
                  <span className="when">{shipment.status}</span>
                  <span>{shipment.order_number}</span>
                </li>
              ))}
            </ul>
          </div>
        ) : null}
      </main>
    </div>
  );
}
