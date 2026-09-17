// Layout regression test for the task-detail step strip (static/style.css
// .console-step-strip): a real browser must be used because this bug is a
// flexbox-shrink clipping issue that jsdom's non-rendering DOM can't catch
// (getBoundingClientRect is always zeroed there). Boots the real
// templates/layout.html + templates/console.html + static/console.js +
// static/style.css in headless Firefox via Playwright, against a fully
// route-mocked backend (no real cloched) — no real network, no Go server.
//
// Also covers the step-strip's "focal cell" treatment: the running (or,
// failing that, first-failed) cell keeps a filled background and a visible
// duration; every other cell shows dot + name only, with its duration
// revealed on hover/focus. Screenshots of a succeeded and a failed task
// (before/after this change) should be captured by hand when this suite is
// run somewhere with the Playwright browser available — see the class-level
// note below on why that can't happen in every environment.
//
// Requires `npx playwright install firefox` (and, on a bare Linux host,
// the system libraries Firefox itself depends on) before running. Not part
// of `npm test` for that reason — run explicitly with `npm run test:e2e`,
// or `node --test console-step-strip-layout.test.js`.
'use strict';

const test = require('node:test');
const assert = require('node:assert/strict');
const fs = require('node:fs');
const path = require('node:path');
const { firefox } = require('playwright');

const LAYOUT_SRC = fs.readFileSync(path.join(__dirname, 'templates/layout.html'), 'utf8');
const CONSOLE_TABS_SRC = fs.readFileSync(path.join(__dirname, 'static/console-tabs.js'), 'utf8');
const CONSOLE_JS_SRC = fs.readFileSync(path.join(__dirname, 'static/console.js'), 'utf8');
const STYLE_CSS_SRC = fs.readFileSync(path.join(__dirname, 'static/style.css'), 'utf8');

const PROJECT_SLUG = 'myproj';
const RUNNING_TASK_ID = 'cloche-eiil8';
const RUNNING_RUN_ID = 'run-eiil8-1';
const FAILED_TASK_ID = 'cloche-usjb1';
const FAILED_RUN_ID = 'run-usjb1-3';

// Real browsers report step-strip layout at 1600px wide — the width named
// by the cell-name-truncation requirement this suite guards ("cell names
// must not truncate below 12 characters at 1600px wide").
const VIEWPORT = { width: 1600, height: 900 };

// Builds the real served page: templates/layout.html's "content" and
// "scripts" blocks filled in exactly as handler_console.go would for
// GET /{slug}/{taskId}, so this test breaks if the markup or console.js's
// element IDs drift, instead of testing a hand-copied fixture that could
// silently go stale.
function pageHTML(taskId) {
    const contentRaw = fs.readFileSync(path.join(__dirname, 'templates/console.html'), 'utf8');
    const contentStart = contentRaw.indexOf('{{define "content"}}') + '{{define "content"}}'.length;
    const contentEnd = contentRaw.indexOf('{{define "scripts"}}');
    const content = contentRaw.slice(contentStart, contentEnd)
        .replace(/\{\{end\}\}\s*$/, '')
        .replace('{{.ProjectSlug}}', PROJECT_SLUG)
        .replace('{{.TaskID}}', taskId)
        .replace('{{clocheVersion}}', 'test');

    return LAYOUT_SRC
        .replace('{{define "layout"}}', '')
        .replace(/\{\{end\}\}\s*$/, '')
        .replace('{{.Title}}', 'Cloche')
        .replace('{{block "content" .}}{{end}}', content)
        .replace('{{block "scripts" .}}{{end}}', '<script src="/static/console-tabs.js"></script><script src="/static/console.js"></script>');
}

// A step strip with many steps of varying name length, matching the bug
// report's repro ("any task with several steps") and also exercising the
// strip's horizontal scroll (long names/many steps) at the same time as
// the vertical-clipping assertion below. The last step is left running
// (empty result, started_at set) so this fixture doubles as the "running
// task" focal-cell scenario.
function runningStepsFixture() {
    const names = [
        'claim-task', 'prepare-workspace', 'implement-feature-with-a-long-descriptive-name',
        'run-unit-tests', 'run-integration-tests', 'self-review', 'address-feedback',
        'open-pull-request', 'poll-pr-checks', 'merge-to-base', 'finalize-and-cleanup'
    ];
    const now = Date.now();
    return names.map(function (name, i) {
        return {
            run_id: RUNNING_RUN_ID,
            step_name: name,
            depth: 0,
            result: i < names.length - 1 ? 'success' : '',
            started_at: new Date(now - (names.length - i) * 60000).toISOString(),
            duration: i < names.length - 1 ? (30 + i) + 's' : null
        };
    });
}

