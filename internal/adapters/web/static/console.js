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

        stopStackPolling();
        stopInstrumentsPolling();
        stopTickerPolling();
        renderTabBar();
        renderCentrePane(null, null);

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
            pollTimer: null
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
        manageDetailPoll(run);
    }

    function manageDetailPoll(run) {
        stopDetailPoll();
        if (run.state === 'running' || run.state === 'pending' || run.state === 'waiting') {
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
