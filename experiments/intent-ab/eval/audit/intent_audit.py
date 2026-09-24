#!/usr/bin/env python3
"""Extractor audit (part b of the intent A/B experiment): score what an
arm-B intent scan produced.

Two questions, per the protocol's secondary experiment:

  1. Seeded-gotcha recall — for each constraint the seed deliberately plants
     (DESIGN.md's Q1–Q7 anti-prior traps and SC1–SC12 standing constraints,
     and the D1–D5 drift corrections embedded in the task list), did the scan
     extract a requirement that captures it?
  2. Unseeded-requirement quality — for everything else the scan produced,
     is it true of the project, would following it prevent a plausible
     regression, and is it a duplicate? This is where "extracted from an
     incident during development" (the expansion beyond the original
     intent-continuity work) shows up or doesn't.

Deterministic parts: frontmatter/statement parsing, confidence and
provenance mix, near-duplicate pairs (token Jaccard), and a keyword-signature
recall pass. Judged part (optional, `--judge-model`): a blind `claude -p`
call that sees only requirement statements + the canonical gotcha texts —
never the arm name, provenance, or run — and returns JSON: confirmed recall
matches (including ones the keyword pass missed) and per-requirement grades.

Usage:
    python3 intent_audit.py --intent /path/to/.cloche/intent [--tasks seed-tasks.json]
        [--design DESIGN.md] [--judge-model claude-sonnet-5 | --no-judge] [--out audit.json]
"""
import argparse
import json
import re
import subprocess
import sys
from pathlib import Path

HERE = Path(__file__).resolve().parent
SEED = HERE.parent.parent / "seed"

# Canonical seeded gotchas: id -> (short text, keyword signature over statement+hints).
SEEDED = {
    "Q1": ("Conditions (if/while, and/or operands) must be booleans; non-boolean is a runtime error",
           r"condition.*boolean|must be a boolean|boolean-only|non-boolean"),
    "Q2": ("`let` declares a new binding, `set` reassigns an existing one; bare assignment is a parse error",
           r"\blet\b.*\bset\b|\bset\b.*\blet\b|declares.*reassign|reassign.*declar"),
    "Q3": ("String concatenation is `~`; `+` on strings is a type error",
           r"\s~\s|`~`|concatenat"),
    "Q4": ("Not-equals is `<>`; `!=` is a lex error",
           r"<>|not-equals|not equals|!="),
    "Q5": ("String builtins at/len/sub are 1-indexed",
           r"1-index|one-index|index(ed)? (from|starting at) 1"),
    "Q6": ("Comparisons do not chain (a < b < c is a parse error)",
           r"comparison.*chain|chain.*comparison|do(es)? not chain|don't chain|non-chain"),
    "Q7": ("`and`/`or` never short-circuit; both operands are always evaluated",
           r"short-circuit|short circuit"),
    "D1": ("Every user-visible error is exactly one `line N: <message>` line, no traceback",
           r"(single|one|exactly one) line|line N:.*(only|single|exactly)|no traceback|traceback"),
    "D2": ("Evaluator is iterative for calls/blocks: user nesting depth never becomes Python stack depth",
           r"recurs|explicit stack|work-list|worklist|iterativ|stack depth"),
    "D3": ("Integer division/remainder truncate toward zero for negative operands",
           r"truncat|toward zero|towards zero"),
    "D4": ("REPL uses the exact prompt string and exits cleanly on EOF (Ctrl-D)",
           r"REPL.*(prompt|EOF|Ctrl-D)|(prompt|EOF|Ctrl-D).*REPL"),
    "D5": ("`bract fmt` is idempotent: fmt(fmt(x)) == fmt(x)",
           r"idempot|fmt\(fmt"),
    "SC1": ("Python standard library only; no third-party packages", r"standard library|stdlib|third-party|pip"),
    "SC2": ("Single-package layout: every module directly under bract/", r"single-package|directly under `?bract/|nested subpackage"),
    "SC3": ("All errors rendered through one shared error-formatting path", r"shared error|error-format|formatting path|single (error )?format|one place"),
    "SC4": ("No print()/stdout/stderr writes outside the CLI module", r"\bprint\(|sys\.stdout|sys\.stderr"),
    "SC5": ("Token type names are SCREAMING_CASE", r"SCREAMING|upper-?case token|token (type )?names"),
    "SC6": ("Exit codes are exactly {0,1,2}, produced only by the CLI dispatch", r"exit code.*(0|1|2)"),
    "SC7": ("No Python eval/exec/compile anywhere in bract/", r"\beval\(|\bexec\(|\bcompile\(|`eval`|`exec`"),
    "SC8": ("No module-level mutable interpreter state; state threaded through an Interpreter/Environment", r"module-level|global (dict|list|state|variable)|no globals"),
    "SC9": ("bract fmt only changes whitespace/indentation/brace placement, never AST", r"only.*whitespace|whitespace.*only|never reorder|reorder|re-associate"),
    "SC10": ("Every CLI entry point dispatched from a single bract/__main__.py", r"__main__|entry point|single top-level"),
    "SC11": ("Line numbers surfaced to users are 1-indexed", r"line numbers? .*1-index|1-indexed.*line|first line is 1"),
    "SC12": ("No TODO/FIXME/pass-stub in modules reachable from __main__ once a feature is claimed complete", r"TODO|FIXME|pass-stub|stub"),
}


