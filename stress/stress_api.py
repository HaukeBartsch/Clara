#!/usr/bin/env python3
"""CLARA API stress test — drives the running HTTP API from the outside.

No dependencies beyond the Python 3 standard library; it talks to the API
exactly like any external client would (curl-style requests over one
keep-alive connection).

Phases (timed individually):

1. setup   — create N projects via POST /api/v1/projects, each with a random
             1..200 events, 1..100 instruments and up to 10,000 fields spread
             over the instruments, created in one bulk call per instrument
             (POST .../fields/bulk, REQ-API-132), plus an
             all-instruments-to-all-events mapping. The data dictionary is
             mixed: plain text, validated text (integer, floating point,
             date, datetime) and choice fields (dropdown, radio with numeric
             codes). One full-permission project API token per project comes
             from PUT /api/v1/projects/{id}/users/{uid} (role-less member).
2. import  — generate random record rows for every project, shuffle them into
             one globally random order and bulk-import them through the data
             API (POST /api/ content=record action=import) as batches of
             data[i][…] rows. Values are generated per field type so every
             row passes validation (choice codes, integer/float grammar,
             canonical dates).
3. export  — export every project twice as CSV through the data API
             (content=record returnFormat=csv): once raw (choice codes) and
             once with rawOrLabel=label (choice labels), both streamed to
             disk as <project>.raw.csv / <project>.label.csv.

Alongside those phases a sampler thread polls GET /api/v1/system/metrics
(§4.25, REQ-API-163) every --metrics-interval seconds — one poll costs the
server a runtime.ReadMemStats and counts as an admin request, so the cadence
stays low; it shares this process with the phases and costs milliseconds per
poll. Every snapshot is flattened into named measures (process.*, http[route].*
…, db[VERB].*, db.pool.*, derived.*) and appended to <out>/metrics.jsonl, so a
series survives an aborted run.

The report names each phase's wall time, the CSV size on disk in megabytes and
the throughput, then lists every measure sampled over time with its start value,
end value, mean and standard deviation — printed and written to
<out>/report.txt, plus <out>/metrics_summary.csv (same numbers, one row per
measure, for plotting).

Everything runs sequentially on purpose: SQLite serializes its writers, so
concurrent callers would measure lock waiting rather than API throughput.

Authentication needs the internal service token (X-Internal-Service-Token)
and a bootstrap administrator: the script logs in once with
POST /api/v1/auth/login (source "local") and uses the returned user id as
X-Internal-User-Id on every administration call — the same trust model nginx
uses.

Usage (see run_stress.sh for a launcher that starts a throwaway server):

    python3 stress_api.py --base-url http://127.0.0.1:8099 \
        --service-token dev-internal-token \
        --email admin@example.org --password secret
"""

import argparse
import csv
import datetime
import http.client
import json
import os
import random
import statistics
import string
import sys
import threading
import time
import urllib.parse
from urllib.parse import urlparse


# --- HTTP plumbing -----------------------------------------------------------

