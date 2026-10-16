# Progress Tracker

A self-contained web app for tracking body recomposition: weight, calories and
macros, with a workout program and a food database. It works offline as a PWA
and can optionally sync across devices through a small Go + SQLite backend that
also exposes an MCP server for AI agents.

## What's in here

| File | Description |
|---|---|
| [`index.html`](./index.html) | The web app (single file). Stores everything in **LocalStorage**. |
| [`cmd/server`](./cmd/server) | Backend (Go + SQLite): serves the app, syncs records incrementally, exposes an **MCP** server, and keeps the legacy blob API for migration. |
| [`Dockerfile`](./Dockerfile) · [`docker-compose.yml`](./docker-compose.yml) | Self-hosting in one container. |
| [`manifest.json`](./manifest.json) · [`sw.js`](./sw.js) · [`favicon.svg`](./favicon.svg) · [`icons/`](./icons) | PWA: installable on the home screen, works offline. |
| [`data/cibi.csv`](./data/cibi.csv) | Example food database. |
| [`data/allenamenti.csv`](./data/allenamenti.csv) | Example 3-day workout program. |
| [`docs/scheda-allenamento.md`](./docs/scheda-allenamento.md) | Example workout plan (2x upper / 2x lower). |
| [`docs/piano-nutrizionale.md`](./docs/piano-nutrizionale.md) | Example calorie and macro targets. |

## Using the tracker

Open `index.html` in a browser (it works on Android phones too). Serve it
locally with:

```bash
# Python
python3 -m http.server 8000

# or Node
npx serve .
```

Then visit <http://localhost:8000>.

### Features

The UI is split into tabs:

- **Dashboard** — current weight, 7-day averages (weight, kcal, P/F/C), a new entry form, and a weight chart.
- **Diary** — foods eaten per day, with grams and kcal/macro totals vs target. Entries can be **reordered** (drag the `⠿` handle or use `▲`/`▼`).
- **Workouts** — the program split by session; add/edit manually or import from CSV.
- **Foods** — the food database: add/edit manually, CSV import/export, search and filter by category.
- **History** — every entry with weight, kcal, macros and day type.
- **Settings** — backup (export/import/clear) and sync.

In detail:

- 📊 Weight trend chart
- ⚖️ Daily weight
- 🔥 Calories in
- 🏋️ Training vs rest days
- 📈 7-day averages (weight, kcal, protein, fat, carbs)
- 🗂️ Full history with macros
- 💾 Automatic save to LocalStorage
- 🗑️ Delete a single entry or the whole history
- 📱 Responsive UI
- 📲 **PWA**: installable on the home screen, works offline
- 🥩 **Food database** importable from CSV
- ➕ **Manual food add/edit** from the UI
- 📓 **Food diary**: add foods with grams; the app computes kcal + macros vs target
- ↕️ **Reorder diary entries**: drag the `⠿` handle (desktop) or use `▲`/`▼` (mobile)
- 🤖 **Greedy suggestion**: what to eat for the rest of the day to hit kcal and macros

> Data stays **on the device/browser** and is not sent to any server unless you
> enable sync.

## Adding a food manually

In the **Foods** tab, the top form creates a food with:

| Field | Notes |
|---|---|
| Name | required |
| Protein / Fat / Carbs | g per 100 g |
| kcal | if left blank, computed as `P*4 + C*4 + F*9` |
| Min / Max | suggested portion in grams (optional) |
| Category | comma-separated tags, e.g. `protein,dairy` |
| Piece | grams per piece (optional) |

Each row has **Add** (to the diary), **Edit** and **×** (delete).

## Importing a food CSV

You can **import a CSV** with the same structure as the food sheet:

```csv
Food (100gr);Protein;Fat;Carbs;kcal;Min (g);Max (g);Category;Piece (g)
Parmesan;33;29;0;398;10;30;extra,dry;
Chicken breast;22.5;2.6;0;120;120;280;protein;
```

- Values are **per 100 g**.
- Delimiter `,`, `;` or tab: auto-detected.
- Decimals with `.` or `,`.
- The header is recognised even with a preamble.
- If `kcal` is missing it is computed as `P*4 + C*4 + F*9`.
- `Min`, `Max`, `Category` and `Piece` are optional.
- The import **merges**: foods with the same name are updated, others added.

