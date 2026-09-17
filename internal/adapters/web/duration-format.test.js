// Table-driven tests for static/console.js's formatDuration/formatElapsed
// (~L4646), mirroring the boundary table in
// internal/durationfmt/durationfmt_test.go: below 60s shows seconds, below
// 1h shows minutes, below 24h shows "Xh Ym", 24h+ rolls into days (dropping
// minutes), and 7d+ drops down to days only. Exercised indirectly through a
// running task-stack row's elapsed column, since formatDuration lives inside
// console.js's module IIFE and isn't exported directly.
//
// Run with: npm test (from internal/adapters/web/), or
// node --test internal/adapters/web/duration-format.test.js
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
    return function (url) {
        if (url === '/api/projects') return jsonResponse([]);
        if (/\/tasks\/stack(\?|$)/.test(url)) return jsonResponse(stack, { ETag: 'W/"stack-1"' });
        return jsonResponse({});
    };
}

async function bootConsole(stack) {
    const dom = new JSDOM(
        '<!DOCTYPE html><html><body>' + consoleContentMarkup('myproj') + '</body></html>',
        { url: 'http://localhost/myproj', runScripts: 'outside-only' }
    );
    const { window } = dom;
    window.HTMLElement.prototype.scrollIntoView = function () {};
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

async function elapsedTextFor(seconds) {
    const window = await bootConsole({
        needs_you: [], queued: [], done: [],
        running: [{ task_id: 'cloche-abcd', title: 'long poll', run_id: 'r1', attempt: 1, elapsed_seconds: seconds }]
    });
    try {
        return window.document.querySelector('.console-stack-row-elapsed').textContent;
    } finally {
        window.close();
    }
}

const BOUNDARIES = [
    { name: '59s', seconds: 59, want: '59s' },
    { name: '60s', seconds: 60, want: '1m' },
    { name: '59m59s', seconds: 59 * 60 + 59, want: '59m' },
    { name: '1h', seconds: 3600, want: '1h 0m' },
    { name: '23h59m', seconds: 23 * 3600 + 59 * 60, want: '23h 59m' },
    { name: '24h', seconds: 24 * 3600, want: '1d 0h' },
    { name: '6d23h', seconds: 6 * 86400 + 23 * 3600, want: '6d 23h' },
    { name: '7d', seconds: 7 * 86400, want: '7d' }
];

for (const { name, seconds, want } of BOUNDARIES) {
    test('formatDuration boundary: ' + name + ' -> "' + want + '"', async () => {
        assert.equal(await elapsedTextFor(seconds), want);
    });
}
