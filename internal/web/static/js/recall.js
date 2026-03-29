import { state, CONFIG } from './state.js';
import { apiFetch, escapeHtml, showToast, memoryType, safeSalience, memoryTypeAbbr, memoryTypeIcon } from './utils.js';

// ── Recall ──
export async function performRecall() {
    var input = document.getElementById('recallInput');
    var query = input.value.trim();
    if (!query) return;
    var btn = document.getElementById('recallBtn');
    btn.disabled = true;
    btn.innerHTML = '<span class="spinner"></span>';
    var results = document.getElementById('recallResults');
    results.innerHTML = '<div class="skeleton skeleton-card"></div><div class="skeleton skeleton-card"></div><div class="skeleton skeleton-card"></div>';
    try {
        var synth = document.getElementById('synthesizeToggle').checked;
        var resp = await apiFetch(CONFIG.API_BASE + '/query', {
            method: 'POST',
            headers: { 'Content-Type': 'application/json' },
            body: JSON.stringify({ query: query, limit: 10, synthesize: synth, include_reasoning: false }),
        });
        var data = await resp.json();
        state.lastQueryId = data.query_id;
        state.lastQueryResults = data.memories || [];
        renderResults(data);
    } catch (e) {
        results.innerHTML = '<div class="empty-state"><div class="empty-state-icon">&#9888;</div><div class="empty-state-text">Failed to execute query</div><div class="empty-state-sub">' + escapeHtml(e.message) + '</div></div>';
    } finally {
        btn.disabled = false;
        btn.textContent = 'Recall';
    }
}

export function renderResults(data) {
    var results = document.getElementById('recallResults');
    var memories = data.memories || [];
    if (memories.length === 0) {
        results.innerHTML = '<div class="empty-state"><div class="empty-state-icon">&#128269;</div><div class="empty-state-text">No memories found</div><div class="empty-state-sub">Try a different search term</div></div>';
        return;
    }
    var hops = data.hops || 0;
    var html = '<div class="recall-info">' + memories.length + ' result' + (memories.length !== 1 ? 's' : '') + ' &middot; ' + (data.took_ms || 0) + 'ms' + (hops ? ' &middot; spread activation: ' + hops + ' hops' : '') + '</div>';
    html += '<div class="forabg" style="margin:0 0 8px"><div class="inner">';
    html += '<ul class="topiclist"><li class="header"><dl class="row-item"><dt><div class="list-inner">Memory</div></dt><dd>Score</dd><dd>Sal</dd><dd class="lastpost">Created</dd></dl></li></ul>';
    html += '<ul class="topiclist forums">';
    memories.forEach(function(mem, idx) {
        var score = mem.score || 0;
        var scoreColor = score > 0.7 ? 'var(--link)' : score > 0.4 ? 'var(--text-secondary)' : 'var(--text-dim)';
        var m = mem.memory;
        var type = memoryType(m);
        var typeAbbr = memoryTypeAbbr(type);
        var iconClass = memoryTypeIcon(type);
        var salPct = safeSalience(m.salience);
        var dateStr = m.created_at ? new Date(m.created_at).toLocaleTimeString([], {hour:'2-digit',minute:'2-digit'}) : '';
        var summary = m.summary || (m.content || '').slice(0, 150);
        var bgClass = idx % 2 === 0 ? 'bg1' : 'bg2';

        html += '<li class="row ' + bgClass + '" onclick="toggleExpand(this)">';
        html += '<dl class="row-item"><dt>';
        html += '<a class="row-item-link"><span class="status-icon ' + iconClass + '">' + typeAbbr + '</span></a>';
        html += '<div class="list-inner"><a class="forumtitle">' + escapeHtml(summary) + '</a></div>';
        html += '</dt>';
        html += '<dd style="color:' + scoreColor + ';font-weight:bold">' + score.toFixed(2) + '</dd>';
        html += '<dd>' + salPct + '%</dd>';
        html += '<dd class="lastpost"><span class="lastpost-time">' + dateStr + '</span></dd>';
        html += '</dl></li>';
    });
    html += '</ul></div></div>';
    html += '<div class="feedback-bar">';
    html += '<span class="feedback-label">Rate these results:</span>';
    html += '<button class="feedback-btn helpful" onclick="submitFeedback(\'helpful\', event)">Helpful</button>';
    html += '<button class="feedback-btn partial" onclick="submitFeedback(\'partial\', event)">Partial</button>';
    html += '<button class="feedback-btn irrelevant" onclick="submitFeedback(\'irrelevant\', event)">Irrelevant</button>';
    html += '</div>';
    if (data.synthesis) {
        html += '<div class="synthesis-block"><div class="synthesis-label">Synthesis</div><div class="synthesis-text">' + escapeHtml(data.synthesis) + '</div></div>';
    }
    results.innerHTML = html;
}

export function toggleExpand(card) {
    var expanded = card.querySelector('.result-expanded, .episode-expanded, .memory-expanded, .expand-zone');
    if (expanded) expanded.classList.toggle('open');
}

export function toggle(id) {
    var el = document.getElementById(id);
    if (el) el.classList.toggle('open');
}


// ── Remember ──
export function toggleRemember() {
    state.rememberOpen = !state.rememberOpen;
    document.getElementById('rememberToggle').classList.toggle('open', state.rememberOpen);
    document.getElementById('rememberForm').classList.toggle('open', state.rememberOpen);
}

export async function submitRemember() {
    var content = document.getElementById('rememberContent').value.trim();
    if (!content) return;
    var type = document.getElementById('rememberType').value;
    var project = document.getElementById('rememberProject').value;
    var btn = document.querySelector('.remember-submit');
    btn.disabled = true;
    try {
        await apiFetch(CONFIG.API_BASE + '/memories', {
            method: 'POST',
            headers: { 'Content-Type': 'application/json' },
            body: JSON.stringify({ content: content, source: 'web_ui', type: type, project: project }),
        });
        document.getElementById('rememberContent').value = '';
        showToast('Memory stored', 'success');
        state.exploreLoaded.memories = false;
    } catch (e) {
        showToast('Failed to store memory', 'error');
    } finally {
        btn.disabled = false;
    }
}