def parse_requirement(path: Path) -> dict:
    text = path.read_text()
    m = re.match(r"^---\n(.*?)\n---\n(.*)$", text, re.S)
    if not m:
        return {"id": path.stem, "raw": text, "statement": text.strip(), "parse_error": True}
    fm, body = m.group(1), m.group(2).strip()
    req = {"id": path.stem, "status": None, "confidence": None, "hints": [], "domains": [],
           "provenance_kind": None, "provenance_ref": None, "user_edited": False}
    section = None
    for line in fm.splitlines():
        s = line.rstrip()
        if not s.strip():
            continue
        indent = len(s) - len(s.lstrip())
        key, _, val = s.strip().partition(":")
        val = val.strip().strip("'\"")
        if indent == 0:
            section = key
            if key == "id":
                req["id"] = val
            elif key == "status":
                req["status"] = val
            elif key == "confidence":
                req["confidence"] = val
            elif key == "user_edited":
                req["user_edited"] = val.lower() == "true"
        elif section == "hints" and s.strip().startswith("- "):
            req["hints"].append(s.strip()[2:].strip().strip("'\""))
        elif section == "scope" and s.strip().startswith("- "):
            req["domains"].append(s.strip()[2:].strip())
        elif section == "provenance":
            if key == "kind":
                req["provenance_kind"] = val
            elif key == "ref":
                req["provenance_ref"] = val
    why = ""
    if "**Why:**" in body:
        statement, _, why = body.partition("**Why:**")
    else:
        statement = body
    req["statement"] = statement.strip()
    req["why"] = why.strip()
    return req


def load_snapshot(intent_dir: Path) -> list:
    reqs = []
    for p in sorted((intent_dir / "requirements").glob("req-*.md")):
        reqs.append(parse_requirement(p))
    return reqs


def keyword_recall(reqs: list) -> dict:
    out = {}
    for gid, (text, sig) in SEEDED.items():
        rx = re.compile(sig, re.I)
        hits = [r["id"] for r in reqs
                if r.get("status", "active") in (None, "active")
                and (rx.search(r["statement"]) or any(rx.search(h) for h in r["hints"]))]
        out[gid] = {"text": text, "keyword_hits": hits}
    return out


_TOKEN_RE = re.compile(r"[a-z0-9_]+")
_STOP = {"the", "a", "an", "of", "to", "in", "is", "and", "or", "for", "be", "must", "never", "not",
         "on", "it", "as", "by", "with", "that", "this", "any", "every", "no", "all", "are", "from"}


def _tokens(s: str) -> set:
    return {t for t in _TOKEN_RE.findall(s.lower()) if t not in _STOP and len(t) > 2}


def near_duplicates(reqs: list, threshold: float = 0.5) -> list:
    pairs = []
    toks = {r["id"]: _tokens(r["statement"]) for r in reqs}
    ids = [r["id"] for r in reqs]
    for i, a in enumerate(ids):
        for b in ids[i + 1:]:
            ta, tb = toks[a], toks[b]
            if not ta or not tb:
                continue
            j = len(ta & tb) / len(ta | tb)
            if j >= threshold:
                pairs.append({"a": a, "b": b, "jaccard": round(j, 3)})
    return sorted(pairs, key=lambda p: -p["jaccard"])


