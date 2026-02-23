"""Score the current working tree diff with speedometer.

Requires SPEEDOMETER_HOME to point at the speedometer checkout
(defaults to ~/projects/speedometer). Needs pyyaml installed.

Exit protocol: prints CLOCHE_RESULT:success or CLOCHE_RESULT:fail.
"""

import os
import subprocess
import sys

THRESHOLD = 0.6  # composite score >= this triggers failure

speedometer_home = os.environ.get(
    "SPEEDOMETER_HOME",
    os.path.expanduser("~/projects/speedometer"),
)
sys.path.insert(0, speedometer_home)

from aggregator import score_pr  # noqa: E402
from metrics.diff_utils import filter_diff  # noqa: E402


def main():
    diff_result = subprocess.run(
        ["git", "diff", "HEAD"],
        capture_output=True,
        text=True,
    )
    raw_diff = diff_result.stdout

    if not raw_diff.strip():
        print("No changes to score.")
        print("CLOCHE_RESULT:success")
        return

    diff = filter_diff(raw_diff)
    if not diff.strip():
        print("All changed files filtered out (generated/vendored). Skipping.")
        print("CLOCHE_RESULT:success")
        return

    added = sum(1 for line in raw_diff.splitlines() if line.startswith("+") and not line.startswith("+++"))
    removed = sum(1 for line in raw_diff.splitlines() if line.startswith("-") and not line.startswith("---"))

    meta = {
        "title": "workflow changes",
        "additions": added,
        "deletions": removed,
    }

    config_path = os.path.join(speedometer_home, "config", "metrics.yaml")
    scores = score_pr(diff, meta, config_path=config_path)
    composite = scores["composite_score"]

    print(f"Speedometer composite score: {composite:.3f}")
    print(f"Metrics evaluated: {scores['metrics_run']}")
    print()

    ranked = sorted(
        scores["metric_results"].items(),
        key=lambda x: x[1]["score"] * x[1]["confidence"],
        reverse=True,
    )
    for name, v in ranked[:5]:
        if v["score"] > 0.05:
            print(f"  {name}: score={v['score']:.2f} confidence={v['confidence']:.2f}")

    print()
    if composite >= THRESHOLD:
        print(f"FAIL: composite score {composite:.3f} exceeds threshold {THRESHOLD}")
        print("CLOCHE_RESULT:fail")
    else:
        print(f"PASS: composite score {composite:.3f} within threshold {THRESHOLD}")
        print("CLOCHE_RESULT:success")


if __name__ == "__main__":
    main()
