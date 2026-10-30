#!/usr/bin/env python3
"""CLARA Incoming-project test — the FIONA intake project at production scale.

Loads the Incoming project from its ODM document (assets/Incoming_odm_2026-10-25.xml)
and fills it the way FIONA does: one record per arriving DICOM study, keyed by its
StudyInstanceUID, carrying 5..20 Series instances and one Transfers request. Then it
times the Record Status Dashboard's read of the full project and exports the project
as ODM, checking the file against what was imported. Standard library only, driven
over HTTP like any external client; the HTTP plumbing, the metrics sampler and the
report helpers are stress_api.py's.

Phases (timed individually):

1. setup     — POST /api/v1/projects/import/odm creates the project from the ODM
               document (§4.29); its instruments, repeating events and field dictionary
               are read back over §4.10/§4.11, and a role-less membership of the acting
               administrator answers with the project API token (REQ-API-055). A project
               that already carries the name stops the run unless --resume adopts it.
2. import    — --records records through the data API (POST /api/ content=record
               action=import, the JSON `data` encoding of REQ-API-031, --batch rows per
               call). Per record: one Incoming Studies row, --min-series..--max-series
               Series instances and one Transfers instance, the instances addressed by
               redcap_repeat_instrument/_instance (REQ-API-155). Every value-carrying
               field gets a value — the @READONLY fields FIONA writes back included — and
               every checkbox box is checked. Values are DICOM-shaped (UIDs, DA/TM strings,
               AE titles, sequence names) and drawn from a per-record seed, so the same
               --seed regenerates any record exactly; --resume and the ODM check rely on
               that. Every row must be accepted — a rejected row fails the test.
3. dashboard — the Record Status Dashboard's read, GET /api/v1/projects/{id}/record-status
               (§4.13), --dashboard-runs times: time to first byte, total time, size. The
               stream is checked to list every record once and every Series and Transfers
               instance. The completeness read the dashboard's dots are built from
               (GET …/record-completeness, REQ-API-171) is timed the same way. What the
               browser adds on top — the PHP data region and the Tabulator grid — is
               measured by e2e/tests/incoming-dashboard.spec.ts against the same project.
4. odm       — GET /api/v1/projects/{id}/export/odm per --odm-versions, streamed to <out>:
               time to first byte, total time, size. The file is then parsed as a stream
               and every SubjectData compared with its regenerated record — one subject
               per record, every instance, every value — so a truncated, incomplete or
               altered document fails the test.

Alongside the phases stress_api.MetricsSampler polls GET /api/v1/system/metrics every
--metrics-interval seconds (<out>/metrics.jsonl, <out>/metrics_summary.csv); the
report goes to stdout and <out>/report.txt.

The default scale is the production one — 400,000 records, about 5.8 million import
rows — which runs for hours. Each import row is its own transaction (REQ-API-035),
so rows/s is the number to watch; try --records 2000 first. An interrupted run is
continued with --resume and the same --seed/--min-series/--max-series: the import
picks up at the highest record stored (re-sending it, which updates it in place) and
fills up to --records.

Usage (run_incoming.sh starts a throwaway API server around this script):

    ./run_incoming.sh                          # 400,000 records
    ./run_incoming.sh --records 2000           # quick run

Browser-side dashboard timing needs the PHP layer, so run against e2e/dev-stack.sh:

    ./e2e/dev-stack.sh                         # API :8085, web :8090
    BASE_URL=http://127.0.0.1:8085 SERVICE_TOKEN=clara-dev-481516-svc \\
      ADMIN_EMAIL=admin@example.org ADMIN_PASSWORD=clara-dev-481516-pw \\
      ./stress/run_incoming.sh --no-server
    CLARA_WEB_URL=http://127.0.0.1:8090 CLARA_PERF_PROJECT=Incoming \\
      npx playwright test --config e2e/playwright.config.ts incoming-dashboard --project chromium
"""

import argparse
import datetime
import json
import os
import random
import re
import statistics
import sys
import time
import urllib.parse
import xml.etree.ElementTree as ET

import stress_api
from stress_api import API, MetricsSampler, mb, metrics_block, record_cells, write_metrics_csv

HERE = os.path.dirname(os.path.abspath(__file__))
DEFAULT_ODM = os.path.join(os.path.dirname(HERE), "assets", "Incoming_odm_2026-10-25.xml")

