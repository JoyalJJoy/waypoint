import type {
  Agent,
  Movement,
  Order,
  OrderSummary,
  Product,
  PublicTracking,
  Session,
  Shipment,
  ShipmentSummary,
  StockLevel,
  Warehouse,
} from "./types";

export const API_URL =
  process.env.NEXT_PUBLIC_API_URL ?? "http://localhost:8080";

export class ApiError extends Error {
  status: number;

  constructor(message: string, status: number) {
    super(message);
    this.status = status;
  }
}

async function request<T>(path: string, init: RequestInit = {}): Promise<T> {
  const headers = new Headers(init.headers);
  if (init.body) {
    headers.set("Content-Type", "application/json");
  }

  const response = await fetch(`${API_URL}${path}`, {
    ...init,
    headers,
    // The API authenticates with an HttpOnly cookie, so credentials must be
    // included on every call.
    credentials: "include",
    cache: "no-store",
  });

  if (!response.ok) {
    let detail = response.statusText;
    try {
      const body = await response.json();
      detail = body.detail ?? body.title ?? detail;
    } catch {
      // keep the status text
    }
    throw new ApiError(detail, response.status);
  }

  if (response.status === 204) {
    return undefined as T;
  }
  return (await response.json()) as T;
}

export const api = {
  get: <T,>(path: string) => request<T>(path),
  post: <T,>(path: string, body?: unknown, init?: RequestInit) =>
    request<T>(path, {
      ...init,
      method: "POST",
      body: body === undefined ? undefined : JSON.stringify(body),
    }),
};

/* ------------------------------------------------------------- auth ---- */

export async function login(email: string, password: string): Promise<Session> {
  return api.post<Session>("/v1/auth/login", { email, password });
}

export async function logout(): Promise<void> {
  await api.post<void>("/v1/auth/logout");
}

export async function me(): Promise<Session> {
  return api.get<Session>("/v1/auth/me");
}

export async function switchTenant(tenantId: string): Promise<Session> {
  return api.post<Session>("/v1/auth/switch-tenant", { tenant_id: tenantId });
}

/* ----------------------------------------------------------- catalog ---- */

export async function listWarehouses(): Promise<Warehouse[]> {
  const body = await api.get<{ items: Warehouse[] }>("/v1/warehouses");
  return body.items;
}

export async function listProducts(): Promise<Product[]> {
  const body = await api.get<{ items: Product[] }>("/v1/products");
  return body.items;
}

/* ---------------------------------------------------------- inventory --- */

export async function listStock(warehouseId?: string): Promise<StockLevel[]> {
  const query = warehouseId ? `?warehouse_id=${warehouseId}` : "";
  const body = await api.get<{ items: StockLevel[] }>(`/v1/inventory${query}`);
  return body.items;
}

export async function listMovements(productId?: string): Promise<Movement[]> {
  const query = productId ? `?product_id=${productId}` : "";
  const body = await api.get<{ items: Movement[] }>(`/v1/inventory/movements${query}`);
  return body.items;
}

/* ------------------------------------------------------------ orders ---- */

export async function listOrders(params: {
  status?: string;
  query?: string;
  limit?: number;
  offset?: number;
}): Promise<{ items: OrderSummary[]; total: number }> {
  const search = new URLSearchParams();
  if (params.status) search.set("status", params.status);
  if (params.query) search.set("query", params.query);
  search.set("limit", String(params.limit ?? 50));
  search.set("offset", String(params.offset ?? 0));
  return api.get(`/v1/orders?${search.toString()}`);
}

export async function getOrder(id: string): Promise<Order> {
  return api.get<Order>(`/v1/orders/${id}`);
}

export async function confirmOrder(id: string): Promise<Order> {
  return api.post<Order>(`/v1/orders/${id}/confirm`);
}

export async function cancelOrder(id: string, reason: string): Promise<Order> {
  return api.post<Order>(`/v1/orders/${id}/cancel`, { reason });
}

export async function createOrder(
  body: Record<string, unknown>,
  idempotencyKey: string,
): Promise<Order> {
  return api.post<Order>(
    "/v1/orders",
    body,
    { headers: { "Idempotency-Key": idempotencyKey } },
  );
}

/* ---------------------------------------------------------- shipments --- */

export async function listShipments(params: {
  status?: string;
  query?: string;
}): Promise<{ items: ShipmentSummary[]; total: number }> {
  const search = new URLSearchParams();
  if (params.status) search.set("status", params.status);
  if (params.query) search.set("query", params.query);
  search.set("limit", "100");
  return api.get(`/v1/shipments?${search.toString()}`);
}

export async function getShipment(id: string): Promise<Shipment> {
  return api.get<Shipment>(`/v1/shipments/${id}`);
}

export async function createShipment(body: {
  order_id: string;
  carrier: string;
  service_level: string;
}): Promise<Shipment> {
  return api.post<Shipment>("/v1/shipments", body);
}

export async function assignShipment(id: string, agentId: string): Promise<Shipment> {
  return api.post<Shipment>(`/v1/shipments/${id}/assign`, { agent_id: agentId });
}

export async function transitionShipment(
  id: string,
  body: { status: string; note?: string; latitude?: number; longitude?: number },
): Promise<Shipment> {
  return api.post<Shipment>(`/v1/shipments/${id}/status`, body);
}

export async function listAgents(): Promise<Agent[]> {
  const body = await api.get<{ items: Agent[] }>("/v1/agents");
  return body.items;
}

/* ----------------------------------------------------------- tracking --- */

export async function publicTracking(trackingNumber: string): Promise<PublicTracking> {
  return api.get<PublicTracking>(`/v1/track/${encodeURIComponent(trackingNumber)}`);
}

export function trackingSocketUrl(trackingNumber: string): string {
  const base = API_URL.replace(/^http/, "ws");
  return `${base}/v1/track/${encodeURIComponent(trackingNumber)}/ws`;
}