def summarize(reqs: list) -> dict:
    def count(key):
        c = {}
        for r in reqs:
            v = r.get(key) or "unknown"
            c[v] = c.get(v, 0) + 1
        return c
    return {
        "total": len(reqs),
        "active": sum(1 for r in reqs if r.get("status") in (None, "active")),
        "by_status": count("status"),
        "by_confidence": count("confidence"),
        "by_provenance_kind": count("provenance_kind"),
        "hints_per_requirement": round(sum(len(r["hints"]) for r in reqs) / len(reqs), 2) if reqs else 0,
        "domains": sorted({d for r in reqs for d in r["domains"]}),
    }


JUDGE_PROMPT = """You are auditing requirements that an automated scanner extracted from a small
interpreter project's design document, task prompts, and commit history. You see only
the requirement statements. Judge them against the reference constraints below and
against general engineering sense. Be strict and literal; do not give credit for
vague or partial matches.

## Reference: constraints the project deliberately planted (id: text)
{seeded}

## Extracted requirements (id: statement)
{requirements}

Return ONLY a CSV document, no prose and no code fence, with exactly this header:

kind,id,seeded,category,valid,actionable,specific,duplicate_of,note

- One `recall` row per planted constraint id: kind=recall, id=<planted id>,
  seeded=<requirement ids that faithfully capture it, separated by ';' — empty if none>,
  the remaining columns empty.
- One `grade` row per extracted requirement: kind=grade, id=<req id>,
  seeded=<planted id it restates, or empty>,
  category=spec-restatement|dev-incident|process|noise
    (dev-incident = a rule that reads as learned from a bug or regression during
    development, not from the spec),
  valid=true|false (consistent with the reference constraints and plausible here),
  actionable=true|false (a coding agent following it would avoid a concrete regression),
  specific=true|false (names a concrete artifact/behaviour rather than a platitude),
  duplicate_of=<req id or empty>,
  note=<= 20 words.
- Quote any field containing a comma, a double quote (written as ""), or a line break.
"""


def _claude(prompt: str, model: str, timeout: int):
    r = subprocess.run(["claude", "-p", "--model", model, "--output-format", "json"],
                       input=prompt, capture_output=True, text=True, timeout=timeout)
    if r.returncode != 0:
        return None, r.stderr.strip()[-1500:], None
    try:
        envelope = json.loads(r.stdout)
        return envelope.get("result", ""), None, envelope.get("total_cost_usd")
    except json.JSONDecodeError:
        return r.stdout, None, None


def parse_judge_csv(text: str):
    """Parse the judge's CSV into {"recall": {...}, "grades": {...}}. Raises
    ValueError with a message a repair prompt can hand back."""
    import csv, io
    body = text.strip()
    body = re.sub(r"^```[a-z]*\n|\n```$", "", body).strip()
    rows = list(csv.reader(io.StringIO(body)))
    if not rows:
        raise ValueError("no CSV rows at all")
    header = [h.strip().lower() for h in rows[0]]
    want = ["kind", "id", "seeded", "category", "valid", "actionable", "specific", "duplicate_of", "note"]
    if header != want:
        raise ValueError(f"header must be exactly {','.join(want)} (got {','.join(rows[0])})")
    recall, grades, problems = {}, {}, []
    for n, row in enumerate(rows[1:], start=2):
        if not row or not any(c.strip() for c in row):
            continue
        if len(row) != len(want):
            problems.append(f"line {n}: expected {len(want)} columns, got {len(row)}")
            continue
        row = [c.strip() for c in row]
        kind, rid = row[0].lower(), row[1]
        if kind == "recall":
            recall[rid] = [x.strip() for x in row[2].split(";") if x.strip()]
        elif kind == "grade":
            def b(v):
                return v.lower() == "true"
            grades[rid] = {"seeded": row[2] or None, "category": row[3], "valid": b(row[4]),
                           "actionable": b(row[5]), "specific": b(row[6]),
                           "duplicate_of": row[7] or None, "note": row[8]}
        else:
            problems.append(f"line {n}: kind must be recall or grade (got {row[0]!r})")
    if problems:
        raise ValueError("; ".join(problems))
    if not grades:
        raise ValueError("no grade rows")
    return {"recall": recall, "grades": grades}


