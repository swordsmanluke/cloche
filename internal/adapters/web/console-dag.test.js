// Unit tests for static/console-dag.js: happy-path spine, off-path lanes,
// and the horizontal/vertical orientation choice. Pure Node, no DOM.
'use strict';

const test = require('node:test');
const assert = require('node:assert/strict');
const Dag = require('./static/console-dag.js');

const wf = {
    name: 'develop', entry_step: 'implement',
    steps: [
        { name: 'fix', type: 'agent' }, { name: 'implement', type: 'agent' },
        { name: 'merge', type: 'script' }, { name: 'review', type: 'poll' },
        { name: 'test', type: 'script' }
    ],
    wires: [
        { from: 'implement', result: 'success', to: 'test' },
        { from: 'test', result: 'pass', to: 'review' },
        { from: 'test', result: 'fail', to: 'fix' },
        { from: 'fix', result: 'fixed', to: 'test' },
        { from: 'fix', result: 'give-up', to: 'abort' },
        { from: 'review', result: 'approved', to: 'merge' },
        { from: 'review', result: 'changes requested', to: 'fix' },
        { from: 'merge', result: 'success', to: 'done' },
        { from: 'merge', result: 'timeout', to: 'abort', implicit: true }
    ]
};

test('happy path runs along lane 0; fix sits under test, abort beside fix', () => {
    const n = Dag.layout(wf).nodes;
    const lane0 = Object.values(n).filter((x) => x.lane === 0).sort((a, b) => a.col - b.col)
        .map((x) => x.pill || x.id);
    assert.deepEqual(lane0, ['task', 'implement', 'test', 'review', 'merge', 'done']);
    assert.equal(n.fix.lane, 1);
    assert.equal(n.fix.col, n.test.col);
    assert.equal(n.abort.lane, 1);
    assert.equal(n.abort.col, n.fix.col - 1);
});

test('orientation: horizontal when it fits, vertical when it does not', () => {
    assert.equal(Dag.render(wf, 0).vertical, false);
    assert.equal(Dag.render(wf, 5000).vertical, false);
    const v = Dag.render(wf, 400);
    assert.equal(v.vertical, true);
    const L = Dag.layout(wf);
    const h = Dag.svg(L, false), vt = Dag.svg(L, true);
    assert.ok(h.w > h.h, 'horizontal drawing is wider than tall');
    assert.ok(vt.h > vt.w, 'vertical drawing is taller than wide');
    assert.ok(vt.w < h.w);
});

test('every step is a clickable node and every wire is drawn once per pair', () => {
    const { html } = Dag.render(wf, 5000);
    for (const s of wf.steps) assert.match(html, new RegExp('data-step="' + s.name + '"'));
    assert.match(html, /changes requested/);
    assert.doesNotMatch(html, /timeout/); // implicit wires are hidden
    assert.match(html, /agent step: an LLM does the work/);
});

test('single step with no wires still renders', () => {
    const one = { name: 'x', entry_step: 'a', steps: [{ name: 'a', type: 'script' }], wires: [] };
    assert.match(Dag.render(one, 800).html, /data-step="a"/);
});

// ---- Real workflows -------------------------------------------------------
// testdata/real-workflows.json holds this repo's own workflows (and a few
// examples) in the shape /api/projects/<p>/workflows returns them.
const real = require('./testdata/real-workflows.json');
const byName = (suffix) => real.find((w) => w.name.endsWith(suffix));

function spineOf(w) {
    return Object.values(Dag.layout(w).nodes).filter((n) => n.lane === 0)
        .sort((a, b) => a.col - b.col).map((n) => n.pill || n.id);
}

// Pull rects and label boxes back out of the generated SVG.
function geometry(html) {
    const num = (s, k) => parseFloat(new RegExp('(?<![a-z])' + k + '="([-0-9.]+)"').exec(s)[1]);
    const svg = /<svg[^>]*>/.exec(html)[0];
    const rects = [...html.matchAll(/<rect [^>]*>/g)].map((m) => ({
        x: num(m[0], 'x'), y: num(m[0], 'y'), w: num(m[0], 'width'), h: num(m[0], 'height')
    }));
    const labels = [...html.matchAll(/<text class="dag-edge-label" text-anchor="(\w+)" x="([-0-9.]+)" y="([-0-9.]+)">([^<]*)<\/text>/g)]
        .map((m) => {
            const w = m[4].length * 6.6, x = +m[2], y = +m[3];
            const x0 = m[1] === 'middle' ? x - w / 2 : (m[1] === 'end' ? x - w : x);
            return { text: m[4], x0, x1: x0 + w, y0: y - 9, y1: y + 2 };
        });
    return { W: num(svg, 'width'), H: num(svg, 'height'), rects, labels };
}
const overlap = (a, b) => a.x0 < b.x1 && b.x0 < a.x1 && a.y0 < b.y1 && b.y0 < a.y1;