# The repeating instruments of the Incoming design. The study instrument is not named
# here: it is whichever instrument holds the record identifier (GD-8).
SERIES_INSTRUMENT = "Series"
TRANSFERS_INSTRUMENT = "Transfers"

# §3.7.2 import answer per row: 1 added (the record is new), 2 updated, 0 rejected.
IMPORT_ADDED, IMPORT_UPDATED = 1, 2

# --- DICOM-shaped values -----------------------------------------------------

# pydicom's documented root for generated example UIDs; the component after it says what
# the UID names (1 study, 2 series, 3 the anonymized copy a transfer produces).
UID_ROOT = "1.2.826.0.1.3680043.8.498"
STUDY_UID = re.compile(r"^" + re.escape(UID_ROOT) + r"\.1\.(\d+)$")

EPOCH = datetime.datetime(2019, 1, 1, 7, 0)
SPAN_MINUTES = 7 * 365 * 24 * 60   # studies arrive over seven years

INSTITUTIONS = ["Haukeland University Hospital", "Haraldsplass Deaconess Hospital",
                "Stavanger University Hospital", "Haugesund Hospital",
                "Førde Central Hospital", "Voss Hospital", "Odda Hospital"]
SENDER_AETS = ["HUS_MR1", "HUS_MR2", "HUS_MR3T", "HUS_CT1", "HUS_PETCT",
               "SUS_MR1", "HDS_MR", "FSS_CT", "VOSS_MR"]
CALLED_AETS = ["FIONA", "FIONA_RES", "FIONA_QC"]
FAMILY_NAMES = ["Hansen", "Johansen", "Olsen", "Larsen", "Andersen", "Pedersen",
                "Nilsen", "Kristiansen", "Jensen", "Karlsen", "Berg", "Haugen"]
GIVEN_NAMES = ["Kari", "Ola", "Ingrid", "Lars", "Sofie", "Jonas", "Nora",
               "Emil", "Sigrid", "Henrik", "Astrid", "Magnus"]
STUDY_DESCRIPTIONS = ["MR Caput", "MR Caput med kontrast", "MR Columna lumbalis",
                      "MR Kne høyre", "MR Prostata", "MR Mamma bilateral",
                      "CT Thorax", "CT Abdomen/Bekken", "PET-CT Helkropp",
                      "Research^ABCD baseline", "Research^NeuroBergen fMRI"]
SERIES_DESCRIPTIONS = ["localizer", "t1_mprage_sag_p2_iso", "t2_tse_tra", "t2_flair_sag_p2_iso",
                       "ep2d_diff_b1000_tra", "ep2d_diff_b1000_tra_ADC", "ep2d_bold_rest",
                       "swi_tra", "t1_vibe_fs_tra_post", "dti_64dir", "gre_field_mapping",
                       "pcasl_3d", "MPR Range", "PhoenixZIPReport"]
SEQUENCE_NAMES = ["*tfl3d1_16ns", "*tse2d1_15", "*epse2d1_86", "*fl3d1r", "*spc_314ns",
                  "*swi3d1r", "*ep_b1000t", "*fl2d1", "*tgse3d1_512"]
CLASSIFY_TAGS = ["MR", "CT", "PT", "T1", "T2", "FLAIR", "DWI", "DTI", "fMRI", "SWI",
                 "localizer", "3D", "2D", "axial", "sagittal", "coronal", "contrast",
                 "derived", "original", "mosaic"]
TRANSFER_PROJECTS = ["ABCD", "NeuroBergen", "PROSTATE-AI", "MS-Pain", "BreastScreen",
                     "MoBa-MRI", "STROKE-REG"]
TRANSFER_EVENTS = ["baseline_arm_1", "followup_1_arm_1", "followup_2_arm_1", "year_2_arm_1"]
ERROR_MESSAGES = [
    "Anonymization rule set 3 rejected tag (0010,1010) PatientAge; value removed and retried.",
    "Send to rDMA timed out after 300 s\nretry 1 of 3 succeeded",
    "Burned-in annotation detected in 2 series; pixel anonymization applied.",
    "C-STORE to research PACS refused: association rejected (0x0002), retried on secondary node.",
]


