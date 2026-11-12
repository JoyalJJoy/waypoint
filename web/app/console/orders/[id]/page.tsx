"use client";

import Link from "next/link";
import { useParams } from "next/navigation";
import { useCallback, useEffect, useState } from "react";
import { ShipmentPanel } from "@/components/ShipmentPanel";
import { StatusChip } from "@/components/StatusChip";
import {
  ApiError,
  cancelOrder,
  confirmOrder,
  createShipment,
  getOrder,
  getShipment,
  listAgents,
  listShipments,
} from "@/lib/api";
import { money, orderColor, stamp } from "@/lib/format";
import type { Agent, Order, Shipment } from "@/lib/types";

export default function OrderDetailPage() {
  const params = useParams<{ id: string }>();
  const orderId = params.id;

  const [order, setOrder] = useState<Order | null>(null);
  const [shipment, setShipment] = useState<Shipment | null>(null);
  const [agents, setAgents] = useState<Agent[]>([]);
  const [error, setError] = useState<string | null>(null);
  const [busy, setBusy] = useState(false);
  const [carrier, setCarrier] = useState("UPS");
  const [serviceLevel, setServiceLevel] = useState("ground");

  const load = useCallback(async () => {
    try {
      const loaded = await getOrder(orderId);
      setOrder(loaded);
      const shipments = await listShipments({ query: loaded.order_number });
      const found = shipments.items.find((item) => item.order_id === loaded.id);
      setShipment(found ? await getShipment(found.id) : null);
      setError(null);
    } catch (err) {
      setError(err instanceof ApiError ? err.message : "could not load the order");
    }
  }, [orderId]);

  useEffect(() => {
    void load();
    listAgents()
      .then(setAgents)
      .catch(() => undefined);
  }, [load]);

  async function act(action: () => Promise<unknown>) {
    setBusy(true);
    setError(null);
    try {
      await action();
      await load();
    } catch (err) {
      setError(err instanceof ApiError ? err.message : "action failed");
    } finally {
      setBusy(false);
    }
  }

  if (!order) {
    return (
      <div>
        {error ? <p className="notice bad">{error}</p> : <span className="spinner" />}
      </div>
    );
  }

  return (
    <>
      <div className="page-head">
        <div>
          <Link href="/console" className="eyebrow" style={{ color: "var(--ink-faint)" }}>
            ← Order board
          </Link>
          <h1 className="mono" style={{ fontSize: 24, marginTop: 6 }}>
            {order.order_number}
          </h1>
          <p className="lede">
            {order.customer_name} · {order.warehouse_name} · placed {stamp(order.created_at)}
          </p>
        </div>
        <div className="btn-row" style={{ alignItems: "center" }}>
          <StatusChip status={order.status} color={orderColor(order.status)} />
          {order.status === "pending" ? (
            <button
              className="btn btn-primary"
              disabled={busy}
              onClick={() => void act(() => confirmOrder(order.id))}
            >
              Confirm & reserve
            </button>
          ) : null}
          {order.status === "pending" || order.status === "confirmed" ? (
            <button
              className="btn btn-danger"
              disabled={busy}
              onClick={() => void act(() => cancelOrder(order.id, "cancelled from the console"))}
            >
              Cancel order
            </button>
          ) : null}
        </div>
      </div>

      {error ? (
        <p className="notice bad" style={{ marginBottom: 18 }}>
          {error}
        </p>
      ) : null}

      <div className="grid-2">
        <div>
          <div className="panel">
            <div className="panel-head">
              <h2>Items</h2>
              <span className="eyebrow">{order.items.length} line</span>
            </div>
            <table className="grid">
              <thead>
                <tr>
                  <th>Product</th>
                  <th className="num">Qty</th>
                  <th className="num">Unit</th>
                  <th className="num">Total</th>
                </tr>
              </thead>
              <tbody>
                {order.items.map((item) => (
                  <tr key={item.id}>
                    <td>
                      <div>{item.name}</div>
                      <div className="faint mono" style={{ fontSize: 11.5 }}>
                        {item.sku}
                      </div>
                    </td>
                    <td className="mono num">{item.quantity}</td>
                    <td className="mono num">{money(item.unit_price_cents, order.currency)}</td>
                    <td className="mono num">{money(item.total_cents, order.currency)}</td>
                  </tr>
                ))}
              </tbody>
            </table>
            <div className="panel-body" style={{ textAlign: "right" }}>
              <span className="mono" style={{ fontSize: 18 }}>
                {money(order.total_cents, order.currency)}
              </span>
              <p className="eyebrow" style={{ marginTop: 2 }}>
                {order.currency} · no taxes or shipping
              </p>
            </div>
          </div>

          <div className="panel">
            <div className="panel-head">
              <h2>Ship to</h2>
            </div>
            <div className="panel-body">
              <dl className="kv">
                <dt>Recipient</dt>
                <dd>
                  {order.customer_name}
                  <br />
                  <span className="faint">{order.customer_email}</span>
                </dd>
                <dt>Address</dt>
                <dd>
                  {order.shipping_address.line1}
                  {order.shipping_address.line2 ? `, ${order.shipping_address.line2}` : ""}
                  <br />
                  {order.shipping_address.city}
                  {order.shipping_address.region ? `, ${order.shipping_address.region}` : ""}{" "}
                  {order.shipping_address.postal_code}
                  <br />
                  {order.shipping_address.country}
                </dd>
                <dt>Notes</dt>
                <dd>{order.notes || <span className="faint">none</span>}</dd>
              </dl>
            </div>
          </div>
        </div>

        <div>
          <div className="panel">
            <div className="panel-head">
              <h2>Lifecycle</h2>
              <span className="eyebrow">version {order.version}</span>
            </div>
            <div className="panel-body">
              <ul className="timeline">
                {order.events.map((event) => (
                  <li key={event.id}>
                    <span className="when">{stamp(event.created_at)}</span>
                    <span>
                      <strong style={{ fontWeight: 500 }}>{event.note}</strong>{" "}
                      <span className="faint mono" style={{ fontSize: 11.5 }}>
                        {event.from_status ? `${event.from_status} → ` : ""}
                        {event.to_status}
                      </span>
                    </span>
                  </li>
                ))}
              </ul>
            </div>
          </div>

          {shipment ? (
            <ShipmentPanel shipment={shipment} agents={agents} onChanged={() => void load()} />
          ) : order.status === "confirmed" ? (
            <div className="panel">
              <div className="panel-head">
                <h2>Create shipment</h2>
                <span className="eyebrow">stock is reserved</span>
              </div>
              <div className="panel-body">
                <p className="muted" style={{ marginTop: 0, fontSize: 13.5 }}>
                  The order is confirmed and its stock is held. Pick a carrier to open a
                  route and assign a courier.
                </p>
                <div className="btn-row" style={{ alignItems: "center" }}>
                  <select
                    value={carrier}
                    onChange={(event) => setCarrier(event.target.value)}
                    style={{
                      background: "var(--panel-sunken)",
                      border: "1px solid var(--line)",
                      borderRadius: 3,
                      color: "var(--ink)",
                      padding: "9px 10px",
                    }}
                  >
                    {["UPS", "FedEx", "DHL", "USPS"].map((value) => (
                      <option key={value}>{value}</option>
                    ))}
                  </select>
                  <select
                    value={serviceLevel}
                    onChange={(event) => setServiceLevel(event.target.value)}
                    style={{
                      background: "var(--panel-sunken)",
                      border: "1px solid var(--line)",
                      borderRadius: 3,
                      color: "var(--ink)",
                      padding: "9px 10px",
                    }}
                  >
                    {["ground", "expedited", "overnight", "freight"].map((value) => (
                      <option key={value}>{value}</option>
                    ))}
                  </select>
                  <button
                    className="btn btn-primary"
                    disabled={busy}
                    onClick={() =>
                      void act(() =>
                        createShipment({
                          order_id: order.id,
                          carrier,
                          service_level: serviceLevel,
                        }),
                      )
                    }
                  >
                    Open route
                  </button>
                </div>
              </div>
            </div>
          ) : null}
        </div>
      </div>
    </>
  );
}
