// Unit tests for the console foot bar described in
// docs/design/console-restructured-mock.html (.m5 .foot): the activity
// ticker on the left and a short, contextual set of key hints on the right
// that changes with the selected task's state. Runs the real
// static/console-tabs.js + static/console.js in a jsdom window against a
// stubbed fetch — no real network, no Go server.
//
// Run with: npm test (from internal/adapters/web/), or
// node --test internal/adapters/web/console-foot.test.js
'use strict';

const test = require('node:test');
const assert = require('node:assert/strict');
const fs = require('node:fs');
const path = require('node:path');
const { JSDOM } = require('jsdom');

const CONSOLE_TABS_SRC = fs.readFileSync(path.join(__dirname, 'static/console-tabs.js'), 'utf8');
const CONSOLE_JS_SRC = fs.readFileSync(path.join(__dirname, 'static/console.js'), 'utf8');

function consoleContentMarkup(projectSlug) {
    const raw = fs.readFileSync(path.join(__dirname, 'templates/console.html'), 'utf8');
    const start = raw.indexOf('{{define "content"}}') + '{{define "content"}}'.length;
    const end = raw.indexOf('{{define "scripts"}}');
    let content = raw.slice(start, end).replace(/\{\{end\}\}\s*$/, '');
    content = content
        .replace('{{.ProjectSlug}}', projectSlug)
        .replace('{{.TaskID}}', '')
        .replace('{{clocheVersion}}', 'test');
    return content;
}

function jsonResponse(body, headers) {
    return Promise.resolve({
        ok: true,
        status: 200,
        headers: { get: (k) => (headers && headers[k]) || null },
        json: () => Promise.resolve(body)
    });
}

function delay(ms) {
    return new Promise((resolve) => setTimeout(resolve, ms));
}

// FakeEventSource stands in for the browser EventSource jsdom doesn't ship,
// so opening a task's log pane (which the foot-key tests exercise as a side
// effect of selecting a task) doesn't throw.
function installFakeEventSource(window) {
    window.EventSource = function () {
        this.close = function () {};
        this.addEventListener = function () {};
    };
}

// installTestShims patches things jsdom doesn't implement (scrollIntoView,
// called whenever a stack row is selected) and makes window.close() clear
// every setInterval the app registered (stack/instruments/ticker polling) —
// otherwise a timer can fire after the test's window/document is gone,
// which the test runner blames on whatever test happens to be running then.
function installTestShims(window) {
    window.HTMLElement.prototype.scrollIntoView = function () {};

    const intervalIds = [];
    const origSetInterval = window.setInterval.bind(window);
    window.setInterval = function (fn, ms) {
        const id = origSetInterval(fn, ms);
        intervalIds.push(id);
        return id;
    };
    const origClose = window.close.bind(window);
    window.close = function () {
        intervalIds.forEach((id) => window.clearInterval(id));
        origClose();
    };
}

function makeStubFetch(stack, attemptsByTask, runsById, activityEntries) {
    return function (url) {
        if (url === '/api/projects') return jsonResponse([]);
        if (/\/tasks\/stack(\?|$)/.test(url)) return jsonResponse(stack, { ETag: 'W/"stack-1"' });
        var attemptsMatch = url.match(/\/tasks\/([^/]+)\/attempts$/);
        if (attemptsMatch) {
            var taskId = decodeURIComponent(attemptsMatch[1]);
            return jsonResponse(attemptsByTask[taskId] || { attempts: [] });
        }
        var runMatch = url.match(/\/api\/runs\/([^/]+)$/);
        if (runMatch) {
            var runId = decodeURIComponent(runMatch[1]);
            return jsonResponse((runsById && runsById[runId]) || { state: 'running' });
        }
        if (/^\/api\/activity/.test(url)) return jsonResponse({ entries: activityEntries || [] });
        return jsonResponse({});
    };
}

async function bootConsole(opts) {
    const dom = new JSDOM(
        '<!DOCTYPE html><html><body>' + consoleContentMarkup('myproj') + '</body></html>',
        { url: 'http://localhost/myproj', runScripts: 'outside-only' }
    );
    const { window } = dom;
    installFakeEventSource(window);
    installTestShims(window);
    window.fetch = makeStubFetch(opts.stack, opts.attemptsByTask || {}, opts.runsById || {}, opts.activityEntries);
    window.eval(CONSOLE_TABS_SRC);
    window.eval(CONSOLE_JS_SRC);

    // Let the init()-triggered fetch/.then chains (loadStack, which starts
    // stack polling once it resolves) fully drain before returning — a bare
    // `await` only yields one microtask turn, not enough to unwind a chain
    // of several .then() hops, so a not-yet-registered polling interval can
    // otherwise survive past this function's window.close() shim below.
    await delay(0);

    const deadline = Date.now() + 2000;
    while (Date.now() < deadline) {
        if (window.document.getElementById('console-keys')) break;
        await delay(20);
    }
    return window;
}