class API:
    """One keep-alive connection to the API, curl-style."""

    def __init__(self, base_url, service_token, timeout=None):
        u = urlparse(base_url)
        self.host, self.port = u.hostname, u.port or (443 if u.scheme == "https" else 80)
        self.https = u.scheme == "https"
        self.service_token = service_token
        self.timeout = timeout    # None = block forever, as a client would
        self.user_id = None

    def _connect(self):
        connector = http.client.HTTPSConnection if self.https else http.client.HTTPConnection
        return connector(self.host, self.port, timeout=self.timeout)

    def request(self, method, path, headers=None, body=None, want=(200,), stream=False):
        """Send one request; retry a dropped keep-alive connection once."""
        hdrs = dict(headers or {})
        for attempt in (1, 2):
            conn = self._connect()
            try:
                conn.request(method, path, body=body, headers=hdrs)
                resp = conn.getresponse()
                if resp.status not in want:
                    payload = resp.read()
                    conn.close()
                    raise RuntimeError(
                        f"{method} {path}: HTTP {resp.status}, want {want} — "
                        f"{payload[:512].decode('utf-8', 'replace')}")
                if stream:
                    return resp, conn  # caller reads and closes
                payload = resp.read()
                conn.close()
                return payload
            except (http.client.HTTPException, ConnectionError, OSError) as e:
                conn.close()
                if attempt == 2:
                    raise RuntimeError(f"{method} {path}: {e}") from e

    # --- administration surface (service token + acting user header) ---

    def admin(self, method, path, body=None, want=(200,)):
        headers = {
            "X-Internal-Service-Token": self.service_token,
            "X-Internal-User-Id": str(self.user_id),
        }
        # A read carries no body — and no Content-Type without one; a write
        # always sends at least the empty object the §4 handlers parse.
        payload = None
        if method not in ("GET", "HEAD"):
            headers["Content-Type"] = "application/json"
            payload = json.dumps(body or {})
        answer = self.request(method, path, headers, payload, want)
        return json.loads(answer) if answer else None

    def login(self, email, password):
        """POST /api/v1/auth/login — exempt from the user header; the
        credential rides in the body (Authentication §2.3)."""
        headers = {
            "X-Internal-Service-Token": self.service_token,
            "Content-Type": "application/json",
        }
        body = json.dumps({"email": email, "password": password, "source": "local"})
        user = json.loads(self.request("POST", "/api/v1/auth/login", headers, body))
        self.user_id = user["id"]

    # --- data API (single POST /api/, form-encoded) ---

    def data_api(self, fields, want=(200,), stream=False):
        headers = {"Content-Type": "application/x-www-form-urlencoded"}
        return self.request("POST", "/api/", headers, urllib.parse.urlencode(fields),
                            want, stream)


# --- stress phases -----------------------------------------------------------

LETTERS = string.ascii_lowercase
ALNUM = string.ascii_lowercase + string.digits

# Weighted mix of dictionary field kinds (sums to 100). Plain text keeps the
# bulk of a realistic dictionary; the validated and choice kinds exercise the
# validation pipeline on import and label rendering on export.
FIELD_MIX = [
    ("text", 30),
    ("integer", 15),
    ("floating point", 10),
    ("date", 10),
    ("datetime", 5),
    ("dropdown", 15),
    ("radio", 15),
]
MIX_KINDS = [k for k, _ in FIELD_MIX]
MIX_WEIGHTS = [w for _, w in FIELD_MIX]

# Canonical storage grammars the import pipeline accepts (record.go):
# date Y-m-d, datetime "Y-m-d H:i"; integer/floating point per validate.go.
EPOCH = datetime.date(2018, 1, 1)
DATE_SPAN = 3200  # days from EPOCH — stays well inside the calendar


def make_field_spec(rnd, name):
    """One bulk-creatable field dictionary entry of a random kind. Only keys
    the §4.11 field body accepts are emitted (unknown attributes are rejected
    by the API)."""
    kind = rnd.choices(MIX_KINDS, weights=MIX_WEIGHTS)[0]
    spec = {"field_name": name}
    if kind in ("dropdown", "radio"):
        codes = list(range(1, rnd.randint(3, 9)))  # 2..8 numeric codes
        spec["field_type"] = kind
        spec["choices"] = "##".join(f"{c}$Option {c}" for c in codes)
    elif kind == "text":
        spec["field_type"] = "text"
    else:  # validated text
        spec["field_type"] = "text"
        spec["validation_type"] = kind
    return spec


def random_value(rnd, spec):
    """One valid value for the field described by spec — choice code, number
    or canonical date per the field's dictionary entry. Plain text stays
    5..20 alphanumeric characters starting with a letter, so no cell can trip
    the CSV formula guard."""
    kind = spec.get("validation_type") or ""
    if spec["field_type"] in ("dropdown", "radio"):
        return rnd.choice(spec["choices"].split("##")).split("$")[0]
    if kind == "integer":
        return str(rnd.randint(-1_000_000, 1_000_000))
    if kind == "floating point":
        return f"{rnd.uniform(-100000, 100000):.4f}"
    if kind == "date":
        return (EPOCH + datetime.timedelta(days=rnd.randrange(DATE_SPAN))).isoformat()
    if kind == "datetime":
        d = EPOCH + datetime.timedelta(days=rnd.randrange(DATE_SPAN))
        return f"{d.isoformat()} {rnd.randrange(24):02d}:{rnd.randrange(60):02d}"
    return rnd.choice(LETTERS) + "".join(rnd.choice(ALNUM) for _ in range(rnd.randrange(4, 20)))


