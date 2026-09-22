"""Deterministic parts of intent_audit.py (no judge)."""
import json
import tempfile
import unittest
from pathlib import Path

import intent_audit as ia

REQ = """---
id: req-41c8
status: active
scope:
    level: domain
    domains:
        - evaluator
hints:
    - evaluator hits RecursionError on deeply nested Bract program
    - 'implementing chained calls f()()() '
confidence: high
user_edited: false
provenance:
    kind: prompt
    ref: 3a90-main
    extracted_at: 2026-09-22T19:35:00Z
    extracted_by: intent-scan
created: 2026-09-22T19:31:19Z
updated: 2026-09-22T19:31:19Z
---

The evaluator must be iterative for call and block evaluation — use an explicit stack/work-list.

**Why:** deep programs must not blow the Python recursion limit.
"""

REQ2 = """---
id: req-aaaa
status: active
scope:
    level: project
hints: []
confidence: medium
user_edited: false
provenance:
    kind: doc
    ref: DESIGN.md
---

String concatenation is `~`; `+` on strings is a runtime type error.
"""


class TestIntentAudit(unittest.TestCase):
    def setUp(self):
        self.tmp = Path(tempfile.mkdtemp())
        (self.tmp / "requirements").mkdir()
        (self.tmp / "requirements" / "req-41c8.md").write_text(REQ)
        (self.tmp / "requirements" / "req-aaaa.md").write_text(REQ2)

    def test_parse(self):
        r = ia.parse_requirement(self.tmp / "requirements" / "req-41c8.md")
        self.assertEqual(r["id"], "req-41c8")
        self.assertEqual(r["confidence"], "high")
        self.assertEqual(r["provenance_kind"], "prompt")
        self.assertEqual(r["provenance_ref"], "3a90-main")
        self.assertEqual(r["domains"], ["evaluator"])
        self.assertEqual(len(r["hints"]), 2)
        self.assertTrue(r["statement"].startswith("The evaluator must be iterative"))
        self.assertIn("recursion limit", r["why"])

    def test_keyword_recall_and_summary(self):
        report = ia.audit(self.tmp)
        self.assertEqual(report["recall"]["D2"]["keyword_hits"], ["req-41c8"])
        self.assertEqual(report["recall"]["Q3"]["keyword_hits"], ["req-aaaa"])
        self.assertEqual(report["recall"]["D5"]["keyword_hits"], [])
        self.assertEqual(report["summary"]["total"], 2)
        self.assertEqual(report["summary"]["by_provenance_kind"], {"prompt": 1, "doc": 1})
        self.assertGreaterEqual(report["summary"]["keyword_recall_hit"], 2)
        self.assertEqual(report["near_duplicates"], [])
        json.dumps(report)  # serializable

    def test_near_duplicates(self):
        (self.tmp / "requirements" / "req-bbbb.md").write_text(REQ2.replace("req-aaaa", "req-bbbb"))
        report = ia.audit(self.tmp)
        self.assertEqual(report["near_duplicates"][0]["jaccard"], 1.0)


if __name__ == "__main__":
    unittest.main()
