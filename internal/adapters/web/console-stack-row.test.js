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
    // jsdom doesn't implement scrollIntoView, called whenever a stack row is
    // selected (applySelectionHighlight) — stub it so selecting/clicking a
    // row doesn't throw.
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

test('running row: id, step, and title render as separate elements in that order, and the id never carries ellipsis/truncation styling or sits inside the step element', async () => {
    const window = await bootConsole({
        needs_you: [],
        running: [{ task_id: 'cloche-zhn6.10', title: 'move the step line off line 1', run_id: 'r1', attempt: 1, current_step: 'develop,implement', elapsed_seconds: 22320 }],
        queued: [], done: []
    });
    try {
        const row = window.document.querySelector('.console-stack-row');
        const content = row.querySelector('.console-stack-row-content');
        const idEl = content.querySelector('.console-stack-row-id');
        const stepEl = content.querySelector('.console-stack-row-step');
        const titleEl = content.querySelector('.console-stack-row-title');

        assert.ok(idEl, 'id element must be present');
        assert.ok(stepEl, 'step element must be present for a running row with a known step');
        assert.ok(titleEl, 'title element must be present');

        // Order: line1 (holding the id) comes before the step line, which
        // comes before the title — three stacked elements, not one crowded
        // line1.
        const order = Array.prototype.indexOf.call(content.children, idEl.closest('.console-stack-row-line1'));
        const stepOrder = Array.prototype.indexOf.call(content.children, stepEl);
        const titleOrder = Array.prototype.indexOf.call(content.children, titleEl);
        assert.ok(order < stepOrder && stepOrder < titleOrder, 'id line, then step, then title, in that order');

        assert.equal(idEl.textContent, 'cloche-zhn6.10', 'the id is never truncated, however long');
        assert.equal(idEl.className, 'console-stack-row-id', 'the id element carries no ellipsis/truncation modifier class');
        assert.equal(stepEl.closest('.console-stack-row-id'), null, 'the id is not nested inside the step element');
        assert.equal(idEl.closest('.console-stack-row-step'), null, 'the id is not nested inside the step element');

        assert.equal(stepEl.textContent, 'develop,implement');
        // The step no longer crowds into the elapsed text on line 1.
        const elapsed = row.querySelector('.console-stack-row-elapsed');
        assert.equal(elapsed.textContent, '6h 12m');
    } finally {
        window.close();
    }
});

test('done row: never renders a step element, even though the field could in principle be present', async () => {
    const window = await bootConsole({
        needs_you: [], running: [], queued: [],
        done: [{ task_id: 'cloche-7pf5', title: 'recover missing result marker', run_id: 'r3', outcome: 'succeeded', duration_seconds: 480, current_step: 'finalize' }]
    });
    try {
        const row = window.document.querySelector('.console-stack-row');
        assert.equal(row.querySelector('.console-stack-row-step'), null, 'a done row never shows a step line');
    } finally {
        window.close();
    }
});

test('running row: with no known step, the row renders no step element at all (no empty line-2 gap)', async () => {
    const window = await bootConsole({
        needs_you: [], queued: [], done: [],
        running: [{ task_id: 'cloche-ccgl', title: 'twelve-task list', run_id: 'r2', attempt: 1, elapsed_seconds: 85 }]
    });
    try {
        const row = window.document.querySelector('.console-stack-row');
        assert.equal(row.querySelector('.console-stack-row-step'), null, 'no step element when current_step is unknown');
    } finally {
        window.close();
    }
});