test('real develop: implement..update-docs on the spine, fix under test, abort beside it', () => {
    const w = byName('develop.cloche#develop');
    assert.deepEqual(spineOf(w), ['task', 'implement', 'verify-changes', 'test', 'review', 'update-docs', 'done']);
    const n = Dag.layout(w).nodes;
    assert.equal(n.fix.lane, 1);
    assert.equal(n.fix.col, n.test.col);
});

test('real host main: fix-merge-conflict hangs off merge; the happy path runs to done', () => {
    const w = byName('host.cloche#main');
    assert.deepEqual(spineOf(w), ['task', 'prepare-prompt', 'develop', 'merge', 'bump-version', 'close-task', 'cleanup', 'done']);
    const n = Dag.layout(w).nodes;
    assert.equal(n['fix-merge-conflict'].lane, 1);
    assert.equal(n['fix-merge-conflict'].col, n.merge.col);
});

test('real vertical (25 steps): a loop that only cycles back is not the happy path', () => {
    const w = byName('vertical.cloche#vertical');
    const spine = spineOf(w);
    assert.equal(spine[1], 'claim');
    assert.equal(spine[spine.length - 2], 'finalize');
    assert.equal(spine[spine.length - 1], 'done');
    const n = Dag.layout(w).nodes;
    // implement-layer -> open-pr -> poll-pr -> close-layer cycles back to pick-next-layer
    for (const s of ['implement-layer', 'open-pr', 'poll-pr', 'close-layer']) assert.ok(n[s].lane > 0, s);
    assert.ok(n['pick-next-layer'].lane === 0 && n['update-docs'].lane === 0);
});

test('a fail edge is never the happy path (mini-redis: fix stays off the spine)', () => {
    const w = byName('mini-redis/develop.cloche#develop');
    assert.ok(!spineOf(w).includes('fix'));
    assert.ok(Dag.layout(w).nodes.fix.lane >= 1);
});

test('workflow with no done (check-main) still lays out a spine', () => {
    const w = byName('host.cloche#check-main');
    assert.deepEqual(spineOf(w).slice(0, 4), ['task', 'wait', 'changed', 'suite']);
});

for (const w of real.filter((x) => x.steps.length > 1)) {
    test('drawing is well-formed in both orientations: ' + w.name, () => {
        const L = Dag.layout(w);
        // no two nodes share a grid cell, and every step was placed
        const cells = new Set();
        for (const n of Object.values(L.nodes)) {
            const k = n.lane + ',' + n.col;
            assert.ok(!cells.has(k), 'cell ' + k + ' used twice');
            cells.add(k);
        }
        for (const s of w.steps) assert.ok(L.nodes[s.name], s.name + ' not placed');

        for (const vertical of [false, true]) {
            const html = Dag.svg(L, vertical).svg;
            const g = geometry(html);
            for (const r of g.rects) {
                assert.ok(r.x >= 0 && r.y >= 0 && r.x + r.w <= g.W && r.y + r.h <= g.H, 'node outside drawing');
            }
            for (let i = 0; i < g.rects.length; i++) {
                for (let j = i + 1; j < g.rects.length; j++) {
                    const a = g.rects[i], b = g.rects[j];
                    assert.ok(!(a.x < b.x + b.w && b.x < a.x + a.w && a.y < b.y + b.h && b.y < a.y + a.h), 'nodes overlap');
                }
            }
            for (const l of g.labels) {
                assert.ok(l.x0 >= 0 && l.x1 <= g.W + 1 && l.y0 >= 0 && l.y1 <= g.H + 1, 'label "' + l.text + '" clipped');
            }
        }
    });
}

