import { state, CONFIG } from './state.js';
import { apiFetch, fetchJSON, escapeHtml, showToast } from './utils.js';
import { relativeTime } from './forum.js';

// ── Activity Drawer ──
var _conceptsInterval = null;
export function toggleDrawer() {
    state.drawerOpen = !state.drawerOpen;
    document.getElementById('activityDrawer').classList.toggle('open', state.drawerOpen);
    document.getElementById('activityBackdrop').classList.toggle('open', state.drawerOpen);
    if (state.drawerOpen) {
        state.unreadEvents = 0; updateBadge();
        loadActivityConcepts();
        _conceptsInterval = setInterval(loadActivityConcepts, 10000);
    } else if (_conceptsInterval) {
        clearInterval(_conceptsInterval);
        _conceptsInterval = null;
    }
}

export async function loadActivityConcepts() {
    try {
        var resp = await fetch(CONFIG.API_BASE + '/activity');
        if (!resp.ok) return;
        var data = await resp.json();
        var windowMs = (data.window_minutes || 30) * 60 * 1000;
        var now = Date.now();
        var concepts = [];
        for (var name in (data.concepts || {})) {
            var ts = new Date(data.concepts[name]).getTime();
            var elapsed = now - ts;
            var remaining = windowMs - elapsed;
            if (remaining > 0) {
                concepts.push({ name: name, remaining: remaining, pct: (remaining / windowMs) * 100 });
            }
        }
        concepts.sort(function(a, b) { return b.remaining - a.remaining; });
        var container = document.getElementById('conceptsContainer');
        if (concepts.length === 0) {
            container.innerHTML = '<div style="font-size:0.75rem;color:var(--text-muted);padding:4px 8px">No active concepts (watcher idle)</div>';
            return;
        }
        container.innerHTML = concepts.slice(0, 15).map(function(c) {
            var mins = Math.ceil(c.remaining / 60000);
            var color = c.pct > 50 ? 'var(--accent-green)' : c.pct > 20 ? 'var(--accent-yellow, #f0c040)' : 'var(--accent-pink)';
            return '<div class="concept-row">' +
                '<span class="concept-name">' + escapeHtml(c.name) + '</span>' +
                '<div class="concept-bar-bg"><div class="concept-bar-fill" style="width:' + Math.round(c.pct) + '%;background:' + color + '"></div></div>' +
                '<span class="concept-time">' + mins + 'm</span></div>';
        }).join('');
    } catch(e) { /* ignore */ }
}

export function updateBadge() {
    var badge = document.getElementById('activityBadge');
    if (state.unreadEvents > 0) { badge.textContent = state.unreadEvents > 99 ? '99+' : state.unreadEvents; badge.classList.add('visible'); }
    else { badge.classList.remove('visible'); }
}

var _eventDotColors = { raw_memory_created: 'green', memory_encoded: 'blue', memory_accessed: 'violet', query_executed: 'cyan', consolidation_started: 'orange', consolidation_completed: 'orange', dream_cycle_completed: 'violet', meta_cycle_completed: 'violet', watcher_event: 'yellow', system_health: 'cyan', episode_closed: 'green', pattern_discovered: 'cyan', abstraction_created: 'violet', memory_amended: 'blue', session_ended: 'green' };
var _eventNavTargets = { pattern_discovered: ['explore', 'patterns'], abstraction_created: ['explore', 'abstractions'], episode_closed: ['explore', 'episodes'], memory_encoded: ['explore', 'memories'] };

export function _nonZero(items) {
    return items.filter(function(i) { return i[0] > 0; }).map(function(i) { return i[0] + ' ' + i[1]; }).join(' \u00b7 ');
}

