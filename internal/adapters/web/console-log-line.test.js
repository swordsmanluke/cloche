// Unit tests for the log pane's per-line renderer (static/console.js
// buildLogLineEl / appendLogLineEl / renderLogLines): the dimmed prefix now
// shows a short HH:MM:SS clock (full ISO kept in a title attribute) instead
// of the raw timestamp, and a one-line date divider is inserted whenever
// the date changes between consecutive lines. Runs the real
// static/console-tabs.js + static/console.js in a jsdom window against a
// stubbed fetch and a fake EventSource — no real network, no Go server.
//
// Run with: npm test (from internal/adapters/web/), or
// node --test internal/adapters/web/console-log-line.test.js
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

// installFakeEventSource stands in for the browser EventSource jsdom
// doesn't ship. Every constructed instance is pushed onto `sink` so the
// test can reach in and drive its onmessage handler directly, the same way
// a real SSE frame would via startDetailLogStream's es.onmessage.
function installFakeEventSource(window, sink) {
    window.EventSource = function (url) {
        this.url = url;
        this.close = function () {};
        this.addEventListener = function () {};
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
        { needs_you: [], queued: [], done: [], running: [{ task_id: 'cloche-abcd', title: 'log line task', run_id: 'r1', attempt: 1, elapsed_seconds: 5 }] },
        { 'cloche-abcd': { title: 'log line task', attempts: [{ attempt_num: 1, attempt_id: 'a1', run_id: 'r1', outcome: '' }] } },
        { r1: { id: 'r1', state: 'running', steps: [{ run_id: 'r1', step_name: 'build', depth: 0, result: '', duration: null }] } }
    );
    window.eval(CONSOLE_TABS_SRC);
    window.eval(CONSOLE_JS_SRC);
    await delay(0);
    return window;
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
    deadline = Date.now() + 2000;
    while (Date.now() < deadline) {
        if (window.document.getElementById('console-log-content')) break;
        await delay(20);
    }
}

async function waitForStream(sources) {
    const deadline = Date.now() + 2000;
    while (Date.now() < deadline) {
        if (sources.length) return sources[sources.length - 1];
        await delay(20);
    }
    throw new Error('no EventSource was ever constructed');
}

function emit(es, line) {
    es.onmessage({ data: JSON.stringify(line) });
}

function logLineEls(window) {
    return Array.from(window.document.getElementById('console-log-content').children);
}

test('log line prefix shows HH:MM:SS with the full ISO timestamp in a title attribute', async () => {
    const sources = [];
    const window = await bootConsole(sources);
    try {
        await selectFirstRow(window);
        const es = await waitForStream(sources);

        emit(es, { timestamp: '2026-09-16T20:04:26Z', type: 'script', step_name: 'build', content: 'building things' });

        const els = logLineEls(window);
        assert.equal(els.length, 1);
        const prefix = els[0].querySelector('.log-line-prefix');
        assert.ok(prefix, 'line must have a prefix span');
        assert.match(prefix.textContent, /\[20:04:26\]/, 'prefix should render a short HH:MM:SS clock');
        assert.doesNotMatch(prefix.textContent, /2026-09-16T20:04:26Z/, 'prefix should not show the raw ISO timestamp');
        assert.equal(prefix.getAttribute('title'), '2026-09-16T20:04:26Z', 'full ISO value must be recoverable from the title attribute');
        assert.match(prefix.textContent, /\(build\)/, 'step name is shown when the log is not scoped to a single step');

        const content = els[0].querySelector('.log-line-content');
        assert.equal(content.textContent, 'building things');
    } finally {
        window.close();
    }
});

test('date divider is inserted when the date changes between consecutive lines, but not otherwise', async () => {
    const sources = [];
    const window = await bootConsole(sources);
    try {
        await selectFirstRow(window);
        const es = await waitForStream(sources);

        emit(es, { timestamp: '2026-09-16T23:59:00Z', type: 'script', step_name: 'build', content: 'end of day one' });
        emit(es, { timestamp: '2026-09-16T23:59:30Z', type: 'script', step_name: 'build', content: 'still day one' });

        let els = logLineEls(window);
        assert.equal(els.length, 2, 'same-date consecutive lines must not get a divider between them');
        assert.ok(els.every((el) => !el.classList.contains('log-date-divider')));

        emit(es, { timestamp: '2026-09-17T00:00:05Z', type: 'script', step_name: 'build', content: 'day two begins' });

        els = logLineEls(window);
        assert.equal(els.length, 4, 'a date-change divider element must be inserted before the new day\'s line');
        assert.ok(!els[0].classList.contains('log-date-divider'));
        assert.ok(!els[1].classList.contains('log-date-divider'));
        assert.ok(els[2].classList.contains('log-date-divider'), 'divider must sit between the last line of the old date and the first of the new date');
        assert.match(els[2].textContent, /2026-09-17/);
        assert.ok(!els[3].classList.contains('log-date-divider'));
        assert.equal(els[3].querySelector('.log-line-content').textContent, 'day two begins');
    } finally {
        window.close();
    }
});
