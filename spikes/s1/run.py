"""S1 spike harness: run each case in cases.yaml through Malloy (via the compile sidecar) and
MetricFlow, execute the SQL against the dev mart as hareno_reader, and compare with the expected
rows. Throwaway spike code; the findings are the deliverable.

    uv run python run.py [malloy|metricflow ...] [-k CASE_ID] [--sql]
"""
import argparse
import json
import os
import re
import shlex
import subprocess
import sys
import time
import urllib.request
from decimal import Decimal
from pathlib import Path

import psycopg2
import psycopg2.extras
import yaml
from datetime import datetime
from zoneinfo import ZoneInfo

HERE = Path(__file__).parent
ROOT = HERE.parent.parent
DSN = "host=localhost port=55433 dbname=mart user=hareno_reader password=hareno_reader"
SIDECAR = "http://127.0.0.1:55451"
BUSINESS_TZ = ZoneInfo("Asia/Hong_Kong")  # dev mart test convention


class ExactLoader(yaml.SafeLoader):
    """Expected numbers stay exact decimals: a YAML float would hide precision loss."""


ExactLoader.add_constructor("tag:yaml.org,2002:float", lambda loader, node: Decimal(loader.construct_scalar(node)))

# Keep JSON numbers exact so floating-point artefacts in results stay visible.
psycopg2.extras.register_default_json(loads=lambda s: json.loads(s, parse_float=Decimal))
psycopg2.extras.register_default_jsonb(loads=lambda s: json.loads(s, parse_float=Decimal))

PG_TYPES = {  # pg type oid -> Malloy AtomicType
    16: {"kind": "boolean_type"},
    20: {"kind": "number_type", "subtype": "bigint"},
    21: {"kind": "number_type", "subtype": "integer"},
    23: {"kind": "number_type", "subtype": "integer"},
    700: {"kind": "number_type", "subtype": "decimal"},
    701: {"kind": "number_type", "subtype": "decimal"},
    1700: {"kind": "number_type", "subtype": "decimal"},
    25: {"kind": "string_type"},
    1043: {"kind": "string_type"},
    1082: {"kind": "date_type"},
    1114: {"kind": "timestamp_type"},
    1184: {"kind": "timestamptz_type"},
}


def schema_of(cur, sql):
    cur.execute(f"SELECT * FROM ({sql}) AS t LIMIT 0")
    return {"fields": [{"kind": "dimension", "name": d.name, "type": PG_TYPES[d.type_code]} for d in cur.description]}


# ---- Malloy ---------------------------------------------------------------------------------

def post(path, body):
    req = urllib.request.Request(SIDECAR + path, json.dumps(body).encode(), {"content-type": "application/json"})
    with urllib.request.urlopen(req) as r:
        return json.load(r)


def start_sidecar():
    env = dict(os.environ, PORT=SIDECAR.rsplit(":", 1)[1])
    proc = subprocess.Popen(["node", str(ROOT / "compiler/server.ts")], env=env, stdout=subprocess.DEVNULL)
    for _ in range(50):
        try:
            urllib.request.urlopen(SIDECAR + "/healthz")
            return proc
        except OSError:
            time.sleep(0.1)
    raise RuntimeError("sidecar did not start")


def malloy_sql(cur, query):
    """The compile loop Go will own: answer compiler_needs until SQL comes back."""
    model_url = "file://hareno/mart.malloy"
    needs = {
        "files": [{"url": model_url, "contents": (HERE / "malloy/mart.malloy").read_text()}],
        "connections": [{"name": "mart", "dialect": "postgres"}],
    }
    for _ in range(5):
        resp = post("/compile", {"model_url": model_url, "query_malloy": query, "compiler_needs": needs})
        errors = [l["message"] for l in resp.get("logs", []) if l["severity"] == "error"]
        if errors:
            raise CompileError("; ".join(errors))
        if "result" in resp:
            return resp["result"]["sql"], [f["name"] for f in resp["result"]["schema"]["fields"]]
        asked = resp["compiler_needs"]
        for t in asked.get("table_schemas", []):
            t["schema"] = schema_of(cur, f"SELECT * FROM {t['name']}")
            needs.setdefault("table_schemas", []).append(t)
        for s in asked.get("sql_schemas", []):
            s["schema"] = schema_of(cur, s["sql"])
            needs.setdefault("sql_schemas", []).append(s)
    raise CompileError("compiler_needs loop did not converge")


def run_malloy(cur, query):
    sql, cols = malloy_sql(cur, query)
    cur.execute(sql)
    rows = [[r[0].get(c) for c in cols] for r in cur.fetchall()]  # Malloy wraps rows in row_to_json
    return sql, rows