export function buildEventDetail(type, payload) {
    var p = payload || {};
    switch (type) {
        case 'consolidation_completed': {
            var line1 = _nonZero([[p.memories_processed, 'processed'], [p.memories_decayed, 'decayed'], [p.merged_clusters, 'merged']]);
            var line2 = _nonZero([[p.patterns_extracted, 'patterns found'], [p.never_recalled_archived, 'noise archived'], [p.transitioned_fading, 'fading'], [p.transitioned_archived, 'archived']]);
            var dur = p.duration_ms ? (p.duration_ms / 1000).toFixed(1) + 's' : '';
            var html = '';
            if (line1) html += '<div class="event-metrics">' + line1 + '</div>';
            if (line2) html += '<div class="event-metrics">' + line2 + '</div>';
            if (dur) html += '<div class="event-duration">' + dur + '</div>';
            return html;
        }
        case 'dream_cycle_completed': {
            var l1 = _nonZero([[p.memories_replayed, 'replayed'], [p.associations_strengthened, 'strengthened'], [p.new_associations_created, 'new links']]);
            var l2 = _nonZero([[p.insights_generated, 'insights'], [p.cross_project_links, 'cross-project'], [p.noisy_memories_demoted, 'demoted']]);
            var d = p.duration_ms ? (p.duration_ms / 1000).toFixed(1) + 's' : '';
            var h = '';
            if (l1) h += '<div class="event-metrics">' + l1 + '</div>';
            if (l2) h += '<div class="event-metrics">' + l2 + '</div>';
            if (d) h += '<div class="event-duration">' + d + '</div>';
            return h;
        }
        case 'memory_encoded': {
            var concepts = (p.concepts || []).length;
            var assocs = p.associations_created || 0;
            var parts = _nonZero([[concepts, 'concepts'], [assocs, 'associations']]);
            return parts ? '<div class="event-metrics">' + parts + '</div>' : '';
        }
        case 'pattern_discovered': {
            var html = '';
            if (p.title) html += '<div class="event-subtitle">' + escapeHtml(p.title.slice(0, 60)) + '</div>';
            var meta = _nonZero([[1, p.pattern_type || 'pattern'], [p.evidence_count, 'evidence']]);
            if (meta) html += '<div class="event-metrics">' + meta + '</div>';
            return html;
        }
        case 'abstraction_created': {
            var html = '';
            if (p.title) html += '<div class="event-subtitle">' + escapeHtml(p.title.slice(0, 60)) + '</div>';
            if (p.source_count) html += '<div class="event-metrics">from ' + p.source_count + ' sources</div>';
            return html;
        }
        case 'episode_closed': {
            var html = '';
            if (p.title) html += '<div class="event-subtitle">' + escapeHtml(p.title.slice(0, 60)) + '</div>';
            var meta = _nonZero([[p.event_count, 'events']]);
            if (p.duration_sec) meta += (meta ? ' \u00b7 ' : '') + Math.round(p.duration_sec / 60) + 'min';
            if (meta) html += '<div class="event-metrics">' + meta + '</div>';
            return html;
        }
        case 'query_executed': {
            return '<div class="event-metrics">' + (p.results_returned || 0) + ' results \u00b7 ' + (p.took_ms || 0) + 'ms</div>';
        }
        default: return '';
    }
}

export function addEvent(type, description, timestamp, payload) {
    var container = document.getElementById('eventsContainer');
    var el = document.createElement('div');
    var navTarget = _eventNavTargets[type];
    el.className = 'event-item' + (navTarget ? ' event-clickable' : '');
    if (navTarget) {
        el.onclick = function() {
            toggleDrawer();
            window.switchView(navTarget[0]);
            if (navTarget[1]) window.switchExploreTab(navTarget[1]);
        };
    }
    var detail = buildEventDetail(type, payload);
    el.innerHTML = '<div class="event-dot ' + (_eventDotColors[type] || 'cyan') + '"></div><div class="event-body"><div class="event-type">' + escapeHtml(type.replace(/_/g, ' ')) + '</div><div class="event-desc">' + escapeHtml(description) + '</div>' + detail + '<div class="event-time" data-ts="' + new Date(timestamp).toISOString() + '">' + relativeTime(new Date(timestamp)) + '</div></div>';
    container.prepend(el);
    while (container.children.length > CONFIG.MAX_EVENTS) container.removeChild(container.lastChild);
    if (!state.drawerOpen) { state.unreadEvents++; updateBadge(); }
}

