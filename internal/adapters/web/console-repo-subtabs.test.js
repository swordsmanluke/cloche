// Headless smoke test for the console's repo sub-tab row (docs/design/
// console-repo-grouping-mock.html, option 1): a multi-repo project renders
// the sub-tab row and scoping to one repo removes the other repo's rows from
// the stack (and updates the URL); a legacy (single/no-repo) project renders
// no sub-tab row at all. Boots the real templates/layout.html +
// templates/console.html + static/console-tabs.js + static/console.js +
// static/style.css in headless Firefox via Playwright, against a fully
// route-mocked backend (no real cloched) — no real network, no Go server.
//
// Requires `npx playwright install firefox` (and, on a bare Linux host, the
// system libraries Firefox itself depends on) before running. Not part of
// `npm test` for that reason — run explicitly with `npm run test:e2e`, or
// `node --test console-repo-subtabs.test.js`.
'use strict';

const test = require('node:test');
const assert = require('node:assert/strict');
const fs = require('node:fs');
const path = require('node:path');
const { firefox } = require('playwright');

const LAYOUT_SRC = fs.readFileSync(path.join(__dirname, 'templates/layout.html'), 'utf8');
const CONSOLE_TABS_SRC = fs.readFileSync(path.join(__dirname, 'static/console-tabs.js'), 'utf8');
const CONSOLE_JS_SRC = fs.readFileSync(path.join(__dirname, 'static/console.js'), 'utf8');
const STYLE_CSS_SRC = fs.readFileSync(path.join(__dirname, 'static/style.css'), 'utf8');

const MULTI_SLUG = 'manager';
const LEGACY_SLUG = 'cloche';

// Builds the real served page for slug, exactly as handler_console.go would
// for GET /{slug} — so this test breaks if the markup or console.js's
// element IDs drift, instead of testing a hand-copied fixture.
function pageHTML(slug) {
    const contentRaw = fs.readFileSync(path.join(__dirname, 'templates/console.html'), 'utf8');
    const contentStart = contentRaw.indexOf('{{define "content"}}') + '{{define "content"}}'.length;
    const contentEnd = contentRaw.indexOf('{{define "scripts"}}');
    const content = contentRaw.slice(contentStart, contentEnd)
        .replace(/\{\{end\}\}\s*$/, '')
        .replace('{{.ProjectSlug}}', slug)
        .replace('{{.TaskID}}', '')
        .replace('{{clocheVersion}}', 'test');

    return LAYOUT_SRC
        .replace('{{define "layout"}}', '')
        .replace(/\{\{end\}\}\s*$/, '')
        .replace('{{.Title}}', 'Cloche')
        .replace('{{block "content" .}}{{end}}', content)
        .replace('{{block "scripts" .}}{{end}}', '<script src="/static/console-tabs.js"></script><script src="/static/console.js"></script>');
}

function jsonRoute(route, body) {
    return route.fulfill({ status: 200, contentType: 'application/json', body: JSON.stringify(body) });
}

const PROJECTS = [
    {
        dir: '/home/user/manager', label: 'manager', slug: MULTI_SLUG,
        health: { status: 'green', passed: 1, failed: 0, total: 1 },
        active_count: 2, attention_count: 0, attention_computed_at: '', loop_running: false, latest_run_at: '',
        repositories: [{ name: 'manager', path: '.' }, { name: 'anarkana', path: './repos/anarkana' }]
    },
    {
        dir: '/home/user/cloche', label: 'cloche', slug: LEGACY_SLUG,
        health: { status: 'green', passed: 1, failed: 0, total: 1 },
        active_count: 0, attention_count: 0, attention_computed_at: '', loop_running: false, latest_run_at: ''
    }
];

function multiRepoStack() {
    return {
        needs_you: [], queued: [],
        done: [],
        running: [
            { task_id: 'manager-ccgl', title: 'Add per-repo occupancy to status page', run_id: 'run-manager-ccgl', repository: 'manager', current_step: 'build', started_at: new Date().toISOString(), elapsed_seconds: 60 },
            { task_id: 'manager-fnn6', title: 'Port event bus to typed channels', run_id: 'run-manager-fnn6', repository: 'anarkana', current_step: 'build', started_at: new Date().toISOString(), elapsed_seconds: 124 }
        ],
        repo_counts: {
            manager: { needs_you: 0, running: 1, queued: 0, done: 0 },
            anarkana: { needs_you: 0, running: 1, queued: 0, done: 0 }
        }
    };
}

function legacyStack() {
    return {
        needs_you: [], queued: [], done: [],
        running: [
            { task_id: 'cloche-eiil8', title: 'Fix stale project cache', run_id: 'run-cloche-eiil8', current_step: 'build', started_at: new Date().toISOString(), elapsed_seconds: 30 }
        ]
    };
}

