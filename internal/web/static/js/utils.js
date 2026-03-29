import { CONFIG } from './state.js';

// Wrapper for fetch that adds auth header when token is configured
export function apiFetch(url, opts) {
    opts = opts || {};
    if (CONFIG.TOKEN) {
        opts.headers = Object.assign({}, opts.headers, { 'Authorization': 'Bearer ' + CONFIG.TOKEN });
    }
    return fetch(url, opts);
}

// Fetch JSON helper — calls apiFetch, checks response, parses JSON
export async function fetchJSON(path, opts) {
    var resp = await apiFetch(CONFIG.API_BASE + path, opts);
    if (!resp.ok) throw new Error('HTTP ' + resp.status + ': ' + resp.statusText);
    return resp.json();
}

// Generate an array of day buckets for the last N days (for D3 charts)
export function makeDayBuckets(days, extraFields) {
    var now = new Date();
    var dayMs = 86400000;
    var buckets = [];
    for (var i = days - 1; i >= 0; i--) {
        var d = new Date(now.getTime() - i * dayMs);
        d.setHours(0, 0, 0, 0);
        var bucket = { date: d, idx: days - 1 - i };
        if (extraFields) {
            for (var k in extraFields) { bucket[k] = extraFields[k]; }
        }
        buckets.push(bucket);
    }
    return buckets;
}

// ── Toast ──
export function showToast(message, type) {
    type = type || 'success';
    var container = document.getElementById('toastContainer');
    var toast = document.createElement('div');
    toast.className = 'toast ' + type;
    toast.innerHTML = '<span class="toast-icon">' + (type === 'success' ? '&#10003;' : '&#9888;') + '</span> ' + escapeHtml(message);
    container.appendChild(toast);
    setTimeout(function() { if (toast.parentNode) toast.parentNode.removeChild(toast); }, 3000);
}

// ── SVG Chart Helpers ──
var SVG_NS = 'http://www.w3.org/2000/svg';
export function svgEl(tag, attrs) {
    var el = document.createElementNS(SVG_NS, tag);
    if (attrs) for (var k in attrs) el.setAttribute(k, attrs[k]);
    return el;
}
export function linScale(val, dMin, dMax, rMin, rMax) {
    var dRange = dMax - dMin;
    if (dRange === 0) return rMin;
    return rMin + ((val - dMin) / dRange) * (rMax - rMin);
}
export function svgText(x, y, text, attrs) {
    var el = svgEl('text', Object.assign({ x: x, y: y, fill: 'var(--text-dim)', 'font-size': '10' }, attrs || {}));
    el.textContent = text;
    return el;
}
export function fmtNum(n) {
    if (n >= 1e6) return (n / 1e6).toFixed(1) + 'M';
    if (n >= 1e3) return (n / 1e3).toFixed(1) + 'k';
    return String(n);
}

// ── Utilities ──
export function escapeHtml(str) { if (!str) return ''; var div = document.createElement('div'); div.textContent = str; return div.innerHTML; }

// Map raw source event types to display types
var _sourceTypeMap = { file_created: 'general', file_modified: 'general', repo_changed: 'general', clipboard: 'general', command_run: 'general', handoff: 'general' };
export function memoryType(m) {
    var raw = (m.type || m.memory_type || 'general').toLowerCase();
    return _sourceTypeMap[raw] || raw;
}
export function memoryTypeAbbr(type) { return type === 'insight' ? 'IN' : type === 'decision' ? 'DE' : type === 'learning' ? 'LE' : type === 'error' ? 'ER' : 'GN'; }
export function memoryTypeIcon(type) { return type === 'insight' ? 'icon-in' : type === 'decision' ? 'icon-de' : type === 'learning' ? 'icon-le' : type === 'error' ? 'icon-er' : 'icon-ep'; }
export function safeSalience(s) { return Math.min(100, Math.round((s || 0) * 100)); }

