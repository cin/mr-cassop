# mr-cassop nodetool web UI — seed doc for future sessions

Paste this whole file as your first message in a new conversation to pick up
this work with full context. It replaces an earlier version of this file
that was accidentally deleted (see "The deletion incident" near the end) —
this one is written after the fact, with the benefit of hindsight, rather
than session-by-session as it happened.

## What this is

A web UI for viewing (and, for a bounded set of config knobs, editing)
Cassandra `nodetool`-style status across the multi-DC `mr-cassop` cluster,
built on top of the operator's existing Prober component. **It is now
committed**: branch `feature/nodetool-ui`, commit `f35a6c8` ("Add
nodetool-style status/stats web UI and Prober command catalog"), open as
GitHub PR #114 (based on `fix/sysctl-tolerant-init-container`, since that's
the commit it was branched from — see "Branch/PR structure" below).

Everything described below was built and verified against a live local
`kind` cluster using headless-Chrome/Puppeteer driving the actual running
UI — real read AND write JMX calls, applied and reverted, not just code
review. That verification discipline (build, deploy to the real cluster,
drive it with Puppeteer, read back the real result, fix what's actually
wrong) caught a real bug in nearly every feature added; don't skip it when
extending this.

## Where the code lives

- **`ui/`** — a separate Go module (own `go.mod`, no external dependencies —
  stdlib only), the UI's backend + frontend in two files:
  - `ui/main.go` — a small HTTP server: serves the embedded static frontend
    (`//go:embed static`), lists configured environments at
    `GET /api/environments` (currently just one, from `PROBER_ENV_NAME` +
    `PROBER_URL`/`PROBER_USER`/`PROBER_PASSWORD`/`LISTEN_ADDR` env vars —
    single-environment POC config, not a config file), and proxies
    `/api/environments/{env}/{nodes|tools|stats|settings|tables}` to the
    matching Prober endpoint (method-checked per route; the browser never
    sees Prober's URL or credentials).
  - `ui/static/index.html` — the entire frontend: vanilla JS/CSS, no build
    step, no framework, ~2100 lines.
- **`prober/jolokia/stats.go`** — the generic "stats catalog" system (see
  below), ~1160 lines.
- **`prober/jolokia/{cassandra,jolokia}.go`** — gossip/token data additions
  for the ring view, the split poll/tool HTTP clients, shared bulk-request
  helpers (`bulkReadAttributes`, `bulkReadGauges`, `bulkReadMeterCounts`,
  `bulkWriteAttributes`).
- **`prober/prober/{handlers,node_states,prober}.go`** — the new Prober
  routes (`/stats`, `/settings`, `/tables`) and per-node token derivation.

## Architecture decisions already made (don't re-litigate without reason)

- **Prober is the extension point.** New endpoints go on Prober, not by
  having the UI talk to Jolokia/Cassandra directly, and not by reaching into
  the operator's `nodectl` package (root module) either — that stays
  reserved for the operator's own reconcile-driven operations
  (`Decommission`, `Assassinate`).
- **One generic "stats" system, not one bespoke thing per command.** See
  `prober/jolokia/stats.go`:
  - `StatsResult{Groups []StatGroup{Name, Rows []StatRow{Label, Value}}}` —
    one wire shape for every command's output.
  - `StatDef{Name, Label, Diffable, RequiresTable, Fetch, FetchTable}` and
    `Catalog []StatDef` — adding a new node-scoped read-only command is
    "write one `fetchX` function, add one line to `Catalog`." A
    table-scoped command (`RequiresTable: true`) sets `FetchTable` instead
    of `Fetch` (see "Table-scoped commands" below).
  - Prober exposes `GET /tools` (lists the catalog's name/label/diffable/
    requiresTable) and `GET /stats?name=X&ip=Y[&table=ks.table]` (runs one).
  - **The frontend builds its tool buttons from `/tools` at load time** — it
    does not hardcode a tool list (except "Status", the one purely local
    tool needing no backend call).
- **Read-only by default, but not absolute.** Every `StatDef.Fetch`/
  `FetchTable` is a JMX read. The one write path that exists — the
  `settings` pane's `Set*` editor — is deliberately narrow and separate:
  scoped to `settingSources`' ~15 bounded scalar knobs, its own modal, its
  own confirm step, never reachable from the middle-click wheel (only an
  explicit button), and PUT-only via a dedicated `/settings` route. Don't
  generalize `Fetch` into something bidirectional by default; a new write
  capability needs the same kind of deliberate, separate design this one
  got.
- **Deliberately still not implemented: `describering`/`ring`-style
  per-node ownership percentages.** Computing "Owns %" correctly needs each
  keyspace's replication factor (and NetworkTopologyStrategy DC-awareness
  for a multi-DC cluster like this one) — unlike everything implemented so
  far, which only ever reads a single bounded JMX value/set. A
  confidently-wrong ownership percentage is worse than not having the
  feature; don't attempt it without either finding real JMX-exposed
  ownership numbers or a lot more live-verification budget than everything
  else here needed.