class Context:
    """What the values of one row derive from: the record, the instance and the
    record's own random stream — consumed in dictionary order, so a record is the same
    record every time it is generated."""

    def __init__(self, n, rnd):
        self.n = n
        self.rnd = rnd
        self.uid = study_uid(n)
        self.at = EPOCH + datetime.timedelta(minutes=rnd.randrange(SPAN_MINUTES))
        self.arrived = self.at + datetime.timedelta(minutes=rnd.randrange(5, 240))
        self.requested = self.arrived + datetime.timedelta(days=rnd.randrange(0, 30),
                                                           minutes=rnd.randrange(1440))
        self.sent = self.requested + datetime.timedelta(minutes=rnd.randrange(5, 60))
        self.instance = 1


def study_uid(n):
    return f"{UID_ROOT}.1.{n}"


def ymd_hm(moment):
    """The canonical datetime grammar the import accepts (record.go): Y-m-d H:i."""
    return moment.strftime("%Y-%m-%d %H:%M")


# Field name → value. A field the Incoming design gains later and this table does not
# know falls back to stress_api's type-driven generator (record_cells), so the run
# still fills it.
VALUES = {
    "study_instance_uid": lambda c: c.uid,
    "study_institution_name": lambda c: c.rnd.choice(INSTITUTIONS),
    "study_aetitle_sender": lambda c: c.rnd.choice(SENDER_AETS),
    "study_aetitle_addressed": lambda c: c.rnd.choice(CALLED_AETS),
    "study_patient_name": lambda c: f"{c.rnd.choice(FAMILY_NAMES)}^{c.rnd.choice(GIVEN_NAMES)}",
    "study_patient_id": lambda c: str(c.rnd.randrange(10**7, 10**8)),
    "study_description": lambda c: c.rnd.choice(STUDY_DESCRIPTIONS),
    "study_date": lambda c: c.at.strftime("%Y%m%d"),
    "study_time": lambda c: c.at.strftime("%H%M%S"),
    "study_accession_number": lambda c: f"NHB{c.rnd.randrange(10**8, 10**9)}",
    "study_arrival_date": lambda c: ymd_hm(c.arrived),
    "series_instance_uid": lambda c: f"{UID_ROOT}.2.{c.n}.{c.instance}",
    "series_description": lambda c: c.rnd.choice(SERIES_DESCRIPTIONS),
    "series_date": lambda c: c.at.strftime("%Y%m%d"),
    "series_time": lambda c: (c.at + datetime.timedelta(minutes=2 * c.instance)).strftime("%H%M%S"),
    "series_num_files": lambda c: str(c.rnd.randint(1, 640)),
    "series_sequence_name": lambda c: c.rnd.choice(SEQUENCE_NAMES),
    "series_classify_types": lambda c: ",".join(c.rnd.sample(CLASSIFY_TAGS, c.rnd.randint(2, 5))),
    "transfer_requested_date": lambda c: ymd_hm(c.requested),
    "transfer_project_name": lambda c: c.rnd.choice(TRANSFER_PROJECTS),
    "transfer_name": lambda c: f"P{c.n:07d}",
    "transfer_event_name": lambda c: c.rnd.choice(TRANSFER_EVENTS),
    "transfer_date": lambda c: ymd_hm(c.sent),
    "transfer_mapped_uid": lambda c: f"{UID_ROOT}.3.{c.n}",
    "transfer_error_message": lambda c: c.rnd.choice(ERROR_MESSAGES),
    "transfer_active_proc_time": lambda c: str(c.rnd.randint(5, 3600)),
}


def instrument_cells(plan, instrument, ctx):
    """The (name, value) pairs one instrument row carries — every value-carrying field,
    every checkbox box checked; display rows and the identifier (framing, set by the
    caller) contribute nothing."""
    for spec in plan["fields"][instrument]:
        name = spec["field_name"]
        if name == plan["identifier"]:
            continue
        if name in VALUES:
            yield name, VALUES[name](ctx)
        else:
            yield from record_cells(ctx.rnd, spec, fill_all=True)


def series_count(cfg, n):
    """The record's Series instance count — its random stream's first draw, so the
    dashboard check knows it without generating the record."""
    return random.Random(f"{cfg.seed}/{n}").randint(cfg.min_series, cfg.max_series)


