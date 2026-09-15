// Unit tests for the pure tab-bar/landing-project logic in
// static/console-tabs.js. Kept outside static/ so it isn't picked up by the
// //go:embed static/* directive and served over HTTP.
// Run with: node --test internal/adapters/web/console-tabs.test.js
// No DOM/browser is involved — these functions never touch document/fetch.
'use strict';

const test = require('node:test');
const assert = require('node:assert/strict');
const ConsoleTabs = require('./static/console-tabs.js');

function project(overrides) {
    return Object.assign({
        dir: '/home/user/proj',
        label: 'proj',
        slug: 'proj',
        health: { status: 'green', passed: 1, failed: 0, total: 1 },
        active_count: 0,
        attention_count: 0,
        attention_computed_at: '',
        loop_running: false,
        latest_run_at: ''
    }, overrides);
}

test('computeTabPlan always keeps the current project visible', () => {
    const projects = [project({ slug: 'a', label: 'a' }), project({ slug: 'b', label: 'b' })];
    const plan = ConsoleTabs.computeTabPlan(projects, 'b', 1);
    const visibleSlugs = plan.visible.map((p) => p.slug);
    assert.ok(visibleSlugs.includes('b'), 'active project must stay visible even under a tight budget');
});

test('computeTabPlan keeps every project with live activity visible regardless of budget', () => {
    const projects = [
        project({ slug: 'current', label: 'current' }),
        project({ slug: 'running', label: 'running', active_count: 2 }),
        project({ slug: 'loopy', label: 'loopy', loop_running: true }),
        project({ slug: 'flagged', label: 'flagged', attention_count: 3 }),
        project({ slug: 'quiet1', label: 'quiet1' }),
        project({ slug: 'quiet2', label: 'quiet2' })
    ];
    const plan = ConsoleTabs.computeTabPlan(projects, 'current', 1);
    const visibleSlugs = plan.visible.map((p) => p.slug).sort();
    assert.deepEqual(visibleSlugs, ['current', 'flagged', 'loopy', 'running']);
});

test('computeTabPlan never folds every idle project to a single tab: it fills the budget with the most recent', () => {
    // Reproduces the reported bug: loop stopped, no active runs or
    // attention anywhere, 16 other idle projects besides the current one.
    const projects = [project({ slug: 'current', label: 'current', latest_run_at: '2026-01-01T00:00:00Z' })];
    for (let i = 0; i < 16; i++) {
        projects.push(project({
            slug: 'p' + i,
            label: 'p' + i,
            latest_run_at: new Date(Date.UTC(2026, 0, 2 + i)).toISOString()
        }));
    }
    const plan = ConsoleTabs.computeTabPlan(projects, 'current', 8);
    assert.equal(plan.visible.length, 8, 'budget of 8 should show 8 tabs, not collapse to 1');
    assert.equal(plan.folded.length, 9);
    // The 7 most recently active idle projects (p15..p9) fill out the
    // budget alongside "current"; older ones (p8..p0) fold into "More".
    const visibleSlugs = plan.visible.map((p) => p.slug);
    assert.ok(visibleSlugs.includes('p15'), 'most recently active idle project should stay visible');
    assert.ok(!visibleSlugs.includes('p0'), 'oldest idle project beyond the budget should fold');
});

test('computeTabPlan shows a stopped-loop project with no activity if it is among the most recent', () => {
    const projects = [
        project({ slug: 'current', label: 'current' }),
        project({ slug: 'recent-idle', label: 'recent-idle', latest_run_at: '2026-05-01T00:00:00Z' })
    ];
    const plan = ConsoleTabs.computeTabPlan(projects, 'current', 8);
    assert.ok(plan.visible.some((p) => p.slug === 'recent-idle'));
    assert.equal(plan.folded.length, 0);
});

test('pickLandingSlug prefers a still-valid stored (localStorage) slug over recency', () => {
    const projects = [
        project({ slug: 'old', label: 'old', latest_run_at: '2020-01-01T00:00:00Z' }),
        project({ slug: 'new', label: 'new', latest_run_at: '2026-01-01T00:00:00Z' })
    ];
    assert.equal(ConsoleTabs.pickLandingSlug('old', projects, 'new'), 'old');
});

test('pickLandingSlug falls back to the most recent run when stored slug is missing or stale', () => {
    const projects = [
        project({ slug: 'anarkana', label: 'anarkana', latest_run_at: '2020-01-01T00:00:00Z' }),
        project({ slug: 'active-proj', label: 'active-proj', latest_run_at: '2026-01-01T00:00:00Z' })
    ];
    assert.equal(ConsoleTabs.pickLandingSlug(null, projects, ''), 'active-proj');
    // A localStorage entry for a project that no longer exists is ignored.
    assert.equal(ConsoleTabs.pickLandingSlug('deleted-project', projects, ''), 'active-proj');
});

test('pickLandingSlug falls back to alphabetical when no project has ever run', () => {
    const projects = [
        project({ slug: 'zeta', label: 'zeta' }),
        project({ slug: 'anarkana', label: 'anarkana' })
    ];
    assert.equal(ConsoleTabs.pickLandingSlug(null, projects, ''), 'anarkana');
});

test('pickLandingSlug trusts the stored/server-default slug before the project list has loaded', () => {
    // This is the optimistic first-paint guess: no network round trip has
    // happened yet, so there is no project list to validate against.
    assert.equal(ConsoleTabs.pickLandingSlug('last-viewed', [], 'server-default'), 'last-viewed');
    assert.equal(ConsoleTabs.pickLandingSlug(null, [], 'server-default'), 'server-default');
    assert.equal(ConsoleTabs.pickLandingSlug(null, [], ''), '');
});

test('sortedProjects orders by label then slug', () => {
    const projects = [
        project({ slug: 'b', label: 'Bravo' }),
        project({ slug: 'a', label: 'Alpha' })
    ];
    assert.deepEqual(ConsoleTabs.sortedProjects(projects).map((p) => p.slug), ['a', 'b']);
});
