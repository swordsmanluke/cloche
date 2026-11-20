// Workflow graph layout + SVG rendering for the console's Workflows view.
// Pure (no DOM): layout(wf) places steps on a "happy path" spine with
// off-path steps (fix loops, abort) in lanes beside it; svg(wf, layout,
// vertical) draws it. The same layout is drawn left-to-right when it fits
// the container, otherwise top-to-bottom so long workflows scroll
// vertically. Loaded as a global (ConsoleDag) in the browser and as a
// CommonJS module under Node for tests.
(function (root, factory) {
    'use strict';
    if (typeof module !== 'undefined' && module.exports) {
        module.exports = factory();
    } else {
        root.ConsoleDag = factory();
    }
}(typeof self !== 'undefined' ? self : this, function () {
    'use strict';

    var TYPE_DESC = {
        agent: 'agent step: an LLM does the work',
        script: 'script step: a command decides',
        poll: 'poll step: waits for a person or a system',
        workflow: 'workflow step: runs another workflow'
    };

    function isSuccess(r) { return r === 'success' || r === 'ok' || r === 'done' || r === 'pass'; }

    // fail, failed, error, env-error, timeout, give-up ...: never the happy path.
    function isFailure(r) { return /^(fail|error|env-error|timeout|give-up|token-limit)/.test(r); }

    function esc(s) {
        return String(s).replace(/&/g, '&amp;').replace(/</g, '&lt;').replace(/>/g, '&gt;').replace(/"/g, '&quot;');
    }

    // layout assigns every node a (col, lane): lane 0 is the happy path
    // (start, entry ... last, done); lanes 1+ hold steps reached off it.
    function layout(wf) {
        var stepBy = {};
        wf.steps.forEach(function (s) { stepBy[s.name] = s; });
        var wires = (wf.wires || []).filter(function (w) { return !w.implicit; });

        // One edge per (from, to); its label joins all results wired between them.
        var edges = [], edgeBy = {}, adj = {};
        wires.forEach(function (w) {
            if (w.from === w.to) return; // shortcut: self-loops are not drawn, add a loop arc if one is needed
            var key = w.from + '\u0000' + w.to;
            var e = edgeBy[key];
            if (!e) {
                e = edgeBy[key] = { key: key, from: w.from, to: w.to, results: [] };
                edges.push(e);
                (adj[w.from] = adj[w.from] || []).push(e);
            }
            if (e.results.indexOf(w.result) < 0) e.results.push(w.result);
        });

        var entry = stepBy[wf.entry_step] ? wf.entry_step : null;
        if (!entry) {
            var hasIn = {};
            edges.forEach(function (e) { hasIn[e.to] = true; });
            var roots = wf.steps.filter(function (s) { return !hasIn[s.name]; });
            entry = (roots[0] || wf.steps[0]).name;
        }

        // Back edges (retry loops) are found by DFS and ignored when choosing the spine.
        var seen = {}, back = {};
        function dfs(n) {
            seen[n] = 1;
            (adj[n] || []).forEach(function (e) {
                if (!stepBy[e.to]) return;
                if (seen[e.to] === 1) back[e.key] = true;
                else if (!seen[e.to]) dfs(e.to);
            });
            seen[n] = 2;
        }
        dfs(entry);
        wf.steps.forEach(function (s) { if (!seen[s.name]) dfs(s.name); });

        // reach(n): length of the longest forward path from n to a natural end
        // (done, or a step with no forward exits besides abort), or -1.
        var memo = {}, lenient = !edges.some(function (e) { return e.to === 'done'; });
        function reach(n) {
            if (memo[n] !== undefined) return memo[n];
            memo[n] = -1;
            var best = -1, exits = 0;
            (adj[n] || []).forEach(function (e) {
                if (back[e.key] || e.to === 'abort') return;
                exits++;
                var d = e.to === 'done' ? 1 : (stepBy[e.to] && reach(e.to) > 0 ? reach(e.to) + 1 : -1);
                if (d > best) best = d;
            });
            // No exits at all is a natural end; with a done in the graph, a step that
            // only loops back is not one (without a done, it is the best end we have).
            if (!exits && (lenient || !(adj[n] || []).some(function (e) { return back[e.key]; }))) best = 1;
            memo[n] = best;
            return best;
        }
        function score(e) { return e.to === 'done' ? 1 : reach(e.to) + 1; }

        var spine = [entry], onSpine = {};
        onSpine[entry] = true;
        for (var cur = entry; ;) {
            var cands = (adj[cur] || []).filter(function (e) {
                return !back[e.key] && !onSpine[e.to] && !e.results.every(isFailure);
            });
            var viable = cands.filter(function (e) { return e.to === 'done' || (stepBy[e.to] && reach(e.to) > 0); });
            var pick;
            if (viable.length) {
                pick = viable.slice().sort(function (a, b) {
                    var sa = a.results.some(isSuccess) ? 1 : 0, sb = b.results.some(isSuccess) ? 1 : 0;
                    return sb - sa || score(b) - score(a);
                })[0];
            } else {
                pick = cands.filter(function (e) { return stepBy[e.to] && e.results.some(isSuccess); })[0];
            }
            if (!pick || pick.to === 'done') break;
            spine.push(pick.to);
            onSpine[pick.to] = true;
            cur = pick.to;
        }

        var nodes = {}, order = [], occ = {};
        function place(id, col, lane, extra) {
            var n = { id: id, col: col, lane: lane, step: stepBy[id] || null };
            for (var k in extra) n[k] = extra[k];
            nodes[id] = n;
            order.push(id);
            occ[lane + ',' + col] = true;
            return n;
        }
        place('\u0000start', 0, 0, { pill: 'task' });
        spine.forEach(function (n, i) { place(n, i + 1, 0); });
        var wiresToDone = edges.some(function (e) { return e.to === 'done'; });
        if (wiresToDone) place('done', spine.length + 1, 0, { pill: 'done' });

        // Off-spine nodes hang beside their first predecessor: steps one lane
        // out in the same column, terminals (abort) one column back.
        var STEP_OFFS = [0, 1, -1], TERM_OFFS = [-1, 1];
        function slotFor(lane, anchor, isTerm) {
            var offs = isTerm ? TERM_OFFS : STEP_OFFS;
            for (var d = 0; ; d++) {
                for (var j = 0; j < offs.length; j++) {
                    var o = offs[j] === 0 ? 0 : offs[j] * d;
                    if (offs[j] !== 0 && d === 0) continue;
                    if (offs[j] === 0 && d > 0) continue;
                    if (!occ[lane + ',' + (anchor + o)]) return anchor + o;
                }
            }
        }
        var queue = order.slice();
        while (queue.length) {
            var p = nodes[queue.shift()];
            (adj[p.id] || []).forEach(function (e) {
                if (nodes[e.to]) return;
                var term = !stepBy[e.to];
                var lane = term ? Math.max(1, p.lane) : p.lane + 1;
                var col = slotFor(lane, p.col, term);
                place(e.to, col, lane, term ? { pill: e.to } : {});
                queue.push(e.to);
            });
        }
        wf.steps.forEach(function (s) {
            if (!nodes[s.name]) place(s.name, slotFor(1, 1, false), 1);
        });

        var all = [{ from: '\u0000start', to: entry, results: [] }].concat(edges);
        all = all.filter(function (e) { return nodes[e.from] && nodes[e.to]; });

        var minCol = Infinity, maxCol = -Infinity, maxLane = 0;
        order.forEach(function (id) {
            var n = nodes[id];
            if (n.col < minCol) minCol = n.col;
            if (n.col > maxCol) maxCol = n.col;
            if (n.lane > maxLane) maxLane = n.lane;
        });
        order.forEach(function (id) { nodes[id].col -= minCol; });

        var types = [];
        wf.steps.forEach(function (s) { if (types.indexOf(s.type) < 0) types.push(s.type); });
        return { nodes: nodes, order: order, edges: all, cols: maxCol - minCol + 1, lanes: maxLane + 1, types: types, wfName: wf.name };
    }

    var STEP_H = 54, PILL_W = 84, PILL_H = 34, PAD = 24;

    // svg draws a layout; returns { svg, w, h } (pixel size of the drawing).
    function svg(L, vertical) {
        var maxName = 0;
        L.order.forEach(function (id) {
            var n = L.nodes[id];
            if (n.step && n.step.name.length > maxName) maxName = n.step.name.length;
        });
        var stepW = Math.max(120, maxName * 8 + 36);

        function label(e) { return e.results.join(', '); }
        var labelLen = 0;
        L.edges.forEach(function (e) {
            var a = L.nodes[e.from], b = L.nodes[e.to];
            if (!vertical && !(a.lane === b.lane && Math.abs(a.col - b.col) === 1)) return;
            if (label(e).length > labelLen) labelLen = label(e).length;
        });
        var gap = Math.min(170, Math.max(72, labelLen * 6.6 + 24));

        // Abstract axes: m runs along the flow, c across it.
        var mPitch = vertical ? STEP_H + 56 : stepW + gap;
        var cPitch = vertical ? stepW + gap : STEP_H + 64;

        var info = {};
        L.order.forEach(function (id) {
            var n = L.nodes[id];
            var w = n.pill ? PILL_W : stepW, h = n.pill ? PILL_H : STEP_H;
            var maxM = vertical ? STEP_H : stepW, maxC = vertical ? stepW : STEP_H;
            info[id] = {
                n: n, w: w, h: h,
                m: PAD + n.col * mPitch + maxM / 2,
                c: PAD + n.lane * cPitch + maxC / 2,
                hm: (vertical ? h : w) / 2,
                hc: (vertical ? w : h) / 2
            };
        });

        var maxX = 0, maxY = 0;
        function grow(x, y) { if (x > maxX) maxX = x; if (y > maxY) maxY = y; }
        function pt(m, c) { return vertical ? [c, m] : [m, c]; }

        var has = {};
        L.edges.forEach(function (e) { has[e.from + '\u0000' + e.to] = true; });

        var obstacles = L.order.map(function (id) {
            var i = info[id], c = pt(i.m, i.c);
            return [c[0] - i.w / 2 - 2, c[1] - i.h / 2 - 2, c[0] + i.w / 2 + 2, c[1] + i.h / 2 + 2];
        });
        var detours = 0, out = '';
        L.edges.forEach(function (e) {
            var a = info[e.from], b = info[e.to];
            var dl = b.n.lane - a.n.lane, dc = b.n.col - a.n.col;
            var paired = has[e.to + '\u0000' + e.from];
            var pts, sign = 0;
            if (dl === 0 && Math.abs(dc) === 1) {
                sign = paired ? (dc > 0 ? -1 : 1) : 0;
                var o = sign * 10, s = dc > 0 ? 1 : -1;
                pts = [[a.m + s * a.hm, a.c + o], [b.m - s * b.hm, b.c + o]];
            } else if (dl === 0) {
                var cd = a.c + a.hc + 18 + 10 * detours++;
                pts = [[a.m, a.c + a.hc], [a.m, cd], [b.m, cd], [b.m, b.c + b.hc]];
            } else if (dc === 0) {
                sign = paired ? (dl > 0 ? -1 : 1) : 0;
                var o2 = sign * 10, s2 = dl > 0 ? 1 : -1;
                pts = [[a.m + o2, a.c + s2 * a.hc], [b.m + o2, b.c - s2 * b.hc]];
            } else if (dl > 0) {
                pts = [[a.m, a.c + a.hc], [a.m, b.c], [b.m + (dc < 0 ? b.hm : -b.hm), b.c]];
            } else {
                pts = [[a.m + (dc > 0 ? a.hm : -a.hm), a.c], [b.m, a.c], [b.m, b.c + b.hc]];
            }
            var sp = pts.map(function (p) { return pt(p[0], p[1]); });
            sp.forEach(function (p) { grow(p[0], p[1]); });
            out += '<path class="dag-edge" marker-end="url(#dag-arrow)" d="' +
                sp.map(function (p, i) { return (i ? 'L' : 'M') + p[0] + ' ' + p[1]; }).join(' ') + '"/>';

            var text = label(e);
            if (!text) return;
            var tw = text.length * 6.6;
            // Candidate spots: every segment, both sides. Prefer the segment next
            // to the source (so edges sharing a trunk stay readable), then the
            // longest; take the first spot that hits no node or earlier label.
            var segLen = function (i) { return Math.abs(sp[i + 1][0] - sp[i][0]) + Math.abs(sp[i + 1][1] - sp[i][1]); };
            var idx = sp.slice(1).map(function (_, i) { return i; }).filter(function (i) { return segLen(i) >= 20; });
            idx.sort(function (x, y) {
                var fx = x === 0 && segLen(0) >= tw + 12 ? 1 : 0, fy = y === 0 && segLen(0) >= tw + 12 ? 1 : 0;
                return fy - fx || segLen(y) - segLen(x);
            });
            var spots = [];
            idx.forEach(function (i) {
                var p0 = sp[i], p1 = sp[i + 1];
                var mx = (p0[0] + p1[0]) / 2, my = (p0[1] + p1[1]) / 2;
                // default: above a horizontal line, right of a vertical one
                var horiz = p0[1] === p1[1];
                var sides = sign > 0 ? [1, -1] : (sign < 0 ? [-1, 1] : (horiz ? [-1, 1] : [1, -1]));
                sides.forEach(function (sd) {
                    if (p0[1] === p1[1]) {
                        var y = sd > 0 ? my + 16 : my - 6;
                        spots.push({ x: mx, y: y, anchor: 'middle', box: [mx - tw / 2, y - 11, mx + tw / 2, y + 3] });
                    } else {
                        var x = sd > 0 ? mx + 6 : mx - 6;
                        spots.push({ x: x, y: my + 4, anchor: sd > 0 ? 'start' : 'end', box: sd > 0 ? [x, my - 7, x + tw, my + 5] : [x - tw, my - 7, x, my + 5] });
                    }
                });
            });
            if (!spots.length) return;
            var hit = function (a, b) { return a[0] < b[2] && b[0] < a[2] && a[1] < b[3] && b[1] < a[3]; };
            var spot = spots.filter(function (c) {
                return c.box[0] >= 2 && c.box[1] >= 2 && !obstacles.some(function (o) { return hit(c.box, o); });
            })[0] || spots[0];
            obstacles.push(spot.box);
            grow(spot.box[2], spot.box[3]);
            out += '<text class="dag-edge-label" text-anchor="' + spot.anchor + '" x="' + spot.x + '" y="' + spot.y + '">' + esc(text) + '</text>';
        });

        L.order.forEach(function (id) {
            var i = info[id];
            var c = pt(i.m, i.c);
            var x = c[0] - i.w / 2, y = c[1] - i.h / 2;
            grow(x + i.w, y + i.h);
            if (i.n.pill) {
                out += '<g class="dag-pill dag-pill-' + esc(i.n.pill) + '"><rect class="dag-node-rect" rx="' + i.h / 2 + '" x="' + x + '" y="' + y + '" width="' + i.w + '" height="' + i.h + '"/>' +
                    '<text class="dag-node-text" text-anchor="middle" x="' + c[0] + '" y="' + (c[1] + 4) + '">' + esc(i.n.pill) + '</text></g>';
            } else {
                var s = i.n.step;
                out += '<g class="dag-node" data-workflow="' + esc(L.wfName) + '" data-step="' + esc(s.name) + '">' +
                    '<rect class="dag-node-rect dag-type-' + esc(s.type) + '" rx="8" x="' + x + '" y="' + y + '" width="' + i.w + '" height="' + i.h + '"/>' +
                    '<text class="dag-node-text" text-anchor="middle" x="' + c[0] + '" y="' + (c[1] - 3) + '">' + esc(s.name) + '</text>' +
                    '<text class="dag-node-sub" text-anchor="middle" x="' + c[0] + '" y="' + (c[1] + 14) + '">' + esc(s.type) + '</text></g>';
            }
        });

        var w = maxX + PAD, h = maxY + PAD;
        var head = '<svg class="dag-svg" width="' + w + '" height="' + h + '"><defs>' +
            '<marker id="dag-arrow" markerWidth="8" markerHeight="6" refX="8" refY="3" orient="auto"><polygon class="dag-arrow" points="0 0, 8 3, 0 6"/></marker></defs>';
        return { svg: head + out + '</svg>', w: w, h: h };
    }

    function legend(L) {
        var items = L.types.filter(function (t) { return TYPE_DESC[t]; }).map(function (t) {
            return '<span class="dag-legend-item"><span class="dag-swatch dag-type-' + esc(t) + '"></span>' + esc(TYPE_DESC[t]) + '</span>';
        });
        return items.length ? '<div class="dag-legend">' + items.join('') + '</div>' : '';
    }

    // render picks the orientation: horizontal when it fits `availWidth`
    // (0/unknown counts as fitting), else vertical. Returns the html.
    function render(wf, availWidth) {
        var L = layout(wf);
        var r = svg(L, false);
        var vertical = availWidth > 0 && r.w > availWidth;
        if (vertical) r = svg(L, true);
        return { html: r.svg + legend(L), vertical: vertical };
    }

    return { layout: layout, svg: svg, render: render };
}));
