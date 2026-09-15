(function () {
    'use strict';

    var root = document.getElementById('console-app');
    if (!root) return;

    var GROUPS = [
        { key: 'needs_you', field: 'needs_you', label: 'Needs you' },
        { key: 'running', field: 'running', label: 'Running' },
        { key: 'queued', field: 'queued', label: 'Queued' },
        { key: 'done_today', field: 'done_today', label: 'Done today' }
    ];

    var STACK_POLL_MS = 4000;
    var INSTRUMENTS_POLL_MS = 5000;
    var TICKER_POLL_MS = 5000;

    var state = {
        projects: [],
        activeSlug: '',
        activeTaskId: '',
        stack: null,          // last "today" window from the server
        stackEtag: null,
        extraDone: [],         // accumulated "earlier" pages fetched via cursor
        doneCursor: null,
        rowEls: {},            // key -> row element
        rowOrder: [],          // ordered list of keys, document order
        selectedIndex: -1,
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

    function parseLocation() {
        var segments = location.pathname.replace(/^\/+|\/+$/g, '').split('/').filter(Boolean);
        var params = new URLSearchParams(location.search);
        return {
            slug: segments[0] ? decodeURIComponent(segments[0]) : '',
            taskId: segments[1] ? decodeURIComponent(segments[1]) : '',
            attempt: params.get('attempt') || '',
            step: params.get('step') || ''
        };
    }

    function pushLocation(slug, taskId) {
        var path = '/' + encodeURIComponent(slug);
        if (taskId) path += '/' + encodeURIComponent(taskId);
        if (path !== location.pathname) {
            history.pushState({ slug: slug, taskId: taskId }, '', path + location.search);
        }
    }

    window.addEventListener('popstate', function () {
        var loc = parseLocation();
        selectProject(loc.slug, { taskId: loc.taskId, pushHistory: false });
    });

    // ---------- tab bar ----------

    function loadProjects() {
        return fetch('/api/projects').then(function (r) { return r.json(); }).then(function (projects) {
            state.projects = projects || [];
            renderTabBar();
        }).catch(function () {
            state.projects = [];
        });
    }

    function sortedProjects(list) {
        return list.slice().sort(function (a, b) {
            if (a.label === b.label) return a.slug < b.slug ? -1 : (a.slug > b.slug ? 1 : 0);
            return a.label < b.label ? -1 : 1;
        });
    }

    function renderTabBar() {
        var tabsEl = document.getElementById('console-project-tabs');
        var moreBtn = document.getElementById('console-more-btn');
        var menuEl = document.getElementById('console-idle-menu');
        tabsEl.innerHTML = '';
        menuEl.innerHTML = '';

        if (!state.projects.length) {
            var none = document.createElement('span');
            none.className = 'console-tab-count';
            none.textContent = 'No projects registered';
            tabsEl.appendChild(none);
            moreBtn.hidden = true;
            menuEl.hidden = true;
            return;
        }

        var visible = [];
        var idle = [];
        state.projects.forEach(function (p) {
            var isIdle = (p.active_count || 0) === 0 && (p.attention_count || 0) === 0;
            if (isIdle && p.slug !== state.activeSlug) {
                idle.push(p);
            } else {
                visible.push(p);
            }
        });

        sortedProjects(visible).forEach(function (p) { tabsEl.appendChild(renderTab(p, false)); });

        if (idle.length) {
            moreBtn.hidden = false;
            moreBtn.textContent = 'More (' + idle.length + ') ▾';
            sortedProjects(idle).forEach(function (p) { menuEl.appendChild(renderTab(p, true)); });
        } else {
            moreBtn.hidden = true;
            menuEl.hidden = true;
        }
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

        if (p.active_count) {
            var count = document.createElement('span');
            count.className = 'console-tab-count';
            count.textContent = String(p.active_count);
            btn.appendChild(count);
        }

        if (p.attention_count) {
            var flag = document.createElement('span');
            flag.className = 'console-tab-flag';
            flag.title = p.attention_count + ' need' + (p.attention_count === 1 ? 's' : '') + ' you';
            flag.textContent = '!';
            btn.appendChild(flag);
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
        var menu = document.getElementById('console-idle-menu');
        var willOpen = menu.hidden;
        menu.hidden = !willOpen;
        this.setAttribute('aria-expanded', String(willOpen));
    });
    document.addEventListener('click', closeIdleMenu);

    // ---------- project selection ----------

    function selectProject(slug, opts) {
        opts = opts || {};
        if (!slug) return;

        state.activeSlug = slug;
        state.activeTaskId = opts.taskId || '';
        state.stack = null;
        state.stackEtag = null;
        state.extraDone = [];
        state.doneCursor = null;
        state.rowEls = {};
        state.rowOrder = [];
        state.selectedIndex = -1;

        closeView();
        scanStatus = null;

        stopStackPolling();
        stopInstrumentsPolling();
        stopTickerPolling();
        renderTabBar();
        renderCentrePane(null, null);
        updateViewButtonsEnabled();

        loadInstruments();
        startInstrumentsPolling();

        loadTicker();
        startTickerPolling();

        loadStack(true).then(function () {
            startStackPolling();
            if (state.activeTaskId) selectTaskById(state.activeTaskId);
        });

        if (state.activity.open && state.activity.scope === 'project') loadActivityStream(true);

        if (opts.pushHistory) pushLocation(slug, state.activeTaskId);
    }

    function switchProject(delta) {
        var slugs = sortedProjects(state.projects).map(function (p) { return p.slug; });
        if (!slugs.length) return;
        var idx = slugs.indexOf(state.activeSlug);
        if (idx === -1) idx = 0;
        idx = (idx + delta + slugs.length) % slugs.length;
        selectProject(slugs[idx], { pushHistory: true });
    }

    // ---------- task stack ----------

    function loadStack(initial) {
        if (!state.activeSlug) return Promise.resolve();
        var url = '/api/projects/' + encodeURIComponent(state.activeSlug) + '/tasks/stack';
        var headers = {};
        if (!initial && state.stackEtag) headers['If-None-Match'] = state.stackEtag;
        return fetch(url, { headers: headers }).then(function (r) {
            if (r.status === 304) return null;
            state.stackEtag = r.headers.get('ETag');
            return r.json();
        }).then(function (stack) {
            if (!stack) return;
            state.stack = stack;
            state.doneCursor = stack.cursor || null;
            if (initial) state.extraDone = [];
            renderMergedStack(initial);
        }).catch(function () {});
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
        fetch(url).then(function (r) { return r.json(); }).then(function (stack) {
            state.extraDone = state.extraDone.concat(stack.done_today || []);
            state.doneCursor = stack.cursor || null;
            renderMergedStack(false);
        }).catch(function () {});
    }

    function renderMergedStack(initial) {
        if (!state.stack) return;
        var merged = {
            needs_you: state.stack.needs_you || [],
            running: state.stack.running || [],
            queued: state.stack.queued || [],
            done_today: (state.stack.done_today || []).concat(state.extraDone),
            cursor: state.doneCursor
        };
        renderStack(merged, initial);
    }

    function rowKey(group, entry) {
        return group + ':' + (entry.task_id || entry.run_id || entry.title);
    }

    function renderStack(stack, initial) {
        var container = document.getElementById('console-stack');

        if (initial) {
            container.innerHTML = '';
            GROUPS.forEach(function (g) {
                var section = document.createElement('section');
                section.className = 'console-stack-group';

                var h = document.createElement('h3');
                h.className = 'console-stack-group-title';
                h.appendChild(document.createTextNode(g.label + ' '));
                var count = document.createElement('span');
                count.className = 'console-stack-group-count';
                h.appendChild(count);
                section.appendChild(h);

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

        var earlierBtn2 = document.getElementById('console-load-earlier');
        if (earlierBtn2) earlierBtn2.hidden = !stack.cursor;

        rebuildFlatIndex();
        applySelectionHighlight();
    }

    function diffGroup(groupKey, entries) {
        var list = document.getElementById('console-stack-list-' + groupKey);
        var countEl = list.parentElement.querySelector('.console-stack-group-count');
        countEl.textContent = entries.length ? '(' + entries.length + ')' : '';

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

        var empty = list.querySelector('.console-stack-empty');
        if (entries.length === 0) {
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

        var title = document.createElement('span');
        title.className = 'console-stack-row-title';
        title.textContent = entry.title || entry.task_id || entry.run_id || '(untitled)';
        row.appendChild(title);

        var meta = document.createElement('span');
        meta.className = 'console-stack-row-meta';
        meta.textContent = rowMetaText(groupKey, entry);
        row.appendChild(meta);
    }

    function rowMetaText(groupKey, entry) {
        switch (groupKey) {
            case 'needs_you':
                return entry.reason || '';
            case 'running':
                return (entry.current_step ? entry.current_step + ' · ' : '') + formatElapsed(entry.elapsed_seconds);
            case 'queued':
                return entry.reason || '';
            case 'done_today':
                return (entry.outcome || '') + ' · ' + formatDuration(entry.duration_seconds);
            default:
                return '';
        }
    }

    function rebuildFlatIndex() {
        var order = [];
        GROUPS.forEach(function (g) {
            var list = document.getElementById('console-stack-list-' + g.key);
            Array.prototype.forEach.call(list.children, function (el) {
                if (el.classList.contains('console-stack-row')) order.push(el.dataset.key);
            });
        });
        // Preserve the current selection's identity across a re-render.
        var selectedKey = state.rowOrder[state.selectedIndex];
        state.rowOrder = order;
        if (selectedKey) {
            var idx = order.indexOf(selectedKey);
            if (idx !== -1) state.selectedIndex = idx;
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
        pushLocation(state.activeSlug, state.activeTaskId);
    }

    function deselect() {
        state.activeTaskId = '';
        renderCentrePane(null, null);
        pushLocation(state.activeSlug, '');
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
                openContainersView();
                e.preventDefault();
                break;
            case '?':
                toggleHelp(true);
                e.preventDefault();
                break;
            case '[':
                if (detail) { switchAttempt(-1); e.preventDefault(); }
                break;
            case ']':
                if (detail) { switchAttempt(1); e.preventDefault(); }
                break;
            case 'g':
                if (detail) { scrollLogTo('top'); e.preventDefault(); }
                break;
            case 'G':
                if (detail) { scrollLogTo('bottom'); e.preventDefault(); }
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
        return fetch('/api/activity?project=' + encodeURIComponent(slug) + '&limit=1')
            .then(function (r) { return r.json(); })
            .then(function (resp) {
                if (slug !== state.activeSlug) return; // stale response from a since-abandoned project
                var entries = (resp && resp.entries) || [];
                var latest = entries[0];
                tickerEl.textContent = latest ? latest.text : '—';
                tickerEl.classList.toggle('console-ticker-failure', !!(latest && latest.failure));
            })
            .catch(function () {});
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
            threadLoadedFor: '' // run id the thread panel was last loaded for, '' = not loaded
        };

        renderDetailShell();

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

        var actionPanel = document.createElement('div');
        actionPanel.className = 'console-action-panel';
        actionPanel.id = 'console-action-panel';
        actionPanel.hidden = true;
        el.appendChild(actionPanel);

        var facts = document.createElement('div');
        facts.className = 'console-facts-block';
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
        renderLogPaneShell();
        if (attempt.attempt_id) {
            startDetailLogStream('attempt', attempt.attempt_id);
        } else {
            startDetailLogStream('run', attempt.run_id);
        }

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
        var note = document.createElement('p');
        note.className = 'console-facts-note';
        note.textContent = 'No attempts recorded for this task.';
        el.appendChild(note);
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
        var note = document.createElement('p');
        note.className = 'console-facts-note';
        note.textContent = 'Failed to load run detail.';
        el.appendChild(note);
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

    function headerPillClass(headerState, run) {
        switch (headerState) {
            case 'done': return 'badge-' + run.state;
            case 'needs_you': return 'badge-failed';
            case 'queued': return 'badge-pending';
            case 'running': return 'badge-running';
            case 'parked': return 'badge-parked';
            default: return 'badge-cancelled';
        }
    }

    function headerPillLabel(headerState, run) {
        if (headerState === 'done') return run.state;
        if (headerState === 'needs_you') return 'needs you';
        if (headerState === 'parked' && run.parked_seconds) return 'parked · ' + formatDuration(run.parked_seconds);
        return headerState;
    }

    function renderHeader(run) {
        var el = document.getElementById('console-task-header');
        if (!el) return;
        el.innerHTML = '';

        var title = document.createElement('h2');
        title.textContent = detail.title;
        el.appendChild(title);

        var headerState = computeHeaderState(run);
        var pill = document.createElement('span');
        pill.className = 'badge ' + headerPillClass(headerState, run);
        pill.textContent = headerPillLabel(headerState, run);
        el.appendChild(pill);

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
        }
        el.appendChild(actions);
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

    // ---------- centre pane: facts row + attempt tabs ----------

    function renderFactsRow(run) {
        var container = document.getElementById('console-facts-block');
        if (!container) return;
        container.innerHTML = '';

        if (detail.attempts.length > 1) {
            container.appendChild(renderAttemptTabs());
        }

        var dl = document.createElement('dl');
        dl.className = 'console-facts-row';
        factsForRun(run).forEach(function (f) {
            var dt = document.createElement('dt');
            dt.textContent = f[0];
            var dd = document.createElement('dd');
            dd.textContent = f[1];
            dl.appendChild(dt);
            dl.appendChild(dd);
        });
        container.appendChild(dl);
    }

    function renderAttemptTabs() {
        var wrap = document.createElement('div');
        wrap.className = 'console-attempt-tabs';
        detail.attempts.forEach(function (a, i) {
            var tab = document.createElement('button');
            tab.type = 'button';
            tab.className = 'console-attempt-tab' + (i === detail.attemptIndex ? ' console-attempt-tab-active' : '');

            var dot = document.createElement('span');
            dot.className = 'run-dot ' + attemptDotClass(a.outcome);
            tab.appendChild(dot);

            var label = document.createElement('span');
            label.textContent = '#' + a.attempt_num;
            tab.appendChild(label);

            if (a.duration) {
                var dur = document.createElement('span');
                dur.className = 'console-attempt-tab-duration';
                dur.textContent = a.duration;
                tab.appendChild(dur);
            }

            tab.title = (a.run_id || '') + ' · ' + (a.outcome || '');
            tab.addEventListener('click', function () { selectAttempt(i); });
            wrap.appendChild(tab);
        });
        return wrap;
    }

    function attemptDotClass(outcome) {
        switch (outcome) {
            case 'succeeded': return 'run-dot-succeeded';
            case 'failed': return 'run-dot-failed';
            case 'cancelled': return 'run-dot-cancelled';
            case 'running': return 'run-dot-running';
            default: return 'run-dot-pending';
        }
    }

    function factsForRun(run) {
        if (!run) return [['Status', 'Loading…']];

        var facts = [];
        facts.push(['Run', run.id]);
        if (run.child_runs && run.child_runs.length) {
            facts.push(['Child runs', run.child_runs.map(function (c) { return c.id; }).join(', ')]);
        }
        facts.push(['Container', (run.container_id ? shortId(run.container_id) : '—') + ' (' + (run.container_state || 'removed') + ')']);
        if (run.token_usage && run.token_usage.length) {
            facts.push(['Tokens', run.token_usage.map(function (u) {
                return u.agent_name + ': ' + u.input_tokens + '/' + u.output_tokens;
            }).join(', ')]);
        }
        if (run.prompt_file) {
            facts.push(['Prompt', run.prompt_file + (run.git_revision ? ' @ ' + run.git_revision : '')]);
        }
        var attempt = detail.attempts[detail.attemptIndex];
        if (attempt && attempt.retry_reason) {
            facts.push(['Retry reason', 'previous attempt failed at "' + attempt.retry_reason + '"']);
        }
        if (run.error_message) {
            facts.push(['Error', run.error_message]);
        }
        facts.push(['Timing', run.timing || '—']);
        return facts;
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

        clusterSteps(steps).forEach(function (cluster) {
            container.appendChild(renderStepCluster(cluster));
        });
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

    function renderStepCluster(cluster) {
        var box = document.createElement('div');
        box.className = 'console-step-cluster';
        box.appendChild(renderStepSegment(cluster.step, false));
        if (cluster.children.length) {
            var sub = document.createElement('div');
            sub.className = 'console-step-substrip';
            cluster.children.forEach(function (child) {
                sub.appendChild(renderStepSegment(child, true));
            });
            box.appendChild(sub);
        }
        return box;
    }

    function renderStepSegment(step, isChild) {
        var seg = document.createElement('button');
        seg.type = 'button';
        seg.className = 'console-step-segment' + (isChild ? ' console-step-segment-child' : '');

        var isScoped = detail.scopedStep && detail.scopedStep.run_id === step.run_id && detail.scopedStep.step_name === step.step_name;
        var isLive = !step.result && !!step.started_at;
        if (isScoped) seg.classList.add('console-step-segment-selected');
        if (isLive) seg.classList.add('console-step-segment-live');

        var dot = document.createElement('span');
        dot.className = 'run-dot ' + stepDotClass(step);
        seg.appendChild(dot);

        var name = document.createElement('span');
        name.className = 'console-step-segment-name';
        name.textContent = step.step_name;
        seg.appendChild(name);

        var metaText = step.duration || (isLive ? 'running…' : '');
        if (step.poll_count) {
            metaText += (metaText ? ' · ' : '') + '⟳' + step.poll_count + ' · ' + step.last_poll_at;
        }
        if (metaText) {
            var meta = document.createElement('span');
            meta.className = 'console-step-segment-meta';
            meta.textContent = metaText;
            seg.appendChild(meta);
        }

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
        detail.scopedStep = { run_id: step.run_id, step_name: step.step_name };
        if (detail.run) renderStepStrip(detail.run);
        loadStepOutput();
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

    function renderLogPaneShell() {
        var el = document.getElementById('console-log-pane');
        if (!el) return;
        el.innerHTML = '';

        var toolbar = document.createElement('div');
        toolbar.className = 'console-log-toolbar';

        var status = document.createElement('span');
        status.id = 'console-log-status';
        status.className = 'badge badge-running';
        status.textContent = 'Live';
        toolbar.appendChild(status);

        var scopeIndicator = document.createElement('button');
        scopeIndicator.type = 'button';
        scopeIndicator.id = 'console-log-scope';
        scopeIndicator.className = 'console-log-scope';
        scopeIndicator.hidden = true;
        scopeIndicator.addEventListener('click', clearStepScope);
        toolbar.appendChild(scopeIndicator);

        var select = document.createElement('select');
        select.id = 'console-log-type-filter';
        ['all', 'llm', 'script', 'status'].forEach(function (t) {
            var opt = document.createElement('option');
            opt.value = t;
            opt.textContent = t === 'all' ? 'All types' : t;
            select.appendChild(opt);
        });
        select.value = detail.logTypeFilter;
        select.addEventListener('change', function () {
            detail.logTypeFilter = select.value;
            if (detail.scopedStep) {
                loadStepOutput();
            } else {
                renderLogLines();
            }
        });
        toolbar.appendChild(select);

        var wrapBtn = document.createElement('button');
        wrapBtn.type = 'button';
        wrapBtn.className = 'btn btn-sm btn-secondary';
        wrapBtn.textContent = 'Wrap: ' + (detail.logWrap ? 'On' : 'Off');
        wrapBtn.addEventListener('click', function () {
            detail.logWrap = !detail.logWrap;
            wrapBtn.textContent = 'Wrap: ' + (detail.logWrap ? 'On' : 'Off');
            applyLogWrap();
        });
        toolbar.appendChild(wrapBtn);

        var followBtn = document.createElement('button');
        followBtn.type = 'button';
        followBtn.className = 'btn btn-sm btn-secondary';
        followBtn.textContent = 'Follow: ' + (detail.logFollow ? 'On' : 'Off');
        followBtn.addEventListener('click', function () {
            detail.logFollow = !detail.logFollow;
            followBtn.textContent = 'Follow: ' + (detail.logFollow ? 'On' : 'Off');
            if (detail.logFollow) scrollLogTo('bottom');
        });
        toolbar.appendChild(followBtn);

        el.appendChild(toolbar);

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
    }

    function applyLogWrap() {
        var pre = document.getElementById('console-log-content');
        if (pre) pre.style.whiteSpace = detail.logWrap ? 'pre-wrap' : 'pre';
    }

    function updateLogScopeIndicator() {
        var el = document.getElementById('console-log-scope');
        if (el) {
            if (detail.scopedStep) {
                el.hidden = false;
                el.textContent = 'Step: ' + detail.scopedStep.step_name + ' ✕';
            } else {
                el.hidden = true;
            }
        }
        var earlierBtn = document.getElementById('console-log-earlier');
        if (earlierBtn) earlierBtn.hidden = !!detail.scopedStep || !detail.logSkipped;
    }

    function setLogStatus(status) {
        if (detail) detail.logStatus = status;
        var el = document.getElementById('console-log-status');
        if (!el) return;
        if (status === 'live') {
            el.textContent = 'Live';
            el.className = 'badge badge-running';
        } else if (status === 'complete') {
            el.textContent = 'Complete';
            el.className = 'badge badge-succeeded';
        } else if (status === 'disconnected') {
            el.textContent = 'Disconnected';
            el.className = 'badge badge-failed';
        }
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
                    visible.forEach(function (l) { frag.appendChild(buildLogLineEl(l)); });
                    pre.insertBefore(frag, pre.firstChild);
                    viewer.scrollTop += viewer.scrollHeight - prevHeight;
                }
                updateLogScopeIndicator();
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
        pre.appendChild(buildLogLineEl(line));
        if (detail.logFollow) scrollLogTo('bottom');
    }

    function buildLogLineEl(line) {
        var span = document.createElement('span');
        span.className = 'log-line log-type-' + (line.type || 'script');
        var prefix = '';
        if (line.timestamp) prefix += '[' + line.timestamp + '] ';
        prefix += '[' + (line.type || '') + '] ';
        if (!detail.scopedStep && line.step_name) prefix += '(' + line.step_name + ') ';
        span.textContent = prefix + line.content + '\n';
        return span;
    }

    function renderLogLines() {
        var pre = document.getElementById('console-log-content');
        if (!pre || !detail) return;
        pre.innerHTML = '';
        var lines = detail.scopedStep ? detail.stepLines : detail.allLines;
        var filter = detail.logTypeFilter;
        var frag = document.createDocumentFragment();
        (lines || []).forEach(function (line) {
            if (filter !== 'all' && line.type !== filter) return;
            frag.appendChild(buildLogLineEl(line));
        });
        pre.appendChild(frag);
        if (detail.logFollow) scrollLogTo('bottom');
    }

    function scrollLogTo(where) {
        var viewer = document.getElementById('console-log-viewer');
        if (!viewer) return;
        viewer.scrollTop = where === 'top' ? 0 : viewer.scrollHeight;
    }

    // ---------- secondary views (Workflows / Intent / Containers) ----------
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
        ['console-view-workflows-btn', 'console-view-intent-btn', 'console-view-containers-btn'].forEach(function (id) {
            document.getElementById(id).disabled = disabled;
        });
    }

    document.getElementById('console-view-close').addEventListener('click', closeView);
    document.getElementById('step-drawer-close').addEventListener('click', closeStepDrawer);
    document.getElementById('intent-drawer-close').addEventListener('click', closeIntentDrawer);
    document.getElementById('console-view-workflows-btn').addEventListener('click', openWorkflowsView);
    document.getElementById('console-view-intent-btn').addEventListener('click', openIntentView);
    document.getElementById('console-view-containers-btn').addEventListener('click', openContainersView);
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

                var locationTabs = document.getElementById('location-tabs');
                if (hasContainer && hasHost) {
                    locationTabs.innerHTML =
                        '<button type="button" class="tab-btn tab-active" data-location="container">Container</button>' +
                        '<button type="button" class="tab-btn" data-location="host">Host</button>';
                } else {
                    locationTabs.innerHTML = '';
                }

                activeLocation = hasContainer ? 'container' : 'host';
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
            b.classList.toggle('tab-active', b.getAttribute('data-location') === loc);
        });
        renderWorkflowTabs();
    }

    function renderWorkflowTabs() {
        var filtered = workflowsData.filter(function (wf) { return wf.location === activeLocation; });
        var tabs = document.getElementById('workflow-tabs');

        if (filtered.length === 0) {
            tabs.innerHTML = '';
            document.getElementById('workflow-dag').innerHTML = '<p class="empty">No ' + activeLocation + ' workflows found</p>';
            return;
        }

        if (filtered.length > 1) {
            var html = '';
            filtered.forEach(function (wf, i) {
                var badge = wf.builtin ? ' <span class="badge badge-cancelled">built-in</span>' : '';
                html += '<button type="button" class="tab-btn' + (i === 0 ? ' tab-active' : '') +
                    '" data-workflow-index="' + i + '">' + escapeHtml(wf.name) + badge + '</button>';
            });
            tabs.innerHTML = html;
        } else {
            tabs.innerHTML = '';
        }
        showFilteredWorkflow(0);
    }

    function showFilteredWorkflow(idx) {
        var filtered = workflowsData.filter(function (wf) { return wf.location === activeLocation; });
        var btns = document.querySelectorAll('#workflow-tabs .tab-btn');
        Array.prototype.forEach.call(btns, function (b, i) { b.classList.toggle('tab-active', i === idx); });
        showWorkflow(filtered[idx]);
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

    // ---------- Intent view ----------

    var intentData = { requirements: [], last_scan_at: '' };
    var domainsData = { version: 1, domains: [] };
    var scanStatus = null; // { runId, state, taskId }
    var scanPollTimer = null;

    function formatIntentTime(s) {
        return s ? formatTimestamp(s) : 'never';
    }

    function isTerminalRunState(s) {
        return s === 'succeeded' || s === 'failed' || s === 'cancelled';
    }

    function openIntentView() {
        if (!state.activeSlug) return;
        openView('Intent');
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
                intentData = data || { requirements: [], last_scan_at: '' };
                var metaEl = document.getElementById('intent-meta');
                if (metaEl) metaEl.textContent = 'Last scan: ' + formatIntentTime(intentData.last_scan_at);
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

        var loopBtn = document.createElement('button');
        loopBtn.type = 'button';
        loopBtn.id = 'console-loop-toggle';
        loopBtn.className = 'btn btn-sm ' + (state.loopRunning ? 'btn-danger' : 'btn-secondary');
        loopBtn.textContent = state.loopRunning ? 'Stop loop' : 'Start loop';
        loopBtn.addEventListener('click', toggleLoop);
        el.appendChild(loopBtn);

        var occ = data.occupancy || {};
        var busy = (occ.slots || []).length;
        var max = occ.max_concurrency || 0;
        el.appendChild(instrumentSpan('console-slot-meter', 'Slots ' + busy + '/' + max));
        el.appendChild(instrumentSpan('console-queue-depth', 'Queue ' + ((occ.queued || []).length)));
        el.appendChild(instrumentSpan('console-burn', 'Burn ' + formatBurn(data.usage)));
        el.appendChild(instrumentSpan('console-version', 'v' + (root.dataset.clocheVersion || '')));
    }

    function instrumentSpan(cls, text) {
        var span = document.createElement('span');
        span.className = 'console-instrument ' + cls;
        span.textContent = text;
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
        if (total <= 0) return '0/hr';
        return formatNumber(total) + '/hr';
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

    function init() {
        var loc = parseLocation();
        var initialSlug = loc.slug || root.dataset.projectSlug || '';
        var initialTaskId = loc.taskId || root.dataset.taskId || '';

        loadProjects().then(function () {
            if (!initialSlug && state.projects.length) {
                initialSlug = sortedProjects(state.projects)[0].slug;
            }
            if (initialSlug) {
                selectProject(initialSlug, { taskId: initialTaskId, pushHistory: false });
            } else {
                renderCentrePane(null, null);
            }
        });
    }

    init();
})();