// check-main has parallel loop-back edges through one row, so its labels are
// known to crowd; every other real workflow must be free of label collisions.
for (const w of real.filter((x) => x.steps.length > 1 && !x.name.endsWith('#check-main'))) {
    test('no overlapping edge labels (vertical and horizontal): ' + w.name, () => {
        const L = Dag.layout(w);
        for (const vertical of [false, true]) {
            const g = geometry(Dag.svg(L, vertical).svg);
            for (let i = 0; i < g.labels.length; i++) {
                for (let j = i + 1; j < g.labels.length; j++) {
                    assert.ok(!overlap(g.labels[i], g.labels[j]),
                        (vertical ? 'vertical' : 'horizontal') + ': "' + g.labels[i].text + '" overlaps "' + g.labels[j].text + '"');
                }
            }
        }
    });
}

test('vertical drawing is never wider than horizontal for real workflows with 5+ columns', () => {
    for (const w of real.filter((x) => Dag.layout(x).cols >= 5)) {
        const L = Dag.layout(w);
        assert.ok(Dag.svg(L, true).w < Dag.svg(L, false).w, w.name);
    }
});

test('render() flips to vertical exactly when horizontal would not fit', () => {
    const w = byName('develop.cloche#develop');
    const need = Dag.svg(Dag.layout(w), false).w;
    assert.equal(Dag.render(w, need).vertical, false);
    assert.equal(Dag.render(w, need - 1).vertical, true);
});

test('odd shapes: self-loop, two-step cycle, long names, parallel results', () => {
    const odd = {
        name: 'odd', entry_step: 'a-very-long-step-name-indeed',
        steps: [{ name: 'a-very-long-step-name-indeed', type: 'agent' }, { name: 'b', type: 'script' }],
        wires: [
            { from: 'a-very-long-step-name-indeed', result: 'success', to: 'b' },
            { from: 'a-very-long-step-name-indeed', result: 'ok', to: 'b' },
            { from: 'b', result: 'retry', to: 'b' },
            { from: 'b', result: 'again', to: 'a-very-long-step-name-indeed' },
            { from: 'b', result: 'success', to: 'done' }
        ]
    };
    const r = Dag.render(odd, 0);
    assert.match(r.html, /success, ok/);
    const g = geometry(r.html);
    assert.ok(g.rects.every((x) => x.x + x.w <= g.W));
});

// An edge must never run behind a node that is not one of its own endpoints
// (it would read as that node's edge).
function edgesAndNodes(html) {
    const num = (s, k) => parseFloat(new RegExp('(?<![a-z])' + k + '="([-0-9.]+)"').exec(s)[1]);
    const nodes = [...html.matchAll(/<g class="dag-(?:node|pill)[^>]*data-node="([^"]*)"[^>]*><rect[^>]*>/g)].map((m) => ({
        id: m[1], x: num(m[0].slice(m[0].indexOf('<rect')), 'x'), y: num(m[0].slice(m[0].indexOf('<rect')), 'y'),
        w: num(m[0].slice(m[0].indexOf('<rect')), 'width'), h: num(m[0].slice(m[0].indexOf('<rect')), 'height')
    }));
    const edges = [...html.matchAll(/<path class="dag-edge" data-from="([^"]*)" data-to="([^"]*)"[^>]* d="([^"]*)"/g)].map((m) => ({
        from: m[1], to: m[2],
        pts: [...m[3].matchAll(/[ML]([-0-9.]+) ([-0-9.]+)/g)].map((p) => [+p[1], +p[2]])
    }));
    return { nodes, edges };
}

for (const w of real.filter((x) => x.steps.length > 1)) {
    test('no edge runs behind a node it does not touch: ' + w.name, () => {
        const L = Dag.layout(w);
        for (const vertical of [false, true]) {
            const { nodes, edges } = edgesAndNodes(Dag.svg(L, vertical).svg);
            for (const e of edges) {
                for (const n of nodes) {
                    if (n.id === e.from || n.id === e.to) continue;
                    for (let i = 0; i < e.pts.length - 1; i++) {
                        const [x0, y0] = e.pts[i], [x1, y1] = e.pts[i + 1];
                        const hit = Math.min(x0, x1) < n.x + n.w - 1 && Math.max(x0, x1) > n.x + 1 &&
                            Math.min(y0, y1) < n.y + n.h - 1 && Math.max(y0, y1) > n.y + 1;
                        assert.ok(!hit, (vertical ? 'vertical' : 'horizontal') + ': ' + e.from + ' -> ' + e.to + ' runs behind ' + n.id);
                    }
                }
            }
        }
    });
}
