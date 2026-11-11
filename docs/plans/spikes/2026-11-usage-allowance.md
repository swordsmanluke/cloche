# Spike: can the Claude usage allowance be read by a program?

Date: 2026-11-11. Claude Code 2.1.322, subscription login (`authMethod: claude.ai`,
`subscriptionType: max`, `planTier: max_20x`, as reported by `claude auth status`).
Design: `docs/plans/2026-11-10-autonomous-loop-design.md`, Part 1 "The unit".
Rulings: CLOCHE-A106, CLOCHE-A124.

## Answer

**Yes — read it.** Two routes work; I stopped at the first per the task order, then
checked the second because it is cheaper for cloche.

## 1. What works

### 1a. `claude -p "/usage"` (headless, no model call)

```
$ claude -p "/usage"
You are currently using your subscription to power your Claude Code usage

Current week (all models): 32% used · resets Nov 11, 12:59am (UTC)
Current week (Fable): 40% used · resets Nov 11, 12:59am (UTC)

What's contributing to your usage?
Approximate · sessions on this machine only · shares overlap

Last 24h · 11 requests · 4 sessions
  100%  Run fewer sessions at once · up to 4 at once

Last 7d · 11 requests · 4 sessions
  100%  Run fewer sessions at once · up to 4 at once
```

`--output-format json` wraps the same text in `result` and marks it `"local_command":"usage"`,
`"num_turns":0`, `"total_cost_usd":0`, `"duration_api_ms":0`, all token counts 0:

```
{"is_error":false,"duration_api_ms":0,"num_turns":0,"total_cost_usd":0,...,
 "subtype":"success","result":"You are currently using your subscription ...\n\nCurrent week (all models): 32% used · resets Nov 11, 12:59am (UTC)\nCurrent week (Fable): 40% used · resets Nov 11, 12:59am (UTC)","local_command":"usage","type":"result","duration_ms":148,...}
```

The output is human text, not a structured schema; parsing it means a regex on
`Current week \((.+)\): (\d+)% used · resets (.+)`. The "What's contributing" section is
local-machine only (see below) and should be ignored.

### 1b. `rate_limit_event` in stream-json (structured, no extra call)

Every `claude -p ... --output-format stream-json --verbose` run emits an event (observed
on a trivial `say hi` prompt):

```
{"type":"rate_limit_event","rate_limit_info":{"status":"allowed","resetsAt":1794358800,
 "rateLimitType":"seven_day","overageStatus":"rejected","overageDisabledReason":"org_level_disabled",
 "isUsingOverage":false,
 "unifiedWindows":{"seven_day":{"utilization":0.32,"resetsAt":1794358800,"status":"allowed"},
                   "seven_day_overage_included":{"utilization":0.4,"resetsAt":1794358800,"status":"allowed"}}},
 "uuid":"...","session_id":"..."}
```

`resetsAt` 1794358800 = 2026-11-11 01:00:00 UTC, matching the `/usage` text ("12:59am",
i.e. rounded from just before the hour). `utilization` 0.32 / 0.40 match the 32% / 40%
in `/usage` at the same moment.

Cloche's prompt adapter already receives this line and currently drops it
(`internal/adapters/agents/prompt/extract_test.go`, `TestExtractStreamText_RateLimitEvent`).
A `/usage` run emits no such event (0 `rate_limit_event` lines observed), so the two routes
are not interchangeable: 1b appears only when a real model request is made.

## 2. What it reports

| Question | Observed |
|---|---|
| Percent or absolute? | Percent only. No token counts or absolute allowance size anywhere in the output. Text is integer percent; the event carries a fraction (0.32), and both showed the same value, so effective resolution looked like 1%. |
| Windows | Weekly (`seven_day`) only. **No short (5-hour) window appeared** in either route for this account. I can't say whether other plans/tiers show one. |
| Per model or combined? | Both: "all models" row plus a per-model row ("Fable" = 40%; event key `seven_day_overage_included`, whose mapping to the Fable row I inferred from the matching 0.40 value — not documented). |
| Whole account or this machine? | The two weekly percentages are account-level: the value is reported by the server, not derived from local files. The "What's contributing" breakdown is explicitly "sessions on this machine only" and "Approximate". I did not run a second machine, so "covers interactive use on other machines" is inferred from the figure being server-supplied, not observed. |
| Auth | Uses the subscription login in `~/.claude/.credentials.json` (OAuth). Works inside this container with no flags. |

## 3. Staleness and cost

- **Cost of `/usage`:** ~0.9 s wall (`time` measured 0.858 s; reported `duration_ms` 148),
  zero tokens, zero cost (`total_cost_usd: 0`, all usage fields 0). It does not consume
  allowance.
- **Cost of the event:** free, but only as a side effect of a real request (`say hi` = 2 input /
  14 output tokens, $0.0049 reported, ~1.4 s).
- **Staleness:** not measured well. A ~16-token request between two `/usage` calls moved
  neither number (32% / 40% both times), which is expected at 1% granularity and tells us
  nothing about server-side lag. I did not generate enough usage to see a figure change, so the
  update latency is **unknown**. Treat the event as "as of the latest response" and `/usage` as
  "as of the call", and measure lag before relying on tight caps.

## 4. Recommendation: **read it**

- Poll `claude -p "/usage"` from the daemon (host-side, with the same login the runs use) at
  the day boundary and periodically (e.g. each few minutes while launching work). At ~1 s and
  zero tokens it is cheap enough to call before every launch decision.
- Optionally also capture `rate_limit_info.unifiedWindows` from the stream-json of each agent
  step to get a free, fresher reading after each run.
- Use the "Current week (all models)" percentage for the cap. Compute "used today" as the
  difference from a reading taken at the start of the day (the percentage is a running
  weekly total, so the daily share is a delta; the weekly reset zeroes the baseline, handle
  `resetsAt` changing).

Caveats the implementation must handle:
1. Output is free-form text from an interactive command; the format may change between Claude Code
   versions. Parse defensively, and fall back to the **estimate** path (tokens × per-model weight)
   on parse failure. The estimate cannot see interactive usage.
2. 1% granularity: a 10%/day cap works; a cap of a fraction of a percent will not.
3. Only the weekly window was observed; the design's decision not to cap the short window
   (CLOCHE-A125) happens to match.
4. Codex and other providers are not covered by this spike.

## What was tried

1. CLI surface: `claude --help`, `claude auth status` (reports plan tier only, no usage),
   `claude -p "/usage"` — works.
2. stream-json: `rate_limit_event` carries utilization — works.
3. Local files (`~/.claude`): not pursued, since 1 and 2 worked. Listing showed only
   credentials, settings, sessions, projects transcripts and caches. No usage file was
   identified.
   I did not call undocumented HTTP endpoints or read the OAuth token.