def setup_projects(api, cfg, rnd):
    """Create every project's structure; returns per-project plans carrying
    the token, unique event names and field names per instrument."""
    projects = []
    totals = {"events": 0, "instruments": 0, "fields": 0}
    mix = {}  # created-field kinds → count, for the report
    start = time.perf_counter()
    for i in range(cfg.projects):
        name = f"stress_p{i:03d}"

        # Project — arm 1 and its first event "baseline" come with creation.
        created = api.admin("POST", "/api/v1/projects",
                            {"project_name": name, "organization": "CLARA Stress"},
                            want=(201,))

        # Role-less membership of the acting administrator → full permissions;
        # the response carries the project's API token (REQ-API-055).
        member = api.admin("PUT", f"/api/v1/projects/{created['id']}/users/{api.user_id}",
                           {}, want=(201,))

        # Events 2..n on arm 1; unique name is <label>_arm_1 by rule.
        n_events = rnd.randint(cfg.min_events, cfg.max_events)
        events = ["baseline_arm_1"]
        for j in range(1, n_events):
            label = f"visit_{j:03d}"
            api.admin("POST", f"/api/v1/projects/{created['id']}/events",
                      {"arm_num": 1, "event_name": label}, want=(201,))
            events.append(f"{label}_arm_1")

        # Instruments in position order; the first one hosts record_id.
        n_instr = rnd.randint(cfg.min_instruments, cfg.max_instruments)
        instruments = []   # names, position order
        instr_ids = []     # database ids, same order
        for j in range(n_instr):
            created_instr = api.admin("POST", f"/api/v1/projects/{created['id']}/instruments",
                                      {"name": f"instr_{j:03d}"}, want=(201,))
            instruments.append(f"instr_{j:03d}")
            instr_ids.append(created_instr["id"])

        # Fields: one per instrument to start, the rest thrown at random
        # instruments. The total is square-skewed inside [n_instr, max_fields]
        # so "up to 10000" holds without every project paying the cap.
        total = n_instr
        if cfg.max_fields > n_instr:
            u = rnd.random()
            total = n_instr + int(u * u * (cfg.max_fields - n_instr))
        counts = [1] * n_instr  # every instrument needs ≥1 field (record_id below)
        for _ in range(total - n_instr):
            counts[rnd.randrange(n_instr)] += 1

        fields_by_instr = {}
        for j, instr in enumerate(instruments):
            specs = []
            if j == 0:
                # The record identifier stays a plain text field.
                specs.append({"field_name": "record_id", "field_type": "text"})
            specs += [make_field_spec(rnd, f"{instr}_f{k:05d}")
                      for k in range(len(specs), counts[j])]
            create_fields_bulk(api, created["id"], instr_ids[j], specs)
            fields_by_instr[instr] = specs
            for spec in specs:
                kind = spec.get("validation_type") or spec["field_type"]
                mix[kind] = mix.get(kind, 0) + 1

        # Mapping: every instrument active in every event of arm 1.
        api.admin("PUT", f"/api/v1/projects/{created['id']}/instrument-event-mapping",
                  {"arm_num": 1, "mapping": {instr: events for instr in instruments}})

        totals["events"] += len(events)
        totals["instruments"] += len(instruments)
        totals["fields"] += total
        projects.append({"id": created["id"], "name": name, "token": member["token"],
                         "events": events, "instruments": instruments,
                         "fields_by_instr": fields_by_instr})
        if (i + 1) % 20 == 0 or i + 1 == cfg.projects:
            print(f"setup: {i + 1}/{cfg.projects} projects "
                  f"({time.perf_counter() - start:.0f}s elapsed)", flush=True)
    return projects, totals, mix, time.perf_counter() - start


def create_fields_bulk(api, project_id, instrument_id, field_specs):
    """All of an instrument's mixed-kind fields in one bulk call (REQ-API-132).
    The batch is all-or-nothing server-side; a single request replaces up to
    thousands of single-field POSTs."""
    body = {"fields": field_specs}
    api.admin("POST",
              f"/api/v1/projects/{project_id}/instruments/{instrument_id}/fields/bulk",
              body, want=(201,))