// A workflow step ("develop") with its inlined child-run steps, the middle
// one failing and the rest after it never starting — the needs-you/failed
// shape from docs/design/console-restructured-mock.html's "usjb" example.
function failedStepsFixture() {
    return [
        { run_id: FAILED_RUN_ID, step_name: 'claim-task', depth: 0, result: 'success', duration: '1.1s' },
        { run_id: FAILED_RUN_ID, step_name: 'develop', depth: 0, result: 'fail', duration: '7m 40s' },
        { run_id: 'child-' + FAILED_RUN_ID, step_name: 'implement', depth: 1, result: 'success', duration: '5m 12s' },
        { run_id: 'child-' + FAILED_RUN_ID, step_name: 'test', depth: 1, result: 'fail', duration: '2m 28s' },
        { run_id: 'child-' + FAILED_RUN_ID, step_name: 'review', depth: 1, result: '', started_at: '' },
        { run_id: FAILED_RUN_ID, step_name: 'finalize', depth: 0, result: '', started_at: '' }
    ];
}

// A long, realistic log body so the log pane's intrinsic (max-content)
// height is large enough to force the centre column's flex children to
// shrink — the exact condition that exposed the bug: the log pane is the
// sole flex: 1 1 auto band, but the step strip was missing flex: 0 0 auto,
// so it shrank and clipped along with it.
function sseLogBody() {
    const parts = ['event: meta\ndata: {"skipped":0}\n\n'];
    for (let i = 0; i < 250; i++) {
        const line = { timestamp: new Date().toISOString(), type: 'script', step_name: 'run-unit-tests', content: 'line ' + i + ': ' + 'building and testing the project '.repeat(2) };
        parts.push('data: ' + JSON.stringify(line) + '\n\n');
    }
    parts.push('event: done\ndata: {}\n\n');
    return parts.join('');
}

function jsonRoute(route, body) {
    return route.fulfill({ status: 200, contentType: 'application/json', body: JSON.stringify(body) });
}

// mockBackend serves one task/run's worth of fixture data — the task ID,
// run ID, run state and step list are all parameterized so the running and
// failed scenarios below can share this same route table.
async function mockBackend(page, opts) {
    await page.route('**/*', function (route) {
        const req = route.request();
        const url = new URL(req.url());
        const p = url.pathname;

        if (p === '/' + PROJECT_SLUG + '/' + opts.taskId) {
            return route.fulfill({ status: 200, contentType: 'text/html', body: pageHTML(opts.taskId) });
        }
        if (p === '/static/console-tabs.js') {
            return route.fulfill({ status: 200, contentType: 'application/javascript', body: CONSOLE_TABS_SRC });
        }
        if (p === '/static/console.js') {
            return route.fulfill({ status: 200, contentType: 'application/javascript', body: CONSOLE_JS_SRC });
        }
        if (p === '/static/style.css') {
            return route.fulfill({ status: 200, contentType: 'text/css', body: STYLE_CSS_SRC });
        }
        if (p === '/api/projects') {
            return jsonRoute(route, [{ slug: PROJECT_SLUG, name: PROJECT_SLUG }]);
        }
        if (/\/tasks\/stack$/.test(p)) {
            return jsonRoute(route, {
                needs_you: [], queued: [], done: [],
                running: [{ task_id: opts.taskId, title: opts.title, run_id: opts.runId, kind: 'main', since: new Date().toISOString() }]
            });
        }
        if (new RegExp('/tasks/' + opts.taskId + '/attempts$').test(p)) {
            return jsonRoute(route, {
                title: opts.title,
                attempts: [{ attempt_num: 1, attempt_id: 'a1', run_id: opts.runId, outcome: opts.status === 'failed' ? 'failed' : '', started_at: new Date().toISOString(), duration: '' }]
            });
        }
        if (p === '/api/runs/' + opts.runId) {
            return jsonRoute(route, { id: opts.runId, task_id: opts.taskId, title: opts.title, state: opts.status, steps: opts.steps });
        }
        if (p === '/api/attempts/' + encodeURIComponent('a1') + '/stream' || p === '/api/runs/' + opts.runId + '/stream') {
            return route.fulfill({ status: 200, contentType: 'text/event-stream', body: sseLogBody() });
        }
        // Everything else (instruments, ticker, ledger, workflows, thread,
        // etc.) is irrelevant to this layout assertion — answer with an
        // inert empty body so those pollers don't error.
        return jsonRoute(route, {});
    });
}

