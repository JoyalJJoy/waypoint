# Architecture notes

This is the long version: what each layer guarantees, why it was chosen, and what it
costs. The README has the summary.

## 1. Tenancy

Three mechanisms, in order of how much they protect:

1. **PostgreSQL row-level security** — the real boundary. Every tenant-scoped table
   has `tenant_id` and a policy `USING (tenant_id = app_current_tenant())`. The API
   role (`waypoint_app`) is `NOSUPERUSER NOBYPASSRLS`, so the policy applies even if
   application code forgets to filter. `FORCE ROW LEVEL SECURITY` also binds the
   table owner, so only a superuser (the migration role) can read across tenants.
2. **Tenant context in the transaction** — `database.InTenantTx` runs
   `set_config('app.tenant_id', …, true)` *inside* the transaction. The `true` makes
   it transaction-local, which matters: the pool is shared, so a session-level
   `SET` would leak the tenant into the next request that grabbed the connection.
3. **Explicit tenant filters in SQL** — belt and braces, and they let the query
   planner use the composite indexes.

`switch-tenant` exists because a user can belong to several tenants; the access
token carries the tenant, and switching mints a new one.

*Cost:* every query has to remember its tenant, and "why is this row missing?"
becomes a support question. The payoff is that the answer is usually "RLS said no".

## 2. Identity and sessions

* **Passwords** — Argon2id (64 MiB, t=3, p=2). The encoded string carries its
  parameters, so cost can be raised later without invalidating existing hashes.
* **Access tokens** — HS256 JWT, 15 minutes, carrying `tid` (tenant), `sub` (user)
  and `typ`. The middleware re-loads the membership on every request, so a revoked
  user or a downgraded role loses access immediately instead of at token expiry.
* **Refresh tokens** — opaque random strings stored hashed (SHA-256), one row per
  token, grouped into a family. Rotation happens on every refresh; presenting an
  already-rotated token is treated as theft and revokes the entire family.

*Tradeoff:* refresh reuse detection revokes every device for that user. It is the
safe default; a "trust this device for N days" grace window is a product decision
that has not been made yet.

## 3. RBAC

Seven roles (`owner`, `admin`, `dispatcher`, `warehouse_manager`,
`warehouse_operator`, `delivery_agent`, `support`) map to fine-grained permissions
(`orders:update`, `shipments:assign`, …). The middleware checks the caller's role
against the permissions the operation declares.

The important split is *capability*, not *role*: a warehouse operator may adjust
stock but not cancel an order; support can read everything but write nothing; a
delivery agent can only advance shipments assigned to them (enforced again in the
store, because the API layer is not the only caller).

## 4. Inventory

Three tables:

* `stock_levels` — the current position per (warehouse, product): `on_hand`,
  `reserved`, `version`. `reserved <= on_hand` is a table constraint.
* `stock_movements` — append-only, signed quantities, with `kind` deciding which
  counter moves (`receipt`, `reservation`, `release`, `pick`, `adjustment`, `return`).
* `stock_reservations` — a hold created when an order is confirmed, released when it
  is cancelled, fulfilled when the parcel is picked up.

Reserving is done under `SELECT … FOR UPDATE` on the stock row plus a guarded
`UPDATE … WHERE reserved + $qty <= on_hand`. The guard is the point: it makes
overselling impossible even if two transactions race, and the losing transaction
gets `409 insufficient stock` instead of silently corrupting the count.

*Tradeoff:* the reservation expiry window (24h) is written but not yet swept.
Without a sweeper, an abandoned order holds stock forever. The sweeper is a
background job — it belongs in the worker, which already owns scheduled work.

## 5. Orders

`pending → confirmed → processing → completed`, with `cancelled` reachable from
`pending` and `confirmed`. Transitions are explicit code (`orders.CanTransition`),
never a free-form status field, so an "order" can only move because someone did
something.

Creation snapshots prices and names into `order_items`, so a later price change
does not rewrite history. Confirmation reserves stock and writes an
`order.created`/`order.confirmed` event and audit row in the same transaction.

Cancelling an order releases its reservations. If a shipment already exists it is
either cancelled alongside the order (nothing has moved yet) or the cancellation is
refused (`409`) because the parcel is already on the road.

## 6. Shipments