def generate_rows(projects, cfg, rnd):
    """Compact row specs (record, form, event) for every project — the values
    themselves are generated at send time. Each record fills 1..4 random
    instruments, each at a random event."""
    specs = []
    seq = 0
    for p in projects:
        for _ in range(rnd.randint(cfg.min_records, cfg.max_records)):
            seq += 1
            record = f"REC{seq:07d}"
            n_forms = rnd.randint(1, min(4, len(p["instruments"])))
            for fi in rnd.sample(range(len(p["instruments"])), n_forms):
                specs.append((p, record, p["instruments"][fi], rnd.choice(p["events"])))
    # The globally random import order: one shuffled stream over all projects.
    rnd.shuffle(specs)
    return specs


def import_rows(api, specs, cfg, rnd):
    """POST the shuffled rows in batches of same-project runs (one token per
    call); every row must pass validation — a rejected row fails the test."""
    values = 0

    def flush(batch):
        nonlocal values
        p = batch[0][0]
        form = {"token": p["token"], "content": "record", "action": "import",
                "returnFormat": "json"}
        for i, (_p, record, instr, event) in enumerate(batch):
            base = f"data[{i}]"
            form[f"{base}[record_id]"] = record
            form[f"{base}[form_name]"] = instr
            form[f"{base}[event_name]"] = event
            for spec in p["fields_by_instr"][instr]:
                if spec["field_name"] == "record_id":
                    continue  # the identifier value travels above
                form[f"{base}[{spec['field_name']}]"] = random_value(rnd, spec)
                values += 1
        results = json.loads(api.data_api(form))
        for r in results:
            if r["import_record_id"] == 0:
                raise RuntimeError(f"import rejected {p['name']}/{r['record_id']}"
                                   f"/{r['form_name']}: {r['import_form_name']}")

    start = time.perf_counter()
    print(f"import: {len(specs)} rows shuffled, streaming in batches of "
          f"{cfg.batch} …", flush=True)
    batch, batch_pid = [], None
    for spec in specs:
        if batch and (spec[0]["id"] != batch_pid or len(batch) >= cfg.batch):
            flush(batch)
            batch = []
        batch_pid = spec[0]["id"]
        batch.append(spec)
    if batch:
        flush(batch)
    return values, time.perf_counter() - start


def export_projects(api, projects, out_dir):
    """Stream every project's CSV export twice to <out_dir>: raw (choice codes
    as stored) and label (rawOrLabel=label renders choice labels). Returns
    per-project rows plus the byte totals of both variants."""
    os.makedirs(out_dir, exist_ok=True)

    def stream_one(p, suffix, extra_form):
        path = os.path.join(out_dir, f"{p['name']}{suffix}.csv")
        form = {"token": p["token"], "content": "record",
                "action": "export", "returnFormat": "csv"}
        form.update(extra_form)
        resp, conn = api.data_api(form, want=(200,), stream=True)
        n_bytes = 0
        n_lines = 0
        try:
            with open(path, "wb") as f:
                while True:
                    chunk = resp.read(1 << 16)
                    if not chunk:
                        break
                    f.write(chunk)
                    n_bytes += len(chunk)
                    n_lines += chunk.count(b"\n")
        finally:
            conn.close()
        return n_bytes, max(n_lines - 1, 0)  # rows = lines minus the header

    per_project = []
    raw_bytes = 0
    label_bytes = 0
    total_rows = 0
    start = time.perf_counter()
    for i, p in enumerate(projects):
        n_raw, data_rows = stream_one(p, ".raw", {})
        n_label, _ = stream_one(p, ".label", {"rawOrLabel": "label"})
        raw_bytes += n_raw
        label_bytes += n_label
        total_rows += data_rows
        per_project.append((p["name"], data_rows, n_raw, n_label))
        if (i + 1) % 20 == 0 or i + 1 == len(projects):
            print(f"export: {i + 1}/{len(projects)} projects "
                  f"({mb(n_raw + n_label):.1f} MB last)", flush=True)
    return per_project, raw_bytes, label_bytes, total_rows, time.perf_counter() - start


def mb(n):
    return n / (1 << 20)


# --- metrics over time -------------------------------------------------------

METRICS_PATH = "/api/v1/system/metrics"
MAX_POLL_FAILURES = 3        # consecutive failures after which sampling gives up