The **Load sample foods** button inserts the 32 foods from
[`data/cibi.csv`](./data/cibi.csv) without importing anything. You can also
**export** the current database or **download an empty CSV template**.

## Workouts (CSV)

The **Workouts** tab manages the program split by **session** (day). Each
exercise has: Session, Exercise, Sets, Type, Rest, Notes. **Type** distinguishes
**compound** from **isolation**; Sets and Rest are free text so both `3` and
`2-3'` work.

- **Load sample program** inserts the example 3-day program (22 exercises,
  `data/allenamenti.csv`).
- **Import CSV** with this structure:

```csv
Session;Exercise;Sets;Type;Rest;Notes
Day 1 - Upper A;Dumbbell bench press;3;compound;2-3';
Day 1 - Upper A;Lateral raises;3;isolation;60-90";
```

- Delimiter `,` or `;` auto-detected; header recognised automatically.
- `Type` accepts `compound`/`isolation` (also `multi`/`iso`).
- `Session`, `Type` and `Notes` are optional.
- The import **merges**: same session + exercise = update, otherwise add.
- You can also **export** the program or download a CSV **template**.
- **Fullscreen rest timer**: each exercise has a button that starts a fullscreen
  countdown from the *Rest* column (`2-3'` → 180s, `60-90"` → 90s, `1'30"` → 90s).
  It includes pause, ±15s, a progress ring, a beep and a vibration at the end.

## Greedy algorithm (score per macro)

In the **Diary** tab, below the totals, the app suggests **what to eat to finish
the day** by scoring the whole food table:

1. Compute the remaining need `target - consumed` for kcal, protein, fat and carbs.
2. For each food, with its suggested portion (`Max`, else `Min`, else 100 g),
   compute a **per-macro score**: how much the portion covers that macro's
   remainder (100% = exactly covers it; overshooting is penalised with `1/ratio`).
3. Combine the scores into a **total score**, weighting the most deficient macros
   and especially protein (weights: P 0.40, C 0.25, F 0.20, kcal 0.15).
4. Sort and return the **top 10** foods, with the option to re-sort by a single
   macro (protein / fat / carbs / calories) and exclude foods already in the diary.

Each row shows the score and the grams it contributes per macro, plus an **Add**
button to put the portion in the diary.

## 🐳 Self-hosting (docker compose)

The repo includes a **Go** backend that serves the app and stores data in
**SQLite** (one row per record). Clients sync **incrementally** (`/api/sync`) and
can receive updates **in real time** via SSE (`/api/events`). There is no
authentication: it is meant to run on a **private network / LAN**, not exposed
to the internet.

> **Migration phase.** The old opaque-blob API (`/api/kv`) stays available as a
> *view* over SQLite: a `GET` rebuilds the blob from records, a `PUT` imports it
> record by record. Browsers still using the previous client migrate on their
> first sync, without data loss. The old blob file is read once (and kept as a
> backup); once all clients use `/api/sync`, `/api/kv` can go away.

### Quick start

```bash
cp .env.example .env
docker compose up --build
# open http://localhost:8080
```

### API

| Method | Path | Description |
|---|---|---|
| `GET` | `/api/sync/{key}?since=TS` | Incremental delta: records and tombstones with `updated > TS` |
| `POST` | `/api/sync/{key}` | Send local changes (last-write-wins) |
| `GET` | `/api/events/{key}` | **SSE** stream of real-time changes |
| `GET` | `/api/kv/{key}` | *(compat)* Blob rebuilt from records |
| `PUT` | `/api/kv/{key}` | *(compat/migration)* Import a blob record by record |
| `DELETE` | `/api/kv/{key}` | *(compat)* Delete all records for the key |
| `POST` | `/mcp` | MCP server (Streamable HTTP, JSON-RPC 2.0) for agents |
| `GET` | `/healthz` | Healthcheck |
| `*` | `/` | Serve the static app (`index.html`) |

Data lives in `DATA_DIR/tracker.db` (SQLite, WAL). Keys are
`[A-Za-z0-9_-]{1,64}` and isolate datasets from each other.

#### Records and merge

Each record is `(collection, id, data, updated, deleted)`:

