"use client";

import Link from "next/link";
import { useState } from "react";
import { RouteStrip } from "@/components/RouteStrip";
import { StatusChip } from "@/components/StatusChip";
import { ApiError, assignShipment, transitionShipment } from "@/lib/api";
import { grams, shipmentColor, stamp } from "@/lib/format";
import { useSession } from "@/lib/useSession";
import type { Agent, Shipment, ShipmentStatus } from "@/lib/types";

interface Props {
  shipment: Shipment;
  agents: Agent[];
  onChanged: () => void;
}

const NEXT_STEPS: Partial<Record<ShipmentStatus, { status: string; label: string }[]>> = {
  assigned: [{ status: "picked_up", label: "Mark picked up" }],
  picked_up: [{ status: "in_transit", label: "In transit" }],
  in_transit: [{ status: "out_for_delivery", label: "Out for delivery" }],
  out_for_delivery: [
    { status: "delivered", label: "Delivered" },
    { status: "failed", label: "Report failed" },
  ],
  failed: [{ status: "out_for_delivery", label: "Retry delivery" }],
};

export function ShipmentPanel({ shipment, agents, onChanged }: Props) {
  const { session } = useSession();
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [agentId, setAgentId] = useState(shipment.agent?.id ?? agents[0]?.id ?? "");

  const steps = NEXT_STEPS[shipment.status] ?? [];
  const canAssign =
    shipment.status === "pending" &&
    ["owner", "admin", "dispatcher"].includes(session?.tenant.role ?? "");

  async function run(action: () => Promise<unknown>) {
    setBusy(true);
    setError(null);
    try {
      await action();
      onChanged();
    } catch (err) {
      setError(err instanceof ApiError ? err.message : "action failed");
    } finally {
      setBusy(false);
    }
  }

  return (
    <div className="panel">
      <div className="panel-head">
        <h2>
          Shipment <span className="mono">{shipment.shipment_number}</span>
        </h2>
        <StatusChip status={shipment.status} color={shipmentColor(shipment.status)} />
      </div>

      <div className="panel-body">
        <RouteStrip status={shipment.status} />

        <dl className="kv" style={{ marginTop: 20 }}>
          <dt>Tracking</dt>
          <dd className="mono">
            <Link href={`/track/${shipment.tracking_number}`} style={{ color: "var(--amber)" }}>
              {shipment.tracking_number}
            </Link>
          </dd>
          <dt>Carrier</dt>
          <dd>
            {shipment.carrier} · {shipment.service_level}
          </dd>
          <dt>Agent</dt>
          <dd>{shipment.agent ? shipment.agent.name : <span className="faint">unassigned</span>}</dd>
          <dt>Weight</dt>
          <dd className="mono">{grams(shipment.weight_grams)}</dd>
          <dt>Destination</dt>
          <dd>
            {shipment.destination.city}
            {shipment.destination.region ? `, ${shipment.destination.region}` : ""} ·{" "}
            {shipment.destination.country}
          </dd>
          {shipment.picked_up_at ? (
            <>
              <dt>Picked up</dt>
              <dd className="mono">{stamp(shipment.picked_up_at)}</dd>
            </>
          ) : null}
          {shipment.delivered_at ? (
            <>
              <dt>Delivered</dt>
              <dd className="mono">{stamp(shipment.delivered_at)}</dd>
            </>
          ) : null}
        </dl>

        {canAssign ? (
          <div className="btn-row" style={{ marginTop: 16, alignItems: "center" }}>
            <select
              value={agentId}
              onChange={(event) => setAgentId(event.target.value)}
              style={{
                background: "var(--panel-sunken)",
                border: "1px solid var(--line)",
                borderRadius: 3,
                color: "var(--ink)",
                padding: "8px 10px",
              }}
            >
              {agents.map((agent) => (
                <option key={agent.id} value={agent.id}>
                  {agent.name}
                </option>
              ))}
            </select>
            <button
              className="btn btn-primary"
              disabled={busy || !agentId}
              onClick={() => run(() => assignShipment(shipment.id, agentId))}
            >
              Assign agent
            </button>
          </div>
        ) : null}

        {steps.length > 0 ? (
          <div className="btn-row" style={{ marginTop: 16 }}>
            {steps.map((step, index) => (
              <button
                key={step.status}
                className={`btn ${index === 0 ? "btn-primary" : ""} ${step.status === "failed" ? "btn-danger" : ""}`}
                disabled={busy}
                onClick={() =>
                  run(() =>
                    transitionShipment(shipment.id, {
                      status: step.status,
                      note: `marked ${step.status} from the console`,
                    }),
                  )
                }
              >
                {step.label}
              </button>
            ))}
          </div>
        ) : null}

        {error ? (
          <p className="notice bad" style={{ marginTop: 14 }}>
            {error}
          </p>
        ) : null}

        <ul className="timeline" style={{ marginTop: 18 }}>
          {shipment.events.map((event) => (
            <li key={event.id}>
              <span className="when">{stamp(event.created_at)}</span>
              <span>
                <strong style={{ fontWeight: 500 }}>{event.description}</strong>{" "}
                <span className="faint mono" style={{ fontSize: 11.5 }}>
                  {event.status.replace(/_/g, " ")}
                </span>
              </span>
            </li>
          ))}
        </ul>
      </div>
    </div>
  );
}