test('updateRow (via the 4s poll) updates an existing running row\'s step text in place, reusing the same row element', async () => {
    let stack = {
        needs_you: [], queued: [], done: [],
        running: [{ task_id: 'cloche-fnn6', title: 'hidden acceptance corpus', run_id: 'r1', attempt: 1, current_step: 'develop', elapsed_seconds: 30 }]
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
        if (window.document.querySelector('.console-stack-row-step')) break;
        await delay(20);
    }

    try {
        const row = window.document.querySelector('.console-stack-row');
        assert.equal(row.querySelector('.console-stack-row-step').textContent, 'develop');

        stack = {
            needs_you: [], queued: [], done: [],
            running: [{ task_id: 'cloche-fnn6', title: 'hidden acceptance corpus', run_id: 'r1', attempt: 1, current_step: 'implement', elapsed_seconds: 34 }]
        };
        assert.ok(timers[4000], 'stack poll interval was registered');
        timers[4000]();

        const pollDeadline = Date.now() + 2000;
        while (Date.now() < pollDeadline) {
            const s = row.querySelector('.console-stack-row-step');
            if (s && s.textContent === 'implement') break;
            await delay(20);
        }

        assert.equal(
            window.document.querySelector('.console-stack-row'), row,
            'the poll reuses the same row element rather than rebuilding the whole row'
        );
        assert.equal(row.querySelector('.console-stack-row-step').textContent, 'implement', 'the step text updates in place on the 4s poll');
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

// bootConsoleWithProject is bootConsole, but with a real /api/projects entry
// (rather than an empty list) so ConsoleTabs.repoNamesForProject can resolve
// the active project's repo grouping.
async function bootConsoleWithProject(project, stack) {
    const dom = new JSDOM(
        '<!DOCTYPE html><html><body>' + consoleContentMarkup(project.slug) + '</body></html>',
        { url: 'http://localhost/' + project.slug, runScripts: 'outside-only' }
    );
    const { window } = dom;
    window.HTMLElement.prototype.scrollIntoView = function () {};
    window.fetch = function (url) {
        if (url === '/api/projects') return jsonResponse([project]);
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
    return window;
}

test('all-repos view on a multi-repo project: rows carry a small repo tag so the merged view stays legible', async () => {
    const window = await bootConsoleWithProject(
        { slug: 'manager', label: 'manager', repositories: [{ name: 'manager', path: '.' }, { name: 'anarkana', path: './repos/anarkana' }] },
        {
            needs_you: [], queued: [], done: [],
            running: [{ task_id: 'manager-fnn6', title: 'Port event bus to typed channels', run_id: 'r1', attempt: 1, elapsed_seconds: 10, repository: 'anarkana' }]
        }
    );
    try {
        // The project list resolves asynchronously; wait for the repo tag
        // (which depends on it) to actually render.
        const deadline = Date.now() + 2000;
        while (Date.now() < deadline) {
            if (window.document.querySelector('.console-stack-row-repotag')) break;
            await delay(20);
        }
        const idEl = window.document.querySelector('.console-stack-row-id');
        assert.ok(idEl.querySelector('.console-stack-row-repotag'), 'a repo tag must be present in the all-repos view');
        assert.equal(idEl.textContent, 'manager-fnn6 · anarkana');
    } finally {
        window.close();
    }
});

test('a legacy (single/no-repo) project never renders a repo tag, even if a run somehow carries one', async () => {
    const window = await bootConsoleWithProject(
        { slug: 'myproj', label: 'myproj' },
        {
            needs_you: [], queued: [], done: [],
            running: [{ task_id: 'cloche-fnn6', title: 'some task', run_id: 'r1', attempt: 1, elapsed_seconds: 10, repository: 'stray' }]
        }
    );
    try {
        await delay(50);
        const idEl = window.document.querySelector('.console-stack-row-id');
        assert.equal(idEl.querySelector('.console-stack-row-repotag'), null);
        assert.equal(idEl.textContent, 'cloche-fnn6');
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

test('done row: succeeded outcome drops the redundant status word, keeping only the duration', async () => {
    const window = await bootConsole({
        needs_you: [], running: [], queued: [],
        done: [{ task_id: 'cloche-7pf5', title: 'recover missing result marker', run_id: 'r3', outcome: 'succeeded', duration_seconds: 480 }]
    });
    try {
        const row = window.document.querySelector('.console-stack-row');
        const elapsed = row.querySelector('.console-stack-row-elapsed');
        assert.equal(elapsed.textContent, '8m', 'the "succeeded" word is dropped — the dot colour and Done heading already say it');
        assert.equal(elapsed.classList.contains('console-stack-row-elapsed-bad'), false);
    } finally {
        window.close();
    }
});

test('done row: failed and cancelled outcomes keep the status word, scannable in the matching status colour', async () => {
    const window = await bootConsole({
        needs_you: [], running: [], queued: [],
        done: [
            { task_id: 'cloche-ulid', title: 'first attempt, retrying now', run_id: 'r4', outcome: 'failed', duration_seconds: 1740 },
            { task_id: 'cloche-xq2p', title: 'stopped mid-run', run_id: 'r5', outcome: 'cancelled', duration_seconds: 60 }
        ]
    });
    try {
        const rows = window.document.querySelectorAll('.console-stack-row');
        const failedElapsed = rows[0].querySelector('.console-stack-row-elapsed');
        assert.equal(failedElapsed.textContent, 'failed · 29m');
        assert.ok(failedElapsed.classList.contains('console-stack-row-elapsed-bad'), 'failed status word is coloured with the bad status token');

        const cancelledElapsed = rows[1].querySelector('.console-stack-row-elapsed');
        assert.equal(cancelledElapsed.textContent, 'cancelled · 1m');
    } finally {
        window.close();
    }
});

test('row title is omitted (no empty gap) when it is identical to the task id, e.g. a default user-* task title', async () => {
    const window = await bootConsole({
        needs_you: [], running: [], queued: [],
        done: [{ task_id: 'user-zfo8', title: 'user-zfo8', run_id: 'r6', outcome: 'succeeded', duration_seconds: 30 }]
    });
    try {
        const row = window.document.querySelector('.console-stack-row');
        assert.equal(row.querySelector('.console-stack-row-id').textContent, 'user-zfo8');
        assert.equal(row.querySelector('.console-stack-row-title'), null, 'a title identical to the id renders no second line');
    } finally {
        window.close();
    }
});

test('row title still renders on its own line when it differs from the task id', async () => {
    const window = await bootConsole({
        needs_you: [], running: [], queued: [],
        done: [{ task_id: 'cloche-7pf5', title: 'recover missing result marker', run_id: 'r3', outcome: 'succeeded', duration_seconds: 480 }]
    });
    try {
        const row = window.document.querySelector('.console-stack-row');
        assert.equal(row.querySelector('.console-stack-row-title').textContent, 'recover missing result marker');
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

test('empty Needs you / Queued groups are omitted; Running and Done always stay, dash and all', async () => {
    let stack = {
        needs_you: [],
        running: [],
        queued: [],
        done: []
    };
    const dom = new JSDOM(
        '<!DOCTYPE html><html><body>' + consoleContentMarkup('myproj') + '</body></html>',
        { url: 'http://localhost/myproj', runScripts: 'outside-only' }
    );
    const { window } = dom;
    window.HTMLElement.prototype.scrollIntoView = function () {};
    const timers = installStackPollShim(window);
    window.fetch = function (url) {
        if (url === '/api/projects') return jsonResponse([]);
        if (/\/tasks\/stack(\?|$)/.test(url)) return jsonResponse(stack, { ETag: 'W/"stack-1"' });
        return jsonResponse({});
    };
    window.eval(CONSOLE_TABS_SRC);
    window.eval(CONSOLE_JS_SRC);

    // Wait for the real render (which has a .console-stack-group-count
    // element) rather than the loading skeleton (which doesn't), since an
    // all-empty stack never produces a .console-stack-row to key off of.
    const deadline = Date.now() + 2000;
    while (Date.now() < deadline) {
        if (window.document.querySelector('.console-stack-group-count')) break;
        await delay(20);
    }

    try {
        assert.equal(groupSection(window, 'Needs you').hidden, true, 'empty Needs you group is omitted');
        assert.equal(groupSection(window, 'Queued').hidden, true, 'empty Queued group is omitted');

        var runningSection = groupSection(window, 'Running');
        assert.equal(runningSection.hidden, false, 'Running always stays visible, even when empty');
        assert.ok(runningSection.querySelector('.console-stack-empty'), 'empty Running still shows its dash placeholder');
        assert.equal(runningSection.querySelector('.console-stack-group-count').textContent, '0', 'empty Running count reads 0, not blank');

        var doneSection = groupSection(window, 'Done');
        assert.equal(doneSection.hidden, false, 'Done always stays visible, even when empty');
        assert.ok(doneSection.querySelector('.console-stack-empty'), 'empty Done still shows its dash placeholder');
        assert.equal(groupSection(window, 'Needs you').querySelector('.console-stack-empty'), null, 'omitted groups do not render a dash placeholder');

        // Simulate the next 4s poll finding a Needs-you row and a Running one.
        stack = {
            needs_you: [{ task_id: 'cloche-usjb', title: 'bonsai executor wrapper', reason: 'failed ×3' }],
            running: [{ task_id: 'cloche-fnn6', title: 'hidden acceptance corpus', run_id: 'r1', attempt: 1, elapsed_seconds: 10 }],
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
        assert.equal(groupSection(window, 'Running').hidden, false, 'Running stays visible with rows too');

        // Losing its only row again should not make Running disappear.
        stack = { needs_you: [], running: [], queued: [], done: [] };
        timers[4000]();
        const emptyAgainDeadline = Date.now() + 2000;
        while (Date.now() < emptyAgainDeadline) {
            if (groupSection(window, 'Needs you').hidden === true) break;
            await delay(20);
        }
        assert.equal(groupSection(window, 'Running').hidden, false, 'Running stays visible even after emptying out again');
        assert.equal(groupSection(window, 'Needs you').hidden, true, 'Needs you is omitted again once it empties out');
    } finally {
        window.close();
    }
});

test('Done toggle: clicking the header collapses the body while the header and count stay visible', async () => {
    const window = await bootConsole({
        needs_you: [], running: [], queued: [],
        done: [{ task_id: 'cloche-7pf5', title: 'recover missing result marker', run_id: 'r3', outcome: 'succeeded', duration_seconds: 480 }]
    });
    try {
        const doneSection = groupSection(window, 'Done');
        const header = doneSection.querySelector('.console-stack-group-title');
        assert.equal(header.getAttribute('aria-expanded'), 'true', 'Done starts expanded by default');
        assert.equal(doneSection.classList.contains('console-stack-group-collapsed'), false);

        header.dispatchEvent(new window.Event('click', { bubbles: true }));

        assert.equal(doneSection.classList.contains('console-stack-group-collapsed'), true, 'clicking the header collapses the body');
        assert.equal(header.getAttribute('aria-expanded'), 'false');
        assert.equal(doneSection.hidden, false, 'the header row stays visible when collapsed');
        assert.equal(header.querySelector('.console-stack-group-count').textContent, '1', 'the count stays visible when collapsed');

        header.dispatchEvent(new window.Event('click', { bubbles: true }));
        assert.equal(doneSection.classList.contains('console-stack-group-collapsed'), false, 'clicking again re-expands');
        assert.equal(header.getAttribute('aria-expanded'), 'true');
    } finally {
        window.close();
    }
});

test('Done toggle: collapsed state persists to localStorage and survives a poll re-render', async () => {
    const dom = new JSDOM(
        '<!DOCTYPE html><html><body>' + consoleContentMarkup('myproj') + '</body></html>',
        { url: 'http://localhost/myproj', runScripts: 'outside-only' }
    );
    const { window } = dom;
    window.HTMLElement.prototype.scrollIntoView = function () {};
    const timers = installStackPollShim(window);
    const stack = {
        needs_you: [], running: [], queued: [],
        done: [{ task_id: 'cloche-7pf5', title: 'task', run_id: 'r3', outcome: 'succeeded', duration_seconds: 480 }]
    };
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
        const doneSection = groupSection(window, 'Done');
        const header = doneSection.querySelector('.console-stack-group-title');
        header.dispatchEvent(new window.Event('click', { bubbles: true }));
        assert.equal(window.localStorage.getItem('console.stack.done.collapsed'), '1', 'collapse persists to localStorage');

        // A poll re-render must not rebuild the section (which would lose
        // the collapsed class) nor flip it back to expanded.
        assert.ok(timers[4000], 'stack poll interval was registered');
        timers[4000]();
        await delay(50);

        assert.equal(groupSection(window, 'Done').classList.contains('console-stack-group-collapsed'), true, 'collapsed state survives a poll re-render');
    } finally {
        window.close();
    }
});

test('Done toggle: falls back to expanded when localStorage throws on read or write', async () => {
    const window = await bootConsole({
        needs_you: [], running: [], queued: [],
        done: [{ task_id: 'cloche-7pf5', title: 'task', run_id: 'r3', outcome: 'succeeded', duration_seconds: 480 }]
    });
    try {
        const throwingStorage = {
            getItem: () => { throw new Error('storage disabled'); },
            setItem: () => { throw new Error('storage disabled'); }
        };
        Object.defineProperty(window, 'localStorage', { value: throwingStorage, configurable: true });

        const doneSection = groupSection(window, 'Done');
        const header = doneSection.querySelector('.console-stack-group-title');

        assert.doesNotThrow(function () {
            header.dispatchEvent(new window.Event('click', { bubbles: true }));
        }, 'a throwing localStorage.setItem must not break the toggle');

        assert.equal(doneSection.classList.contains('console-stack-group-collapsed'), true, 'the toggle still applies in memory even when persistence fails');
    } finally {
        window.close();
    }
});

test('j/k skip the rows of a collapsed Done group, and selection moves off a Done row when it collapses', async () => {
    const window = await bootConsole({
        needs_you: [], running: [], queued: [],
        done: [
            { task_id: 'task-a', title: 'task a', run_id: 'run-a', outcome: 'succeeded', duration_seconds: 10 },
            { task_id: 'task-b', title: 'task b', run_id: 'run-b', outcome: 'succeeded', duration_seconds: 20 }
        ]
    });
    try {
        const rows = window.document.querySelectorAll('.console-stack-row');
        rows[0].click();
        assert.ok(rows[0].classList.contains('console-stack-row-selected'), 'first Done row is selected');
        // Opening a row kicks off an attempts-list fetch (renderCentrePane ->
        // loadAttempts); let it settle before the window closes below, or its
        // .then callback fires against an already-torn-down document.
        await delay(50);

        const header = groupSection(window, 'Done').querySelector('.console-stack-group-title');
        header.dispatchEvent(new window.Event('click', { bubbles: true }));

        assert.equal(rows[0].classList.contains('console-stack-row-selected'), false, 'selection moves off a Done row once its group collapses');

        // With nothing else in the stack, j/k must not throw and must leave
        // no row selected — there is nothing else to navigate to.
        window.document.dispatchEvent(new window.KeyboardEvent('keydown', { key: 'j', bubbles: true }));
        window.document.dispatchEvent(new window.KeyboardEvent('keydown', { key: 'k', bubbles: true }));
        assert.equal(window.document.querySelectorAll('.console-stack-row-selected').length, 0, 'j/k do not select a collapsed row');
    } finally {
        window.close();
    }
});

test('opening a Done task by URL expands a collapsed Done group', async () => {
    const window = await bootConsole({
        needs_you: [], running: [], queued: [],
        done: [{ task_id: 'cloche-7pf5', title: 'recover missing result marker', run_id: 'r3', outcome: 'succeeded', duration_seconds: 480 }]
    });
    try {
        const doneSection = groupSection(window, 'Done');
        const header = doneSection.querySelector('.console-stack-group-title');
        header.dispatchEvent(new window.Event('click', { bubbles: true }));
        assert.equal(doneSection.classList.contains('console-stack-group-collapsed'), true, 'Done starts collapsed for this test');

        window.history.pushState({}, '', '/myproj/cloche-7pf5');
        window.dispatchEvent(new window.PopStateEvent('popstate'));
        await delay(50);

        assert.equal(groupSection(window, 'Done').classList.contains('console-stack-group-collapsed'), false, 'opening a Done task by URL expands the group');
        const row = window.document.querySelector('.console-stack-row[data-key="done:cloche-7pf5"]');
        assert.ok(row.classList.contains('console-stack-row-selected'), 'the opened Done row is selected once expanded');
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