# Sections of the report's metrics block, in print order: (title, prefixes). A
# measure appears under the first title whose prefix it starts with, so fields
# the endpoint gains later (§4.25's schema is add-only) report under "Other".
METRIC_SECTIONS = [
    ("Derived per-interval rates", ("derived.",)),
    ("Server process", ("uptime_seconds", "process.")),
    ("HTTP totals and routes", ("http.requests_total", "http[")),
    ("Database pool and dials", ("db.",)),
    ("Database statements per verb", ("db[",)),
    ("Data API calls per content", ("data_api[",)),
]


def num(v):
    """A snapshot value as a float; absent or non-numeric counts as 0 (sums)."""
    return float(v) if isinstance(v, (int, float)) and not isinstance(v, bool) else 0.0


def flatten_snapshot(snap):
    """Every numeric leaf of one §4.25 snapshot as measure → value, with the
    per-route / per-content / per-verb entries keyed by their label:
    http[POST /api/].p95_ms, data_api[record].count, db[INSERT].rows_total.

    Generic on purpose — a field the endpoint gains later is a measure here
    without touching this script. Histogram bucket lists are skipped; their
    content reaches the report through the series' own count, sum and
    percentiles.
    """
    out = {}

    def walk(prefix, node):
        for key, value in node.items():
            name = f"{prefix}.{key}" if prefix else str(key)
            if isinstance(value, bool):
                continue
            if isinstance(value, (int, float)):
                out[name] = float(value)
            elif isinstance(value, dict):
                walk(name, value)
            # lists (routes, calls, statements, buckets) are keyed by label below

    walk("", snap)
    for route in (snap.get("http") or {}).get("routes") or []:
        walk(f"http[{route.get('route', 'unmatched')}]", route)
    for call in (snap.get("data_api") or {}).get("calls") or []:
        walk(f"data_api[{call.get('content', 'other')}]", call)
    for stmt in (snap.get("db") or {}).get("statements") or []:
        walk(f"db[{stmt.get('verb', 'OTHER')}]", stmt)
    return out


def snapshot_aggregates(snap):
    """Σ over the per-route and per-verb entries — the counters the derived rates
    are taken from, mirroring routeTotals() in web/assets/js/monitoring.js."""
    agg = {"requests": 0.0, "sum_ms": 0.0, "resp_bytes": 0.0,
           "statements": 0.0, "rows": 0.0, "pool_waits": 0.0}
    for route in (snap.get("http") or {}).get("routes") or []:
        agg["requests"] += num(route.get("count"))
        agg["sum_ms"] += num(route.get("sum_ms"))
        agg["resp_bytes"] += num(route.get("resp_bytes_total"))
    for stmt in (snap.get("db") or {}).get("statements") or []:
        agg["statements"] += num(stmt.get("count"))
        agg["rows"] += num(stmt.get("rows_total"))
    agg["pool_waits"] = num(((snap.get("db") or {}).get("pool") or {}).get("wait_count"))
    return agg


def derive_rates(prev, cur):
    """Per-interval rates between two accepted samples: cumulative counters over
    the elapsed time. Δt rides this process's monotonic clock — the snapshot's own
    generated_at has one-second resolution, too coarse next to a 5 s cadence.

    Any counter that went backwards means the API restarted since the previous
    sample: every derived measure reads as a gap for that interval rather than a
    spike (the Monitoring page's rule), and the new sample is the next baseline.
    """
    dt = cur["t"] - prev["t"]
    p, c = prev["agg"], cur["agg"]
    if dt <= 0 or any(c[key] < p[key] for key in c):
        return {}
    out = {"derived.pool_waits_per_s": (c["pool_waits"] - p["pool_waits"]) / dt}
    req = c["requests"] - p["requests"]
    out["derived.requests_per_s"] = req / dt
    if req > 0:  # a mean over no requests is undefined, not zero
        out["derived.mean_latency_ms"] = (c["sum_ms"] - p["sum_ms"]) / req
    resp = c["resp_bytes"] - p["resp_bytes"]
    if resp > 0:
        out["derived.resp_MiB_per_s"] = resp / (1 << 20) / dt
    stmt = c["statements"] - p["statements"]
    if stmt > 0:
        out["derived.db_statements_per_s"] = stmt / dt
    rows = c["rows"] - p["rows"]
    if rows > 0:
        out["derived.db_rows_per_s"] = rows / dt
    return out