def record_rows(plan, cfg, n):
    """Import rows of record n: the study row, its Series instances, one Transfers
    request. Deterministic in (seed, n)."""
    rnd = random.Random(f"{cfg.seed}/{n}")
    n_series = rnd.randint(cfg.min_series, cfg.max_series)
    ctx = Context(n, rnd)
    ident, event = plan["identifier"], plan["event"]

    study = {ident: ctx.uid, "redcap_event_name": event, "form_name": plan["study"]}
    study.update(instrument_cells(plan, plan["study"], ctx))
    study[ident] = ctx.uid  # the identifier field's value is the record id
    rows = [study]
    for instrument, count in ((SERIES_INSTRUMENT, n_series), (TRANSFERS_INSTRUMENT, 1)):
        for instance in range(1, count + 1):
            ctx.instance = instance
            row = {ident: ctx.uid, "redcap_event_name": event,
                   "redcap_repeat_instrument": instrument, "redcap_repeat_instance": str(instance)}
            row.update(instrument_cells(plan, instrument, ctx))
            rows.append(row)
    return rows


FRAMING_KEYS = ("redcap_event_name", "form_name", "redcap_repeat_instrument", "redcap_repeat_instance")


def stored_values(row):
    """The values a row stores: its cells minus the framing keys, and minus the
    identifier on an instance row, where it only addresses the record."""
    framing = sum(1 for k in FRAMING_KEYS if k in row)
    return len(row) - framing - (1 if "redcap_repeat_instrument" in row else 0)


def expected_values(plan, rows):
    """(instrument, instance) → {item name: value} for a record's rows: the shape the ODM
    check reads a SubjectData into. The identifier belongs to the study row only."""
    out = {}
    for row in rows:
        instrument = row.get("redcap_repeat_instrument") or row["form_name"]
        instance = int(row.get("redcap_repeat_instance") or 1)
        cells = {k: v for k, v in row.items() if k not in FRAMING_KEYS and v != ""}
        if instrument != plan["study"]:
            cells.pop(plan["identifier"], None)
        out[(instrument, instance)] = cells
    return out


# --- setup -------------------------------------------------------------------

def find_project(api, name):
    return next((p for p in api.admin("GET", "/api/v1/projects") or []
                 if p.get("project_name") == name), None)


def import_odm(api, path, name):
    """POST the ODM document (§4.29); the report says what was built."""
    with open(path, "rb") as f:
        body = f.read()
    headers = {"X-Internal-Service-Token": api.service_token,
               "X-Internal-User-Id": str(api.user_id),
               "Content-Type": "application/xml"}
    query = urllib.parse.urlencode({"project_name": name})
    report = json.loads(api.request("POST", f"/api/v1/projects/import/odm?{query}",
                                    headers, body, want=(201,)))
    if report.get("problems"):
        raise RuntimeError(f"ODM import reported problems: {report['problems'][:5]}")
    return report


def read_plan(api, project_id, name):
    """The project as this run uses it: token, event, the study instrument (the one
    holding the record identifier, GD-8), the two repeating ones and every dictionary."""
    instruments = api.admin("GET", f"/api/v1/projects/{project_id}/instruments") or []
    fields = {}
    for instrument in instruments:
        fields[instrument["name"]] = api.admin(
            "GET", f"/api/v1/projects/{project_id}/instruments/{instrument['id']}/fields") or []
    with_fields = [i for i in instruments if fields[i["name"]]]
    if not with_fields:
        raise RuntimeError(f"{name}: no instrument carries fields")
    study = with_fields[0]
    identifier = next(f["field_name"] for f in fields[study["name"]] if f["field_type"] != "description")
    by_name = {i["name"]: i for i in instruments}
    for needed in (SERIES_INSTRUMENT, TRANSFERS_INSTRUMENT):
        if needed not in by_name:
            raise RuntimeError(f"{name}: no instrument '{needed}' — is this the Incoming design?")
    events = set(by_name[SERIES_INSTRUMENT]["repeating_events"]) & set(by_name[TRANSFERS_INSTRUMENT]["repeating_events"])
    if not events:
        raise RuntimeError(f"{name}: {SERIES_INSTRUMENT} and {TRANSFERS_INSTRUMENT} repeat at no common event")
    return {"id": project_id, "name": name, "event": sorted(events)[0],
            "study": study["name"], "identifier": identifier, "fields": fields,
            "token": stress_api.project_token(api, project_id)}