export function clearEvents() { document.getElementById('eventsContainer').innerHTML = ''; }

export async function triggerConsolidation() {
    try { await apiFetch(CONFIG.API_BASE + '/consolidation/run', { method: 'POST' }); showToast('Consolidation triggered', 'success'); }
    catch (e) { showToast('Failed to trigger consolidation', 'error'); }
}

// ── Insights ──
export function formatInsightDetail(obsType, details) {
    if (!details || Object.keys(details).length === 0) return '';
    switch (obsType) {
        case 'source_balance': {
            var counts = details.source_counts || {};
            var parts = Object.entries(counts).sort(function(a,b) { return b[1] - a[1]; }).map(function(kv) { return kv[0] + ': ' + kv[1].toLocaleString(); });
            return (details.dominant_source || '?') + ' dominates at ' + ((details.dominant_ratio || 0) * 100).toFixed(1) + '% (' + parts.join(', ') + ')';
        }
        case 'autonomous_action': {
            var action = (details.action || '').replace(/_/g, ' ');
            if (details.trigger) return action + ' (trigger: ' + details.trigger.replace(/_/g, ' ') + ')';
            if (details.count) return action + ' (' + details.count + ' memories)';
            return action;
        }
        case 'quality_audit': {
            var issues = [];
            if (details.no_embedding) issues.push(details.no_embedding + ' missing embeddings');
            if (details.no_compression) issues.push(details.no_compression + ' uncompressed');
            if (details.short_summary) issues.push(details.short_summary + ' short summaries');
            if (details.long_summary) issues.push(details.long_summary + ' long summaries');
            return issues.length > 0 ? issues.join(', ') : 'All checks passed';
        }
        case 'recall_effectiveness': {
            var active = details.total_active || 0;
            var dead = details.dead_count || 0;
            var ratio = ((details.dead_ratio || 0) * 100).toFixed(0);
            return dead + ' of ' + active + ' memories inactive (' + ratio + '% dead ratio)';
        }
        default: {
            return Object.entries(details).map(function(kv) {
                var v = kv[1];
                if (v && typeof v === 'object') v = JSON.stringify(v);
                return kv[0].replace(/_/g, ' ') + ': ' + v;
            }).join(' \u00b7 ');
        }
    }
}

export async function loadInsights() {
    try {
        var data = await fetchJSON('/insights');
        var observations = data.observations || [];
        var container = document.getElementById('insightsContainer');
        if (observations.length === 0) { container.innerHTML = '<div style="padding:8px;font-size:0.8rem;color:var(--text-dim)">No insights yet</div>'; return; }
        // Deduplicate: keep only the most recent of each observation type
        var seenTypes = {};
        observations = observations.filter(function(obs) {
            var t = obs.observation_type || '';
            if (seenTypes[t]) return false;
            seenTypes[t] = true;
            return true;
        });
        container.innerHTML = observations.slice(0, 10).map(function(obs) {
            var sevClass = obs.severity === 'critical' ? 'critical' : obs.severity === 'warning' ? 'warning' : '';
            var detailStr = formatInsightDetail(obs.observation_type, obs.details);
            return '<div class="insight-item ' + sevClass + '"><div class="insight-type">' + escapeHtml(obs.observation_type || '').replace(/_/g, ' ') + '</div><div class="insight-detail">' + escapeHtml(detailStr) + '</div></div>';
        }).join('');
    } catch (e) { console.error('Failed to load insights:', e); showToast('Failed to load insights', 'error'); }
}