class MetricsSampler(threading.Thread):
    """Polls GET /api/v1/system/metrics on a fixed cadence while the phases run.

    One snapshot says where the server stands right now; the point of sampling
    across the whole run is the series — whether latency grows as the database
    grows, whether the heap comes back down, how steady the pool stays. Each
    answer is flattened into named measures and appended to a JSONL file, tagged
    with the phase that was running, so an aborted run keeps its history.

    The cost is one runtime.ReadMemStats (a short stop-the-world) per poll plus
    one admin request, which is why the cadence stays low; the sampler never runs
    a poll in parallel with itself and keeps its own connection, so it cannot
    disturb the phases' streaming. A failed poll is counted and skipped — after
    MAX_POLL_FAILURES in a row sampling stops with the reason recorded, because
    the run's verdict comes from the data API and missing metric samples must not
    abort a long stress run.
    """

    def __init__(self, base_url, service_token, user_id, interval, jsonl_path):
        super().__init__(name="metrics-sampler", daemon=True)
        # Own connection (the phases keep theirs) with a generous socket timeout:
        # a wedged poll must not outlive stop(), and one slow read is never worth
        # blocking the report on.
        self.api = API(base_url, service_token, timeout=max(30.0, 6 * interval))
        self.api.user_id = user_id               # set by login() before sampling starts
        self.interval = interval
        self.samples = []   # {"t", "phase", "agg": {…}, "values": {measure: float}}
        self.failures = 0
        self.last_error = None
        self.gave_up = False
        self._phase = "startup"
        self._consecutive = 0
        self._done = threading.Event()
        self._out = open(jsonl_path, "w")        # truncated: one series per run

    def set_phase(self, phase):
        """Tags every sample taken from here on, so the series cuts per phase."""
        self._phase = phase

    def run(self):
        try:
            while True:
                self.poll()
                if self._done.wait(self.interval):  # interruptible sleep
                    return
        except Exception as e:  # a broken sampler never takes the run down
            self.last_error = f"sampler thread stopped: {e}"

    def poll(self, phase=None):
        """One snapshot → one sample on the series. False on a failed poll."""
        try:
            snap = self.api.admin("GET", METRICS_PATH)
            if not isinstance(snap, dict):
                raise RuntimeError("empty metrics answer")
        except (RuntimeError, ValueError) as e:  # HTTP failure / undecodable body
            self.failures += 1
            self._consecutive += 1
            self.last_error = str(e).splitlines()[0][:160]
            if self._consecutive >= MAX_POLL_FAILURES:
                self.gave_up = True
                self._done.set()
            return False
        self._consecutive = 0
        sample = {"t": time.monotonic(), "phase": phase or self._phase,
                  "agg": snapshot_aggregates(snap), "values": flatten_snapshot(snap)}
        if self.samples:
            sample["values"].update(derive_rates(self.samples[-1], sample))
        self.samples.append(sample)
        self._out.write(json.dumps(sample) + "\n")
        self._out.flush()
        return True

    def stop(self):
        """Ends sampling and takes the closing sample synchronously, so the
        report's end values cover everything the phases did. Returns the series."""
        self._done.set()
        self.join(timeout=2 * self.interval + 30)
        if self.is_alive():  # wedged connection: report what was collected
            self.last_error = "sampler thread did not stop in time"
        elif not self.gave_up:  # a broken endpoint needs no further proof
            self.poll(phase="end")
        self._out.close()
        return self.samples


def summarize(samples):
    """measure → (n, start, end, mean, std) over every sample that carried it."""
    series = {}
    for s in samples:
        for name, value in s["values"].items():
            series.setdefault(name, []).append(value)
    out = {}
    for name, values in series.items():
        out[name] = (len(values), values[0], values[-1], statistics.fmean(values),
                     statistics.stdev(values) if len(values) > 1 else 0.0)
    return out


BYTE_UNITS = (("TiB", 1 << 40), ("GiB", 1 << 30), ("MiB", 1 << 20), ("KiB", 1 << 10), ("B", 1))


def fmt_row(name, values):
    """One measure's four numbers as text, all at the scale of the largest among
    them so a row reads as one measurement — byte-valued measures pick their unit
    the same way, so a std of zero never prints as "0 B" beside "96.0 MiB"."""
    top = max(abs(v) for v in values)
    if name.endswith(("_bytes", "_bytes_total")):
        for unit, scale in BYTE_UNITS:
            if top >= scale or unit == "B":
                digits = 0 if unit == "B" else 1
                return [f"{v / scale:.{digits}f} {unit}" for v in values]
    if top == 0:
        return ["0"] * len(values)
    digits, sep = ((".0f", ",") if top >= 1000 else (".1f", "") if top >= 100
                   else (".2f", "") if top >= 1 else (".4f", ""))
    return [f"{v:{sep}{digits}}" for v in values]