def setup(api, cfg):
    """Create the project from the ODM document, or adopt it under --resume."""
    start = time.perf_counter()
    existing = find_project(api, cfg.project_name)
    if existing and not cfg.resume:
        raise RuntimeError(f"a project named '{cfg.project_name}' already exists — pass --resume "
                           f"to continue filling it, or --project-name for a fresh one")
    if existing:
        project_id, built = existing["id"], None
        print(f"setup: adopting existing project {cfg.project_name} (id {project_id})", flush=True)
    else:
        built = import_odm(api, cfg.odm, cfg.project_name)
        project_id = built["project_id"]
        print(f"setup: ODM import built {cfg.project_name} (id {project_id}): "
              f"{built['instruments']} instruments, {built['fields']} fields", flush=True)
    plan = read_plan(api, project_id, cfg.project_name)
    return plan, built, time.perf_counter() - start


# --- import ------------------------------------------------------------------

def highest_stored(api, plan):
    """The highest record number stored under this run's UID scheme, 0 for none. Only
    the study instrument's identifier column is read."""
    rows = json.loads(api.data_api({"token": plan["token"], "content": "record", "action": "export",
                                    "format": "json", "returnFormat": "json",
                                    "fields[0]": plan["identifier"], "forms[0]": plan["study"]})) or []
    found = (STUDY_UID.match(str(row.get(plan["identifier"], ""))) for row in rows)
    return max((int(m.group(1)) for m in found if m), default=0)


def import_records(api, plan, cfg, first):
    """Import records first..--records in batches of --batch rows. A row the API rejects
    fails the test; a record that already existed fails it too, except the first one of
    a resumed run, which an interruption may have left half-written."""
    rows_done = values = records_done = 0
    batch, fresh = [], []
    total = cfg.records - first + 1
    start = last_print = time.perf_counter()

    def flush():
        nonlocal rows_done, values
        form = {"token": plan["token"], "content": "record", "action": "import",
                "format": "json", "returnFormat": "json",
                "data": json.dumps(batch, ensure_ascii=False, separators=(",", ":"))}
        results = json.loads(api.data_api(form))
        if len(results) != len(batch):
            raise RuntimeError(f"import answered {len(results)} rows for {len(batch)} sent")
        for result, row, is_new in zip(results, batch, fresh):
            if result["import_record_id"] not in (IMPORT_ADDED, IMPORT_UPDATED):
                raise RuntimeError(f"import rejected {result['record_id']} "
                                   f"{row.get('redcap_repeat_instrument') or row.get('form_name')} "
                                   f"#{row.get('redcap_repeat_instance', 1)}: {result['import_form_name']}")
            if is_new and result["import_record_id"] != IMPORT_ADDED:
                raise RuntimeError(f"record {result['record_id']} already existed — "
                                   f"pass --resume to continue a stored run")
        rows_done += len(batch)
        values += sum(stored_values(row) for row in batch)
        batch.clear()
        fresh.clear()

    print(f"import: records {first}..{cfg.records} ({total}) in batches of {cfg.batch} rows", flush=True)
    for n in range(first, cfg.records + 1):
        for i, row in enumerate(record_rows(plan, cfg, n)):
            batch.append(row)
            fresh.append(i == 0 and not (cfg.resume and n == first))
        records_done += 1
        if len(batch) >= cfg.batch:
            flush()
            now = time.perf_counter()
            if now - last_print >= 30:
                last_print = now
                rate = records_done / (now - start)
                eta = (total - records_done) / max(rate, 1e-9)
                print(f"import: {records_done}/{total} records, {rows_done} rows "
                      f"({rows_done / (now - start):.0f} rows/s, ETA {eta / 3600:.1f} h)", flush=True)
    if batch:
        flush()
    return rows_done, values, time.perf_counter() - start


# --- dashboard ---------------------------------------------------------------

