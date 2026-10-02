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

The report names each phase's wall time, the CSV size on disk in megabytes and
the throughput; it is printed and written to <out>/report.txt.

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
import datetime
import http.client
import json
import os
import random
import string
import sys
import time
import urllib.parse
from urllib.parse import urlparse


# --- HTTP plumbing -----------------------------------------------------------

class API:
    """One keep-alive connection to the API, curl-style."""

    def __init__(self, base_url, service_token):
        u = urlparse(base_url)
        self.host, self.port = u.hostname, u.port or (443 if u.scheme == "https" else 80)
        self.https = u.scheme == "https"
        self.service_token = service_token
        self.user_id = None

    def _connect(self):
        return http.client.HTTPSConnection(self.host, self.port) if self.https \
            else http.client.HTTPConnection(self.host, self.port)

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
            "Content-Type": "application/json",
        }
        payload = self.request(method, path, headers, json.dumps(body or {}), want)
        return json.loads(payload) if payload else None

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
    ap.add_argument("--out", default=os.environ.get("STRESS_OUT", "stress-out"),
                    help="directory for the CSV exports and the report")
    cfg = ap.parse_args()
    if cfg.seed == 0:
        cfg.seed = int(time.time())
    rnd = random.Random(cfg.seed)

    api = API(cfg.base_url, cfg.service_token)
    api.login(cfg.email, cfg.password)
    print(f"logged in as user {api.user_id} at {cfg.base_url} "
          f"(seed {cfg.seed}, {cfg.projects} projects)", flush=True)

    projects, totals, mix, setup_s = setup_projects(api, cfg, rnd)
    specs = generate_rows(projects, cfg, rnd)
    values, import_s = import_rows(api, specs, cfg, rnd)
    per_project, raw_bytes, label_bytes, csv_rows, export_s = \
        export_projects(api, projects, cfg.out)

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
"""
    print("\n" + report, flush=True)
    with open(os.path.join(cfg.out, "report.txt"), "w") as f:
        f.write(report)
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