## Everything currently implemented

### The stats catalog (`prober/jolokia/stats.go`)

Node-scoped (no target beyond `ip`): `info`, `describecluster` (Diffable),
`compactionstats`, `tpstats`, `netstats`, `gcstats`, `proxyhistograms`,
`cachestats`, `settings` (Diffable, has a write path — see below),
`statusflags` (Diffable), `compactionhistory`.

Table-scoped (`RequiresTable: true`, need a `keyspace.table` target):
`cfstats` ("Table Stats"), `cfhistograms` ("Table Histograms").

Real JMX bugs found and fixed via live-cluster verification (this pattern —
guess an attribute name/type, deploy, curl the real cluster, fix — is how
every one of these was actually built; don't skip the verify step when
adding more):

- **Gauge vs. Meter/Counter confusion.** A Dropwizard Gauge's JMX value is
  under the `Value` attribute; a Meter's or Counter's is under `Count` —
  same numeric shape, different attribute name, and guessing wrong doesn't
  error, it just silently returns "n/a". Hit this for `cachestats`'
  Requests/Hits (Meters) mixed with Capacity/Entries/Size/HitRate (Gauges),
  and again for `cfstats`' disk-space Counters mixed with its other Gauges.
  Fixed with a Gauge/Meter split read (`bulkReadGauges` +
  `bulkReadMeterCounts`) wherever a metric group mixes types.
- **Writing a whole-number `double` fails outright.** `SetSettings`
  (`TraceProbability`, a JMX `double`) rejected any whole-number value
  ("0", even "0.0") with *"Cannot convert a java.lang.Long value to
  java.lang.Double"*. Cause: Go's default `float64`→JSON marshaling drops
  the decimal point for a whole number, and Jolokia infers a bare integer
  JSON literal as a Java `Long`. Fixed in `parseSettingValue` by formatting
  the value back to a string and forcing a `.` onto it, wrapped as
  `json.RawMessage` (not a bare Go `float64`) so the wire literal is
  unambiguously a double. This will bite any *new* `settingFloat` entry
  too if its write path isn't verified the same way — don't skip that
  verification just because the read side works.
