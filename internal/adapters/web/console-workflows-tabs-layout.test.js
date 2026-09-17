// Layout regression test for the Workflows view (static/style.css .tab-bar):
// with many workflows, or many host/container location tabs, the tab strip
// must scroll horizontally on its own — the DAG below it must not be
// dragged along sideways. A real browser is required because this is a
// flexbox/overflow sizing issue that jsdom's non-rendering DOM can't catch
// (getBoundingClientRect and scrollWidth/scrollLeft are meaningless there).
// Boots the real templates/layout.html + templates/console.html +
// static/console.js + static/style.css in headless Firefox via Playwright,
// against a fully route-mocked backend (no real cloched) — no real network,
// no Go server.
//
// Requires `npx playwright install firefox` (and, on a bare Linux host, the
// system libraries Firefox itself depends on) before running. Not part of
// `npm test` for that reason — run explicitly with `npm run test:e2e`, or
// `node --test console-workflows-tabs-layout.test.js`.
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

// Many workflows across both locations, with long names, so the tab strip
// overflows the panel width and both the workflow-name tab bar and the
// host/container location tab bar are exercised.
function workflowsFixture() {
    const workflows = [];
    for (let i = 0; i < 20; i++) {
        workflows.push({
            name: 'a-fairly-long-descriptive-workflow-name-' + i,
            file: 'develop.cloche',
            location: 'container',
            steps: [{ name: 'step-one', type: 'agent', results: ['done'], config: {} }],
            wires: [],
            entry_step: 'step-one',
            builtin: false
        });
    }
    workflows.push({
        name: 'release',
        file: 'host.cloche',
        location: 'host',
        steps: [{ name: 'tag', type: 'run', results: ['done'], config: {} }],
        wires: [],
        entry_step: 'tag',
        builtin: false
    });
    return workflows;
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
            return jsonRoute(route, [{ slug: PROJECT_SLUG, name: PROJECT_SLUG }]);
        }
        if (/\/tasks\/stack$/.test(p)) {
            return jsonRoute(route, {
                needs_you: [], queued: [], done: [],
                running: [{ task_id: TASK_ID, title: 'Workflow tabs layout', run_id: RUN_ID, kind: 'main', since: new Date().toISOString() }]
            });
        }
        if (new RegExp('/tasks/' + TASK_ID + '/attempts$').test(p)) {
            return jsonRoute(route, {
                title: 'Workflow tabs layout',
                attempts: [{ attempt_num: 1, attempt_id: 'a1', run_id: RUN_ID, outcome: '', started_at: new Date().toISOString(), duration: '' }]
            });
        }
        if (p === '/api/runs/' + RUN_ID) {
            return jsonRoute(route, { id: RUN_ID, task_id: TASK_ID, title: 'Workflow tabs layout', status: 'running', steps: [] });
        }
        if (p === '/api/projects/' + PROJECT_SLUG + '/workflows') {
            return jsonRoute(route, workflowsFixture());
        }
        if (p === '/api/attempts/' + encodeURIComponent('a1') + '/stream' || p === '/api/runs/' + RUN_ID + '/stream') {
            return route.fulfill({ status: 200, contentType: 'text/event-stream', body: 'event: meta\ndata: {"skipped":0}\n\nevent: done\ndata: {}\n\n' });
        }
        // Everything else (instruments, ticker, ledger, thread, etc.) is
        // irrelevant to this layout assertion — answer with an inert empty
        // body so those pollers don't error.
        return jsonRoute(route, {});
    });
}

test('workflows view: tab bars scroll on their own, the DAG panel does not move with them', async function () {
    const browser = await firefox.launch({ headless: true });
    try {
        const page = await browser.newPage({ viewport: { width: 1000, height: 800 } });
        await mockBackend(page);
        await page.goto('http://cloche.test/' + PROJECT_SLUG + '/' + TASK_ID);

        await page.click('#console-tools-btn');
        await page.click('#console-view-workflows-btn');
        await page.waitForSelector('#workflow-tabs .tab-btn');
        await page.waitForSelector('#workflow-dag svg');

        const tabBar = page.locator('#workflow-tabs');
        const locationBar = page.locator('#location-tabs');
        const viewBody = page.locator('#console-view-body');
        const dag = page.locator('#workflow-dag');

        // The tab strip itself must overflow (that's the whole point of the
        // fixture: 20 long workflow names plus a host/container split).
        const tabBarOverflow = await tabBar.evaluate((el) => ({ scrollWidth: el.scrollWidth, clientWidth: el.clientWidth }));
        assert.ok(tabBarOverflow.scrollWidth > tabBarOverflow.clientWidth,
            'fixture must overflow #workflow-tabs (scrollWidth ' + tabBarOverflow.scrollWidth + ' vs clientWidth ' + tabBarOverflow.clientWidth + ')');

        const locationBarOverflow = await locationBar.evaluate((el) => ({ scrollWidth: el.scrollWidth, clientWidth: el.clientWidth }));
        assert.ok(locationBarOverflow.scrollWidth <= locationBarOverflow.clientWidth + 0.5,
            'the two-item location tab bar should fit without overflowing in this fixture');

        // The enclosing view body (which also contains the DAG) must not
        // grow wider than the panel to accommodate the tabs — only the tab
        // bar itself should be scrollable.
        const bodyOverflow = await viewBody.evaluate((el) => ({ scrollWidth: el.scrollWidth, clientWidth: el.clientWidth }));
        assert.ok(bodyOverflow.scrollWidth <= bodyOverflow.clientWidth + 0.5,
            '#console-view-body must not overflow horizontally just because the tab bar has many tabs ' +
            '(scrollWidth ' + bodyOverflow.scrollWidth + ' vs clientWidth ' + bodyOverflow.clientWidth + ') — ' +
            'if this fails, .tab-bar lost its own overflow-x:auto and is dragging the whole view sideways again');

        const dagBoxBefore = await dag.boundingBox();

        // Scrolling the tab strip itself must not move the DAG panel below it.
        await tabBar.evaluate((el) => { el.scrollLeft = el.scrollWidth; });
        const dagBoxAfter = await dag.boundingBox();
        assert.ok(Math.abs(dagBoxBefore.x - dagBoxAfter.x) < 0.5,
            'scrolling #workflow-tabs must not shift #workflow-dag horizontally (before x=' + dagBoxBefore.x + ', after x=' + dagBoxAfter.x + ')');

        // The active tab, once selected, must be scrolled into view within
        // its own tab bar rather than left clipped off-screen.
        const lastTab = tabBar.locator('.tab-btn').last();
        await lastTab.click({ force: true });
        await page.waitForFunction(() => {
            const btns = document.querySelectorAll('#workflow-tabs .tab-btn');
            return btns[btns.length - 1].classList.contains('tab-active');
        });
        const tabBarBox = await tabBar.boundingBox();
        const lastTabBox = await lastTab.boundingBox();
        assert.ok(lastTabBox.x >= tabBarBox.x - 0.5 && (lastTabBox.x + lastTabBox.width) <= (tabBarBox.x + tabBarBox.width) + 0.5,
            'the active tab must be scrolled fully into view within #workflow-tabs after selection');
    } finally {
        await browser.close();
    }
});