def table_block(title, names, summary):
    """A start/end/mean/std table for one section; nothing when it has no measures."""
    if not names:
        return []
    header = ["measure", "start", "end", "mean", "std"]
    rows = [[name] + fmt_row(name, summary[name][1:]) for name in names]

    def render(cells):
        return "  " + "  ".join(c.ljust(widths[i]) if i == 0 else c.rjust(widths[i])
                                for i, c in enumerate(cells))

    widths = [max(len(r[i]) for r in rows + [header]) for i in range(5)]
    return ["", f"  {title}", render(header)] + [render(row) for row in rows]


def metrics_block(samples, interval, failures, last_error, stopped_early=False):
    """The report's metrics-over-time section: every measure sampled over the run
    with its start value, end value, mean and standard deviation."""
    if interval <= 0:
        return "\nmetrics over time:\n  sampling disabled (--metrics-interval 0)"
    if not samples:
        return ("\nmetrics over time:\n  no samples collected"
                + (f" — {last_error}" if last_error else ""))
    summary = summarize(samples)
    span = samples[-1]["t"] - samples[0]["t"]
    notes = [f"{len(samples)} samples of GET {METRICS_PATH} over "
             f"{span:.0f}s" + (f" ({span / 60:.1f} min)" if span >= 120 else "")
             + f" at {interval:g}s"]
    if stopped_early:
        notes.append("sampling stopped early, so start/end do not span the whole run")
    if failures:
        notes.append(f"{failures} failed poll{'' if failures == 1 else 's'}")
    if last_error:
        notes.append(f"last error: {last_error}")
    lines = ["", "metrics over time:", "  " + "; ".join(notes) + ".",
             "  start/end are the first and last sample, mean/std over all samples (sample",
             "  std). Counters are cumulative since the server started — end − start is the",
             "  run total and their std reflects the ramp, not jitter. Gauges read as",
             "  observed; derived.* are per-interval rates between consecutive samples."]
    covered = set()
    for title, prefixes in METRIC_SECTIONS:
        names = sorted(n for n in summary if n.startswith(prefixes))
        covered.update(names)
        lines += table_block(title, names, summary)
    lines += table_block("Other measures", sorted(set(summary) - covered), summary)
    return "\n".join(lines)


def write_metrics_csv(path, samples):
    """The same summary as the report, one row per measure with raw values, for
    plotting outside this script."""
    with open(path, "w", newline="") as f:
        writer = csv.writer(f)
        writer.writerow(["measure", "n", "start", "end", "mean", "std"])
        for name, stats in sorted(summarize(samples).items()):
            n, start, end, mean, std = stats
            writer.writerow([name, n, round(start, 6), round(end, 6),
                             round(mean, 6), round(std, 6)])


# --- main --------------------------------------------------------------------

