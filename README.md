# Waypoint

A multi-tenant logistics and order management platform. Waypoint takes an order from
intent to doorstep: stock is reserved inside the order transaction, every state change
is published as an event, couriers move parcels along a strict route, and customers watch
the journey live.

```
┌────────────┐    HTTP/JSON + cookies     ┌──────────────────────────────┐
│  Next.js   │ ─────────────────────────► │  Go API (Chi + Huma)        │
│  console   │                            │  auth · catalog · inventory  │
│  agent app │                            │  orders · shipments          │
│  tracking  │ ◄───────────────────────── │  RBAC · idempotency · RLS    │
└────────────┘        WebSocket           └───────────────┬──────────────┘
                                                             │ tx + outbox
                          ┌───────────────┐      ┌───────────▼──────────┐
                          │ Redis (cache) │      │ PostgreSQL 16        │
                          └───────────────┘      │ RLS per tenant       │
                                                 └───────────┬──────────┘
                                                             │ relay
                                                 ┌───────────▼──────────┐
                                                 │ NATS JetStream       │
                                                 └───────────┬──────────┘
                                                             │
                                                 ┌───────────▼──────────┐
                                                 │ Worker: emails,      │
                                                 │ projections, fan-out │
                                                 └──────────────────────┘
```

## Quick start

```bash
docker compose up -d          # Postgres 5433, Redis 6380, NATS 4222, Mailpit 8025
cd backend
go run ./cmd/migrate up      # schema
go run ./cmd/seed            # demo tenants, users, warehouses, stock
go run ./cmd/api              # API on :8080 (OpenAPI at /docs)

go run ./cmd/worker           # outbox relay + event consumer

cd ../web && npm install && npm run dev   # console on :3100
```

Demo accounts (password `Passw0rd!`):

| Account | Role | Use it for |
| --- | --- | --- |
| `dispatcher@northwind.test` | dispatcher | orders, shipments, assigning couriers |
| `owner@northwind.test` | owner | everything in the tenant |
| `warehouse@northwind.test` | warehouse_manager | stock adjustments |
| `agent@northwind.test` | delivery_agent | the courier console |
| `support@acme.test` | support | a second tenant, for isolation checks |

## Why the design looks like this

**Shared schema, enforced by the database.** Every tenant-scoped table carries
`tenant_id` and has a row-level-security policy. The API connects as
`waypoint_app`, a non-superuser role that cannot bypass RLS, so a missing `WHERE`
clause in application code produces an empty result set rather than another
tenant's data. Migrations run as `waypoint`, a superuser that *can* bypass RLS.

*Tradeoff:* RLS is invisible to the ORM-free SQL layer, so every query must remember
to scope by tenant. The upside is that the database is the last line of defence —
a bug in one handler cannot leak a customer list.

**Stock is a ledger, not a counter.** `stock_levels` holds the current position
(`on_hand`, `reserved`); `stock_movements` is append-only and signed, so the position
can be rebuilt by replaying the ledger. Confirming an order *reserves* stock
(`reserved += qty`, `on_hand` unchanged); picking a parcel up *deducts* it
(`on_hand -= qty`, `reserved -= qty`) and writes a `pick` movement. Every change
writes a movement row in the same transaction, so the audit trail can never drift
from the counters.

*Tradeoff:* reservations need a lifecycle (held → released / fulfilled / expired).
Cancelling an order releases them; an abandoned reservation is left to expire — a
sweeper job is the obvious next step and is deliberately out of scope.

**Idempotency is a first-class column.** `POST /v1/orders` requires an
`Idempotency-Key` header. The key is claimed inside the same transaction as the
order insert, and the request is hashed: replaying the same key with the same body
returns the original order, replaying it with a different body returns `409`. This is
what makes a double-tap on a flaky mobile connection safe.

**Events are written, not published.** The outbox pattern: a business change and its
event row commit in one transaction, and a separate relay moves pending rows to NATS
JetStream. The alternative — publishing inside the request — fails in the gap between
"database committed" and "message published". The relay is at-least-once, so
consumers deduplicate on `event_id` (JetStream's `Nats-Msg-Id` plus a Redis
`SET NX` marker).

*Tradeoff:* the relay adds latency between a database commit and a side effect
(order confirmation e-mail arrives ~100ms later). For a control tower that is a
feature, not a bug: the request path stays fast and a slow SMTP provider can never
stall an order.

**One shipment state machine, separate from the order's.** Orders go
`pending → confirmed → processing → completed`; shipments go
`pending → assigned → picked_up → in_transit → out_for_delivery → delivered`.
Couriers cannot jump steps, and repeating the current step is a no-op so a flaky
retry does not duplicate tracking events. The two machines are linked at two points
only: picking a parcel up moves the order to `processing`, delivering it completes
the order — both in the same transaction as the stock movement.

**Public tracking without accounts.** A tracking number is 96 bits of random data
(`WP` + base32) and acts as a bearer token. The public endpoint is a
`SECURITY DEFINER` SQL function that returns a sanitized projection — no customer
name, no street address, no actor IDs — so the API can answer anonymous requests
without a tenant context. The WebSocket pushes the same sanitized snapshot.

*Tradeoff:* tracking numbers are secrets. Production would add signed, expiring
links and rate limiting; the current version relies on unguessability.

## Layout

```
backend/
  cmd/api          HTTP API entrypoint
  cmd/worker       outbox relay + event consumer
  cmd/migrate      embedded Goose migrations
  cmd/seed         demo data
  internal/
    auth           argon2id, JWT access tokens, rotating refresh tokens
    rbac           roles → permissions
    catalog        warehouses, products
    inventory      stock ledger, reservations
    orders         order lifecycle, reservations, events
    shipments      courier routes, public tracking, WebSocket hub
    worker         NATS consumer, e-mails, cache invalidation
    platform/      database, outbox, idempotency, audit, migrations
web/
  app/console      dispatch console (orders, shipments, inventory)
  app/agent        courier console, mobile-first
  app/track        public tracking page with live updates
docs/
  architecture.md  the long version, with the tradeoffs
  demo.md          a scripted walkthrough
```

## Testing

```bash
cd backend
go test ./...                      # unit tests, no database needed
go test -tags=integration ./...    # database-backed: oversell, idempotency, RLS
```

The integration suite creates its own tenants and deletes them afterwards. The
concurrency test confirms two orders for the same stock at once: exactly one
confirms, the other gets `409 insufficient stock`, and the ledger shows a single
reservation.

## Not built (deliberately)

Payments, real carrier integrations, route optimisation, native mobile apps,
SSO/billing, Kubernetes. Each of those is a product, not a platform feature —
the seams for them (the shipment carrier/service fields, the event stream) are in
place.

## Known limits

* The WebSocket hub is in-process. Two API instances would each hold their own
  subscribers; the fix is to fan out through NATS or Redis pub/sub. The handler
  boundary is already shaped for it.
* The tracking cache is a short 5-second TTL, invalidated on every shipment event
  by the worker. A read-heavy deployment would want a proper projection table.
* Refresh-token reuse detection revokes the whole family, which signs the user out
  of every device. That is the safe default; a grace window is a product call.
