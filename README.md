# Pipeline Health Monitoring Platform

A **data observability / pipeline health monitoring** platform. It periodically checks
the state of data infrastructure entities (Kafka topics, files, database tables, and
later S3, FTP/SFTP, etc.), groups them into logical pipelines, computes an overall
pipeline health status, and raises alerts when something breaks — including when data
that *should* arrive does not.

This document captures the agreed design decisions for the first version.

---

## 1. Core Concepts

### 1.1 Node = Target + Check + Schedule

A "node" (or "instance") is **not** a single monolithic object. It is split into three
distinct, independently reusable sub-entities. This avoids duplicating connection config
and secrets, and lets one target carry multiple checks.

| Sub-entity | Answers | Examples |
|------------|---------|----------|
| **Target** (connection) | *Where* do we look? | Type (kafka / file / db), address, reference to a Credential |
| **Check** (probe) | *What* do we measure and *what condition* is healthy? | Kafka: consumer lag, last-message timestamp, partition count. Table: `row_count > 0`, freshness via `max(updated_at)`. File: existence, size, age, checksum |
| **Schedule** | *How often*, with what timeout / retries? | Interval or cron, per-check timeout, retry policy |

One target may have several checks (e.g. a table checked both for "has fresh data" and
"no duplicates"). Credentials are referenced, never copied into the node.

### 1.2 Pipeline

A **pipeline** is a logical grouping of nodes. It has a name, an overall status, and is
created through the web UI. A pipeline carries **two different kinds of relationship**
between its nodes, which must not be conflated:

1. **Data dependency (flow / order)** — e.g. `step2 → step3`, where data physically
   flows from a file into a table. This drives **root-cause analysis** and **alert
   suppression**.
2. **Boolean composition (`&&`, `||`)** — how individual node statuses *combine* into the
   pipeline status. This expresses **redundancy and alternatives** (primary path OR
   backup path).

Internally a pipeline is a **dependency DAG** for order/flow, with **boolean logic layered
on top** for status rollup. A simple linear pipeline (`1 → 2 → 3`) is just a degenerate DAG.

Supported boolean groupings:

```
node1 && node2
node1 || node2
(node1 && node2) || (node3 && node4)
(node1 || node2) && (node3 || node4)
```

### 1.3 Status Model

Status is **not** binary. The supported states are:

| Status | Meaning |
|--------|---------|
| `OK` | Check ran, condition satisfied |
| `WARNING` | Degraded but functioning (soft threshold breached) |
| `CRITICAL` | Check ran, condition failed |
| `UNKNOWN` | Check could **not** run — expired credentials, network down, timeout. Operationally very different from `CRITICAL` |
| `BLOCKED` | An upstream data dependency is failing, so this node is starved of input. Behaves like `UNKNOWN` in logic, but is attributed to the upstream root cause |
| `DISABLED` | Intentionally turned off |

The distinction between "the node is bad" (`CRITICAL`) and "we couldn't even evaluate it"
(`UNKNOWN` / `BLOCKED`) is fundamental — it prevents false greens and misdirected alerts.

### 1.4 Three-Valued (Kleene) Logic

Because `UNKNOWN` / `BLOCKED` exist, plain binary logic breaks. Status rollup uses
**Kleene strong three-valued logic**, mapping `OK`/`WARNING` → TRUE, `CRITICAL` → FALSE,
`UNKNOWN`/`BLOCKED` → UNKNOWN:

```
A && B:                          A || B:
  OK   && OK      = OK             OK   || anything = OK
  OK   && CRIT    = CRITICAL       CRIT || CRIT     = CRITICAL
  CRIT && anything= CRITICAL       CRIT || UNKNOWN  = UNKNOWN
  OK   && UNKNOWN = UNKNOWN        UNKNOWN || UNKNOWN = UNKNOWN
  UNKNOWN && UNKNOWN = UNKNOWN
```

`WARNING` counts as "passing" for composition, but the pipeline's displayed severity
surfaces the worst non-fatal state among the nodes that determine the result.

### 1.5 Independent Checking vs Dependency-Aware Alerting

Key rule: **checks run independently on their own schedules; only alerting and status
rollup are dependency-aware.**

When an upstream node fails, a downstream node will inevitably fail too — but that is not
its own fault. So:

- The downstream node is marked **`BLOCKED`** ("waiting for input from step N"), not
  `CRITICAL`.
- The alert fires **once**, at the **root cause** (the upstream node). Downstream nodes
  stay silent → no alert storm.
- Checks still run independently, because the most valuable alert is **"upstream GREEN
  but downstream RED"** (e.g. file arrived but table didn't load → the loader is broken).
  That case is only detectable if the downstream check runs on its own.
