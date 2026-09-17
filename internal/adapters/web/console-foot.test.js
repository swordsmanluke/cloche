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
    if (opts.beforeScripts) opts.beforeScripts(window);
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

async function waitFor(predicate) {
    const deadline = Date.now() + 2000;
    while (Date.now() < deadline) {
        if (predicate()) return true;
        await delay(20);
    }
    return predicate();
}

function dispatchKey(window, key) {
    window.document.dispatchEvent(new window.KeyboardEvent('keydown', { key: key, bubbles: true, cancelable: true }));
}

function densityAttr(window) {
    return window.document.getElementById('console-app').getAttribute('data-density');
}

function selectedStepName(window) {
    const seg = window.document.querySelector('.console-step-strip .console-step-segment-selected .console-step-segment-name');
    return seg ? seg.textContent.trim() : null;
}

function currentAttemptChipText(window) {
    const chip = window.document.querySelector('.console-attempt-chip-on');
    return chip ? chip.textContent.trim() : null;
}

test('[ and ] scope the log to the previous/next step in strip order, crossing into child-run steps and wrapping at the ends', async () => {
    const r1Steps = [
        { run_id: 'r1', step_name: 'alpha', depth: 0, result: 'success', duration: '1s' },
        { run_id: 'child-r1', step_name: 'inner-a', depth: 1, result: 'success', duration: '1s' },
        { run_id: 'child-r1', step_name: 'inner-b', depth: 1, result: 'success', duration: '1s' },
        { run_id: 'r1', step_name: 'beta', depth: 0, result: 'success', duration: '1s' }
    ];
    const window = await bootConsole({
        stack: {
            needs_you: [], queued: [], done: [],
            running: [{ task_id: 'cloche-abcd', title: 'multi-step task', run_id: 'r1', attempt: 1, elapsed_seconds: 10 }]
        },
        attemptsByTask: {
            'cloche-abcd': {
                title: 'multi-step task',
                attempts: [
                    { attempt_num: 1, attempt_id: 'a1', run_id: 'r1', outcome: '' },
                    { attempt_num: 2, attempt_id: 'a2', run_id: 'r2', outcome: '' }
                ]
            }
        },
        runsById: {
            r1: { id: 'r1', state: 'succeeded', steps: r1Steps },
            r2: { id: 'r2', state: 'succeeded', steps: [{ run_id: 'r2', step_name: 'gamma', depth: 0, result: 'success', duration: '1s' }] }
        }
    });
    try {
        await selectFirstRow(window);
        assert.ok(await waitFor(() => window.document.querySelectorAll('.console-step-strip .console-step-segment').length === 4));

        // Nothing scoped yet: ']' starts at the first step.
        dispatchKey(window, ']');
        assert.ok(await waitFor(() => (selectedStepName(window) || '').indexOf('alpha') !== -1));

        // Crosses into the child-run steps in strip order.
        dispatchKey(window, ']');
        assert.equal(selectedStepName(window), 'inner-a');
        dispatchKey(window, ']');
        assert.equal(selectedStepName(window), 'inner-b');
        dispatchKey(window, ']');
        assert.ok((selectedStepName(window) || '').indexOf('beta') !== -1);

        // Wraps back to the first step past the end.
        dispatchKey(window, ']');
        assert.ok((selectedStepName(window) || '').indexOf('alpha') !== -1);

        // Wraps to the last step going backward past the start.
        dispatchKey(window, '[');
        assert.ok((selectedStepName(window) || '').indexOf('beta') !== -1);

        // Shift+] switches attempts instead (a less prominent binding),
        // resetting the step strip to the new attempt's run.
        dispatchKey(window, '}');
        assert.ok(await waitFor(() => currentAttemptChipText(window) === '2 r2'));
        assert.ok(await waitFor(() => window.document.querySelectorAll('.console-step-strip .console-step-segment').length === 1));
        assert.equal(selectedStepName(window), null);

        dispatchKey(window, ']');
        assert.ok((selectedStepName(window) || '').indexOf('gamma') !== -1);

        // Shift+[ switches back to the previous attempt.
        dispatchKey(window, '{');
        assert.ok(await waitFor(() => currentAttemptChipText(window) === '1 r1'));
    } finally {
        window.close();
    }
});

test('no task selected: short baseline hints, no task-specific keys', async () => {
    const window = await bootConsole({ stack: { needs_you: [], running: [], queued: [], done: [] } });
    try {
        assert.deepEqual(footHints(window), ['j/k task', 'enter open', 'tab project', 'a activity']);
    } finally {
        window.close();
    }
});