// ── Stats ──
export async function loadStats() {
    try {
        var results = await Promise.all([apiFetch(CONFIG.API_BASE + '/health'), apiFetch(CONFIG.API_BASE + '/stats')]);
        var health = await results[0].json();
        var stats = await results[1].json();
        var mc = document.getElementById('navMemoryCount');
        if (mc) mc.textContent = (stats.store && stats.store.active_memories) || health.memory_count || 0;
        var dot = document.getElementById('healthDot') || document.getElementById('navHealth');
        if (dot) {
            dot.className = health.status === 'ok' ? 'nav-health' : 'nav-health degraded';
            dot.title = health.status === 'ok' ? 'daemon online' : 'degraded';
        }
        if (health.version) {
            var vEl = document.getElementById('navVersion');
            if (vEl) {
                vEl.textContent = 'v' + health.version;
                vEl.href = 'https://github.com/AppSprout-dev/mnemonic/releases/tag/v' + health.version;
            }
        }
        if (health.tool_count) {
            document.getElementById('toolHeaderSub').textContent = 'recall \u00b7 remember \u00b7 get_context \u00b7 ' + health.tool_count + ' tools';
        }
        state.previousStats = stats;
        // Update forum footer
        var total = (stats.store && stats.store.active_memories) || health.memory_count || 0;
        var fa = document.getElementById('footActive');
        var ff = document.getElementById('footFading');
        var far = document.getElementById('footArchived');
        var fv = document.getElementById('footVersion');
        var fe = document.getElementById('footEncoding');
        var fc = document.getElementById('footConsolidation');
        if (fa) fa.textContent = (stats.store ? stats.store.active_memories : total) + ' active';
        if (ff) ff.textContent = (stats.store ? stats.store.fading_memories : 0) + ' fading';
        if (far) far.textContent = (stats.store ? stats.store.archived_memories : 0) + ' archived';
        if (fv) fv.textContent = 'mnemonic ' + (health.version ? 'v' + health.version : '');
        if (fe) fe.textContent = 'encoding: ' + (health.encoding_pending > 0 ? health.encoding_pending + ' pending' : 'idle');
        if (fc && stats.last_consolidation) {
            var d = new Date(stats.last_consolidation);
            fc.textContent = 'consolidation: ' + d.toLocaleTimeString([], {hour:'2-digit',minute:'2-digit'});
        }
        // Update welcome panel
        var wa = document.getElementById('welcomeActive');
        var wf = document.getElementById('welcomeFading');
        var war = document.getElementById('welcomeArchived');
        var wv = document.getElementById('welcomeVisit');
        if (wa) wa.textContent = stats.store ? stats.store.active_memories : total;
        if (wf) wf.textContent = stats.store ? stats.store.fading_memories : 0;
        if (war) war.textContent = stats.store ? stats.store.archived_memories : 0;
        if (wv) {
            var lastVisit = localStorage.getItem('mnemonic-last-visit');
            if (lastVisit) {
                wv.textContent = 'Last visit: ' + new Date(parseInt(lastVisit)).toLocaleString([], {hour:'2-digit',minute:'2-digit',month:'short',day:'numeric'});
            } else {
                wv.textContent = 'Welcome to mnemonic';
            }
            localStorage.setItem('mnemonic-last-visit', Date.now().toString());
        }
        // Update healthDot (forum nav uses different ID)
        var hd = document.getElementById('healthDot');
        if (hd) {
            hd.className = health.status === 'ok' ? 'nav-health' : 'nav-health degraded';
            hd.title = health.status === 'ok' ? 'daemon online' : 'degraded';
        }
    } catch (e) {
        document.getElementById('navHealth').className = 'nav-health down';
        document.getElementById('navHealth').title = 'Cannot reach server';
    }
}

// ── Projects ──
export async function loadProjects() {
    try {
        var data = await fetchJSON('/projects');
        state.projectList = data.projects || [];
        var select = document.getElementById('rememberProject');
        state.projectList.forEach(function(p) {
            var opt = document.createElement('option');
            opt.value = p; opt.textContent = p;
            select.appendChild(opt);
        });
        window.populateTimelineProjects();
    } catch (e) { console.error('Failed to load projects:', e); }
}

