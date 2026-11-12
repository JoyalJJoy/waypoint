export type OrderStatus =
  | "pending"
  | "confirmed"
  | "processing"
  | "completed"
  | "cancelled";

export type ShipmentStatus =
  | "pending"
  | "assigned"
  | "picked_up"
  | "in_transit"
  | "out_for_delivery"
  | "delivered"
  | "failed"
  | "cancelled";

export interface Address {
  line1: string;
  line2?: string;
  city: string;
  region?: string;
  postal_code: string;
  country: string;
}

export interface OrderItem {
  id: string;
  product_id: string;
  sku: string;
  name: string;
  quantity: number;
  unit_price_cents: number;
  total_cents: number;
}

export interface OrderEvent {
  id: number;
  from_status: OrderStatus | null;
  to_status: OrderStatus;
  actor_id?: string;
  note: string;
  created_at: string;
}

export interface Order {
  id: string;
  order_number: string;
  status: OrderStatus;
  warehouse_id: string;
  warehouse_name: string;
  customer_name: string;
  customer_email: string;
  shipping_address: Address;
  currency: string;
  subtotal_cents: number;
  total_cents: number;
  notes: string;
  version: number;
  items: OrderItem[];
  events: OrderEvent[];
  confirmed_at?: string;
  cancelled_at?: string;
  completed_at?: string;
  created_at: string;
  updated_at: string;
}

export interface OrderSummary {
  id: string;
  order_number: string;
  status: OrderStatus;
  customer_name: string;
  customer_email: string;
  currency: string;
  total_cents: number;
  item_count: number;
  created_at: string;
}

export interface Agent {
  id: string;
  name: string;
  email: string;
}

export interface Destination {
  city: string;
  region: string;
  country: string;
}

export interface ShipmentEvent {
  id: number;
  status: ShipmentStatus;
  description: string;
  latitude?: number;
  longitude?: number;
  actor_id?: string;
  created_at: string;
}

export interface Shipment {
  id: string;
  shipment_number: string;
  tracking_number: string;
  status: ShipmentStatus;
  order_id: string;
  order_number: string;
  customer_name: string;
  customer_email: string;
  warehouse_id: string;
  warehouse_name: string;
  carrier: string;
  service_level: string;
  destination: Destination;
  weight_grams: number;
  agent: Agent | null;
  estimated_delivery_at?: string;
  picked_up_at?: string;
  delivered_at?: string;
  cancelled_at?: string;
  version: number;
  events: ShipmentEvent[];
  created_at: string;
  updated_at: string;
}

export interface ShipmentSummary {
  id: string;
  shipment_number: string;
  tracking_number: string;
  status: ShipmentStatus;
  order_id: string;
  order_number: string;
  customer_name: string;
  carrier: string;
  service_level: string;
  agent_id?: string;
  agent_name: string;
  created_at: string;
  delivered_at?: string;
}

export interface StockLevel {
  warehouse_id: string;
  warehouse_name: string;
  product_id: string;
  sku: string;
  product_name: string;
  on_hand: number;
  reserved: number;
  available: number;
  updated_at: string;
}

export interface Movement {
  id: number;
  warehouse_id: string;
  warehouse_name: string;
  product_id: string;
  sku: string;
  product_name: string;
  kind: string;
  quantity: number;
  reason: string;
  order_id?: string;
  created_at: string;
}

export interface PublicEvent {
  status: ShipmentStatus;
  description: string;
  latitude?: number;
  longitude?: number;
  created_at: string;
}

export interface PublicTracking {
  tracking_number: string;
  status: ShipmentStatus;
  carrier: string;
  service_level: string;
  order_number: string;
  destination: Destination;
  estimated_delivery_at?: string;
  picked_up_at?: string;
  delivered_at?: string;
  events: PublicEvent[];
  created_at: string;
  updated_at: string;
}

export interface Membership {
  tenant_id: string;
  tenant_name: string;
  tenant_slug: string;
  role: string;
}

export interface Session {
  user: { id: string; email: string; name: string };
  tenant: Membership;
  memberships: Membership[];
}

export interface Warehouse {
  id: string;
  name: string;
  code: string;
  address: Address;
  is_active: boolean;
}

export interface Product {
  id: string;
  sku: string;
  name: string;
  unit_price_cents: number;
  currency: string;
  is_active: boolean;
}