// Agent identity system — maps memory sources to forum "users"
var _agentProfiles = {
    mcp: { name: 'Claude Session', title: 'MCP Client', icon: 'CS', color: 'var(--accent-cyan)' },
    filesystem: { name: 'Perception Agent', title: 'Filesystem Watcher', icon: 'PA', color: 'var(--accent-green)' },
    git: { name: 'Git Observer', title: 'Repository Watcher', icon: 'GO', color: 'var(--accent-orange)' },
    terminal: { name: 'Terminal Agent', title: 'Command Observer', icon: 'TA', color: 'var(--accent-yellow)' },
    clipboard: { name: 'Clipboard Agent', title: 'Clipboard Monitor', icon: 'CB', color: 'var(--accent-pink)' },
    ingest: { name: 'Ingest Engine', title: 'Bulk Importer', icon: 'IE', color: 'var(--text-dim)' },
    system: { name: 'Daemon', title: 'System Process', icon: 'SY', color: 'var(--accent-violet)' },
    benchmark: { name: 'Benchmark', title: 'Quality Tester', icon: 'BM', color: 'var(--accent-blue)' },
};
export function agentProfile(source) {
    return _agentProfiles[(source || 'mcp').toLowerCase()] || { name: source || 'Unknown', title: 'Agent', icon: '??', color: 'var(--text-dim)' };
}

// Live forum feed — agents posting in real time
var _liveFeedCount = 0;
var _wsAgentMap = {
    raw_memory_created: function(p) { return { agent: agentProfile(p.source || 'filesystem'), text: 'Observed: ' + (p.summary || p.content || '').slice(0, 120), action: "switchView('timeline')" }; },
    memory_encoded: function(p) { return { agent: _agentProfiles.system || agentProfile('system'), text: 'Encoded memory: ' + (p.summary || '').slice(0, 120), action: "switchView('explore')" }; },
    consolidation_started: function() { return { agent: { name: 'Consolidation Agent', title: 'Memory Maintainer', icon: 'CA', color: 'var(--accent-yellow)' }, text: 'Starting consolidation cycle...', action: null }; },
    consolidation_completed: function(p) { return { agent: { name: 'Consolidation Agent', title: 'Memory Maintainer', icon: 'CA', color: 'var(--accent-yellow)' }, text: 'Consolidation complete: ' + (p.memories_processed || 0) + ' processed, ' + (p.memories_decayed || 0) + ' decayed, ' + (p.memories_merged || 0) + ' merged', action: "switchView('tools')" }; },
    dream_cycle_completed: function(p) { return { agent: { name: 'Dreaming Agent', title: 'Memory Replay', icon: 'DA', color: 'var(--accent-violet)' }, text: 'Dream cycle: replayed ' + (p.memories_replayed || 0) + ', strengthened ' + (p.associations_strengthened || 0) + ' associations, ' + (p.insights_generated || 0) + ' insights', action: "switchView('tools')" }; },
    pattern_discovered: function(p) { return { agent: { name: 'Abstraction Agent', title: 'Pattern Discovery', icon: 'AA', color: 'var(--accent-orange)' }, text: 'New pattern: ' + (p.title || ''), action: "switchView('explore')" }; },
    abstraction_created: function(p) { return { agent: { name: 'Abstraction Agent', title: 'Principle Synthesis', icon: 'AA', color: 'var(--accent-orange)' }, text: (p.level === 3 ? 'New axiom' : 'New principle') + ': ' + (p.title || ''), action: "switchView('explore')" }; },
    episode_closed: function(p) { return { agent: { name: 'Episoding Agent', title: 'Episode Clustering', icon: 'EP', color: 'var(--accent-violet)' }, text: 'Episode closed: ' + (p.title || ''), action: p.episode_id ? "loadThread('" + p.episode_id + "')" : "switchView('explore')" }; },
    query_executed: function(p) { return { agent: { name: 'Retrieval Agent', title: 'Spread Activation', icon: 'RA', color: 'var(--accent-cyan)' }, text: 'Recall query: "' + (p.query_text || '').slice(0, 60) + '" → ' + (p.results_returned || 0) + ' results', action: "switchView('recall')" }; },
    forum_post_created: function(p) {
        var key = p.author_key || '';
        var profiles = {
            consolidation: { name: 'Consolidation Agent', icon: 'CA', color: 'var(--accent-yellow)' },
            dreaming: { name: 'Dreaming Agent', icon: 'DA', color: 'var(--accent-violet)' },
            episoding: { name: 'Episoding Agent', icon: 'EP', color: 'var(--accent-violet)' },
            abstraction: { name: 'Abstraction Agent', icon: 'AA', color: 'var(--accent-orange)' },
            metacognition: { name: 'Metacognition Agent', icon: 'MA', color: 'var(--accent-blue)' },
            encoding: { name: 'Encoding Agent', icon: 'EA', color: 'var(--accent-blue)' },
            perception: { name: 'Perception Agent', icon: 'PA', color: 'var(--accent-green)' },
            retrieval: { name: 'Retrieval Agent', icon: 'RA', color: 'var(--accent-cyan)' },
        };
        var agent = profiles[key] || { name: p.author_name || 'Human', icon: 'HU', color: 'var(--accent-cyan)' };
        return { agent: agent, text: (p.content || '').slice(0, 120), action: "loadForumThread('" + p.thread_id + "')" };
    },
};

