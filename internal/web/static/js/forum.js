import { state } from './state.js';
import { fetchJSON, escapeHtml, showToast } from './utils.js';

// ── Forum Communication Layer ──

export async function forumFetch(url, options) {
    var resp = await fetch(url, options);
    if (!resp.ok) {
        var errText = 'HTTP ' + resp.status;
        try { var d = await resp.json(); if (d.error) errText = d.error; } catch(e) { /* ignore parse errors */ }
        throw new Error(errText);
    }
    return resp;
}

export async function loadForumIndex() {
    try {
        var resp = await forumFetch('/api/v1/forum/categories');
        var data = await resp.json();
        state.forumLoaded = true;
        var container = document.getElementById('forumIndex');
        if (!container) return;
        var categories = data.categories || [];
        if (categories.length === 0) {
            container.innerHTML = '<div style="padding:10px 16px;color:var(--text-dim)">No categories found.</div>';
            return;
        }
        // Group categories by type
        var groups = { system: [], project: [], agent: [], custom: [] };
        var groupLabels = { system: 'General', project: 'Projects', agent: 'Agents', custom: 'Custom' };
        categories.forEach(function(c) { (groups[c.category.type] || groups.custom).push(c); });
        // Populate new thread category selector
        var select = document.getElementById('newThreadCategory');
        if (select) {
            var optHtml = '';
            categories.filter(function(c) { return c.category.type === 'system' || c.category.type === 'custom'; }).forEach(function(c) {
                var sel = c.category.id === 'discussions' ? ' selected' : '';
                optHtml += '<option value="' + c.category.id + '"' + sel + '>' + escapeHtml(c.category.name) + '</option>';
            });
            select.innerHTML = optHtml;
        }
        var groupDescs = {
            system: 'General discussion, announcements, and system reports',
            project: 'Threads organized by project',
            agent: 'Each cognitive agent has its own sub-forum',
            custom: 'User-created categories'
        };
        var groupColors = { system: 'var(--accent-cyan)', project: 'var(--accent-green)', agent: 'var(--accent-violet)', custom: 'var(--accent-orange)' };
        var groupIcons = { system: 'GN', project: 'PJ', agent: 'AG', custom: 'CU' };

        // Build the phpBB-style forum index: top-level categories as rows
        // with sub-forums listed inline below the description
        var html = '<div class="forabg">';
        html += '<div class="forabg-head"><span class="forabg-title">Forum Index</span></div>';
        html += '<div class="inner">';
        html += '<ul class="topiclist"><li class="header"><dl class="row-item">';
        html += '<dt><div class="list-inner">Category</div></dt>';
        html += '<dd class="posts">Threads</dd>';
        html += '<dd class="posts">Posts</dd>';
        html += '<dd class="lastpost">Last post</dd>';
        html += '</dl></li></ul>';
        html += '<ul class="topiclist forums">';

        var rowIdx = 0;
        ['system', 'project', 'agent', 'custom'].forEach(function(type) {
            var cats = groups[type];
            if (cats.length === 0) return;
            var totalThreads = cats.reduce(function(s, c) { return s + c.thread_count; }, 0);
            var totalPosts = cats.reduce(function(s, c) { return s + c.post_count; }, 0);
            var bgClass = rowIdx % 2 === 0 ? 'bg1' : 'bg2';
            rowIdx++;

            // Find the most recent post across all sub-forums in this group
            var lastInfo = '';
            var lastTime = 0;
            cats.forEach(function(c) {
                if (c.last_post) {
                    var t = new Date(c.last_post.created_at).getTime();
                    if (t > lastTime) {
                        lastTime = t;
                        var lp = c.last_post;
                        var lpAuthor = lp.author_type === 'agent' ? ('@' + (lp.author_key || 'agent')) : (lp.author_name || 'Human');
                        var lpTime = new Date(lp.created_at).toLocaleString([], { month: 'short', day: 'numeric', hour: '2-digit', minute: '2-digit' });
                        lastInfo = '<span style="font-size:0.75rem;color:var(--text-dim)">' + escapeHtml(lpAuthor) + '<br>' + lpTime + '</span>';
                    }
                }
            });

            // Build sub-forum links (shown inline below description)
            var subLinks = cats.map(function(c) {
                var cat = c.category;
                var badge = c.thread_count > 0 ? ' <span style="font-size:0.65rem;color:var(--text-dim)">(' + c.thread_count + ')</span>' : '';
                return '<a onclick="loadForumCategory(\'' + cat.id + '\', \'' + escapeHtml(cat.name).replace(/'/g, "\\'") + '\')" style="color:' + (cat.color || 'var(--link)') + ';cursor:pointer;font-size:0.78rem">' + escapeHtml(cat.name) + badge + '</a>';
            }).join(', ');

            html += '<li class="row ' + bgClass + '" style="cursor:pointer" onclick="loadForumGroup(\'' + type + '\')">';
            html += '<dl class="row-item"><dt>';
            html += '<span class="status-icon" style="width:32px;height:32px;font-size:0.7rem;flex-shrink:0;background:color-mix(in srgb, ' + groupColors[type] + ' 15%, transparent);color:' + groupColors[type] + ';border:1px solid color-mix(in srgb, ' + groupColors[type] + ' 25%, transparent)">' + groupIcons[type] + '</span>';
            html += '<div class="list-inner">';
            html += '<a class="topictitle" style="color:var(--link);font-size:0.95rem">' + groupLabels[type] + '</a>';
            html += '<br><span style="font-size:0.78rem;color:var(--text-dim)">' + groupDescs[type] + '</span>';
            html += '<br><span style="font-size:0.72rem;color:var(--text-dim)">Sub-forums: </span>' + subLinks;
            html += '</div></dt>';
            html += '<dd class="posts">' + totalThreads + '</dd>';
            html += '<dd class="posts">' + totalPosts + '</dd>';
            html += '<dd class="lastpost">' + lastInfo + '</dd>';
            html += '</dl></li>';
        });

        html += '</ul></div></div>';

        // Memory System group — links to episode, memory, pattern, abstraction views
        html += '<div class="forabg" style="margin-bottom:2px">';
        html += '<div class="forabg-head"><span class="forabg-title">Memory System</span></div>';
        html += '<div class="inner">';
        html += '<ul class="topiclist"><li class="header"><dl class="row-item">';
        html += '<dt><div class="list-inner">Section</div></dt>';
        html += '<dd class="posts">Count</dd>';
        html += '<dd class="posts"></dd>';
        html += '<dd class="lastpost"></dd>';
        html += '</dl></li></ul>';
        html += '<ul class="topiclist forums">';

        var memSections = [
            { id: 'episodes', name: 'Episodes', desc: 'Temporal groupings of observations', icon: 'EP', color: 'var(--accent-violet)', countId: 'epCount' },
            { id: 'memories', name: 'Recent Memories', desc: 'Encoded knowledge from all sources', icon: 'MM', color: 'var(--accent-cyan)', countId: 'memCount' },
            { id: 'handoffs', name: 'Session Handoffs', desc: 'Handoff memories from prior sessions', icon: 'HO', color: 'var(--accent-yellow)', countId: 'handoffCount' },
            { id: 'patterns', name: 'Discovered Patterns', desc: 'Recurring patterns across memories', icon: 'PT', color: 'var(--accent-orange)', countId: 'patCount' },
            { id: 'abstractions', name: 'Abstractions & Principles', desc: 'Higher-order knowledge and axioms', icon: 'AB', color: 'var(--accent-green)', countId: 'absCount' },
        ];
        for (var mi = 0; mi < memSections.length; mi++) {
            var ms = memSections[mi];
            var bgClass = mi % 2 === 0 ? 'bg1' : 'bg2';
            html += '<li class="row ' + bgClass + '" id="memsec-' + ms.id + '" onclick="window._loadMemSec(\'' + ms.id + '\')" style="cursor:pointer">';
            html += '<dl class="row-item"><dt>';
            html += '<span class="status-icon" style="width:28px;height:28px;font-size:0.6rem;flex-shrink:0;background:color-mix(in srgb, ' + ms.color + ' 15%, transparent);color:' + ms.color + ';border:1px solid color-mix(in srgb, ' + ms.color + ' 25%, transparent)">' + ms.icon + '</span>';
            html += '<div class="list-inner">';
            html += '<a class="topictitle" style="color:var(--link)">' + ms.name + '</a>';
            html += '<br><span style="font-size:0.75rem;color:var(--text-dim)">' + ms.desc + '</span>';
            html += '</div></dt>';
            html += '<dd class="posts" id="forumIdx_' + ms.countId + '"></dd>';
            html += '<dd class="posts"></dd>';
            html += '<dd class="lastpost"></dd>';
            html += '</dl></li>';
        }
        html += '</ul></div></div>';

        html += '<div style="padding:6px 16px"><button class="recall-btn" style="font-size:0.78rem;padding:3px 10px" onclick="showNewThreadForm()">+ New Thread</button></div>';
        container.innerHTML = html;

        // Bind click handlers for memory section rows via delegation
        container.addEventListener('click', function(e) {
            var row = e.target.closest('[id^="memsec-"]');
            if (row) {
                var secId = row.id.replace('memsec-', '');
                var secNames = { episodes: 'Episodes', memories: 'Recent Memories', handoffs: 'Session Handoffs', patterns: 'Discovered Patterns', abstractions: 'Abstractions & Principles' };
                if (secNames[secId]) loadMemorySection(secId, secNames[secId]);
            }
        });
    } catch (e) {
        console.error('Failed to load forum index:', e);
        var c = document.getElementById('forumIndex');
        if (c) c.innerHTML = '<div style="padding:16px;color:var(--text-dim)">Failed to load forum: ' + escapeHtml(e.message) + '</div>';
    }
}

window._loadMemSec = function(id) {
    var names = { episodes: 'Episodes', memories: 'Recent Memories', handoffs: 'Session Handoffs', patterns: 'Discovered Patterns', abstractions: 'Abstractions & Principles' };
    loadMemorySection(id, names[id] || id);
};
// Per-section cache + sort state. Cleared on reload.
var _memSectionState = {};

function _memCompare(a, b, dir) {
    if (a == null && b == null) return 0;
    if (a == null) return dir === 'asc' ? 1 : -1;
    if (b == null) return dir === 'asc' ? -1 : 1;
    if (typeof a === 'number' && typeof b === 'number') return dir === 'asc' ? a - b : b - a;
    var sa = String(a).toLowerCase(), sb = String(b).toLowerCase();
    if (sa < sb) return dir === 'asc' ? -1 : 1;
    if (sa > sb) return dir === 'asc' ? 1 : -1;
    return 0;
}

function _memSortedRows(sectionId) {
    var st = _memSectionState[sectionId];
    if (!st || !st.rows) return [];
    var rows = st.rows.slice();
    if (st.sort && st.sort.col && st.cfg.columns) {
        var col = st.cfg.columns.find(function(c) { return c.key === st.sort.col; });
        if (col && col.accessor) {
            rows.sort(function(a, b) { return _memCompare(col.accessor(a), col.accessor(b), st.sort.dir); });
        }
    }
    return rows;
}

window._memSortToggle = function(sectionId, col) {
    var st = _memSectionState[sectionId];
    if (!st) return;
    if (st.sort && st.sort.col === col) {
        st.sort.dir = st.sort.dir === 'asc' ? 'desc' : 'asc';
    } else {
        st.sort = { col: col, dir: 'desc' };
    }
    _memRenderSection(sectionId);
};

window._memAction = async function(sectionId, id, action) {
    if (typeof event !== 'undefined' && event.stopPropagation) event.stopPropagation();
    var st = _memSectionState[sectionId];
    if (!st) return;
    var cfg = st.cfg;
    try {
        if (action === 'delete') {
            var label = cfg.itemLabel || 'item';
            if (!confirm('Permanently delete this ' + label + '? This cannot be undone.')) return;
            var resp = await fetch(cfg.apiBase + '/' + encodeURIComponent(id), { method: 'DELETE' });
            if (!resp.ok) {
                var msg = 'HTTP ' + resp.status;
                try { var d = await resp.json(); if (d.error) msg = d.error; } catch(e) { /* ignore parse */ }
                showToast('Delete failed: ' + msg, 'error');
                return;
            }
            st.rows = st.rows.filter(function(r) { return r[cfg.idKey || 'id'] !== id; });
            _memRenderSection(sectionId);
            showToast(label + ' deleted', 'success');
        } else if (action === 'edit') {
            var row = st.rows.find(function(r) { return r[cfg.idKey || 'id'] === id; });
            if (!row) return;
            if (!cfg.editFields) { showToast('Editing not supported for this section', 'info'); return; }
            var updates = {};
            for (var i = 0; i < cfg.editFields.length; i++) {
                var field = cfg.editFields[i];
                var current = row[field];
                if (Array.isArray(current)) current = current.join(', ');
                var val = prompt('Edit ' + field + ':', current == null ? '' : String(current));
                if (val === null) return;
                if (field === 'concepts') updates[field] = val.split(',').map(function(s) { return s.trim(); }).filter(Boolean);
                else updates[field] = val;
            }
            var resp2 = await fetch(cfg.apiBase + '/' + encodeURIComponent(id), {
                method: 'PATCH',
                headers: { 'Content-Type': 'application/json' },
                body: JSON.stringify(updates),
            });
            if (!resp2.ok) {
                var msg2 = 'HTTP ' + resp2.status;
                try { var d2 = await resp2.json(); if (d2.error) msg2 = d2.error; } catch(e) { /* ignore parse */ }
                showToast('Edit failed: ' + msg2, 'error');
                return;
            }
            Object.assign(row, updates);
            _memRenderSection(sectionId);
            showToast((cfg.itemLabel || 'item') + ' updated', 'success');
        }
    } catch (e) {
        console.error('Memory section action failed:', e);
        showToast('Action failed: ' + e.message, 'error');
    }
};

function _memRenderHeader(sectionId) {
    var st = _memSectionState[sectionId];
    var cols = st.cfg.columns;
    var sort = st.sort || {};
    var h = '<ul class="topiclist"><li class="header"><dl class="row-item">';
    for (var i = 0; i < cols.length; i++) {
        var c = cols[i];
        var arrow = sort.col === c.key ? (sort.dir === 'asc' ? ' ▲' : ' ▼') : '';
        var labelHtml = c.sortable === false
            ? escapeHtml(c.label)
            : '<span style="cursor:pointer;user-select:none" onclick="event.stopPropagation(); window._memSortToggle(\'' + sectionId + '\', \'' + c.key + '\')">' + escapeHtml(c.label) + arrow + '</span>';
        if (i === 0) h += '<dt><div class="list-inner">' + labelHtml + '</div></dt>';
        else if (i === cols.length - 1) h += '<dd class="lastpost">' + labelHtml + '</dd>';
        else h += '<dd class="posts">' + labelHtml + '</dd>';
    }
    if (st.cfg.editFields || st.cfg.deletable !== false) {
        h += '<dd class="posts" style="min-width:70px">Actions</dd>';
    }
    h += '</dl></li></ul>';
    return h;
}

function _memRowActions(sectionId, id) {
    var cfg = _memSectionState[sectionId].cfg;
    var parts = [];
    if (cfg.editFields) {
        parts.push('<button type="button" class="recall-btn" style="font-size:0.7rem;padding:2px 6px" onclick="window._memAction(\'' + sectionId + '\', \'' + id + '\', \'edit\')" title="Edit">✎</button>');
    }
    if (cfg.deletable !== false) {
        parts.push('<button type="button" class="recall-btn" style="font-size:0.7rem;padding:2px 6px;color:var(--accent-red);border-color:var(--accent-red)" onclick="window._memAction(\'' + sectionId + '\', \'' + id + '\', \'delete\')" title="Delete">🗑</button>');
    }
    return '<dd class="posts" style="min-width:70px;display:flex;gap:4px;align-items:center;justify-content:center">' + parts.join('') + '</dd>';
}

function _memRenderSection(sectionId) {
    var st = _memSectionState[sectionId];
    if (!st) return;
    var container = document.getElementById('threadContent');
    if (!container) return;
    var rows = _memSortedRows(sectionId);
    var html = '<div class="forabg" style="margin:0"><div class="forabg-head"><span class="forabg-title">' + escapeHtml(st.cfg.title) + '</span><span class="forabg-meta">' + rows.length + ' ' + (st.cfg.unit || 'items') + '</span></div>';
    html += '<div class="inner">' + _memRenderHeader(sectionId);
    html += '<ul class="topiclist forums">';
    for (var i = 0; i < rows.length; i++) {
        var bgClass = i % 2 === 0 ? 'bg1' : 'bg2';
        var rowHtml = st.cfg.renderRow(rows[i], bgClass);
        if (st.cfg.editFields || st.cfg.deletable !== false) {
            var id = rows[i][st.cfg.idKey || 'id'];
            var actionsCell = _memRowActions(sectionId, id);
            rowHtml = rowHtml.replace('</dl></li>', actionsCell + '</dl></li>');
        }
        html += rowHtml;
    }
    html += '</ul></div></div>';
    container.innerHTML = html;
}

function _buildMemoriesConfig(title, unit, typeFilter) {
    var url = '/memories?state=active&limit=' + (typeFilter ? 1000 : 50) + (typeFilter ? '&type=' + encodeURIComponent(typeFilter) : '');
    return {
        title: title, unit: unit, itemLabel: typeFilter === 'handoff' ? 'handoff' : 'memory',
        fetch: function() { return fetchJSON(url).then(function(d) { return d.memories || []; }); },
        apiBase: '/api/v1/memories',
        editFields: ['summary', 'content', 'concepts'],
        columns: [
            { label: typeFilter === 'handoff' ? 'Handoff' : 'Memory', key: 'summary', accessor: function(r) { return r.summary || ''; } },
            { label: 'Salience', key: 'salience', accessor: function(r) { return r.salience || 0; } },
            { label: 'Type', key: 'type', accessor: function(r) { return r.type || ''; } },
            { label: 'Created', key: 'created_at', accessor: function(r) { return r.created_at || ''; } },
        ],
        renderRow: function(m, bgClass) {
            var time = new Date(m.created_at).toLocaleString([], { month: 'short', day: 'numeric', hour: '2-digit', minute: '2-digit' });
            var typeColor = { decision: 'var(--accent-orange)', error: 'var(--accent-red)', insight: 'var(--accent-violet)', learning: 'var(--accent-blue)', handoff: 'var(--accent-yellow)' }[m.type] || 'var(--text-dim)';
            var iconLabel = m.type === 'handoff' ? 'HO' : (m.type || 'G').charAt(0).toUpperCase();
            var summaryLimit = typeFilter === 'handoff' ? 160 : 100;
            var h = '<li class="row ' + bgClass + '" style="cursor:pointer" onclick="this.querySelector(\'.expand-zone\').classList.toggle(\'open\')">';
            h += '<dl class="row-item"><dt>';
            h += '<span class="status-icon" style="width:26px;height:26px;font-size:0.6rem;flex-shrink:0;background:color-mix(in srgb, ' + typeColor + ' 15%, transparent);color:' + typeColor + ';border:1px solid color-mix(in srgb, ' + typeColor + ' 25%, transparent)">' + iconLabel + '</span>';
            h += '<div class="list-inner">';
            h += '<a class="topictitle" style="color:var(--link)">' + escapeHtml(m.summary || '').slice(0, summaryLimit) + '</a>';
            if (m.concepts && m.concepts.length) h += '<br><span style="font-size:0.72rem;color:var(--text-dim)">' + m.concepts.slice(0, 5).map(function(c) { return escapeHtml(c); }).join(' · ') + '</span>';
            h += '<div class="expand-zone" style="margin-top:6px;padding:8px;background:var(--bg-row-alt);border-radius:4px;font-size:0.82rem;color:var(--text-secondary);white-space:pre-wrap">';
            h += escapeHtml(m.content || '');
            if (m.source) h += '<br><span style="color:var(--text-dim)">Source: ' + escapeHtml(m.source) + '</span>';
            if (m.project) h += ' · <span style="color:var(--text-dim)">Project: ' + escapeHtml(m.project) + '</span>';
            if (m.episode_id) h += '<br><a onclick="event.stopPropagation(); window.loadThread(\'' + m.episode_id + '\')" style="color:var(--accent-cyan);cursor:pointer">View episode thread →</a>';
            h += '</div>';
            h += '</div></dt>';
            h += '<dd class="posts" style="color:' + typeColor + '">' + (m.salience || 0).toFixed(2) + '</dd>';
            h += '<dd class="posts" style="font-size:0.72rem">' + escapeHtml(m.type || 'general') + '</dd>';
            h += '<dd class="lastpost"><span style="font-size:0.75rem;color:var(--text-dim)">' + time + '</span></dd>';
            h += '</dl></li>';
            return h;
        },
    };
}

function _memSectionConfigs() {
    return {
        episodes: {
            title: 'Episodes', unit: 'episodes', itemLabel: 'episode',
            fetch: function() { return fetchJSON('/episodes?limit=50').then(function(d) { return d.episodes || []; }); },
            apiBase: '/api/v1/episodes',
            deletable: false,
            columns: [
                { label: 'Episode', key: 'title', accessor: function(r) { return r.title || ''; } },
                { label: 'Obs', key: 'obs', accessor: function(r) { return (r.raw_memory_ids || []).length; } },
                { label: 'Files', key: 'files', accessor: function(r) { return (r.files_modified || []).length; } },
                { label: 'Activity', key: 'activity', accessor: function(r) { return r.end_time || r.start_time || ''; } },
            ],
            renderRow: function(ep, bgClass) {
                var mems = (ep.raw_memory_ids || []).length;
                var files = (ep.files_modified || []).length;
                var time = ep.end_time ? new Date(ep.end_time).toLocaleString([], { month: 'short', day: 'numeric', hour: '2-digit', minute: '2-digit' }) : '';
                var stateLabel = ep.state === 'open' ? '<span style="color:var(--accent-green)">open</span>' : '';
                var h = '<li class="row ' + bgClass + '" onclick="window.loadThread(\'' + ep.id + '\')" style="cursor:pointer">';
                h += '<dl class="row-item"><dt>';
                h += '<span class="status-icon" style="width:26px;height:26px;font-size:0.6rem;flex-shrink:0;background:color-mix(in srgb, var(--accent-violet) 15%, transparent);color:var(--accent-violet);border:1px solid color-mix(in srgb, var(--accent-violet) 25%, transparent)">EP</span>';
                h += '<div class="list-inner">';
                h += '<a class="topictitle" style="color:var(--link)">' + escapeHtml(ep.title || 'Untitled episode') + '</a> ' + stateLabel;
                if (ep.summary) h += '<br><span style="font-size:0.75rem;color:var(--text-dim)">' + escapeHtml(ep.summary).slice(0, 120) + '</span>';
                h += '</div></dt>';
                h += '<dd class="posts">' + mems + '</dd><dd class="posts">' + files + '</dd>';
                h += '<dd class="lastpost"><span style="font-size:0.75rem;color:var(--text-dim)">' + time + '</span></dd>';
                h += '</dl></li>';
                return h;
            },
        },
        memories: _buildMemoriesConfig('Recent Memories', 'memories', null),
        handoffs: _buildMemoriesConfig('Session Handoffs', 'handoffs', 'handoff'),
        patterns: {
            title: 'Discovered Patterns', unit: 'patterns', itemLabel: 'pattern',
            fetch: function() { return fetchJSON('/patterns?limit=50').then(function(d) { return d.patterns || []; }); },
            apiBase: '/api/v1/patterns',
            columns: [
                { label: 'Pattern', key: 'title', accessor: function(r) { return r.title || ''; } },
                { label: 'Strength', key: 'strength', accessor: function(r) { return r.strength || 0; } },
                { label: 'Evidence', key: 'evidence', accessor: function(r) { return (r.evidence_ids || []).length; } },
                { label: 'Discovered', key: 'created_at', accessor: function(r) { return r.created_at || ''; } },
            ],
            renderRow: function(p, bgClass) {
                var time = new Date(p.created_at).toLocaleString([], { month: 'short', day: 'numeric', hour: '2-digit', minute: '2-digit' });
                var h = '<li class="row ' + bgClass + '" style="cursor:pointer" onclick="this.querySelector(\'.expand-zone\').classList.toggle(\'open\')">';
                h += '<dl class="row-item"><dt>';
                h += '<span class="status-icon" style="width:26px;height:26px;font-size:0.6rem;flex-shrink:0;background:color-mix(in srgb, var(--accent-orange) 15%, transparent);color:var(--accent-orange);border:1px solid color-mix(in srgb, var(--accent-orange) 25%, transparent)">PT</span>';
                h += '<div class="list-inner">';
                h += '<a class="topictitle" style="color:var(--link)">' + escapeHtml(p.title || '') + '</a>';
                h += '<br><span style="font-size:0.75rem;color:var(--text-dim)">' + escapeHtml(p.description || '').slice(0, 120) + '</span>';
                h += '<div class="expand-zone" style="margin-top:6px;padding:8px;background:var(--bg-row-alt);border-radius:4px;font-size:0.82rem;color:var(--text-secondary)">';
                h += '<strong>Type:</strong> ' + escapeHtml(p.pattern_type || '') + '<br>';
                h += '<strong>Description:</strong> ' + escapeHtml(p.description || '') + '<br>';
                if (p.project) h += '<strong>Project:</strong> ' + escapeHtml(p.project) + '<br>';
                if (p.concepts && p.concepts.length) h += '<strong>Concepts:</strong> ' + p.concepts.map(function(c) { return escapeHtml(c); }).join(', ') + '<br>';
                h += '<strong>Evidence:</strong> ' + (p.evidence_ids || []).length + ' memories';
                h += '</div>';
                h += '</div></dt>';
                h += '<dd class="posts" style="color:var(--accent-orange)">' + (p.strength || 0).toFixed(2) + '</dd>';
                h += '<dd class="posts">' + (p.evidence_ids || []).length + '</dd>';
                h += '<dd class="lastpost"><span style="font-size:0.75rem;color:var(--text-dim)">' + time + '</span></dd>';
                h += '</dl></li>';
                return h;
            },
        },
        abstractions: {
            title: 'Abstractions & Principles', unit: 'abstractions', itemLabel: 'abstraction',
            fetch: function() { return fetchJSON('/abstractions?limit=50').then(function(d) { return d.abstractions || []; }); },
            apiBase: '/api/v1/abstractions',
            columns: [
                { label: 'Abstraction', key: 'title', accessor: function(r) { return r.title || ''; } },
                { label: 'Confidence', key: 'confidence', accessor: function(r) { return r.confidence || 0; } },
                { label: 'Level', key: 'level', accessor: function(r) { return r.level || 1; } },
                { label: 'Created', key: 'created_at', accessor: function(r) { return r.created_at || ''; } },
            ],
            renderRow: function(a, bgClass) {
                var time = new Date(a.created_at).toLocaleString([], { month: 'short', day: 'numeric', hour: '2-digit', minute: '2-digit' });
                var level = a.level === 3 ? 'Axiom' : a.level === 2 ? 'Principle' : 'Pattern';
                var levelColor = a.level === 3 ? 'var(--accent-yellow)' : 'var(--accent-green)';
                var h = '<li class="row ' + bgClass + '" style="cursor:pointer" onclick="this.querySelector(\'.expand-zone\').classList.toggle(\'open\')">';
                h += '<dl class="row-item"><dt>';
                h += '<span class="status-icon" style="width:26px;height:26px;font-size:0.6rem;flex-shrink:0;background:color-mix(in srgb, ' + levelColor + ' 15%, transparent);color:' + levelColor + ';border:1px solid color-mix(in srgb, ' + levelColor + ' 25%, transparent)">AB</span>';
                h += '<div class="list-inner">';
                h += '<a class="topictitle" style="color:var(--link)">' + escapeHtml(a.title || '') + '</a>';
                h += '<br><span style="font-size:0.75rem;color:var(--text-dim)">' + escapeHtml(a.description || '').slice(0, 120) + '</span>';
                h += '<div class="expand-zone" style="margin-top:6px;padding:8px;background:var(--bg-row-alt);border-radius:4px;font-size:0.82rem;color:var(--text-secondary)">';
                h += '<strong>Level:</strong> ' + level + '<br>';
                h += '<strong>Description:</strong> ' + escapeHtml(a.description || '') + '<br>';
                h += '<strong>Sources:</strong> ' + (a.source_pattern_ids || []).length + ' patterns, ' + (a.source_memory_ids || []).length + ' memories';
                h += '</div>';
                h += '</div></dt>';
                h += '<dd class="posts" style="color:' + levelColor + '">' + (a.confidence || 0).toFixed(2) + '</dd>';
                h += '<dd class="posts" style="font-size:0.72rem">' + level + '</dd>';
                h += '<dd class="lastpost"><span style="font-size:0.75rem;color:var(--text-dim)">' + time + '</span></dd>';
                h += '</dl></li>';
                return h;
            },
        },
    };
}

export async function loadMemorySection(sectionId, sectionName) {
    state.currentView = 'thread';
    document.querySelectorAll('.view').forEach(function(v) { v.classList.remove('active'); });
    document.querySelectorAll('.ntab').forEach(function(t) { t.classList.remove('active'); });
    var viewEl = document.getElementById('view-thread');
    if (viewEl) viewEl.classList.add('active');
    window.location.hash = 'memory-section/' + sectionId;
    var bc = document.getElementById('breadcrumbs');
    if (bc) bc.innerHTML = '<a href="#" onclick="window.switchView(\'explore\')">mnemonic</a><span class="sep"> › </span><a href="#" onclick="window.switchView(\'explore\')">Forum</a><span class="sep"> › </span>Memory System<span class="sep"> › </span>' + escapeHtml(sectionName);
    var compose = document.getElementById('threadCompose');
    if (compose) compose.style.display = 'none';
    var container = document.getElementById('threadContent');
    if (!container) return;
    container.innerHTML = '<div style="padding:16px;color:var(--text-dim)">Loading ' + escapeHtml(sectionId) + '...</div>';

    var cfgs = _memSectionConfigs();
    var cfg = cfgs[sectionId];
    if (!cfg) {
        container.innerHTML = '<div style="padding:16px;color:var(--accent-red)">Unknown section: ' + escapeHtml(sectionId) + '</div>';
        return;
    }
    try {
        var rows = await cfg.fetch();
        var hasCreatedAt = cfg.columns.find(function(c) { return c.key === 'created_at'; });
        var defaultSort = hasCreatedAt
            ? { col: 'created_at', dir: 'desc' }
            : (cfg.columns.find(function(c) { return c.key === 'strength' || c.key === 'salience' || c.key === 'confidence'; }) ? { col: cfg.columns[1].key, dir: 'desc' } : null);
        _memSectionState[sectionId] = { cfg: cfg, rows: rows, sort: defaultSort };
        _memRenderSection(sectionId);
        return;
    } catch (e) {
        console.error('Failed to load memory section:', e);
        container.innerHTML = '<div style="padding:16px;color:var(--accent-red)">Failed to load: ' + escapeHtml(e.message) + '</div>';
    }
}

export async function loadForumGroup(type) {
    // Show the sub-forums within a category group (General, Agents, Projects, Custom)
    var groupLabels = { system: 'General', project: 'Projects', agent: 'Agents', custom: 'Custom' };
    var groupName = groupLabels[type] || type;
    state.currentView = 'thread';
    document.querySelectorAll('.view').forEach(function(v) { v.classList.remove('active'); });
    document.querySelectorAll('.ntab').forEach(function(t) { t.classList.remove('active'); });
    var viewEl = document.getElementById('view-thread');
    if (viewEl) viewEl.classList.add('active');
    window.location.hash = 'forum-group/' + type;
    var bc = document.getElementById('breadcrumbs');
    if (bc) bc.innerHTML = '<a href="#" onclick="window.switchView(\'explore\')">mnemonic</a><span class="sep"> › </span><a href="#" onclick="window.switchView(\'explore\')">Forum</a><span class="sep"> › </span>' + escapeHtml(groupName);
    var compose = document.getElementById('threadCompose');
    if (compose) compose.style.display = 'none';

    try {
        var resp = await forumFetch('/api/v1/forum/categories');
        var data = await resp.json();
        var cats = (data.categories || []).filter(function(c) { return c.category.type === type; });
        var container = document.getElementById('threadContent');
        if (!container) return;

        var html = '<div class="forabg" style="margin:0">';
        html += '<div class="forabg-head"><span class="forabg-title">' + escapeHtml(groupName) + '</span>';
        html += '<span class="forabg-meta">' + cats.length + ' sub-forums</span></div>';
        html += '<div class="inner">';
        html += '<ul class="topiclist"><li class="header"><dl class="row-item">';
        html += '<dt><div class="list-inner">Sub-forum</div></dt>';
        html += '<dd class="posts">Threads</dd>';
        html += '<dd class="posts">Posts</dd>';
        html += '<dd class="lastpost">Last post</dd>';
        html += '</dl></li></ul>';
        html += '<ul class="topiclist forums">';

        for (var i = 0; i < cats.length; i++) {
            var c = cats[i];
            var cat = c.category;
            var bgClass = i % 2 === 0 ? 'bg1' : 'bg2';
            var lastInfo = '';
            if (c.last_post) {
                var lp = c.last_post;
                var lpAuthor = lp.author_type === 'agent' ? ('@' + (lp.author_key || 'agent')) : (lp.author_name || 'Human');
                var lpTime = new Date(lp.created_at).toLocaleString([], { month: 'short', day: 'numeric', hour: '2-digit', minute: '2-digit' });
                lastInfo = '<span style="font-size:0.75rem;color:var(--text-dim)">' + escapeHtml(lpAuthor) + '<br>' + lpTime + '</span>';
            }
            html += '<li class="row ' + bgClass + '" onclick="loadForumCategory(\'' + cat.id + '\', \'' + escapeHtml(cat.name).replace(/'/g, "\\'") + '\')" style="cursor:pointer">';
            html += '<dl class="row-item"><dt>';
            html += '<span class="status-icon" style="width:28px;height:28px;font-size:0.6rem;flex-shrink:0;background:color-mix(in srgb, ' + (cat.color || 'var(--text-dim)') + ' 15%, transparent);color:' + (cat.color || 'var(--text-dim)') + ';border:1px solid color-mix(in srgb, ' + (cat.color || 'var(--text-dim)') + ' 25%, transparent)">' + (cat.icon || '??') + '</span>';
            html += '<div class="list-inner">';
            html += '<a class="topictitle" style="color:var(--link)">' + escapeHtml(cat.name) + '</a>';
            html += '<br><span style="font-size:0.75rem;color:var(--text-dim)">' + escapeHtml(cat.description) + '</span>';
            html += '</div></dt>';
            html += '<dd class="posts">' + c.thread_count + '</dd>';
            html += '<dd class="posts">' + c.post_count + '</dd>';
            html += '<dd class="lastpost">' + lastInfo + '</dd>';
            html += '</dl></li>';
        }
        html += '</ul></div></div>';
        container.innerHTML = html;
    } catch (e) {
        console.error('Failed to load forum group:', e);
        var c = document.getElementById('threadContent');
        if (c) c.innerHTML = '<div style="padding:16px;color:var(--text-dim)">Failed to load: ' + escapeHtml(e.message) + '</div>';
    }
}

export async function loadForumCategory(categoryId, categoryName) {
    state.currentView = 'thread'; // reuse thread view for category listing
    document.querySelectorAll('.view').forEach(function(v) { v.classList.remove('active'); });
    document.querySelectorAll('.ntab').forEach(function(t) { t.classList.remove('active'); });
    var viewEl = document.getElementById('view-thread');
    if (viewEl) viewEl.classList.add('active');
    window.location.hash = 'forum-category/' + categoryId;
    var bc = document.getElementById('breadcrumbs');
    if (bc) bc.innerHTML = '<a href="#" onclick="window.switchView(\'explore\')">mnemonic</a><span class="sep"> › </span><a href="#" onclick="window.switchView(\'explore\')">Forum</a><span class="sep"> › </span>' + escapeHtml(categoryName);
    // Hide compose box (it's for threads, not category view)
    var compose = document.getElementById('threadCompose');
    if (compose) compose.style.display = 'none';
    try {
        var resp = await forumFetch('/api/v1/forum/threads?category=' + categoryId + '&limit=50');
        var data = await resp.json();
        var threads = data.threads || [];
        var container = document.getElementById('threadContent');
        if (!container) return;
        if (threads.length === 0) {
            container.innerHTML = '<div class="thread-wrap"><div class="thread-top"><h2 class="thread-title-big">' + escapeHtml(categoryName) + '</h2><div class="thread-meta">No threads yet</div></div></div>';
            return;
        }
        var html = '<div class="thread-wrap">';
        html += '<div class="thread-top"><h2 class="thread-title-big">' + escapeHtml(categoryName) + '</h2>';
        html += '<div class="thread-meta">' + threads.length + ' threads</div></div>';
        html += '<ul class="topiclist forums" style="margin:8px 0">';
        for (var i = 0; i < threads.length; i++) {
            var t = threads[i];
            var rp = t.root_post;
            if (!rp) continue;
            var bgClass = i % 2 === 0 ? 'bg1' : 'bg2';
            var agentKey = rp.author_key || '';
            var prof = _forumAgentProfiles[agentKey] || { tag: rp.author_name || 'Human', icon: 'HU', color: 'var(--accent-cyan)' };
            var preview = escapeHtml((rp.content || '').slice(0, 120));
            var lastActive = t.last_reply ? new Date(t.last_reply).toLocaleString([], { month: 'short', day: 'numeric', hour: '2-digit', minute: '2-digit' }) : '';
            html += '<li class="row ' + bgClass + '" onclick="loadForumThread(\'' + rp.id + '\')" style="cursor:pointer">';
            html += '<dl class="row-item"><dt>';
            html += '<span class="status-icon" style="width:26px;height:26px;font-size:0.6rem;flex-shrink:0;background:color-mix(in srgb, ' + prof.color + ' 15%, transparent);color:' + prof.color + ';border:1px solid color-mix(in srgb, ' + prof.color + ' 25%, transparent)">' + prof.icon + '</span>';
            html += '<div class="list-inner"><a class="topictitle" style="color:var(--link)">' + preview + '</a>';
            html += '<br><span style="font-size:0.75rem;color:var(--text-dim)">by <strong style="color:' + prof.color + '">' + escapeHtml(rp.author_type === 'agent' ? prof.tag : (rp.author_name || 'Human')) + '</strong></span>';
            html += '</div></dt>';
            html += '<dd class="posts">' + (t.reply_count || 0) + ' <dfn>replies</dfn></dd>';
            html += '<dd class="lastpost"><span style="font-size:0.75rem;color:var(--text-dim)">' + lastActive + '</span></dd>';
            html += '</dl></li>';
        }
        html += '</ul></div>';
        container.innerHTML = html;
    } catch (e) {
        console.error('Failed to load forum category:', e);
        var c = document.getElementById('threadContent');
        if (c) c.innerHTML = '<div style="padding:16px;color:var(--text-dim)">Failed to load category: ' + escapeHtml(e.message) + '</div>';
    }
}

export async function loadForumThread(threadId) {
    state.currentForumThread = threadId;
    state.currentEpisodeId = ''; // clear — this is a forum thread, not an episode
    state.currentView = 'thread';
    subscribeToThread(threadId);
    markThreadRead(threadId);
    // Show thread view
    document.querySelectorAll('.view').forEach(function(v) { v.classList.remove('active'); });
    document.querySelectorAll('.ntab').forEach(function(t) { t.classList.remove('active'); });
    var viewEl = document.getElementById('view-thread');
    if (viewEl) viewEl.classList.add('active');
    window.location.hash = 'forum-thread/' + threadId;
    var bc = document.getElementById('breadcrumbs');
    if (bc) bc.innerHTML = '<a href="#" onclick="window.switchView(\'explore\')">mnemonic</a><span class="sep"> › </span><a href="#" onclick="window.switchView(\'explore\')">Forum</a><span class="sep"> › </span>Thread';
    // Show compose box and init autocomplete
    var compose = document.getElementById('threadCompose');
    if (compose) { compose.style.display = ''; ensureMentionAutocomplete('threadReplyContent'); }
    try {
        var resp = await forumFetch('/api/v1/forum/threads/' + threadId);
        var data = await resp.json();
        var posts = data.posts || [];
        var container = document.getElementById('threadContent');
        if (!container) return;
        if (posts.length === 0) {
            container.innerHTML = '<div style="padding:16px;color:var(--text-dim)">Empty thread.</div>';
            return;
        }
        var html = '<div class="thread-wrap">';
        html += '<div class="thread-top"><h2 class="thread-title-big">' + escapeHtml((posts[0].content || '').slice(0, 80)) + '</h2>';
        html += '<div class="thread-meta">' + posts.length + ' posts · started ' + new Date(posts[0].created_at).toLocaleString() + '</div>';
        html += '</div>';
        for (var i = 0; i < posts.length; i++) {
            html += renderForumPost(posts[i], i);
        }
        html += '</div>';
        container.innerHTML = html;
        // Delegated click handler for data-action buttons
        container.onclick = function(e) {
            var btn = e.target.closest('[data-action]');
            if (!btn) return;
            var action = btn.getAttribute('data-action');
            if (action === 'quote') quotePostById(btn.getAttribute('data-target'));
            else if (action === 'internalize') internalizePost(btn.getAttribute('data-post-id'), btn);
            else if (action === 'insert-tag') insertTagInReply(btn.getAttribute('data-agent-key'));
            else if (action === 'edit-post') editForumPost(btn.getAttribute('data-post-id'));
            else if (action === 'delete-post') deleteForumPost(btn.getAttribute('data-post-id'));
        };
    } catch (e) {
        console.error('Failed to load forum thread:', e);
        var c = document.getElementById('threadContent');
        if (c) c.innerHTML = '<div style="padding:16px;color:var(--text-dim)">Failed to load thread: ' + escapeHtml(e.message) + '</div>';
    }
}

export function formatForumContent(text) {
    if (!text) return '';
    var lines = text.split('\n');
    var html = '';
    var inQuote = false;
    var quoteLines = [];

    function flushQuote() {
        if (quoteLines.length === 0) return;
        var quoteHtml = quoteLines.map(function(l) {
            var line = escapeHtml(l);
            return line.replace(/@(retrieval|metacognition|encoding|episoding|consolidation|dreaming|abstraction|perception)/g, '<strong style="color:var(--accent-cyan)">@$1</strong>');
        }).join('<br>');
        html += '<blockquote style="margin:6px 0;padding:8px 12px;border-left:3px solid var(--border-accent);background:color-mix(in srgb, var(--bg-head) 50%, transparent);border-radius:0 4px 4px 0;font-size:0.84rem;color:var(--text-dim)">' + quoteHtml + '</blockquote>';
        quoteLines = [];
    }

    for (var i = 0; i < lines.length; i++) {
        var line = lines[i];
        if (line.startsWith('> ')) {
            inQuote = true;
            quoteLines.push(line.substring(2));
        } else {
            if (inQuote) { flushQuote(); inQuote = false; }
            if (line.trim() === '') {
                if (html && !html.endsWith('<br>')) html += '<br>';
            } else {
                var escaped = escapeHtml(line);
                escaped = escaped.replace(/@(retrieval|metacognition|encoding|episoding|consolidation|dreaming|abstraction|perception)/g, '<strong style="color:var(--accent-cyan)">@$1</strong>');
                html += escaped + '<br>';
            }
        }
    }
    if (inQuote) flushQuote();
    // Trim trailing <br>
    html = html.replace(/(<br>)+$/, '');
    return html;
}

var _forumAgentProfiles = {
    consolidation: { tag: '@consolidation', title: 'Memory Maintainer', icon: 'CA', color: 'var(--accent-yellow)' },
    dreaming: { tag: '@dreaming', title: 'Memory Replay', icon: 'DA', color: 'var(--accent-violet)' },
    episoding: { tag: '@episoding', title: 'Episode Clustering', icon: 'EP', color: 'var(--accent-violet)' },
    abstraction: { tag: '@abstraction', title: 'Pattern Discovery', icon: 'AA', color: 'var(--accent-orange)' },
    metacognition: { tag: '@metacognition', title: 'Self-Reflection', icon: 'MA', color: 'var(--accent-blue)' },
    encoding: { tag: '@encoding', title: 'Memory Encoder', icon: 'EA', color: 'var(--accent-blue)' },
    perception: { tag: '@perception', title: 'Filesystem Watcher', icon: 'PA', color: 'var(--accent-green)' },
    retrieval: { tag: '@retrieval', title: 'Spread Activation', icon: 'RA', color: 'var(--accent-cyan)' },
};

export function renderForumPost(post, index) {
    var bgClass = index % 2 === 0 ? 'bg1' : 'bg2';
    var isAgent = post.author_type === 'agent';
    var agentKey = post.author_key || '';
    var prof = _forumAgentProfiles[agentKey] || { tag: post.author_name || 'Human', title: isAgent ? 'Agent' : 'User', icon: isAgent ? agentKey.slice(0,2).toUpperCase() : 'HU', color: isAgent ? 'var(--text-dim)' : 'var(--accent-cyan)' };
    var displayName = isAgent ? prof.tag : (post.author_name || 'Human');
    var time = new Date(post.created_at).toLocaleString([], { month: 'short', day: 'numeric', hour: '2-digit', minute: '2-digit' });
    // Parse content: render > lines as blockquotes, highlight @mentions
    var contentHtml = formatForumContent(post.content || '');
    // Store post data for quote functionality via data attributes
    var postDataId = 'forum-post-' + post.id;
    var html = '<div class="post ' + bgClass + '" id="' + postDataId + '" data-author="' + escapeHtml(displayName) + '" data-content="' + escapeHtml(post.content || '') + '">';
    html += '<div class="inner"><dl class="postprofile">';
    html += '<dt><div class="avatar-container" style="background:color-mix(in srgb, ' + prof.color + ' 15%, transparent);color:' + prof.color + ';border:1px solid color-mix(in srgb, ' + prof.color + ' 25%, transparent)">' + prof.icon + '</div>';
    // Clickable @tag — clicking it inserts @tag into reply box
    if (isAgent && agentKey) {
        html += '<strong style="color:' + prof.color + ';cursor:pointer" data-action="insert-tag" data-agent-key="' + escapeHtml(agentKey) + '">' + escapeHtml(displayName) + '</strong></dt>';
    } else {
        html += '<strong style="color:' + prof.color + '">' + escapeHtml(displayName) + '</strong></dt>';
    }
    html += '<dd class="profile-rank"><span class="rank-' + (isAgent ? 'insight' : 'general') + '">' + escapeHtml(prof.title) + '</span></dd>';
    if (post.event_ref) html += '<dd style="font-size:0.7rem;color:var(--text-dim)">via ' + escapeHtml(post.event_ref) + '</dd>';
    html += '</dl><div class="postbody">';
    html += '<p class="post-meta"><span>Posted by <strong style="color:' + prof.color + '">' + escapeHtml(displayName) + '</strong> · ' + time + '</span><span style="color:var(--text-dim)">#' + (index + 1) + '</span></p>';
    html += '<div class="content">' + contentHtml + '</div>';
    // Action buttons
    html += '<div style="margin-top:6px;display:flex;gap:6px;align-items:center">';
    html += '<button data-action="quote" data-target="' + postDataId + '" style="font-size:0.7rem;padding:1px 8px;background:transparent;border:1px solid var(--border-subtle);color:var(--text-dim);border-radius:3px;cursor:pointer">quote</button>';
    if (post.state === 'internalized') {
        html += '<span style="font-size:0.7rem;padding:1px 6px;background:color-mix(in srgb, var(--accent-green) 15%, transparent);color:var(--accent-green);border-radius:3px">internalized</span>';
    } else {
        html += '<button data-action="internalize" data-post-id="' + escapeHtml(post.id) + '" style="font-size:0.7rem;padding:1px 8px;background:transparent;border:1px solid var(--border-subtle);color:var(--text-dim);border-radius:3px;cursor:pointer">internalize</button>';
    }
    html += '<button data-action="edit-post" data-post-id="' + escapeHtml(post.id) + '" style="font-size:0.7rem;padding:1px 8px;background:transparent;border:1px solid var(--border-subtle);color:var(--text-dim);border-radius:3px;cursor:pointer">edit</button>';
    html += '<button data-action="delete-post" data-post-id="' + escapeHtml(post.id) + '" style="font-size:0.7rem;padding:1px 8px;background:transparent;border:1px solid var(--accent-red);color:var(--accent-red);border-radius:3px;cursor:pointer">delete</button>';
    html += '</div>';
    html += '</div></div></div>';
    return html;
}

async function editForumPost(postId) {
    var postEl = document.getElementById('forum-post-' + postId);
    if (!postEl) return;
    var current = postEl.getAttribute('data-content') || '';
    var next = prompt('Edit post content:', current);
    if (next === null || next === current) return;
    try {
        var resp = await fetch('/api/v1/forum/posts/' + encodeURIComponent(postId), {
            method: 'PATCH',
            headers: { 'Content-Type': 'application/json' },
            body: JSON.stringify({ content: next }),
        });
        if (!resp.ok) {
            var msg = 'HTTP ' + resp.status;
            try { var d = await resp.json(); if (d.error) msg = d.error; } catch(e) { /* ignore parse */ }
            showToast('Edit failed: ' + msg, 'error');
            return;
        }
        postEl.setAttribute('data-content', next);
        var body = postEl.querySelector('.content');
        if (body) body.innerHTML = formatForumContent(next);
        showToast('Post updated', 'success');
    } catch (e) {
        console.error('edit post failed', e);
        showToast('Edit failed: ' + e.message, 'error');
    }
}

async function deleteForumPost(postId) {
    if (!confirm('Permanently delete this post? This cannot be undone. Posts with replies cannot be deleted.')) return;
    try {
        var resp = await fetch('/api/v1/forum/posts/' + encodeURIComponent(postId), { method: 'DELETE' });
        if (!resp.ok) {
            var msg = 'HTTP ' + resp.status;
            try { var d = await resp.json(); if (d.error) msg = d.error; } catch(e) { /* ignore parse */ }
            showToast('Delete failed: ' + msg, 'error');
            return;
        }
        var postEl = document.getElementById('forum-post-' + postId);
        if (postEl) postEl.remove();
        showToast('Post deleted', 'success');
    } catch (e) {
        console.error('delete post failed', e);
        showToast('Delete failed: ' + e.message, 'error');
    }
}

export function appendForumPostToThread(payload) {
    var container = document.getElementById('threadContent');
    if (!container) return;
    var wrap = container.querySelector('.thread-wrap');
    if (!wrap) return;
    var postCount = wrap.querySelectorAll('.post').length;
    var postObj = {
        id: payload.post_id,
        content: payload.content,
        author_type: payload.author_type,
        author_name: payload.author_name,
        author_key: payload.author_key || '',
        created_at: payload.timestamp || new Date().toISOString(),
        state: 'active',
    };
    wrap.insertAdjacentHTML('beforeend', renderForumPost(postObj, postCount));
    // Scroll to new post
    var newPost = document.getElementById('forum-post-' + payload.post_id);
    if (newPost) newPost.scrollIntoView({ behavior: 'smooth', block: 'end' });
}

export function showNewThreadForm() {
    var form = document.getElementById('newThreadForm');
    if (form) { form.style.display = ''; document.getElementById('newThreadContent').focus(); ensureMentionAutocomplete('newThreadContent'); }
}

export async function submitNewThread() {
    var textarea = document.getElementById('newThreadContent');
    var content = (textarea.value || '').trim();
    if (!content) return;
    var catSelect = document.getElementById('newThreadCategory');
    var categoryId = catSelect ? catSelect.value : 'discussions';
    textarea.disabled = true;
    try {
        var resp = await forumFetch('/api/v1/forum/posts', {
            method: 'POST',
            headers: { 'Content-Type': 'application/json' },
            body: JSON.stringify({ content: content, category_id: categoryId })
        });
        var data = await resp.json();
        textarea.value = '';
        textarea.disabled = false;
        document.getElementById('newThreadForm').style.display = 'none';
        showToast('Thread created');
        subscribeToThread(data.thread_id);
        state.forumLoaded = false;
        loadForumIndex();
        // Navigate to the new thread
        loadForumThread(data.thread_id);
    } catch (e) {
        textarea.disabled = false;
        showToast('Failed to create thread: ' + e.message, 'error');
    }
}

export async function submitThreadReply() {
    var textarea = document.getElementById('threadReplyContent');
    var content = (textarea.value || '').trim();
    if (!content || !state.currentForumThread) return;
    textarea.disabled = true;
    try {
        await forumFetch('/api/v1/forum/posts', {
            method: 'POST',
            headers: { 'Content-Type': 'application/json' },
            body: JSON.stringify({ content: content, thread_id: state.currentForumThread, episode_id: state.currentEpisodeId || '' })
        });
        textarea.value = '';
        textarea.disabled = false;
        showToast('Reply posted');
        subscribeToThread(state.currentForumThread);
        // The WebSocket event will handle live-inserting the post
    } catch (e) {
        textarea.disabled = false;
        showToast('Failed to post reply: ' + e.message, 'error');
    }
}

export function quotePostById(postElementId) {
    var postEl = document.getElementById(postElementId);
    if (!postEl) return;
    var authorName = postEl.getAttribute('data-author') || 'Unknown';
    var content = postEl.getAttribute('data-content') || '';
    var textarea = document.getElementById('threadReplyContent');
    if (!textarea) return;
    var quotedLines = content.split('\n').map(function(line) { return '> ' + line; }).join('\n');
    var quote = '> ' + authorName + ' wrote:\n' + quotedLines + '\n\n';
    textarea.value = quote;
    textarea.focus();
    textarea.scrollIntoView({ behavior: 'smooth', block: 'center' });
}

export function insertTagInReply(agentKey) {
    var textarea = document.getElementById('threadReplyContent');
    if (!textarea) return;
    var tag = '@' + agentKey + ' ';
    textarea.value = tag + textarea.value;
    textarea.selectionStart = textarea.selectionEnd = tag.length;
    textarea.focus();
    textarea.scrollIntoView({ behavior: 'smooth', block: 'center' });
}

// ── @mention autocomplete ──
var _mentionAgents = [
    { key: 'retrieval', label: '@retrieval', desc: 'Search memories' },
    { key: 'metacognition', label: '@metacognition', desc: 'System health' },
    { key: 'encoding', label: '@encoding', desc: 'Memory encoder' },
    { key: 'episoding', label: '@episoding', desc: 'Episodes & timeline' },
    { key: 'consolidation', label: '@consolidation', desc: 'Memory maintenance' },
    { key: 'dreaming', label: '@dreaming', desc: 'Dream cycle insights' },
    { key: 'abstraction', label: '@abstraction', desc: 'Patterns & principles' },
    { key: 'perception', label: '@perception', desc: 'File watcher' },
];

export function setupMentionAutocomplete(textarea) {
    var dropdown = document.createElement('div');
    dropdown.className = 'mention-dropdown';
    dropdown.style.cssText = 'display:none;position:absolute;z-index:100;background:var(--bg-primary);border:1px solid var(--border-accent);border-radius:4px;box-shadow:0 4px 16px rgba(0,0,0,0.5);max-height:240px;overflow-y:auto;font-size:0.82rem;width:260px';
    textarea.parentNode.style.position = 'relative';
    textarea.parentNode.insertBefore(dropdown, textarea);

    textarea.addEventListener('input', function() {
        var val = textarea.value;
        var cursor = textarea.selectionStart;
        // Find @ before cursor
        var before = val.substring(0, cursor);
        var atMatch = before.match(/@(\w*)$/);
        if (!atMatch) { dropdown.style.display = 'none'; return; }
        var filter = atMatch[1].toLowerCase();
        var filtered = _mentionAgents.filter(function(a) { return a.key.startsWith(filter); });
        if (filtered.length === 0) { dropdown.style.display = 'none'; return; }
        var html = '';
        for (var i = 0; i < filtered.length; i++) {
            var a = filtered[i];
            var prof = _forumAgentProfiles[a.key] || {};
            html += '<div class="mention-option" data-key="' + a.key + '" style="padding:6px 10px;cursor:pointer;display:flex;align-items:center;gap:8px;border-bottom:1px solid var(--border-subtle)"';
            html += ' onmousedown="insertMention(\'' + textarea.id + '\', \'' + a.key + '\')"';
            html += ' onmouseenter="this.style.background=\'var(--bg-row-hover)\'" onmouseleave="this.style.background=\'none\'">';
            html += '<span style="width:20px;height:20px;font-size:0.55rem;display:flex;align-items:center;justify-content:center;border-radius:3px;background:color-mix(in srgb, ' + (prof.color || 'var(--text-dim)') + ' 15%, transparent);color:' + (prof.color || 'var(--text-dim)') + '">' + (prof.icon || '') + '</span>';
            html += '<span><strong style="color:' + (prof.color || 'var(--link)') + '">' + a.label + '</strong> <span style="color:var(--text-dim)">' + a.desc + '</span></span>';
            html += '</div>';
        }
        dropdown.innerHTML = html;
        dropdown.style.display = 'block';
    });

    textarea.addEventListener('blur', function() {
        setTimeout(function() { dropdown.style.display = 'none'; }, 150);
    });

    textarea.addEventListener('keydown', function(e) {
        if (dropdown.style.display === 'none') return;
        if (e.key === 'Escape') { dropdown.style.display = 'none'; e.preventDefault(); }
        if (e.key === 'Tab' || e.key === 'Enter') {
            var first = dropdown.querySelector('.mention-option');
            if (first) { insertMention(textarea.id, first.getAttribute('data-key')); e.preventDefault(); }
        }
    });
}

export function insertMention(textareaId, agentKey) {
    var textarea = document.getElementById(textareaId);
    if (!textarea) return;
    var val = textarea.value;
    var cursor = textarea.selectionStart;
    var before = val.substring(0, cursor);
    var after = val.substring(cursor);
    // Replace the @partial with @agentKey
    var newBefore = before.replace(/@\w*$/, '@' + agentKey + ' ');
    textarea.value = newBefore + after;
    textarea.selectionStart = textarea.selectionEnd = newBefore.length;
    textarea.focus();
    // Hide dropdown
    var dropdown = textarea.parentNode.querySelector('.mention-dropdown');
    if (dropdown) dropdown.style.display = 'none';
}

// Initialize autocomplete on compose textareas when they become visible
var _mentionInitialized = {};
export function ensureMentionAutocomplete(textareaId) {
    if (_mentionInitialized[textareaId]) return;
    var el = document.getElementById(textareaId);
    if (el) { setupMentionAutocomplete(el); _mentionInitialized[textareaId] = true; }
}

// ── Thread subscriptions ──
var _subscriptions = JSON.parse(localStorage.getItem('mnemonic_forum_subs') || '{}');
// { threadId: { unread: 0, lastSeen: timestamp } }

export function subscribeToThread(threadId) {
    if (!_subscriptions[threadId]) {
        _subscriptions[threadId] = { unread: 0, lastSeen: Date.now() };
        localStorage.setItem('mnemonic_forum_subs', JSON.stringify(_subscriptions));
    }
}

export function markThreadRead(threadId) {
    if (_subscriptions[threadId]) {
        _subscriptions[threadId].unread = 0;
        _subscriptions[threadId].lastSeen = Date.now();
        localStorage.setItem('mnemonic_forum_subs', JSON.stringify(_subscriptions));
        updateNotificationBadge();
    }
}

export function onForumPostWebSocket(payload) {
    var threadId = payload.thread_id;
    // If we're subscribed and not currently viewing this thread, increment unread
    if (_subscriptions[threadId] && state.currentForumThread !== threadId) {
        _subscriptions[threadId].unread = (_subscriptions[threadId].unread || 0) + 1;
        localStorage.setItem('mnemonic_forum_subs', JSON.stringify(_subscriptions));
        updateNotificationBadge();
        // Show toast for agent replies
        if (payload.author_type === 'agent') {
            var agentName = payload.author_key ? ('@' + payload.author_key) : payload.author_name;
            showToast(agentName + ' replied to your thread');
        }
    }
}

export function updateNotificationBadge() {
    var total = 0;
    for (var tid in _subscriptions) {
        total += (_subscriptions[tid].unread || 0);
    }
    var badge = document.getElementById('forumNotifBadge');
    if (badge) {
        badge.textContent = total > 0 ? total : '';
        badge.style.display = total > 0 ? 'inline-flex' : 'none';
    }
}

export async function internalizePost(postId, btn) {
    btn.disabled = true;
    btn.textContent = 'internalizing...';
    try {
        var resp = await fetch('/api/v1/forum/posts/' + postId + '/internalize', {
            method: 'POST',
            headers: { 'Content-Type': 'application/json' },
            body: JSON.stringify({})
        });
        if (resp.ok) {
            btn.textContent = 'internalized';
            btn.style.color = 'var(--accent-green)';
            btn.style.borderColor = 'var(--accent-green)';
            showToast('Post internalized into memory');
        } else {
            var data = await resp.json();
            btn.textContent = data.error === 'post already internalized' ? 'already internalized' : 'failed';
            btn.disabled = false;
        }
    } catch (e) {
        btn.textContent = 'failed';
        btn.disabled = false;
        showToast('Failed to internalize', 'error');
    }
}

export function toggleToolDetail(e) {
    var pill = e.currentTarget;
    var toolId = pill.getAttribute('data-tool-use-id');
    var entry = toolId ? toolDataMap.get(toolId) : null;
    var input = entry ? entry.input : null;
    var result = entry ? entry.result : null;
    if (!input && !result) return;
    // The detail panel lives after the tool-row, not after the pill
    var toolRow = pill.closest('.chat-tool-row') || pill.parentNode;
    var existing = toolRow.nextElementSibling;
    if (existing && existing.classList.contains('chat-tool-detail') && existing.getAttribute('data-for') === toolId) {
        existing.classList.toggle('visible');
        pill.classList.toggle('expanded');
    } else {
        // Collapse any other open detail in this row
        if (existing && existing.classList.contains('chat-tool-detail')) {
            existing.remove();
            toolRow.querySelectorAll('.chat-tool-pill.expanded').forEach(function(p) { p.classList.remove('expanded'); });
        }
        var detail = document.createElement('div');
        detail.className = 'chat-tool-detail visible';
        if (toolId) {
            detail.setAttribute('data-for', toolId);
        }
        var html = '';
        if (input) {
            html += '<div class="tool-detail-section"><div class="tool-detail-label">Input</div><pre>' + escapeHtml(input) + '</pre></div>';
        }
        if (result) {
            html += '<div class="tool-detail-section"><div class="tool-detail-label">Result</div><pre>' + escapeHtml(result) + '</pre></div>';
        }
        detail.innerHTML = html;
        toolRow.parentNode.insertBefore(detail, toolRow.nextSibling);
        pill.classList.add('expanded');
        detail.scrollIntoView({ behavior: 'smooth', block: 'nearest' });
    }
    scrollChatBottom();
}
export function relativeTime(input) {
    if (!input) return '';
    var date = input instanceof Date ? input : new Date(input);
    var diff = Date.now() - date.getTime();
    if (diff < 60000) return 'just now';
    if (diff < 3600000) return Math.floor(diff / 60000) + 'm ago';
    if (diff < 86400000) return Math.floor(diff / 3600000) + 'h ago';
    if (diff < 604800000) return Math.floor(diff / 86400000) + 'd ago';
    return date.toLocaleDateString();
}