function footHints(window) {
    return Array.from(window.document.querySelectorAll('#console-keys > span')).map((span) => span.textContent.trim());
}

async function selectFirstRow(window) {
    let row = null;
    let deadline = Date.now() + 2000;
    while (Date.now() < deadline) {
        row = window.document.querySelector('.console-stack-row');
        if (row) break;
        await delay(20);
    }
    row.dispatchEvent(new window.Event('click', { bubbles: true }));
    // Selecting a row kicks off attempts -> run-detail fetches before the
    // header/footer re-render; give the stubbed promises a turn to resolve.
    deadline = Date.now() + 2000;
    while (Date.now() < deadline) {
        var header = window.document.getElementById('console-task-header');
        if (header && header.children.length) break;
        await delay(20);
    }
}

test('no task selected: short baseline hints, no task-specific keys', async () => {
    const window = await bootConsole({ stack: { needs_you: [], running: [], queued: [], done_today: [] } });
    try {
        assert.deepEqual(footHints(window), ['j/k task', 'enter open', 'tab project', 'a activity']);
    } finally {
        window.close();
    }
});

test('running task: j/k, attempt, project, follow, activity — no g/G or filter', async () => {
    const window = await bootConsole({
        stack: {
            needs_you: [], queued: [], done_today: [],
            running: [{ task_id: 'cloche-fnn6', title: 'hidden acceptance corpus', run_id: 'r1', attempt: 1, elapsed_seconds: 10 }]
        },
        attemptsByTask: {
            'cloche-fnn6': { title: 'hidden acceptance corpus', attempts: [{ attempt_num: 1, attempt_id: 'a1', run_id: 'r1', outcome: '' }] }
        },
        runsById: { r1: { state: 'running', id: 'r1' } }
    });
    try {
        await selectFirstRow(window);
        assert.deepEqual(footHints(window), ['j/k task', '[/] attempt', 'tab project', 'f follow', 'a activity']);
    } finally {
        window.close();
    }
});

test('needs-you task: only the actions the attention item actually offers show up', async () => {
    const window = await bootConsole({
        stack: {
            running: [], queued: [], done_today: [],
            needs_you: [{ kind: 'stale-claim', task_id: 'cloche-usjb', title: 'bonsai wrapper', reason: 'failed x3', since: new Date().toISOString(), actions: ['release', 'close'] }]
        },
        attemptsByTask: {
            'cloche-usjb': { title: 'bonsai wrapper', attempts: [{ attempt_num: 1, attempt_id: 'a1', run_id: 'r2', outcome: 'failed' }] }
        },
        runsById: { r2: { state: 'failed', id: 'r2' } }
    });
    try {
        await selectFirstRow(window);
        assert.deepEqual(footHints(window), ['j/k task', '[/] attempt', 'r release', 'x close']);
    } finally {
        window.close();
    }
});

test('needs-you task with no release/close action: hints drop to the base pair', async () => {
    const window = await bootConsole({
        stack: {
            running: [], queued: [], done_today: [],
            needs_you: [{ kind: 'parked', task_id: 'cloche-park', title: 'agent waiting for reply', reason: 'waiting', since: new Date().toISOString(), actions: ['mute'] }]
        },
        attemptsByTask: {
            'cloche-park': { title: 'agent waiting for reply', attempts: [{ attempt_num: 1, attempt_id: 'a1', run_id: 'r3', outcome: '' }] }
        },
        runsById: { r3: { state: 'parked', id: 'r3' } }
    });
    try {
        await selectFirstRow(window);
        assert.deepEqual(footHints(window), ['j/k task', '[/] attempt']);
    } finally {
        window.close();
    }
});

test('foot ticker: packs recent entries newest-first, separated by " · ", failed entries flagged for --bad styling', async () => {
    const window = await bootConsole({
        stack: { needs_you: [], running: [], queued: [], done_today: [] },
        activityEntries: [
            { ts: '2026-09-14T17:08:27Z', text: 'intent-scan failed', failure: true },
            { ts: '2026-09-14T17:07:41Z', text: '7pf5 succeeded', failure: false }
        ]
    });
    try {
        // loadTicker() runs on selectProject(); wait for it to land.
        const ticker = window.document.getElementById('console-ticker');
        const deadline = Date.now() + 2000;
        while (Date.now() < deadline) {
            if (ticker.textContent !== '—') break;
            await delay(20);
        }
        assert.match(ticker.textContent, /intent-scan failed/);
        assert.match(ticker.textContent, / · /);
        assert.match(ticker.textContent, /7pf5 succeeded/);
        const bad = ticker.querySelector('.console-ticker-frag-bad');
        assert.ok(bad, 'the failed entry is wrapped in a --bad fragment span');
        assert.match(bad.textContent, /intent-scan failed/);
    } finally {
        window.close();
    }
});