async function gotoTask(page, opts) {
    await mockBackend(page, opts);
    await page.goto('http://cloche.test/' + PROJECT_SLUG + '/' + opts.taskId);
    await page.waitForSelector('.console-step-strip .console-step-segment');
}

test('console-step-strip: every segment stays fully above the log viewer (no flex-shrink clipping), and cell names never truncate', async function () {
    const browser = await firefox.launch({ headless: true });
    try {
        const page = await browser.newPage({ viewport: VIEWPORT });
        await gotoTask(page, { taskId: RUNNING_TASK_ID, runId: RUNNING_RUN_ID, title: 'CSS bug: step strip clipped', status: 'running', steps: runningStepsFixture() });

        // Let the mocked SSE stream's onmessage handlers flush into the DOM.
        await page.waitForFunction(function () {
            var pre = document.getElementById('console-log-content');
            return !!pre && pre.children.length > 100;
        }, { timeout: 5000 });

        const logPaneBox = await page.locator('.console-log-pane').boundingBox();
        assert.ok(logPaneBox, 'log pane must be present and rendered');

        const segments = await page.locator('.console-step-strip .console-step-segment').all();
        assert.ok(segments.length > 1, 'fixture must render multiple step segments');

        for (const segment of segments) {
            const box = await segment.boundingBox();
            assert.ok(box, 'every step segment must have a bounding box');
            assert.ok(
                box.y + box.height <= logPaneBox.y + 0.5,
                'step segment bottom (' + (box.y + box.height) + ') must sit at or above the log pane top (' + logPaneBox.y + ') — ' +
                'if this fails, .console-step-strip lost its flex: 0 0 auto and is being shrunk/clipped by the log pane again'
            );

            // The row-strip clipping bug doesn't push segments below the log
            // pane's top edge — align-items: stretch on the flex-shrunk strip
            // instead squashes each segment's own box, so its two-line grid
            // content (name row + meta row) overflows past its own bottom
            // edge and gets clipped there. scrollHeight > clientHeight is the
            // direct signal for that; the boundingBox check above stays as a
            // second guard against the strip container itself overflowing
            // into the log pane.
            const overflow = await segment.evaluate(function (el) {
                return { scrollHeight: el.scrollHeight, clientHeight: el.clientHeight };
            });
            assert.ok(
                overflow.scrollHeight <= overflow.clientHeight + 0.5,
                'step segment content (scrollHeight ' + overflow.scrollHeight + ') must fit within its own box (clientHeight ' + overflow.clientHeight + ') — ' +
                'if this fails, the segment is being squashed and its step name is clipped'
            );

            // No cell may truncate its name below 12 characters at this
            // width — in practice the name span carries no overflow/
            // ellipsis styling at all, so this should never clip regardless
            // of length; scrollWidth > clientWidth would mean a truncation
            // style crept back in.
            const nameOverflow = await segment.locator('.console-step-segment-name').evaluate(function (el) {
                return { scrollWidth: el.scrollWidth, clientWidth: el.clientWidth, text: el.textContent };
            });
            assert.ok(
                nameOverflow.scrollWidth <= nameOverflow.clientWidth + 0.5,
                'step name "' + nameOverflow.text.trim() + '" must not be truncated (scrollWidth ' + nameOverflow.scrollWidth + ' > clientWidth ' + nameOverflow.clientWidth + ')'
            );
        }
    } finally {
        await browser.close();
    }
});

