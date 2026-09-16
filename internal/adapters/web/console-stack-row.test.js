// Unit tests for the task-stack row layout in static/console.js: the
// status-dot / id+title / elapsed three-column grid described in
// docs/design/console-restructured-mock.html (.m5 .row). Runs the real
// static/console-tabs.js + static/console.js in a jsdom window against a
// stubbed fetch — no real network, no Go server.
//
// Run with: npm test (from internal/adapters/web/), or
// node --test internal/adapters/web/console-stack-row.test.js
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

function makeStubFetch(stack) {
    const fetchFn = function (url) {
        if (url === '/api/projects') return jsonResponse([]);
        if (/\/tasks\/stack(\?|$)/.test(url)) return jsonResponse(stack, { ETag: 'W/"stack-1"' });
        return jsonResponse({});
    };
    return fetchFn;
}

async function bootConsole(stack) {
    const dom = new JSDOM(
        '<!DOCTYPE html><html><body>' + consoleContentMarkup('myproj') + '</body></html>',
        { url: 'http://localhost/myproj', runScripts: 'outside-only' }
    );
    const { window } = dom;
    window.fetch = makeStubFetch(stack);
    window.eval(CONSOLE_TABS_SRC);
    window.eval(CONSOLE_JS_SRC);

    const deadline = Date.now() + 2000;
    while (Date.now() < deadline) {
        if (window.document.querySelector('.console-stack-row')) break;
        await delay(20);
    }
    return window;
}

test('needs-you row: amber dot, id line, title, and elapsed column carry the reason with warn styling', async () => {
    const window = await bootConsole({
        needs_you: [{ kind: 'compare', task_id: 'cloche-usjb', title: 'bonsai executor wrapper', reason: 'failed ×3', since: new Date().toISOString() }],
        running: [], queued: [], done: []
    });
    try {
        const row = window.document.querySelector('.console-stack-row');
        const dot = row.querySelector('.console-stack-row-dot');
        assert.ok(dot.classList.contains('console-stack-row-dot-y'), 'needs-you rows use the amber (y) dot');
        assert.equal(row.querySelector('.console-stack-row-id').textContent, 'cloche-usjb');
        assert.equal(row.querySelector('.console-stack-row-title').textContent, 'bonsai executor wrapper');
        const elapsed = row.querySelector('.console-stack-row-elapsed');
        assert.equal(elapsed.textContent, 'failed ×3');
        assert.ok(elapsed.classList.contains('console-stack-row-elapsed-warn'), 'needs-you elapsed column is amber');

        const header = window.document.querySelector('.console-stack-group-title');
        assert.ok(header.classList.contains('console-stack-group-title-warn'), 'Needs you header is amber');
        assert.equal(header.querySelector('.console-stack-group-count').textContent, '1', 'count is a bare number, not "(1)"');
    } finally {
        window.close();
    }
});

test('running row: pulsing blue dot and id line includes the attempt suffix when attempt > 1', async () => {
    const window = await bootConsole({
        needs_you: [],
        running: [{ task_id: 'cloche-fnn6', title: 'hidden acceptance corpus', run_id: 'r1', attempt: 3, current_step: 'implement', elapsed_seconds: 124 }],
        queued: [], done: []
    });
    try {
        const row = window.document.querySelector('.console-stack-row');
        const dot = row.querySelector('.console-stack-row-dot');
        assert.ok(dot.classList.contains('console-stack-row-dot-b'), 'running rows use the running-blue (b) dot');
        assert.ok(dot.classList.contains('console-stack-row-dot-pulse'), 'running rows pulse');
        assert.equal(row.querySelector('.console-stack-row-id').textContent, 'cloche-fnn6 · a3');
    } finally {
        window.close();
    }
});

test('running row: id line has no attempt suffix for a first attempt', async () => {
    const window = await bootConsole({
        needs_you: [],
        running: [{ task_id: 'cloche-ccgl', title: 'twelve-task list', run_id: 'r2', attempt: 1, elapsed_seconds: 85 }],
        queued: [], done: []
    });
    try {
        const row = window.document.querySelector('.console-stack-row');
        assert.equal(row.querySelector('.console-stack-row-id').textContent, 'cloche-ccgl');
    } finally {
        window.close();
    }
});