def timed_stream(api, path, sink=None, markers=()):
    """GET path on the administration surface, streamed: (ttfb s, total s, bytes,
    marker counts). Markers are counted across chunk boundaries; a sink receives
    every chunk."""
    headers = {"X-Internal-Service-Token": api.service_token, "X-Internal-User-Id": str(api.user_id)}
    counts = dict.fromkeys(markers, 0)
    overlap = max((len(m) for m in markers), default=1) - 1
    start = time.perf_counter()
    resp, conn = api.request("GET", path, headers, None, want=(200,), stream=True)
    ttfb = time.perf_counter() - start
    size, tail = 0, b""
    try:
        while chunk := resp.read(1 << 16):
            size += len(chunk)
            if sink:
                sink.write(chunk)
            if markers:
                window = tail + chunk
                for marker in markers:
                    # Matches lying wholly inside the carried tail were counted already.
                    counts[marker] += window.count(marker) - tail.count(marker)
                tail = window[-overlap:] if overlap else b""
    finally:
        conn.close()
    return ttfb, time.perf_counter() - start, size, counts


def time_dashboard(api, plan, cfg, records, instances):
    """Time the dashboard's reads --dashboard-runs times each and check the record
    status stream lists every record and instance. Returns report lines."""
    lines = []
    reads = (("record-status", [b'"record_id":', b'"instance":'], (records, instances)),
             ("record-completeness", [b'"record_id":'], (records,)))
    for read, markers, want in reads:
        path = f"/api/v1/projects/{plan['id']}/{read}"
        runs = []
        for i in range(cfg.dashboard_runs):
            ttfb, total, size, counts = timed_stream(api, path, markers=markers)
            got = tuple(counts[m] for m in markers)
            if got != want:
                names = ", ".join(m.decode().strip('":') for m in markers)
                raise RuntimeError(f"GET {path}: lists {got} ({names}), want {want}")
            runs.append((ttfb, total, size))
            print(f"dashboard: {read} run {i + 1}/{cfg.dashboard_runs}: {total:.2f}s "
                  f"(first byte {ttfb:.2f}s), {mb(size):.1f} MB", flush=True)
        totals = [r[1] for r in runs]
        lines.append(f"{read + ':':<22}median {statistics.median(totals):.2f}s, "
                     f"min {min(totals):.2f}s, max {max(totals):.2f}s over {len(runs)} runs; "
                     f"first byte {statistics.median(r[0] for r in runs):.2f}s; "
                     f"{mb(runs[0][2]):.1f} MB")
    return lines


# --- ODM export --------------------------------------------------------------

COLLECTION_OFFSET = re.compile(r"[+-]\d\d:\d\d$")


def local(tag):
    return tag.rsplit("}", 1)[-1]


def verify_odm(path, plan, cfg, first_record):
    """Stream-parse an exported ODM file and compare every SubjectData with its
    regenerated record. Returns (subjects, item values, mismatches as text)."""
    owner = {}  # item name → instrument; a checkbox box belongs to its parent's
    datetimes = set()
    for instrument, specs in plan["fields"].items():
        for spec in specs:
            owner[spec["field_name"]] = instrument
            if spec.get("validation_type") == "datetime":
                datetimes.add(spec["field_name"])
    problems, seen = [], set()
    subjects = items = 0
    subject = group = None
    observed, value = {}, None
    container = None

    def complain(text):
        if len(problems) < 20:
            problems.append(text)

    for event, elem in ET.iterparse(path, events=("start", "end")):
        tag = local(elem.tag)
        if event == "start":
            if tag == "ClinicalData":
                container = elem
            elif tag == "SubjectData":
                subject, observed = elem.get("SubjectKey"), {}
            elif tag == "ItemGroupData":
                group = int(elem.get("ItemGroupRepeatKey") or 1)
            continue
        if tag == "Value":                       # ODM 2.0 carries the value as a child
            value = elem.text or ""
        elif tag == "ItemData":
            items += 1
            name = elem.get("ItemOID", "").removeprefix("IT_")
            instrument = owner.get(name) or owner.get(name.split("___")[0])
            if instrument is None:
                complain(f"{subject}: item {name} is not in the dictionary")
            else:
                found = elem.get("Value") if elem.get("Value") is not None else value
                if name in datetimes:
                    # Stored with the server's collection offset (GD-16), which an import
                    # without one receives; the offset depends on APP_TIMEZONE.
                    found = COLLECTION_OFFSET.sub("", found or "")
                observed.setdefault((instrument, group), {})[name] = found
            value = None
        elif tag == "SubjectData":
            subjects += 1
            match = STUDY_UID.match(subject or "")
            n = int(match.group(1)) if match else 0
            if not first_record <= n <= cfg.records:
                complain(f"unexpected subject {subject}")
            elif n in seen:
                complain(f"subject {subject} appears twice")
            else:
                seen.add(n)
                want = expected_values(plan, record_rows(plan, cfg, n))
                if observed != want:
                    complain(f"{subject}: {describe_difference(want, observed)}")
            if container is not None:
                container.clear()                # keep memory flat over millions of items
        elif tag == "ItemGroupData":
            group = None
    missing = cfg.records - first_record + 1 - len(seen)
    if missing:
        problems.insert(0, f"{missing} record(s) missing from the export")
    return subjects, items, problems


