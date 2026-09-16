// Pure decision logic for the console tab bar's fold rule and "/" landing
// project — split out from console.js so it can run (and be unit tested)
// without a DOM. Loaded as a plain global (ConsoleTabs) in the browser via
// <script src="/static/console-tabs.js">, and as a CommonJS module under
// Node for tests. See docs/plans/... console boot ticket for the behaviour
// this encodes: the tab fold rule and the "/" landing project fallback
// chain (localStorage, then most recent run, then alphabetical).
(function (root, factory) {
    'use strict';
    if (typeof module !== 'undefined' && module.exports) {
        module.exports = factory();
    } else {
        root.ConsoleTabs = factory();
    }
}(typeof self !== 'undefined' ? self : this, function () {
    'use strict';

    function sortedProjects(list) {
        return list.slice().sort(function (a, b) {
            if (a.label === b.label) return a.slug < b.slug ? -1 : (a.slug > b.slug ? 1 : 0);
            return a.label < b.label ? -1 : 1;
        });
    }

    function projectRecencyMs(p) {
        if (!p || !p.latest_run_at) return 0;
        var t = Date.parse(p.latest_run_at);
        return isNaN(t) ? 0 : t;
    }

    // Tab fold rule: always show the current project and anything with live
    // activity (running loop, active runs, or attention items), then fill
    // the rest of the width budget with the most recently active projects —
    // so a stopped loop with no current activity doesn't make every other
    // project vanish into "More" (16 quiet projects folding down to a
    // single visible tab). Only genuinely stale, inactive projects beyond
    // the budget fold.
    function computeTabPlan(projects, activeSlug, budget) {
        var forced = [];
        var candidates = [];
        projects.forEach(function (p) {
            var hasLiveActivity = p.loop_running || (p.active_count || 0) > 0 || (p.attention_count || 0) > 0;
            if (hasLiveActivity || p.slug === activeSlug) {
                forced.push(p);
            } else {
                candidates.push(p);
            }
        });
        candidates.sort(function (a, b) { return projectRecencyMs(b) - projectRecencyMs(a); });
        var remaining = Math.max(0, budget - forced.length);
        return {
            visible: forced.concat(candidates.slice(0, remaining)),
            folded: candidates.slice(remaining)
        };
    }

    // Most recently active project by latest run start; falls back to
    // alphabetical-by-label/slug when no project has ever run.
    function mostRecentSlug(projects) {
        var ranked = projects.slice().sort(function (a, b) { return projectRecencyMs(b) - projectRecencyMs(a); });
        if (ranked.length && projectRecencyMs(ranked[0]) > 0) return ranked[0].slug;
        return sortedProjects(projects)[0].slug;
    }

    // Landing project for a bare "/" visit: the last project the user
    // viewed (storedSlug, from localStorage) if it still exists among the
    // loaded projects; otherwise the project with the most recent run, else
    // alphabetical. When projects hasn't loaded yet (empty list — the
    // optimistic pre-fetch guess made at boot), falls back to storedSlug
    // verbatim (trusted without validation) and then serverDefaultSlug (the
    // server's own best-effort pick, rendered server-side into the page).
    function pickLandingSlug(storedSlug, projects, serverDefaultSlug) {
        if (!projects || !projects.length) {
            return storedSlug || serverDefaultSlug || '';
        }
        var storedValid = storedSlug && projects.some(function (p) { return p.slug === storedSlug; });
        if (storedValid) return storedSlug;
        return mostRecentSlug(projects);
    }

    // Orchestration entry points, shown first and in this order (ahead of a
    // separator) regardless of location, because they're the workflows a
    // user reaches for first rather than ones they browse alphabetically.
    var SPECIAL_WORKFLOW_NAMES = ['list-tasks', 'main'];

    // Order a location's workflow tabs: list-tasks then main (whichever are
    // present, in that order), then every other workflow sorted
    // case-insensitively by name. specialCount tells the caller how many
    // leading entries are "special" so it knows where to render the
    // separator bar (0 means no separator, since there's nothing to divide
    // from the rest).
    function orderWorkflowTabs(workflows) {
        var byName = {};
        workflows.forEach(function (wf) { byName[wf.name] = wf; });

        var special = SPECIAL_WORKFLOW_NAMES.filter(function (name) {
            return Object.prototype.hasOwnProperty.call(byName, name);
        }).map(function (name) { return byName[name]; });

        var specialNames = {};
        SPECIAL_WORKFLOW_NAMES.forEach(function (name) { specialNames[name] = true; });
        var rest = workflows.filter(function (wf) { return !specialNames[wf.name]; });
        rest.sort(function (a, b) { return a.name.localeCompare(b.name, undefined, { sensitivity: 'base' }); });

        return { ordered: special.concat(rest), specialCount: special.length };
    }

    // repoNamesForProject returns the repo names a project's console should
    // render sub-tabs for, or null for a legacy project (see docs/design/
    // console-repo-grouping-mock.html, option 1): no [[repositories]]
    // configured, or exactly one (the auto-seeded/explicit implicit
    // default) — either way there's nothing to disambiguate, so the
    // sub-tab row is omitted entirely rather than rendered with one entry.
    function repoNamesForProject(project) {
        var repos = (project && project.repositories) || [];
        if (repos.length <= 1) return null;
        return repos.map(function (r) { return r.name; });
    }

    // normalizeRepo maps a URL repo segment to its canonical form: "" means
    // "all repos". Anything not in names (including "all" itself, and any
    // unrecognized value) also normalizes to "" so a stale/bad repo segment
    // degrades to the merged view rather than a dead end.
    function normalizeRepo(name, names) {
        if (!name || name === 'all') return '';
        return names.indexOf(name) !== -1 ? name : '';
    }

    // resolveRepoAndTask disambiguates the path segments after a project
    // slug (e.g. "anarkana", "manager-fnn6", or "anarkana/manager-fnn6")
    // into a repo scope and a task ID, per the URL scheme in docs/design/
    // console-repo-grouping-mock.html, option 1:
    //   legacy (project === null, or repoNamesForProject returns null):
    //     rest[0], if present, is always the task ID — /{project}/{task}.
    //   multi-repo: rest[0] is the repo segment only when it exactly
    //     matches a configured repo name (or "all"); otherwise it's a
    //     legacy-shaped task-only link (/{project}/{task}, "all repos"
    //     scope) that must keep working. Two segments are always
    //     /{project}/{repo}/{task}.
    // project may be null (e.g. before the project list has loaded) — that
    // is treated the same as a legacy project, an optimistic guess the
    // caller should reconcile once the real project data (with
    // .repositories) is available.
    function resolveRepoAndTask(project, rest) {
        var names = repoNamesForProject(project);
        rest = rest || [];
        if (!names) {
            return { repo: '', taskId: rest[0] || '' };
        }
        if (rest.length >= 2) {
            return { repo: normalizeRepo(rest[0], names), taskId: rest[1] };
        }
        if (rest.length === 1) {
            if (rest[0] === 'all' || names.indexOf(rest[0]) !== -1) {
                return { repo: normalizeRepo(rest[0], names), taskId: '' };
            }
            return { repo: '', taskId: rest[0] };
        }
        return { repo: '', taskId: '' };
    }

    return {
        sortedProjects: sortedProjects,
        projectRecencyMs: projectRecencyMs,
        computeTabPlan: computeTabPlan,
        mostRecentSlug: mostRecentSlug,
        pickLandingSlug: pickLandingSlug,
        orderWorkflowTabs: orderWorkflowTabs,
        repoNamesForProject: repoNamesForProject,
        normalizeRepo: normalizeRepo,
        resolveRepoAndTask: resolveRepoAndTask
    };
}));