test('queued row: idle grey dot', async () => {
    const window = await bootConsole({
        needs_you: [], running: [],
        queued: [{ task_id: 'user-hecd', title: 'main', reason: 'waiting for a slot', since: new Date().toISOString() }],
        done: []
    });
    try {
        const dot = window.document.querySelector('.console-stack-row-dot');
        assert.ok(dot.classList.contains('console-stack-row-dot-x'), 'queued rows use the idle (x) dot');
    } finally {
        window.close();
    }
});

test('done rows: green dot for a succeeded outcome, red for failed', async () => {
    const window = await bootConsole({
        needs_you: [], running: [], queued: [],
        done: [
            { task_id: 'cloche-7pf5', title: 'recover missing result marker', run_id: 'r3', outcome: 'succeeded', duration_seconds: 480 },
            { task_id: 'cloche-ulid', title: 'first attempt, retrying now', run_id: 'r4', outcome: 'failed', duration_seconds: 1740 }
        ]
    });
    try {
        const rows = window.document.querySelectorAll('.console-stack-row');
        assert.equal(rows.length, 2);
        assert.ok(rows[0].querySelector('.console-stack-row-dot').classList.contains('console-stack-row-dot-g'), 'succeeded outcome uses the ok (g) dot');
        assert.ok(rows[1].querySelector('.console-stack-row-dot').classList.contains('console-stack-row-dot-r'), 'failed outcome uses the failed (r) dot');
    } finally {
        window.close();
    }
});

// installStackPollShim intercepts window.setInterval and hands back the
// registered callbacks keyed by delay, so a test can fire the 4s stack poll
// on demand instead of waiting on a real timer.
function installStackPollShim(window) {
    const timers = {};
    const origSetInterval = window.setInterval.bind(window);
    window.setInterval = function (fn, ms) {
        timers[ms] = fn;
        return origSetInterval(fn, ms);
    };
    return timers;
}

function groupSection(window, label) {
    const headers = window.document.querySelectorAll('.console-stack-group-title');
    const header = Array.from(headers).find((h) => h.textContent.indexOf(label) === 0);
    return header && header.closest('.console-stack-group');
}

test('empty Needs you / Running / Queued groups are omitted; Done always stays, dash and all', async () => {
    let stack = {
        needs_you: [],
        running: [{ task_id: 'cloche-fnn6', title: 'hidden acceptance corpus', run_id: 'r1', attempt: 1, elapsed_seconds: 10 }],
        queued: [],
        done: []
    };
    const dom = new JSDOM(
        '<!DOCTYPE html><html><body>' + consoleContentMarkup('myproj') + '</body></html>',
        { url: 'http://localhost/myproj', runScripts: 'outside-only' }
    );
    const { window } = dom;
    const timers = installStackPollShim(window);
    window.fetch = function (url) {
        if (url === '/api/projects') return jsonResponse([]);
        if (/\/tasks\/stack(\?|$)/.test(url)) return jsonResponse(stack, { ETag: 'W/"stack-1"' });
        return jsonResponse({});
    };
    window.eval(CONSOLE_TABS_SRC);
    window.eval(CONSOLE_JS_SRC);

    const deadline = Date.now() + 2000;
    while (Date.now() < deadline) {
        if (window.document.querySelector('.console-stack-row')) break;
        await delay(20);
    }

    try {
        assert.equal(groupSection(window, 'Needs you').hidden, true, 'empty Needs you group is omitted');
        assert.equal(groupSection(window, 'Queued').hidden, true, 'empty Queued group is omitted');
        assert.equal(groupSection(window, 'Running').hidden, false, 'non-empty Running group stays visible');

        var doneSection = groupSection(window, 'Done');
        assert.equal(doneSection.hidden, false, 'Done always stays visible, even when empty');
        assert.ok(doneSection.querySelector('.console-stack-empty'), 'empty Done still shows its dash placeholder');
        assert.equal(groupSection(window, 'Needs you').querySelector('.console-stack-empty'), null, 'omitted groups do not render a dash placeholder');

        // Simulate the next 4s poll finding a Needs-you row and losing its Running one.
        stack = {
            needs_you: [{ task_id: 'cloche-usjb', title: 'bonsai executor wrapper', reason: 'failed ×3' }],
            running: [],
            queued: [],
            done: []
        };
        assert.ok(timers[4000], 'stack poll interval was registered');
        timers[4000]();

        const reappearDeadline = Date.now() + 2000;
        while (Date.now() < reappearDeadline) {
            if (groupSection(window, 'Needs you').hidden === false) break;
            await delay(20);
        }

        assert.equal(groupSection(window, 'Needs you').hidden, false, 'Needs you reappears as soon as it has rows');
        assert.equal(groupSection(window, 'Running').hidden, true, 'Running is omitted again once it empties out');
    } finally {
        window.close();
    }
});

