"use client";

import Link from "next/link";
import { useCallback, useEffect, useState } from "react";
import { RouteStrip } from "@/components/RouteStrip";
import { StatusChip } from "@/components/StatusChip";
import { ApiError, listShipments } from "@/lib/api";
import { relative, shipmentColor } from "@/lib/format";
import type { ShipmentSummary } from "@/lib/types";

const filters = ["", "pending", "assigned", "picked_up", "in_transit", "out_for_delivery", "delivered", "failed"];

export default function ShipmentsPage() {
  const [shipments, setShipments] = useState<ShipmentSummary[]>([]);
  const [status, setStatus] = useState("");
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState<string | null>(null);

  const load = useCallback(async () => {
    setLoading(true);
    try {
      const body = await listShipments({ status });
      setShipments(body.items);
      setError(null);
    } catch (err) {
      setError(err instanceof ApiError ? err.message : "could not load shipments");
    } finally {
      setLoading(false);
    }
  }, [status]);

  useEffect(() => {
    void load();
  }, [load]);

  return (
    <>
      <div className="page-head">
        <div>
          <p className="eyebrow">Logistics</p>
          <h1>Shipments</h1>
          <p className="lede">
            Each row is a courier route. The strip shows where the parcel sits right now;
            picking a parcel up deducts stock and moves the order into processing.
          </p>
        </div>
        <button className="btn" onClick={() => void load()}>
          Refresh
        </button>
      </div>

      <div className="panel">
        <div className="panel-head">
          <div className="pill-row">
            {filters.map((value) => (
              <button
                key={value || "all"}
                className="pill"
                data-active={status === value}
                onClick={() => setStatus(value)}
              >
                {value ? value.replace(/_/g, " ") : "All"}
              </button>
            ))}
          </div>
        </div>

        {error ? (
          <div className="panel-body">
            <p className="notice bad">{error}</p>
          </div>
        ) : null}

        {loading && shipments.length === 0 ? (
          <div className="empty">
            <span className="spinner" /> loading shipments
          </div>
        ) : shipments.length === 0 ? (
          <div className="empty">No shipments for this filter.</div>
        ) : (
          <table className="grid">
            <thead>
              <tr>
                <th>Shipment</th>
                <th>Order</th>
                <th>Customer</th>
                <th>Route</th>
                <th>Carrier</th>
                <th>Courier</th>
                <th>Status</th>
                <th>Age</th>
              </tr>
            </thead>
            <tbody>
              {shipments.map((shipment) => (
                <tr key={shipment.id}>
                  <td>
                    <Link
                      href={`/console/orders/${shipment.order_id}`}
                      className="mono"
                      style={{ fontSize: 12.5, color: "var(--amber)" }}
                    >
                      {shipment.shipment_number}
                    </Link>
                    <div className="faint mono" style={{ fontSize: 11 }}>
                      <Link href={`/track/${shipment.tracking_number}`}>{shipment.tracking_number}</Link>
                    </div>
                  </td>
                  <td className="mono" style={{ fontSize: 12.5 }}>
                    {shipment.order_number}
                  </td>
                  <td>{shipment.customer_name}</td>
                  <td style={{ width: 190 }}>
                    <RouteStrip status={shipment.status} compact />
                  </td>
                  <td>
                    {shipment.carrier}
                    <div className="faint" style={{ fontSize: 11.5 }}>
                      {shipment.service_level}
                    </div>
                  </td>
                  <td>{shipment.agent_name || <span className="faint">unassigned</span>}</td>
                  <td>
                    <StatusChip status={shipment.status} color={shipmentColor(shipment.status)} />
                  </td>
                  <td className="mono faint" style={{ fontSize: 12 }}>
                    {relative(shipment.created_at)}
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
        )}
      </div>
    </>
  );
}