async function mockBackend(page, stackByProject) {
    await page.route('**/*', function (route) {
        const req = route.request();
        const url = new URL(req.url());
        const p = url.pathname;

        if (p === '/' + MULTI_SLUG || p === '/' + LEGACY_SLUG) {
            return route.fulfill({ status: 200, contentType: 'text/html', body: pageHTML(p.slice(1)) });
        }
        if (p === '/static/console-tabs.js') {
            return route.fulfill({ status: 200, contentType: 'application/javascript', body: CONSOLE_TABS_SRC });
        }
        if (p === '/static/console.js') {
            return route.fulfill({ status: 200, contentType: 'application/javascript', body: CONSOLE_JS_SRC });
        }
        if (p === '/static/style.css') {
            return route.fulfill({ status: 200, contentType: 'text/css', body: STYLE_CSS_SRC });
        }
        if (p === '/api/projects') {
            return jsonRoute(route, PROJECTS);
        }
        const stackMatch = /^\/api\/projects\/([^/]+)\/tasks\/stack$/.exec(p);
        if (stackMatch) {
            const slug = stackMatch[1];
            const repo = url.searchParams.get('repo');
            const full = stackByProject[slug]();
            if (!repo || repo === 'all') return jsonRoute(route, full);
            return jsonRoute(route, Object.assign({}, full, {
                running: (full.running || []).filter((r) => r.repository === repo),
                needs_you: (full.needs_you || []).filter((r) => r.repository === repo),
                queued: (full.queued || []).filter((r) => r.repository === repo),
                done: (full.done || []).filter((r) => r.repository === repo)
            }));
        }
        // Everything else (instruments, ticker, ledger, workflows, thread,
        // etc.) is irrelevant to this test — answer with an inert empty
        // body so those pollers don't error.
        return jsonRoute(route, {});
    });
}

test('console repo sub-tabs: multi-repo project shows the row and scoping removes the other repo\'s rows', async function () {
    const browser = await firefox.launch({ headless: true });
    try {
        const page = await browser.newPage({ viewport: { width: 1440, height: 900 } });
        await mockBackend(page, { [MULTI_SLUG]: multiRepoStack, [LEGACY_SLUG]: legacyStack });
        await page.goto('http://cloche.test/' + MULTI_SLUG);

        await page.waitForSelector('.console-subtabs .console-subtab');
        const subtabs = await page.locator('.console-subtabs .console-subtab').allTextContents();
        assert.equal(subtabs.length, 3, 'expected "all repos" + 2 configured repos');
        assert.match(subtabs[0], /all repos/);
        assert.match(subtabs[1], /manager/);
        assert.match(subtabs[2], /anarkana/);

        // Unscoped: both repos' rows are present.
        await page.waitForFunction(() => document.querySelectorAll('.console-stack-row').length === 2);

        // Scope to "anarkana": the manager row must disappear, not just be
        // labelled — the interleaving itself goes away.
        await page.locator('.console-subtabs .console-subtab', { hasText: 'anarkana' }).click();
        await page.waitForFunction(() => {
            const rows = Array.from(document.querySelectorAll('.console-stack-row'));
            return rows.length === 1 && rows[0].textContent.includes('manager-fnn6');
        });
        const rowIds = await page.locator('.console-stack-row .console-stack-row-id').allTextContents();
        assert.ok(!rowIds.some((t) => t.includes('manager-ccgl')), 'the other repo\'s row must be gone, not just unlabelled');

        assert.equal(new URL(page.url()).pathname, '/' + MULTI_SLUG + '/anarkana');

        // Jump back to "all repos".
        await page.locator('.console-subtabs .console-subtab', { hasText: 'all repos' }).click();
        await page.waitForFunction(() => document.querySelectorAll('.console-stack-row').length === 2);
        assert.equal(new URL(page.url()).pathname, '/' + MULTI_SLUG);
    } finally {
        await browser.close();
    }
});

test('console repo sub-tabs: a legacy project renders no sub-tab row', async function () {
    const browser = await firefox.launch({ headless: true });
    try {
        const page = await browser.newPage({ viewport: { width: 1440, height: 900 } });
        await mockBackend(page, { [MULTI_SLUG]: multiRepoStack, [LEGACY_SLUG]: legacyStack });
        await page.goto('http://cloche.test/' + LEGACY_SLUG);

        await page.waitForSelector('.console-stack-row');
        const subtabsRow = page.locator('#console-subtabs');
        assert.equal(await subtabsRow.isHidden(), true, 'a legacy project must render no sub-tab row at all');
        assert.equal((await subtabsRow.innerHTML()).trim(), '', 'the sub-tab row must be empty, not just visually collapsed');
    } finally {
        await browser.close();
    }
});