// Covers the Done group's poll-merge contract: the 4s poll only ever
// re-fetches page one (see loadStack), so a freshly-completed task should
// show up by prepending to that page — without knocking out whatever
// "earlier" pages the user has already paged into via cursor (state.extraDone,
// which loadStack only ever clears on a genuine project switch/initial load).
test('poll-merge: a refreshed first page prepends new completions without discarding an already-loaded earlier page', async () => {
    const page1 = {
        needs_you: [], running: [], queued: [],
        done: [{ task_id: 'task-a', title: 'task a', run_id: 'run-a', outcome: 'succeeded', duration_seconds: 10 }],
        cursor: 'CURSOR-1'
    };
    const page2 = {
        needs_you: [], running: [], queued: [],
        done: [{ task_id: 'task-b', title: 'task b', run_id: 'run-b', outcome: 'succeeded', duration_seconds: 20 }],
        cursor: ''
    };
    const polledFirstPage = {
        needs_you: [], running: [], queued: [],
        done: [
            { task_id: 'task-c', title: 'task c (new)', run_id: 'run-c', outcome: 'succeeded', duration_seconds: 5 },
            { task_id: 'task-a', title: 'task a', run_id: 'run-a', outcome: 'succeeded', duration_seconds: 10 }
        ],
        cursor: 'CURSOR-1'
    };

    const dom = new JSDOM(
        '<!DOCTYPE html><html><body>' + consoleContentMarkup('myproj') + '</body></html>',
        { url: 'http://localhost/myproj', runScripts: 'outside-only' }
    );
    const { window } = dom;
    const timers = installStackPollShim(window);
    let firstPageCalls = 0;
    window.fetch = function (url) {
        if (url === '/api/projects') return jsonResponse([]);
        if (/\/tasks\/stack\?cursor=/.test(url)) return jsonResponse(page2);
        if (/\/tasks\/stack(\?|$)/.test(url)) {
            firstPageCalls++;
            const body = firstPageCalls === 1 ? page1 : polledFirstPage;
            return jsonResponse(body, { ETag: 'W/"stack-' + firstPageCalls + '"' });
        }
        return jsonResponse({});
    };
    window.eval(CONSOLE_TABS_SRC);
    window.eval(CONSOLE_JS_SRC);

    const deadline = Date.now() + 2000;
    while (Date.now() < deadline) {
        if (window.document.querySelector('.console-stack-row')) break;
        await delay(20);
    }

    function doneRowKeys() {
        return Array.from(window.document.querySelectorAll('#console-stack-list-done .console-stack-row'))
            .map(function (r) { return r.dataset.key; });
    }

    try {
        window.document.getElementById('console-load-earlier').click();
        const loadDeadline = Date.now() + 2000;
        while (Date.now() < loadDeadline) {
            if (doneRowKeys().length >= 2) break;
            await delay(20);
        }
        assert.deepEqual(doneRowKeys(), ['done:task-a', 'done:task-b'], 'first page plus the loaded earlier page are both shown');

        assert.ok(timers[4000], 'stack poll interval was registered');
        timers[4000]();

        const pollDeadline = Date.now() + 2000;
        while (Date.now() < pollDeadline) {
            if (doneRowKeys().length >= 3) break;
            await delay(20);
        }
        assert.deepEqual(
            doneRowKeys(),
            ['done:task-c', 'done:task-a', 'done:task-b'],
            'the new completion prepends and the already-loaded earlier page survives the poll'
        );
    } finally {
        window.close();
    }
});
