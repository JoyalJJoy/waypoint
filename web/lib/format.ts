import type { OrderStatus, ShipmentStatus } from "./types";

export function money(cents: number, currency = "USD"): string {
  return new Intl.NumberFormat("en-US", {
    style: "currency",
    currency,
  }).format(cents / 100);
}

export function shortDate(iso: string): string {
  return new Date(iso).toLocaleDateString("en-US", {
    month: "short",
    day: "numeric",
  });
}

export function clockTime(iso: string): string {
  return new Date(iso).toLocaleTimeString("en-US", {
    hour: "2-digit",
    minute: "2-digit",
    hour12: false,
  });
}

export function stamp(iso: string): string {
  return `${shortDate(iso)} ${clockTime(iso)}`;
}

export function relative(iso: string): string {
  const seconds = Math.round((Date.now() - new Date(iso).getTime()) / 1000);
  if (seconds < 60) return "just now";
  const minutes = Math.round(seconds / 60);
  if (minutes < 60) return `${minutes}m ago`;
  const hours = Math.round(minutes / 60);
  if (hours < 24) return `${hours}h ago`;
  const days = Math.round(hours / 24);
  return `${days}d ago`;
}

export function grams(value: number): string {
  if (value >= 1000) {
    return `${(value / 1000).toFixed(2)} kg`;
  }
  return `${value} g`;
}

const ORDER_COLORS: Record<OrderStatus, string> = {
  pending: "var(--steel)",
  confirmed: "var(--teal)",
  processing: "var(--amber)",
  completed: "var(--teal)",
  cancelled: "var(--vermilion)",
};

const SHIPMENT_COLORS: Record<ShipmentStatus, string> = {
  pending: "var(--steel)",
  assigned: "var(--steel)",
  picked_up: "var(--amber)",
  in_transit: "var(--amber)",
  out_for_delivery: "var(--amber)",
  delivered: "var(--teal)",
  failed: "var(--vermilion)",
  cancelled: "var(--vermilion)",
};

export function orderColor(status: OrderStatus): string {
  return ORDER_COLORS[status] ?? "var(--ink-dim)";
}

export function shipmentColor(status: ShipmentStatus): string {
  return SHIPMENT_COLORS[status] ?? "var(--ink-dim)";
}

export function humanStatus(status: string): string {
  return status.replace(/_/g, " ");
}