- **`CompactionHistory`'s TabularData has a composite index.** Unlike every
  other TabularData/composite MBean read in this file (all single-column
  index, serialized by Jolokia as a flat array of row objects),
  `CompactionHistory` has a *composite* index (id, keyspace_name,
  columnfamily_name, compacted_at, bytes_in, bytes_out, rows_merged), which
  Jolokia serializes as **one nested map per index column** instead.
  Handled generically by `flattenCompactionHistoryRows`, which recurses
  through the nesting until it finds a map that looks like a full row
  (has both `keyspace_name` and `compacted_at`), rather than hard-coding
  the nesting depth/order. Also: the attribute lives on
  `org.apache.cassandra.db:type=CompactionManager`, not `StorageService`
  (an initial guess by analogy with `fetchCompactionStats` came back "No
  such attribute").
- **Large int-typed values render in scientific notation if not handled
  carefully.** An int-typed JMX attribute (e.g. `MaxHintWindow` = 10800000)
  decodes from JSON as Go `float64`, and `fmt.Sprint`/`%v` on that can
  render as `"1.08e+07"` instead of `"10800000"` — actively wrong to show
  in a field you can also *edit*. Fixed via `formatSettingValue`'s
  `settingInt` case (round-trip through `int64`) and reused for
  `compactionHistoryCount`'s byte counts.
- **Raw `curl` against Jolokia directly is flakier than the app's own
  client.** Ad hoc `curl -X POST` probing occasionally got connection resets
  that the compiled Prober→Jolokia path never hit. When debugging a
  wrong attribute-name guess, prefer the real `GET /stats` path (temporarily
  swap in a `bulkReadAttributes` call trying every plausible (mbean,
  attribute) candidate at once, deploy, curl *that* route, read back which
  candidate actually returned `status:200`) over raw ad hoc Jolokia curls.

### Table-scoped commands + the Tables picker panel

`cfstats`/`cfhistograms` needed a `keyspace.table` target the ordinary
per-node model doesn't have. `StatDef` gained `RequiresTable bool` +
`FetchTable func(j *Client, ip, table string) (StatsResult, error)` (kept
separate from `Fetch` rather than adding an always-empty parameter to every
other entry). `RunStat`, the `Jolokia` interface, and `GET /stats` all
gained a `table` parameter/query param that non-`RequiresTable` stats
ignore.

The frontend's **Tables panel** (sidebar, below the node detail panel) is a
global, persistent multi-select — not a per-click prompt — sourced from a
new `GET /tables?ip=X` route backed by `jolokia.ListTables`, which needs no
new JMX call shape: searching
`type=Table,keyspace=*,scope=*,name=LiveSSTableCount` (one fixed metric
name) picks up exactly one MBean per table. Fetched once per environment
(not on every 5s node poll, since schema rarely changes); a "refresh" link
re-fetches on demand. `runTool` (the dispatcher every button/wheel-item
click goes through) reads this global selection for a `requiresTable` tool
and runs once per selected table, each landing as its own results tab
(`"Table Stats (system.paxos)"`) — reusing the existing tab system rather
than inventing a multi-table-in-one-tab view.

Deliberately still missing from `cfstats`/`cfhistograms`: memtable stats,
bloom filter stats, and the two `Estimated*Histogram` metrics (verified live
these are raw bucket-offset/count arrays, not simple percentile-attribute
MBeans — would need real histogram-chart rendering, not this fetcher's
percentile-row shape).

### The `Set*` settings editor (the write path)

Triggered only by an explicit amber "Set\*" button in the node-list toolbar
(never the middle-click wheel — a write action shouldn't be able to fire
from a stray drag gesture). Flow: query every targeted node's current
`settings` in parallel → one consolidated table (not per-node cards), one
row per label, pre-filled with the common value or blank+red+a hover badge
("N distinct (M nodes)") if nodes disagree → typing in a row marks it
"dirty" → "Set" collects only dirty rows, shows a `confirm()` summary, then
`PUT`s the same changes to every targeted node in parallel and shows each
node's own OK/error line as it lands.

Backend: `SetSettings(j, ip, changes map[string]string) (map[string]string, error)`
validates+parses each changed label independently (a bad value or a
rejected JMX write on one field never blocks any other field in the same
call), batches the valid writes into one JMX round trip via
`bulkWriteAttributes`. New `PUT /settings?ip=X` route, body a flat
`{label: newValue}` JSON object, response a `{label: error}` map (empty =
full success).

### Cross-node diff highlighting (`StatDef.Diffable`)

Built generically, not special-cased to `settings`: any `Diffable` catalog
entry gets it for free. Frontend keeps each node's raw `StatsResult` (not
pre-rendered HTML) so `computeDiffMap` can build a
`"groupName||label" -> Set(values seen)` map across every `done` card and
flag any row whose set has more than one member with `.kv.diff` (red
background/text) plus a "`N settings differ across the targeted nodes`"
hint. Scoped deliberately to `Diffable: true` entries — `describecluster`
(schema version disagreement is a real problem) and `statusflags` (gossip/
native-transport/backups disagreeing is a real problem) got it "for free";
runtime counters that are *expected* to vary (`gcstats`, `cachestats`) don't
set it, since highlighting expected variance would just be noise. Check any
future stat's rows against that same bar before flipping it on.

### Ring viz, wheel, multi-select, list panel, results history

- One ring per DC, nodes placed by token position.
- Middle-click hold-and-drag radial "wheel" for quick single/multi-node tool
  dispatch. `WHEEL_RADIUS` is not a fixed constant — `wheelRadius()`
  computes `Math.max(90, TOOLS.length * 26)`, since a fixed radius that fit
  the original ~7 tools visibly overlapped once the catalog grew past a
  dozen. Re-check this scaling if the catalog grows a lot further.
- Ctrl/Cmd+click or drag-select for multi-node selection; a checkbox-based
  side list panel as an alternative to the wheel gesture, sharing the exact
  same `TOOLS`/selection state.
- Running a tool opens a tabbed results panel (`resultsHistory`), each tab
  an independent invocation with its own per-node cards; a history-length
  dropdown trims old tabs; each tab has its own close button.
- A "Lock scroll" checkbox in the results panel's tabbar syncs every
  visible card's `scrollTop` together (absolute position, not a
  proportional ratio — same-tool results across nodes are normally
  near-identical in shape), so the same row stays lined up while comparing
  long output across nodes.

## Local environment notes

- `kind` cluster name: `mr-cassop-local` (single node — 4-core host, that's
  the ceiling). 2 DCs × 2 replicas (`local-cluster.yaml` at repo root,
  scaled down from a more realistic 3 due to host limits).
- Prober image tagged `:dev`. After any Prober code change:
  ```
  docker buildx build --platform=linux/amd64 --build-arg="VERSION=dev" \
    -f prober/Dockerfile -t ghcr.io/cin/mr-cassop/prober:dev --load prober
  kind load docker-image ghcr.io/cin/mr-cassop/prober:dev --name mr-cassop-local
  kubectl -n mr-cassop-system delete pod -l cassandra-cluster-component=prober
  ```
  then re-establish the port-forward (`kubectl -n mr-cassop-system
  port-forward svc/local-cluster-cassandra-prober 8888:80`) — the pod
  restart kills any existing one.
