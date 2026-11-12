"use client";

import { useState } from "react";
import { ApiError, createOrder } from "@/lib/api";
import type { Order, Product, Warehouse } from "@/lib/types";

interface Props {
  warehouses: Warehouse[];
  products: Product[];
  onCreated: (order: Order) => void;
  onCancel: () => void;
}

interface Line {
  productId: string;
  quantity: number;
}

export function NewOrderForm({ warehouses, products, onCreated, onCancel }: Props) {
  const [warehouseId, setWarehouseId] = useState(warehouses[0]?.id ?? "");
  const [name, setName] = useState("");
  const [email, setEmail] = useState("");
  const [line1, setLine1] = useState("");
  const [city, setCity] = useState("");
  const [region, setRegion] = useState("");
  const [postal, setPostal] = useState("");
  const [country, setCountry] = useState("US");
  const [notes, setNotes] = useState("");
  const [lines, setLines] = useState<Line[]>([
    { productId: products[0]?.id ?? "", quantity: 1 },
  ]);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);
  // The key is generated once per form so a retry after a network hiccup
  // replays the same order instead of creating a second one.
  const [idempotencyKey, setIdempotencyKey] = useState(() => crypto.randomUUID());

  async function submit(event: React.FormEvent) {
    event.preventDefault();
    setBusy(true);
    setError(null);
    try {
      const order = await createOrder(
        {
          warehouse_id: warehouseId,
          customer: { name, email },
          shipping_address: {
            line1,
            city,
            region,
            postal_code: postal,
            country,
          },
          notes,
          items: lines.map((line) => ({
            product_id: line.productId,
            quantity: Number(line.quantity),
          })),
        },
        idempotencyKey,
      );
      onCreated(order);
    } catch (err) {
      setError(err instanceof ApiError ? err.message : "could not create the order");
    } finally {
      setBusy(false);
    }
  }

  function updateLine(index: number, patch: Partial<Line>) {
    setLines((current) =>
      current.map((line, position) => (position === index ? { ...line, ...patch } : line)),
    );
  }

  return (
    <form className="panel" onSubmit={submit}>
      <div className="panel-head">
        <h2>New order</h2>
        <span className="eyebrow">Idempotency-Key {idempotencyKey.slice(0, 8)}…</span>
      </div>
      <div className="panel-body">
        <div className="grid-2" style={{ gap: 18 }}>
          <div>
            <label className="field">
              <span className="label">Ship from</span>
              <select value={warehouseId} onChange={(e) => setWarehouseId(e.target.value)}>
                {warehouses.map((warehouse) => (
                  <option key={warehouse.id} value={warehouse.id}>
                    {warehouse.name} ({warehouse.code})
                  </option>
                ))}
              </select>
            </label>

            <label className="field">
              <span className="label">Customer name</span>
              <input value={name} onChange={(e) => setName(e.target.value)} required minLength={2} />
            </label>

            <label className="field">
              <span className="label">Customer email</span>
              <input
                type="email"
                value={email}
                onChange={(e) => setEmail(e.target.value)}
                required
              />
            </label>

            <label className="field">
              <span className="label">Street address</span>
              <input value={line1} onChange={(e) => setLine1(e.target.value)} required />
            </label>

            <div style={{ display: "grid", gridTemplateColumns: "1fr 1fr 1fr", gap: 12 }}>
              <label className="field">
                <span className="label">City</span>
                <input value={city} onChange={(e) => setCity(e.target.value)} required />
              </label>
              <label className="field">
                <span className="label">Region</span>
                <input value={region} onChange={(e) => setRegion(e.target.value)} />
              </label>
              <label className="field">
                <span className="label">Postcode</span>
                <input value={postal} onChange={(e) => setPostal(e.target.value)} required />
              </label>
            </div>

            <label className="field">
              <span className="label">Country</span>
              <input
                value={country}
                onChange={(e) => setCountry(e.target.value)}
                required
                minLength={2}
                maxLength={2}
              />
            </label>
          </div>

          <div>
            <p className="eyebrow">Line items</p>
            {lines.map((line, index) => (
              <div
                key={index}
                style={{
                  display: "grid",
                  gridTemplateColumns: "1fr 84px auto",
                  gap: 8,
                  marginTop: 10,
                  alignItems: "end",
                }}
              >
                <label className="field" style={{ marginBottom: 0 }}>
                  <span className="label">Product</span>
                  <select
                    value={line.productId}
                    onChange={(e) => updateLine(index, { productId: e.target.value })}
                  >
                    {products.map((product) => (
                      <option key={product.id} value={product.id}>
                        {product.name} · {product.sku}
                      </option>
                    ))}
                  </select>
                </label>
                <label className="field" style={{ marginBottom: 0 }}>
                  <span className="label">Qty</span>
                  <input
                    type="number"
                    min={1}
                    max={10000}
                    value={line.quantity}
                    onChange={(e) => updateLine(index, { quantity: Number(e.target.value) })}
                  />
                </label>
                <button
                  type="button"
                  className="btn btn-sm"
                  onClick={() =>
                    setLines((current) => current.filter((_, position) => position !== index))
                  }
                  disabled={lines.length === 1}
                >
                  Remove
                </button>
              </div>
            ))}

            <button
              type="button"
              className="btn btn-sm"
              style={{ marginTop: 12 }}
              onClick={() =>
                setLines((current) => [
                  ...current,
                  { productId: products[0]?.id ?? "", quantity: 1 },
                ])
              }
            >
              Add line
            </button>

            <label className="field" style={{ marginTop: 16 }}>
              <span className="label">Notes</span>
              <input value={notes} onChange={(e) => setNotes(e.target.value)} />
            </label>
          </div>
        </div>

        {error ? (
          <p className="notice bad" style={{ marginTop: 14 }}>
            {error}
          </p>
        ) : null}

        <div className="btn-row" style={{ marginTop: 16 }}>
          <button className="btn btn-primary" type="submit" disabled={busy}>
            {busy ? <span className="spinner" /> : "Create order"}
          </button>
          <button className="btn" type="button" onClick={onCancel}>
            Cancel
          </button>
        </div>
      </div>
    </form>
  );
}
