#!/usr/bin/env python3
"""Check an exported CLARA ODM document against the published CDISC schema.

Offline conformance gate for REQ-ODM-055. The running program stays pure Go and never
validates against a schema (ASM-ODM-5, CGO_ENABLED=0), so dialect conformance is proven
here — against CDISC's own XSDs, vendored under tools/odm_schema/. This exists because
a round trip cannot see a dialect error: exporter and importer agreed with each other
that ODM 2.0's data nesting was ODM 1.3, and the suite stayed green doing it (O-7).

Usage:
    tools/odm_conformance.py FILE.xml [FILE.xml ...]      # through CLARA's wrapper
    tools/odm_conformance.py --plain FILE.xml             # against vanilla CDISC ODM

The dialect is read from the document's root namespace, never from an option, so what
gets checked is what the exporter claimed to write (REQ-ODM-010). ODM 1.3 documents go
through tools/odm_schema/clara_odm_ext.xsd, which redefines the extension hooks 1.3.2
leaves empty; `--plain` swaps in CDISC's unmodified driver, which is how the wrapper's
own contribution stays demonstrated rather than assumed — an extension-bearing document
fails vanilla ODM at exactly its `clara:*` positions and nowhere else.

Provenance of what is vendored (a re-download changes the standard CLARA claims
conformance to, so it is a review event like any other dependency bump):
    cdisc-odm-1.3.2/   upstream cdisc-org/DataExchange-CT `schema/cdisc-odm-1.3.2`,
                       ODM1-3-2-foundation.xsd sha256 1adc48512d34b003…
    cdisc-odm-2.0/     upstream cdisc-org/DataExchange-ODM `schema`,
                       ODM-foundation.xsd sha256 75798cc513d76414…
    core/              w3.org xml.xsd and xmldsig-core-schema.xsd, which the CDISC
                       schemas import through ../core/

lxml is development tooling on the order of Playwright or semgrep (ASM-ODM-5): nothing
in the running program validates an XSD. Exit codes keep "found nothing" apart from
"nothing was checked": 0 every document valid · 1 a document deviates · 2 bad
invocation · 3 environment cannot check (no lxml) — which is what a soft step reads.
"""

import collections
import os
import re
import sys
import xml.etree.ElementTree as ET

HERE = os.path.dirname(os.path.abspath(__file__))
SCHEMA_DIR = os.path.join(HERE, "odm_schema")

# Root namespace -> (schema through CLARA's wrapper, vanilla CDISC driver).
# Both dialects need their own wrapper: neither 1.3.2 nor 2.0 declares an xs:any, so a document
# carrying urn:x-clara:odm-ext:v1 is invalid against vanilla CDISC at exactly those positions and
# only a redefinition makes it checkable (REQ-ODM-055). Pointing 2.0 at the unmodified schema would
# report CLARA's own namespace as a deviation and bury the dialect's real structural gaps in it.
SCHEMAS = {
    "http://www.cdisc.org/ns/odm/v1.3": ("clara_odm_ext.xsd",
                                         os.path.join("cdisc-odm-1.3.2", "ODM1-3-2.xsd")),
    "http://www.cdisc.org/ns/odm/v2.0": ("clara_odm_ext_2.0.xsd",
                                         os.path.join("cdisc-odm-2.0", "ODM-foundation.xsd")),
}

# Findings collapse by message: one real defect usually repeats once per row, and
# "22x this" reads better than twenty-two lines.
MAX_DISTINCT = 12


def dialect_of(path):
    """The ODM namespace the document's root element declares."""
    root = ET.parse(path).getroot()
    match = re.match(r"\{(.*)\}", root.tag)
    return match.group(1) if match else ""


def distinct(error_log):
    counts = collections.Counter()
    for entry in error_log:
        counts[re.sub(r"^.*?:ERROR:\w+:\s*", "", str(entry).replace("\n", " "))] += 1
    return counts


def check(path, wrapper, cache):
    """Validate one document against its dialect's schema; True when it conforms."""
    try:
        namespace = dialect_of(path)
    except ET.ParseError as error:
        print(f"  FAIL {os.path.basename(path)}: not well-formed XML — {error}")
        return False
    if namespace not in SCHEMAS:
        print(f"  SKIP {os.path.basename(path)}: root namespace {namespace!r} is neither "
              "ODM 1.3 nor 2.0, so no schema applies")
        return True

    from lxml import etree
    name = SCHEMAS[namespace][0 if wrapper else 1]
    if name not in cache:
        try:
            cache[name] = etree.XMLSchema(etree.parse(os.path.join(SCHEMA_DIR, name)))
        except (etree.XMLSchemaParseError, OSError) as error:
            print(f"  FAIL schema {name} will not load — {error}")
            return False

    with open(path, "rb") as handle:
        document = etree.parse(handle)
    label = "ODM 1.3" if namespace.endswith("/v1.3") else "ODM 2.0"
    through = "CLARA wrapper" if wrapper and name != SCHEMAS[namespace][1] else "vanilla CDISC"
    if cache[name].validate(document):
        print(f"  OK   {os.path.basename(path)} ({label}, {through}): conforms")
        return True

    found = distinct(cache[name].error_log)
    print(f"  FAIL {os.path.basename(path)} ({label}, {through}): "
          f"{sum(found.values())} finding(s) in {len(found)} class(es)")
    for message, count in found.most_common(MAX_DISTINCT):
        print(f"         {f'{count}x ' if count > 1 else ''}{message[:200]}")
    if len(found) > MAX_DISTINCT:
        print(f"         … {len(found) - MAX_DISTINCT} further class(es) not shown")
    return False


def main(argv):
    args = [a for a in argv[1:] if a != "--plain"]
    wrapper = "--plain" not in argv[1:]
    if len(argv[1:]) and not args:  # only --plain was given
        print("Usage: tools/odm_conformance.py [--plain] FILE.xml [FILE.xml ...]")
        return 2
    if not args:
        print(__doc__.strip().splitlines()[0])
        print("\nUsage: tools/odm_conformance.py [--plain] FILE.xml [FILE.xml ...]")
        return 2
    missing = [p for p in args if not os.path.isfile(p)]
    for path in missing:
        print(f"  FAIL no such file: {path}")
    if missing:
        return 2

    try:
        import lxml.etree  # noqa: F401  presence check; the work imports it where used
    except ImportError:
        print("  SKIP python3 lxml is not installed, so ODM schema conformance was not checked")
        print("       setup: python3 -m pip install --user lxml")
        return 3

    cache, failed = {}, 0
    for path in args:
        if not check(path, wrapper, cache):
            failed += 1
    return 1 if failed else 0


if __name__ == "__main__":
    sys.exit(main(sys.argv))
