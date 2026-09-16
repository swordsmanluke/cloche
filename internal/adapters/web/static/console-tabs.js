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

    return {
        sortedProjects: sortedProjects,
        projectRecencyMs: projectRecencyMs,
        computeTabPlan: computeTabPlan,
        mostRecentSlug: mostRecentSlug,
        pickLandingSlug: pickLandingSlug,
        orderWorkflowTabs: orderWorkflowTabs
    };
}));
