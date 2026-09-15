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
        instrumentsTimer: null
    };

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
        renderTabBar();
        renderCentrePane(null, null);

        loadInstruments();
        startInstrumentsPolling();

        loadStack(true).then(function () {
            startStackPolling();
            if (state.activeTaskId) selectTaskById(state.activeTaskId);
        });

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
            case '?':
                toggleHelp(true);
                e.preventDefault();
                break;
            default:
                break;
        }
    });

    function toggleHelp(show) {
        document.getElementById('console-help-overlay').hidden = !show;
    }
    document.getElementById('console-help-close').addEventListener('click', function () { toggleHelp(false); });

    // ---------- centre pane ----------

    function renderCentrePane(entry, group) {
        var el = document.getElementById('console-centre');
        el.innerHTML = '';

        if (!entry) {
            var empty = document.createElement('div');
            empty.className = 'console-centre-empty';
            empty.textContent = 'Select a task from the stack.';
            el.appendChild(empty);
            return;
        }

        var header = document.createElement('div');
        header.className = 'console-task-header';

        var title = document.createElement('h2');
        title.textContent = entry.title || entry.task_id || '(untitled task)';
        header.appendChild(title);

        var label = groupStatusLabel(group, entry);
        if (label) {
            var pill = document.createElement('span');
            pill.className = 'badge ' + groupBadgeClass(group, entry);
            pill.textContent = label;
            header.appendChild(pill);
        }
        el.appendChild(header);

        var facts = document.createElement('dl');
        facts.className = 'console-facts-row';
        factsFor(group, entry).forEach(function (f) {
            var dt = document.createElement('dt');
            dt.textContent = f[0];
            var dd = document.createElement('dd');
            dd.textContent = f[1];
            facts.appendChild(dt);
            facts.appendChild(dd);
        });
        el.appendChild(facts);

        if (!group) {
            var note = document.createElement('p');
            note.className = 'console-facts-note';
            note.textContent = 'This task is not in the current stack view — no further detail is available here yet.';
            el.appendChild(note);
        }
    }

    function groupBadgeClass(group, entry) {
        switch (group) {
            case 'done_today': return 'badge-' + entry.outcome;
            case 'running': return 'badge-running';
            case 'queued': return 'badge-pending';
            case 'needs_you': return 'badge-failed';
            default: return 'badge-cancelled';
        }
    }

    function groupStatusLabel(group, entry) {
        switch (group) {
            case 'done_today': return entry.outcome;
            case 'running': return 'running';
            case 'queued': return 'queued';
            case 'needs_you': return 'needs you';
            default: return '';
        }
    }

    function factsFor(group, entry) {
        switch (group) {
            case 'needs_you':
                return [
                    ['Reason', entry.reason || '—'],
                    ['Since', formatTimestamp(entry.since)],
                    ['Run', entry.run_id || '—']
                ];
            case 'running':
                return [
                    ['Attempt', String(entry.attempt || 1)],
                    ['Current step', entry.current_step || '—'],
                    ['Started', formatTimestamp(entry.started_at)],
                    ['Elapsed', formatElapsed(entry.elapsed_seconds)]
                ];
            case 'queued':
                return [
                    ['Reason', entry.reason || '—'],
                    ['Since', formatTimestamp(entry.since)],
                    ['Run', entry.run_id || '—']
                ];
            case 'done_today':
                return [
                    ['Outcome', entry.outcome || '—'],
                    ['Completed', formatTimestamp(entry.completed_at)],
                    ['Duration', formatDuration(entry.duration_seconds)]
                ];
            default:
                return [['Task ID', entry.task_id || '—']];
        }
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
