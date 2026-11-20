// Hover / click highlighting of the workflow graph: ConsoleDag.highlight
// unit tests, plus the console wiring (hover lights a step's outgoing edges,
// click keeps them lit while the drawer is open). jsdom, stubbed fetch.
'use strict';

const test = require('node:test');
const assert = require('node:assert/strict');
const fs = require('node:fs');
const path = require('node:path');
const { JSDOM } = require('jsdom');
const Dag = require('./static/console-dag.js');

const WF = {
    name: 'develop', file: 'develop.cloche', location: 'container', entry_step: 'implement',
    steps: [
        { name: 'implement', type: 'agent', results: ['success'] },
        { name: 'test', type: 'script', results: ['pass', 'fail'] },
        { name: 'review', type: 'poll', results: ['approved'] },
        { name: 'fix', type: 'agent', results: ['fixed', 'give-up'] }
    ],
    wires: [
        { from: 'implement', result: 'success', to: 'test' },
        { from: 'test', result: 'pass', to: 'review' },
        { from: 'test', result: 'fail', to: 'fix' },
        { from: 'fix', result: 'fixed', to: 'test' },
        { from: 'fix', result: 'give-up', to: 'abort' },
        { from: 'review', result: 'approved', to: 'done' }
    ]
};

const hotEdges = (doc) => [...doc.querySelectorAll('path.dag-hot')].map((e) => e.dataset.from + '>' + e.dataset.to).sort();
const lit = (doc, cls) => [...doc.querySelectorAll('.' + cls)].map((e) => e.dataset.node).sort();

test('highlight(): outgoing edges of a step are hot, its targets are marked, the rest dim', () => {
    const { window } = new JSDOM('<div id="r"></div>');
    const root = window.document.getElementById('r');
    root.innerHTML = Dag.render(WF, 0).html;
    Dag.highlight(root, ['test']);
    assert.deepEqual(hotEdges(root), ['test>fix', 'test>review']);
    assert.deepEqual(lit(root, 'dag-sel'), ['test']);
    assert.deepEqual(lit(root, 'dag-dest'), ['fix', 'review']);
    assert.ok(root.classList.contains('dag-focus'));
    // labels follow their edge
    assert.deepEqual([...root.querySelectorAll('text.dag-hot')].map((t) => t.textContent).sort(), ['fail', 'pass']);
    // two steps at once (hover + selected)
    Dag.highlight(root, ['fix', 'implement']);
    assert.deepEqual(hotEdges(root), ['fix>@abort'.replace('@', ''), 'fix>test', 'implement>test'].sort());
    Dag.highlight(root, [null, null]);
    assert.deepEqual(hotEdges(root), []);
    assert.ok(!root.classList.contains('dag-focus'));
    assert.deepEqual(lit(root, 'dag-sel').concat(lit(root, 'dag-dest')), []);
});

test('arrowheads follow the edge colour (context-stroke)', () => {
    assert.match(Dag.render(WF, 0).html, /<polygon class="dag-arrow" fill="context-stroke"/);
});

// ---- console wiring --------------------------------------------------------
const tabsSrc = fs.readFileSync(path.join(__dirname, 'static/console-tabs.js'), 'utf8');
const dagSrc = fs.readFileSync(path.join(__dirname, 'static/console-dag.js'), 'utf8');
const consoleSrc = fs.readFileSync(path.join(__dirname, 'static/console.js'), 'utf8');

function markup() {
    const raw = fs.readFileSync(path.join(__dirname, 'templates/console.html'), 'utf8');
    const start = raw.indexOf('{{define "content"}}') + '{{define "content"}}'.length;
    const end = raw.indexOf('{{define "scripts"}}');
    return raw.slice(start, end).replace(/\{\{end\}\}\s*$/, '')
        .replace('{{.ProjectSlug}}', 'myproj').replace('{{.TaskID}}', '').replace('{{clocheVersion}}', 'test');
}
const json = (body) => Promise.resolve({ ok: true, status: 200, headers: { get: () => null }, json: () => Promise.resolve(body), text: () => Promise.resolve('') });
const delay = (ms) => new Promise((r) => setTimeout(r, ms));

async function bootWorkflows() {
    const dom = new JSDOM('<!DOCTYPE html><html><body>' + markup() + '</body></html>',
        { url: 'http://localhost/myproj', runScripts: 'outside-only', pretendToBeVisual: true });
    const w = dom.window;
    w.HTMLElement.prototype.scrollIntoView = function () {};
    w.fetch = (url) => {
        if (url === '/api/projects') return json([{ slug: 'myproj', label: 'myproj', dir: '/x' }]);
        if (/\/workflows$/.test(url)) return json([WF]);
        return json({});
    };
    w.eval(tabsSrc); w.eval(dagSrc); w.eval(consoleSrc);
    await delay(100);
    w.document.getElementById('console-view-workflows-btn').click();
    for (let i = 0; i < 100 && !w.document.querySelector('#workflow-dag svg'); i++) await delay(20);
    assert.ok(w.document.querySelector('#workflow-dag svg'), 'graph rendered');
    return w;
}
const ev = (w, type, el) => el.dispatchEvent(new w.MouseEvent(type, { bubbles: true }));

test('console: hover lights a step, leaving clears it; click keeps it lit and opens the drawer', async () => {
    const w = await bootWorkflows();
    try {
        const doc = w.document, dag = doc.getElementById('workflow-dag');
        const node = (n) => dag.querySelector('.dag-node[data-step="' + n + '"]');

        ev(w, 'mouseover', node('test'));
        assert.deepEqual(hotEdges(dag), ['test>fix', 'test>review']);

        ev(w, 'mouseleave', dag);
        assert.deepEqual(hotEdges(dag), [], 'cleared when the pointer leaves');

        ev(w, 'mouseover', node('test'));
        ev(w, 'click', node('test'));
        assert.ok(doc.getElementById('step-drawer').classList.contains('drawer-open'), 'drawer opens');
        ev(w, 'mouseleave', dag);
        assert.deepEqual(hotEdges(dag), ['test>fix', 'test>review'], 'stays lit after the pointer leaves');

        // hovering another step adds its edges while test stays selected
        ev(w, 'mouseover', node('fix'));
        assert.deepEqual(hotEdges(dag), ['fix>abort', 'fix>test', 'test>fix', 'test>review']);
        ev(w, 'mouseleave', dag);

        // clicking another step moves the selection
        ev(w, 'click', node('implement'));
        assert.deepEqual(hotEdges(dag), ['implement>test']);

        // closing the drawer drops the selection
        doc.getElementById('step-drawer-close').click();
        assert.deepEqual(hotEdges(dag), []);

        // clicking empty graph space also clears it
        ev(w, 'click', node('test'));
        ev(w, 'click', dag.querySelector('svg'));
        assert.deepEqual(hotEdges(dag), []);
    } finally {
        w.close();
    }
});