# ---- MetricFlow -----------------------------------------------------------------------------

def run_metricflow(cur, args):
    cmd = ["mf", "query", "--explain", *shlex.split(args)]
    env = dict(os.environ, DBT_PROFILES_DIR=str(HERE / "metricflow"))
    out = subprocess.run(cmd, cwd=HERE / "metricflow", capture_output=True, text=True, env=env)
    text = re.sub(r"\x1b\[[0-9;]*m|\r", "", out.stdout + out.stderr)
    marker = "SQL (remove --explain"
    if out.returncode != 0 or marker not in text:
        lines = [l.strip() for l in text.splitlines() if l.strip()]
        if "Message:" in lines:
            i = lines.index("Message:")
            raise CompileError(" ".join(lines[i + 1:i + 3]))
        raise CompileError(" | ".join(lines[-3:]) if lines else "mf failed")
    sql = text.split(marker, 1)[1].split("\n", 1)[1].strip()
    cur.execute(sql)
    return sql, [list(r) for r in cur.fetchall()]


# ---- comparison -----------------------------------------------------------------------------

class CompileError(Exception):
    pass


def same(actual, expected):
    if expected is None or actual is None:
        return actual is None and expected is None
    if isinstance(expected, (int, float, Decimal)) or (isinstance(expected, str) and _num(expected)):
        if isinstance(actual, bool) or not _num(str(actual)):
            return False
        exp = Decimal(str(expected))
        scale = Decimal(1).scaleb(exp.as_tuple().exponent)
        return Decimal(str(actual)).quantize(scale) == exp
    if re.fullmatch(r"\d{4}-\d{2}(-\d{2})?", str(expected)):
        # Time buckets come back as instants; render them in business time, as Hareno will.
        text = str(actual)
        if re.fullmatch(r"\d{4}-\d{2}-\d{2}T.*[+-]\d{2}:\d{2}", text):
            text = datetime.fromisoformat(text).astimezone(BUSINESS_TZ).isoformat()
        return text.startswith(str(expected))
    return str(actual) == str(expected)


def _num(s):
    try:
        Decimal(s)
        return True
    except Exception:
        return False


def rows_match(actual, expected):
    return len(actual) == len(expected) and all(
        len(a) == len(e) and all(same(x, y) for x, y in zip(a, e)) for a, e in zip(actual, expected))


def main():
    ap = argparse.ArgumentParser()
    ap.add_argument("engines", nargs="*", default=["malloy", "metricflow"])
    ap.add_argument("-k", help="only this case id")
    ap.add_argument("--sql", action="store_true", help="print generated SQL")
    a = ap.parse_args()

    cases = yaml.load((HERE / "cases.yaml").read_text(), ExactLoader)
    conn = psycopg2.connect(DSN)
    conn.autocommit = True
    cur = conn.cursor()
    sidecar = start_sidecar() if "malloy" in a.engines else None
    tally = {}
    try:
        for case in cases:
            if a.k and case["id"] != a.k:
                continue
            for engine in a.engines:
                spec = case.get(engine)
                status, detail = evaluate(cur, engine, spec, a.sql)
                tally.setdefault(engine, {}).setdefault(status, []).append(case["id"])
                print(f"{case['id']:<4} {engine:<10} {status:<11} {case['title']}{detail}")
    finally:
        if sidecar:
            sidecar.terminate()
    print()
    for engine, by in tally.items():
        print(engine, {k: len(v) for k, v in by.items()})


def evaluate(cur, engine, spec, show_sql):
    """Statuses: PASS, FAIL, REJECTED (engine refused a query the model must refuse; good),
    NO_REJECT (engine answered a query that has no correct answer), UNSUPPORTED, ERROR."""
    if spec is None:
        return "SKIPPED", ""
    if "unsupported" in spec:
        return "UNSUPPORTED", f"  [{spec['unsupported']}]"
    run = run_malloy if engine == "malloy" else run_metricflow
    detail = ""
    for step in spec["queries"]:
        try:
            sql, rows = run(cur, step["q"])
        except (CompileError, psycopg2.Error) as e:
            if step.get("reject"):
                return "REJECTED", f"  [{str(e).strip()[:120]}]"
            return "ERROR", f"\n      {str(e).strip()[:400]}"
        if show_sql:
            detail += "\n" + sql
        if step.get("reject"):
            return "NO_REJECT", f"  [returned {rows}]"
        if not rows_match(rows, step["expect"]):
            return "FAIL", detail + f"\n      expected {step['expect']}\n      actual   {rows}"
    return "PASS", detail


if __name__ == "__main__":
    sys.exit(main())