test('console-step-strip: the running step is the filled focal cell; other cells reveal their duration only on hover/focus', async function () {
    const browser = await firefox.launch({ headless: true });
    try {
        const page = await browser.newPage({ viewport: VIEWPORT });
        await gotoTask(page, { taskId: RUNNING_TASK_ID, runId: RUNNING_RUN_ID, title: 'CSS bug: step strip clipped', status: 'running', steps: runningStepsFixture() });

        const segments = page.locator('.console-step-strip .console-step-segment');
        const focal = page.locator('.console-step-strip .console-step-segment-focal');
        await assert.doesNotReject(focal.waitFor({ state: 'attached', timeout: 5000 }));
        assert.equal(await focal.count(), 1, 'exactly one segment should be focal');

        const focalName = (await focal.locator('.console-step-segment-name').textContent()).trim();
        assert.ok(focalName.indexOf('finalize-and-cleanup') !== -1, 'the running (last) step should be the focal cell, got: ' + focalName);

        const focalStyle = await focal.evaluate(function (el) {
            var cs = getComputedStyle(el);
            var meta = el.querySelector('.console-step-segment-meta');
            return { background: cs.backgroundColor, metaOpacity: getComputedStyle(meta).opacity, metaText: meta.textContent };
        });
        assert.equal(focalStyle.background, 'rgb(34, 43, 39)', 'focal running cell should be filled with --pnl3 (#222b27)');
        assert.equal(focalStyle.metaOpacity, '1', 'the focal cell duration must always be visible');
        assert.ok(/running/.test(focalStyle.metaText), 'the live focal cell should show a running indicator, got: ' + focalStyle.metaText);

        const dotColor = await focal.locator('.run-dot').evaluate(function (el) { return getComputedStyle(el).backgroundColor; });
        assert.equal(dotColor, 'rgb(90, 169, 255)', 'the focal running cell dot should use --run (#5aa9ff)');

        // A non-focal cell: duration hidden until hover/focus, surfaced via
        // the title attribute in the meantime.
        const first = segments.first();
        const beforeHover = await first.evaluate(function (el) {
            return { opacity: getComputedStyle(el.querySelector('.console-step-segment-meta')).opacity, title: el.title };
        });
        assert.equal(beforeHover.opacity, '0', 'a non-focal cell should hide its duration by default');
        assert.ok(beforeHover.title.length > 0, 'a non-focal cell should carry its duration in the title attribute');

        await first.hover();
        const afterHover = await first.evaluate(function (el) {
            return getComputedStyle(el.querySelector('.console-step-segment-meta')).opacity;
        });
        assert.equal(afterHover, '1', 'hovering a non-focal cell should reveal its duration');

        await first.focus();
        const afterFocus = await first.evaluate(function (el) {
            return getComputedStyle(el.querySelector('.console-step-segment-meta')).opacity;
        });
        assert.equal(afterFocus, '1', 'focusing a non-focal cell should also reveal its duration');
    } finally {
        await browser.close();
    }
});

test('console-step-strip: on a failed task, the first failed step (not the un-started ones after it) is the filled focal cell', async function () {
    const browser = await firefox.launch({ headless: true });
    try {
        const page = await browser.newPage({ viewport: VIEWPORT });
        await gotoTask(page, { taskId: FAILED_TASK_ID, runId: FAILED_RUN_ID, title: 'Intent A/B: bonsai executor wrapper', status: 'failed', steps: failedStepsFixture() });

        const focal = page.locator('.console-step-strip .console-step-segment-focal');
        assert.equal(await focal.count(), 1, 'exactly one segment should be focal');
        assert.equal(await focal.locator('.console-step-segment-child').count(), 0); // sanity: locator scoped correctly

        const focalInfo = await focal.evaluate(function (el) {
            return { name: el.querySelector('.console-step-segment-name').textContent.trim(), background: getComputedStyle(el).backgroundColor };
        });
        assert.equal(focalInfo.name, 'test', 'the first failed step ("test", nested under "develop") should be focal, not "develop" itself or the never-run "review"/"finalize"');

        // color-mix(in srgb, var(--bad) 18%, transparent) resolves to the
        // --bad rgb triple at partial alpha — assert the hue matches --bad
        // and the cell isn't fully opaque (which would mean the running
        // --pnl3 fill leaked into the failed case) or fully transparent
        // (which would mean the focal styling didn't apply at all).
        const m = focalInfo.background.match(/^rgba?\((\d+),\s*(\d+),\s*(\d+)(?:,\s*([\d.]+))?\)$/);
        assert.ok(m, 'expected an rgb/rgba background, got: ' + focalInfo.background);
        assert.equal(m[1] + ',' + m[2] + ',' + m[3], '239,124,114', 'focal failed cell should be tinted with --bad (#ef7c72)');
        const alpha = m[4] === undefined ? 1 : parseFloat(m[4]);
        assert.ok(alpha > 0 && alpha < 1, 'focal failed cell should be --bad at low alpha, not fully opaque or fully transparent, got alpha=' + alpha);
    } finally {
        await browser.close();
    }
});
