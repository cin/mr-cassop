# mr-cassop local dev: WSL2 sysctl defaulting blocks Cassandra pod bootstrap

## Repo / context
- `github.com/cin/mr-cassop` — Kubernetes operator for multi-region Apache Cassandra (Go, controller-runtime v0.24.1).
- Reproducing on: WSL2, kernel `6.6.114.1-microsoft-standard-WSL2`, single-node `kind` cluster (`kind create cluster --name mr-cassop-local`), operator + Cassandra images built locally via `VERSION=dev ./build-local.sh --cassandra` + `make docker-build-jolokia docker-build-icarus` (retagged to `:dev`), then `kind load docker-image`.
- Goal that surfaced this: standing up a local 2-DC / 6-pod (3 nodes/DC) `CassandraCluster` to verify a new Prober `/nodes` HTTP endpoint (already implemented and verified working against 2 live gossiping nodes in DC1 — that part is done, not blocked).

## Root cause #1: unsupported sysctls, no way to opt out via the CR

`controllers/defaults.go:337` (`defaultSysctls`) hardcodes 13 default sysctls onto `cc.Spec.Cassandra.Sysctls`, including:
```
net.core.rmem_max, net.core.wmem_max, net.core.rmem_default,
net.core.wmem_default, net.core.optmem_max
```
On this WSL2 kernel, these 5 `/proc/sys/net/core/*` files **do not exist inside a pod's network namespace** (confirmed: they exist fine on the WSL2 *host*'s own `/proc/sys/net/core/*` when checked directly, just not inside a container's netns — looks like a WSL2-specific netns limitation, not a real absence of kernel support).

The `privileged-init` container (`controllers/cassandra_init_containers.go` ~line 88-99) runs **one combined command**:
```
sysctl -w net.core.optmem_max="40960" net.core.rmem_default="16777216" ... (all 13, space-joined)
```
`sysctl` continues past `cannot stat ...: No such file or directory` errors for the 5 bad keys but the **whole command's exit code is non-zero**, so the init container is reported `Error`, and the pod sits in `Init:Error` / `Init:CrashLoopBackOff` forever.

**Critical detail — there is no CR-level workaround.** `defaultSysctls` merges in any *missing* key even into a non-nil, explicitly-empty map:
```go
func (r *CassandraClusterReconciler) defaultSysctls(cc *dbv1alpha1.CassandraCluster) {
	defaultSysctls := map[string]string{ /* the 13 keys */ }
	if cc.Spec.Cassandra.Sysctls == nil {
		cc.Spec.Cassandra.Sysctls = defaultSysctls
		return
	}
	for key, value := range defaultSysctls {
		if _, exists := cc.Spec.Cassandra.Sysctls[key]; !exists {
			cc.Spec.Cassandra.Sysctls[key] = value
		}
	}
}
```
Setting `cassandra.sysctls: {}` in the CR does **not** skip defaulting — the empty map still gets every default key merged back in (every key is "missing" from an empty map). You can only *override a key's value*, never remove/suppress a key. Since the failure is "file doesn't exist," no value fixes it — only omitting the `sysctl -w` call for that specific key would.

## What was tried as a live (no-code-change) workaround, and why it doesn't reliably converge

Approach: directly `kubectl patch` the StatefulSet's `initContainers[0].command` to drop the 5 unsupported keys, keeping only the 8 that work (`somaxconn`, `ip_local_port_range`, `tcp_rmem`, `tcp_wmem`, `vm.dirty_background_bytes`, `vm.dirty_bytes`, `vm.max_map_count`, `vm.swappiness`).

This does **not stick**: the operator's reconcile loop is watch-triggered (fires again on its own writes) and immediately re-renders the StatefulSet from the CR spec + `defaultSysctls()`, overwriting the patch back to the broken 13-key command — usually within well under a second. It's a race, not a one-time nuisance:
- Sometimes the patch wins the window before a pod is (re)created, and once a pod object exists its spec is immutable, so that pod is fine forever after.
- Sometimes the operator's revert wins, the (re)created pod gets the broken command, and it crash-loops.
- Observed win rate empirically: maybe ~50% per pod for a single contested pod; noticeably worse when two pods need to win simultaneously (both `dc1-1` and `dc2-1` needed independently-lucky windows, and after ~6 rounds of synchronized `patch`+`delete pod` for both, neither had converged).

