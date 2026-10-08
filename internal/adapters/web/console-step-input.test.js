'use strict';

// Step input panel: the "in" affordance on a step chip opens the action
// panel on /api/runs/{id}/steps/{step}/input — one block per attempt — and
// does not also toggle the log scope the chip itself controls. Boots
// static/console.js in jsdom against a stubbed fetch, like console-foot.test.js.

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
    return raw.slice(start, end).replace(/\{\{end\}\}\s*$/, '')
        .replace('{{.ProjectSlug}}', projectSlug)
        .replace('{{.TaskID}}', '')
        .replace('{{clocheVersion}}', 'test');
}

function jsonResponse(body, headers) {
    return Promise.resolve({
        ok: true,
        status: 200,
        headers: { get: (k) => (headers && headers[k]) || null },
        json: () => Promise.resolve(body)
    });
}

function delay(ms) { return new Promise((resolve) => setTimeout(resolve, ms)); }

async function waitFor(predicate) {
    const deadline = Date.now() + 2000;
    while (Date.now() < deadline) {
        if (predicate()) return true;
        await delay(20);
    }
    return predicate();
}

test('the step chip\'s "in" affordance opens the input panel per attempt without changing the log scope', async () => {
    const steps = [
        { run_id: 'r1', step_name: 'implement', depth: 0, result: '', started_at: '2026-10-08T10:00:00Z' }
    ];
    const inputRequests = [];
    const dom = new JSDOM('<!DOCTYPE html><html><body>' + consoleContentMarkup('myproj') + '</body></html>',
        { url: 'http://localhost/myproj', runScripts: 'outside-only' });
    const { window } = dom;
    window.EventSource = function () { this.close = function () {}; this.addEventListener = function () {}; };
    window.HTMLElement.prototype.scrollIntoView = function () {};
    const intervalIds = [];
    const origSetInterval = window.setInterval.bind(window);
    window.setInterval = function (fn, ms) { const id = origSetInterval(fn, ms); intervalIds.push(id); return id; };
    window.fetch = function (url) {
        if (url === '/api/projects') return jsonResponse([]);
        if (/\/tasks\/stack(\?|$)/.test(url)) {
            return jsonResponse({ needs_you: [], queued: [], done: [],
                running: [{ task_id: 'cloche-abcd', title: 'task', run_id: 'r1', attempt: 1, elapsed_seconds: 10 }] }, { ETag: 'W/"s1"' });
        }
        if (/\/tasks\/[^/]+\/attempts$/.test(url)) {
            return jsonResponse({ title: 'task', attempts: [{ attempt_num: 1, attempt_id: 'a1', run_id: 'r1', outcome: '' }] });
        }
        if (/\/steps\/implement\/input$/.test(url)) {
            inputRequests.push(url);
            return jsonResponse({ run_id: 'r1', step_name: 'implement', inputs: [
                { attempt: 1, kind: 'prompt', content: 'Original prompt text', created_at: '2026-10-08T10:00:00Z' },
                { attempt: 2, kind: 'prompt', content: 'Retry prompt text', created_at: '2026-10-08T10:05:00Z' }
            ] });
        }
        if (/\/api\/runs\/r1$/.test(url)) return jsonResponse({ id: 'r1', state: 'running', steps: steps });
        if (/^\/api\/activity/.test(url)) return jsonResponse({ entries: [] });
        return jsonResponse({});
    };
    window.eval(CONSOLE_TABS_SRC);
    window.eval(CONSOLE_JS_SRC);
    await delay(0);

    try {
        assert.ok(await waitFor(() => window.document.querySelector('.console-stack-row')));
        window.document.querySelector('.console-stack-row').dispatchEvent(new window.Event('click', { bubbles: true }));
        assert.ok(await waitFor(() => window.document.querySelector('.console-step-strip .console-step-segment')));

        const seg = window.document.querySelector('.console-step-strip .console-step-segment');
        // The affordance adds no text to the chip name.
        assert.equal(seg.querySelector('.console-step-segment-name').textContent.trim(), 'implement');
        const affordance = seg.querySelector('.console-step-segment-input');
        assert.ok(affordance, 'chip should carry an input affordance');
        assert.equal(affordance.getAttribute('aria-label'), 'Show input for step implement');

        affordance.dispatchEvent(new window.MouseEvent('click', { bubbles: true }));
        assert.equal(inputRequests.length, 1);
        assert.equal(inputRequests[0], '/api/runs/r1/steps/implement/input');

        const panel = window.document.getElementById('console-action-panel');
        assert.ok(await waitFor(() => !panel.hidden && panel.querySelectorAll('.console-step-input-pre').length === 2));
        assert.equal(panel.querySelector('h3').textContent, 'Input · implement');
        const heads = Array.from(panel.querySelectorAll('.console-step-input-head')).map((h) => h.textContent);
        assert.match(heads[0], /^attempt 1 · prompt/);
        assert.match(heads[1], /^attempt 2 · prompt/);
        const pres = Array.from(panel.querySelectorAll('.console-step-input-pre')).map((p) => p.textContent);
        assert.deepEqual(pres, ['Original prompt text', 'Retry prompt text']);

        // Clicking the affordance did not scope the log to the step.
        assert.equal(window.document.querySelector('.console-step-segment-selected'), null);
        // The input text is nowhere in the log pane.
        const logPane = window.document.getElementById('console-log') || window.document.querySelector('.console-log-lines');
        if (logPane) assert.ok(logPane.textContent.indexOf('Original prompt text') === -1);
    } finally {
        intervalIds.forEach((id) => window.clearInterval(id));
        window.close();
    }
});