test('running task: j/k, attempt, project, follow, activity — no g/G or filter', async () => {
    const window = await bootConsole({
        stack: {
            needs_you: [], queued: [], done: [],
            running: [{ task_id: 'cloche-fnn6', title: 'hidden acceptance corpus', run_id: 'r1', attempt: 1, elapsed_seconds: 10 }]
        },
        attemptsByTask: {
            'cloche-fnn6': { title: 'hidden acceptance corpus', attempts: [{ attempt_num: 1, attempt_id: 'a1', run_id: 'r1', outcome: '' }] }
        },
        runsById: { r1: { state: 'running', id: 'r1' } }
    });
    try {
        await selectFirstRow(window);
        assert.deepEqual(footHints(window), ['j/k task', '[/] step', '⇧[/⇧] attempt', 'tab project', 'f follow', 'a activity']);
    } finally {
        window.close();
    }
});

test('needs-you task: only the actions the attention item actually offers show up', async () => {
    const window = await bootConsole({
        stack: {
            running: [], queued: [], done: [],
            needs_you: [{ kind: 'stale-claim', task_id: 'cloche-usjb', title: 'bonsai wrapper', reason: 'failed x3', since: new Date().toISOString(), actions: ['release', 'close'] }]
        },
        attemptsByTask: {
            'cloche-usjb': { title: 'bonsai wrapper', attempts: [{ attempt_num: 1, attempt_id: 'a1', run_id: 'r2', outcome: 'failed' }] }
        },
        runsById: { r2: { state: 'failed', id: 'r2' } }
    });
    try {
        await selectFirstRow(window);
        assert.deepEqual(footHints(window), ['j/k task', '[/] step', '⇧[/⇧] attempt', 'r release', 'x close']);
    } finally {
        window.close();
    }
});

test('needs-you task with no release/close action: hints drop to the base pair', async () => {
    const window = await bootConsole({
        stack: {
            running: [], queued: [], done: [],
            needs_you: [{ kind: 'parked', task_id: 'cloche-park', title: 'agent waiting for reply', reason: 'waiting', since: new Date().toISOString(), actions: ['mute'] }]
        },
        attemptsByTask: {
            'cloche-park': { title: 'agent waiting for reply', attempts: [{ attempt_num: 1, attempt_id: 'a1', run_id: 'r3', outcome: '' }] }
        },
        runsById: { r3: { state: 'parked', id: 'r3' } }
    });
    try {
        await selectFirstRow(window);
        assert.deepEqual(footHints(window), ['j/k task', '[/] step', '⇧[/⇧] attempt']);
    } finally {
        window.close();
    }
});

test('footer shows the daemon version to the right of the key hints', async () => {
    const window = await bootConsole({ stack: { needs_you: [], running: [], queued: [], done: [] } });
    try {
        const footbar = window.document.getElementById('console-footbar');
        const keys = window.document.getElementById('console-keys');
        const version = window.document.getElementById('console-daemon-version');
        assert.ok(version, 'footer must contain a daemon version element');
        assert.equal(version.textContent, 'test', 'version text comes from data-cloche-version');

        const children = Array.from(footbar.children);
        assert.ok(children.indexOf(keys) < children.indexOf(version),
            'the version must sit to the right of (after, in DOM order) the key hints');
    } finally {
        window.close();
    }
});

test('Workflows/Requirements/Containers/Ledger are folded behind a single Tools menu button', async () => {
    const window = await bootConsole({ stack: { needs_you: [], running: [], queued: [], done: [] } });
    try {
        const toolsBtn = window.document.getElementById('console-tools-btn');
        const menu = window.document.getElementById('console-tools-menu');
        assert.ok(toolsBtn, 'a single Tools button must exist in the tab bar');
        assert.equal(menu.hidden, true, 'the menu starts closed');

        ['console-view-workflows-btn', 'console-view-intent-btn', 'console-view-containers-btn', 'console-ledger-btn']
            .forEach((id) => {
                const btn = window.document.getElementById(id);
                assert.ok(btn, id + ' must still exist (inside the Tools menu)');
                assert.ok(menu.contains(btn), id + ' must be nested inside #console-tools-menu');
            });

        toolsBtn.dispatchEvent(new window.Event('click', { bubbles: true }));
        assert.equal(menu.hidden, false, 'clicking the Tools button opens the menu');
        assert.equal(toolsBtn.getAttribute('aria-expanded'), 'true');

        // Clicking a menu item closes the menu (same pattern as the
        // idle-projects menu closing after a tab is picked).
        window.document.getElementById('console-view-workflows-btn')
            .dispatchEvent(new window.Event('click', { bubbles: true }));
        assert.equal(menu.hidden, true, 'picking a menu item closes the menu');
        assert.equal(window.document.getElementById('console-view-overlay').hidden, false, 'and opens the Workflows view');

        // Let openWorkflowsView's loadWorkflows() fetch/.then chain drain
        // before the window is torn down (see bootConsole's own comment on
        // the same pattern above).
        await delay(50);
    } finally {
        window.close();
    }
});