`pending → assigned → picked_up → in_transit → out_for_delivery → delivered`, plus
`failed` (retryable) and `cancelled`.

Two side effects are coupled to the route, both atomic with the status change:

* **picked up** — reservations become `fulfilled`, `on_hand` drops, a `pick` movement
  is appended, and the order moves to `processing`.
* **delivered** — the order moves to `completed`.

Every status change appends to `shipment_events` (description, optional
lat/long, actor) and publishes a `shipment.status_changed` event carrying the
tracking number, so downstream consumers can react without querying the API.

Public tracking uses a `SECURITY DEFINER` SQL function. It returns one shipment by
tracking number and exposes only customer-safe fields. Because the function is
`SECURITY DEFINER` and owned by the superuser, it can read the rows that RLS hides
from the API role — the sanitisation lives in the query, not in application code.

## 7. Idempotency

`idempotency_keys` is keyed by `(tenant_id, key)` and holds the request hash, the
status and the stored response body.

* First request: the key is claimed inside the business transaction, the work
  happens, the response body is stored, the transaction commits. If the work fails,
  the claim rolls back with it — a failed request never burns a key.
* Replay with the same hash: the stored body is returned (HTTP 201 again).
* Replay with a different hash: `409`, because the same key cannot mean two
  different requests.
* Concurrent replay: the second transaction blocks on the unique index until the
  first commits, then replays the stored response.

Order creation *requires* the header; stock adjustments accept one optionally.
## 8. Events and the outbox

Business transactions never talk to NATS. They append to `outbox_events`; the
relay (in `cmd/worker`) polls for unpublished rows with
`FOR UPDATE SKIP LOCKED`, publishes them to `waypoint.events.<type>` with
`Nats-Msg-Id` set to the event id, and marks them published in the same
transaction.

Delivery is at-least-once. Consumers therefore deduplicate: JetStream drops
identical message ids inside its duplicate window, and the worker additionally
records processed event ids in Redis before acknowledging.

*Tradeoffs:*
* Polling adds up to ~500 ms of latency versus a logical-decoding trigger. The
  trigger approach is faster but couples the API to Postgres logical decoding,
  which is heavier to operate. For a showcase, polling wins on simplicity.
* The relay holds row locks while publishing. Batches are small (100) and the
  publish is a local NATS call, so the lock window is short.
* Consumers must be idempotent. The worker marks events processed *after* the
  side effect succeeds, so a crash mid-handler causes a redelivery rather than a
  silently dropped event.

The worker currently sends order e-mails (through Mailpit), invalidates the public
tracking cache, and logs everything else.

## 9. Caching and live updates

* **Redis** — the public tracking snapshot is cached for five seconds and
  invalidated by the worker on every shipment event. Caching is best-effort: a
  Redis outage degrades to a direct database read rather than an error.
* **WebSocket** — `/v1/track/{tracking}/ws` upgrades and streams updates. The API
  keeps an in-process hub keyed by tracking number; handlers publish after a
  successful transaction.

The hub is the part that would change first in a multi-instance deployment: the
publish would go through NATS (or Redis pub/sub) so every instance forwards to its
own subscribers. The client contract does not change.

*Cost:* the server's read/write timeouts are cleared for upgraded connections, and
a ping keeps idle sockets alive.

## 10. The web app

Next.js App Router, React 19, no state-management library and no CSS framework —
the design system is ~700 lines of CSS with explicit tokens, because a
control-room aesthetic (sharp corners, hairline rules, mono labels) fights
utility classes.

The browser talks to the API directly with `credentials: "include"`. The API sets
`SameSite=Lax` cookies, and `localhost:3000`/`localhost:3100` are the same *site*,
so the cookie is sent on cross-origin fetches; CORS is configured with
`AllowCredentials` and an explicit origin list.

*Tradeoff:* a server-side BFF would hide CORS and cookie juggling entirely, and
would be the right call if the app ever needed to talk to the API from the server
with the user's session (for example, server-rendered pages).

## 11. What I would do next

1. A reservation sweeper in the worker (expire stale holds).
2. Fan the WebSocket hub through NATS so multiple API instances work.
3. Real carrier integration behind a `Carrier` interface, with the tracking number
   supplied by the carrier instead of generated locally.
4. Payment authorisation as another state machine feeding into order creation.
5. A read replica and a real projection for tracking history.
