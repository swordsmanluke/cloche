// Layout regression test for the task-detail step strip (static/style.css
// .console-step-strip): a real browser must be used because this bug is a
// flexbox-shrink clipping issue that jsdom's non-rendering DOM can't catch
// (getBoundingClientRect is always zeroed there). Boots the real
// templates/layout.html + templates/console.html + static/console.js +
// static/style.css in headless Firefox via Playwright, against a fully
// route-mocked backend (no real cloched) — no real network, no Go server.
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
const TASK_ID = 'cloche-eiil8';
const RUN_ID = 'run-eiil8-1';

// Builds the real served page: templates/layout.html's "content" and
// "scripts" blocks filled in exactly as handler_console.go would for
// GET /{slug}/{taskId}, so this test breaks if the markup or console.js's
// element IDs drift, instead of testing a hand-copied fixture that could
// silently go stale.
function pageHTML() {
    const contentRaw = fs.readFileSync(path.join(__dirname, 'templates/console.html'), 'utf8');
    const contentStart = contentRaw.indexOf('{{define "content"}}') + '{{define "content"}}'.length;
    const contentEnd = contentRaw.indexOf('{{define "scripts"}}');
    const content = contentRaw.slice(contentStart, contentEnd)
        .replace(/\{\{end\}\}\s*$/, '')
        .replace('{{.ProjectSlug}}', PROJECT_SLUG)
        .replace('{{.TaskID}}', TASK_ID)
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
// the vertical-clipping assertion below.
function stepsFixture() {
    const names = [
        'claim-task', 'prepare-workspace', 'implement-feature-with-a-long-descriptive-name',
        'run-unit-tests', 'run-integration-tests', 'self-review', 'address-feedback',
        'open-pull-request', 'poll-pr-checks', 'merge-to-base', 'finalize-and-cleanup'
    ];
    const now = Date.now();
    return names.map(function (name, i) {
        return {
            run_id: RUN_ID,
            step_name: name,
            depth: 0,
            result: i < names.length - 1 ? 'success' : '',
            started_at: new Date(now - (names.length - i) * 60000).toISOString(),
            duration: i < names.length - 1 ? (30 + i) + 's' : null
        };
    });
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

async function mockBackend(page) {
    await page.route('**/*', function (route) {
        const req = route.request();
        const url = new URL(req.url());
        const p = url.pathname;

        if (p === '/' + PROJECT_SLUG + '/' + TASK_ID) {
            return route.fulfill({ status: 200, contentType: 'text/html', body: pageHTML() });
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
                running: [{ task_id: TASK_ID, title: 'CSS bug: step strip clipped', run_id: RUN_ID, kind: 'main', since: new Date().toISOString() }]
            });
        }
        if (new RegExp('/tasks/' + TASK_ID + '/attempts$').test(p)) {
            return jsonRoute(route, {
                title: 'CSS bug: step strip clipped',
                attempts: [{ attempt_num: 1, attempt_id: 'a1', run_id: RUN_ID, outcome: '', started_at: new Date().toISOString(), duration: '' }]
            });
        }
        if (p === '/api/runs/' + RUN_ID) {
            return jsonRoute(route, { id: RUN_ID, task_id: TASK_ID, title: 'CSS bug: step strip clipped', status: 'running', steps: stepsFixture() });
        }
        if (p === '/api/attempts/' + encodeURIComponent('a1') + '/stream' || p === '/api/runs/' + RUN_ID + '/stream') {
            return route.fulfill({ status: 200, contentType: 'text/event-stream', body: sseLogBody() });
        }
        // Everything else (instruments, ticker, ledger, workflows, thread,
        // etc.) is irrelevant to this layout assertion — answer with an
        // inert empty body so those pollers don't error.
        return jsonRoute(route, {});
    });
}

test('console-step-strip: every segment stays fully above the log viewer (no flex-shrink clipping)', async function () {
    const browser = await firefox.launch({ headless: true });
    try {
        const page = await browser.newPage({ viewport: { width: 1440, height: 900 } });
        await mockBackend(page);
        await page.goto('http://cloche.test/' + PROJECT_SLUG + '/' + TASK_ID);

        await page.waitForSelector('.console-step-strip .console-step-segment');
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
        }
    } finally {
        await browser.close();
    }
});