test('w/i/c/l keyboard shortcuts keep opening their views directly, independent of the Tools menu', async () => {
    const window = await bootConsole({ stack: { needs_you: [], running: [], queued: [], done: [] } });
    try {
        const viewOverlay = window.document.getElementById('console-view-overlay');
        const viewTitle = window.document.getElementById('console-view-title');
        const ledgerOverlay = window.document.getElementById('console-ledger-overlay');

        dispatchKey(window, 'w');
        assert.equal(viewOverlay.hidden, false);
        assert.equal(viewTitle.textContent, 'Workflows');
        // Let the view's own fetch/.then chain (loadWorkflows) land before
        // closing it — closing tears down the elements it writes into.
        await delay(50);
        window.document.getElementById('console-view-close').dispatchEvent(new window.Event('click', { bubbles: true }));

        dispatchKey(window, 'i');
        assert.equal(viewOverlay.hidden, false);
        assert.equal(viewTitle.textContent, 'Requirements');
        await delay(50);
        window.document.getElementById('console-view-close').dispatchEvent(new window.Event('click', { bubbles: true }));

        dispatchKey(window, 'c');
        assert.equal(viewOverlay.hidden, false);
        assert.equal(viewTitle.textContent, 'Containers');
        await delay(50);
        window.document.getElementById('console-view-close').dispatchEvent(new window.Event('click', { bubbles: true }));

        dispatchKey(window, 'l');
        assert.equal(ledgerOverlay.hidden, false);
        await delay(50);
    } finally {
        window.close();
    }
});

test('foot ticker: packs recent entries newest-first, separated by " · ", failed entries flagged for --bad styling', async () => {
    const window = await bootConsole({
        stack: { needs_you: [], running: [], queued: [], done: [] },
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

test('density toggle: defaults to comfortable, and the footer button / "d" key flip and persist data-density', async () => {
    const window = await bootConsole({ stack: { needs_you: [], running: [], queued: [], done: [] } });
    try {
        assert.equal(densityAttr(window), 'comfortable');
        const btn = window.document.getElementById('console-density-toggle');
        assert.ok(btn, 'footer exposes a density toggle button next to the key hints');
        assert.equal(btn.textContent, 'Comfortable');

        btn.dispatchEvent(new window.Event('click', { bubbles: true }));
        assert.equal(densityAttr(window), 'compact');
        assert.equal(btn.textContent, 'Compact');
        assert.equal(window.localStorage.getItem('cloche:density'), 'compact');

        dispatchKey(window, 'd');
        assert.equal(densityAttr(window), 'comfortable');
        assert.equal(btn.textContent, 'Comfortable');
        assert.equal(window.localStorage.getItem('cloche:density'), 'comfortable');
    } finally {
        window.close();
    }
});

test('density toggle: a stored "compact" choice is restored on boot', async () => {
    const window = await bootConsole({
        stack: { needs_you: [], running: [], queued: [], done: [] },
        beforeScripts: (win) => { win.localStorage.setItem('cloche:density', 'compact'); }
    });
    try {
        assert.equal(densityAttr(window), 'compact');
        assert.equal(window.document.getElementById('console-density-toggle').textContent, 'Compact');
    } finally {
        window.close();
    }
});

test('density toggle: falls back to comfortable, and keeps working, when localStorage is unavailable', async () => {
    const window = await bootConsole({
        stack: { needs_you: [], running: [], queued: [], done: [] },
        beforeScripts: (win) => {
            // Some environments (private browsing, storage disabled) throw
            // just touching window.localStorage, not only on get/setItem.
            Object.defineProperty(win, 'localStorage', {
                get() { throw new Error('storage disabled'); }
            });
        }
    });
    try {
        assert.equal(densityAttr(window), 'comfortable');
        const btn = window.document.getElementById('console-density-toggle');
        assert.doesNotThrow(() => btn.dispatchEvent(new window.Event('click', { bubbles: true })));
        assert.equal(densityAttr(window), 'compact');
    } finally {
        window.close();
    }
});
