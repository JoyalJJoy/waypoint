"use client";

import { useRouter } from "next/navigation";
import { useCallback, useEffect, useMemo, useState } from "react";
import { NewOrderForm } from "@/components/NewOrderForm";
import { StatusChip } from "@/components/StatusChip";
import { ApiError, cancelOrder, confirmOrder, listOrders, listProducts, listWarehouses } from "@/lib/api";
import { money, orderColor, relative } from "@/lib/format";
import type { Order, OrderSummary, Product, Warehouse } from "@/lib/types";

const filters: { key: string; label: string }[] = [
  { key: "", label: "All" },
  { key: "pending", label: "Pending" },
  { key: "confirmed", label: "Confirmed" },
  { key: "processing", label: "Processing" },
  { key: "completed", label: "Completed" },
  { key: "cancelled", label: "Cancelled" },
];

export default function OrderBoardPage() {
  const router = useRouter();
  const [orders, setOrders] = useState<OrderSummary[]>([]);
  const [status, setStatus] = useState("");
  const [query, setQuery] = useState("");
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState<string | null>(null);
  const [showForm, setShowForm] = useState(false);
  const [warehouses, setWarehouses] = useState<Warehouse[]>([]);
  const [products, setProducts] = useState<Product[]>([]);
  const [busyId, setBusyId] = useState<string | null>(null);

  const load = useCallback(async () => {
    setLoading(true);
    try {
      const body = await listOrders({ status, query, limit: 200 });
      setOrders(body.items);
      setError(null);
    } catch (err) {
      setError(err instanceof ApiError ? err.message : "could not load orders");
    } finally {
      setLoading(false);
    }
  }, [status, query]);

  useEffect(() => {
    void load();
  }, [load]);

  useEffect(() => {
    Promise.all([listWarehouses(), listProducts()])
      .then(([w, p]) => {
        setWarehouses(w);
        setProducts(p);
      })
      .catch(() => undefined);
  }, []);

  const counts = useMemo(() => {
    const base: Record<string, number> = {
      pending: 0,
      confirmed: 0,
      processing: 0,
      completed: 0,
      cancelled: 0,
    };
    orders.forEach((order) => {
      base[order.status] = (base[order.status] ?? 0) + 1;
    });
    return base;
  }, [orders]);

  async function act(order: OrderSummary, action: "confirm" | "cancel") {
    setBusyId(order.id);
    try {
      if (action === "confirm") {
        await confirmOrder(order.id);
      } else {
        await cancelOrder(order.id, "cancelled from the dispatch board");
      }
      await load();
    } catch (err) {
      setError(err instanceof ApiError ? err.message : "action failed");
    } finally {
      setBusyId(null);
    }
  }

  return (
    <>
      <div className="page-head">
        <div>
          <p className="eyebrow">Operations</p>
          <h1>Order board</h1>
          <p className="lede">
            Confirming an order reserves stock inside the same database transaction, so a
            confirmation can never oversell a warehouse.
          </p>
        </div>
        <div className="btn-row">
          <button className="btn" onClick={() => void load()}>
            Refresh
          </button>
          <button className="btn btn-primary" onClick={() => setShowForm((value) => !value)}>
            {showForm ? "Close form" : "New order"}
          </button>
        </div>
      </div>

      <div className="stat-row" style={{ marginBottom: 18 }}>
        <Stat label="Pending" value={counts.pending} />
        <Stat label="Confirmed" value={counts.confirmed} accent />
        <Stat label="Processing" value={counts.processing} accent />
        <Stat label="Completed" value={counts.completed} ok />
        <Stat label="Cancelled" value={counts.cancelled} bad />
      </div>

      {showForm ? (
        <div style={{ marginBottom: 18 }}>
          <NewOrderForm
            warehouses={warehouses}
            products={products}
            onCancel={() => setShowForm(false)}
            onCreated={(order: Order) => {
              setShowForm(false);
              router.push(`/console/orders/${order.id}`);
            }}
          />
        </div>
      ) : null}

      <div className="panel">
        <div className="panel-head">
          <div className="pill-row">
            {filters.map((filter) => (
              <button
                key={filter.key}
                className="pill"
                data-active={status === filter.key}
                onClick={() => setStatus(filter.key)}
              >
                {filter.label}
              </button>
            ))}
          </div>
          <form
            onSubmit={(event) => {
              event.preventDefault();
              void load();
            }}
            style={{ display: "flex", gap: 8 }}
          >
            <input
              className="mono"
              style={{
                background: "var(--panel-sunken)",
                border: "1px solid var(--line)",
                borderRadius: 3,
                color: "var(--ink)",
                padding: "7px 10px",
                fontSize: 12.5,
                width: 220,
              }}
              placeholder="order, customer…"
              value={query}
              onChange={(event) => setQuery(event.target.value)}
            />
            <button className="btn btn-sm" type="submit">
              Find
            </button>
          </form>
        </div>

        {error ? (
          <div className="panel-body">
            <p className="notice bad">{error}</p>
          </div>
        ) : null}

        {loading && orders.length === 0 ? (
          <div className="empty">
            <span className="spinner" /> loading orders
          </div>
        ) : orders.length === 0 ? (
          <div className="empty">No orders match this filter.</div>
        ) : (
          <table className="grid">
            <thead>
              <tr>
                <th>Order</th>
                <th>Customer</th>
                <th>Items</th>
                <th className="num">Total</th>
                <th>Status</th>
                <th>Age</th>
                <th />
              </tr>
            </thead>
            <tbody>
              {orders.map((order) => (
                <tr
                  key={order.id}
                  data-href
                  onClick={() => router.push(`/console/orders/${order.id}`)}
                >
                  <td className="mono" style={{ fontSize: 12.5 }}>
                    {order.order_number}
                  </td>
                  <td>
                    <div>{order.customer_name}</div>
                    <div className="faint" style={{ fontSize: 12 }}>
                      {order.customer_email}
                    </div>
                  </td>
                  <td className="mono num">{order.item_count}</td>
                  <td className="mono num">{money(order.total_cents, order.currency)}</td>
                  <td>
                    <StatusChip status={order.status} color={orderColor(order.status)} />
                  </td>
                  <td className="mono faint" style={{ fontSize: 12 }}>
                    {relative(order.created_at)}
                  </td>
                  <td onClick={(event) => event.stopPropagation()}>
                    <div className="btn-row">
                      {order.status === "pending" ? (
                        <button
                          className="btn btn-sm btn-primary"
                          disabled={busyId === order.id}
                          onClick={() => void act(order, "confirm")}
                        >
                          Confirm
                        </button>
                      ) : null}
                      {order.status === "pending" || order.status === "confirmed" ? (
                        <button
                          className="btn btn-sm btn-danger"
                          disabled={busyId === order.id}
                          onClick={() => void act(order, "cancel")}
                        >
                          Cancel
                        </button>
                      ) : null}
                    </div>
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

function Stat({
  label,
  value,
  accent,
  ok,
  bad,
}: {
  label: string;
  value: number;
  accent?: boolean;
  ok?: boolean;
  bad?: boolean;
}) {
  return (
    <div className="stat">
      <p className="eyebrow">{label}</p>
      <div className={`value ${accent ? "accent" : ""} ${ok ? "ok" : ""} ${bad ? "bad" : ""}`}>
        {value}
      </div>
    </div>
  );
}