- Local `ui` server: `cd ui && PROBER_URL=http://localhost:8888
  PROBER_USER=admin PROBER_PASSWORD=admin123 PROBER_ENV_NAME=local-kind
  LISTEN_ADDR=:3000 go run .`
- **`kubectl port-forward` orphan-process trap.** Killing the process you
  *started* a port-forward with doesn't always kill the actual listener —
  check the real holder (`ss -ltnp | grep <port>` or `pgrep -f
  "port-forward.*<port>"`) and `kill -9` that PID explicitly before
  retrying, rather than assuming a fresh backgrounded command actually took
  over the port.
- **`go run .` has the same trap, worse.** `go run` execs a separately
  compiled child binary that can outlive the wrapper if it's SIGKILLed
  rather than exiting cleanly — the old child keeps holding the port *with
  the old embedded content* even after you think you've restarted with new
  code. Confirm the actual listener PID and kill that, not just whatever
  process you invoked.
- **Puppeteer**: `puppeteer-core` (not full `puppeteer`) +
  a manually-downloaded Chrome binary under `~/.cache/puppeteer`. This is
  the verification method that caught nearly every real bug above — not
  curl alone, not code review alone.

## Branch/PR structure (as of this doc)

The original combined uncommitted work got split across three branches, all
originally branched off `fix/sysctl-tolerant-init-container` (commit
`7816b82`, itself based on `main`):

- **PR #112** — `fix/sysctl-tolerant-init-container` → `main`. Unrelated to
  the nodetool UI (a privileged-init container sysctl-tolerance fix). Its
  integration tests were failing for a bit due to two stale test
  assertions that hadn't been updated for the new per-key `sysctl -w
  key="value" || true` format — fixed in a follow-up commit
  (`5393f85`); full integration suite verified green locally (envtest 1.33,
  since CI's 1.32.x wasn't available locally) before pushing.
- **PR #114** — `feature/nodetool-ui` → `fix/sysctl-tolerant-init-container`.
  This document's subject. Everything described above, in one commit
  (`f35a6c8`).
- **PR #116** — `feature/reaper-auth` → `fix/sysctl-tolerant-init-container`.
  Unrelated: Reaper HTTP Basic Auth support. Leave alone unless asked.

\#114 and #116 both target #112's branch rather than `main` directly (an
accurate reflection of the real branch ancestry, not a mistake) — they're
effectively stacked on #112 and will auto-retarget to `main` once #112
merges and its branch is deleted.

## The deletion incident (why this doc exists, and a cautionary tale)

Partway through this work, a *different* session, while trying to split the
single working branch into the three above, accidentally `rm`'d the
untracked `ui/` directory, `prober/jolokia/stats.go`, and the previous
version of this recap doc instead of stashing them. Since these were all
**untracked** files, `git` had zero record of them — no way to `git
checkout` them back.

Recovery (all fully successful, `ui/main.go` reconstructed rather than
byte-recovered):
- `prober/jolokia/stats.go` — recovered **byte-for-byte exact** from Docker
  buildx's own build cache. The prober Dockerfile does `COPY . .` in its
  builder stage; buildx (with a `docker-container` driver) caches that as
  an uncompressed snapshot inside the buildkit container
  (`/var/lib/buildkit/runc-overlayfs/snapshots/snapshots/<id>/fs/`). Found
  the right snapshot by `find`ing `stats.go` by filename across all
  snapshots, then grepping the small candidate set for a symbol unique to
  the final version, then `docker cp`'d it out.
- `ui/static/index.html` — also recovered **byte-for-byte exact**. An
  earlier `go run .` background process had been killed without its temp
  binary being cleaned up (see the port-forward/go-run orphan traps above —
  same underlying mechanism). Since the file is `go:embed`'d, the orphaned
  binary under `/tmp/go-build*/b001/exe/ui` still served the exact original
  bytes over HTTP once re-run.
- `ui/main.go` + `ui/go.mod` — **reconstructed from memory** (the binary's
  symbol table was stripped, so no exact-source recovery route existed),
  then validated by running the reconstruction side-by-side with the still-
  live original binary and diffing every observable behavior (root page
  bytes, JSON error formats, status codes, routing/method-checking edge
  cases) — all matched exactly, including one fix along the way (`writeJSON`
  needed `json.NewEncoder(...).Encode(v)`, not `json.Marshal`+`Write`, to
  match a trailing-newline difference the first draft missed).

**Lesson for future sessions**: this whole incident was possible only
because the nodetool UI work sat uncommitted, as untracked files, across
many sessions. It's committed now — keep it that way. If a future session
needs to reorganize commits/branches again, stash or commit before any
`rm`/`clean`/`reset`, never delete untracked work as a way to "start clean."
