(function () {
    'use strict';

    var root = document.getElementById('console-app');
    if (!root) return;

    var GROUPS = [
        { key: 'needs_you', field: 'needs_you', label: 'Needs you' },
        { key: 'running', field: 'running', label: 'Running' },
        { key: 'queued', field: 'queued', label: 'Queued' },
        { key: 'done', field: 'done', label: 'Done' }
    ];

    var STACK_POLL_MS = 4000;
    var INSTRUMENTS_POLL_MS = 5000;
    var TICKER_POLL_MS = 5000;
    // How many recent activity entries the foot ticker packs into its one
    // ellipsised line (see renderTicker) — enough to read as a feed, not so
    // many the request/DOM cost is noticeable.
    var TICKER_ENTRY_LIMIT = 8;
    // How often the tab bar's project list (health/active/loop/attention) is
    // refreshed in the background. Cheaper than the stack poll since it
    // covers every project, not just the active one.
    var PROJECTS_POLL_MS = 15000;
    // How many project tabs the fold rule keeps visible before spilling into
    // "More" — see computeTabPlan(). Not a true pixel-width measurement, but
    // a fixed budget large enough that a quiet loop never folds every
    // project down to a single visible tab.
    var TAB_VISIBLE_BUDGET = 8;
    // Matches internal/attention.DefaultRefreshInterval — attention data
    // older than this is flagged stale in the tab bar rather than presented
    // as current.
    var ATTENTION_STALE_MS = 60000;
    var LAST_PROJECT_STORAGE_KEY = 'cloche:lastProjectSlug';
    // Matches web.SystemProjectSlug — the synthetic tab grouping tasks/runs
    // with no owning project. It has no orchestration loop, so loop controls
    // are disabled rather than wired up to start/stop.
    var SYSTEM_PROJECT_SLUG = 'system';

    var state = {
        projects: [],
        projectsStatus: 'loading', // 'loading' | 'ready' | 'error'
        projectsTimer: null,
        activeSlug: '',
        activeTaskId: '',
        // activeRepo is a RepositoryConfig.Name, or '' for "all repos" (the
        // default, and the only value a legacy/single-repo project ever
        // has) — see docs/design/console-repo-grouping-mock.html, option 1.
        activeRepo: '',
        stack: null,          // last first-page snapshot from the server
        stackEtag: null,
        extraDone: [],         // accumulated "earlier" pages fetched via cursor
        doneCursor: null,
        rowEls: {},            // key -> row element
        rowOrder: [],          // ordered list of keys, document order
        selectedIndex: -1,
        doneCollapsed: loadStoredDoneCollapsed(),
        loopRunning: false,
        stackTimer: null,
        instrumentsTimer: null,
        tickerTimer: null,
        activity: {
            open: false,
            scope: 'project',   // 'project' | 'all'
            failuresOnly: false,
            entries: [],
            cursor: null
        }
    };

    // Active task-detail state (centre pane), or null when nothing is open.
    // See renderCentrePane / stopDetail.
    var detail = null;

    // ---------- routing ----------

    // parseLocation splits the path into a slug and up to two further
    // segments ("rest") — legacy /{project}/{task}, or multi-repo
    // /{project}[/{repo}]/{task}. Which interpretation applies depends on
    // the project's configured repo names, so disambiguating rest is left
    // to ConsoleTabs.resolveRepoAndTask (see docs/design/
    // console-repo-grouping-mock.html, option 1) rather than done here.
    function parseLocation() {
        var segments = location.pathname.replace(/^\/+|\/+$/g, '').split('/').filter(Boolean).map(decodeURIComponent);
        var params = new URLSearchParams(location.search);
        return {
            slug: segments[0] || '',
            rest: segments.slice(1),
            attempt: params.get('attempt') || '',
            step: params.get('step') || ''
        };
    }

    function pushLocation(slug, repo, taskId) {
        var path = '/' + encodeURIComponent(slug);
        if (repo) path += '/' + encodeURIComponent(repo);
        if (taskId) path += '/' + encodeURIComponent(taskId);
        if (path !== location.pathname) {
            history.pushState({ slug: slug, repo: repo, taskId: taskId }, '', path + location.search);
        }
    }

    function findProject(slug) {
        var match = null;
        state.projects.forEach(function (p) { if (p.slug === slug) match = p; });
        return match;
    }

    window.addEventListener('popstate', function () {
        var loc = parseLocation();
        var resolved = ConsoleTabs.resolveRepoAndTask(findProject(loc.slug), loc.rest);
        selectProject(loc.slug, { repo: resolved.repo, taskId: resolved.taskId, pushHistory: false });
    });

    // ---------- tab bar ----------

    // Fetches the tab bar's project list. Never gates first paint: callers
    // render a skeleton via renderTabBar() before this resolves, and this
    // patches in real data (including attention counts, which ride along on
    // the same cached-and-fast /api/projects response) whenever it lands.
    function loadProjects() {
        return fetch('/api/projects').then(function (r) {
            if (!r.ok) throw new Error('bad status ' + r.status);
            return r.json();
        }).then(function (projects) {
            state.projects = projects || [];
            state.projectsStatus = 'ready';
            renderTabBar();
            // The sub-tab row (and each row's "all repos" repo tag) needs
            // .repositories from this same response, which can resolve
            // after the stack already painted without it — refresh both
            // once real project data is in, rather than waiting for the
            // next stack poll.
            renderSubtabs();
            if (state.stack) renderMergedStack(false);
        }).catch(function () {
            state.projectsStatus = 'error';
            renderTabBar();
        });
    }

    function startProjectsPolling() {
        stopProjectsPolling();
        state.projectsTimer = setInterval(loadProjects, PROJECTS_POLL_MS);
    }

    function stopProjectsPolling() {
        if (state.projectsTimer) {
            clearInterval(state.projectsTimer);
            state.projectsTimer = null;
        }
    }

    // Fold-rule/landing-project decision logic lives in console-tabs.js
    // (loaded before this script — see console.html) so it can be unit
    // tested without a DOM. sortedProjects is used pervasively below, so
    // keep a local alias.
    var sortedProjects = ConsoleTabs.sortedProjects;

    function renderTabBarSkeleton() {
        var tabsEl = document.getElementById('console-project-tabs');
        var moreBtn = document.getElementById('console-more-btn');
        var menuEl = document.getElementById('console-idle-menu');
        tabsEl.innerHTML = '';
        menuEl.innerHTML = '';
        moreBtn.hidden = true;
        menuEl.hidden = true;
        for (var i = 0; i < 3; i++) {
            var sk = document.createElement('span');
            sk.className = 'console-tab-skeleton';
            sk.setAttribute('aria-hidden', 'true');
            tabsEl.appendChild(sk);
        }
    }

    function renderTabBar() {
        var tabsEl = document.getElementById('console-project-tabs');
        var moreBtn = document.getElementById('console-more-btn');
        var menuEl = document.getElementById('console-idle-menu');

        if (!state.projects.length && state.projectsStatus === 'loading') {
            renderTabBarSkeleton();
            renderStalenessHint([]);
            return;
        }

        tabsEl.innerHTML = '';
        menuEl.innerHTML = '';

        if (!state.projects.length) {
            var none = document.createElement('span');
            none.className = 'console-tab-count';
            none.textContent = state.projectsStatus === 'error' ? 'Could not load projects' : 'No projects registered';
            tabsEl.appendChild(none);
            moreBtn.hidden = true;
            menuEl.hidden = true;
            renderStalenessHint([]);
            return;
        }

        var plan = ConsoleTabs.computeTabPlan(state.projects, state.activeSlug, TAB_VISIBLE_BUDGET);

        sortedProjects(plan.visible).forEach(function (p) { tabsEl.appendChild(renderTab(p, false)); });

        if (plan.folded.length) {
            moreBtn.hidden = false;
            moreBtn.textContent = '+ ' + plan.folded.length + ' idle ▾';
            sortedProjects(plan.folded).forEach(function (p) { menuEl.appendChild(renderTab(p, true)); });
        } else {
            moreBtn.hidden = true;
            menuEl.hidden = true;
        }

        renderStalenessHint(state.projects);
    }

    // Flags the tab bar when the cached attention data behind attention_count
    // is older than the cache's own refresh interval — a stuck/slow refresher
    // rather than genuinely fresh "zero items".
    function renderStalenessHint(projects) {
        var el = document.getElementById('console-attention-stale');
        if (!el) return;
        var oldest = null;
        projects.forEach(function (p) {
            if (!p.attention_computed_at) return;
            var t = Date.parse(p.attention_computed_at);
            if (isNaN(t)) return;
            if (oldest === null || t < oldest) oldest = t;
        });
        if (oldest === null || (Date.now() - oldest) <= ATTENTION_STALE_MS) {
            el.hidden = true;
            return;
        }
        var ageSeconds = Math.round((Date.now() - oldest) / 1000);
        el.hidden = false;
        el.textContent = 'attention data stale (' + formatDuration(ageSeconds) + ' old)';
        el.title = 'The attention cache has not refreshed in over ' + formatDuration(Math.round(ATTENTION_STALE_MS / 1000));
    }

    function renderTab(p, inMenu) {
        var btn = document.createElement('button');
        btn.type = 'button';
        btn.className = 'console-tab' + (p.slug === state.activeSlug ? ' console-tab-active' : '');

        var dot = document.createElement('span');
        dot.className = 'health-dot health-' + ((p.health && p.health.status) || 'grey');
        btn.appendChild(dot);

        var label = document.createElement('span');
        label.textContent = p.label;
        btn.appendChild(label);

        if (p.active_count || p.attention_count) {
            var meta = document.createElement('span');
            meta.className = 'console-tab-meta';

            if (p.active_count) {
                var count = document.createElement('span');
                count.className = 'console-tab-count';
                count.textContent = p.active_count + ' ▸';
                meta.appendChild(count);
            }

            if (p.active_count && p.attention_count) {
                meta.appendChild(document.createTextNode(' · '));
            }

            if (p.attention_count) {
                var flag = document.createElement('span');
                flag.className = 'console-tab-flag';
                flag.title = p.attention_count + ' need' + (p.attention_count === 1 ? 's' : '') + ' you';
                flag.textContent = p.attention_count + ' ⚑';
                meta.appendChild(flag);
            }

            btn.appendChild(meta);
        }

        btn.addEventListener('click', function () {
            closeIdleMenu();
            selectProject(p.slug, { pushHistory: true });
        });

        return btn;
    }

    function closeIdleMenu() {
        var menu = document.getElementById('console-idle-menu');
        var btn = document.getElementById('console-more-btn');
        if (!menu.hidden) {
            menu.hidden = true;
            btn.setAttribute('aria-expanded', 'false');
        }
    }

    document.getElementById('console-more-btn').addEventListener('click', function (e) {
        e.stopPropagation();
        closeViewsMenu();
        var menu = document.getElementById('console-idle-menu');
        var willOpen = menu.hidden;
        menu.hidden = !willOpen;
        this.setAttribute('aria-expanded', String(willOpen));
    });
    document.addEventListener('click', closeIdleMenu);

    // ---------- density ----------

    // Persists the compact/comfortable choice across sessions (see
    // toggleDensity and the 'd' key binding below); every read and write is
    // wrapped since localStorage can throw (private browsing, disabled
    // storage) rather than just being absent. Falls back to 'comfortable'
    // whenever storage is unavailable or holds something unexpected.
    var DENSITY_STORAGE_KEY = 'cloche:density';

    function loadStoredDensity() {
        try {
            var v = localStorage.getItem(DENSITY_STORAGE_KEY);
            if (v === 'comfortable' || v === 'compact') return v;
        } catch (e) { /* storage unavailable (private mode, disabled, etc.) */ }
        return 'comfortable';
    }

    function saveDensity(value) {
        try {
            localStorage.setItem(DENSITY_STORAGE_KEY, value);
        } catch (e) { /* storage unavailable (private mode, disabled, etc.) */ }
    }

    // applyDensity only ever sets the data-density attribute and the
    // toggle button's own label/state — every visual difference between
    // the two modes lives in CSS tokens (see style.css), not here.
    function applyDensity(value) {
        root.setAttribute('data-density', value);
        var btn = document.getElementById('console-density-toggle');
        if (btn) {
            btn.textContent = value === 'compact' ? 'Compact' : 'Comfortable';
            btn.setAttribute('aria-pressed', String(value === 'compact'));
        }
    }

    function toggleDensity() {
        var next = root.getAttribute('data-density') === 'compact' ? 'comfortable' : 'compact';
        applyDensity(next);
        saveDensity(next);
    }

    applyDensity(loadStoredDensity());
    var densityToggleBtn = document.getElementById('console-density-toggle');
    if (densityToggleBtn) densityToggleBtn.addEventListener('click', toggleDensity);

    // ---------- done-group collapse ----------

    // Persists whether the Done stack group is collapsed, mirroring the
    // density storage pattern above (try/catch around every read and write,
    // since localStorage can throw). Defaults to expanded.
    var DONE_COLLAPSED_STORAGE_KEY = 'console.stack.done.collapsed';

    function loadStoredDoneCollapsed() {
        try {
            return localStorage.getItem(DONE_COLLAPSED_STORAGE_KEY) === '1';
        } catch (e) { /* storage unavailable (private mode, disabled, etc.) */ }
        return false;
    }

    function saveDoneCollapsed(value) {
        try {
            localStorage.setItem(DONE_COLLAPSED_STORAGE_KEY, value ? '1' : '0');
        } catch (e) { /* storage unavailable (private mode, disabled, etc.) */ }
    }

    // ---------- project selection ----------

    // Persists the last project the user viewed so a bare "/" visit can land
    // there next time (see resolveLandingSlug/reconcileLandingProject in init()).
    function rememberLastProject(slug) {
        try {
            localStorage.setItem(LAST_PROJECT_STORAGE_KEY, slug);
        } catch (e) { /* storage unavailable (private mode, disabled, etc.) */ }
    }

    function selectProject(slug, opts) {
        opts = opts || {};
        if (!slug) return;

        state.activeSlug = slug;
        state.activeRepo = opts.repo || '';
        state.activeTaskId = opts.taskId || '';
        state.stack = null;
        state.stackEtag = null;
        state.extraDone = [];
        state.doneCursor = null;
        state.rowEls = {};
        state.rowOrder = [];
        state.selectedIndex = -1;

        rememberLastProject(slug);

        closeView();
        scanStatus = null;

        stopStackPolling();
        stopInstrumentsPolling();
        stopTickerPolling();
        renderTabBar();
        renderSubtabs();
        renderStackSkeleton();
        renderCentrePane(null, null);
        updateViewButtonsEnabled();

        loadInstruments();
        startInstrumentsPolling();

        loadTicker();
        startTickerPolling();

        loadStack(true).then(function () {
            startStackPolling();
            renderSubtabs();
            if (state.activeTaskId) selectTaskById(state.activeTaskId);
        });

        if (state.activity.open && state.activity.scope === 'project') loadActivityStream(true);

        if (opts.pushHistory) pushLocation(slug, state.activeRepo, state.activeTaskId);
    }

    function switchProject(delta) {
        var slugs = sortedProjects(state.projects).map(function (p) { return p.slug; });
        if (!slugs.length) return;
        var idx = slugs.indexOf(state.activeSlug);
        if (idx === -1) idx = 0;
        idx = (idx + delta + slugs.length) % slugs.length;
        selectProject(slugs[idx], { pushHistory: true });
    }

    // ---------- repo sub-tabs (docs/design/console-repo-grouping-mock.html, option 1) ----------

    function currentProject() {
        return findProject(state.activeSlug);
    }

    // selectRepo scopes the stack to repo ('' = all repos), reloading it
    // from scratch (the previous scope's data can't be filtered client-side
    // — see the ?repo= server-side aggregation) while leaving the rest of
    // the project (instruments, ticker, tab bar) alone.
    function selectRepo(repo, opts) {
        opts = opts || {};
        if (repo === state.activeRepo) {
            if (opts.pushHistory) pushLocation(state.activeSlug, repo, state.activeTaskId);
            return;
        }
        state.activeRepo = repo;
        state.activeTaskId = opts.taskId || '';
        state.stack = null;
        state.stackEtag = null;
        state.extraDone = [];
        state.doneCursor = null;
        state.rowEls = {};
        state.rowOrder = [];
        state.selectedIndex = -1;

        renderSubtabs();
        renderStackSkeleton();
        renderCentrePane(null, null);

        loadStack(true).then(function () {
            renderSubtabs();
            if (state.activeTaskId) selectTaskById(state.activeTaskId);
        });

        if (opts.pushHistory) pushLocation(state.activeSlug, repo, state.activeTaskId);
    }

    // switchRepo cycles the repo sub-tabs ("all repos" plus every
    // configured repo, in that order); a no-op on a legacy project (no
    // sub-tab row) since ConsoleTabs.repoNamesForProject returns null.
    function switchRepo(delta) {
        var names = ConsoleTabs.repoNamesForProject(currentProject());
        if (!names) return;
        var order = [''].concat(names);
        var idx = order.indexOf(state.activeRepo);
        if (idx === -1) idx = 0;
        idx = (idx + delta + order.length) % order.length;
        selectRepo(order[idx], { pushHistory: true });
    }

    function renderSubtabs() {
        var row = document.getElementById('console-subtabs');
        if (!row) return;
        var names = ConsoleTabs.repoNamesForProject(currentProject());
        if (!names) {
            row.hidden = true;
            row.innerHTML = '';
            return;
        }
        row.hidden = false;
        row.innerHTML = '';

        var lbl = document.createElement('span');
        lbl.className = 'console-subtabs-label';
        lbl.textContent = 'repo';
        row.appendChild(lbl);

        var counts = (state.stack && state.stack.repo_counts) || {};

        function makeSubtab(repoName, label) {
            var btn = document.createElement('button');
            btn.type = 'button';
            btn.className = 'console-subtab' + (state.activeRepo === repoName ? ' console-subtab-active' : '');

            var text = document.createElement('span');
            text.textContent = label;
            btn.appendChild(text);

            var c = repoName === ''
                ? Object.keys(counts).reduce(function (acc, key) {
                    var rc = counts[key] || {};
                    acc.running += rc.running || 0;
                    acc.needs_you += rc.needs_you || 0;
                    return acc;
                }, { running: 0, needs_you: 0 })
                : (counts[repoName] || { running: 0, needs_you: 0 });

            if (c.running || c.needs_you) {
                var meta = document.createElement('span');
                meta.className = 'console-tab-meta';
                if (c.running) {
                    var count = document.createElement('span');
                    count.className = 'console-tab-count';
                    count.textContent = c.running + ' ▸';
                    meta.appendChild(count);
                }
                if (c.running && c.needs_you) meta.appendChild(document.createTextNode(' · '));
                if (c.needs_you) {
                    var flag = document.createElement('span');
                    flag.className = 'console-tab-flag';
                    flag.textContent = c.needs_you + ' ⚑';
                    meta.appendChild(flag);
                }
                btn.appendChild(meta);
            }

            btn.addEventListener('click', function () { selectRepo(repoName, { pushHistory: true }); });
            row.appendChild(btn);
        }

        makeSubtab('', 'all repos');
        names.forEach(function (name) { makeSubtab(name, name); });
    }

    // ---------- task stack ----------

    function loadStack(initial) {
        if (!state.activeSlug) return Promise.resolve();
        var url = '/api/projects/' + encodeURIComponent(state.activeSlug) + '/tasks/stack';
        if (state.activeRepo) url += '?repo=' + encodeURIComponent(state.activeRepo);
        var headers = {};
        if (!initial && state.stackEtag) headers['If-None-Match'] = state.stackEtag;
        return fetch(url, { headers: headers }).then(function (r) {
            if (!r.ok && r.status !== 304) throw new Error('bad status ' + r.status);
            if (r.status === 304) return null;
            state.stackEtag = r.headers.get('ETag');
            return r.json();
        }).then(function (stack) {
            if (!stack) return;
            state.stack = stack;
            state.doneCursor = stack.cursor || null;
            if (initial) state.extraDone = [];
            renderMergedStack(initial);
        }).catch(function () {
            // First load failed outright (as opposed to just being slow):
            // swap the "Loading…" skeleton for a retry message. Stack
            // polling (started by the caller regardless) will replace it
            // once a request succeeds.
            if (initial && !state.stack) renderStackError();
        });
    }

    function startStackPolling() {
        stopStackPolling();
        state.stackTimer = setInterval(function () { loadStack(false); }, STACK_POLL_MS);
    }

    function stopStackPolling() {
        if (state.stackTimer) {
            clearInterval(state.stackTimer);
            state.stackTimer = null;
        }
    }

    function loadEarlierDone() {
        if (!state.doneCursor || !state.activeSlug) return;
        var url = '/api/projects/' + encodeURIComponent(state.activeSlug) +
            '/tasks/stack?cursor=' + encodeURIComponent(state.doneCursor);
        if (state.activeRepo) url += '&repo=' + encodeURIComponent(state.activeRepo);
        fetch(url).then(function (r) { return r.json(); }).then(function (stack) {
            state.extraDone = state.extraDone.concat(stack.done || []);
            state.doneCursor = stack.cursor || null;
            renderMergedStack(false);
        }).catch(function () {});
    }

    // renderMergedStack combines the latest first page (state.stack, which
    // the 4s poll keeps refreshing so new completions show up promptly) with
    // every "earlier" page loaded so far (state.extraDone, which the poll
    // never discards) — so scrolling back through Done survives a poll
    // instead of resetting to page one each time.
    function renderMergedStack(initial) {
        if (!state.stack) return;
        var merged = {
            needs_you: state.stack.needs_you || [],
            running: state.stack.running || [],
            queued: state.stack.queued || [],
            done: (state.stack.done || []).concat(state.extraDone),
            cursor: state.doneCursor
        };
        renderStack(merged, initial);
    }

    function rowKey(group, entry) {
        return group + ':' + (entry.task_id || entry.run_id || entry.title);
    }

    // Painted immediately on project selection, before /tasks/stack resolves,
    // so the frame never sits blank while the request is in flight.
    function renderStackSkeleton() {
        var container = document.getElementById('console-stack');
        container.innerHTML = '';
        GROUPS.forEach(function (g) {
            var section = document.createElement('section');
            section.className = 'console-stack-group';

            var h = document.createElement('h3');
            h.className = 'console-stack-group-title' + (g.key === 'needs_you' ? ' console-stack-group-title-warn' : '');
            h.appendChild(document.createTextNode(g.label + ' '));
            section.appendChild(h);

            var list = document.createElement('div');
            list.className = 'console-stack-list';
            var row = document.createElement('div');
            row.className = 'console-stack-empty console-stack-loading';
            row.textContent = 'Loading…';
            list.appendChild(row);
            section.appendChild(list);

            container.appendChild(section);
        });
    }

    function renderStackError() {
        var container = document.getElementById('console-stack');
        container.innerHTML = '';
        var msg = document.createElement('div');
        msg.className = 'console-stack-empty';
        msg.textContent = 'Could not load tasks — retrying…';
        container.appendChild(msg);
    }

    function renderStack(stack, initial) {
        var container = document.getElementById('console-stack');

        if (initial) {
            container.innerHTML = '';
            GROUPS.forEach(function (g) {
                var section = document.createElement('section');
                section.className = 'console-stack-group';

                var h = document.createElement('h3');
                h.className = 'console-stack-group-title' + (g.key === 'needs_you' ? ' console-stack-group-title-warn' : '');
                var titleWrap = document.createElement('span');
                titleWrap.appendChild(document.createTextNode(g.label));
                if (g.key === 'done') {
                    var caret = document.createElement('span');
                    caret.className = 'console-stack-group-caret';
                    caret.setAttribute('aria-hidden', 'true');
                    titleWrap.appendChild(caret);
                }
                h.appendChild(titleWrap);
                var count = document.createElement('span');
                count.className = 'console-stack-group-count';
                h.appendChild(count);
                section.appendChild(h);

                // Only Done collapses — Needs you / Running / Queued stay
                // plain headers (Needs you / Queued disappear entirely when
                // empty anyway; Running is the always-visible activity
                // signal and isn't meant to be hidden by the user).
                if (g.key === 'done') {
                    h.classList.add('console-stack-group-title-toggle');
                    h.setAttribute('role', 'button');
                    h.setAttribute('tabindex', '0');
                    h.addEventListener('click', toggleDoneCollapsed);
                    h.addEventListener('keydown', function (e) {
                        if (e.key === 'Enter' || e.key === ' ') {
                            toggleDoneCollapsed();
                            e.preventDefault();
                        }
                    });
                }

                var list = document.createElement('div');
                list.className = 'console-stack-list';
                list.id = 'console-stack-list-' + g.key;
                section.appendChild(list);

                container.appendChild(section);
            });

            var earlierBtn = document.createElement('button');
            earlierBtn.type = 'button';
            earlierBtn.className = 'console-load-earlier';
            earlierBtn.id = 'console-load-earlier';
            earlierBtn.textContent = 'Load earlier';
            earlierBtn.hidden = true;
            earlierBtn.addEventListener('click', loadEarlierDone);
            container.appendChild(earlierBtn);
        }

        GROUPS.forEach(function (g) { diffGroup(g.key, stack[g.field] || []); });

        applyDoneCollapsedUI();

        renderSubtabs();
        rebuildFlatIndex();
        applySelectionHighlight();
    }

    // applyDoneCollapsedUI syncs the Done section's collapsed class, its
    // header's aria-expanded, and the "Load earlier" button's visibility
    // with state.doneCollapsed. Called on every render (poll included) —
    // it only ever toggles classes/attributes on the existing section, so
    // it never rebuilds the DOM and so never disturbs the collapsed state
    // a poll would otherwise stomp on.
    function applyDoneCollapsedUI() {
        var list = document.getElementById('console-stack-list-done');
        var section = list && list.parentElement;
        if (section) {
            section.classList.toggle('console-stack-group-collapsed', state.doneCollapsed);
            var header = section.querySelector('.console-stack-group-title');
            if (header) header.setAttribute('aria-expanded', String(!state.doneCollapsed));
        }
        var earlierBtn = document.getElementById('console-load-earlier');
        if (earlierBtn) earlierBtn.hidden = state.doneCollapsed || !state.doneCursor;
    }

    function setDoneCollapsed(collapsed) {
        if (state.doneCollapsed === collapsed) return;
        state.doneCollapsed = collapsed;
        saveDoneCollapsed(collapsed);
        applyDoneCollapsedUI();
        rebuildFlatIndex();
        applySelectionHighlight();
    }

    function toggleDoneCollapsed() {
        setDoneCollapsed(!state.doneCollapsed);
    }

    function diffGroup(groupKey, entries) {
        var list = document.getElementById('console-stack-list-' + groupKey);
        var section = list.parentElement;
        var countEl = section.querySelector('.console-stack-group-count');
        // Needs you / Queued disappear entirely when empty, so their count
        // never needs to read "0" — but Running and Done stay in the stack
        // even with no rows, and an empty header with a blank count would
        // read as broken rather than "genuinely zero".
        var alwaysShown = groupKey === 'running' || groupKey === 'done';
        countEl.textContent = entries.length ? String(entries.length) : (alwaysShown ? '0' : '');

        var seen = {};
        var prevKeys = Object.keys(state.rowEls).filter(function (k) {
            return k.indexOf(groupKey + ':') === 0;
        });

        entries.forEach(function (entry) {
            var key = rowKey(groupKey, entry);
            seen[key] = true;
            var el = state.rowEls[key];
            if (!el) {
                el = buildRow(groupKey, entry);
                state.rowEls[key] = el;
            } else {
                updateRow(el, groupKey, entry);
            }
            list.appendChild(el); // re-appending an existing node just reorders it
        });

        prevKeys.forEach(function (key) {
            if (!seen[key]) {
                var el = state.rowEls[key];
                if (el && el.parentElement) el.parentElement.removeChild(el);
                delete state.rowEls[key];
            }
        });

        // Needs you / Queued disappear entirely when empty, rather than
        // showing a header over a dash placeholder — they reappear on the
        // next poll (STACK_POLL_MS) as soon as they have rows. Running
        // always stays visible (dash and all) so an idle system reads as
        // "nothing running" rather than as a missing section; Done always
        // stays visible too, since it's the paginated group users expect
        // to keep finding in the same place.
        section.hidden = !alwaysShown && entries.length === 0;

        var empty = list.querySelector('.console-stack-empty');
        if (entries.length === 0 && alwaysShown) {
            if (!empty) {
                empty = document.createElement('div');
                empty.className = 'console-stack-empty';
                empty.textContent = '—';
                list.appendChild(empty);
            }
        } else if (empty) {
            empty.parentElement.removeChild(empty);
        }
    }

    function buildRow(groupKey, entry) {
        var row = document.createElement('button');
        row.type = 'button';
        row.className = 'console-stack-row';
        row.addEventListener('click', function () { openRow(row); });
        updateRow(row, groupKey, entry);
        return row;
    }

    function updateRow(row, groupKey, entry) {
        row.innerHTML = '';
        row.dataset.key = rowKey(groupKey, entry);
        row.__entry = entry;
        row.__group = groupKey;

        var dot = document.createElement('span');
        dot.className = 'console-stack-row-dot console-stack-row-dot-' + rowDotClass(groupKey, entry);
        if (groupKey === 'running') dot.classList.add('console-stack-row-dot-pulse');
        row.appendChild(dot);

        var content = document.createElement('div');
        content.className = 'console-stack-row-content';

        var line1 = document.createElement('div');
        line1.className = 'console-stack-row-line1';

        var id = document.createElement('span');
        id.className = 'console-stack-row-id';
        id.textContent = rowIdText(entry);
        // In the "all repos" view of a multi-repo project, a small repo tag
        // keeps the merged view legible (docs/design/
        // console-repo-grouping-mock.html's "problem today" frame) without
        // scoping away the interleaving the way a sub-tab does.
        if (!state.activeRepo && entry.repository && ConsoleTabs.repoNamesForProject(currentProject())) {
            var repoTag = document.createElement('span');
            repoTag.className = 'console-stack-row-repotag';
            repoTag.textContent = ' · ' + entry.repository;
            id.appendChild(repoTag);
        }
        line1.appendChild(id);

        var elapsed = document.createElement('span');
        elapsed.className = 'console-stack-row-elapsed' + rowElapsedModifierClass(groupKey, entry);
        elapsed.textContent = rowMetaText(groupKey, entry);
        line1.appendChild(elapsed);

        content.appendChild(line1);

        // The id and title are frequently identical (user-* tasks default the
        // title to the task id) — in that case the title line would just
        // repeat line 1, so it's omitted entirely rather than left as a
        // blank gap.
        var idText = entry.task_id || entry.run_id || '';
        var titleText = entry.title || '';
        if (titleText && titleText !== idText) {
            var title = document.createElement('div');
            title.className = 'console-stack-row-title';
            title.textContent = titleText;
            content.appendChild(title);
        }

        row.appendChild(content);
    }

    // The status word ("succeeded" / "failed" / "cancelled") is redundant
    // for a succeeded Done row — the dot colour and the Done group heading
    // already say it — but stays for failed/cancelled/needs-you rows since
    // those need to be scannable at a glance, coloured with the matching
    // status token.
    function rowElapsedModifierClass(groupKey, entry) {
        if (groupKey === 'needs_you') return ' console-stack-row-elapsed-warn';
        if (groupKey === 'done' && entry.outcome === 'failed') return ' console-stack-row-elapsed-bad';
        return '';
    }

    // g = ok, y = warn/needs-you, r = failed, b = running, x = idle — mirrors
    // docs/design/console-restructured-mock.html's .m5 .d.* classes.
    function rowDotClass(groupKey, entry) {
        switch (groupKey) {
            case 'needs_you': return 'y';
            case 'running': return 'b';
            case 'queued': return 'x';
            case 'done':
                if (entry.outcome === 'succeeded') return 'g';
                if (entry.outcome === 'failed') return 'r';
                return 'x';
            default: return 'x';
        }
    }

    function rowIdText(entry) {
        var id = entry.task_id || entry.run_id || '';
        if (entry.attempt && entry.attempt > 1) id += ' · a' + entry.attempt;
        return id;
    }

    function rowMetaText(groupKey, entry) {
        switch (groupKey) {
            case 'needs_you':
                return entry.reason || '';
            case 'running':
                return (entry.current_step ? entry.current_step + ' · ' : '') + formatElapsed(entry.elapsed_seconds);
            case 'queued':
                return entry.reason || '';
            case 'done':
                var duration = formatDuration(entry.duration_seconds);
                if (entry.outcome === 'succeeded') return duration;
                return (entry.outcome || '') + ' · ' + duration;
            default:
                return '';
        }
    }

    function rebuildFlatIndex() {
        var order = [];
        GROUPS.forEach(function (g) {
            // A collapsed Done group's rows stay in the DOM (so re-expanding
            // is instant) but drop out of the j/k navigation order.
            if (g.key === 'done' && state.doneCollapsed) return;
            var list = document.getElementById('console-stack-list-' + g.key);
            Array.prototype.forEach.call(list.children, function (el) {
                if (el.classList.contains('console-stack-row')) order.push(el.dataset.key);
            });
        });
        // Preserve the current selection's identity across a re-render. If
        // the selected row just dropped out of the order (e.g. its group
        // collapsed), move to the nearest remaining row instead of leaving
        // a stale index, or clear the selection if nothing is left.
        var selectedKey = state.rowOrder[state.selectedIndex];
        state.rowOrder = order;
        if (selectedKey) {
            var idx = order.indexOf(selectedKey);
            if (idx !== -1) {
                state.selectedIndex = idx;
            } else if (order.length) {
                state.selectedIndex = Math.min(state.selectedIndex, order.length - 1);
            } else {
                state.selectedIndex = -1;
            }
        }
    }

    // ---------- selection & keyboard ----------

    function setSelectedIndex(idx) {
        state.selectedIndex = idx;
        applySelectionHighlight();
    }

    function applySelectionHighlight() {
        Object.keys(state.rowEls).forEach(function (key) {
            state.rowEls[key].classList.remove('console-stack-row-selected');
        });
        var key = state.rowOrder[state.selectedIndex];
        if (key && state.rowEls[key]) {
            var el = state.rowEls[key];
            el.classList.add('console-stack-row-selected');
            el.scrollIntoView({ block: 'nearest' });
        }
    }

    function moveSelection(delta) {
        if (!state.rowOrder.length) return;
        var next = state.selectedIndex + delta;
        if (next < 0) next = 0;
        if (next > state.rowOrder.length - 1) next = state.rowOrder.length - 1;
        setSelectedIndex(next);
    }

    function openRow(rowEl) {
        var idx = state.rowOrder.indexOf(rowEl.dataset.key);
        setSelectedIndex(idx);
        activateSelected();
    }

    function activateSelected() {
        var key = state.rowOrder[state.selectedIndex];
        if (!key) return;
        var el = state.rowEls[key];
        if (!el) return;
        state.activeTaskId = el.__entry.task_id || '';
        renderCentrePane(el.__entry, el.__group);
        pushLocation(state.activeSlug, state.activeRepo, state.activeTaskId);
    }

    function deselect() {
        state.activeTaskId = '';
        renderCentrePane(null, null);
        pushLocation(state.activeSlug, state.activeRepo, '');
    }

    function selectTaskById(taskId) {
        var found = null;
        var foundGroup = null;
        GROUPS.forEach(function (g) {
            if (found) return;
            ((state.stack && state.stack[g.field]) || []).forEach(function (entry) {
                if (!found && entry.task_id === taskId) {
                    found = entry;
                    foundGroup = g.key;
                }
            });
        });
        if (found) {
            if (foundGroup === 'done' && state.doneCollapsed) setDoneCollapsed(false);
            var idx = state.rowOrder.indexOf(rowKey(foundGroup, found));
            setSelectedIndex(idx);
            renderCentrePane(found, foundGroup);
        } else {
            renderCentrePane({ task_id: taskId, title: taskId }, null);
        }
    }

    function isTypingTarget(el) {
        var tag = (el && el.tagName || '').toLowerCase();
        return tag === 'input' || tag === 'textarea' || (el && el.isContentEditable);
    }

    function escapeHtml(s) {
        return String(s === undefined || s === null ? '' : s)
            .replace(/&/g, '&amp;')
            .replace(/</g, '&lt;')
            .replace(/>/g, '&gt;')
            .replace(/"/g, '&quot;')
            .replace(/'/g, '&#39;');
    }

    document.addEventListener('keydown', function (e) {
        if (isTypingTarget(e.target)) return;

        var overlay = document.getElementById('console-help-overlay');
        if (!overlay.hidden) {
            if (e.key === 'Escape' || e.key === '?') {
                toggleHelp(false);
                e.preventDefault();
            }
            return;
        }

        var activityOverlay = document.getElementById('console-activity-overlay');
        if (!activityOverlay.hidden) {
            if (e.key === 'Escape' || e.key === 'a') {
                toggleActivity(false);
                e.preventDefault();
            }
            return;
        }

        var ledgerOverlay = document.getElementById('console-ledger-overlay');
        if (!ledgerOverlay.hidden) {
            if (e.key === 'Escape' || e.key === 'l') {
                toggleLedger(false);
                e.preventDefault();
            }
            return;
        }

        // A drawer sits on top of a secondary view; Escape closes the
        // topmost thing first rather than falling through to the stack.
        if (anyDrawerOpen()) {
            if (e.key === 'Escape') {
                closeStepDrawer();
                closeIntentDrawer();
                e.preventDefault();
            }
            return;
        }

        if (isViewOpen()) {
            if (e.key === 'Escape') {
                closeView();
                e.preventDefault();
            }
            return;
        }

        switch (e.key) {
            case 'j':
                moveSelection(1);
                e.preventDefault();
                break;
            case 'k':
                moveSelection(-1);
                e.preventDefault();
                break;
            case 'Enter':
                activateSelected();
                e.preventDefault();
                break;
            case 'Escape':
                deselect();
                e.preventDefault();
                break;
            case 'Tab':
                switchProject(e.shiftKey ? -1 : 1);
                e.preventDefault();
                break;
            case 'a':
                toggleActivity(true);
                e.preventDefault();
                break;
            case 'w':
                openWorkflowsView();
                e.preventDefault();
                break;
            case 'i':
                openIntentView();
                e.preventDefault();
                break;
            case 'c':
                // A needs-you compare-eligible task open in the centre pane
                // claims 'c' for its compare/single-log toggle; otherwise it
                // opens the Containers view, as advertised in the header button.
                if (detail && detail.entry && isNeedsYouCompareKind(detail.entry.kind)) {
                    toggleCompareView();
                } else {
                    openContainersView();
                }
                e.preventDefault();
                break;
            case 'l':
                toggleLedger(true);
                e.preventDefault();
                break;
            case 'd':
                toggleDensity();
                e.preventDefault();
                break;
            case '?':
                toggleHelp(true);
                e.preventDefault();
                break;
            case '[':
                if (detail) { switchStep(-1); e.preventDefault(); }
                break;
            case ']':
                if (detail) { switchStep(1); e.preventDefault(); }
                break;
            case '{':
                if (detail) { switchAttempt(-1); e.preventDefault(); }
                break;
            case '}':
                if (detail) { switchAttempt(1); e.preventDefault(); }
                break;
            case 'g':
                if (detail) { scrollLogTo('top'); e.preventDefault(); }
                break;
            case 'G':
                if (detail) { scrollLogTo('bottom'); e.preventDefault(); }
                break;
            case 'f':
                if (detail && !detail.compareMode) { toggleLogFollow(); e.preventDefault(); }
                break;
            case 'r':
                // 'r' releases a needs-you task's claim when one is open
                // (pre-existing binding); otherwise it cycles the repo
                // sub-tabs (no-op on a legacy project) — see
                // docs/design/console-repo-grouping-mock.html, option 1.
                if (detail && hasNeedsYouAction('release')) {
                    releaseTask();
                    e.preventDefault();
                } else if (ConsoleTabs.repoNamesForProject(currentProject())) {
                    switchRepo(1);
                    e.preventDefault();
                }
                break;
            case 'R':
                if (ConsoleTabs.repoNamesForProject(currentProject())) {
                    selectRepo('', { pushHistory: true });
                    e.preventDefault();
                }
                break;
            case 'x':
                if (detail && hasNeedsYouAction('close')) { closeTask(); e.preventDefault(); }
                break;
            default:
                break;
        }
    });

    function toggleHelp(show) {
        document.getElementById('console-help-overlay').hidden = !show;
    }
    document.getElementById('console-help-close').addEventListener('click', function () { toggleHelp(false); });

    // ---------- activity ticker & stream ----------

    function loadTicker() {
        var slug = state.activeSlug;
        var tickerEl = document.getElementById('console-ticker');
        if (!slug) {
            tickerEl.textContent = '—';
            return Promise.resolve();
        }
        return fetch('/api/activity?project=' + encodeURIComponent(slug) + '&limit=' + TICKER_ENTRY_LIMIT)
            .then(function (r) { return r.json(); })
            .then(function (resp) {
                if (slug !== state.activeSlug) return; // stale response from a since-abandoned project
                renderTicker(tickerEl, (resp && resp.entries) || []);
            })
            .catch(function () {});
    }

    // renderTicker packs the most recent activity entries (newest first,
    // matching /api/activity's order) into the single ellipsised foot line —
    // it is the widest thing in the foot because it is the only content, so
    // as many recent entries as fit are shown rather than just the latest
    // one. Failed entries are coloured --bad inline; everything else stays
    // --tx2 (inherited).
    function renderTicker(tickerEl, entries) {
        tickerEl.innerHTML = '';
        if (!entries.length) {
            tickerEl.textContent = '—';
            return;
        }
        entries.forEach(function (entry, i) {
            if (i > 0) tickerEl.appendChild(document.createTextNode(' · '));
            var frag = document.createElement('span');
            if (entry.failure) frag.className = 'console-ticker-frag-bad';
            frag.textContent = formatTickerTime(entry.ts) + ' ' + entry.text;
            tickerEl.appendChild(frag);
        });
    }

    function formatTickerTime(iso) {
        if (!iso) return '';
        var d = new Date(iso);
        if (isNaN(d.getTime())) return '';
        return d.toTimeString().slice(0, 8);
    }

    function startTickerPolling() {
        stopTickerPolling();
        state.tickerTimer = setInterval(loadTicker, TICKER_POLL_MS);
    }

    function stopTickerPolling() {
        if (state.tickerTimer) {
            clearInterval(state.tickerTimer);
            state.tickerTimer = null;
        }
    }

    document.getElementById('console-ticker').addEventListener('click', function () { toggleActivity(true); });

    function toggleActivity(show) {
        state.activity.open = show;
        document.getElementById('console-activity-overlay').hidden = !show;
        if (show) loadActivityStream(true);
    }
    document.getElementById('console-activity-close').addEventListener('click', function () { toggleActivity(false); });
    document.getElementById('console-activity-load-earlier').addEventListener('click', function () { loadActivityStream(false); });

    document.getElementById('console-activity-scope-project').addEventListener('change', function () {
        state.activity.scope = 'project';
        loadActivityStream(true);
    });
    document.getElementById('console-activity-scope-all').addEventListener('change', function () {
        state.activity.scope = 'all';
        loadActivityStream(true);
    });
    document.getElementById('console-activity-failures-only').addEventListener('change', function (e) {
        state.activity.failuresOnly = e.target.checked;
        loadActivityStream(true);
    });

    function activityStreamURL(cursor) {
        var params = new URLSearchParams();
        if (state.activity.scope === 'project' && state.activeSlug) params.set('project', state.activeSlug);
        if (state.activity.failuresOnly) params.set('failures_only', '1');
        if (cursor) params.set('before', cursor);
        return '/api/activity?' + params.toString();
    }

    function loadActivityStream(initial) {
        if (initial) {
            state.activity.entries = [];
            state.activity.cursor = null;
        }
        return fetch(activityStreamURL(initial ? null : state.activity.cursor))
            .then(function (r) { return r.json(); })
            .then(function (resp) {
                var entries = (resp && resp.entries) || [];
                state.activity.entries = state.activity.entries.concat(entries);
                state.activity.cursor = resp && resp.cursor;
                renderActivityStream();
            })
            .catch(function () {});
    }

    function renderActivityStream() {
        var list = document.getElementById('console-activity-list');
        list.innerHTML = '';

        if (!state.activity.entries.length) {
            var empty = document.createElement('div');
            empty.className = 'console-activity-empty';
            empty.textContent = 'No activity.';
            list.appendChild(empty);
        }

        state.activity.entries.forEach(function (entry) {
            var row = document.createElement('div');
            row.className = 'console-activity-row' + (entry.failure ? ' console-activity-row-failure' : '');

            var time = document.createElement('span');
            time.className = 'console-activity-row-time';
            time.textContent = formatTimestamp(entry.ts);
            row.appendChild(time);

            if (state.activity.scope === 'all' && entry.project_label) {
                var proj = document.createElement('span');
                proj.className = 'console-activity-row-project';
                proj.textContent = entry.project_label;
                row.appendChild(proj);
            }

            var text = document.createElement('span');
            text.className = 'console-activity-row-text';
            text.textContent = entry.text;
            row.appendChild(text);

            list.appendChild(row);
        });

        var earlierBtn = document.getElementById('console-activity-load-earlier');
        earlierBtn.hidden = !state.activity.cursor;
    }

    // ---------- ledger ----------

    document.getElementById('console-ledger-close').addEventListener('click', function () { toggleLedger(false); });

    function toggleLedger(show) {
        document.getElementById('console-ledger-overlay').hidden = !show;
        if (show) loadLedger();
    }

    function loadLedger() {
        var slug = state.activeSlug;
        var body = document.getElementById('console-ledger-body');
        if (!slug) {
            body.innerHTML = '';
            return;
        }
        body.innerHTML = '<div class="console-ledger-empty">Loading…</div>';
        fetch('/api/projects/' + encodeURIComponent(slug) + '/ledger')
            .then(function (r) { return r.json(); })
            .then(function (data) {
                if (slug !== state.activeSlug) return; // stale response from a since-abandoned project
                renderLedger(data || {});
            })
            .catch(function () {
                body.innerHTML = '<div class="console-ledger-empty">Failed to load ledger.</div>';
            });
    }

    function renderLedger(data) {
        var body = document.getElementById('console-ledger-body');
        body.innerHTML = '';
        body.appendChild(renderLedgerSummary(data));
        body.appendChild(renderLedgerPromptFiles(data.prompt_files || []));
        body.appendChild(renderLedgerRequirements(data.requirements || [], data.task_requirements || []));
    }

    function renderLedgerSummary(data) {
        var section = document.createElement('div');
        section.className = 'console-ledger-section';

        var stats = document.createElement('div');
        stats.className = 'console-ledger-stats';
        stats.appendChild(ledgerStatTile('Mean attempts to success', formatNumber(data.mean_attempts_to_success || 0)));
        stats.appendChild(ledgerStatTile('Tokens per succeeded task', formatNumber(data.tokens_per_succeeded_task || 0)));
        var points = data.pass_rate_over_time || [];
        var latest = points.length ? points[points.length - 1] : null;
        stats.appendChild(ledgerStatTile('Latest day pass rate', latest ? Math.round(latest.pass_rate * 100) + '%' : '—'));
        section.appendChild(stats);

        var table = document.createElement('table');
        table.className = 'console-ledger-table';
        var thead = document.createElement('thead');
        thead.innerHTML = '<tr><th>Date</th><th>Attempts</th><th>Passed</th><th>Pass rate</th></tr>';
        table.appendChild(thead);
        var tbody = document.createElement('tbody');
        if (!points.length) {
            tbody.innerHTML = '<tr><td colspan="4" class="console-ledger-empty">No attempts yet.</td></tr>';
        }
        points.forEach(function (p) {
            var tr = document.createElement('tr');
            tr.innerHTML = '<td>' + escapeHtml(p.date) + '</td><td>' + p.attempts + '</td><td>' + p.passed +
                '</td><td>' + Math.round((p.pass_rate || 0) * 100) + '%</td>';
            tbody.appendChild(tr);
        });
        table.appendChild(tbody);
        section.appendChild(table);
        return section;
    }

    function ledgerStatTile(label, value) {
        var tile = document.createElement('div');
        tile.className = 'console-ledger-stat';
        var v = document.createElement('div');
        v.className = 'console-ledger-stat-value';
        v.textContent = value;
        var l = document.createElement('div');
        l.className = 'console-ledger-stat-label';
        l.textContent = label;
        tile.appendChild(v);
        tile.appendChild(l);
        return tile;
    }

    function renderLedgerPromptFiles(files) {
        var section = document.createElement('div');
        section.className = 'console-ledger-section';
        var h = document.createElement('h3');
        h.textContent = 'Prompt revisions';
        section.appendChild(h);

        if (!files.length) {
            var empty = document.createElement('div');
            empty.className = 'console-ledger-empty';
            empty.textContent = 'No prompt-revision data recorded yet.';
            section.appendChild(empty);
            return section;
        }

        files.forEach(function (file) {
            var block = document.createElement('div');
            block.className = 'console-ledger-file';

            var title = document.createElement('div');
            title.className = 'console-ledger-file-path';
            title.textContent = file.path;
            block.appendChild(title);

            if (file.latest_change) {
                var cmp = document.createElement('div');
                cmp.className = 'console-ledger-comparison';
                var before = file.latest_change.before, after = file.latest_change.after;
                cmp.textContent = 'Latest change: ' + Math.round((before.pass_rate || 0) * 100) + '% pass (' +
                    before.attempts + ' attempts) → ' + Math.round((after.pass_rate || 0) * 100) + '% pass (' +
                    after.attempts + ' attempts)';
                block.appendChild(cmp);
            }

            var table = document.createElement('table');
            table.className = 'console-ledger-table';
            table.innerHTML = '<thead><tr><th>Revision</th><th>Date</th><th>Message</th><th>Attempts</th>' +
                '<th>Pass rate</th><th>Mean tokens</th></tr></thead>';
            var tbody = document.createElement('tbody');
            (file.revisions || []).forEach(function (rev) {
                var tr = document.createElement('tr');
                tr.innerHTML = '<td class="console-ledger-mono">' + escapeHtml(rev.revision) + '</td>' +
                    '<td>' + escapeHtml(rev.date || '') + '</td>' +
                    '<td>' + escapeHtml(rev.message || '') + '</td>' +
                    '<td>' + rev.attempts + '</td>' +
                    '<td>' + Math.round((rev.pass_rate || 0) * 100) + '%</td>' +
                    '<td>' + formatNumber(rev.mean_tokens || 0) + '</td>';
                tbody.appendChild(tr);
            });
            table.appendChild(tbody);
            block.appendChild(table);
            section.appendChild(block);
        });
        return section;
    }

    function renderLedgerRequirements(requirements, taskRequirements) {
        var section = document.createElement('div');
        section.className = 'console-ledger-section';
        var h = document.createElement('h3');
        h.textContent = 'Requirements';
        section.appendChild(h);

        if (!requirements.length) {
            var empty = document.createElement('div');
            empty.className = 'console-ledger-empty';
            empty.textContent = 'No requirement injections recorded yet.';
            section.appendChild(empty);
            return section;
        }

        var table = document.createElement('table');
        table.className = 'console-ledger-table';
        table.innerHTML = '<thead><tr><th>Requirement</th><th>Tasks</th></tr></thead>';
        var tbody = document.createElement('tbody');
        requirements.forEach(function (req) {
            var tr = document.createElement('tr');
            var taskLabels = (req.tasks || []).map(function (t) { return t.title || t.task_id; }).join(', ');
            tr.innerHTML = '<td><span class="console-ledger-mono">' + escapeHtml(req.id) + '</span>' +
                (req.statement ? '<div class="console-ledger-requirement-text">' + escapeHtml(truncateText(req.statement, 140)) + '</div>' : '') +
                '</td><td>' + escapeHtml(taskLabels) + '</td>';
            tbody.appendChild(tr);
        });
        table.appendChild(tbody);
        section.appendChild(table);

        if (taskRequirements.length) {
            var table2 = document.createElement('table');
            table2.className = 'console-ledger-table';
            table2.innerHTML = '<thead><tr><th>Task</th><th>Requirements</th></tr></thead>';
            var tbody2 = document.createElement('tbody');
            taskRequirements.forEach(function (t) {
                var tr = document.createElement('tr');
                tr.innerHTML = '<td>' + escapeHtml(t.title || t.task_id) + '</td><td class="console-ledger-mono">' +
                    escapeHtml((t.requirement_ids || []).join(', ')) + '</td>';
                tbody2.appendChild(tr);
            });
            table2.appendChild(tbody2);
            section.appendChild(table2);
        }
        return section;
    }

    function truncateText(s, n) {
        if (!s || s.length <= n) return s || '';
        return s.slice(0, n - 1) + '…';
    }

    function escapeHtml(s) {
        var div = document.createElement('div');
        div.textContent = s == null ? '' : String(s);
        return div.innerHTML;
    }

    // ---------- centre pane: entry point ----------

    // renderCentrePane opens (or clears) the task-detail pane for a stack
    // row. entry/group come from the task stack (see rowKey); group is null
    // when the task was opened via a direct URL that isn't in the currently
    // loaded stack window.
    function renderCentrePane(entry, group) {
        stopDetail();

        var el = document.getElementById('console-centre');
        el.innerHTML = '';

        if (!entry) {
            var empty = document.createElement('div');
            empty.className = 'console-centre-empty';
            empty.textContent = 'Select a task from the stack.';
            el.appendChild(empty);
            renderFootKeys();
            return;
        }

        detail = {
            taskId: entry.task_id || '',
            title: entry.title || entry.task_id || entry.run_id || '(untitled)',
            entry: entry,
            group: group,
            attempts: [],
            attemptIndex: -1,
            run: null,
            scopedStep: null,
            stepLines: [],
            allLines: [],
            logKind: 'attempt', // 'attempt' | 'run' — which /api/{kind}s/{id}/stream to use
            logId: '',
            logStatus: 'live',
            logFollow: true,
            logWrap: false,
            logTypeFilter: 'all',
            logSkipped: 0,
            eventSource: null,
            pollTimer: null,
            threadLoadedFor: '', // run id the thread panel was last loaded for, '' = not loaded
            // compareMode defaults on for a needs-you task whose attention
            // kind is stale-claim/repeat-failure (see isNeedsYouCompareKind);
            // 'c' toggles it off/on for the rest of this session.
            compareMode: group === 'needs_you' && isNeedsYouCompareKind(entry.kind)
        };

        renderDetailShell();
        renderWhyLine();
        renderFootKeys();

        if (!detail.taskId) {
            // Ad-hoc run (no task record) — there's exactly one implicit attempt.
            selectAdhocRun(entry.run_id);
            return;
        }

        loadAttempts();
    }

    function stopDetail() {
        stopDetailPoll();
        stopLogStream();
        detail = null;
    }

    // ---------- centre pane: shell + attempts ----------

    function renderDetailShell() {
        var el = document.getElementById('console-centre');
        el.innerHTML = '';

        var header = document.createElement('div');
        header.className = 'console-task-header';
        header.id = 'console-task-header';
        el.appendChild(header);

        var whyLine = document.createElement('div');
        whyLine.className = 'console-why-line';
        whyLine.id = 'console-why-line';
        whyLine.hidden = true;
        el.appendChild(whyLine);

        var actionPanel = document.createElement('div');
        actionPanel.className = 'console-action-panel';
        actionPanel.id = 'console-action-panel';
        actionPanel.hidden = true;
        el.appendChild(actionPanel);

        var facts = document.createElement('dl');
        facts.className = 'console-facts-row';
        facts.id = 'console-facts-block';
        el.appendChild(facts);

        var strip = document.createElement('div');
        strip.className = 'console-step-strip';
        strip.id = 'console-step-strip';
        el.appendChild(strip);

        var threadPanel = document.createElement('div');
        threadPanel.className = 'console-thread-panel';
        threadPanel.id = 'console-thread-panel';
        threadPanel.hidden = true;
        el.appendChild(threadPanel);

        var logPane = document.createElement('div');
        logPane.className = 'console-log-pane';
        logPane.id = 'console-log-pane';
        el.appendChild(logPane);
    }

    function loadAttempts() {
        var taskId = detail.taskId;
        fetch('/api/projects/' + encodeURIComponent(state.activeSlug) + '/tasks/' + encodeURIComponent(taskId) + '/attempts')
            .then(function (r) {
                if (!r.ok) throw new Error('task not found');
                return r.json();
            })
            .then(function (data) {
                if (!detail || detail.taskId !== taskId) return; // stale — user navigated away
                detail.attempts = data.attempts || [];
                detail.title = data.title || detail.title;
                if (!detail.attempts.length) {
                    renderNoAttempts();
                    return;
                }
                // Default to the attempt matching the entry's run_id (i.e. the
                // one the user actually clicked in the stack), else the latest.
                var startIndex = detail.attempts.length - 1;
                if (detail.entry && detail.entry.run_id) {
                    for (var i = 0; i < detail.attempts.length; i++) {
                        if (detail.attempts[i].run_id === detail.entry.run_id) { startIndex = i; break; }
                    }
                }
                selectAttempt(startIndex);
            })
            .catch(function () {
                if (!detail || detail.taskId !== taskId) return;
                renderTaskNotFound();
            });
    }

    function selectAttempt(index) {
        if (!detail) return;
        detail.attemptIndex = index;
        detail.scopedStep = null;
        detail.stepLines = [];
        stopDetailPoll();

        var attempt = detail.attempts[index];
        renderLogArea();

        fetchRunDetail(attempt.run_id).then(function (run) {
            if (!detail || detail.attempts[detail.attemptIndex] !== attempt) return; // stale
            applyRunDetail(run);
        }).catch(function () {
            if (!detail || detail.attempts[detail.attemptIndex] !== attempt) return;
            renderRunLoadError();
        });
    }

    function switchAttempt(delta) {
        if (!detail || !detail.attempts.length) return;
        var next = detail.attemptIndex + delta;
        if (next < 0 || next >= detail.attempts.length) return;
        selectAttempt(next);
    }

    function selectAdhocRun(runId) {
        detail.attempts = [{ attempt_num: 1, attempt_id: '', run_id: runId, outcome: '', started_at: '', duration: '' }];
        detail.attemptIndex = 0;
        renderLogPaneShell();
        startDetailLogStream('run', runId);
        fetchRunDetail(runId).then(function (run) {
            if (!detail) return;
            detail.title = run.title || detail.title;
            applyRunDetail(run);
        }).catch(function () {
            renderRunLoadError();
        });
    }

    function fetchRunDetail(runId) {
        return fetch('/api/runs/' + encodeURIComponent(runId)).then(function (r) {
            if (!r.ok) throw new Error('run not found');
            return r.json();
        });
    }

    function applyRunDetail(run) {
        detail.run = run;
        renderHeader(run);
        renderFootKeys();
        renderFactsRow(run);
        renderStepStrip(run);
        if (computeHeaderState(run) === 'parked') {
            showThreadPanel(run);
        } else {
            hideThreadPanel();
        }
        manageDetailPoll(run);
    }

    function manageDetailPoll(run) {
        stopDetailPoll();
        if (run.state === 'running' || run.state === 'pending' || run.state === 'waiting' || run.state === 'parked') {
            detail.pollTimer = setInterval(function () {
                var attempt = detail.attempts[detail.attemptIndex];
                if (!attempt) return;
                fetchRunDetail(attempt.run_id).then(function (fresh) {
                    if (!detail) return;
                    applyRunDetail(fresh);
                }).catch(function () {});
            }, 2000);
        }
    }

    function stopDetailPoll() {
        if (detail && detail.pollTimer) {
            clearInterval(detail.pollTimer);
            detail.pollTimer = null;
        }
    }

    function renderNoAttempts() {
        var el = document.getElementById('console-facts-block');
        if (!el) return;
        appendFactRow(el, 'status', 'No attempts recorded for this task.');
    }

    function renderTaskNotFound() {
        var el = document.getElementById('console-centre');
        el.innerHTML = '';
        var msg = document.createElement('div');
        msg.className = 'console-centre-empty';
        msg.textContent = 'Task not found.';
        el.appendChild(msg);
    }

    function renderRunLoadError() {
        var el = document.getElementById('console-facts-block');
        if (!el) return;
        appendFactRow(el, 'status', 'Failed to load run detail.');
    }

    // ---------- centre pane: header + actions ----------

    // computeHeaderState maps a run (plus the stack group the task was
    // opened from) onto one of the six states the header actions/pill key
    // off: needs_you, running, queued, done, parked, or the raw run state
    // as a fallback.
    function computeHeaderState(run) {
        if (detail.group === 'needs_you') return 'needs_you';
        if (!run) return 'loading';
        switch (run.state) {
            case 'running':
            case 'waiting':
                return 'running';
            case 'pending':
                return 'queued';
            case 'parked':
                return 'parked';
            case 'succeeded':
            case 'failed':
            case 'cancelled':
                return 'done';
            default:
                return run.state || 'unknown';
        }
    }

    // headerPillColorClass maps a header state onto one of the mock's four
    // pill colors (run/ok/bad/warn) plus a neutral fallback for states the
    // mock doesn't depict (queued, parked, cancelled/stopped).
    function headerPillColorClass(headerState, run) {
        switch (headerState) {
            case 'running': return 'console-task-pill-run';
            case 'needs_you': return 'console-task-pill-warn';
            case 'queued': return 'console-task-pill-warn';
            case 'done':
                if (run.state === 'succeeded') return 'console-task-pill-ok';
                if (run.state === 'failed') return 'console-task-pill-bad';
                return 'console-task-pill-neutral';
            default: return 'console-task-pill-neutral';
        }
    }

    function headerPillLabel(headerState, run) {
        if (headerState === 'done') return run.state;
        if (headerState === 'needs_you') return 'needs you';
        if (headerState === 'parked' && run.parked_seconds) return 'parked · ' + formatDuration(run.parked_seconds);
        if (headerState === 'running' && run.state === 'waiting') return 'waiting';
        return headerState;
    }

    function renderHeader(run) {
        var el = document.getElementById('console-task-header');
        if (!el) return;
        el.innerHTML = '';

        var id = document.createElement('span');
        id.className = 'console-task-id';
        id.textContent = detail.taskId || (run && run.id) || '';
        el.appendChild(id);

        var headerState = computeHeaderState(run);
        var pill = document.createElement('span');
        pill.className = 'console-task-pill ' + headerPillColorClass(headerState, run);
        pill.textContent = headerPillLabel(headerState, run);
        el.appendChild(pill);

        var title = document.createElement('span');
        title.className = 'console-task-title';
        title.textContent = detail.title;
        el.appendChild(title);

        var actions = document.createElement('div');
        actions.className = 'console-header-actions';

        if (headerState === 'running') {
            actions.appendChild(actionButton('Console', function () { showConsoleOutput(run); }));
            actions.appendChild(actionButton('Workflow', function () { showWorkflowInfo(run); }));
            actions.appendChild(actionButton('Cancel', function () { cancelRun(run); }, 'btn-danger'));
        } else if (headerState === 'queued') {
            actions.appendChild(actionButton('Cancel', function () { cancelRun(run); }, 'btn-danger'));
        } else if (headerState === 'parked') {
            actions.appendChild(actionButton('Cancel', function () { cancelRun(run); }, 'btn-danger'));
        } else if (headerState === 'done') {
            actions.appendChild(actionButton('Open branch', function () { showBranch(run); }));
            actions.appendChild(actionButton('Diff', function () { showDiff(run); }));
            if (run.container_state === 'available' || run.container_state === 'stopped') {
                actions.appendChild(actionButton('Delete container', function () { deleteContainerForRun(run); }, 'btn-danger'));
            }
        } else if (headerState === 'needs_you') {
            renderNeedsYouActions(actions);
        }
        el.appendChild(actions);
    }

    // isNeedsYouCompareKind reports whether an attention Kind gets the full
    // needs-you treatment (why-line + compare log view) — stale-claim and
    // repeat-failure both name a specific task with an attempt history to
    // compare; other kinds (parked, long-poll, builtin-failures) keep the
    // ordinary single-attempt view.
    function isNeedsYouCompareKind(kind) {
        return kind === 'stale-claim' || kind === 'repeat-failure';
    }

    // renderWhyLine shows the attention item's one-sentence Reason under the
    // header for compare-kind needs-you tasks. Independent of run/attempt
    // load, since it only depends on the stack entry that was clicked.
    function renderWhyLine() {
        var el = document.getElementById('console-why-line');
        if (!el || !detail) return;
        if (detail.group === 'needs_you' && isNeedsYouCompareKind(detail.entry.kind) && detail.entry.reason) {
            el.textContent = detail.entry.reason;
            el.hidden = false;
        } else {
            el.hidden = true;
        }
    }

    // renderNeedsYouActions renders whichever action buttons apply to the
    // open needs-you item, driven entirely by the attention item's own
    // Actions list (see internal/attention) — release/close/run-once for
    // stale-claim & repeat-failure, mute for builtin-failures. Parked and
    // long-poll items declare no button handled here, matching "Needs-you
    // ... show no actions yet" until their own tickets add them.
    function renderNeedsYouActions(actions) {
        var entry = detail.entry || {};

        if (hasNeedsYouAction('release')) {
            actions.appendChild(actionButton('Release claim', function () { releaseTask(); }));
        }
        if (hasNeedsYouAction('close')) {
            var closeBtn = actionButton('Close in tracker', function () { closeTask(); }, 'btn-danger');
            if (!entry.close_available) {
                closeBtn.disabled = true;
                closeBtn.title = 'Define a "close-task" or "cancel-task" host workflow to enable this.';
            }
            actions.appendChild(closeBtn);
        }
        if (hasNeedsYouAction('run-once')) {
            actions.appendChild(actionButton('Run once…', function () { openRunOncePanel(); }));
        }
        if (hasNeedsYouAction('mute')) {
            actions.appendChild(actionButton('Mute', function () { muteAttentionItem(); }));
        }
    }

    // hasNeedsYouAction reports whether the open needs-you item's own
    // Actions list (see internal/attention) offers the named action —
    // shared by renderNeedsYouActions (which buttons to draw), the 'r'/'x'
    // keyboard shortcuts, and the foot key hints (which hints are truthful).
    function hasNeedsYouAction(name) {
        var available = (detail && detail.entry && detail.entry.actions) || [];
        return available.indexOf(name) !== -1;
    }

    // ---------- foot key hints ----------

    // footKeySpec returns the short, contextual set of key hints for the
    // foot bar (see renderFootKeys) — it changes with the selected task's
    // state rather than always listing every shortcut, so it never
    // advertises an action that doesn't apply to what's open. g/G and the
    // log filter hint live in the log bar instead of here (log-pane
    // ticket); the full shortcut list stays behind '?'.
    function footKeySpec() {
        var headerState = detail ? computeHeaderState(detail.run) : null;

        if (headerState === 'needs_you') {
            var hints = [
                { keys: ['j', 'k'], label: 'task' },
                { keys: ['[', ']'], label: 'step' },
                { keys: ['⇧[', '⇧]'], label: 'attempt' }
            ];
            if (hasNeedsYouAction('release')) hints.push({ keys: ['r'], label: 'release' });
            if (hasNeedsYouAction('close')) hints.push({ keys: ['x'], label: 'close' });
            return hints;
        }

        if (headerState === 'running') {
            return [
                { keys: ['j', 'k'], label: 'task' },
                { keys: ['[', ']'], label: 'step' },
                { keys: ['⇧[', '⇧]'], label: 'attempt' },
                { keys: ['tab'], label: 'project' },
                { keys: ['f'], label: 'follow' },
                { keys: ['a'], label: 'activity' }
            ];
        }

        if (detail) {
            return [
                { keys: ['j', 'k'], label: 'task' },
                { keys: ['[', ']'], label: 'step' },
                { keys: ['⇧[', '⇧]'], label: 'attempt' },
                { keys: ['tab'], label: 'project' },
                { keys: ['a'], label: 'activity' }
            ];
        }

        var base = [
            { keys: ['j', 'k'], label: 'task' },
            { keys: ['enter'], label: 'open' },
            { keys: ['tab'], label: 'project' },
            { keys: ['a'], label: 'activity' }
        ];
        if (ConsoleTabs.repoNamesForProject(currentProject())) base.push({ keys: ['r'], label: 'repo' });
        return base;
    }

    // The daemon version is static for the life of the page (baked into the
    // template at render time), so this only needs to run once at boot.
    function renderFooterVersion() {
        var el = document.getElementById('console-daemon-version');
        if (el) el.textContent = root.dataset.clocheVersion || '';
    }

    function renderFootKeys() {
        var el = document.getElementById('console-keys');
        if (!el) return;
        el.innerHTML = '';
        footKeySpec().forEach(function (hint) {
            var span = document.createElement('span');
            hint.keys.forEach(function (key, i) {
                if (i > 0) span.appendChild(document.createTextNode('/'));
                var kbd = document.createElement('kbd');
                kbd.textContent = key;
                span.appendChild(kbd);
            });
            span.appendChild(document.createTextNode(' ' + hint.label));
            el.appendChild(span);
        });
    }

    // afterNeedsYouAction refreshes the task stack in place (no page reload)
    // after release/close/run-once/mute, and reloads the open task's
    // attempts so the centre pane reflects the new state too.
    function afterNeedsYouAction() {
        loadStack(false); // incremental refresh — the ETag is now stale, so this still fetches fresh data
        if (detail && detail.taskId) loadAttempts();
    }

    function releaseTask() {
        if (!detail || !detail.taskId) return;
        fetch('/api/projects/' + encodeURIComponent(state.activeSlug) + '/tasks/' + encodeURIComponent(detail.taskId) + '/release', { method: 'POST' })
            .then(afterNeedsYouAction)
            .catch(function () {});
    }

    function closeTask() {
        if (!detail || !detail.taskId) return;
        fetch('/api/projects/' + encodeURIComponent(state.activeSlug) + '/tasks/' + encodeURIComponent(detail.taskId) + '/close', { method: 'POST' })
            .then(function (r) {
                return r.json().catch(function () { return {}; }).then(function (body) { return { ok: r.ok, body: body }; });
            })
            .then(function (result) {
                if (!result.ok) {
                    showActionPanel('Close in tracker', actionPre(result.body.hint || result.body.error || 'Failed to close task.'));
                    return;
                }
                afterNeedsYouAction();
            })
            .catch(function () {});
    }

    function muteAttentionItem() {
        if (!detail || !detail.entry || !detail.entry.key) return;
        fetch('/api/projects/' + encodeURIComponent(state.activeSlug) + '/attention/mute', {
            method: 'POST',
            headers: { 'Content-Type': 'application/json' },
            body: JSON.stringify({ key: detail.entry.key })
        }).then(afterNeedsYouAction).catch(function () {});
    }

    function openRunOncePanel() {
        if (!detail) return;
        var wrap = document.createElement('div');
        wrap.className = 'console-run-once-form';

        var wfLabel = document.createElement('label');
        wfLabel.textContent = 'Workflow';
        wrap.appendChild(wfLabel);
        var wfInput = document.createElement('input');
        wfInput.type = 'text';
        wfInput.placeholder = 'e.g. develop';
        wrap.appendChild(wfInput);

        var promptLabel = document.createElement('label');
        promptLabel.textContent = 'Prompt (optional)';
        wrap.appendChild(promptLabel);
        var promptInput = document.createElement('textarea');
        wrap.appendChild(promptInput);

        var status = document.createElement('p');
        status.className = 'console-facts-note';
        wrap.appendChild(status);

        var runBtn = document.createElement('button');
        runBtn.type = 'button';
        runBtn.className = 'btn btn-sm btn-primary';
        runBtn.textContent = 'Run once';
        runBtn.addEventListener('click', function () {
            var workflow = wfInput.value.trim();
            if (!workflow) return;
            runBtn.disabled = true;
            status.textContent = 'Dispatching…';
            fetch('/api/projects/' + encodeURIComponent(state.activeSlug) + '/tasks/' + encodeURIComponent(detail.taskId) + '/run-once', {
                method: 'POST',
                headers: { 'Content-Type': 'application/json' },
                body: JSON.stringify({ workflow: workflow, prompt: promptInput.value })
            }).then(function (r) { return r.json(); }).then(function (body) {
                runBtn.disabled = false;
                if (body.run_id) {
                    hideActionPanel();
                    afterNeedsYouAction();
                } else {
                    status.textContent = body.error || 'Failed to dispatch run.';
                }
            }).catch(function () {
                runBtn.disabled = false;
                status.textContent = 'Failed to dispatch run.';
            });
        });
        wrap.appendChild(runBtn);

        showActionPanel('Run once', wrap);
    }

    function actionButton(label, onClick, extraClass) {
        var btn = document.createElement('button');
        btn.type = 'button';
        btn.className = 'btn btn-sm ' + (extraClass || 'btn-secondary');
        btn.textContent = label;
        btn.addEventListener('click', onClick);
        return btn;
    }

    function showActionPanel(title, bodyNode) {
        var panel = document.getElementById('console-action-panel');
        if (!panel) return;
        panel.innerHTML = '';

        var headRow = document.createElement('div');
        headRow.className = 'console-action-panel-head';
        var h = document.createElement('h3');
        h.textContent = title;
        headRow.appendChild(h);
        var close = document.createElement('button');
        close.type = 'button';
        close.className = 'btn btn-sm btn-secondary';
        close.textContent = 'Close';
        close.addEventListener('click', hideActionPanel);
        headRow.appendChild(close);
        panel.appendChild(headRow);

        panel.appendChild(bodyNode);
        panel.hidden = false;
    }

    function hideActionPanel() {
        var panel = document.getElementById('console-action-panel');
        if (panel) panel.hidden = true;
    }

    function actionPre(loadingText) {
        var pre = document.createElement('pre');
        pre.className = 'console-action-pre';
        pre.textContent = loadingText;
        return pre;
    }

    function showConsoleOutput(run) {
        var pre = actionPre('Loading…');
        showActionPanel('Console (raw container output)', pre);
        fetch('/api/runs/' + encodeURIComponent(run.id) + '/console')
            .then(function (r) { return r.ok ? r.text() : Promise.reject(); })
            .then(function (text) { pre.textContent = text || '(empty)'; })
            .catch(function () { pre.textContent = 'No container output available.'; });
    }

    function showWorkflowInfo(run) {
        var pre = actionPre('Loading…');
        showActionPanel('Workflow: ' + run.workflow_name, pre);
        fetch('/api/projects/' + encodeURIComponent(state.activeSlug) + '/workflows')
            .then(function (r) { return r.json(); })
            .then(function (workflows) {
                var wf = null;
                (workflows || []).forEach(function (w) { if (w.name === run.workflow_name) wf = w; });
                if (!wf) { pre.textContent = 'Workflow definition not found.'; return; }
                var lines = ['Steps:'];
                (wf.steps || []).forEach(function (s) { lines.push('  ' + s.name + ' (' + s.type + ')'); });
                lines.push('', 'Wires:');
                (wf.wires || []).forEach(function (wr) { lines.push('  ' + wr.from + ' :' + wr.result + ' -> ' + wr.to); });
                pre.textContent = lines.join('\n');
            })
            .catch(function () { pre.textContent = 'Failed to load workflow.'; });
    }

    function showBranch(run) {
        var pre = actionPre('Loading…');
        showActionPanel('Result branch', pre);
        fetch('/api/runs/' + encodeURIComponent(run.id) + '/branch')
            .then(function (r) { return r.json(); })
            .then(function (data) {
                var branches = data.branches || [];
                if (!branches.length) { pre.textContent = 'No result branch recorded for this run.'; return; }
                pre.textContent = branches.map(function (b) {
                    return (b.repo ? b.repo + ': ' : '') + b.branch;
                }).join('\n');
            })
            .catch(function () { pre.textContent = 'Failed to load branch info.'; });
    }

    function showDiff(run) {
        var pre = actionPre('Loading…');
        pre.classList.add('console-action-diff');
        showActionPanel('Diff', pre);
        fetch('/api/runs/' + encodeURIComponent(run.id) + '/diff')
            .then(function (r) { return r.ok ? r.text() : Promise.reject(); })
            .then(function (text) { pre.textContent = text || '(no changes)'; })
            .catch(function () { pre.textContent = 'No diff available for this run.'; });
    }

    function cancelRun(run) {
        fetch('/api/runs/' + encodeURIComponent(run.id) + '/stop', { method: 'POST' })
            .then(function () { return fetchRunDetail(run.id); })
            .then(function (fresh) { if (detail && detail.run && detail.run.id === run.id) applyRunDetail(fresh); })
            .catch(function () {});
    }

    function deleteContainerForRun(run) {
        fetch('/api/runs/' + encodeURIComponent(run.id) + '/container', { method: 'DELETE' })
            .then(function () { return fetchRunDetail(run.id); })
            .then(function (fresh) { if (detail && detail.run && detail.run.id === run.id) applyRunDetail(fresh); })
            .catch(function () {});
    }

    // ---------- centre pane: facts row + attempt chips ----------

    // renderFactsRow builds the merged key/value block: at most two dt/dd
    // rows (a leading "run"/"attempt" row with the core run facts, and a
    // "status" row folding in retry reason/error/timing), so the header
    // never grows past two rows before the step strip regardless of how
    // many facts a run has. Keys render in --mu (dt), values in --tx (dd,
    // the block's inherited text color) — see .console-facts-row.
    function renderFactsRow(run) {
        var container = document.getElementById('console-facts-block');
        if (!container) return;
        container.innerHTML = '';

        factRowsForRun(run).forEach(function (row) {
            appendFactRow(container, row.label, row.text, row.chips);
        });
    }

    // appendFactRow adds one dt/dd pair to a .console-facts-row container.
    // chips (when given) are interactive nodes (e.g. attempt tabs) prepended
    // to the value cell ahead of the plain-text fact summary.
    function appendFactRow(container, label, text, chips) {
        var dt = document.createElement('dt');
        dt.textContent = label;
        container.appendChild(dt);

        var dd = document.createElement('dd');
        if (chips) {
            dd.appendChild(chips);
            if (text) dd.appendChild(document.createTextNode(' · '));
        }
        if (text) dd.appendChild(document.createTextNode(text));
        container.appendChild(dd);
    }

    // renderAttemptChips renders the "1 s6hh 2 l0xa 3 vtmf" chip group —
    // bordered chips, current attempt amber-filled (.console-attempt-chip-on),
    // failed attempts red (.console-attempt-chip-bad). Chips stay clickable
    // for switching and the [ / ] keybinding still calls
    // selectAttempt/switchAttempt. Unchanged from the prior facts-row
    // rendering — just relocated into the merged block's lead row.
    function renderAttemptChips() {
        var chips = document.createElement('span');
        chips.className = 'console-attempt-chips';
        detail.attempts.forEach(function (a, i) {
            var chip = document.createElement('button');
            chip.type = 'button';
            chip.className = 'console-attempt-chip';
            if (i === detail.attemptIndex) chip.classList.add('console-attempt-chip-on');
            if (a.outcome === 'failed') chip.classList.add('console-attempt-chip-bad');
            chip.textContent = a.attempt_num + ' ' + shortId(a.run_id || '');
            chip.title = (a.run_id || '') + ' · ' + (a.outcome || '') + (a.duration ? ' · ' + a.duration : '');
            chip.addEventListener('click', function () { selectAttempt(i); });
            chips.appendChild(chip);
        });
        return chips;
    }

    // factRowsForRun collapses every run fact into the lead row and the
    // retry reason/error/timing into the status row — exactly two dt/dd
    // rows, no matter how many individual facts a run carries.
    function factRowsForRun(run) {
        if (!run) return [{ label: 'status', text: 'Loading…' }];

        var multiAttempt = detail.attempts.length > 1;
        var leadParts = [];
        if (!multiAttempt) leadParts.push(run.id);
        if (run.child_runs && run.child_runs.length) {
            leadParts.push('child ' + run.child_runs.map(function (c) { return c.id; }).join(', '));
        }
        leadParts.push('container ' + (run.container_id ? shortId(run.container_id) : '—') + ' (' + (run.container_state || 'removed') + ')');
        if (run.token_usage && run.token_usage.length) {
            leadParts.push('tokens ' + run.token_usage.map(function (u) {
                return u.agent_name + ': ' + u.input_tokens + '/' + u.output_tokens;
            }).join(', '));
        }
        if (run.prompt_file) {
            leadParts.push('prompt ' + run.prompt_file + (run.git_revision ? ' @ ' + run.git_revision : ''));
        }

        var rows = [{
            label: multiAttempt ? 'attempt' : 'run',
            chips: multiAttempt ? renderAttemptChips() : null,
            text: leadParts.join(' · ')
        }];

        var statusParts = [];
        var attempt = detail.attempts[detail.attemptIndex];
        if (attempt && attempt.retry_reason) {
            statusParts.push('retry reason: previous attempt failed at "' + attempt.retry_reason + '"');
        }
        if (run.error_message) {
            statusParts.push('error: ' + run.error_message);
        }
        statusParts.push('timing ' + (run.timing || '—'));
        rows.push({ label: 'status', text: statusParts.join(' · ') });

        return rows;
    }

    function shortId(id) {
        return id.length > 12 ? id.slice(0, 12) : id;
    }

    // ---------- centre pane: step strip ----------

    function renderStepStrip(run) {
        var container = document.getElementById('console-step-strip');
        if (!container) return;
        container.innerHTML = '';

        var steps = run.steps || [];
        if (!steps.length) {
            var empty = document.createElement('div');
            empty.className = 'console-centre-empty';
            empty.textContent = 'No steps recorded yet.';
            container.appendChild(empty);
            return;
        }

        var clusters = clusterSteps(steps);
        var focal = focalStep(clusters);

        clusters.forEach(function (cluster) {
            container.appendChild(renderStepSegment(cluster.step, false, cluster.children.length > 0, cluster.step === focal));
            cluster.children.forEach(function (child) {
                container.appendChild(renderStepSegment(child, true, false, child === focal));
            });
        });
    }

    // focalStep picks the one segment that gets a filled background and a
    // permanently visible duration: the running step if one is live
    // (parent or inlined child), else the first failed step in strip order.
    // Every other cell shows dot + name only, with its duration revealed on
    // hover/focus instead.
    function focalStep(clusters) {
        var flat = [];
        clusters.forEach(function (c) {
            flat.push(c.step);
            c.children.forEach(function (child) { flat.push(child); });
        });
        var running = flat.find(function (s) { return !s.result && !!s.started_at; });
        if (running) return running;
        return flat.find(function (s) { return s.result === 'fail' || s.result === 'error'; }) || null;
    }

    // clusterSteps groups the flattened step list (see flattenRun on the
    // server) into one cluster per depth-0 step, with any depth>0 steps from
    // a spawned child run collected as that cluster's children — steps
    // always arrive with a workflow step immediately followed by its
    // child-run steps, so a single sequential pass suffices.
    function clusterSteps(steps) {
        var clusters = [];
        var current = null;
        steps.forEach(function (s) {
            if (s.depth === 0) {
                current = { step: s, children: [] };
                clusters.push(current);
            } else if (current) {
                current.children.push(s);
            }
        });
        return clusters;
    }

    // renderStepSegment builds one step-strip cell. Only the focal cell (see
    // focalStep) keeps a permanently visible duration and a filled
    // background — every other cell shows just dot + name, with its
    // duration surfaced via the title attribute (native tooltip) and a
    // hover/focus style that reveals the same .console-step-segment-meta
    // text (see style.css), so nothing is truncated to make room for it.
    function renderStepSegment(step, isChild, hasChildren, isFocal) {
        var seg = document.createElement('button');
        seg.type = 'button';
        seg.className = 'console-step-segment' + (isChild ? ' console-step-segment-child' : '');

        var isScoped = detail.scopedStep && detail.scopedStep.run_id === step.run_id && detail.scopedStep.step_name === step.step_name;
        var isLive = !step.result && !!step.started_at;
        if (isScoped) seg.classList.add('console-step-segment-selected');
        if (isLive) seg.classList.add('console-step-segment-live');
        if (isFocal) {
            seg.classList.add('console-step-segment-focal');
            seg.classList.add(isLive ? 'console-step-segment-focal-running' : 'console-step-segment-focal-failed');
        }

        var name = document.createElement('span');
        name.className = 'console-step-segment-name';

        var dot = document.createElement('span');
        dot.className = 'run-dot ' + stepDotClass(step);
        name.appendChild(dot);

        var nameText = document.createElement('span');
        nameText.textContent = step.step_name + (hasChildren ? ' ↳' : '');
        name.appendChild(nameText);

        seg.appendChild(name);

        var metaText = step.duration || (isLive ? 'running…' : '');
        if (step.poll_count) {
            metaText += (metaText ? ' · ' : '') + '⟳' + step.poll_count + ' · ' + step.last_poll_at;
        }
        var meta = document.createElement('span');
        meta.className = 'console-step-segment-meta';
        meta.textContent = metaText;
        seg.appendChild(meta);

        if (!isFocal && metaText) seg.title = metaText;

        seg.addEventListener('click', function () { toggleStepScope(step); });
        return seg;
    }

    function stepDotClass(step) {
        if (step.result === 'parked') return 'run-dot-parked';
        if (!step.result) return 'run-dot-running';
        if (step.result === 'fail' || step.result === 'error') return 'run-dot-failed';
        return 'run-dot-succeeded';
    }

    function toggleStepScope(step) {
        if (!detail) return;
        var key = step.run_id + ':' + step.step_name;
        var current = detail.scopedStep ? (detail.scopedStep.run_id + ':' + detail.scopedStep.step_name) : null;
        if (current === key) {
            clearStepScope();
            return;
        }
        scopeToStep(step);
    }

    function scopeToStep(step) {
        if (!detail) return;
        detail.scopedStep = { run_id: step.run_id, step_name: step.step_name };
        if (detail.run) renderStepStrip(detail.run);
        loadStepOutput();
    }

    // switchStep moves the scoped step by delta through the flattened step
    // list (see flattenRun on the server), which is already in strip order —
    // a workflow step immediately followed by its child-run steps — so this
    // walks it directly rather than re-deriving strip order via
    // clusterSteps. Wraps at both ends; with nothing scoped yet, ']' starts
    // at the first step and '[' at the last, same as wrapping from an
    // implicit "before the start" position.
    function switchStep(delta) {
        if (!detail || !detail.run) return;
        var steps = detail.run.steps || [];
        if (!steps.length) return;
        var currentIndex = -1;
        if (detail.scopedStep) {
            for (var i = 0; i < steps.length; i++) {
                if (steps[i].run_id === detail.scopedStep.run_id && steps[i].step_name === detail.scopedStep.step_name) {
                    currentIndex = i;
                    break;
                }
            }
        }
        var next = ((currentIndex + delta) % steps.length + steps.length) % steps.length;
        scopeToStep(steps[next]);
    }

    function clearStepScope() {
        if (!detail) return;
        detail.scopedStep = null;
        detail.stepLines = [];
        if (detail.run) renderStepStrip(detail.run);
        updateLogScopeIndicator();
        renderLogLines();
    }

    function loadStepOutput() {
        if (!detail || !detail.scopedStep) return;
        var scope = detail.scopedStep;
        var url = '/api/runs/' + encodeURIComponent(scope.run_id) + '/steps/' + encodeURIComponent(scope.step_name) + '/output';
        if (detail.logTypeFilter === 'llm' || detail.logTypeFilter === 'script') {
            url += '?type=' + detail.logTypeFilter;
        }
        detail.stepLines = [];
        updateLogScopeIndicator();
        renderLogLines();
        fetch(url)
            .then(function (r) { return r.ok ? r.text() : Promise.reject(); })
            .then(function (text) {
                if (!detail || !detail.scopedStep || detail.scopedStep.run_id !== scope.run_id || detail.scopedStep.step_name !== scope.step_name) return;
                var lineType = detail.logTypeFilter === 'all' ? 'script' : detail.logTypeFilter;
                detail.stepLines = text.split('\n').filter(function (l) { return l.length > 0; }).map(function (l) {
                    return { timestamp: '', type: lineType, step_name: scope.step_name, content: l };
                });
                renderLogLines();
            })
            .catch(function () {
                if (!detail || !detail.scopedStep) return;
                detail.stepLines = [{ timestamp: '', type: 'status', step_name: scope.step_name, content: 'No output available for this step.' }];
                renderLogLines();
            });
    }

    // ---------- centre pane: parked thread panel ----------

    // showThreadPanel reveals the help-thread transcript + reply box in
    // place of the log pane, for a run parked awaiting a reply (see
    // computeHeaderState). Loads the transcript once per run id; the detail
    // poll (see manageDetailPoll) re-renders the header/facts/step-strip
    // around it as the parked run's state changes, but doesn't need to
    // reload the transcript on every tick.
    function showThreadPanel(run) {
        var panel = document.getElementById('console-thread-panel');
        var logPane = document.getElementById('console-log-pane');
        if (!panel || !logPane) return;
        logPane.hidden = true;
        panel.hidden = false;

        if (detail.threadLoadedFor === run.id) return;
        loadThreadPanel(run);
    }

    function hideThreadPanel() {
        var panel = document.getElementById('console-thread-panel');
        var logPane = document.getElementById('console-log-pane');
        if (panel) panel.hidden = true;
        if (logPane) logPane.hidden = false;
        if (detail) detail.threadLoadedFor = '';
    }

    function loadThreadPanel(run) {
        var panel = document.getElementById('console-thread-panel');
        panel.innerHTML = '';
        var loading = document.createElement('div');
        loading.className = 'console-centre-empty';
        loading.textContent = 'Loading thread…';
        panel.appendChild(loading);

        fetch('/api/runs/' + encodeURIComponent(run.id) + '/thread')
            .then(function (r) {
                if (!r.ok) throw new Error('thread not available');
                return r.json();
            })
            .then(function (thread) {
                if (!detail || !detail.run || detail.run.id !== run.id) return; // stale — user navigated away
                detail.threadLoadedFor = run.id;
                renderThreadPanel(thread, run);
            })
            .catch(function () {
                if (!detail || !detail.run || detail.run.id !== run.id) return;
                panel.innerHTML = '';
                var err = document.createElement('div');
                err.className = 'console-centre-empty';
                err.textContent = 'Failed to load the help thread for this run.';
                panel.appendChild(err);
            });
    }

    function renderThreadPanel(thread, run) {
        var panel = document.getElementById('console-thread-panel');
        panel.innerHTML = '';

        var head = document.createElement('div');
        head.className = 'console-thread-head';
        var title = document.createElement('h3');
        title.textContent = thread.title || 'Help thread';
        head.appendChild(title);
        var meta = document.createElement('span');
        meta.className = 'console-thread-meta';
        var metaBits = ['Parked ' + formatDuration(run.parked_seconds || 0)];
        if (thread.address) metaBits.push(thread.address);
        meta.textContent = metaBits.join(' · ');
        head.appendChild(meta);
        panel.appendChild(head);

        var transcript = document.createElement('div');
        transcript.className = 'console-thread-transcript';
        (thread.messages || []).forEach(function (m) {
            transcript.appendChild(renderThreadMessage(m));
        });
        panel.appendChild(transcript);

        panel.appendChild(renderThreadReplyBox(run));
    }

    function renderThreadMessage(m) {
        var row = document.createElement('div');
        row.className = 'console-thread-message console-thread-message-' + (m.author === 'user' ? 'user' : 'agent');

        var meta = document.createElement('div');
        meta.className = 'console-thread-message-meta';
        meta.textContent = (m.author === 'user' ? 'You' : 'Agent') + ' · ' + formatTimestamp(m.created_at);
        row.appendChild(meta);

        var body = document.createElement('div');
        body.className = 'console-thread-message-body';
        body.textContent = m.body;
        row.appendChild(body);

        if (m.options && m.options.length) {
            var opts = document.createElement('div');
            opts.className = 'console-thread-message-options';
            opts.textContent = 'Options: ' + m.options.join(', ');
            row.appendChild(opts);
        }

        return row;
    }

    // renderThreadReplyBox builds the reply form. Submitting posts through
    // /api/runs/{id}/thread/reply, which the daemon serves via the same
    // ReplyThread RPC `cloche threads reply` uses (see cmd/cloched wiring),
    // so the run resumes exactly as it would from the CLI. The detail poll
    // (still active while state === 'parked') picks up the resulting state
    // change on its next tick, no manual refresh needed here — a
    // best-effort immediate refresh just makes it feel instant.
    function renderThreadReplyBox(run) {
        var form = document.createElement('form');
        form.className = 'console-thread-reply';

        var textarea = document.createElement('textarea');
        textarea.className = 'console-thread-reply-input';
        textarea.rows = 3;
        textarea.placeholder = 'Reply to resume the run…';
        form.appendChild(textarea);

        var row = document.createElement('div');
        row.className = 'console-thread-reply-row';
        var err = document.createElement('span');
        err.className = 'console-thread-reply-error';
        row.appendChild(err);
        var send = document.createElement('button');
        send.type = 'submit';
        send.className = 'btn btn-sm btn-primary';
        send.textContent = 'Reply';
        row.appendChild(send);
        form.appendChild(row);

        form.addEventListener('submit', function (e) {
            e.preventDefault();
            var body = textarea.value.trim();
            if (!body || send.disabled) return;
            send.disabled = true;
            send.textContent = 'Sending…';
            err.textContent = '';
            fetch('/api/runs/' + encodeURIComponent(run.id) + '/thread/reply', {
                method: 'POST',
                headers: { 'Content-Type': 'application/json' },
                body: JSON.stringify({ body: body })
            }).then(function (r) {
                if (!r.ok) throw new Error('reply failed');
                textarea.value = '';
                send.disabled = false;
                send.textContent = 'Reply';
                return fetchRunDetail(run.id);
            }).then(function (fresh) {
                if (detail && detail.run && detail.run.id === run.id) applyRunDetail(fresh);
            }).catch(function () {
                send.disabled = false;
                send.textContent = 'Reply';
                err.textContent = 'Failed to send reply. Try again.';
            });
        });

        return form;
    }

    // ---------- centre pane: log pane ----------

    // renderLogArea renders whichever log view is currently active for the
    // selected attempt: the compare view (needs-you compare-kind tasks,
    // unless toggled off with 'c') or the ordinary single-attempt streaming
    // log. Always stops any live stream first, since switching modes (or
    // attempts) must not leave a stale EventSource running in the background.
    function renderLogArea() {
        stopLogStream();
        if (detail.compareMode) {
            renderCompareView();
            return;
        }
        renderLogPaneShell();
        var attempt = detail.attempts[detail.attemptIndex];
        if (!attempt) return;
        if (attempt.attempt_id) {
            startDetailLogStream('attempt', attempt.attempt_id);
        } else {
            startDetailLogStream('run', attempt.run_id);
        }
    }

    // toggleCompareView is bound to 'c': flips between the needs-you compare
    // view and the ordinary single-attempt log for the rest of this task's
    // session. No-op for tasks that never had a compare view to begin with.
    function toggleCompareView() {
        if (!detail || !detail.entry || !isNeedsYouCompareKind(detail.entry.kind)) return;
        detail.compareMode = !detail.compareMode;
        renderLogArea();
    }

    // renderCompareView renders one column per failed attempt (newest three,
    // oldest of the three first) inside the log-pane region, each showing
    // the failing step's log trimmed to start at its first failure-looking
    // line — see alignToFirstFailureLine. Falls back to the last up-to-three
    // attempts, failed or not, if none recorded a failing step (e.g. a
    // stale-claim task where the loop simply never came back to reclaim it).
    function renderCompareView() {
        var el = document.getElementById('console-log-pane');
        if (!el) return;
        el.innerHTML = '';

        var toolbar = document.createElement('div');
        toolbar.className = 'console-log-toolbar';
        var label = document.createElement('span');
        label.className = 'badge badge-failed';
        label.textContent = 'Compare view — press c for the single-attempt log';
        toolbar.appendChild(label);
        el.appendChild(toolbar);

        var columnsWrap = document.createElement('div');
        columnsWrap.className = 'console-compare-columns';
        el.appendChild(columnsWrap);

        var attempts = detail.attempts || [];
        var picked = [];
        for (var i = attempts.length - 1; i >= 0 && picked.length < 3; i--) {
            if (attempts[i].failed_step) picked.unshift(attempts[i]);
        }
        if (!picked.length) {
            picked = attempts.slice(Math.max(0, attempts.length - 3));
        }

        if (!picked.length) {
            var empty = document.createElement('div');
            empty.className = 'console-centre-empty';
            empty.textContent = 'No attempts to compare yet.';
            columnsWrap.appendChild(empty);
            return;
        }

        picked.forEach(function (attempt) { columnsWrap.appendChild(buildCompareColumn(attempt)); });
    }

    function buildCompareColumn(attempt) {
        var col = document.createElement('div');
        col.className = 'console-compare-column';

        var head = document.createElement('div');
        head.className = 'console-compare-column-head';
        var headText = '#' + attempt.attempt_num;
        if (attempt.failed_step) headText += ' · ' + attempt.failed_step;
        if (attempt.outcome) headText += ' · ' + attempt.outcome;
        head.textContent = headText;
        col.appendChild(head);

        var pre = document.createElement('pre');
        pre.className = 'console-compare-column-log';

        if (!attempt.failed_step) {
            pre.textContent = 'This attempt did not fail.';
        } else {
            pre.textContent = 'Loading…';
            fetch('/api/runs/' + encodeURIComponent(attempt.run_id) + '/steps/' + encodeURIComponent(attempt.failed_step) + '/output')
                .then(function (r) { return r.ok ? r.text() : Promise.reject(); })
                .then(function (text) { pre.textContent = alignToFirstFailureLine(text) || '(empty)'; })
                .catch(function () { pre.textContent = 'No output available for this step.'; });
        }

        col.appendChild(pre);
        return col;
    }

    // alignToFirstFailureLine trims a step's log to start at the first line
    // that looks like a failure indicator, so the compare columns line up on
    // their respective failures rather than on unrelated leading output.
    // Falls back to the full text when no such line is found.
    function alignToFirstFailureLine(text) {
        var lines = text.split('\n');
        var markerRe = /error|exception|panic|traceback|fail(ed|ure)?|fatal/i;
        for (var i = 0; i < lines.length; i++) {
            if (markerRe.test(lines[i])) return lines.slice(i).join('\n');
        }
        return text;
    }

    // renderLogPaneShell builds the flat log status band (scope, type
    // filter chips, live/follow indicator, and right-aligned meta) plus the
    // scrolling log viewer beneath it. See docs/design/console-restructured-
    // mock.html .m5 .logbar for the reference layout — a status band, not
    // form controls.
    function renderLogPaneShell() {
        var el = document.getElementById('console-log-pane');
        if (!el) return;
        el.innerHTML = '';

        var logbar = document.createElement('div');
        logbar.className = 'console-logbar';

        var scope = document.createElement('button');
        scope.type = 'button';
        scope.id = 'console-log-scope';
        scope.className = 'console-logbar-scope';
        scope.addEventListener('click', clearStepScope);
        logbar.appendChild(scope);

        var chips = document.createElement('span');
        chips.className = 'console-logbar-chips';
        ['all', 'llm', 'script', 'status'].forEach(function (t) {
            var chip = document.createElement('button');
            chip.type = 'button';
            chip.className = 'console-logbar-chip' + (detail.logTypeFilter === t ? ' is-active' : '');
            chip.textContent = t;
            chip.addEventListener('click', function () {
                detail.logTypeFilter = t;
                Array.prototype.forEach.call(chips.children, function (c) {
                    c.classList.toggle('is-active', c === chip);
                });
                if (detail.scopedStep) {
                    loadStepOutput();
                } else {
                    renderLogLines();
                }
            });
            chips.appendChild(chip);
        });
        logbar.appendChild(chips);

        var live = document.createElement('button');
        live.type = 'button';
        live.id = 'console-log-status';
        live.className = 'console-logbar-live';
        live.title = 'Toggle follow';
        live.addEventListener('click', toggleLogFollow);
        logbar.appendChild(live);

        var right = document.createElement('span');
        right.className = 'console-logbar-right';

        var count = document.createElement('span');
        count.id = 'console-log-count';
        right.appendChild(count);

        var wrapToggle = document.createElement('button');
        wrapToggle.type = 'button';
        wrapToggle.id = 'console-log-wrap-toggle';
        wrapToggle.className = 'console-logbar-action';
        wrapToggle.textContent = 'wrap: ' + (detail.logWrap ? 'on' : 'off');
        wrapToggle.addEventListener('click', function () {
            detail.logWrap = !detail.logWrap;
            wrapToggle.textContent = 'wrap: ' + (detail.logWrap ? 'on' : 'off');
            applyLogWrap();
        });
        right.appendChild(wrapToggle);

        var hints = document.createElement('span');
        hints.className = 'console-logbar-hints';
        var gKbd = document.createElement('kbd');
        gKbd.textContent = 'g';
        var bigGKbd = document.createElement('kbd');
        bigGKbd.textContent = 'G';
        hints.appendChild(gKbd);
        hints.appendChild(document.createTextNode(' top '));
        hints.appendChild(bigGKbd);
        hints.appendChild(document.createTextNode(' end'));
        right.appendChild(hints);

        logbar.appendChild(right);
        el.appendChild(logbar);

        var viewer = document.createElement('div');
        viewer.className = 'log-viewer console-log-viewer';
        viewer.id = 'console-log-viewer';

        var earlierBtn = document.createElement('button');
        earlierBtn.type = 'button';
        earlierBtn.id = 'console-log-earlier';
        earlierBtn.className = 'load-earlier-btn';
        earlierBtn.textContent = 'Load earlier';
        earlierBtn.hidden = true;
        earlierBtn.addEventListener('click', loadEarlierDetailLogs);
        viewer.appendChild(earlierBtn);

        var pre = document.createElement('pre');
        pre.id = 'console-log-content';
        viewer.appendChild(pre);

        el.appendChild(viewer);

        applyLogWrap();
        setLogStatus(detail.logStatus || 'live');
        updateLogScopeIndicator();
        updateLogCount();
    }

    function applyLogWrap() {
        var pre = document.getElementById('console-log-content');
        if (pre) pre.style.whiteSpace = detail.logWrap ? 'pre-wrap' : 'pre';
    }

    function updateLogScopeIndicator() {
        var el = document.getElementById('console-log-scope');
        if (el) {
            if (detail.scopedStep) {
                el.textContent = 'log · ' + detail.scopedStep.step_name + ' ✕';
                el.classList.add('is-scoped');
            } else {
                el.textContent = 'log';
                el.classList.remove('is-scoped');
            }
        }
        var earlierBtn = document.getElementById('console-log-earlier');
        if (earlierBtn) earlierBtn.hidden = !!detail.scopedStep || !detail.logSkipped;
    }

    // setLogStatus updates the live/follow indicator in the log bar. Text
    // doubles as the control for toggleLogFollow (see the click handler in
    // renderLogPaneShell), so it always reflects both stream state and
    // whether new lines are being auto-scrolled into view.
    function setLogStatus(status) {
        if (detail) detail.logStatus = status;
        var el = document.getElementById('console-log-status');
        if (!el) return;
        if (status === 'live') {
            el.textContent = '● live' + (detail && detail.logFollow ? ' · following' : '');
            el.className = 'console-logbar-live';
        } else if (status === 'complete') {
            el.textContent = 'complete';
            el.className = 'console-logbar-live console-logbar-done';
        } else if (status === 'disconnected') {
            el.textContent = 'disconnected';
            el.className = 'console-logbar-live console-logbar-disconnected';
        }
    }

    function toggleLogFollow() {
        if (!detail) return;
        detail.logFollow = !detail.logFollow;
        setLogStatus(detail.logStatus);
        if (detail.logFollow) scrollLogTo('bottom');
    }

    // updateLogCount refreshes the right-aligned line count in the log bar
    // to match whatever is currently displayed (scoped step output, or the
    // full stream filtered by type).
    function updateLogCount() {
        var el = document.getElementById('console-log-count');
        if (!el || !detail) return;
        var lines = detail.scopedStep ? detail.stepLines : detail.allLines;
        var filter = detail.logTypeFilter;
        var n = (lines || []).filter(function (l) { return filter === 'all' || l.type === filter; }).length;
        el.textContent = n.toLocaleString() + (n === 1 ? ' line' : ' lines');
    }

    function startDetailLogStream(kind, id) {
        stopLogStream();
        detail.logKind = kind;
        detail.logId = id;
        detail.allLines = [];
        detail.logSkipped = 0;
        renderLogLines();
        updateLogScopeIndicator();
        setLogStatus('live');

        var base = kind === 'attempt' ? '/api/attempts/' : '/api/runs/';
        var es = new EventSource(base + encodeURIComponent(id) + '/stream');
        detail.eventSource = es;

        es.onmessage = function (e) {
            setLogStatus('live');
            try {
                appendLine(JSON.parse(e.data));
            } catch (err) { /* ignore malformed event */ }
        };
        es.addEventListener('meta', function (e) {
            try {
                var meta = JSON.parse(e.data);
                detail.logSkipped = meta.skipped || 0;
                updateLogScopeIndicator();
            } catch (err) { /* ignore malformed event */ }
        });
        es.addEventListener('done', function () {
            setLogStatus('complete');
            es.close();
        });
        // A dropped connection is not completion — the run may still be
        // going. Only an explicit "done" event means the stream is over.
        es.onerror = function () {
            setLogStatus('disconnected');
        };
    }

    function stopLogStream() {
        if (detail && detail.eventSource) {
            detail.eventSource.close();
            detail.eventSource = null;
        }
    }

    function loadEarlierDetailLogs() {
        if (!detail || !detail.logId || !detail.logSkipped) return;
        var btn = document.getElementById('console-log-earlier');
        if (btn) { btn.disabled = true; btn.textContent = 'Loading…'; }

        var base = detail.logKind === 'attempt' ? '/api/attempts/' : '/api/runs/';
        var end = detail.logSkipped;
        fetch(base + encodeURIComponent(detail.logId) + '/logs?end=' + end + '&limit=1000')
            .then(function (r) { return r.json(); })
            .then(function (data) {
                var lines = data.lines || [];
                var visible = lines.filter(function (l) { return detail.logTypeFilter === 'all' || l.type === detail.logTypeFilter; });
                detail.allLines = lines.concat(detail.allLines);
                detail.logSkipped = data.start || 0;

                var viewer = document.getElementById('console-log-viewer');
                var pre = document.getElementById('console-log-content');
                if (viewer && pre) {
                    var prevHeight = viewer.scrollHeight;
                    var frag = document.createDocumentFragment();
                    var lastDate = '';
                    visible.forEach(function (l) {
                        var t = logTimeParts(l.timestamp);
                        if (t && lastDate && t.date !== lastDate) {
                            frag.appendChild(buildDateDividerEl(t.date));
                        }
                        if (t) lastDate = t.date;
                        frag.appendChild(buildLogLineEl(l));
                    });
                    pre.insertBefore(frag, pre.firstChild);
                    viewer.scrollTop += viewer.scrollHeight - prevHeight;
                }
                updateLogScopeIndicator();
                updateLogCount();
                if (btn) { btn.disabled = false; btn.textContent = 'Load earlier'; }
            })
            .catch(function () {
                if (btn) { btn.disabled = false; btn.textContent = 'Load earlier'; }
            });
    }

    function appendLine(line) {
        if (!detail) return;
        detail.allLines.push(line);
        if (detail.scopedStep) return;
        if (detail.logTypeFilter !== 'all' && line.type !== detail.logTypeFilter) return;
        var pre = document.getElementById('console-log-content');
        if (!pre) return;
        appendLogLineEl(pre, line);
        updateLogCount();
        if (detail.logFollow) scrollLogTo('bottom');
    }

    // TOOL_LINE_RE matches the "--- Tool: <call> ---" marker that
    // logstream.ParseClaudeLine emits for a tool_use content block (see
    // internal/logstream/parse.go formatToolCall); group 1 is the call
    // summary, e.g. "Bash('go test ./...')".
    var TOOL_LINE_RE = /^-+\s*Tool:\s*(.*?)\s*-+$/;

    // classifyLogLine assigns a semantic class to a line's CONTENT (never
    // the whole line — see buildLogLineEl) based on the line's own type and,
    // for status lines, the step-transition result cloche itself writes
    // ("step_completed: <name> -> <result>", see internal/host/runner.go and
    // internal/adapters/grpc/server.go). Script output is classified by a
    // few common pass/fail/warning markers. Returns '' for plain content,
    // which keeps the default dimmed-content colour.
    function classifyLogLine(line) {
        var type = line.type || 'script';
        var content = (line.content || '').trim();
        if (type === 'llm') {
            return TOOL_LINE_RE.test(content) ? 'tool' : 'l';
        }
        if (type === 'status') {
            if (/->\s*success\b/i.test(content)) return 'g';
            if (/->\s*(fail|error)\b/i.test(content)) return 'w';
            return 'st';
        }
        if (/\b(FAIL|ERROR|panic:|traceback)\b/i.test(content)) return 'b';
        if (/^ok\s+\S/.test(content) || /\b(succeeded|passed)\b/i.test(content)) return 'g';
        if (/\bwarn(ing)?\b/i.test(content)) return 'w';
        return '';
    }

    // STEP_STARTED_RE matches the "step_started: <name>" status content
    // cloche itself writes at the top of every step (see
    // internal/host/runner.go / internal/adapters/grpc/server.go, same
    // family as the "step_completed: <name> -> <result>" line classifyLogLine
    // matches above). Lines matching it render as a section rule instead of
    // an ordinary line — see buildSectionRuleEl.
    var STEP_STARTED_RE = /^step_started\b:?\s*(.*)$/i;

    // logTimeParts splits a "2026-09-16T20:04:26Z"-shaped timestamp into its
    // date and time-of-day, without going through Date/toLocaleString —
    // those convert to the viewer's local timezone, which would make the
    // rendered time (and the date-divider boundary) depend on where the
    // browser happens to be rather than on the log's own clock. Returns null
    // for anything that doesn't look like that shape (empty scoped-step
    // timestamps, malformed data).
    function logTimeParts(iso) {
        var m = typeof iso === 'string' && /^(\d{4}-\d{2}-\d{2})T(\d{2}:\d{2}:\d{2})/.exec(iso);
        return m ? { date: m[1], time: m[2] } : null;
    }

    // buildDateDividerEl renders a one-line divider marking a date change
    // between consecutive log lines (see appendLogLineEl / renderLogLines).
    function buildDateDividerEl(dateStr) {
        var el = document.createElement('span');
        el.className = 'log-date-divider';
        el.textContent = dateStr;
        el.appendChild(document.createTextNode('\n'));
        return el;
    }

    // buildSectionRuleEl renders a "step_started" status line as a section
    // break — a hairline above and just the step name — rather than the
    // ordinary [time] [type] (step) content line, so a long run's log reads
    // as a sequence of step sections instead of one undifferentiated stream.
    function buildSectionRuleEl(line, content) {
        var span = document.createElement('span');
        span.className = 'log-line log-line-section log-type-status';
        var m = STEP_STARTED_RE.exec(content);
        span.textContent = line.step_name || (m && m[1]) || content;
        span.appendChild(document.createTextNode('\n'));
        return span;
    }

    // buildLogLineEl renders one log line as a dimmed prefix span (timestamp
    // + type + step) followed by a separately-classed content span — never
    // one whole-line colour (see classifyLogLine). step_started status lines
    // are the exception — see buildSectionRuleEl.
    function buildLogLineEl(line) {
        var content = (line.content || '').trim();
        if ((line.type || 'script') === 'status' && STEP_STARTED_RE.test(content)) {
            return buildSectionRuleEl(line, content);
        }

        var span = document.createElement('span');
        span.className = 'log-line log-type-' + (line.type || 'script');

        var t = logTimeParts(line.timestamp);
        var prefixParts = [];
        if (line.timestamp) prefixParts.push('[' + (t ? t.time : line.timestamp) + ']');
        prefixParts.push('[' + (line.type || '') + ']');
        if (!detail.scopedStep && line.step_name) prefixParts.push('(' + line.step_name + ')');
        var prefix = document.createElement('span');
        prefix.className = 'log-line-prefix';
        if (line.timestamp) prefix.title = line.timestamp;
        prefix.textContent = prefixParts.join(' ') + ' ';
        span.appendChild(prefix);

        var cls = classifyLogLine(line);
        var contentEl = document.createElement('span');
        if (cls === 'tool') {
            var m = TOOL_LINE_RE.exec(content);
            contentEl.className = 'log-line-content log-line-tool';
            contentEl.textContent = '⚙ ' + (m ? m[1] : content);
        } else {
            contentEl.className = 'log-line-content' + (cls ? ' log-line-' + cls : '');
            contentEl.textContent = line.content || '';
        }
        span.appendChild(contentEl);
        span.appendChild(document.createTextNode('\n'));
        return span;
    }

    // appendLogLineEl appends one line to a live-rendered log <pre>,
    // inserting a date divider first when the line's date differs from the
    // last one rendered into this container (tracked via a dataset
    // attribute so it survives across separate appendLine calls).
    function appendLogLineEl(pre, line) {
        var t = logTimeParts(line.timestamp);
        if (t && pre.dataset.lastLogDate && t.date !== pre.dataset.lastLogDate) {
            pre.appendChild(buildDateDividerEl(t.date));
        }
        pre.appendChild(buildLogLineEl(line));
        if (t) pre.dataset.lastLogDate = t.date;
    }

    function renderLogLines() {
        var pre = document.getElementById('console-log-content');
        if (!pre || !detail) return;
        pre.innerHTML = '';
        delete pre.dataset.lastLogDate;
        var lines = detail.scopedStep ? detail.stepLines : detail.allLines;
        var filter = detail.logTypeFilter;
        var frag = document.createDocumentFragment();
        var lastDate = '';
        (lines || []).forEach(function (line) {
            if (filter !== 'all' && line.type !== filter) return;
            var t = logTimeParts(line.timestamp);
            if (t && lastDate && t.date !== lastDate) {
                frag.appendChild(buildDateDividerEl(t.date));
            }
            if (t) lastDate = t.date;
            frag.appendChild(buildLogLineEl(line));
        });
        pre.appendChild(frag);
        pre.dataset.lastLogDate = lastDate;
        updateLogCount();
        if (detail.logFollow) scrollLogTo('bottom');
    }

    function scrollLogTo(where) {
        var viewer = document.getElementById('console-log-viewer');
        if (!viewer) return;
        viewer.scrollTop = where === 'top' ? 0 : viewer.scrollHeight;
    }

    // ---------- secondary views (Workflows / Requirements / Containers) ----------
    //
    // These are opened from the header (buttons + w/i/c shortcuts), not
    // routed pages — the console shell stays on the current project/task URL
    // while a view is open. Escape closes the topmost drawer, then the view
    // itself (see the keydown handler above).

    function isViewOpen() {
        return !document.getElementById('console-view-overlay').hidden;
    }

    function openView(title) {
        document.getElementById('console-view-title').textContent = title;
        document.getElementById('console-view-overlay').hidden = false;
    }

    function closeView() {
        document.getElementById('console-view-overlay').hidden = true;
        document.getElementById('console-view-body').innerHTML = '';
        closeStepDrawer();
        closeIntentDrawer();
        stopScanPolling();
    }

    function isDrawerOpen(id) {
        var el = document.getElementById(id);
        return !!el && el.classList.contains('drawer-open');
    }

    function anyDrawerOpen() {
        return isDrawerOpen('step-drawer') || isDrawerOpen('intent-drawer');
    }

    function closeStepDrawer() {
        document.getElementById('step-drawer').classList.remove('drawer-open');
    }

    function closeIntentDrawer() {
        document.getElementById('intent-drawer').classList.remove('drawer-open');
    }

    function updateViewButtonsEnabled() {
        var disabled = !state.activeSlug;
        ['console-view-workflows-btn', 'console-view-intent-btn', 'console-view-containers-btn', 'console-ledger-btn'].forEach(function (id) {
            document.getElementById(id).disabled = disabled;
        });
    }

    // The Views menu folds Workflows/Requirements/Containers/Ledger behind
    // one button, following the same open/close pattern as the idle-projects
    // menu (console-more-btn / console-idle-menu) above.
    function closeViewsMenu() {
        var menu = document.getElementById('console-views-menu');
        var btn = document.getElementById('console-views-btn');
        if (!menu.hidden) {
            menu.hidden = true;
            btn.setAttribute('aria-expanded', 'false');
        }
    }

    document.getElementById('console-views-btn').addEventListener('click', function (e) {
        e.stopPropagation();
        closeIdleMenu();
        var menu = document.getElementById('console-views-menu');
        var willOpen = menu.hidden;
        menu.hidden = !willOpen;
        this.setAttribute('aria-expanded', String(willOpen));
    });
    document.addEventListener('click', closeViewsMenu);

    document.getElementById('console-view-close').addEventListener('click', closeView);
    document.getElementById('step-drawer-close').addEventListener('click', closeStepDrawer);
    document.getElementById('intent-drawer-close').addEventListener('click', closeIntentDrawer);
    document.getElementById('console-view-workflows-btn').addEventListener('click', function () { closeViewsMenu(); openWorkflowsView(); });
    document.getElementById('console-view-intent-btn').addEventListener('click', function () { closeViewsMenu(); openIntentView(); });
    document.getElementById('console-view-containers-btn').addEventListener('click', function () { closeViewsMenu(); openContainersView(); });
    document.getElementById('console-ledger-btn').addEventListener('click', function () { closeViewsMenu(); toggleLedger(true); });
    updateViewButtonsEnabled();

    // ---------- Workflows view ----------

    // The DSL's real step config keys (see docs/workflows.md) — anything else
    // (e.g. the old "command"/"script" branches, which no parsed step ever
    // populates) is skipped rather than silently showing nothing useful.
    var DRAWER_CONFIG_KEYS = [
        'prompt', 'run', 'poll', 'interval', 'agent', 'agent_command',
        'agent_args', 'intent_tracking', 'workflow_name', 'max_attempts'
    ];

    var workflowsData = [];
    var activeLocation = 'container';

    function openWorkflowsView() {
        if (!state.activeSlug) return;
        openView('Workflows');
        var body = document.getElementById('console-view-body');
        body.innerHTML =
            '<div id="location-tabs" class="tab-bar"></div>' +
            '<div id="workflow-tabs" class="tab-bar"></div>' +
            '<div id="workflow-name-label"></div>' +
            '<div id="workflow-dag"></div>';
        document.getElementById('location-tabs').addEventListener('click', onLocationTabsClick);
        document.getElementById('workflow-tabs').addEventListener('click', onWorkflowTabsClick);
        document.getElementById('workflow-dag').addEventListener('click', onDagClick);
        loadWorkflows();
    }

    function onLocationTabsClick(e) {
        var btn = e.target.closest('[data-location]');
        if (btn) switchLocation(btn.getAttribute('data-location'));
    }

    function onWorkflowTabsClick(e) {
        var btn = e.target.closest('[data-workflow-index]');
        if (btn) showFilteredWorkflow(parseInt(btn.getAttribute('data-workflow-index'), 10));
    }

    function onDagClick(e) {
        var node = e.target.closest('[data-step]');
        if (node) openStepDrawer(node.getAttribute('data-workflow'), node.getAttribute('data-step'));
    }

    function loadWorkflows() {
        fetch('/api/projects/' + encodeURIComponent(state.activeSlug) + '/workflows')
            .then(function (r) { return r.json(); })
            .then(function (data) {
                workflowsData = data || [];
                if (workflowsData.length === 0) {
                    document.getElementById('workflow-dag').innerHTML = '<p class="empty">No workflows found</p>';
                    document.getElementById('location-tabs').innerHTML = '';
                    document.getElementById('workflow-tabs').innerHTML = '';
                    return;
                }

                var hasContainer = workflowsData.some(function (wf) { return wf.location === 'container'; });
                var hasHost = workflowsData.some(function (wf) { return wf.location === 'host'; });

                activeLocation = hasContainer ? 'container' : 'host';

                var locationTabs = document.getElementById('location-tabs');
                if (hasContainer && hasHost) {
                    locationTabs.innerHTML =
                        '<button type="button" class="tab-btn' + (activeLocation === 'host' ? ' tab-active' : '') +
                        '" data-location="host">Host</button>' +
                        '<button type="button" class="tab-btn' + (activeLocation === 'container' ? ' tab-active' : '') +
                        '" data-location="container">Container</button>';
                } else {
                    locationTabs.innerHTML = '';
                }

                renderWorkflowTabs();
            })
            .catch(function () {
                document.getElementById('workflow-dag').innerHTML = '<p class="empty">Failed to load workflows</p>';
            });
    }

    function switchLocation(loc) {
        activeLocation = loc;
        var btns = document.querySelectorAll('#location-tabs .tab-btn');
        Array.prototype.forEach.call(btns, function (b) {
            var active = b.getAttribute('data-location') === loc;
            b.classList.toggle('tab-active', active);
            if (active) scrollTabIntoView(b);
        });
        renderWorkflowTabs();
    }

    // Scrolls a tab button into view within its own scrollable .tab-bar
    // without dragging the rest of the page (e.g. the DAG below) along —
    // 'nearest' on both axes limits the scroll to whichever ancestor
    // actually needs to move.
    function scrollTabIntoView(btn) {
        if (btn && btn.scrollIntoView) {
            btn.scrollIntoView({ block: 'nearest', inline: 'nearest' });
        }
    }

    // list-tasks and main are the orchestration entry points, so they're
    // pinned first (in that order) ahead of a separator; everything else
    // follows sorted case-insensitively by name. See ConsoleTabs.orderWorkflowTabs.
    function orderedWorkflowsForActiveLocation() {
        var filtered = workflowsData.filter(function (wf) { return wf.location === activeLocation; });
        return ConsoleTabs.orderWorkflowTabs(filtered);
    }

    function renderWorkflowTabs() {
        var order = orderedWorkflowsForActiveLocation();
        var ordered = order.ordered;
        var tabs = document.getElementById('workflow-tabs');

        if (ordered.length === 0) {
            tabs.innerHTML = '';
            document.getElementById('workflow-dag').innerHTML = '<p class="empty">No ' + activeLocation + ' workflows found</p>';
            return;
        }

        if (ordered.length > 1) {
            var html = '';
            ordered.forEach(function (wf, i) {
                var badge = wf.builtin ? ' <span class="badge badge-cancelled">built-in</span>' : '';
                html += '<button type="button" class="tab-btn' + (i === 0 ? ' tab-active' : '') +
                    '" data-workflow-index="' + i + '">' + escapeHtml(wf.name) + badge + '</button>';
                if (order.specialCount > 0 && i === order.specialCount - 1 && i < ordered.length - 1) {
                    html += '<span class="tab-sep" aria-hidden="true"></span>';
                }
            });
            tabs.innerHTML = html;
        } else {
            tabs.innerHTML = '';
        }
        showFilteredWorkflow(0);
    }

    function showFilteredWorkflow(idx) {
        var ordered = orderedWorkflowsForActiveLocation().ordered;
        var btns = document.querySelectorAll('#workflow-tabs .tab-btn');
        Array.prototype.forEach.call(btns, function (b, i) { b.classList.toggle('tab-active', i === idx); });
        if (btns[idx]) scrollTabIntoView(btns[idx]);
        showWorkflow(ordered[idx]);
    }

    function showWorkflow(wf) {
        var dagEl = document.getElementById('workflow-dag');
        var nameLabel = document.getElementById('workflow-name-label');
        if (nameLabel) {
            nameLabel.innerHTML = wf.builtin ? '<span class="badge badge-cancelled">built-in</span>' : '';
        }

        if (!wf.steps || wf.steps.length === 0) {
            dagEl.innerHTML = '<p class="empty">No steps defined</p>';
            return;
        }

        var wires = (wf.wires || []).filter(function (w) { return !w.implicit; });

        function isSuccessResult(r) { return r === 'success' || r === 'ok' || r === 'done' || r === 'pass'; }
        function isFailureResult(r) { return r === 'failed' || r === 'fail' || r === 'error'; }

        var colorPalette = ['#2196f3', '#ffc107', '#e040fb', '#00bcd4', '#ff9800'];
        var otherColorIdx = 0;
        var resultColorCache = {};

        function resultColor(r) {
            if (resultColorCache[r]) return resultColorCache[r];
            if (isSuccessResult(r)) { resultColorCache[r] = '#4caf50'; }
            else if (isFailureResult(r)) { resultColorCache[r] = '#e53935'; }
            else {
                resultColorCache[r] = colorPalette[otherColorIdx % colorPalette.length];
                otherColorIdx++;
            }
            return resultColorCache[r];
        }

        wires.forEach(function (w) { resultColor(w.result); });

        var terminals = {};
        wires.forEach(function (wire) {
            if (wire.to === 'done' || wire.to === 'abort') terminals[wire.to] = true;
        });

        var adj = {};
        var revAdj = {};
        wires.forEach(function (wire) {
            if (!adj[wire.from]) adj[wire.from] = [];
            adj[wire.from].push(wire);
            if (!revAdj[wire.to]) revAdj[wire.to] = [];
            if (revAdj[wire.to].indexOf(wire.from) === -1) revAdj[wire.to].push(wire.from);
        });

        var stepNames = wf.steps.map(function (s) { return s.name; });

        var inDeg = {};
        stepNames.forEach(function (n) { inDeg[n] = 0; });
        wires.forEach(function (wire) {
            if (inDeg[wire.to] !== undefined) inDeg[wire.to]++;
        });

        var queue = [];
        stepNames.forEach(function (n) { if (inDeg[n] === 0) queue.push(n); });

        var topoOrder = [];
        while (queue.length > 0) {
            var n = queue.shift();
            topoOrder.push(n);
            (adj[n] || []).forEach(function (wire) {
                if (inDeg[wire.to] !== undefined) {
                    inDeg[wire.to]--;
                    if (inDeg[wire.to] === 0) queue.push(wire.to);
                }
            });
        }
        var topoVisited = {};
        topoOrder.forEach(function (n) { topoVisited[n] = true; });
        var bfsQ = topoOrder.slice();
        while (bfsQ.length > 0) {
            var bn = bfsQ.shift();
            (adj[bn] || []).forEach(function (wire) {
                if (!topoVisited[wire.to] && inDeg[wire.to] !== undefined) {
                    topoVisited[wire.to] = true;
                    topoOrder.push(wire.to);
                    bfsQ.push(wire.to);
                }
            });
        }
        stepNames.forEach(function (n) { if (!topoVisited[n]) { topoOrder.push(n); } });

        var layerOf = {};
        topoOrder.forEach(function (n) {
            var maxParent = -1;
            (revAdj[n] || []).forEach(function (p) {
                if (layerOf[p] !== undefined && layerOf[p] > maxParent) maxParent = layerOf[p];
            });
            layerOf[n] = maxParent + 1;
        });

        var maxLayer = 0;
        stepNames.forEach(function (n) { if (layerOf[n] > maxLayer) maxLayer = layerOf[n]; });

        var termLayer = maxLayer + 1;
        for (var t in terminals) { layerOf[t] = termLayer; }
        var numLayers = termLayer + 1;

        var layers = [];
        for (var i = 0; i < numLayers; i++) layers.push([]);
        topoOrder.forEach(function (n) { layers[layerOf[n]].push(n); });
        for (var t2 in terminals) { layers[termLayer].push(t2); }

        for (var pass = 0; pass < 4; pass++) {
            for (var li = 1; li < numLayers; li++) {
                var bary = {};
                layers[li].forEach(function (n) {
                    var pars = revAdj[n] || [];
                    var sum = 0, cnt = 0;
                    pars.forEach(function (p) {
                        var idx = layers[li - 1].indexOf(p);
                        if (idx >= 0) { sum += idx; cnt++; }
                    });
                    bary[n] = cnt > 0 ? sum / cnt : 999;
                });
                layers[li].sort(function (a, b) { return bary[a] - bary[b]; });
            }
            for (var li2 = numLayers - 2; li2 >= 0; li2--) {
                var baryB = {};
                layers[li2].forEach(function (n) {
                    var children = (adj[n] || []).map(function (w) { return w.to; });
                    var sum = 0, cnt = 0;
                    children.forEach(function (c) {
                        var idx = layers[li2 + 1].indexOf(c);
                        if (idx >= 0) { sum += idx; cnt++; }
                    });
                    baryB[n] = cnt > 0 ? sum / cnt : 999;
                });
                layers[li2].sort(function (a, b) { return baryB[a] - baryB[b]; });
            }
        }

        var nodeW = 180, nodeH = 48;
        var layerGap = 80;
        var nodeGap = 40;
        var maxOffset = Math.round(nodeW / 6);
        var padL = 30, padT = 30;

        var maxInLayer = 1;
        layers.forEach(function (l) { if (l.length > maxInLayer) maxInLayer = l.length; });
        var totalLayerW = maxInLayer * nodeW + (maxInLayer - 1) * nodeGap;

        var positions = {};
        layers.forEach(function (layer, li) {
            var lw = layer.length * nodeW + (layer.length - 1) * nodeGap;
            var offsetX = (totalLayerW - lw) / 2;
            layer.forEach(function (name, ni) {
                positions[name] = {
                    x: padL + offsetX + ni * (nodeW + nodeGap),
                    y: padT + li * (nodeH + layerGap)
                };
            });
        });

        var termWires = {};
        var normWires = [];
        wires.forEach(function (wire) {
            if (wire.to === 'done' || wire.to === 'abort') {
                if (!termWires[wire.to]) termWires[wire.to] = [];
                termWires[wire.to].push(wire);
            } else {
                normWires.push(wire);
            }
        });

        var wireColStart = padL + totalLayerW + 50;
        var wireColGap = 40;
        var wireColumns = {};
        var wireColIdx = 0;

        for (var term in termWires) {
            var tw = termWires[term];
            var nonSucc = tw.filter(function (w) { return !isSuccessResult(w.result); });
            if (nonSucc.length > 0) {
                var byResult = {};
                nonSucc.forEach(function (w) {
                    if (!byResult[w.result]) byResult[w.result] = [];
                    byResult[w.result].push(w);
                });
                for (var result in byResult) {
                    var colKey = term + ':' + result;
                    wireColumns[colKey] = {
                        x: wireColStart + wireColIdx * wireColGap,
                        terminal: term,
                        result: result,
                        wires: byResult[result]
                    };
                    wireColIdx++;
                }
            }
        }

        var succByDest = {};

        normWires = normWires.filter(function (wire) {
            if (!isSuccessResult(wire.result)) return true;
            var fl = layerOf[wire.from];
            var tl = layerOf[wire.to] !== undefined ? layerOf[wire.to] : termLayer;
            if (tl > fl + 1) {
                if (!succByDest[wire.to]) succByDest[wire.to] = [];
                succByDest[wire.to].push(wire);
                return false;
            }
            return true;
        });

        var crossTermSuccKeys = {};
        for (var sterm in termWires) {
            termWires[sterm].forEach(function (wire) {
                if (!isSuccessResult(wire.result)) return;
                var fl = layerOf[wire.from];
                if (termLayer > fl + 1) {
                    if (!succByDest[wire.to]) succByDest[wire.to] = [];
                    succByDest[wire.to].push(wire);
                    crossTermSuccKeys[wire.from + ':' + wire.to] = true;
                }
            });
        }

        var succColStartX = wireColStart + wireColIdx * wireColGap + (wireColIdx > 0 ? 20 : 0);
        var succColumns = {};
        var succCI = 0;
        for (var succDest in succByDest) {
            succColumns[succDest] = {
                x: succColStartX + succCI * wireColGap,
                dest: succDest,
                wires: succByDest[succDest]
            };
            succCI++;
        }

        for (var term2 in termWires) {
            var tw2 = termWires[term2];
            var hasSucc = tw2.some(function (w) { return isSuccessResult(w.result); });
            if (!hasSucc) {
                var colXs = [];
                for (var ck in wireColumns) {
                    if (wireColumns[ck].terminal === term2) colXs.push(wireColumns[ck].x);
                }
                if (colXs.length > 0) {
                    var avgX = colXs.reduce(function (a, b) { return a + b; }, 0) / colXs.length;
                    positions[term2] = {
                        x: avgX - nodeW / 2,
                        y: positions[term2].y
                    };
                }
            }
        }

        var maxX = padL + totalLayerW;
        for (var ck2 in wireColumns) {
            if (wireColumns[ck2].x + 20 > maxX) maxX = wireColumns[ck2].x + 20;
        }
        for (var t3 in terminals) {
            var tx = positions[t3].x + nodeW + 10;
            if (tx > maxX) maxX = tx;
        }
        for (var sd in succColumns) {
            if (succColumns[sd].x + 20 > maxX) maxX = succColumns[sd].x + 20;
        }

        var svgW = maxX + padL;
        var svgH = padT * 2 + numLayers * nodeH + (numLayers - 1) * layerGap;

        var svg = '<svg class="dag-svg" width="' + svgW + '" height="' + svgH + '">';

        svg += '<defs>';
        var markersDone = {};
        for (var r in resultColorCache) {
            var c = resultColorCache[r];
            if (markersDone[c]) continue;
            markersDone[c] = true;
            svg += '<marker id="arrow-' + c.replace('#', '') + '" markerWidth="8" markerHeight="6" refX="8" refY="3" orient="auto"><polygon points="0 0, 8 3, 0 6" fill="' + c + '"/></marker>';
        }
        svg += '</defs>';

        function mkr(color) { return 'url(#arrow-' + color.replace('#', '') + ')'; }

        function orthoPath(x1, y1, x2, y2) {
            var diff = x2 - x1;
            if (Math.abs(diff) <= 2 * maxOffset) {
                var xc = (x1 + x2) / 2;
                return { d: 'M' + xc + ' ' + y1 + ' L' + xc + ' ' + y2, lx: xc + 6, ly: (y1 + y2) / 2 };
            }
            var midY = (y1 + y2) / 2;
            return { d: 'M' + x1 + ' ' + y1 + ' L' + x1 + ' ' + midY + ' L' + x2 + ' ' + midY + ' L' + x2 + ' ' + y2, lx: (x1 + x2) / 2 + 6, ly: midY };
        }

        normWires.forEach(function (wire) {
            var from = positions[wire.from], to = positions[wire.to];
            if (!from || !to) return;
            var color = resultColor(wire.result);
            var x1 = from.x + nodeW / 2, y1 = from.y + nodeH;
            var x2 = to.x + nodeW / 2, y2 = to.y;
            var rr = orthoPath(x1, y1, x2, y2);
            svg += '<path d="' + rr.d + '" class="dag-edge" style="stroke:' + color + '" marker-end="' + mkr(color) + '"/>';
            svg += '<text x="' + rr.lx + '" y="' + rr.ly + '" class="dag-edge-label" style="fill:' + color + '">' + escapeHtml(wire.result) + '</text>';
        });

        for (var term3 in termWires) {
            var tw3 = termWires[term3];
            var tpos = positions[term3];
            if (!tpos) continue;

            tw3.filter(function (w) { return isSuccessResult(w.result) && !crossTermSuccKeys[w.from + ':' + w.to]; }).forEach(function (wire) {
                var from = positions[wire.from];
                if (!from) return;
                var color = resultColor(wire.result);
                var x1 = from.x + nodeW / 2, y1 = from.y + nodeH;
                var x2 = tpos.x + nodeW / 2, y2 = tpos.y;
                var rr = orthoPath(x1, y1, x2, y2);
                svg += '<path d="' + rr.d + '" class="dag-edge" style="stroke:' + color + '" marker-end="' + mkr(color) + '"/>';
                svg += '<text x="' + rr.lx + '" y="' + rr.ly + '" class="dag-edge-label" style="fill:' + color + '">' + escapeHtml(wire.result) + '</text>';
            });
        }

        for (var ck3 in wireColumns) {
            var col = wireColumns[ck3];
            var color = resultColor(col.result);
            var tpos2 = positions[col.terminal];
            if (!tpos2) continue;

            var links = [];
            col.wires.forEach(function (wire) {
                var from = positions[wire.from];
                if (!from) return;
                links.push({ srcX: from.x + nodeW, srcY: from.y + nodeH / 2 });
            });
            links.sort(function (a, b) { return a.srcY - b.srcY; });
            if (links.length === 0) continue;

            links.forEach(function (ln) {
                svg += '<path d="M' + ln.srcX + ' ' + ln.srcY + ' L' + col.x + ' ' + ln.srcY + '" class="dag-edge" style="stroke:' + color + '"/>';
                svg += '<circle cx="' + col.x + '" cy="' + ln.srcY + '" r="3" class="dag-link-node" style="fill:' + color + '"/>';
                svg += '<text x="' + (ln.srcX + 6) + '" y="' + (ln.srcY - 6) + '" class="dag-edge-label" style="fill:' + color + '">' + escapeHtml(col.result) + '</text>';
            });

            var topY = links[0].srcY;
            var termCX = tpos2.x + nodeW / 2;
            var termTY = tpos2.y;

            var termEntry = (Math.abs(termCX - col.x) <= maxOffset) ? col.x : termCX;
            if (col.x === termEntry) {
                svg += '<path d="M' + col.x + ' ' + topY + ' L' + col.x + ' ' + termTY + '" class="dag-edge" style="stroke:' + color + '" marker-end="' + mkr(color) + '"/>';
            } else {
                svg += '<path d="M' + col.x + ' ' + topY + ' L' + col.x + ' ' + termTY + ' L' + termCX + ' ' + termTY + '" class="dag-edge" style="stroke:' + color + '" marker-end="' + mkr(color) + '"/>';
            }
        }

        for (var sd2 in succColumns) {
            var scol = succColumns[sd2];
            var dpos = positions[scol.dest];
            if (!dpos) continue;

            var links2 = [];
            scol.wires.forEach(function (wire) {
                var from = positions[wire.from];
                if (!from) return;
                links2.push({ srcX: from.x + nodeW, srcY: from.y + nodeH / 2, result: wire.result });
            });
            links2.sort(function (a, b) { return a.srcY - b.srcY; });
            if (links2.length === 0) continue;

            var sColor = resultColor(scol.wires[0].result);

            links2.forEach(function (ln) {
                svg += '<path d="M' + ln.srcX + ' ' + ln.srcY + ' L' + scol.x + ' ' + ln.srcY + '" class="dag-edge" style="stroke:' + sColor + '"/>';
                svg += '<circle cx="' + scol.x + '" cy="' + ln.srcY + '" r="3" class="dag-link-node" style="fill:' + sColor + '"/>';
                svg += '<text x="' + (ln.srcX + 6) + '" y="' + (ln.srcY - 6) + '" class="dag-edge-label" style="fill:' + sColor + '">' + escapeHtml(ln.result) + '</text>';
            });

            var topY2 = links2[0].srcY;
            var destCX = dpos.x + nodeW / 2;
            var destTY = dpos.y;

            var destEntry = (Math.abs(destCX - scol.x) <= maxOffset) ? scol.x : destCX;
            if (scol.x === destEntry) {
                svg += '<path d="M' + scol.x + ' ' + topY2 + ' L' + scol.x + ' ' + destTY + '" class="dag-edge" style="stroke:' + sColor + '" marker-end="' + mkr(sColor) + '"/>';
            } else {
                svg += '<path d="M' + scol.x + ' ' + topY2 + ' L' + scol.x + ' ' + destTY + ' L' + destCX + ' ' + destTY + '" class="dag-edge" style="stroke:' + sColor + '" marker-end="' + mkr(sColor) + '"/>';
            }
        }

        wf.steps.forEach(function (s) {
            var pos = positions[s.name];
            if (!pos) return;
            var icon = '\u{1F4DC}';
            if (s.type === 'agent') icon = '\u{1F916}';
            else if (s.type === 'workflow') icon = '\u{1F501}';
            svg += '<g class="dag-node" data-workflow="' + escapeHtml(wf.name) + '" data-step="' + escapeHtml(s.name) + '" style="cursor:pointer">';
            svg += '<rect x="' + pos.x + '" y="' + pos.y + '" width="' + nodeW + '" height="' + nodeH + '" class="dag-node-rect"/>';
            svg += '<text x="' + (pos.x + 10) + '" y="' + (pos.y + nodeH / 2 + 5) + '" class="dag-node-text">' + icon + ' ' + escapeHtml(s.name) + '</text>';
            svg += '</g>';
        });

        for (var tt in terminals) {
            var posT = positions[tt];
            var cls = tt === 'done' ? 'dag-terminal-done' : 'dag-terminal-abort';
            svg += '<rect x="' + posT.x + '" y="' + posT.y + '" width="' + nodeW + '" height="' + nodeH + '" class="dag-node-rect ' + cls + '" rx="20"/>';
            svg += '<text x="' + (posT.x + nodeW / 2) + '" y="' + (posT.y + nodeH / 2 + 5) + '" class="dag-node-text" text-anchor="middle">' + tt + '</text>';
        }

        svg += '</svg>';
        dagEl.innerHTML = svg;
    }

    function openStepDrawer(workflowName, stepName) {
        var wf = workflowsData.find(function (w) { return w.name === workflowName; });
        if (!wf) return;
        var step = wf.steps.find(function (s) { return s.name === stepName; });
        if (!step) return;

        document.getElementById('drawer-title').textContent = stepName;
        var body = '<dl class="console-facts-row">';
        body += '<dt>Type</dt><dd>' + escapeHtml(step.type) + '</dd>';
        body += '<dt>Results</dt><dd>' + (step.results && step.results.length ? escapeHtml(step.results.join(', ')) : 'none') + '</dd>';
        if (step.config) {
            DRAWER_CONFIG_KEYS.forEach(function (k) {
                if (step.config[k] !== undefined && step.config[k] !== '') {
                    body += '<dt>' + escapeHtml(k) + '</dt><dd class="mono">' + escapeHtml(step.config[k]) + '</dd>';
                }
            });
        }
        body += '</dl>';
        body += '<div id="step-drawer-content"><p style="color:var(--text-muted)">Loading content...</p></div>';

        document.getElementById('drawer-body').innerHTML = body;
        document.getElementById('step-drawer').classList.add('drawer-open');

        fetch('/api/projects/' + encodeURIComponent(state.activeSlug) + '/workflows/' +
            encodeURIComponent(workflowName) + '/steps/' + encodeURIComponent(stepName) + '/content')
            .then(function (r) { return r.ok ? r.text() : ''; })
            .then(function (text) {
                var el = document.getElementById('step-drawer-content');
                if (!el) return;
                if (text) {
                    el.innerHTML = '<pre>' + escapeHtml(text) + '</pre>';
                } else {
                    el.style.display = 'none';
                }
            })
            .catch(function () {});
    }

    // ---------- Requirements view ----------

    var intentData = { requirements: [], last_scan_at: '', last_scan_stats: null };
    var domainsData = { version: 1, domains: [] };
    var scanStatus = null; // { runId, state, taskId }
    var scanPollTimer = null;

    function formatIntentTime(s) {
        return s ? formatTimestamp(s) : 'never';
    }

    // formatScanStats renders a ScanStats (last_scan_stats) as the
    // "N docs · M commits · K transcripts across R repos" summary, plus a
    // warning listing any configured repo that contributed nothing.
    function formatScanStats(stats) {
        if (!stats || !stats.repos || !stats.repos.length) return '';
        var totalDocs = 0, totalCommits = 0, totalRuns = 0, emptyNames = [];
        stats.repos.forEach(function (r) {
            var docs = (r.docs_new || 0) + (r.docs_changed || 0);
            totalDocs += docs;
            totalCommits += r.commits || 0;
            totalRuns += r.runs || 0;
            if (r.name && docs === 0 && !r.commits && !r.runs) emptyNames.push(r.name);
        });
        var line = totalDocs + ' docs · ' + totalCommits + ' commits · ' + totalRuns + ' transcripts';
        if (stats.repos.length > 1) line += ' across ' + stats.repos.length + ' repos';
        if (emptyNames.length) line += ' (warning: ' + emptyNames.join(', ') + ' contributed nothing)';
        return line;
    }

    function isTerminalRunState(s) {
        return s === 'succeeded' || s === 'failed' || s === 'cancelled';
    }

    function openIntentView() {
        if (!state.activeSlug) return;
        openView('Requirements');
        var body = document.getElementById('console-view-body');
        body.innerHTML =
            '<div style="margin-bottom:0.75rem">' +
            '<button type="button" class="btn btn-secondary btn-sm" id="intent-scan-btn">Scan now</button>' +
            '<span class="container-scan-status" id="intent-scan-status"></span>' +
            '</div>' +
            '<div id="intent-meta" style="margin-bottom:0.75rem;color:var(--text-muted);font-size:0.85rem">Loading...</div>' +
            '<label style="font-size:0.85rem;display:inline-flex;align-items:center;gap:0.35rem;margin-bottom:0.75rem">' +
            '<input type="checkbox" id="intent-show-hidden"> Show superseded/disabled</label>' +
            '<div id="intent-requirements">Loading...</div>' +
            '<h2 style="margin-top:1.5rem">Domains</h2>' +
            '<div id="intent-domains">Loading...</div>' +
            '<button type="button" class="btn btn-secondary btn-sm" id="intent-add-domain-btn" style="margin-top:0.5rem">Add domain</button> ' +
            '<button type="button" class="btn btn-secondary btn-sm" id="intent-save-domains-btn" style="margin-top:0.5rem">Save domains</button>';

        document.getElementById('intent-scan-btn').addEventListener('click', scanIntent);
        document.getElementById('intent-show-hidden').addEventListener('change', renderRequirements);
        document.getElementById('intent-add-domain-btn').addEventListener('click', addDomainRow);
        document.getElementById('intent-save-domains-btn').addEventListener('click', saveDomains);
        document.getElementById('intent-requirements').addEventListener('click', onRequirementsClick);
        document.getElementById('intent-domains').addEventListener('change', onDomainsChange);

        renderScanStatus();
        if (scanStatus && !isTerminalRunState(scanStatus.state)) pollScanRun();
        loadIntent();
    }

    function onRequirementsClick(e) {
        var btn = e.target.closest('button[data-action]');
        if (!btn) return;
        var id = btn.getAttribute('data-id');
        if (btn.getAttribute('data-action') === 'toggle-status') {
            toggleRequirementStatus(id, btn.getAttribute('data-status'));
        } else if (btn.getAttribute('data-action') === 'edit') {
            openIntentDrawer(id);
        }
    }

    function loadIntent() {
        fetch('/api/projects/' + encodeURIComponent(state.activeSlug) + '/intent/requirements')
            .then(function (r) { return r.json(); })
            .then(function (data) {
                intentData = data || { requirements: [], last_scan_at: '', last_scan_stats: null };
                var metaEl = document.getElementById('intent-meta');
                if (metaEl) {
                    var line = 'Last scan: ' + formatIntentTime(intentData.last_scan_at);
                    var statsLine = formatScanStats(intentData.last_scan_stats);
                    if (statsLine) line += ' — ' + statsLine;
                    metaEl.textContent = line;
                }
                renderRequirements();
            })
            .catch(function () {});
        fetch('/api/projects/' + encodeURIComponent(state.activeSlug) + '/intent/domains')
            .then(function (r) { return r.json(); })
            .then(function (data) {
                domainsData = data || { version: 1, domains: [] };
                renderDomains();
            })
            .catch(function () {});
    }

    function stopScanPolling() {
        if (scanPollTimer) {
            clearInterval(scanPollTimer);
            scanPollTimer = null;
        }
    }

    function renderScanStatus() {
        var el = document.getElementById('intent-scan-status');
        if (!el) return;
        if (!scanStatus) {
            el.innerHTML = '';
            return;
        }
        var label = 'Scan dispatched: ' + scanStatus.runId + (scanStatus.state ? ' (' + scanStatus.state + ')' : '');
        if (scanStatus.taskId) {
            el.innerHTML = ' — ' + escapeHtml(label) + ' <a href="#" id="intent-scan-open-link">open</a>';
            var link = document.getElementById('intent-scan-open-link');
            link.addEventListener('click', function (e) {
                e.preventDefault();
                var taskId = scanStatus.taskId;
                closeView();
                selectTaskById(taskId);
            });
        } else {
            el.textContent = ' — ' + label;
        }
    }

    function pollScanRun() {
        stopScanPolling();
        var check = function () {
            if (!scanStatus) { stopScanPolling(); return; }
            fetch('/api/runs/' + encodeURIComponent(scanStatus.runId))
                .then(function (r) { return r.json(); })
                .then(function (run) {
                    if (!scanStatus) return;
                    scanStatus.state = run.state;
                    if (run.task_id) scanStatus.taskId = run.task_id;
                    renderScanStatus();
                    if (isTerminalRunState(run.state)) {
                        stopScanPolling();
                        loadIntent();
                    }
                })
                .catch(function () {});
        };
        check();
        scanPollTimer = setInterval(check, 3000);
    }

    function scanIntent() {
        var btn = document.getElementById('intent-scan-btn');
        if (btn) btn.disabled = true;
        fetch('/api/projects/' + encodeURIComponent(state.activeSlug) + '/intent/scan', { method: 'POST' })
            .then(function (r) { return r.json().then(function (d) { return { ok: r.ok, data: d }; }); })
            .then(function (res) {
                if (btn) btn.disabled = false;
                if (!res.ok) {
                    alert('Failed to start scan: ' + (res.data.error || 'unknown error'));
                    return;
                }
                scanStatus = { runId: res.data.run_id || '', state: 'running', taskId: res.data.task_id || '' };
                renderScanStatus();
                pollScanRun();
            })
            .catch(function () {
                if (btn) btn.disabled = false;
                alert('Failed to start scan');
            });
    }

    function renderRequirements() {
        var showHiddenEl = document.getElementById('intent-show-hidden');
        var showHidden = !!(showHiddenEl && showHiddenEl.checked);
        var reqs = (intentData.requirements || []).filter(function (r) {
            return showHidden || (r.status !== 'superseded' && r.status !== 'disabled');
        });

        var el = document.getElementById('intent-requirements');
        if (!el) return;
        if (reqs.length === 0) {
            el.innerHTML = '<p class="empty">No requirements' + (showHidden ? '' : ' (or all hidden)') + '</p>';
            return;
        }

        var html = '<table class="runs-table" style="margin:0"><thead><tr>';
        html += '<th>Statement</th><th>Scope</th><th>Status</th><th>Confidence</th><th>Provenance</th><th></th>';
        html += '</tr></thead><tbody>';
        reqs.forEach(function (r) {
            html += '<tr>';
            html += '<td>' + escapeHtml(r.statement) +
                (r.new_since_scan ? ' <span class="badge badge-running">new</span>' : '') +
                (r.user_edited ? ' <span class="badge badge-stopped">edited</span>' : '') + '</td>';
            html += '<td>';
            if (r.scope && r.scope.level === 'domain' && r.scope.domains) {
                r.scope.domains.forEach(function (d) {
                    html += '<span class="badge badge-cancelled" style="margin-right:0.25rem">' + escapeHtml(d) + '</span>';
                });
            } else {
                html += '<span class="badge badge-cancelled">project</span>';
            }
            html += '</td>';
            html += '<td><button type="button" class="btn btn-secondary btn-sm" data-action="toggle-status" data-id="' +
                escapeHtml(r.id) + '" data-status="' + escapeHtml(r.status) + '">' + escapeHtml(r.status) + '</button></td>';
            html += '<td>' + escapeHtml(r.confidence || '') + '</td>';
            html += '<td>';
            if (r.provenance && r.provenance.link) {
                html += '<a href="' + escapeHtml(r.provenance.link) + '" target="_blank" rel="noopener">' + escapeHtml(r.provenance.kind) + '</a>';
            } else if (r.provenance) {
                html += escapeHtml(r.provenance.kind || '');
            }
            html += '</td>';
            html += '<td><button type="button" class="btn btn-secondary btn-sm" data-action="edit" data-id="' + escapeHtml(r.id) + '">Edit</button></td>';
            html += '</tr>';
        });
        html += '</tbody></table>';
        el.innerHTML = html;
    }

    function toggleRequirementStatus(id, currentStatus) {
        var next = currentStatus === 'disabled' ? 'active' : 'disabled';
        fetch('/api/projects/' + encodeURIComponent(state.activeSlug) + '/intent/requirements', {
            method: 'PATCH',
            headers: { 'Content-Type': 'application/json' },
            body: JSON.stringify({ id: id, status: next })
        })
            .then(function (r) { return r.json(); })
            .then(function () { loadIntent(); })
            .catch(function () { alert('Failed to update status'); });
    }

    function openIntentDrawer(id) {
        var r = (intentData.requirements || []).find(function (x) { return x.id === id; });
        if (!r) return;

        document.getElementById('intent-drawer-title').textContent = r.id;
        var domains = (r.scope && r.scope.domains) ? r.scope.domains.join(', ') : '';
        var body = '<label>Statement</label>';
        body += '<textarea id="intent-edit-statement" rows="4" style="width:100%">' + escapeHtml(r.statement) + '</textarea>';
        body += '<label style="display:block;margin-top:0.75rem">Scope level</label>';
        body += '<select id="intent-edit-scope-level">';
        body += '<option value="project"' + (r.scope && r.scope.level === 'project' ? ' selected' : '') + '>project</option>';
        body += '<option value="domain"' + (r.scope && r.scope.level === 'domain' ? ' selected' : '') + '>domain</option>';
        body += '</select>';
        body += '<label style="display:block;margin-top:0.75rem">Domains (comma-separated)</label>';
        body += '<input type="text" id="intent-edit-domains" value="' + escapeHtml(domains) + '" style="width:100%">';
        body += '<button type="button" class="btn btn-primary" id="intent-edit-save-btn" style="margin-top:1rem">Save</button>';

        document.getElementById('intent-drawer-body').innerHTML = body;
        document.getElementById('intent-edit-save-btn').addEventListener('click', function () { saveIntentEdit(id); });
        document.getElementById('intent-drawer').classList.add('drawer-open');
    }

    function saveIntentEdit(id) {
        var statement = document.getElementById('intent-edit-statement').value;
        var level = document.getElementById('intent-edit-scope-level').value;
        var domains = document.getElementById('intent-edit-domains').value
            .split(',').map(function (s) { return s.trim(); }).filter(function (s) { return s; });

        fetch('/api/projects/' + encodeURIComponent(state.activeSlug) + '/intent/requirements', {
            method: 'PATCH',
            headers: { 'Content-Type': 'application/json' },
            body: JSON.stringify({
                id: id,
                statement: statement,
                scope: { level: level, domains: domains }
            })
        })
            .then(function (r) { return r.json(); })
            .then(function () {
                closeIntentDrawer();
                loadIntent();
            })
            .catch(function () { alert('Failed to save requirement'); });
    }

    function renderDomains() {
        var el = document.getElementById('intent-domains');
        if (!el) return;
        var domains = domainsData.domains || [];
        if (domains.length === 0) {
            el.innerHTML = '<p class="empty">No domains defined</p>';
            return;
        }
        var html = '<table class="runs-table" style="margin:0"><thead><tr>';
        html += '<th>Name</th><th>Description</th><th>Paths (comma-separated)</th>';
        html += '</tr></thead><tbody>';
        domains.forEach(function (d, i) {
            html += '<tr>';
            html += '<td><input type="text" value="' + escapeHtml(d.name || '') + '" data-field="name" data-index="' + i + '"></td>';
            html += '<td><input type="text" value="' + escapeHtml(d.description || '') + '" data-field="description" data-index="' + i + '" style="width:100%"></td>';
            html += '<td><input type="text" value="' + escapeHtml((d.paths || []).join(', ')) + '" data-field="paths" data-index="' + i + '" style="width:100%"></td>';
            html += '</tr>';
        });
        html += '</tbody></table>';
        el.innerHTML = html;
    }

    function onDomainsChange(e) {
        var input = e.target.closest('input[data-field]');
        if (!input) return;
        var i = parseInt(input.getAttribute('data-index'), 10);
        var field = input.getAttribute('data-field');
        domainsData.domains[i] = domainsData.domains[i] || {};
        if (field === 'paths') {
            domainsData.domains[i].paths = input.value.split(',').map(function (s) { return s.trim(); }).filter(function (s) { return s; });
        } else {
            domainsData.domains[i][field] = input.value;
        }
        domainsData.domains[i].user_edited = true;
    }

    function addDomainRow() {
        domainsData.domains = domainsData.domains || [];
        domainsData.domains.push({ name: '', description: '', paths: [], user_edited: true });
        renderDomains();
    }

    function saveDomains() {
        fetch('/api/projects/' + encodeURIComponent(state.activeSlug) + '/intent/domains', {
            method: 'PUT',
            headers: { 'Content-Type': 'application/json' },
            body: JSON.stringify(domainsData)
        })
            .then(function (r) { return r.json(); })
            .then(function (data) {
                domainsData = data;
                renderDomains();
            })
            .catch(function () { alert('Failed to save domains'); });
    }

    // ---------- Containers view ----------

    function formatBytes(n) {
        if (!n) return '0 B';
        var units = ['B', 'KB', 'MB', 'GB', 'TB'];
        var i = 0;
        var v = n;
        while (v >= 1024 && i < units.length - 1) {
            v /= 1024;
            i++;
        }
        return (i === 0 ? String(v) : v.toFixed(1)) + ' ' + units[i];
    }

    function containerStateBadgeClass(s) {
        switch (s) {
            case 'running': return 'badge-running';
            case 'available': return 'badge-succeeded';
            case 'stopped': return 'badge-stopped';
            default: return 'badge-cancelled';
        }
    }

    function openContainersView() {
        if (!state.activeSlug) return;
        openView('Containers');
        var body = document.getElementById('console-view-body');
        body.innerHTML =
            '<div id="containers-list">Loading...</div>' +
            '<div style="margin-top:1rem">' +
            '<button type="button" class="btn btn-danger btn-sm" id="containers-cleanup-btn" hidden>Clean up old containers in this project</button>' +
            '<span class="container-scan-status" id="containers-cleanup-status"></span>' +
            '</div>';
        document.getElementById('containers-cleanup-btn').addEventListener('click', cleanupProjectContainers);
        document.getElementById('containers-list').addEventListener('click', onContainersListClick);
        loadContainers();
    }

    function onContainersListClick(e) {
        var btn = e.target.closest('button[data-delete-run]');
        if (btn) deleteContainer(btn.getAttribute('data-delete-run'), btn);
    }

    function loadContainers() {
        fetch('/api/projects/' + encodeURIComponent(state.activeSlug) + '/containers')
            .then(function (r) { return r.json(); })
            .then(function (groups) { renderContainers(groups || []); })
            .catch(function () {
                var el = document.getElementById('containers-list');
                if (el) el.innerHTML = '<p class="empty">Failed to load containers</p>';
            });
    }

    function renderContainers(groups) {
        var el = document.getElementById('containers-list');
        var cleanupBtn = document.getElementById('containers-cleanup-btn');
        if (!el) return;

        if (groups.length === 0) {
            el.innerHTML = '<p class="empty">No retained containers in this project</p>';
            if (cleanupBtn) cleanupBtn.hidden = true;
            return;
        }
        if (cleanupBtn) cleanupBtn.hidden = false;

        var html = '';
        groups.forEach(function (g) {
            html += '<div class="container-task-group">';
            html += '<div class="container-task-group-title">' + escapeHtml(g.title || g.task_id) + '</div>';
            html += '<table class="runs-table" style="margin:0"><thead><tr>';
            html += '<th>Run</th><th>Workflow</th><th>Container</th><th>State</th><th>Size</th><th>Age</th><th></th>';
            html += '</tr></thead><tbody>';
            (g.containers || []).forEach(function (c) {
                html += '<tr>';
                html += '<td class="mono">' + escapeHtml(c.run_id) + '</td>';
                html += '<td>' + escapeHtml(c.workflow_name) + '</td>';
                html += '<td class="mono">' + escapeHtml((c.container_id || '').slice(0, 12)) + '</td>';
                html += '<td><span class="badge ' + containerStateBadgeClass(c.state) + '">' + escapeHtml(c.state) + '</span></td>';
                html += '<td>' + (c.size_bytes ? formatBytes(c.size_bytes) : '—') + '</td>';
                html += '<td>' + formatDuration(c.age_seconds) + ' ago</td>';
                html += '<td><button type="button" class="btn btn-danger btn-sm" data-delete-run="' + escapeHtml(c.run_id) + '">Delete</button></td>';
                html += '</tr>';
            });
            html += '</tbody></table></div>';
        });
        el.innerHTML = html;
    }

    function deleteContainer(runId, btn) {
        if (btn) btn.disabled = true;
        fetch('/api/runs/' + encodeURIComponent(runId) + '/container', { method: 'DELETE' })
            .then(function (r) { return r.json().then(function (d) { return { ok: r.ok, data: d }; }); })
            .then(function (res) {
                if (!res.ok) {
                    alert('Failed to delete container: ' + (res.data.error || 'unknown error'));
                    if (btn) btn.disabled = false;
                    return;
                }
                loadContainers();
            })
            .catch(function () {
                alert('Failed to delete container');
                if (btn) btn.disabled = false;
            });
    }

    function cleanupProjectContainers() {
        var btn = document.getElementById('containers-cleanup-btn');
        if (btn) btn.disabled = true;
        fetch('/api/projects/' + encodeURIComponent(state.activeSlug) + '/containers', { method: 'DELETE' })
            .then(function (r) { return r.json(); })
            .then(function (data) {
                var status = document.getElementById('containers-cleanup-status');
                if (status) {
                    status.textContent = ' Cleaned up ' + (data.deleted || 0) + ' container' +
                        (data.deleted === 1 ? '' : 's') + ' in this project.';
                }
                if (btn) btn.disabled = false;
                loadContainers();
            })
            .catch(function () {
                if (btn) btn.disabled = false;
                alert('Failed to clean up containers');
            });
    }

    // ---------- daemon instruments ----------

    function loadInstruments() {
        var slug = state.activeSlug;
        var instrumentsEl = document.getElementById('console-instruments');
        if (!slug) {
            instrumentsEl.innerHTML = '';
            return Promise.resolve();
        }
        return Promise.all([
            fetch('/api/projects/' + encodeURIComponent(slug) + '/loop/status')
                .then(function (r) { return r.json(); }).catch(function () { return { running: false }; }),
            fetch('/api/projects/' + encodeURIComponent(slug) + '/loop/occupancy')
                .then(function (r) { return r.json(); }).catch(function () { return { max_concurrency: 0, slots: [], queued: [] }; }),
            fetch('/api/projects/' + encodeURIComponent(slug) + '/usage')
                .then(function (r) { return r.json(); }).catch(function () { return { burn_rate_1h: [] }; })
        ]).then(function (results) {
            if (slug !== state.activeSlug) return; // stale response from a since-abandoned project
            renderInstruments({ loop: results[0], occupancy: results[1], usage: results[2] });
        });
    }

    function startInstrumentsPolling() {
        stopInstrumentsPolling();
        state.instrumentsTimer = setInterval(loadInstruments, INSTRUMENTS_POLL_MS);
    }

    function stopInstrumentsPolling() {
        if (state.instrumentsTimer) {
            clearInterval(state.instrumentsTimer);
            state.instrumentsTimer = null;
        }
    }

    function renderInstruments(data) {
        var el = document.getElementById('console-instruments');
        el.innerHTML = '';

        state.loopRunning = !!(data.loop && data.loop.running);

        var isSystemProject = state.activeSlug === SYSTEM_PROJECT_SLUG;

        var loopSpan = instrumentSpan('console-loop');
        loopSpan.appendChild(document.createTextNode('loop '));
        var loopBtn = document.createElement('button');
        loopBtn.type = 'button';
        loopBtn.id = 'console-loop-toggle';
        loopBtn.className = 'console-instrument-toggle' + (state.loopRunning ? ' on' : '');
        loopBtn.textContent = state.loopRunning ? 'running' : 'stopped';
        if (isSystemProject) {
            loopBtn.disabled = true;
            loopBtn.title = 'No orchestration loop for the system project';
        } else {
            loopBtn.title = state.loopRunning ? 'Stop loop' : 'Start loop';
            loopBtn.addEventListener('click', toggleLoop);
        }
        loopSpan.appendChild(loopBtn);
        el.appendChild(loopSpan);

        var occ = data.occupancy || {};
        var busy = (occ.slots || []).length;
        var max = occ.max_concurrency || 0;

        var slotsSpan = instrumentSpan('console-slot-meter');
        slotsSpan.appendChild(document.createTextNode('slots '));
        var pips = document.createElement('b');
        pips.className = 'console-slots-pips';
        for (var i = 0; i < max; i++) {
            var pip = document.createElement('i');
            if (i < busy) pip.className = 'busy';
            pips.appendChild(pip);
        }
        slotsSpan.appendChild(pips);
        slotsSpan.appendChild(document.createTextNode(' '));
        var slotsVal = document.createElement('b');
        slotsVal.textContent = busy + '/' + max;
        slotsSpan.appendChild(slotsVal);
        el.appendChild(slotsSpan);

        var queueSpan = instrumentSpan('console-queue');
        queueSpan.appendChild(document.createTextNode('queue '));
        var queueVal = document.createElement('b');
        queueVal.textContent = String((occ.queued || []).length);
        queueSpan.appendChild(queueVal);
        el.appendChild(queueSpan);

        var burnSpan = instrumentSpan('console-burn');
        burnSpan.appendChild(document.createTextNode('burn '));
        var burnVal = document.createElement('b');
        burnVal.className = 'ac';
        burnVal.textContent = formatBurn(data.usage);
        burnSpan.appendChild(burnVal);
        el.appendChild(burnSpan);
    }

    function instrumentSpan(cls) {
        var span = document.createElement('span');
        span.className = 'console-instrument ' + cls;
        return span;
    }

    function toggleLoop() {
        var slug = state.activeSlug;
        if (!slug) return;
        var btn = document.getElementById('console-loop-toggle');
        if (btn) btn.disabled = true;
        var url = '/api/projects/' + encodeURIComponent(slug) + (state.loopRunning ? '/loop/stop' : '/trigger');
        fetch(url, { method: 'POST' })
            .then(function () { loadInstruments(); })
            .catch(function () { loadInstruments(); });
    }

    function formatBurn(usage) {
        var rows = (usage && usage.burn_rate_1h) || [];
        var total = rows.reduce(function (sum, r) { return sum + (r.burn_rate || 0); }, 0);
        var inFlight = rows.some(function (r) { return r.in_flight; });
        var prefix = inFlight ? '~' : '';
        if (total <= 0) return prefix + '0/hr';
        return prefix + formatNumber(total) + '/hr';
    }

    function formatNumber(n) {
        if (n >= 1e6) return (n / 1e6).toFixed(1) + 'M';
        if (n >= 1e3) return (n / 1e3).toFixed(1) + 'k';
        return String(Math.round(n));
    }

    // ---------- formatting helpers ----------

    function formatElapsed(seconds) { return formatDuration(seconds); }

    function formatDuration(seconds) {
        seconds = seconds || 0;
        if (seconds < 60) return seconds + 's';
        var m = Math.floor(seconds / 60);
        if (m < 60) return m + 'm';
        var h = Math.floor(m / 60);
        m = m % 60;
        return h + 'h' + (m ? ' ' + m + 'm' : '');
    }

    function formatTimestamp(iso) {
        if (!iso) return '—';
        var d = new Date(iso);
        if (isNaN(d.getTime())) return iso;
        return d.toLocaleString();
    }

    // ---------- init ----------

    // Landing project for a bare "/" visit (loc.slug === ''): the last
    // project the user viewed, read synchronously from localStorage so it
    // doesn't cost a request; falling back to the server's own best-effort
    // pick (root.dataset.projectSlug — most recent run, else alphabetical;
    // see handleLegacyRoot) until the project list loads and
    // reconcileLandingProject() can confirm or correct it.
    function lastViewedSlug() {
        try {
            return localStorage.getItem(LAST_PROJECT_STORAGE_KEY);
        } catch (e) { /* storage unavailable */ }
        return null;
    }

    function resolveLandingSlug() {
        return ConsoleTabs.pickLandingSlug(lastViewedSlug(), [], root.dataset.projectSlug || '');
    }

    // Runs once the project list has actually loaded, and only for a bare
    // "/" visit: upgrades the optimistic landing guess (localStorage or the
    // server's default) to the true "most recent run, else alphabetical"
    // project if that guess was empty or no longer valid (e.g. a
    // localStorage entry for a project that's since been removed).
    function reconcileLandingProject() {
        if (!state.projects.length) return;
        var target = ConsoleTabs.pickLandingSlug(lastViewedSlug(), state.projects, root.dataset.projectSlug || '');
        if (target && target !== state.activeSlug) {
            selectProject(target, { pushHistory: false });
        }
    }

    // reconcileRepoFromURL re-resolves the current URL's rest segments once
    // real project data (with .repositories) is available, upgrading the
    // optimistic legacy guess init() made before that data existed — see
    // ConsoleTabs.resolveRepoAndTask, which needs to know whether the
    // project is multi-repo to tell a repo segment from a task ID.
    function reconcileRepoFromURL() {
        var loc = parseLocation();
        if (loc.slug !== state.activeSlug) return; // navigated elsewhere before this resolved
        var resolved = ConsoleTabs.resolveRepoAndTask(currentProject(), loc.rest);
        if (resolved.repo !== state.activeRepo) {
            selectRepo(resolved.repo, { pushHistory: false, taskId: resolved.taskId || state.activeTaskId });
        } else {
            renderSubtabs();
        }
    }

    function init() {
        var loc = parseLocation();
        var explicitSlug = loc.slug || '';
        var initialSlug = explicitSlug || resolveLandingSlug();
        // Optimistic legacy guess: the project list (and thus its
        // .repositories) hasn't loaded yet, so a single rest segment is
        // assumed to be a task ID until reconcileRepoFromURL can check it
        // against the real repo list below.
        var rest = loc.rest.length ? loc.rest : (root.dataset.taskId ? [root.dataset.taskId] : []);
        var initial = ConsoleTabs.resolveRepoAndTask(null, rest);

        // Paint the tab bar skeleton immediately; nothing below gates on a
        // network round trip.
        renderTabBar();
        renderFooterVersion();

        var projectsPromise = loadProjects();
        startProjectsPolling();

        if (initialSlug) {
            selectProject(initialSlug, { repo: initial.repo, taskId: initial.taskId, pushHistory: false });
        } else {
            renderCentrePane(null, null);
        }

        projectsPromise.then(function () {
            if (!explicitSlug) {
                reconcileLandingProject();
                return;
            }
            reconcileRepoFromURL();
        });
    }

    init();
})();