Also tried setting `spec.updateStrategy: {type: OnDelete}` on the StatefulSet (to stop Kubernetes' `RollingUpdate` from proactively killing/recreating already-healthy pods whenever the template drifts) — **also reverted by the operator**, which unconditionally renders `updateStrategy: RollingUpdate` with no CR-level override. This actually made things *worse* at one point: once `updateStrategy` flipped back to `RollingUpdate` while a mismatch existed, Kubernetes killed two previously-**stable, fully-Running** pods (`dc1-1`, `dc2-1` had reached `2/2 Running`) to "converge" them to the operator's broken template.

## Root cause #2 (compounding, discovered mid-session): one unscheduled pod blocks the whole cluster's bootstrap

`controllers/cassandra_pods_config.go:76-82` (`podsConfigMapData`) iterates **every pod in the cluster** (all DCs) and returns `ErrPodNotScheduled` for the *entire* ConfigMap update if **any single pod** doesn't have a `PodIP` yet:
```go
for _, pod := range podList.Items {
    ...
    if len(pod.Status.PodIP) == 0 {
        return nil, ErrPodNotScheduled  // aborts the WHOLE map, not just this pod's entry
    }
}
```
This ConfigMap (`local-cluster-pods-config`) is what the 3rd init container (`init`, in `cassandra_init_containers.go`) polls for via `/etc/pods-config/<pod>_<uid>.sh` — it just loops "Waiting for the operator to mount pod config... Attempt N" until its entry appears. So: **one Pending/unscheduled pod anywhere in the cluster silently blocks every other pod's init container**, even already-Running pods in a completely different DC, since the operator can never successfully write anyone's config entry while any pod lacks an IP.

## Root cause #3 (compounding): hardcoded, non-configurable init-container CPU requests create a real resource ceiling

`controllers/cassandra_init_containers.go` (~line 85-86) hardcodes `privileged-init` and `maintenance-mode` init container requests to `cpu: 500m, memory: 200Mi` **each**, regardless of what's set in the CR's `cassandra.resources`. Kubernetes computes a pod's scheduling-time resource reservation as `max(sum of app containers, max single init container)`, and that reservation is fixed at pod admission — it does **not** shrink once init containers finish and only the (leaner) app containers are actually running.

On a 4-core host (`nproc`=4, this WSL2 box), attempting 6 concurrent Cassandra pods (2 DCs × 3 replicas) even with trimmed main-container requests (300m/pod) hit `FailedScheduling: Insufficient cpu` at just 4-5 concurrent pods:
```
Allocated resources: cpu 3650m (91%) requests, 4600m (114%) limits
```
Worked around by reducing both DCs to 2 replicas each (4 pods total) in the CR — but even that didn't fully resolve things, because of Root cause #2: as long as any pod was transiently unscheduled during the scale-down transition, it re-triggered the "whole cluster blocked" condition above, and unblocking it re-triggered the sysctl race in Root cause #1 on the pods that were already stable.

## Current state left in the cluster (for reference, not necessarily to preserve)
- CR `local-cluster` in namespace `mr-cassop-system`: `dcs: [{name: dc1, replicas: 2}, {name: dc2, replicas: 2}]` (scaled down from 3/3 due to the CPU ceiling).
- `dc1-0`, `dc2-0`: `1/2 Running`, Cassandra container itself has never restarted (0 restarts) — the missing "2/2" is just a flapping readiness probe (Prober's cross-node gossip check is noisy while peers crash-loop), not an actual failure.
- `dc1-1`, `dc2-1`: crash-looping in `Init:Error` on the sysctl race described above.
- Local manifests at repo root (untracked, not committed): `local-cluster.yaml` (2-DC target), `local-cluster-1dc.yaml` (1-DC stage, since superseded).

## What's NOT blocked / already done
The actual feature work this cluster was standing up to verify — a new Prober `GET /nodes` endpoint exposing per-node gossip state including newly-added `Load`/`Host_ID`/`Release_Version` fields — was already fully verified working correctly against 2 live gossiping DC1 nodes *before* the 2-DC scale-up attempt began. That code (`prober/jolokia/cassandra.go`, `prober/prober/{prober,handlers}.go` + tests) is done and untouched by this issue.

## Suggested angles for an actual fix (none attempted yet — this needs a design decision, not just a patch)
1. **Make the sysctl step tolerant of individual missing keys** — loop `sysctl -w key="value" || true` per key instead of one combined command. Smallest, most defensible change; helps *any* environment with a nonstandard/reduced kernel (WSL2, gVisor, minimal cloud kernels), not just this one. Location: `controllers/cassandra_init_containers.go` (~line 88-99, where `sysctlArgs` is built and joined).
2. **Give `defaultSysctls()` a real way to suppress a specific key** — currently there's no distinction between "user didn't specify this key" and "user wants this key off." Would need a design call (e.g., a sentinel value, or a separate `cassandra.disableSysctls: []string` field). Location: `controllers/defaults.go:337`.
3. **Make `privileged-init`/`maintenance-mode` CPU/memory requests configurable via the CR** instead of hardcoded 500m/200Mi each — helps any resource-constrained dev/CI environment. Location: `controllers/cassandra_init_containers.go` (~line 85-86, and the analogous spot for `maintenance-mode`).
4. **Fix `podsConfigMapData` to skip only the not-yet-scheduled pod's entry** instead of aborting the whole ConfigMap update for every pod in the cluster. Location: `controllers/cassandra_pods_config.go:76-82`.

Item 1 is probably the highest-value, lowest-risk fix — it directly addresses the actual crash, is easy to reason about, and doesn't require any new API surface. Items 2-4 are real but lower-urgency robustness gaps that this session's debugging happened to surface.