def run_judge(reqs: list, model: str, timeout: int = 600) -> dict:
    seeded = "\n".join(f"- {gid}: {text}" for gid, (text, _) in SEEDED.items())
    requirements = "\n".join(f"- {r['id']}: {r['statement']}" for r in reqs
                             if r.get("status") in (None, "active"))
    prompt = JUDGE_PROMPT.format(seeded=seeded, requirements=requirements)
    text, err, cost = _claude(prompt, model, timeout)
    if err:
        return {"error": err}
    try:
        verdict = parse_judge_csv(text)
    except ValueError as exc:
        # One repair call: reformat only, with the parser's complaint.
        repair = ("Your previous answer could not be parsed as the requested CSV. "
                  f"The parser said: {exc}\n\nReturn the same judgments again as ONLY the CSV "
                  "document with the exact header, correctly quoted, no prose, no code fence.\n\n"
                  "Previous answer:\n" + text)
        text2, err2, cost2 = _claude(repair, model, timeout)
        if err2:
            return {"error": err2, "first_parse_error": str(exc)}
        try:
            verdict = parse_judge_csv(text2)
        except ValueError as exc2:
            return {"error": f"judge CSV unparseable after repair: {exc2}", "raw": text2[-1500:]}
        verdict["repaired"] = str(exc)
        cost = (cost or 0) + (cost2 or 0)
    verdict["model"] = model
    verdict["cost_usd"] = cost
    return verdict


def audit(intent_dir: Path, judge_model=None) -> dict:
    reqs = load_snapshot(intent_dir)
    report = {
        "intent_dir": str(intent_dir),
        "summary": summarize(reqs),
        "recall": keyword_recall(reqs),
        "near_duplicates": near_duplicates(reqs),
        "requirements": {r["id"]: {k: r[k] for k in ("status", "confidence", "provenance_kind",
                                                       "provenance_ref", "domains", "hints", "statement")}
                         for r in reqs},
    }
    if judge_model:
        j = run_judge(reqs, judge_model)
        report["judge"] = j
        if "recall" in j:
            for gid in report["recall"]:
                report["recall"][gid]["judge_hits"] = j["recall"].get(gid, [])
        if "grades" in j:
            grades = j["grades"]
            unseeded = [g for g in grades.values() if not g.get("seeded")]
            report["summary"]["judged"] = {
                "graded": len(grades),
                "seeded_recall_hit": sum(1 for gid in SEEDED if j.get("recall", {}).get(gid)),
                "seeded_total": len(SEEDED),
                "unseeded": len(unseeded),
                "unseeded_valid": sum(1 for g in unseeded if g.get("valid")),
                "unseeded_actionable": sum(1 for g in unseeded if g.get("actionable")),
                "unseeded_dev_incident": sum(1 for g in unseeded if g.get("category") == "dev-incident"),
                "duplicates_flagged": sum(1 for g in grades.values() if g.get("duplicate_of")),
                "noise": sum(1 for g in grades.values() if g.get("category") == "noise"),
            }
    report["summary"]["keyword_recall_hit"] = sum(1 for g in report["recall"].values() if g["keyword_hits"])
    report["summary"]["seeded_total"] = len(SEEDED)
    return report


def main(argv=None) -> int:
    p = argparse.ArgumentParser(description=__doc__.split("\n\n")[0])
    p.add_argument("--intent", required=True, type=Path, help="an arm's .cloche/intent directory (or a snapshot of it)")
    p.add_argument("--judge-model", default="claude-sonnet-5")
    p.add_argument("--no-judge", action="store_true")
    p.add_argument("--out", type=Path)
    args = p.parse_args(argv)
    report = audit(args.intent, judge_model=None if args.no_judge else args.judge_model)
    text = json.dumps(report, indent=2, sort_keys=True)
    if args.out:
        args.out.write_text(text + "\n")
        print(json.dumps(report["summary"], indent=2))
    else:
        print(text)
    return 0


if __name__ == "__main__":
    sys.exit(main())
