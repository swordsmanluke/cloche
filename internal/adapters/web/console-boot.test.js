// Smoke test for console.js's boot sequence: first paint must not gate on
// /api/projects, and both the tab bar and the task stack must populate
// within 2s even when /api/projects is slow (e.g. a cold attention cache).
// Runs the actual static/console-tabs.js + static/console.js in a jsdom
// window against a stubbed fetch — no real network, no Go server.
//
// Run with: npm test (from internal/adapters/web/), or
// node --test internal/adapters/web/console-boot.test.js
'use strict';

const test = require('node:test');
const assert = require('node:assert/strict');
const fs = require('node:fs');
const path = require('node:path');
const { JSDOM } = require('jsdom');

const CONSOLE_TABS_SRC = fs.readFileSync(path.join(__dirname, 'static/console-tabs.js'), 'utf8');
const CONSOLE_JS_SRC = fs.readFileSync(path.join(__dirname, 'static/console.js'), 'utf8');

// Reuses the real template so this test breaks if the markup and
// console.js's element IDs drift apart, instead of testing a hand-copied
// fixture that could silently go stale.
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

// Builds a fetch stub: /api/projects (the tab bar's fast projects list,
// standing in for the attention-cache-backed endpoint) resolves only after
// projectsDelayMs, simulating a slow/cold attention cache; /tasks/stack
// resolves quickly. Every other endpoint the instruments/ticker pollers hit
// resolves immediately with an empty body — those aren't under test here.
function makeStubFetch({ projectsDelayMs, projects, stack }) {
    const calls = [];
    const fetchFn = function (url) {
        calls.push({ url: String(url), t: Date.now() });
        if (url === '/api/projects') {
            // Infinity: a promise that never settles, rather than a real
            // (Node-global, window-independent) timer that would otherwise
            // fire — and touch the jsdom window — after the test, and any
            // window.close() it did for cleanup, have already finished.
            if (projectsDelayMs === Infinity) return new Promise(() => {});
            return delay(projectsDelayMs).then(() => jsonResponse(projects));
        }
        if (/\/tasks\/stack(\?|$)/.test(url)) {
            return jsonResponse(stack, { ETag: 'W/"stack-1"' });
        }
        return jsonResponse({});
    };
    fetchFn.calls = calls;
    return fetchFn;
}

function bootConsole(dom, fetchStub) {
    const { window } = dom;
    window.fetch = fetchStub;
    window.eval(CONSOLE_TABS_SRC);
    window.eval(CONSOLE_JS_SRC);
    return window;
}

test('boot paints tab bar and stack skeletons synchronously, before any fetch resolves', async () => {
    const dom = new JSDOM(
        '<!DOCTYPE html><html><body>' + consoleContentMarkup('myproj') + '</body></html>',
        { url: 'http://localhost/myproj', runScripts: 'outside-only' }
    );
    const fetchStub = makeStubFetch({
        projectsDelayMs: Infinity, // never resolves — see makeStubFetch
        projects: [],
        stack: { needs_you: [], running: [], queued: [], done: [] }
    });
    const window = bootConsole(dom, fetchStub);
    try {
        const tabs = window.document.getElementById('console-project-tabs');
        assert.ok(tabs.querySelector('.console-tab-skeleton'), 'tab bar should show a skeleton immediately, not a blank frame');

        const stack = window.document.getElementById('console-stack');
        assert.match(stack.textContent, /Loading/, 'stack pane should show a loading placeholder immediately, not a blank frame');

        // The stack request for the active project must have been issued
        // immediately — it must not wait on /api/projects to resolve first.
        const stackCalled = fetchStub.calls.some((c) => /\/tasks\/stack/.test(c.url));
        assert.ok(stackCalled, '/tasks/stack must be requested without waiting for /api/projects');

        // Let the other boot-time fetches (instruments, ticker — all
        // stubbed to resolve immediately) finish their .then chains before
        // closing the window, so they don't touch a torn-down `document`.
        await delay(50);
    } finally {
        // console.js's pollers (setInterval) hold real timers open; without
        // this the process — and `node --test` — would hang after the test
        // finishes instead of exiting.
        window.close();
    }
});

test('stack and tab bar populate within 2s even when /api/projects (the attention-backed list) is slow', async () => {
    const dom = new JSDOM(
        '<!DOCTYPE html><html><body>' + consoleContentMarkup('myproj') + '</body></html>',
        { url: 'http://localhost/myproj', runScripts: 'outside-only' }
    );
    const fetchStub = makeStubFetch({
        projectsDelayMs: 1500, // slow, but still under the 2s budget
        projects: [{
            dir: '/home/user/myproj', label: 'myproj', slug: 'myproj',
            health: { status: 'green', passed: 1, failed: 0, total: 1 },
            active_count: 0, attention_count: 2, attention_computed_at: new Date().toISOString(),
            loop_running: false, latest_run_at: new Date().toISOString()
        }],
        stack: {
            needs_you: [{ kind: 'parked', task_id: 't1', title: 'Needs a decision', reason: 'parked', since: new Date().toISOString() }],
            running: [], queued: [], done: []
        }
    });
    const window = bootConsole(dom, fetchStub);
    try {
        const deadline = Date.now() + 2000;
        while (Date.now() < deadline) {
            const stackText = window.document.getElementById('console-stack').textContent;
            const tabsText = window.document.getElementById('console-project-tabs').textContent;
            if (stackText.indexOf('Needs a decision') !== -1 && tabsText.indexOf('myproj') !== -1) break;
            await delay(20);
        }

        const stackEl = window.document.getElementById('console-stack');
        assert.match(stackEl.textContent, /Needs a decision/, 'needs-you row should populate once the (fast) stack request resolves');

        const tabsEl = window.document.getElementById('console-project-tabs');
        assert.match(tabsEl.textContent, /myproj/, 'tab bar should populate once /api/projects resolves, even though it was slow');

        // Attention count patches into the tab (rendered once /api/projects
        // resolves) rather than blocking anything else.
        assert.ok(tabsEl.querySelector('.console-tab-flag'), 'attention flag should render once the slow attention-backed list arrives');
    } finally {
        window.close();
    }
});
