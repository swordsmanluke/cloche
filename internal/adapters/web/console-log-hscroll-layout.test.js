// Layout regression test for the console log viewer's wrap toggle
// (static/style.css .console-log-viewer pre / .console-log-wrap): with wrap
// off, a line wider than the viewer must be reachable by scrolling
// horizontally; with wrap on, it must wrap instead. A real browser is
// required — the bug was .log-line's content-visibility paint containment
// clipping the overflow of a viewer-width line box, which jsdom's
// non-rendering DOM can't show. Renders the real static/style.css against a
// minimal hand-built log pane; no console.js, no backend.
//
// Requires `npx playwright install firefox`, same as the other layout
// tests. Not part of `npm test` — run with `npm run test:e2e`, or
// `node --test console-log-hscroll-layout.test.js`.
'use strict';

const test = require('node:test');
const assert = require('node:assert/strict');
const fs = require('node:fs');
const path = require('node:path');
const { firefox } = require('playwright');

const STYLE_CSS_SRC = fs.readFileSync(path.join(__dirname, 'static/style.css'), 'utf8');

const VIEWER_WIDTH = 800;
const LONG_LINE_INDEX = 5;

function pageHTML() {
    const lines = [];
    for (let i = 0; i < 300; i++) {
        const content = i === LONG_LINE_INDEX ? 'word '.repeat(300) + 'END' : 'short line ' + i;
        lines.push('<span class="log-line"><span class="log-line-prefix">12:00:00 </span>' +
            '<span class="log-line-content">' + content + '</span></span>');
    }
    return '<style>' + STYLE_CSS_SRC + '</style>' +
        '<div style="display:flex;flex-direction:column;height:500px;width:' + VIEWER_WIDTH + 'px">' +
        '<div class="log-viewer console-log-viewer" id="console-log-viewer">' +
        '<pre id="console-log-content">' + lines.join('') + '</pre></div></div>';
}

// content-visibility: auto decides what to render after the first layout, so
// measurements need a beat to settle.
async function measure(page) {
    await page.waitForTimeout(300);
    return page.evaluate((idx) => {
        const viewer = document.getElementById('console-log-viewer');
        const line = document.querySelectorAll('.log-line')[idx];
        const shortLine = document.querySelectorAll('.log-line')[idx + 1];
        return {
            clientWidth: viewer.clientWidth,
            scrollWidth: viewer.scrollWidth,
            lineHeight: line.getBoundingClientRect().height,
            shortLineHeight: shortLine.getBoundingClientRect().height,
        };
    }, LONG_LINE_INDEX);
}

test('log viewer: wrap off scrolls horizontally, wrap on wraps', async (t) => {
    const browser = await firefox.launch();
    t.after(() => browser.close());
    const page = await browser.newPage({ viewport: { width: 1000, height: 700 } });
    await page.setContent(pageHTML());

    const off = await measure(page);
    assert.ok(off.scrollWidth > off.clientWidth * 2,
        'wrap off: viewer must be horizontally scrollable to the end of a long line ' +
        '(scrollWidth ' + off.scrollWidth + ', clientWidth ' + off.clientWidth + ')');
    assert.equal(off.lineHeight, off.shortLineHeight, 'wrap off: a long line stays on one row');

    // The end of the line must actually be reachable, and stay reachable
    // once the long line has scrolled out of view vertically.
    const scrolled = await page.evaluate(async () => {
        const viewer = document.getElementById('console-log-viewer');
        viewer.scrollLeft = 1e6;
        const maxLeft = viewer.scrollLeft;
        viewer.scrollTop = 1e6;
        await new Promise((r) => setTimeout(r, 200));
        return { maxLeft: maxLeft, leftAfterVerticalScroll: viewer.scrollLeft };
    });
    assert.ok(scrolled.maxLeft > VIEWER_WIDTH, 'wrap off: scrollLeft reaches the end of the long line');
    assert.equal(scrolled.leftAfterVerticalScroll, scrolled.maxLeft,
        'wrap off: horizontal position survives scrolling the long line off-screen');

    await page.evaluate(() => {
        const viewer = document.getElementById('console-log-viewer');
        viewer.scrollLeft = 0;
        viewer.scrollTop = 0;
        document.getElementById('console-log-content').classList.add('console-log-wrap');
    });
    const on = await measure(page);
    assert.equal(on.scrollWidth, on.clientWidth, 'wrap on: nothing overflows horizontally');
    assert.ok(on.lineHeight > on.shortLineHeight * 2, 'wrap on: a long line wraps onto several rows');
});