export function addLivePost(wsType, payload, timestamp) {
    var mapper = _wsAgentMap[wsType];
    if (!mapper) return;
    var post = mapper(payload);
    if (!post) return;
    var feed = document.getElementById('liveFeedBody');
    if (!feed) return;

    _liveFeedCount++;
    var countEl = document.getElementById('liveFeedCount');
    if (countEl) countEl.textContent = _liveFeedCount + ' events this session';

    var time = timestamp ? new Date(timestamp).toLocaleTimeString([], {hour:'2-digit',minute:'2-digit',second:'2-digit'}) : new Date().toLocaleTimeString([], {hour:'2-digit',minute:'2-digit',second:'2-digit'});
    var bgClass = _liveFeedCount % 2 === 0 ? 'bg1' : 'bg2';

    var clickAttr = post.action ? ' onclick="' + post.action + '" style="animation:fadeIn 0.3s ease-out;cursor:pointer"' : ' style="animation:fadeIn 0.3s ease-out"';
    var html = '<div class="tl-row ' + bgClass + '"' + clickAttr + '>';
    html += '<span class="status-icon" style="width:22px;height:22px;font-size:0.6rem;flex-shrink:0;background:color-mix(in srgb, ' + post.agent.color + ' 15%, transparent);color:' + post.agent.color + ';border:1px solid color-mix(in srgb, ' + post.agent.color + ' 25%, transparent)">' + post.agent.icon + '</span>';
    html += '<span class="tl-row-title" style="white-space:normal"><strong style="color:' + post.agent.color + '">' + escapeHtml(post.agent.name) + '</strong> &middot; ' + escapeHtml(post.text) + '</span>';
    html += '<span class="tl-row-time">' + time + '</span>';
    html += '</div>';

    // Remove the "Listening..." placeholder
    if (_liveFeedCount === 1) feed.innerHTML = '';

    // Prepend (newest at top)
    feed.insertAdjacentHTML('afterbegin', html);

    // Cap at 50 entries
    while (feed.children.length > 50) feed.removeChild(feed.lastChild);
}
export function simpleMarkdown(str) {
    if (!str) return '';
    var lines = str.split('\n');
    var html = '';
    var inList = false;
    for (var i = 0; i < lines.length; i++) {
        var line = lines[i];
        var isBullet = /^[-*]\s/.test(line);
        if (isBullet) {
            if (!inList) { html += '<ul>'; inList = true; }
            var content = escapeHtml(line.replace(/^[-*]\s+/, ''));
            content = content.replace(/\*\*(.+?)\*\*/g, '<strong>$1</strong>');
            content = content.replace(/`([^`]+)`/g, '<code>$1</code>');
            html += '<li>' + content + '</li>';
        } else {
            if (inList) { html += '</ul>'; inList = false; }
            var content = escapeHtml(line);
            content = content.replace(/\*\*(.+?)\*\*/g, '<strong>$1</strong>');
            content = content.replace(/`([^`]+)`/g, '<code>$1</code>');
            if (content) html += '<p style="margin:4px 0">' + content + '</p>';
        }
    }
    if (inList) html += '</ul>';
    return html;
}