def describe_difference(want, got):
    for key in sorted(set(want) | set(got)):
        if key not in got:
            return f"{key[0]} #{key[1]} missing"
        if key not in want:
            return f"{key[0]} #{key[1]} not imported"
        for name in sorted(set(want[key]) | set(got[key])):
            if want[key].get(name) != got[key].get(name):
                return f"{key[0]} #{key[1]} {name}: exported {got[key].get(name)!r}, imported {want[key].get(name)!r}"
    return "differs"


def export_odm(api, plan, cfg, first_record):
    """Export and check the project once per ODM version. Returns report lines."""
    lines = []
    for version in cfg.odm_versions.split(","):
        version = version.strip()
        path = os.path.join(cfg.out, f"{plan['name']}_odm_{version}.xml")
        query = urllib.parse.urlencode({"odm_version": version})
        with open(path, "wb") as sink:
            ttfb, total, size, _ = timed_stream(api, f"/api/v1/projects/{plan['id']}/export/odm?{query}", sink)
        print(f"odm: {version} exported in {total:.1f}s (first byte {ttfb:.2f}s), "
              f"{mb(size):.1f} MB → {path}", flush=True)
        start = time.perf_counter()
        try:
            subjects, items, problems = verify_odm(path, plan, cfg, first_record)
        except ET.ParseError as e:  # a truncated stream ends here (REQ-ODM-025)
            raise RuntimeError(f"ODM {version} export is not a complete document: {e}") from e
        check_s = time.perf_counter() - start
        if problems:
            raise RuntimeError(f"ODM {version} export differs from the imported data:\n  "
                               + "\n  ".join(problems))
        print(f"odm: {version} checked in {check_s:.1f}s — {subjects} subjects, {items} items match", flush=True)
        lines.append(f"{'ODM ' + version + ' export:':<22}{total:.1f}s (first byte {ttfb:.2f}s), "
                     f"{mb(size):.1f} MB, {mb(size) / max(total, 1e-9):.1f} MB/s; "
                     f"{subjects} subjects / {items} items verified in {check_s:.1f}s")
    return lines


# --- main --------------------------------------------------------------------

