# Demo walkthrough

A ten-minute script that shows the parts of Waypoint worth showing. It assumes the
stack is up (`docker compose up -d`), the schema is migrated, the demo data is
seeded, and the API, worker and web app are running.

```
API      http://localhost:8080      (OpenAPI UI at /docs)
Console  http://localhost:3100/console
Agent    http://localhost:3100/agent
Tracking http://localhost:3100/track/<tracking-number>
Mailpit  http://localhost:8025      (e-mails land here)
```

## 1. Multi-tenancy is enforced by the database (30s)

```bash
curl -s -c /tmp/nw.cookies -H 'Content-Type: application/json' \
  -d '{"email":"dispatcher@northwind.test","password":"Passw0rd!"}' \
  http://localhost:8080/v1/auth/login > /dev/null

curl -s -c /tmp/acme.cookies -H 'Content-Type: application/json' \
  -d '{"email":"support@acme.test","password":"Passw0rd!"}' \
  http://localhost:8080/v1/auth/login > /dev/null

# A Northwind product id, requested by an Acme user
curl -s -o /dev/null -w '%{http_code}\n' -b /tmp/acme.cookies \
  http://localhost:8080/v1/products/8b1dadd9-6e06-4505-81be-3539a0effba9
```

`404`, not `403` and not the row. Row-level security cannot be bypassed by the API
role, so a forgotten tenant filter produces an empty result rather than a leak.

## 2. RBAC is capability-based (30s)

```bash
# Support may read inventory but not adjust it
curl -s -o /dev/null -w '%{http_code}\n' -b /tmp/acme.cookies http://localhost:8080/v1/inventory
curl -s -o /dev/null -w '%{http_code}\n' -b /tmp/acme.cookies \
  -H 'Content-Type: application/json' \
  -d '{"warehouse_id":"00000000-0000-0000-0000-000000000001","product_id":"00000000-0000-0000-0000-000000000002","kind":"receipt","quantity":1,"reason":"x"}' \
  http://localhost:8080/v1/inventory/adjust
```

`200` then `403`.

## 3. Idempotent order creation (1 min)

```bash
BODY='{"warehouse_id":"d8739701-ad95-4a0a-b816-8e602c8a2cad",
  "customer":{"name":"Casey Customer","email":"casey@example.com"},
  "shipping_address":{"line1":"42 Market St","city":"San Francisco","region":"CA","postal_code":"94105","country":"US"},
  "items":[{"product_id":"8b1dadd9-6e06-4505-81be-3539a0effba9","quantity":2}]}'

curl -s -b /tmp/nw.cookies -H 'Content-Type: application/json' \
  -H 'Idempotency-Key: demo-1' -d "$BODY" http://localhost:8080/v1/orders | jq .id
# repeat the exact same call -> same id, no second order
# change one field but keep the key -> 409
```

The console shows the same thing: the form displays the `Idempotency-Key` it will
use, so a double submit is visibly safe.

## 4. Reservation, and what happens when there is not enough stock (1 min)

```bash
curl -s -b /tmp/nw.cookies -X POST http://localhost:8080/v1/orders/<id>/confirm
curl -s -b /tmp/nw.cookies http://localhost:8080/v1/inventory | jq '.items[] | {sku, on_hand, reserved, available}'
```

`reserved` goes up, `on_hand` does not move, `available` drops.

Ask for more than exists:

```bash
# order 9999 units, confirm it
curl -s -o /dev/null -w '%{http_code}\n' -b /tmp/nw.cookies -X POST \
  http://localhost:8080/v1/orders/<big-id>/confirm
```

`409 insufficient stock`, and the stock row is untouched — the whole confirmation
rolls back, including the event row.

The automated version of this is
`go test -tags=integration ./internal/orders -run Oversell`.

## 5. The route, end to end (3 min)

In the console, open the order → **Open route** → assign **Alex Agent**.

Open `/agent` on a phone-sized window and sign in as `agent@northwind.test`:

* **Collected from the warehouse** — stock is deducted, the reservation is
  fulfilled, the order moves to `processing`.
* **On the road** → **Out for delivery** → **Delivered** — the order completes.

Watch the database while you do it:

```sql
select status, quantity from stock_reservations;
select kind, quantity, reason from stock_movements order by id desc limit 5;
```

The ledger tells the story: `reservation` then `pick`, never a silent counter.

## 6. Live public tracking (1 min)

Copy the tracking number from the shipment and open `/track/<tracking>`.

In a second tab, advance the shipment from the agent console. The public page
updates **without a reload** — the WebSocket pushes the change, and the page pulls
a fresh sanitised snapshot.

The public page never shows the customer name, the street address or who moved the
parcel: the endpoint is a `SECURITY DEFINER` function that projects only
customer-safe fields.

## 7. Events, outbox and e-mail (1 min)

```sql
select event_type, attempts, published_at is not null as published from outbox_events order by id;
```

Every row was written in the same transaction as the business change and published
by the relay afterwards.

Open Mailpit (`http://localhost:8025`): confirming an order sends a confirmation
e-mail. That e-mail was produced by the worker consuming a NATS event — the
request path never touched SMTP.

Stop the worker and confirm an order: the order still succeeds, the outbox row
stays unpublished, and the e-mail arrives when the worker comes back. That is the
whole point of the outbox.

## 8. Restart the world (30s)

```bash
docker compose restart postgres
curl -s -o /dev/null -w '%{http_code}\n' http://localhost:8080/readyz
```

Data survives (volumes), the API reports degraded while Postgres is down, and
sessions survive because refresh tokens are stored hashed in the database.
