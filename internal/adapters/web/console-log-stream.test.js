// Unit tests for the log pane's stream-lifecycle fixes (static/console.js
// appendLine / flushPendingAppend / resetLogBuffer / enforceLineCap):
//
//   (A) appendLine appends exactly one element per line — scoped or not —
//       without ever resetting the pane's innerHTML, coalescing bursts into
//       one flush via requestAnimationFrame (here, its setTimeout fallback,
//       since jsdom has no rAF).
//   (B) a server-sent "reset" event (the SSE server's signal that a
//       reconnect's Last-Event-ID fell out of its retained history window
//       and it can't resume gap-free — see handler.go streamBroadcastLog)
//       clears the buffer and DOM before the replay that follows is
//       consumed, the same as a genuinely fresh connection.
//   (C) the client-side line/byte cap trims the oldest buffered lines and
//       surfaces a "N earlier lines trimmed" notice wired to the existing
//       Load-earlier path.
//
// Runs the real static/console-tabs.js + static/console.js in a jsdom
// window against a stubbed fetch and a fake EventSource that (unlike the
// no-op stub in the older log test files) actually dispatches named
// events, so 'reset'/'meta'/'done' listeners can be driven directly.
//
// Run with: npm test (from internal/adapters/web/), or
// node --test internal/adapters/web/console-log-stream.test.js
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

// installFakeEventSource, unlike the no-op stub in console-log-line.test.js
// / console-log-scope.test.js, actually stores addEventListener callbacks
// so a test can dispatch a named event ('meta' / 'reset' / 'done') the same
// way a real EventSource would deliver it.
function installFakeEventSource(window, sink) {
    window.EventSource = function (url) {
        this.url = url;
        this.listeners = {};
        this.closed = false;
        this.close = function () { this.closed = true; };
        this.addEventListener = function (name, fn) {
            (this.listeners[name] = this.listeners[name] || []).push(fn);
        };
        this.dispatch = function (name, data) {
            (this.listeners[name] || []).forEach((fn) => fn({ data: data === undefined ? '' : data }));
        };
        sink.push(this);
    };
}

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

function makeStubFetch(stack, attemptsByTask, runsById) {
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
        return jsonResponse({});
    };
}

async function bootConsole(sources) {
    const dom = new JSDOM(
        '<!DOCTYPE html><html><body>' + consoleContentMarkup('myproj') + '</body></html>',
        { url: 'http://localhost/myproj', runScripts: 'outside-only' }
    );
    const { window } = dom;
    installFakeEventSource(window, sources);
    installTestShims(window);
    window.fetch = makeStubFetch(
        { needs_you: [], queued: [], done: [], running: [{ task_id: 'cloche-abcd', title: 'stream task', run_id: 'r1', attempt: 1, elapsed_seconds: 5 }] },
        { 'cloche-abcd': { title: 'stream task', attempts: [{ attempt_num: 1, attempt_id: 'a1', run_id: 'r1', outcome: '' }] } },
        { r1: { id: 'r1', state: 'running', steps: [{ run_id: 'r1', step_name: 'build', depth: 0, result: '', duration: null }] } }
    );
    window.eval(CONSOLE_TABS_SRC);
    window.eval(CONSOLE_JS_SRC);
    await delay(0);
    return window;
}

async function waitFor(predicate, message) {
    const deadline = Date.now() + 2000;
    while (Date.now() < deadline) {
        const v = predicate();
        if (v) return v;
        await delay(20);
    }
    throw new Error(message || 'condition never became true');
}

async function selectFirstRow(window) {
    const row = await waitFor(() => window.document.querySelector('.console-stack-row'), 'no stack row rendered');
    row.dispatchEvent(new window.Event('click', { bubbles: true }));
    await waitFor(() => window.document.getElementById('console-log-content'), 'log pane never rendered');
}

async function waitForStream(sources) {
    return waitFor(() => sources.length && sources[sources.length - 1], 'no EventSource was ever constructed');
}

function emit(es, line) {
    es.onmessage({ data: JSON.stringify(line) });
}

function logContentEl(window) {
    return window.document.getElementById('console-log-content');
}

function logLineEls(window) {
    return Array.from(logContentEl(window).children);
}

