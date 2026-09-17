// Layout regression test for the header's dropdown menus (static/style.css
// .console-menu-wrap / .console-tools-menu / .console-idle-menu): both menus
// must open flush under their own trigger button, not anchored to the edge
// of the whole .console-tabbar. A real browser is required because this is
// a getBoundingClientRect/position:absolute anchoring issue that jsdom's
// non-rendering DOM can't catch. Boots the real templates/layout.html +
// templates/console.html + static/console.js + static/style.css in headless
// Firefox via Playwright, against a fully route-mocked backend (no real
// cloched) — no real network, no Go server.
//
// Requires `npx playwright install firefox` (and, on a bare Linux host, the
// system libraries Firefox itself depends on) before running. Not part of
// `npm test` for that reason — run explicitly with `npm run test:e2e`, or
// `node --test console-tools-menu-layout.test.js`.
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

const PROJECT_SLUG = 'myproj';
const TASK_ID = 'cloche-eiil8';
const RUN_ID = 'run-eiil8-1';

// Builds the real served page the same way console-step-strip-layout.test.js
// does: templates/layout.html's "content" and "scripts" blocks filled in
// exactly as handler_console.go would for GET /{slug}/{taskId}, so this
// test breaks if the markup or console.js's element IDs drift.
function pageHTML() {
    const contentRaw = fs.readFileSync(path.join(__dirname, 'templates/console.html'), 'utf8');
    const contentStart = contentRaw.indexOf('{{define "content"}}') + '{{define "content"}}'.length;
    const contentEnd = contentRaw.indexOf('{{define "scripts"}}');
    const content = contentRaw.slice(contentStart, contentEnd)
        .replace(/\{\{end\}\}\s*$/, '')
        .replace('{{.ProjectSlug}}', PROJECT_SLUG)
        .replace('{{.TaskID}}', TASK_ID)
        .replace('{{clocheVersion}}', 'test');

    return LAYOUT_SRC
        .replace('{{define "layout"}}', '')
        .replace(/\{\{end\}\}\s*$/, '')
        .replace('{{.Title}}', 'Cloche')
        .replace('{{block "content" .}}{{end}}', content)
        .replace('{{block "scripts" .}}{{end}}', '<script src="/static/console-tabs.js"></script><script src="/static/console.js"></script>');
}

// 12 idle projects (no live activity) beyond the active one so the tab-bar
// fold rule (TAB_VISIBLE_BUDGET = 8 in console.js) folds several of them
// into the idle-projects "More" menu.
function projectsFixture() {
    const projects = [{ slug: PROJECT_SLUG, label: PROJECT_SLUG, latest_run_at: new Date().toISOString() }];
    for (let i = 0; i < 12; i++) {
        projects.push({ slug: 'idle-' + i, label: 'idle-project-' + i, latest_run_at: new Date(Date.now() - i * 60000).toISOString() });
    }
    return projects;
}

function jsonRoute(route, body) {
    return route.fulfill({ status: 200, contentType: 'application/json', body: JSON.stringify(body) });
}

