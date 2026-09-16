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

function workflow(name, location) {
    return { name: name, location: location || 'container' };
}

test('orderWorkflowTabs pins list-tasks then main ahead of the rest, sorted case-insensitively (container location)', () => {
    const workflows = [
        workflow('Zebra', 'container'),
        workflow('main', 'container'),
        workflow('apple', 'container'),
        workflow('list-tasks', 'container'),
        workflow('Banana', 'container')
    ];
    const result = ConsoleTabs.orderWorkflowTabs(workflows);
    assert.deepEqual(result.ordered.map((w) => w.name), ['list-tasks', 'main', 'apple', 'Banana', 'Zebra']);
    assert.equal(result.specialCount, 2);
});

test('orderWorkflowTabs pins list-tasks then main ahead of the rest, sorted case-insensitively (host location)', () => {
    const workflows = [
        workflow('release', 'host'),
        workflow('Changelog', 'host'),
        workflow('main', 'host'),
        workflow('list-tasks', 'host')
    ];
    const result = ConsoleTabs.orderWorkflowTabs(workflows);
    assert.deepEqual(result.ordered.map((w) => w.name), ['list-tasks', 'main', 'Changelog', 'release']);
    assert.equal(result.specialCount, 2);
});

test('orderWorkflowTabs only includes whichever of list-tasks/main are actually present, in that order', () => {
    const withMainOnly = ConsoleTabs.orderWorkflowTabs([workflow('zeta'), workflow('main'), workflow('alpha')]);
    assert.deepEqual(withMainOnly.ordered.map((w) => w.name), ['main', 'alpha', 'zeta']);
    assert.equal(withMainOnly.specialCount, 1);

    const withNeither = ConsoleTabs.orderWorkflowTabs([workflow('zeta'), workflow('alpha')]);
    assert.deepEqual(withNeither.ordered.map((w) => w.name), ['alpha', 'zeta']);
    assert.equal(withNeither.specialCount, 0);
});

// ---------- repo grouping (docs/design/console-repo-grouping-mock.html, option 1) ----------

function multiRepoProject() {
    return project({
        slug: 'manager',
        repositories: [{ name: 'manager', path: '.' }, { name: 'anarkana', path: './repos/anarkana' }]
    });
}

test('repoNamesForProject returns null for a project with no repositories', () => {
    assert.equal(ConsoleTabs.repoNamesForProject(project()), null);
});

test('repoNamesForProject returns null for a project with exactly one repository (implicit default)', () => {
    assert.equal(ConsoleTabs.repoNamesForProject(project({ repositories: [{ name: 'only', path: '.' }] })), null);
});

test('repoNamesForProject returns null for a null project (not yet loaded)', () => {
    assert.equal(ConsoleTabs.repoNamesForProject(null), null);
});

test('repoNamesForProject returns every repo name for a multi-repo project', () => {
    assert.deepEqual(ConsoleTabs.repoNamesForProject(multiRepoProject()), ['manager', 'anarkana']);
});

test('normalizeRepo maps "all" and unknown names to "" (all repos)', () => {
    const names = ['manager', 'anarkana'];
    assert.equal(ConsoleTabs.normalizeRepo('all', names), '');
    assert.equal(ConsoleTabs.normalizeRepo('', names), '');
    assert.equal(ConsoleTabs.normalizeRepo('nonexistent', names), '');
    assert.equal(ConsoleTabs.normalizeRepo('anarkana', names), 'anarkana');
});

test('resolveRepoAndTask on a legacy project always treats the first segment as a task ID', () => {
    assert.deepEqual(ConsoleTabs.resolveRepoAndTask(project(), []), { repo: '', taskId: '' });
    assert.deepEqual(ConsoleTabs.resolveRepoAndTask(project(), ['task-123']), { repo: '', taskId: 'task-123' });
    // Even a segment that happens to be named like a repo has no repo list
    // to match against, so it stays a task ID on a legacy project.
    assert.deepEqual(ConsoleTabs.resolveRepoAndTask(project(), ['anarkana']), { repo: '', taskId: 'anarkana' });
});

test('resolveRepoAndTask on a null project (not yet loaded) is treated as legacy', () => {
    assert.deepEqual(ConsoleTabs.resolveRepoAndTask(null, ['manager-fnn6']), { repo: '', taskId: 'manager-fnn6' });
});

test('resolveRepoAndTask on a multi-repo project: bare project URL is "all repos", no task', () => {
    assert.deepEqual(ConsoleTabs.resolveRepoAndTask(multiRepoProject(), []), { repo: '', taskId: '' });
});

test('resolveRepoAndTask on a multi-repo project: one segment matching a repo name scopes the stack, no task', () => {
    assert.deepEqual(ConsoleTabs.resolveRepoAndTask(multiRepoProject(), ['anarkana']), { repo: 'anarkana', taskId: '' });
    assert.deepEqual(ConsoleTabs.resolveRepoAndTask(multiRepoProject(), ['all']), { repo: '', taskId: '' });
});

test('resolveRepoAndTask on a multi-repo project: one segment not matching any repo name is a legacy task-only link', () => {
    assert.deepEqual(ConsoleTabs.resolveRepoAndTask(multiRepoProject(), ['manager-fnn6']), { repo: '', taskId: 'manager-fnn6' });
});

test('resolveRepoAndTask on a multi-repo project: two segments are always /{repo}/{task}', () => {
    assert.deepEqual(ConsoleTabs.resolveRepoAndTask(multiRepoProject(), ['anarkana', 'manager-fnn6']), { repo: 'anarkana', taskId: 'manager-fnn6' });
    // An invalid repo segment normalizes to "all" rather than rejecting the link.
    assert.deepEqual(ConsoleTabs.resolveRepoAndTask(multiRepoProject(), ['bogus', 'manager-fnn6']), { repo: '', taskId: 'manager-fnn6' });
});
