// Unit tests for background-tab throttling (static/console.js
// pauseBackgroundWork / resumeBackgroundWork, wired to the
// 'visibilitychange' event): a hidden console tab must stop polling
// (stack/ticker/instruments/projects), and showing it again must both
// restart those polls and immediately refresh once rather than waiting for
// the next interval tick. Runs the real static/console-tabs.js +
// static/console.js in a jsdom window against a stubbed fetch — no real
// network, no Go server.
//
// Run with: npm test (from internal/adapters/web/), or
// node --test internal/adapters/web/console-visibility.test.js
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

// installIntervalSpy tracks every currently-active window.setInterval id so
// a test can observe pausing/resuming without waiting for real poll
// intervals (several seconds) to elapse.
function installIntervalSpy(window) {
    const active = new Set();
    const origSet = window.setInterval.bind(window);
    const origClear = window.clearInterval.bind(window);
    window.setInterval = function (fn, ms) {
        const id = origSet(fn, ms);
        active.add(id);
        return id;
    };
    window.clearInterval = function (id) {
        active.delete(id);
        return origClear(id);
    };
    return active;
}

function installTestShims(window) {
    window.HTMLElement.prototype.scrollIntoView = function () {};
    const origClose = window.close.bind(window);
    window.close = function () { origClose(); };
}

function makeStubFetch(stack) {
    const calls = [];
    const fetchFn = function (url) {
        calls.push(String(url));
        if (url === '/api/projects') return jsonResponse([]);
        if (/\/tasks\/stack(\?|$)/.test(url)) return jsonResponse(stack, { ETag: 'W/"stack-1"' });
        return jsonResponse({});
    };
    fetchFn.calls = calls;
    return fetchFn;
}

function setHidden(window, hidden) {
    Object.defineProperty(window.document, 'hidden', { value: hidden, configurable: true });
}

function fireVisibilityChange(window) {
    window.document.dispatchEvent(new window.Event('visibilitychange'));
}

async function bootWithProject(window, fetchStub) {
    window.fetch = fetchStub;
    window.eval(CONSOLE_TABS_SRC);
    window.eval(CONSOLE_JS_SRC);
    await delay(50); // let init()'s selectProject settle and its pollers start
}

test('hiding the tab clears the background poll intervals', async () => {
    const dom = new JSDOM(
        '<!DOCTYPE html><html><body>' + consoleContentMarkup('myproj') + '</body></html>',
        { url: 'http://localhost/myproj', runScripts: 'outside-only' }
    );
    const { window } = dom;
    installTestShims(window);
    const active = installIntervalSpy(window);
    await bootWithProject(window, makeStubFetch({ needs_you: [], running: [], queued: [], done: [] }));

    try {
        const before = active.size;
        assert.ok(before > 0, 'boot should have started at least one poll interval (projects/stack/ticker/instruments)');

        setHidden(window, true);
        fireVisibilityChange(window);

        assert.equal(active.size, 0, 'hiding the tab must clear every pollable interval');
    } finally {
        window.close();
    }
});

test('showing the tab again restarts polling and does one immediate refresh, not a wait for the next tick', async () => {
    const dom = new JSDOM(
        '<!DOCTYPE html><html><body>' + consoleContentMarkup('myproj') + '</body></html>',
        { url: 'http://localhost/myproj', runScripts: 'outside-only' }
    );
    const { window } = dom;
    installTestShims(window);
    const active = installIntervalSpy(window);
    const fetchStub = makeStubFetch({ needs_you: [], running: [], queued: [], done: [] });
    await bootWithProject(window, fetchStub);

    try {
        setHidden(window, true);
        fireVisibilityChange(window);
        assert.equal(active.size, 0);

        const stackCallsBefore = fetchStub.calls.filter((u) => /\/tasks\/stack/.test(u)).length;

        setHidden(window, false);
        fireVisibilityChange(window);
        await delay(20);

        assert.ok(active.size > 0, 'showing the tab must restart the poll intervals');
        const stackCallsAfter = fetchStub.calls.filter((u) => /\/tasks\/stack/.test(u)).length;
        assert.ok(stackCallsAfter > stackCallsBefore, 'showing the tab must trigger an immediate stack refresh, not wait for the next 4s tick');
    } finally {
        window.close();
    }
});
