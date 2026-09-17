---
title: Nodetool UI
slug: /nodetool-ui
---

`ui/` is a small standalone web UI for running [Prober](prober.md)'s read-only `nodetool`-style stats
catalog, and a handful of destructive `nodectl` operations, across a cluster's nodes from a browser
instead of `kubectl exec`-ing into pods one at a time. It's a developer/operator diagnostic tool -- it
isn't deployed by the Helm chart, has no Dockerfile, and isn't part of the reconciled `CassandraCluster`
resources.

:::info

The UI is read-mostly by default: everything in the tool catalog other than the `Danger` category is a
read-only stats call. The `Danger` category (`Decommission`, `Drain`, `Assassinate Endpoint`, `Remove Node`,
`Move`, `Stop Daemon`, `Force Remove Completion`) and a few other write operations (repair, cache
invalidation, seed reload) are real, cluster-affecting calls -- see [Running commands](#running-commands)
below for how those are gated.

:::

## Running it

`ui/main.go` is a self-contained Go binary that serves the frontend (`ui/static/index.html`) and proxies
a fixed set of read/write API calls to one configured Prober instance. It never exposes Prober's URL or
credentials to the browser -- only this backend process sees them.

```bash
# Port-forward the target cluster's prober service (adjust namespace/name)
kubectl port-forward -n <namespace> svc/<cluster>-cassandra-prober 18080:80

# In another terminal, from the repo root:
cd ui
PROBER_ENV_NAME=local \
PROBER_URL=http://localhost:18080 \
PROBER_USER=<admin role> \
PROBER_PASSWORD=<admin password> \
LISTEN_ADDR=:8090 \
go run .
```

Then open `http://localhost:8090`. `PROBER_USER`/`PROBER_PASSWORD` are the same admin role/password
used for CQL and JMX (see [Admin Auth Management](admin-auth.md)) -- Prober's own HTTP API is gated by
the same credentials via HTTP Basic Auth.

`loadEnvironments()` in `ui/main.go` only ever configures the single environment described by its own
process env vars (`PROBER_ENV_NAME`/`PROBER_URL`/`PROBER_USER`/`PROBER_PASSWORD`) -- there's no config
file for multiple named environments today. The environment dropdown in the header exists in the
frontend for when that changes, but currently only ever shows the one configured environment.

## Layout

- **Ring view** (center): one ring per DC, one arc segment per rack, one dot per node. Node color reflects
  status (`up` = `NORMAL`, `unknown` = any other reported status, `down` = unreachable).
- **Node list** (left sidebar): every node as a row, with a category filter for the tool button list below
  it and persistent checkbox-based multi-select (`Select all` / `Clear` buttons).
- **Detail panel + Tables panel** (right sidebar): clicking a node on the ring shows its details here. The
  Tables panel lists every keyspace/table for table-scoped commands (see below) -- selection here is
  independent of node selection.
- **Results panel** (bottom, appears on demand): output of whatever command you last ran, one tab per
  invocation, newest first. Explained further under [Running commands](#running-commands).

### Selecting nodes

- Click a node on the ring for its details in the right-hand panel.
- Ctrl/Cmd+click or drag a rectangle across the ring to multi-select nodes.
- The left sidebar's checkboxes are an alternate, persistent way to build the same multi-selection --
  useful for selecting nodes across DCs/racks that aren't visually adjacent on the ring.

## Running commands

There are two ways to invoke a tool:

1. **Middle-click a node** (or a multi-selection) on the ring to open a radial "wheel" of the tools
   flagged `Wheel: true` in Prober's catalog (a curated subset -- the full catalog is longer than is
   usable as a wheel). Hover a wheel entry and release to run it.
2. **Use the tool buttons** in the left sidebar, filtered by category (`Status`, `Performance`,
   `Compaction`, `Tables`, `Actions`, `Danger`, ...). These run against whatever nodes are currently
   selected.

Some tools need more than just a target node:

- **Table-scoped tools** (`Table Stats`, `Table Histograms`, `Top Partitions`, `Describe Ring`,
  `Effective Ownership`, `Repair`) require picking a keyspace/table in the Tables panel first.
- **Argument tools** (e.g. `Assassinate Endpoint`, `Remove Node`, `Move`) prompt for a free-text value
  (an endpoint IP, a host ID, a token) before running.
- **Destructive tools** (anything with `Danger: true` in the catalog, plus some non-Danger writes like
  cache invalidation) require typing the tool's name in a confirmation prompt before it runs, and several
  (`Decommission`, `Drain`, `Assassinate Endpoint`, `Remove Node`, `Move`, `Stop Daemon`,
  `Force Remove Completion`, `Reload Seeds`) only ever target one node at a time regardless of the current
  selection.

### Reading results

The results panel opens at the bottom on the first command you run and stays open across subsequent runs,
tabbed by invocation (newest first). A few things worth knowing:

- **Diffable tools** (`Describe`, `Version`, `Status Flags`, `Settings (get*)`, ...) highlight, per row,
  any value that disagrees across the targeted nodes -- useful for spotting a node with a different
  schema version, config value, etc. without reading every card by hand.
- **Lock scroll** ties every node's result card to the same scroll position, so scrolling one long output
  (e.g. `Net Stats`' dropped-message list) keeps the equivalent row lined up across every other node's
  card.
- **History** controls how many past invocations' tabs are kept (5-100); older ones are dropped once the
  limit is hit.

## Set Settings

`Set Settings` (in the left sidebar, separate from the tool catalog) is a bulk settings editor rather than
a single-shot command: select one or more nodes, open it, and it fetches every targeted node's current
`getBatchlogReplayThrottle`-style settings values (`nodetool getBatchlogReplayThrottle`,
`getConcurrentCompactors`, etc. -- Prober's own `settings` stat), showing a diff badge for any value that's
inconsistent across the selection. Edit any value and apply to push it to every targeted node in parallel
via `PUT /settings`, with a per-node success/failure result once it's done.

## Backend API surface

`ui/main.go` only forwards these Prober routes, each restricted to one HTTP method (see
`proxyablePaths` in `ui/main.go`):

| Path (under `/api/environments/{env}/`) | Method | Prober endpoint | Purpose                          |
|------------------------------------------|--------|------------------|-----------------------------------|
| `nodes`                                   | GET    | `/nodes`         | Node list backing the ring/sidebar |
| `tools`                                   | GET    | `/tools`         | The tool catalog (see above)      |
| `stats`                                   | GET    | `/stats`         | Running a tool against one node   |
| `settings`                                | PUT    | `/settings`      | Applying a settings change        |
| `tables`                                  | GET    | `/tables`        | Keyspace/table list for table-scoped tools |

Adding a new read-only stats command is purely a backend change -- add an entry to Prober's catalog
(`prober/jolokia/stats.go`) and it's picked up by the frontend automatically, with no frontend code
changes needed.