async function mockBackend(page) {
    await page.route('**/*', function (route) {
        const req = route.request();
        const url = new URL(req.url());
        const p = url.pathname;

        if (p === '/' + PROJECT_SLUG + '/' + TASK_ID) {
            return route.fulfill({ status: 200, contentType: 'text/html', body: pageHTML() });
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
            return jsonRoute(route, projectsFixture());
        }
        if (/\/tasks\/stack$/.test(p)) {
            return jsonRoute(route, {
                needs_you: [], queued: [], done: [],
                running: [{ task_id: TASK_ID, title: 'Tools menu layout', run_id: RUN_ID, kind: 'main', since: new Date().toISOString() }]
            });
        }
        if (new RegExp('/tasks/' + TASK_ID + '/attempts$').test(p)) {
            return jsonRoute(route, {
                title: 'Tools menu layout',
                attempts: [{ attempt_num: 1, attempt_id: 'a1', run_id: RUN_ID, outcome: '', started_at: new Date().toISOString(), duration: '' }]
            });
        }
        if (p === '/api/runs/' + RUN_ID) {
            return jsonRoute(route, { id: RUN_ID, task_id: TASK_ID, title: 'Tools menu layout', status: 'running', steps: [] });
        }
        if (p === '/api/attempts/' + encodeURIComponent('a1') + '/stream' || p === '/api/runs/' + RUN_ID + '/stream') {
            return route.fulfill({ status: 200, contentType: 'text/event-stream', body: 'event: meta\ndata: {"skipped":0}\n\nevent: done\ndata: {}\n\n' });
        }
        // Everything else (instruments, ticker, ledger, workflows, thread,
        // etc.) is irrelevant to this layout assertion — answer with an
        // inert empty body so those pollers don't error.
        return jsonRoute(route, {});
    });
}

// Real browsers must render at least the 1024px-and-up range this bug fix
// promises to cover; 1024 is the narrow edge of that range and 1600 is a
// wide desktop viewport, so both ends are exercised.
for (const width of [1024, 1600]) {
    test('console-tools-btn: the Tools menu opens flush under its own button at ' + width + 'px wide', async function () {
        const browser = await firefox.launch({ headless: true });
        try {
            const page = await browser.newPage({ viewport: { width, height: 900 } });
            await mockBackend(page);
            await page.goto('http://cloche.test/' + PROJECT_SLUG + '/' + TASK_ID);
            await page.waitForSelector('#console-tools-btn');

            const btn = page.locator('#console-tools-btn');
            await btn.click();
            const menu = page.locator('#console-tools-menu');
            await assert.doesNotReject(menu.waitFor({ state: 'visible', timeout: 5000 }));

            const btnBox = await btn.boundingBox();
            const menuBox = await menu.boundingBox();
            assert.ok(btnBox && menuBox, 'button and menu must both be rendered');

            assert.ok(
                Math.abs(menuBox.x - btnBox.x) < 1,
                'the Tools menu left edge (' + menuBox.x + ') must equal the button left edge (' + btnBox.x + ') — ' +
                'if this fails, the menu is anchored to the tab bar again instead of its own button'
            );
            assert.ok(
                menuBox.y >= btnBox.y + btnBox.height - 0.5,
                'the Tools menu must sit below the button, not overlap it'
            );
            assert.ok(
                menuBox.x + menuBox.width <= width + 0.5,
                'the Tools menu must not overflow the right edge of the viewport'
            );
        } finally {
            await browser.close();
        }
    });

    test('console-more-btn: the idle-projects menu opens flush under its own button at ' + width + 'px wide', async function () {
        const browser = await firefox.launch({ headless: true });
        try {
            const page = await browser.newPage({ viewport: { width, height: 900 } });
            await mockBackend(page);
            await page.goto('http://cloche.test/' + PROJECT_SLUG + '/' + TASK_ID);
            await page.waitForSelector('#console-more-btn:not([hidden])');

            const btn = page.locator('#console-more-btn');
            await btn.click();
            const menu = page.locator('#console-idle-menu');
            await assert.doesNotReject(menu.waitFor({ state: 'visible', timeout: 5000 }));

            const btnBox = await btn.boundingBox();
            const menuBox = await menu.boundingBox();
            assert.ok(btnBox && menuBox, 'button and menu must both be rendered');

            assert.ok(
                Math.abs(menuBox.x - btnBox.x) < 1,
                'the idle-projects menu left edge (' + menuBox.x + ') must equal the button left edge (' + btnBox.x + ')'
            );
            assert.ok(
                menuBox.y >= btnBox.y + btnBox.height - 0.5,
                'the idle-projects menu must sit below the button, not overlap it'
            );
        } finally {
            await browser.close();
        }
    });
}
