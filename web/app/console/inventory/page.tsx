"use client";

import { useCallback, useEffect, useState } from "react";
import { ApiError, listMovements, listStock } from "@/lib/api";
import { relative, stamp } from "@/lib/format";
import type { Movement, StockLevel } from "@/lib/types";

const KIND_COLORS: Record<string, string> = {
  receipt: "var(--teal)",
  reservation: "var(--steel)",
  release: "var(--ink-dim)",
  pick: "var(--amber)",
  adjustment: "var(--vermilion)",
  return: "var(--steel)",
};

export default function InventoryPage() {
  const [stock, setStock] = useState<StockLevel[]>([]);
  const [movements, setMovements] = useState<Movement[]>([]);
  const [error, setError] = useState<string | null>(null);
  const [loading, setLoading] = useState(true);

  const load = useCallback(async () => {
    setLoading(true);
    try {
      const [levels, ledger] = await Promise.all([listStock(), listMovements()]);
      setStock(levels);
      setMovements(ledger);
      setError(null);
    } catch (err) {
      setError(err instanceof ApiError ? err.message : "could not load inventory");
    } finally {
      setLoading(false);
    }
  }, []);

  useEffect(() => {
    void load();
  }, [load]);

  const totals = stock.reduce(
    (acc, level) => ({
      onHand: acc.onHand + level.on_hand,
      reserved: acc.reserved + level.reserved,
      available: acc.available + level.available,
    }),
    { onHand: 0, reserved: 0, available: 0 },
  );

  return (
    <>
      <div className="page-head">
        <div>
          <p className="eyebrow">Warehouse</p>
          <h1>Inventory</h1>
          <p className="lede">
            Stock is a ledger, not a counter. Reservations hold units for a confirmed
            order, and a pick deducts them for good.
          </p>
        </div>
        <button className="btn" onClick={() => void load()}>
          Refresh
        </button>
      </div>

      <div className="stat-row" style={{ marginBottom: 18 }}>
        <div className="stat">
          <p className="eyebrow">SKUs</p>
          <div className="value">{stock.length}</div>
        </div>
        <div className="stat">
          <p className="eyebrow">On hand</p>
          <div className="value">{totals.onHand}</div>
        </div>
        <div className="stat">
          <p className="eyebrow">Reserved</p>
          <div className="value accent">{totals.reserved}</div>
        </div>
        <div className="stat">
          <p className="eyebrow">Available</p>
          <div className="value ok">{totals.available}</div>
        </div>
      </div>

      {error ? (
        <p className="notice bad" style={{ marginBottom: 18 }}>
          {error}
        </p>
      ) : null}

      <div className="panel">
        <div className="panel-head">
          <h2>Stock levels</h2>
          <span className="eyebrow">{loading ? "loading" : "live"}</span>
        </div>
        <table className="grid">
          <thead>
            <tr>
              <th>Warehouse</th>
              <th>Product</th>
              <th className="num">On hand</th>
              <th className="num">Reserved</th>
              <th className="num">Available</th>
              <th>Updated</th>
            </tr>
          </thead>
          <tbody>
            {stock.map((level) => (
              <tr key={`${level.warehouse_id}-${level.product_id}`}>
                <td>{level.warehouse_name}</td>
                <td>
                  <div>{level.product_name}</div>
                  <div className="faint mono" style={{ fontSize: 11.5 }}>
                    {level.sku}
                  </div>
                </td>
                <td className="mono num">{level.on_hand}</td>
                <td className="mono num" style={{ color: level.reserved ? "var(--amber)" : undefined }}>
                  {level.reserved}
                </td>
                <td className="mono num">{level.available}</td>
                <td className="mono faint" style={{ fontSize: 12 }}>
                  {relative(level.updated_at)}
                </td>
              </tr>
            ))}
          </tbody>
        </table>
      </div>

      <div className="panel">
        <div className="panel-head">
          <h2>Stock ledger</h2>
          <span className="eyebrow">append only</span>
        </div>
        <table className="grid">
          <thead>
            <tr>
              <th>When</th>
              <th>Kind</th>
              <th>Product</th>
              <th className="num">Qty</th>
              <th>Reason</th>
            </tr>
          </thead>
          <tbody>
            {movements.map((movement) => (
              <tr key={movement.id}>
                <td className="mono faint" style={{ fontSize: 12 }}>
                  {stamp(movement.created_at)}
                </td>
                <td>
                  <span className="chip" style={{ ["--chip" as string]: KIND_COLORS[movement.kind] ?? "var(--ink-dim)" }}>
                    <span className="dot" />
                    {movement.kind}
                  </span>
                </td>
                <td>
                  {movement.product_name}
                  <div className="faint mono" style={{ fontSize: 11 }}>
                    {movement.warehouse_name}
                  </div>
                </td>
                <td className="mono num" style={{ color: movement.quantity < 0 ? "var(--vermilion)" : "var(--teal)" }}>
                  {movement.quantity > 0 ? `+${movement.quantity}` : movement.quantity}
                </td>
                <td className="muted">{movement.reason}</td>
              </tr>
            ))}
          </tbody>
        </table>
      </div>
    </>
  );
}