test('appendLine appends one element per line without resetting innerHTML', async () => {
    const sources = [];
    const window = await bootConsole(sources);
    try {
        await selectFirstRow(window);
        const es = await waitForStream(sources);

        emit(es, { type: 'script', step_name: 'build', content: 'line one' });
        await delay(30);
        const firstEl = logLineEls(window)[0];
        assert.ok(firstEl, 'first line should have rendered');

        emit(es, { type: 'script', step_name: 'build', content: 'line two' });
        emit(es, { type: 'script', step_name: 'build', content: 'line three' });
        await delay(30);

        const els = logLineEls(window);
        assert.equal(els.length, 3);
        // If appendLine ever fell back to pre.innerHTML = '' + rebuild, the
        // very first element would have been destroyed and replaced with an
        // equal-but-different node — assert the same node survived.
        assert.equal(els[0], firstEl, 'earlier lines must not be torn down and rebuilt on a later append');
        assert.equal(els[0].isConnected, true);
    } finally {
        window.close();
    }
});

test('bursts of lines within one tick are coalesced into a single flush', async () => {
    const sources = [];
    const window = await bootConsole(sources);
    try {
        await selectFirstRow(window);
        const es = await waitForStream(sources);

        for (let i = 0; i < 20; i++) {
            emit(es, { type: 'script', step_name: 'build', content: 'burst line ' + i });
        }
        // Nothing should be in the DOM synchronously — the whole burst is
        // queued for one coalesced flush (see scheduleLogFlush).
        assert.equal(logLineEls(window).length, 0, 'a burst must not render synchronously, one line at a time');

        await delay(30);
        assert.equal(logLineEls(window).length, 20, 'the coalesced flush should render the whole burst at once');
    } finally {
        window.close();
    }
});

test('a server "reset" event clears the buffer and DOM before the following replay is consumed', async () => {
    const sources = [];
    const window = await bootConsole(sources);
    try {
        await selectFirstRow(window);
        const es = await waitForStream(sources);

        emit(es, { type: 'script', step_name: 'build', content: 'before reset' });
        await delay(30);
        assert.equal(logLineEls(window).length, 1);

        es.dispatch('reset', '{}');
        await delay(30);
        assert.equal(logLineEls(window).length, 0, 'reset must clear previously rendered lines');
        assert.equal(logContentEl(window).textContent, '', 'reset must clear the pane, not just stop appending');

        emit(es, { type: 'script', step_name: 'build', content: 'fresh window line' });
        await delay(30);
        const els = logLineEls(window);
        assert.equal(els.length, 1, 'lines after reset should render normally');
        assert.equal(els[0].querySelector('.log-line-content').textContent, 'fresh window line');
    } finally {
        window.close();
    }
});

test('under the default cap, ordinary streaming never trims or shows the notice', async () => {
    const sources = [];
    const window = await bootConsole(sources);
    try {
        await selectFirstRow(window);
        const es = await waitForStream(sources);

        for (let i = 0; i < 50; i++) {
            emit(es, { type: 'script', step_name: 'build', content: 'line ' + i });
        }
        await delay(30);

        const btn = window.document.getElementById('console-log-earlier');
        assert.equal(btn.hidden, true, 'no trimming should have happened this far under the default 5,000-line cap');
        assert.equal(logLineEls(window).length, 50);
    } finally {
        window.close();
    }
});

// DEFAULT_LOG_LINE_CAP / LOG_CAP_SLACK mirror the constants in
// static/console.js (enforceLineCap) — the module keeps them private, so
// this duplicates their values rather than exercising real production
// scale (5,000+ lines) on every test run.
const DEFAULT_LOG_LINE_CAP = 5000;
const LOG_CAP_SLACK = 200;

test('exceeding the line cap trims the oldest buffered lines and shows a "trimmed" notice wired to Load earlier', async () => {
    const sources = [];
    const window = await bootConsole(sources);
    try {
        await selectFirstRow(window);
        const es = await waitForStream(sources);

        const total = DEFAULT_LOG_LINE_CAP + LOG_CAP_SLACK + 10;
        for (let i = 0; i < total; i++) {
            emit(es, { type: 'script', step_name: 'build', content: 'line ' + i });
        }
        await delay(200);

        const btn = window.document.getElementById('console-log-earlier');
        assert.equal(btn.hidden, false, 'exceeding the cap must reveal the Load-earlier control as a trim notice');
        assert.match(btn.textContent, /earlier lines? trimmed/, 'the notice must say lines were trimmed, not just "Load earlier"');
        assert.match(btn.textContent, /Load earlier/);

        // The DOM itself must also be bounded — an incremental append never
        // calls the full renderLogLines() rebuild that would otherwise
        // naturally shed evicted lines (see flushPendingAppend's own trim).
        const domCount = logContentEl(window).children.length;
        assert.ok(domCount <= DEFAULT_LOG_LINE_CAP + LOG_CAP_SLACK, 'rendered DOM line count must stay bounded, not grow with every line ever streamed');
    } finally {
        window.close();
    }
});