def main():
    ap = argparse.ArgumentParser(description=__doc__.splitlines()[0],
                                 formatter_class=argparse.ArgumentDefaultsHelpFormatter)
    ap.add_argument("--base-url", default=os.environ.get("BASE_URL", "http://127.0.0.1:8099"))
    ap.add_argument("--service-token", default=os.environ.get("SERVICE_TOKEN", "dev-internal-token"))
    ap.add_argument("--email", default=os.environ.get("ADMIN_EMAIL", "admin@stress.local"))
    ap.add_argument("--password", default=os.environ.get("ADMIN_PASSWORD", "stress-bootstrap-password"))
    ap.add_argument("--odm", default=DEFAULT_ODM, help="the Incoming project's ODM document")
    ap.add_argument("--project-name", default="Incoming")
    ap.add_argument("--resume", action="store_true",
                    help="adopt an existing project of that name and continue at its highest "
                         "stored record (same --seed and series bounds as the first run)")
    ap.add_argument("--records", type=int, default=400_000)
    ap.add_argument("--min-series", type=int, default=5)
    ap.add_argument("--max-series", type=int, default=20)
    ap.add_argument("--batch", type=int, default=500, help="import rows per data API call")
    ap.add_argument("--seed", type=int, default=20261030)
    ap.add_argument("--dashboard-runs", type=int, default=3,
                    help="timed reads of the dashboard's record-status and completeness (0 skips)")
    ap.add_argument("--odm-versions", default="1.3",
                    help="comma list of ODM versions to export and check (1.3, 2.0); empty skips")
    ap.add_argument("--skip-import", action="store_true",
                    help="only time the dashboard and export an already filled project (with --resume)")
    ap.add_argument("--metrics-interval", type=float, default=5.0,
                    help="seconds between GET /api/v1/system/metrics samples (0 disables)")
    ap.add_argument("--out", default=os.environ.get("STRESS_OUT", os.path.join(HERE, "stress-out-incoming")))
    cfg = ap.parse_args()
    if cfg.min_series < 1 or cfg.max_series < cfg.min_series:
        ap.error("want 1 <= --min-series <= --max-series")
    os.makedirs(cfg.out, exist_ok=True)

    api = API(cfg.base_url, cfg.service_token)
    api.login(cfg.email, cfg.password)
    print(f"using administrator user {api.user_id} at {cfg.base_url} (seed {cfg.seed}, "
          f"{cfg.records} records, {cfg.min_series}..{cfg.max_series} series each)", flush=True)

    sampler = MetricsSampler(cfg.base_url, cfg.service_token, api.user_id, cfg.metrics_interval,
                             os.path.join(cfg.out, "metrics.jsonl")) if cfg.metrics_interval > 0 else None
    if sampler:
        sampler.start()
    set_phase = sampler.set_phase if sampler else (lambda phase: None)

    rows = values = 0
    import_s = 0.0
    dashboard_lines, odm_lines = [], []
    try:
        set_phase("setup")
        plan, built, setup_s = setup(api, cfg)
        first = 1
        if cfg.resume:
            first = max(highest_stored(api, plan), 1)
        set_phase("import")
        if not cfg.skip_import and first <= cfg.records:
            rows, values, import_s = import_records(api, plan, cfg, first)
        elif first > cfg.records:
            print(f"import: {first - 1} records already stored, nothing to add", flush=True)
        stored = max(highest_stored(api, plan), 0)
        if stored != cfg.records:
            raise RuntimeError(f"the project holds records up to {stored}, want {cfg.records}")
        instances = sum(series_count(cfg, n) for n in range(1, cfg.records + 1))
        if cfg.dashboard_runs > 0:
            set_phase("dashboard")
            dashboard_lines = time_dashboard(api, plan, cfg, cfg.records, instances + cfg.records)
        if cfg.odm_versions.strip():
            set_phase("odm")
            odm_lines = export_odm(api, plan, cfg, 1)
    finally:
        samples = sampler.stop() if sampler else []

    built_line = (f"built from {os.path.relpath(cfg.odm)} in {setup_s:.1f}s "
                  f"({built['instruments']} instruments, {built['fields']} fields)" if built
                  else f"adopted (id {plan['id']})")
    report = f"""CLARA Incoming project report (external client)
base url:             {cfg.base_url}
seed:                 {cfg.seed}
project:              {plan['name']} — {built_line}
records:              {cfg.records} ({cfg.min_series}..{cfg.max_series} Series + 1 Transfers each, \
{instances} Series instances)
imported rows:        {rows} (values: {values}{', resumed at record ' + str(first) if cfg.resume else ''})
import time:          {import_s:.1f}s ({rows / max(import_s, 1e-9):.0f} rows/s, \
{values / max(import_s, 1e-9):.0f} values/s)

record status dashboard (API reads, GET /api/v1/projects/{plan['id']}/…):
""" + "\n".join("  " + line for line in dashboard_lines or ["skipped"]) + """

ODM export:
""" + "\n".join("  " + line for line in odm_lines or ["skipped"]) + f"""

browser display (PHP data region + Tabulator grid):
  CLARA_WEB_URL=<web url> CLARA_PERF_PROJECT='{plan['name']}' CLARA_PERF_RECORDS={cfg.records} \\
    npx playwright test --config e2e/playwright.config.ts incoming-dashboard --project chromium
output directory:     {os.path.abspath(cfg.out)}
""" + metrics_block(samples, cfg.metrics_interval, sampler.failures if sampler else 0,
                    sampler.last_error if sampler else None,
                    bool(sampler and sampler.gave_up)) + "\n"
    if samples:
        write_metrics_csv(os.path.join(cfg.out, "metrics_summary.csv"), samples)
    print("\n" + report, flush=True)
    with open(os.path.join(cfg.out, "report.txt"), "w") as f:
        f.write(report)


if __name__ == "__main__":
    try:
        main()
    except Exception as e:
        print(f"incoming test failed: {e}", file=sys.stderr)
        sys.exit(1)