- For **expensive checks** (heavy DB queries), an optional flag *"do not run while
  upstream is not OK"* is available as a deliberate optimization — never as the basis of
  the logic.

### 1.6 Freshness / Dead Man's Switch

Absence of data is as important as bad data. A dedicated check type detects **expected
events that never happened**:

- "This file must arrive by 06:00 daily."
- "A message must land in this topic at least every 5 minutes."

If the expected event does not occur within its window, an alert fires.

---

## 2. Credentials

Access to each node uses credentials (login/password, AWS role, etc.). Decisions:

- Credentials are a **standalone, reusable entity** referenced by nodes — not copied per
  node.
- Stored **encrypted at rest**.
- Support **AWS IAM role assumption**, not only static keys.
- **Masked**: even the operator who created a credential cannot read the secret back
  (write-only; displayed as `••••`).
- **Test connection** on save — verify access immediately.
- Rotation and expiry supported.
- *(Roadmap)* Integration with an external vault (HashiCorp Vault / AWS Secrets Manager)
  so the platform need not be the owner of secrets.

---

## 3. Alerts

Alerts are notifications about process violations via Slack or Telegram/WhatsApp, created
in the web UI. The notification send is only a small part — the lifecycle is what prevents
alert fatigue.

- **Channels as reusable entities** — a specific Slack channel / Telegram chat is
  configured once; alerts reference it.
- **Acknowledge / mute / silence** — an operator can take ownership or mute a specific
  alert.
- **Maintenance windows** — suppress alerts during planned downtime.
- **Deduplication & throttling** — do not re-send the same alert repeatedly.
- **Recovery notifications** — "node returned to OK".
- **Escalation** — if not acknowledged within N minutes, raise severity / switch channel.
- **Cascade suppression** — downstream `BLOCKED` nodes do not alert; only the root cause does.
- **Actual vs expected in the body** — e.g. `row_count = 0, expected > 100`;
  `file modified 26h ago, threshold 24h`.

---

## 4. Users & Roles

| Role | Access |
|------|--------|
| **Admin** | Full access to all functionality, including adding new users and system config |
| **Operator** | Configures nodes, pipelines, and alerts |
| **Manager** | Read access to dashboards |

Refinements:

- **Scoping / multi-tenancy** — operators should manage *their own* team's/project's
  pipelines, not everything globally. A team/project boundary is introduced from the start,
  even if v1 runs with a single team.
- **Secret access is a separate permission**, not implied by the operator role.
- **Environments** (dev / staging / prod) — the same pipeline definition across
  environments, with potentially different permissions.

---

## 5. Architecture

### 5.1 Worker Pool (built from day one)

The execution model is decoupled so it scales horizontally without a later rewrite:

- **Scheduler is separate from workers.** The scheduler decides *what* runs *when*;
  workers *execute*. A **queue** sits between them.
- **Each check is a job** with a timeout, retries, and idempotency. Workers are
  **stateless**; results are written to a store.
- **Status engine** recomputes pipeline status whenever a new result arrives (applying the
  dependency DAG + Kleene logic from §1).
- **Isolation by type** — a hung FTP/DB check must not block Kafka checks. Minimum: hard
  timeouts; better: separate queues / worker classes per node type.
- **Per-target concurrency limit** — never hammer a single DB with dozens of simultaneous
  checks.
- **Self-monitoring** — the platform monitors itself via an external heartbeat. If the
  checker is down, everything must not appear "green" simply because no checks ran.

### 5.2 Extensibility

Node types are implemented as **plugin/adapter interfaces**. The v1 set (Kafka / files /
DB) is deliberately diverse — stream / object / query — to validate the adapter interface
early. Adding new types (S3, FTP/SFTP, HTTP, Redis, BigQuery, GCS, Azure Blob, …) must not
require core changes.

---

## 6. Scope

### v1 (MVP)

- Node types: **Kafka topics, files, database tables**
- Target / Check / Schedule split
- Pipelines with boolean grouping + dependency DAG
- Full status model + Kleene logic + freshness checks
- Encrypted, reusable credentials (login/password, AWS role) with masking and test-connection
- Alerts via Slack / Telegram / WhatsApp with the lifecycle in §3
- Roles: admin / operator / manager (with team scoping)
- Scheduler + queue + stateless worker pool
- Web UI for all configuration

### Roadmap

- Additional node types (S3, FTP/SFTP, HTTP, …) via the adapter interface
- External vault integration for secrets
- **Public API + config-as-code** (export/import pipeline definitions as YAML/JSON) for
  versioning, bulk operations, and disaster recovery — UI stays primary but sits on top of
  the API
- **Status history (time series)** for dashboards, SLA reports, MTTR
- **Audit log** — who changed what (check edits, user management)
- Full multi-tenancy across teams/projects/environments