def main():
    ap = argparse.ArgumentParser(description=__doc__.splitlines()[0],
                                 formatter_class=argparse.ArgumentDefaultsHelpFormatter)
    ap.add_argument("--base-url", default=os.environ.get("BASE_URL", "http://127.0.0.1:8099"))
    ap.add_argument("--service-token", default=os.environ.get("SERVICE_TOKEN", "dev-internal-token"))
    ap.add_argument("--email", default=os.environ.get("ADMIN_EMAIL", "admin@stress.local"))
    ap.add_argument("--password", default=os.environ.get("ADMIN_PASSWORD", "stress-bootstrap-password"))
    ap.add_argument("--projects", type=int, default=200)
    ap.add_argument("--min-events", type=int, default=1)
    ap.add_argument("--max-events", type=int, default=200)
    ap.add_argument("--min-instruments", type=int, default=1)
    ap.add_argument("--max-instruments", type=int, default=100)
    ap.add_argument("--max-fields", type=int, default=10000,
                    help="upper bound of fields per project (square-skewed)")
    ap.add_argument("--min-records", type=int, default=10)
    ap.add_argument("--max-records", type=int, default=60)
    ap.add_argument("--batch", type=int, default=25, help="rows per import call")
    ap.add_argument("--seed", type=int, default=20261001, help="0 = time-seeded")
    ap.add_argument("--metrics-interval", type=float, default=5.0,
                    help="seconds between GET /api/v1/system/metrics samples "
                         "(0 disables sampling over time)")
    ap.add_argument("--out", default=os.environ.get("STRESS_OUT", "stress-out"),
                    help="directory for the CSV exports, the report and the metric series")
    cfg = ap.parse_args()
    if cfg.seed == 0:
        cfg.seed = int(time.time())
    rnd = random.Random(cfg.seed)
    os.makedirs(cfg.out, exist_ok=True)

    api = API(cfg.base_url, cfg.service_token)
    api.login(cfg.email, cfg.password)
    print(f"logged in as user {api.user_id} at {cfg.base_url} "
          f"(seed {cfg.seed}, {cfg.projects} projects)", flush=True)

    # Sampling runs alongside every phase; None when switched off.
    sampler = MetricsSampler(cfg.base_url, cfg.service_token, api.user_id,
                             cfg.metrics_interval, os.path.join(cfg.out, "metrics.jsonl")) \
        if cfg.metrics_interval > 0 else None
    if sampler:
        sampler.start()
        print(f"sampling {METRICS_PATH} every {cfg.metrics_interval:g}s", flush=True)
    set_phase = sampler.set_phase if sampler else (lambda phase: None)

    try:
        set_phase("setup")
        projects, totals, mix, setup_s = setup_projects(api, cfg, rnd)
        set_phase("import")
        specs = generate_rows(projects, cfg, rnd)
        values, import_s = import_rows(api, specs, cfg, rnd)
        set_phase("export")
        per_project, raw_bytes, label_bytes, csv_rows, export_s = \
            export_projects(api, projects, cfg.out)
    finally:  # the collected series is written out even when a phase failed
        samples = sampler.stop() if sampler else []

    failures = sampler.failures if sampler else 0
    last_error = sampler.last_error if sampler else None
    stopped_early = bool(sampler and sampler.gave_up)
    mix_line = ", ".join(f"{kind}: {n}" for kind, n in sorted(mix.items()))
    report = f"""CLARA API stress report (external client)
base url:             {cfg.base_url}
seed:                 {cfg.seed}
projects:             {len(projects)}
events total:         {totals['events']}
instruments total:    {totals['instruments']}
fields total:         {totals['fields']}
field mix:            {mix_line}
imported rows:        {len(specs)} (values: {values})

setup time:           {setup_s:.1f}s  ({cfg.projects / max(setup_s, 1e-9) * 60:.1f} projects/min)
import time:          {import_s:.1f}s  ({len(specs) / max(import_s, 1e-9):.0f} rows/s, \
{values / max(import_s, 1e-9):.0f} values/s)
export time:          {export_s:.1f}s  ({cfg.projects / max(export_s, 1e-9) * 60:.1f} projects/min)

CSV on disk (raw):    {mb(raw_bytes):.2f} MB in {len(projects)} files
CSV on disk (label):  {mb(label_bytes):.2f} MB in {len(projects)} files \
({csv_rows} data rows each)
csv directory:        {os.path.abspath(cfg.out)}
""" + metrics_block(samples, cfg.metrics_interval, failures, last_error,
                    stopped_early) + "\n"
    if samples:
        write_metrics_csv(os.path.join(cfg.out, "metrics_summary.csv"), samples)

    print("\n" + report, flush=True)
    with open(os.path.join(cfg.out, "report.txt"), "w") as f:
        f.write(report)
        if samples:  # the series files exist only when something was sampled
            f.write("\n")
            for label, name in (("metric series", "metrics.jsonl"),
                                ("metric summary", "metrics_summary.csv")):
                f.write(f"{label:<22}{os.path.abspath(os.path.join(cfg.out, name))}\n")
        f.write("\nper-project exports (name, data rows, raw bytes, label bytes):\n")
        for name, rows, n_raw, n_label in per_project:
            f.write(f"{name},{rows},{n_raw},{n_label}\n")


if __name__ == "__main__":
    try:
        main()
    except (RuntimeError, SystemExit) as e:
        if isinstance(e, SystemExit) and getattr(e, "code", 1) in (0, None):
            raise
        print(f"stress test failed: {e}", file=sys.stderr)
        sys.exit(1)