| Collection | id | data |
|---|---|---|
| `entries` | date (`YYYY-MM-DD`) | weight, calories, day type |
| `foods` | food id | name, macros per 100 g, … |
| `diary` | date | day type + diary items |
| `workouts` | exercise id | session, sets, reps, … |
| `done` | `date\0id` | a workout's checked state for a day |

The merge is **last-write-wins** on the client timestamp; on a tie the
*tombstone* wins, otherwise the incoming change. Saving a change is a single
`UPSERT`: the whole blob is no longer rewritten.

#### gzip

Compression is transport-only: the `/api/kv` response is compressed if the
client sends `Accept-Encoding: gzip`, and a gzip upload is decompressed before
import. There is no compressed blob "at rest" anymore: storage is SQLite.

### Sync in the app

Sync is **on by default** (same server that serves the app, key `default`): no
configuration needed. The **Settings** panel shows the status, lets you force
**Sync now** / **Send to server** / **Reload from server**, and point to another
server or key.

Sync is **per record**, not whole-blob:

- every record (weight/calories per date, a diary day, a food or exercise by id,
  a single workout check) carries a last-modified timestamp;
- on sync the app asks only for changes newer than its last pull
  (`GET /api/sync/{key}?since=…`) and sends only pending records from a local
  outbox: no more rewriting whole blobs;
- other devices' updates arrive **in real time** via SSE
  (`GET /api/events/{key}`), without waiting for the next sync;
- a device that fell behind **can no longer delete** days recorded on another
  device: it merges them;
- deletions travel as *tombstones*, so they are not resurrected by an old copy.

Sending is automatic and resilient:

- short debounce on every change, with backoff **retry** (3 s → max 5 min);
- it resumes when the device comes back **online**, when the app returns to the
  foreground, and every minute while visible;
- a best-effort attempt is also made on page close (`pagehide`, with `keepalive`);
- the card shows how many **changes are still pending** and the last attempt's
  result.

The per-record merge logic is unchanged (extracted into `SYNC-CORE` and covered
by tests): only the transport changed, from whole blob to incremental deltas +
SSE. Data also stays in **LocalStorage**, which acts as a local cache.

### MCP (for agents)

The same process exposes an **MCP** server (Model Context Protocol, Streamable
HTTP) at `POST /mcp`, so an agent can **inspect and extend** the food database,
the workout program and the food diary. No second process or extra protocol
dependency: it is hand-written JSON-RPC 2.0 (the server's only dependency is the
pure-Go SQLite driver).

| Tool | What it does |
|---|---|
| `tracker_summary` | Counts, sessions, categories, latest entries, targets |
| `list_foods` | List and search foods (by name and category) |
| `add_food` | Add or update a food (upsert by name) |
| `list_workouts` | List exercises (filter by session/name) |
| `add_workout` | Add or update an exercise (upsert by session+name) |
| `list_diary` | Read the diary: one full day or a list of recent days |
| `add_diary_item` | Add a food to a day (macros computed from grams) |
| `update_diary_item` | Change an item's grams and recompute macros |
| `remove_diary_item` | Remove an item from a day |
| `set_diary_type` | Mark the day as training or rest |

MCP writes act directly on the SQLite **records**: they touch only the affected
rows and publish the change via SSE, so other clients catch up immediately
without waiting for the next sync.

Configuring an MCP client (example with [pi](https://pi.dev)):

```json
{
  "mcpServers": {
    "tracker": { "url": "http://localhost:8080/mcp" }
  }
}
```

From the command line:

```bash
pi mcp add tracker --url http://localhost:8080/mcp
```

Each tool accepts an optional `key` parameter to choose the sync dataset
(default `default`).

### Development

```bash
make run      # :8080, serves ./index.html, data in ./data
make test     # Go + JS tests (per-record merge/sync)
make test-js  # JS sync tests only
make image REGISTRY=registry.example.com   # build + push (linux/arm64)
```

## 🔄 Alternative without a server: JSON backup

If you don't want to self-host, the app has **Export backup (.json)** and
**Import backup** at the bottom: move the file with iCloud Drive / Google Drive /
AirDrop / WhatsApp. It contains weight/calories, the diary, the food database and
the workout program.

## Possible next steps

- [x] Daily macro tracking (protein, carbs, fat)
- [ ] Weekly reminder to log weight and calories
- [ ] Export data (weight, calories, diary) as CSV
