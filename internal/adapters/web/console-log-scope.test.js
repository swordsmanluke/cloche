// Unit tests for scoping the log pane to a step (static/console.js
// scopeToStep / buildScopeFilter / scopedLines / appendLine): scoping is a
// filter over the live stream (detail.allLines), not a one-shot fetch that
// replaces it, and it respects the workflow hierarchy — scoping to a child
// step shows only that step's own lines, while scoping to a parent
// workflow step shows the parent's own lines plus every line from its
// child run's steps, in stream order. Runs the real static/console-tabs.js
// + static/console.js in a jsdom window against a stubbed fetch and a fake
// EventSource — no real network, no Go server.
//
// Run with: npm test (from internal/adapters/web/), or
// node --test internal/adapters/web/console-log-scope.test.js
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

function notFoundResponse() {
    return Promise.resolve({ ok: false, status: 404, text: () => Promise.resolve('') });
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

// The host run "r1" has one step, the workflow step "develop", which
// spawned child run "r2" (steps "implement" and "verify" inlined at
// depth 1) — the same shape internal/adapters/web/handler.go's flattenRun
// produces for a develop run whose child run inlines implement/verify.
const HOST_RUN_STEPS = [
    { run_id: 'r1', step_name: 'develop', depth: 0, parent_index: -1, index: 0, is_workflow: true, child_run_id: 'r2', result: '', started_at: '2026-09-17T10:00:00Z' },
    { run_id: 'r2', step_name: 'implement', depth: 1, parent_index: 0, index: 1, is_workflow: false, result: '', started_at: '2026-09-17T10:00:01Z' },
    { run_id: 'r2', step_name: 'verify', depth: 1, parent_index: 0, index: 2, is_workflow: false, result: '', started_at: '' }
];

function makeStubFetch(stack, attemptsByTask, runsById) {
    return function (url) {
        if (url === '/api/projects') return jsonResponse([]);
        if (/\/tasks\/stack(\?|$)/.test(url)) return jsonResponse(stack, { ETag: 'W/"stack-1"' });
        var attemptsMatch = url.match(/\/tasks\/([^/]+)\/attempts$/);
        if (attemptsMatch) {
            var taskId = decodeURIComponent(attemptsMatch[1]);
            return jsonResponse(attemptsByTask[taskId] || { attempts: [] });
        }
        if (/\/steps\/[^/]+\/output(\?|$)/.test(url)) return notFoundResponse();
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
        { needs_you: [], queued: [], done: [], running: [{ task_id: 'cloche-abcd', title: 'scope task', run_id: 'r1', attempt: 1, elapsed_seconds: 5 }] },
        { 'cloche-abcd': { title: 'scope task', attempts: [{ attempt_num: 1, attempt_id: 'a1', run_id: 'r1', outcome: '' }] } },
        { r1: { id: 'r1', state: 'running', steps: HOST_RUN_STEPS } }
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

function logLineEls(window) {
    return Array.from(window.document.getElementById('console-log-content').children);
}

function contentTexts(window) {
    return logLineEls(window)
        .map((el) => el.querySelector('.log-line-content'))
        .filter(Boolean)
        .map((el) => el.textContent);
}

// scopeToStep is only reachable through a step-strip click in the running
// app; find the segment for (step_name) and click it. clusterSteps groups
// depth-0 "develop" first, then its depth>0 children (implement, verify)
// in order — see static/console.js clusterSteps / renderStepStrip.
async function clickStep(window, stepName) {
    await waitFor(() => window.document.querySelectorAll('.console-step-segment').length >= HOST_RUN_STEPS.length, 'step strip never rendered');
    const segments = Array.from(window.document.querySelectorAll('.console-step-segment'));
    const idx = HOST_RUN_STEPS.findIndex((s) => s.step_name === stepName);
    segments[idx].dispatchEvent(new window.Event('click', { bubbles: true }));
}

test('scoping to a child step shows only that step\'s own lines', async () => {
    const sources = [];
    const window = await bootConsole(sources);
    try {
        await selectFirstRow(window);
        const es = await waitForStream(sources);

        emit(es, { run_id: 'r1', step_name: 'develop', type: 'script', content: 'develop status', timestamp: '2026-09-17T10:00:00Z' });
        emit(es, { run_id: 'r2', step_name: 'implement', type: 'llm', content: 'implement output', timestamp: '2026-09-17T10:00:01Z' });
        emit(es, { run_id: 'r2', step_name: 'verify', type: 'llm', content: 'verify output', timestamp: '2026-09-17T10:00:02Z' });

        await clickStep(window, 'implement');
        await waitFor(() => contentTexts(window).length > 0, 'scoped view never rendered any line');

        assert.deepEqual(contentTexts(window), ['implement output'], 'scoping to a leaf step must exclude sibling and parent lines');
    } finally {
        window.close();
    }
});

test('scoping to a parent workflow step shows its own lines plus every line from its child run\'s steps, in stream order', async () => {
    const sources = [];
    const window = await bootConsole(sources);
    try {
        await selectFirstRow(window);
        const es = await waitForStream(sources);

        emit(es, { run_id: 'r1', step_name: 'develop', type: 'script', content: 'develop status A', timestamp: '2026-09-17T10:00:00Z' });
        emit(es, { run_id: 'r2', step_name: 'implement', type: 'llm', content: 'implement output', timestamp: '2026-09-17T10:00:01Z' });
        emit(es, { run_id: 'r2', step_name: 'verify', type: 'llm', content: 'verify output', timestamp: '2026-09-17T10:00:02Z' });
        emit(es, { run_id: 'r1', step_name: 'develop', type: 'script', content: 'develop status B', timestamp: '2026-09-17T10:00:03Z' });

        await clickStep(window, 'develop');
        await waitFor(() => contentTexts(window).length >= 4, 'scoped parent view never rendered the merged lines');

        assert.deepEqual(
            contentTexts(window),
            ['develop status A', 'implement output', 'verify output', 'develop status B'],
            'parent scope must include the parent\'s own lines and its child run\'s lines, in the order they streamed in'
        );

        // Step names must be shown (unlike a leaf scope) since lines from
        // multiple distinct steps are merged together here.
        const prefixes = logLineEls(window).map((el) => el.querySelector('.log-line-prefix').textContent);
        assert.ok(prefixes.some((p) => p.includes('(implement)')), 'merged parent scope should still label which step a line came from');
        assert.ok(prefixes.some((p) => p.includes('(verify)')));
    } finally {
        window.close();
    }
});

test('a scoped view keeps appending matching lines live, and ignores lines outside the scope', async () => {
    const sources = [];
    const window = await bootConsole(sources);
    try {
        await selectFirstRow(window);
        const es = await waitForStream(sources);

        await clickStep(window, 'implement');
        await waitFor(() => window.document.getElementById('console-log-content'), 'log pane never rendered');

        // Nothing has streamed yet — a running step with no lines shows a
        // waiting placeholder, not the "no output" message.
        await waitFor(() => window.document.getElementById('console-log-content').textContent.trim().length > 0, 'placeholder never rendered');
        assert.match(window.document.getElementById('console-log-content').textContent, /Waiting for output/);

        emit(es, { run_id: 'r2', step_name: 'implement', type: 'llm', content: 'first implement line', timestamp: '2026-09-17T10:00:01Z' });
        await waitFor(() => contentTexts(window).length === 1, 'first scoped line never appended');
        assert.deepEqual(contentTexts(window), ['first implement line']);

        // A line from a sibling step must not leak into the scoped view.
        emit(es, { run_id: 'r2', step_name: 'verify', type: 'llm', content: 'verify line', timestamp: '2026-09-17T10:00:02Z' });
        await delay(20);
        assert.deepEqual(contentTexts(window), ['first implement line'], 'a sibling step\'s line must not appear while scoped');

        // A second matching line keeps appending after the first.
        emit(es, { run_id: 'r2', step_name: 'implement', type: 'llm', content: 'second implement line', timestamp: '2026-09-17T10:00:03Z' });
        await waitFor(() => contentTexts(window).length === 2, 'second scoped line never appended');
        assert.deepEqual(contentTexts(window), ['first implement line', 'second implement line']);
    } finally {
        window.close();
    }
});
