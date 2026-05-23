# Source Asia — Backend Assignment

> **Language:** Go · **Dependencies:** None (standard library only) · **Port:** `:8080`  

---

## Table of Contents

1. [Setup & Running](#1-setup--running)
2. [Part 1 — Rate-Limited API](#2-part-1--rate-limited-api)
3. [Part 2 — Product Catalog with Media](#3-part-2--product-catalog-with-media)
4. [Assumptions & Design Decisions](#4-assumptions--design-decisions)
5. [Tradeoffs](#5-tradeoffs)
6. [Production Limitations](#6-production-limitations)

---

## 1. Setup & Running

### Prerequisites

| Tool | Minimum version |
|------|----------------|
| Go   | 1.22           |

No third-party packages are used. The module has **zero external dependencies**.

### Start the server

```bash
cd e:\Backend_Assignment\solution
go run .
```

Expected output:

```
2026/05/23 11:00:00 Server listening on :8080
```

The server listens on **port 8080**. Both Part 1 and Part 2 are served from the same process on the same port.

### Run the smoke-test client

Open a **second terminal** while the server is running:

```bash
cd e:\Backend_Assignment\solution
go run ./smoketest/
```

The smoke-test exercises every happy-path and every documented error case for both parts and prints the HTTP status code and response body for each call.

### Project layout

```
solution/
├── main.go               ← wires both handlers onto http.ServeMux
├── go.mod                ← module sourceasia-backend (no external deps)
├── ratelimit/
│   └── handler.go        ← Part 1  /request  /stats
├── catalog/
│   └── handler.go        ← Part 2  /products  /products/{id}  /products/{id}/media
├── smoketest/
│   └── main.go           ← integration smoke-test client
└── README.md             ← this file
```

---

## 2. Part 1 — Rate-Limited API

### Rate limiting rules

- **Algorithm:** Rolling 1-minute sliding window (not a fixed window — see §4).
- **Limit:** Maximum **5 accepted requests** per `user_id` per 60-second window.
- **Exceeded:** `429 Too Many Requests`.
- **Rejected counter:** Cumulative (all-time since server start), not per-window — easier to audit.

### Endpoints

---

#### `POST /request`

**Request body** (`Content-Type: application/json` required):

```json
{
  "user_id": "alice",
  "payload": { "any": "json value" }
}
```

| Scenario | Status | Body |
|----------|--------|------|
| Accepted | `201 Created` | `{ "status": "accepted", "user_id": "alice" }` |
| Rate limit exceeded | `429 Too Many Requests` | `{ "error": "rate limit exceeded", "message": "maximum 5 requests per 1-minute rolling window" }` |
| Missing / empty `user_id` | `400 Bad Request` | `{ "error": "user_id is required and must be non-empty" }` |
| Missing `payload` | `400 Bad Request` | `{ "error": "payload is required" }` |
| Invalid JSON | `400 Bad Request` | `{ "error": "invalid JSON" }` |
| Wrong Content-Type | `415 Unsupported Media Type` | `{ "error": "Content-Type must be application/json" }` |
| Wrong method | `405 Method Not Allowed` | `{ "error": "method not allowed" }` |

**curl example:**

```bash
curl -s -X POST http://localhost:8080/request \
  -H "Content-Type: application/json" \
  -d '{"user_id":"alice","payload":{"n":1}}'
```

---

#### `GET /stats`

Returns per-user statistics and optional global totals.

**Response schema:**

```json
{
  "users": {
    "alice": {
      "accepted_in_current_window": 3,
      "rejected_total": 0
    },
    "bob": {
      "accepted_in_current_window": 5,
      "rejected_total": 1
    }
  },
  "global": {
    "accepted_in_current_window": 8,
    "rejected_total": 1
  }
}
```

| Field | Meaning |
|-------|---------|
| `accepted_in_current_window` | Requests accepted **within the last 60 seconds** (live, recalculated on each GET /stats call) |
| `rejected_total` | **Cumulative** rejected count since server start (documented choice — see §4) |

**curl example:**

```bash
curl -s http://localhost:8080/stats
```

---

### Part 1 — curl walkthrough (send 6 requests, expect 5 accepted + 1 rejected)

```bash
for i in 1 2 3 4 5 6; do
  curl -s -X POST http://localhost:8080/request \
    -H "Content-Type: application/json" \
    -d '{"user_id":"alice","payload":{"n":'"$i"'}}'; echo
done

curl -s http://localhost:8080/stats
```

---

## 3. Part 2 — Product Catalog with Media

### Data model

Two separate in-memory structures keep the list endpoint fast:

```
products     []Product          // lightweight slice — id, name, sku, created_at only
productIndex map[int64]int      // product_id → slice index (O(1) detail/media lookups)
media        map[int64]*Media   // product_id → { image_urls[], video_urls[] }
skuIndex     map[string]int64   // sku → product_id (O(1) uniqueness check on create)
```

`GET /products` (list) reads only the `products` slice items and two `len()` calls on the `Media` struct — it **never deserialises the URL strings**. With 1,000 products × 10 images each, the list still only touches 1,000 lightweight structs.

### Validation rules

| Field | Rule |
|-------|------|
| `name` | Required, non-empty after trimming whitespace |
| `sku` | Required, non-empty, **globally unique** |
| `image_urls` | Each URL must be `http://` or `https://`, max **2048 characters**, max **20 URLs per request** |
| `video_urls` | Same rules as `image_urls` |

### Endpoints

---

#### `POST /products`

**Request body** (`Content-Type: application/json` required):

```json
{
  "name": "Widget A",
  "sku": "SKU-001",
  "image_urls": [
    "https://cdn.example.com/products/sku-001/img-1.jpg",
    "https://cdn.example.com/products/sku-001/img-2.jpg"
  ],
  "video_urls": [
    "https://cdn.example.com/products/sku-001/demo.mp4"
  ]
}
```

`image_urls` and `video_urls` are **optional** on create; you can add them later via `POST /products/{id}/media`.

**Success `201 Created`:**

```json
{
  "id": 1,
  "name": "Widget A",
  "sku": "SKU-001",
  "image_urls": ["https://cdn.example.com/products/sku-001/img-1.jpg"],
  "video_urls": ["https://cdn.example.com/products/sku-001/demo.mp4"],
  "thumbnail_url": "https://cdn.example.com/products/sku-001/img-1.jpg",
  "created_at": "2026-05-23T11:00:00Z"
}
```

| Scenario | Status |
|----------|--------|
| Created | `201 Created` |
| Duplicate SKU | `409 Conflict` — `{ "error": "duplicate sku" }` |
| Missing / empty name or sku | `400 Bad Request` |
| Invalid URL in arrays | `400 Bad Request` — includes the offending URL |
| More than 20 URLs in one array | `400 Bad Request` |
| Wrong Content-Type | `415 Unsupported Media Type` |

```bash
curl -s -X POST http://localhost:8080/products \
  -H "Content-Type: application/json" \
  -d '{
    "name": "Widget A",
    "sku": "SKU-001",
    "image_urls": [
      "https://cdn.example.com/products/sku-001/img-1.jpg",
      "https://cdn.example.com/products/sku-001/img-2.jpg"
    ],
    "video_urls": [
      "https://cdn.example.com/products/sku-001/demo.mp4"
    ]
  }'
```

---

#### `GET /products`

Returns a **paginated list** — never includes the full `image_urls` or `video_urls` arrays.

**Query parameters:**

| Parameter | Default | Maximum | Description |
|-----------|---------|---------|-------------|
| `limit`   | `20`    | `100`   | Number of items per page. Clamped to 100 if larger. |
| `offset`  | `0`     | —       | Number of items to skip. Must be ≥ 0. |
| `search`  | —       | —       | Optional. Case-insensitive substring match on `name` and `sku`. |

**Response `200 OK`:**

```json
{
  "total": 2,
  "limit": 20,
  "offset": 0,
  "items": [
    {
      "id": 1,
      "name": "Widget A",
      "sku": "SKU-001",
      "image_count": 2,
      "video_count": 1,
      "thumbnail_url": "https://cdn.example.com/products/sku-001/img-1.jpg",
      "created_at": "2026-05-23T11:00:00Z"
    },
    {
      "id": 2,
      "name": "Gadget Z",
      "sku": "SKU-002",
      "image_count": 1,
      "video_count": 0,
      "thumbnail_url": "https://cdn.example.com/products/sku-002/img-1.jpg",
      "created_at": "2026-05-23T11:01:00Z"
    }
  ]
}
```

Note: `image_urls` and `video_urls` are **never present** in list items.

```bash
# Basic list
curl -s "http://localhost:8080/products?limit=10&offset=0"

# Search by name or SKU
curl -s "http://localhost:8080/products?search=widget"
```

---

#### `GET /products/{id}`

Returns the **full product** including all URL arrays.

**Success `200 OK`:**

```json
{
  "id": 1,
  "name": "Widget A",
  "sku": "SKU-001",
  "image_urls": [
    "https://cdn.example.com/products/sku-001/img-1.jpg",
    "https://cdn.example.com/products/sku-001/img-2.jpg"
  ],
  "video_urls": [
    "https://cdn.example.com/products/sku-001/demo.mp4"
  ],
  "created_at": "2026-05-23T11:00:00Z"
}
```

| Scenario | Status |
|----------|--------|
| Found | `200 OK` |
| Unknown ID | `404 Not Found` — `{ "error": "product not found" }` |
| Non-integer ID | `400 Bad Request` — `{ "error": "invalid product id" }` |

```bash
curl -s http://localhost:8080/products/1
curl -s http://localhost:8080/products/9999   # → 404
```

---

#### `POST /products/{id}/media`

Appends new image or video URLs to an existing product.

**Request body** (`Content-Type: application/json` required):

```json
{
  "image_urls": ["https://cdn.example.com/products/sku-001/img-3.jpg"],
  "video_urls": []
}
```

At least one of `image_urls` or `video_urls` must be non-empty.

**Success `200 OK`:**

```json
{
  "product_id": 1,
  "image_count": 3,
  "video_count": 1
}
```

| Scenario | Status |
|----------|--------|
| URLs appended | `200 OK` with updated counts |
| Both arrays empty | `400 Bad Request` — `{ "error": "at least one of image_urls or video_urls is required" }` |
| Invalid URL | `400 Bad Request` |
| Unknown product ID | `404 Not Found` |
| Wrong Content-Type | `415 Unsupported Media Type` |

```bash
curl -s -X POST http://localhost:8080/products/1/media \
  -H "Content-Type: application/json" \
  -d '{"image_urls":["https://cdn.example.com/products/sku-001/img-3.jpg"]}'
```

---

## 4. Assumptions & Design Decisions

### Part 1

| Decision | Choice | Rationale |
|----------|--------|-----------|
| Window type | **Rolling / sliding window** | Fairer to users than a fixed window. A fixed window allows a burst of 10 requests at the window boundary (5 at the end of window N, 5 at the start of window N+1). A rolling window prevents this. |
| Success status code | **201 Created** | Each accepted request represents a newly-recorded resource (a recorded event). 200 OK would also be acceptable; this is the documented choice. |
| Rejected counter | **Cumulative (all-time)** | Per-window rejected counts would reset and lose audit history. Cumulative is more useful for ops dashboards. |
| `accepted_in_current_window` on `/stats` | Live, recalculated each call | Calls `prune()` on each user entry so the window is always up-to-date at query time. No background goroutine needed. |
| Concurrency | Per-user `sync.Mutex` + top-level `sync.Mutex` for the users map | Avoids a single global bottleneck. Two users can be rate-checked in parallel; only map inserts (first request per user) serialise at the top level. |

### Part 2

| Decision | Choice | Rationale |
|----------|--------|-----------|
| Duplicate SKU | **409 Conflict** | Semantically correct — the resource (SKU) already exists. 400 would also be acceptable per the assignment brief. |
| `thumbnail_url` | First `image_url` supplied, or omitted if no images | Simple, deterministic, and consistent with common API conventions. |
| URL validation | `http://` or `https://` scheme, max 2048 chars, max 20 per array | 2048 is the de-facto safe URL length (IE11 limit); 20-per-request prevents accidental mass-upload in a single call. |
| Product ID | Auto-incrementing `int64` via `atomic.Int64` | Deterministic, simple, collision-free within a single process. A UUID would be better for distributed systems. |
| `productIndex map[int64]int` | Maps product ID → slice index | Gives **O(1)** detail and media lookups instead of O(n) linear scans over the products slice. Mirrors a primary-key index in a real database. |
| `search` query param | Case-insensitive substring match on `name` and `sku` | Useful for UI autocomplete. Not required by the brief but adds no complexity. Only iterates over lightweight `Product` structs — does not touch media. |
| List endpoint omits URL arrays | URLs stored in a separate `Media` struct | The list endpoint reads only `len(m.ImageURLs)` and `len(m.VideoURLs)` — O(1) integer reads — regardless of how many URLs exist per product. Satisfies the performance rule. |

---

## 5. Tradeoffs

### In-memory vs persistent storage

The assignment explicitly permits in-memory storage. Tradeoffs accepted:

| Accepted tradeoff | Production remedy |
|-------------------|-------------------|
| All data is lost on restart | Use PostgreSQL or Redis with persistence enabled |
| Single process only — state cannot be shared across instances | Move rate-limit state to Redis (sliding window via `ZADD` + `ZREMRANGEBYSCORE`); move product data to a relational DB |
| No durability / crash safety | WAL-based storage (Postgres) or an append-only log |

### Linear list vs indexed list (search)

The `?search=` filter does a linear scan over the `products` slice. For a list of millions of products this would be slow. In production this would be a `WHERE name ILIKE $1 OR sku ILIKE $1` query with a full-text or trigram index in PostgreSQL.

### Rolling window timestamp slice

Each user's accepted-request timestamps are stored in a `[]time.Time` slice. Memory per user is bounded at `5 × sizeof(time.Time)` = 5 × 24 bytes = 120 bytes (at most 5 timestamps in a 60-second window). Acceptable for this scale.

### No authentication

Any caller can pass any `user_id`. In production, a JWT or API-key middleware would verify identity before the rate-limit check.

---

## 6. Production Limitations

### Part 1 — Rate Limiter

| Limitation | Production Fix |
|------------|----------------|
| State is in-memory — lost on restart | Redis sliding window (`ZADD` / `ZREMRANGEBYSCORE` / `EXPIRE`) |
| Single-instance only — no cross-node coordination | Redis as a shared rate-limit store; all instances point to the same cluster |
| `users` map grows unboundedly | Periodic eviction goroutine (evict entries with no activity in > 10 min), or Redis TTL-based auto-expiry |
| Clock skew in distributed deployments | Use `TIME` command from Redis as a coordinated clock source |
| No authentication — any caller supplies any `user_id` | JWT / API-key middleware before the rate-limit check |

### Part 2 — Product Catalog

| Limitation | Production Fix |
|------------|---------------|
| All data lost on restart | PostgreSQL: `products` table + `product_media(product_id, url, media_type, sort_order)` table |
| `GET /products/{id}` is O(1) via `productIndex` but that map is also in-memory | Postgres `WHERE id = $1` with primary-key index |
| No soft delete / update endpoints | Add `PATCH /products/{id}` and `DELETE /products/{id}` |
| No file upload — URLs only | Presigned S3 / CDN upload; store the returned URL string |
| SKU uniqueness enforced by a map — not crash-safe | Postgres `UNIQUE` constraint on `sku` column |

### PostgreSQL + CDN schema (what would change)

```sql
-- Core products table (no URL columns)
CREATE TABLE products (
  id            BIGSERIAL PRIMARY KEY,
  name          TEXT        NOT NULL,
  sku           TEXT        NOT NULL UNIQUE,
  thumbnail_url TEXT,
  image_count   INT         NOT NULL DEFAULT 0,
  video_count   INT         NOT NULL DEFAULT 0,
  created_at    TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

-- Media stored separately — never joined on the list query
CREATE TABLE product_media (
  id          BIGSERIAL PRIMARY KEY,
  product_id  BIGINT      NOT NULL REFERENCES products(id) ON DELETE CASCADE,
  url         TEXT        NOT NULL,
  media_type  TEXT        NOT NULL CHECK (media_type IN ('image','video')),
  sort_order  INT         NOT NULL DEFAULT 0
);
CREATE INDEX ON product_media(product_id);
```

**List query** (no JOIN):
```sql
SELECT id, name, sku, thumbnail_url, image_count, video_count, created_at
  FROM products
 WHERE (name ILIKE $1 OR sku ILIKE $1)   -- optional search
 ORDER BY id
 LIMIT $2 OFFSET $3;
```

**Detail query** (two separate queries):
```sql
SELECT * FROM products WHERE id = $1;
SELECT url, media_type FROM product_media WHERE product_id = $1 ORDER BY sort_order;
```

---

*All source code is in `e:\Backend_Assignment\solution\`. To run: `cd solution && go run .`*
