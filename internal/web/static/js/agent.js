import { state, CONFIG, MONTHS } from './state.js';
import { apiFetch, fetchJSON, escapeHtml, showToast, makeDayBuckets, simpleMarkdown, svgEl, linScale, svgText, renderMarkdown } from './utils.js';

// ── Agent ──
export async function loadAgentData() {
    try {
        var [evoRes, logRes, sessRes, sysRes, cfgRes] = await Promise.all([
            apiFetch(CONFIG.API_BASE + '/agent/evolution').then(function(r) { return r.json(); }),
            apiFetch(CONFIG.API_BASE + '/agent/changelog').then(function(r) { return r.json(); }),
            apiFetch(CONFIG.API_BASE + '/agent/sessions').then(function(r) { return r.json(); }),
            apiFetch(CONFIG.API_BASE + '/stats').then(function(r) { return r.json(); }),
            apiFetch(CONFIG.API_BASE + '/agent/config').then(function(r) { return r.json(); }).catch(function() { return {}; }),
        ]);
        state.agentData = { evolution: evoRes, changelog: logRes, sessions: sessRes, system: sysRes };
        state.agentLoaded = true;
        renderAgentDashboard();
        // Initialize chat panel (only once)
        if (cfgRes.chat_enabled && cfgRes.web_port && !agentChat.wsUrl) {
            initAgentChat(cfgRes.web_port);
        }
    } catch (e) {
        console.error('Failed to load agent data:', e);
        document.getElementById('agentStatsRow').innerHTML = '<div class="agent-empty">Agent dashboard unavailable. Is agent_sdk enabled in config?</div>';
    }
}

export function refreshAgentData() {
    var btn = document.getElementById('agentRefreshBtn');
    btn.classList.add('loading');
    state.agentLoaded = false;
    loadAgentData().finally(function() { btn.classList.remove('loading'); });
}

export function renderAgentDashboard() {
    var d = state.agentData;
    if (!d) return;
    // Update timestamp
    document.getElementById('agentUpdated').textContent = 'Updated ' + new Date().toLocaleTimeString([], { hour: '2-digit', minute: '2-digit' });
    renderAgentStats(d.evolution, d.sessions);
    renderSDKCostChart(d.sessions.sessions || []);
    renderAgentMemoryBar(d.system);
    renderAgentPrinciples(d.evolution.principles);
    renderAgentStrategies(d.evolution.strategies);
    renderAgentTimeline(d.changelog.entries);
    renderAgentSessions(d.sessions);
    renderAgentPatches(d.evolution.patches);
}

export function renderAgentStats(evo, sess) {
    var patchCount = (evo.patches || []).filter(function(p) { var text = p.content || p.instruction || ''; return text.trim(); }).length;
    var avgCost = sess.stats.total_tasks > 0 ? sess.stats.total_cost_usd / sess.stats.total_tasks : 0;
    var lastEvo = '';
    if (state.agentData && state.agentData.changelog && state.agentData.changelog.entries && state.agentData.changelog.entries.length > 0) {
        lastEvo = state.agentData.changelog.entries[0].date || '';
    }

    var totalTurns = 0;
    (sess.sessions || []).forEach(function(s) { (s.tasks || []).forEach(function(t) { totalTurns += (t.turns || 0); }); });
    var totalApiEquiv = sess.stats.total_cost_usd || 0;
    var taskCards = [
        { n: sess.stats.session_count, l: 'Sessions' },
        { n: sess.stats.total_tasks, l: 'Tasks Run' },
        { n: totalTurns.toLocaleString(), l: 'Total Turns', sub: 'subscription · not billed' },
        { n: sess.stats.avg_turns ? sess.stats.avg_turns.toFixed(1) : '0', l: 'Avg Turns / Task' },
        { n: '$' + totalApiEquiv.toFixed(4), l: 'API Equiv.', sub: 'if billed per token', cls: 'sdk-api-equiv' },
    ];
    var evoCards = [
        { n: evo.principles.length, l: 'Principles', sub: lastEvo ? 'Last: ' + lastEvo : '' },
        { n: Object.keys(evo.strategies).length, l: 'Strategies' },
        { n: patchCount, l: 'Prompt Patches' },
    ];

    function cardHtml(cards, gridClass) {
        return '<div class="' + gridClass + '">' + cards.map(function(s) {
            return '<div class="agent-stat-card' + (s.cls ? ' ' + s.cls : '') + '"><div class="stat-number">' + s.n + '</div><div class="stat-label">' + s.l + '</div>' +
                (s.sub ? '<div class="stat-sub">' + escapeHtml(s.sub) + '</div>' : '') + '</div>';
        }).join('') + '</div>';
    }

    document.getElementById('agentStatsRow').innerHTML =
        '<div class="sdk-section">' +
            '<div class="sdk-section-label">Task Activity</div>' +
            cardHtml(taskCards, 'sdk-stat-cards-5') +
        '</div>' +
        '<div class="sdk-section">' +
            '<div class="sdk-section-label">Evolution State</div>' +
            cardHtml(evoCards, 'sdk-evo-cards') +
        '</div>';
}

export function renderSDKCostChart(sessions) {
    var container = document.getElementById('sdkCostChart');
    var tooltip = document.getElementById('sdkCostTooltip');
    container.innerHTML = '';
    if (!sessions || sessions.length === 0) {
        container.innerHTML = '<div class="llm-empty">No sessions yet</div>';
        return;
    }

    var now = new Date();
    var dayMs = 86400000;
    var buckets = makeDayBuckets(30, { tasks: 0, turns: 0 });
    sessions.forEach(function(s) {
        (s.tasks || []).forEach(function(t) {
            if (!t.started) return;
            var td = new Date(t.started);
            td.setHours(0, 0, 0, 0);
            var diff = Math.floor((now.getTime() - td.getTime()) / dayMs);
            if (diff >= 0 && diff < 30) {
                buckets[29 - diff].tasks++;
                buckets[29 - diff].turns += (t.turns || 0);
            }
        });
    });

    var maxTasks = Math.max.apply(null, buckets.map(function(b) { return b.tasks; })) || 1;
    var w = container.clientWidth || 400;
    var h = 140;
    var margin = { top: 8, right: 10, bottom: 24, left: 36 };
    var iw = w - margin.left - margin.right;
    var ih = h - margin.top - margin.bottom;

    var svg = svgEl('svg', { width: w, height: h });
    container.appendChild(svg);
    var g = svgEl('g', { transform: 'translate(' + margin.left + ',' + margin.top + ')' });
    svg.appendChild(g);

    var nb = buckets.length;
    var bandStep = iw / nb;
    var bandPad = 0.3;
    var bandW = bandStep * (1 - bandPad);
    var bandOff = bandStep * bandPad / 2;

    // Y axis ticks
    var yTicks = Math.min(maxTasks, 4);
    var yStep = maxTasks / yTicks;
    for (var t = 0; t <= yTicks; t++) {
        var tv = Math.round(t * yStep);
        var ty = linScale(tv, 0, maxTasks, ih, 0);
        g.appendChild(svgEl('line', { x1: -4, x2: 0, y1: ty, y2: ty, stroke: 'var(--border-subtle)' }));
        g.appendChild(svgText(-8, ty, String(tv), { 'text-anchor': 'end', 'dominant-baseline': '0.32em', 'font-size': '10' }));
    }
    // X axis baseline
    g.appendChild(svgEl('line', { x1: 0, x2: iw, y1: ih, y2: ih, stroke: 'var(--border-subtle)' }));
    // X axis labels (every 7 days)
    buckets.forEach(function(b) {
        if (b.idx % 7 !== 0) return;
        var bx = b.idx * bandStep + bandStep / 2;
        g.appendChild(svgEl('line', { x1: bx, x2: bx, y1: ih, y2: ih + 4, stroke: 'var(--border-subtle)' }));
        g.appendChild(svgText(bx, ih + 14, MONTHS[b.date.getMonth()] + ' ' + b.date.getDate(), { 'text-anchor': 'middle', 'font-size': '10' }));
    });

    // Bars + hit areas
    buckets.forEach(function(b) {
        var bx = b.idx * bandStep + bandOff;
        if (b.tasks > 0) {
            var barY = linScale(b.tasks, 0, maxTasks, ih, 0);
            g.appendChild(svgEl('rect', { x: bx, y: barY, width: bandW, height: ih - barY, rx: 2, fill: 'var(--accent-green)', opacity: '0.8', 'pointer-events': 'none' }));
        }
        var hit = svgEl('rect', { x: bx - bandW * 0.1, y: 0, width: bandW * 1.2, height: ih, fill: 'transparent' });
        (function(bb) {
            hit.addEventListener('mouseover', function() {
                if (bb.tasks === 0) return;
                tooltip.textContent = MONTHS[bb.date.getMonth()] + ' ' + bb.date.getDate() + ' \u00B7 ' + bb.tasks + ' task' + (bb.tasks === 1 ? '' : 's') + ' \u00B7 ' + bb.turns + ' turns';
                tooltip.style.display = 'block';
                var cx = margin.left + bb.idx * bandStep + bandStep / 2;
                var ttLeft = cx + 8;
                if (ttLeft + 200 > w) ttLeft = cx - 210;
                tooltip.style.left = ttLeft + 'px';
            });
            hit.addEventListener('mouseout', function() { tooltip.style.display = 'none'; });
        })(b);
        g.appendChild(hit);
    });
}

export function renderAgentMemoryBar(sys) {
    if (!sys || !sys.store) { document.getElementById('agentMemoryBar').innerHTML = ''; return; }
    var st = sys.store;
    var sizeMB = (st.storage_size_bytes / (1024 * 1024)).toFixed(1);
    var items = [
        { n: st.total_memories, l: 'Total Memories' },
        { n: st.active_memories, l: 'Active' },
        { n: st.fading_memories + st.archived_memories, l: 'Fading + Archived' },
        { n: (st.total_associations || 0).toLocaleString(), l: 'Associations' },
        { n: sizeMB + ' MB', l: 'Storage' },
    ];
    document.getElementById('agentMemoryBar').innerHTML = items.map(function(s) {
        return '<div class="agent-memory-stat"><div class="stat-number">' + s.n + '</div><div class="stat-label">' + s.l + '</div></div>';
    }).join('');
}

export function renderAgentPrinciples(principles) {
    var el = document.getElementById('agentPrinciplesContent');
    var countEl = document.getElementById('agentPrinciplesCount');
    if (countEl) countEl.textContent = (principles || []).length;
    if (!principles || principles.length === 0) { el.innerHTML = '<div class="agent-empty">No principles learned yet. Run the agent to start building knowledge.</div>'; return; }
    // Sort by confidence descending, then by numeric ID ascending as tiebreaker
    var sorted = principles.slice().sort(function(a, b) {
        var diff = (b.confidence || 0) - (a.confidence || 0);
        if (diff !== 0) return diff;
        var aNum = parseInt((a.id || '').replace(/\D/g, ''), 10) || 0;
        var bNum = parseInt((b.id || '').replace(/\D/g, ''), 10) || 0;
        return aNum - bNum;
    });
    el.innerHTML = sorted.map(function(p) {
        var confPct = Math.round((p.confidence || 0) * 100);
        return '<div class="principle-card">' +
            '<div class="principle-header">' +
                '<span class="principle-id">' + escapeHtml(p.id || '?') + '</span>' +
                (p.created ? '<span class="principle-date">' + escapeHtml(p.created) + '</span>' : '') +
            '</div>' +
            '<div class="principle-text">' + escapeHtml(p.text) + '</div>' +
            '<div class="principle-meta">' +
                '<div class="confidence-bar"><div class="confidence-fill" style="width:' + confPct + '%"></div></div>' +
                '<span>' + confPct + '%</span>' +
            '</div>' +
            (p.source ? '<div class="principle-source" title="' + escapeHtml(p.source) + '">' + escapeHtml(p.source) + '</div>' : '') +
            '</div>';
    }).join('');
}

export function renderAgentStrategies(strategies) {
    var el = document.getElementById('agentStrategiesContent');
    var keys = Object.keys(strategies || {});
    var countEl = document.getElementById('agentStrategiesCount');
    if (countEl) countEl.textContent = keys.length;
    if (keys.length === 0) { el.innerHTML = '<div class="agent-empty">No strategies developed yet. The agent creates strategies as it completes tasks.</div>'; return; }
    el.innerHTML = keys.map(function(key) {
        var s = strategies[key];
        var steps = s.steps || [];
        var tips = s.tips || [];
        var stepsHtml = steps.map(function(step, i) {
            return '<div class="strategy-step" data-n="' + (i + 1) + '">' + escapeHtml(step) + '</div>';
        }).join('');
        var tipsHtml = tips.length > 0 ? '<div class="strategy-section-label">Tips</div>' + tips.map(function(tip) {
            return '<div class="strategy-tip">' + escapeHtml(tip) + '</div>';
        }).join('') : '';
        return '<div class="strategy-card" onclick="this.classList.toggle(\'open\')">' +
            '<div class="strategy-header">' +
                '<div class="strategy-name">' + escapeHtml(key.replace(/_/g, ' ')) + '</div>' +
                '<div class="strategy-counts">' +
                    '<span class="strategy-count-badge">' + steps.length + ' steps</span>' +
                    (tips.length > 0 ? '<span class="strategy-count-badge">' + tips.length + ' tips</span>' : '') +
                '</div>' +
                '<span class="strategy-expand">&#9654;</span>' +
            '</div>' +
            '<div class="strategy-detail">' +
                '<div class="strategy-section-label">Steps</div>' +
                '<div class="strategy-steps">' + stepsHtml + '</div>' +
                tipsHtml +
            '</div></div>';
    }).join('');
}

export function renderAgentTimeline(entries) {
    var el = document.getElementById('agentTimelineContent');
    var countEl = document.getElementById('agentTimelineCount');
    if (countEl) countEl.textContent = (entries || []).length + ' events';
    if (!entries || entries.length === 0) { el.innerHTML = '<div class="agent-empty">No evolution events yet. The agent logs changes here as it evolves.</div>'; return; }
    el.innerHTML = entries.map(function(e) {
        return '<div class="changelog-entry">' +
            '<div class="changelog-date">' + escapeHtml(e.date) + '</div>' +
            '<div class="changelog-title">' + escapeHtml(e.title) + '</div>' +
            (e.rationale ? '<div class="changelog-rationale">' + simpleMarkdown(e.rationale) + '</div>' : '') +
            '</div>';
    }).join('');
}

export function renderAgentSessions(sessData) {
    var el = document.getElementById('agentSessionsContent');
    var sessions = sessData.sessions || [];
    var countEl = document.getElementById('agentSessionsCount');
    if (countEl) countEl.textContent = sessions.length + ' sessions, ' + sessData.stats.total_tasks + ' tasks';
    if (sessions.length === 0) { el.innerHTML = '<div class="agent-empty">No sessions recorded yet. Run the agent to start tracking activity.</div>'; return; }
    var html = '';
    sessions.slice().reverse().forEach(function(s) {
        var sessionCost = (s.tasks || []).reduce(function(sum, t) { return sum + (t.cost_usd || 0); }, 0);
        var sessionDate = s.started ? new Date(s.started).toLocaleDateString([], { month: 'short', day: 'numeric', hour: '2-digit', minute: '2-digit' }) : '';
        // Use first task description as session label instead of opaque ID
        var firstTask = (s.tasks || [])[0];
        var sessionLabel = (firstTask && firstTask.description) ? firstTask.description.substring(0, 60) : (s.id || '?');
        html += '<div class="session-group">';
        html += '<div class="session-group-header">' +
            '<span class="session-group-id" title="' + escapeHtml(s.id || '') + '">' + escapeHtml(sessionLabel) + '</span>' +
            '<span class="session-group-model">' + escapeHtml(s.model || '?') + '</span>' +
            '<span class="session-group-cost">$' + sessionCost.toFixed(2) + '</span>' +
            '<span class="session-group-date">' + escapeHtml(sessionDate) + '</span>' +
            '</div>';
        (s.tasks || []).forEach(function(t) {
            var durSec = Math.round((t.duration_ms || 0) / 1000);
            var durStr = durSec >= 60 ? Math.round(durSec / 60) + 'm ' + (durSec % 60) + 's' : durSec + 's';
            html += '<div class="session-task">' +
                '<div class="session-task-desc">' + escapeHtml(t.description || 'Task') + '</div>' +
                '<span class="session-badge turns">' + (t.turns || 0) + ' turns</span>' +
                '<span class="session-badge cost">$' + (t.cost_usd || 0).toFixed(2) + '</span>' +
                '<span class="session-badge duration">' + durStr + '</span>' +
                (t.evolved ? '<span class="session-badge evolved">evolved</span>' : '') +
                '</div>';
        });
        html += '</div>';
    });
    el.innerHTML = html;
}

export function renderAgentPatches(patches) {
    var container = document.getElementById('agentPatches');
    var el = document.getElementById('agentPatchesContent');
    var countEl = document.getElementById('agentPatchesCount');
    var valid = (patches || []).filter(function(p) {
        var text = p.content || p.instruction || '';
        return text.trim();
    });
    if (countEl) countEl.textContent = valid.length;
    if (valid.length === 0) { container.style.display = 'none'; return; }
    container.style.display = 'block';
    // Sort by numeric ID ascending (pp1 before pp2, etc.)
    valid.sort(function(a, b) {
        var aNum = parseInt((a.id || '').replace(/\D/g, ''), 10) || 0;
        var bNum = parseInt((b.id || '').replace(/\D/g, ''), 10) || 0;
        return aNum - bNum;
    });
    el.innerHTML = valid.map(function(p) {
        var label = p.action || p.id;
        var body = p.content || p.instruction || '';
        var meta = '';
        if (p.created) meta = '<div style="font-size:0.7rem;color:var(--text-muted);margin-top:2px">' + escapeHtml(p.created) + '</div>';
        if (p.reason) meta += '<div style="font-size:0.75rem;color:var(--text-secondary);margin-top:2px;font-style:italic">' + escapeHtml(p.reason) + '</div>';
        return '<div style="padding:8px 0;border-bottom:1px solid var(--border-subtle)">' +
            '<div style="font-size:0.75rem;color:var(--accent-cyan);font-weight:600">' + escapeHtml(label) + '</div>' +
            '<div style="font-size:0.85rem;color:var(--text-primary);margin-top:2px">' + escapeHtml(body) + '</div>' +
            meta + '</div>';
    }).join('');
}

// ── Lightweight Markdown → HTML ──
export function renderMarkdown(src) {
    var html = escapeHtml(src);
    // Code blocks (``` ... ```)
    html = html.replace(/```(\w*)\n([\s\S]*?)```/g, function(_, lang, code) {
        return '<pre class="chat-code-block"><code>' + code.replace(/\n$/, '') + '</code></pre>';
    });
    // Inline code
    html = html.replace(/`([^`\n]+)`/g, '<code class="chat-inline-code">$1</code>');
    // Bold
    html = html.replace(/\*\*([^*]+)\*\*/g, '<strong>$1</strong>');
    // Italic
    html = html.replace(/\*([^*]+)\*/g, '<em>$1</em>');
    // Headings (### / ## / #)
    html = html.replace(/^### (.+)$/gm, '<div class="chat-heading chat-h3">$1</div>');
    html = html.replace(/^## (.+)$/gm, '<div class="chat-heading chat-h2">$1</div>');
    html = html.replace(/^# (.+)$/gm, '<div class="chat-heading chat-h1">$1</div>');
    // Unordered list items
    html = html.replace(/^[-*] (.+)$/gm, '<div class="chat-list-item">$1</div>');
    // Numbered list items
    html = html.replace(/^\d+\. (.+)$/gm, '<div class="chat-list-item chat-list-num">$1</div>');
    // Line breaks (preserve double newlines as paragraph breaks)
    html = html.replace(/\n\n/g, '<div class="chat-para-break"></div>');
    html = html.replace(/\n/g, '<br>');
    return html;
}

// ── Agent Chat ──
var agentChat = { ws: null, connected: false, busy: false, wsUrl: null, currentBubble: null, reconnectTimer: null, currentConvId: null };
// Store tool input/result data in a Map keyed by tool_use_id to avoid DOM bloat from large data-attributes
var toolDataMap = new Map();

export function initAgentChat(webPort) {
    if (!webPort || webPort === 0) return;
    var proto = window.location.protocol === 'https:' ? 'wss:' : 'ws:';
    agentChat.wsUrl = proto + '//127.0.0.1:' + webPort + '/ws';
    var hint = document.getElementById('chatSetupHint');
    if (hint) hint.remove();
    // Restore model preference from localStorage
    var savedModel = localStorage.getItem('agentModel');
    if (savedModel) {
        var sel = document.getElementById('chatModelSelect');
        if (sel) sel.value = savedModel;
    }
    connectAgentChat();
}

export function connectAgentChat() {
    if (!agentChat.wsUrl) return;
    setChatStatus('connecting', 'Connecting...');
    try { agentChat.ws = new WebSocket(agentChat.wsUrl); } catch (e) {
        setChatStatus('error', 'Failed to connect');
        scheduleReconnect();
        return;
    }
    agentChat.ws.onopen = function() {
        agentChat.connected = true;
        agentChat.busy = false;
        setChatStatus('connected', 'Ready');
        document.getElementById('chatSendBtn').disabled = false;
        document.getElementById('chatInput').disabled = false;
        if (agentChat.reconnectTimer) { clearTimeout(agentChat.reconnectTimer); agentChat.reconnectTimer = null; }
    };
    agentChat.ws.onclose = function() {
        agentChat.connected = false;
        agentChat.busy = false;
        setChatStatus('disconnected', 'Disconnected');
        document.getElementById('chatSendBtn').disabled = true;
        document.getElementById('chatInput').disabled = true;
        scheduleReconnect();
    };
    agentChat.ws.onerror = function() { setChatStatus('error', 'Connection error'); };
    agentChat.ws.onmessage = function(event) {
        var msg; try { msg = JSON.parse(event.data); } catch(e) { return; }
        handleChatMessage(msg);
    };
}

export function scheduleReconnect() {
    if (agentChat.reconnectTimer) return;
    agentChat.reconnectTimer = setTimeout(function() { agentChat.reconnectTimer = null; connectAgentChat(); }, 5000);
}

export function setChatStatus(state, text) {
    var dot = document.getElementById('chatDot');
    var label = document.getElementById('chatStatusText');
    dot.className = 'agent-chat-dot';
    if (state === 'connected') dot.classList.add('connected');
    else if (state === 'working') dot.classList.add('working');
    label.textContent = text;
}

export function handleChatMessage(msg) {
    var container = document.getElementById('chatMessages');
    switch (msg.type) {
        case 'text':
            if (!agentChat.currentBubble) {
                agentChat.currentBubble = appendChatBubble('assistant', '');
            }
            var bubble = agentChat.currentBubble.querySelector('.chat-bubble');
            // Accumulate raw text on a data attribute, render markdown on each chunk
            var raw = (bubble.getAttribute('data-raw') || '') + msg.content;
            bubble.setAttribute('data-raw', raw);
            bubble.innerHTML = renderMarkdown(raw);
            bubble.classList.add('chat-streaming');
            scrollChatBottom();
            break;
        case 'tool_use':
            // Show tool name as a compact pill (strip mcp__ prefixes)
            var toolName = (msg.name || '').replace(/^mcp__mnemonic__/, '').replace(/^mcp__/, '');
            var toolInput = msg.input || '';
            var pill = document.createElement('div');
            pill.className = 'chat-tool-pill';
            pill.innerHTML = '<span class="tool-icon">\u25B6</span>' + escapeHtml(toolName);
            if (msg.tool_use_id) {
                pill.setAttribute('data-tool-use-id', msg.tool_use_id);
                if (toolInput) {
                    toolDataMap.set(msg.tool_use_id, { input: toolInput, result: null });
                }
                pill.addEventListener('click', toggleToolDetail);
            }
            // Group consecutive tool pills in a flex row
            var lastChild = container.lastElementChild;
            var row;
            if (lastChild && lastChild.classList.contains('chat-tool-row')) {
                row = lastChild;
            } else {
                row = document.createElement('div');
                row.className = 'chat-tool-row';
                container.appendChild(row);
            }
            row.appendChild(pill);
            scrollChatBottom();
            break;
        case 'tool_result':
            // Store result in the Map; mark pill as error if needed
            if (msg.tool_use_id) {
                var existing = toolDataMap.get(msg.tool_use_id);
                if (existing) {
                    existing.result = msg.content || '';
                } else {
                    toolDataMap.set(msg.tool_use_id, { input: null, result: msg.content || '' });
                }
                if (msg.is_error) {
                    var targetPill = container.querySelector('.chat-tool-pill[data-tool-use-id="' + msg.tool_use_id + '"]');
                    if (targetPill) targetPill.classList.add('tool-error');
                }
            }
            break;
        case 'status':
            var labels = { recalling: 'Recalling context...', working: 'Working...', reflecting: 'Reflecting...', ready: 'Ready' };
            var label = labels[msg.status] || msg.status;
            setChatStatus(msg.status === 'ready' ? 'connected' : 'working', label);
            if (msg.status !== 'ready') {
                var sp = document.createElement('div');
                sp.className = 'chat-status-pill';
                sp.textContent = label;
                container.appendChild(sp);
                scrollChatBottom();
            }
            break;
        case 'done':
            if (agentChat.currentBubble) {
                agentChat.currentBubble.querySelector('.chat-bubble').classList.remove('chat-streaming');
                agentChat.currentBubble = null;
            }
            agentChat.busy = false;
            setChatStatus('connected', 'Ready \u2014 $' + (msg.cost_usd || 0).toFixed(4) + ' \u00B7 ' + (msg.turns || 0) + ' turns');
            document.getElementById('chatSendBtn').disabled = false;
            document.getElementById('chatInput').disabled = false;
            document.getElementById('chatInput').focus();
            scrollChatBottom();
            break;
        case 'error':
            agentChat.busy = false;
            agentChat.currentBubble = null;
            var errMsg = msg.message || 'Unknown error';
            var isSetupError = /not authenticated|not logged in|cli not found/i.test(errMsg);
            appendChatBubble('assistant', isSetupError ? errMsg : 'Error: ' + errMsg);
            setChatStatus(isSetupError ? 'disconnected' : 'connected', isSetupError ? 'Setup required' : 'Ready (error)');
            document.getElementById('chatSendBtn').disabled = false;
            document.getElementById('chatInput').disabled = false;
            scrollChatBottom();
            break;
        case 'conversation_started':
            agentChat.currentConvId = msg.id;
            document.getElementById('chatTitle').textContent = 'New conversation';
            if (msg.model) {
                var sel = document.getElementById('chatModelSelect');
                if (sel) sel.value = msg.model;
            }
            break;
        case 'conversations_list':
            renderConversationList(msg.conversations);
            break;
        case 'conversation_loaded':
            displayLoadedConversation(msg.conversation);
            break;
        case 'conversation_deleted':
            // Refresh the list if history panel is open
            var hp = document.getElementById('chatHistoryPanel');
            if (hp && hp.style.display !== 'none' && agentChat.connected) {
                agentChat.ws.send(JSON.stringify({ type: 'list_conversations' }));
            }
            break;
        case 'model_set':
            var msel = document.getElementById('chatModelSelect');
            if (msel) msel.value = msg.model;
            localStorage.setItem('agentModel', msg.model);
            break;
    }
}

export function appendChatBubble(role, text) {
    var container = document.getElementById('chatMessages');
    var div = document.createElement('div');
    div.className = 'chat-msg ' + role;
    var bubble = document.createElement('div');
    bubble.className = 'chat-bubble';
    if (text) {
        bubble.setAttribute('data-raw', text);
        bubble.innerHTML = role === 'user' ? escapeHtml(text) : renderMarkdown(text);
    }
    div.appendChild(bubble);
    container.appendChild(div);
    scrollChatBottom();
    return div;
}

export function scrollChatBottom() {
    var el = document.getElementById('chatMessages');
    var nearBottom = (el.scrollHeight - el.scrollTop - el.clientHeight) < 80;
    if (nearBottom) el.scrollTop = el.scrollHeight;
}

export function sendChatMessage() {
    if (!agentChat.connected || agentChat.busy) return;
    var input = document.getElementById('chatInput');
    var text = input.value.trim();
    if (!text) return;
    appendChatBubble('user', text);
    input.value = '';
    input.style.height = 'auto';
    agentChat.currentBubble = null;
    agentChat.busy = true;
    // Update title from first message
    var titleEl = document.getElementById('chatTitle');
    if (titleEl && titleEl.textContent === 'New conversation') {
        titleEl.textContent = text.length > 50 ? text.substring(0, 50) + '...' : text;
    }
    setChatStatus('working', 'Working...');
    document.getElementById('chatSendBtn').disabled = true;
    document.getElementById('chatInput').disabled = true;
    agentChat.ws.send(JSON.stringify({ type: 'message', content: text }));
}

// ── Conversation Management ──

export function toggleChatHistory() {
    var panel = document.getElementById('chatHistoryPanel');
    if (panel.style.display === 'none') {
        panel.style.display = 'block';
        if (agentChat.connected) {
            agentChat.ws.send(JSON.stringify({ type: 'list_conversations' }));
        }
    } else {
        panel.style.display = 'none';
    }
}

export function renderConversationList(conversations) {
    var list = document.getElementById('chatHistoryList');
    if (!conversations || conversations.length === 0) {
        list.innerHTML = '<div class="chat-history-empty">No conversations yet</div>';
        return;
    }
    list.innerHTML = conversations.map(function(c) {
        var d = new Date(c.updated_at);
        var date = d.toLocaleDateString([], { month: 'short', day: 'numeric' });
        var time = d.toLocaleTimeString([], { hour: '2-digit', minute: '2-digit' });
        var active = c.id === agentChat.currentConvId ? ' active' : '';
        return '<div class="chat-history-item' + active + '" onclick="loadConversation(\'' + c.id + '\')">' +
            '<div class="chat-history-content">' +
                '<div class="chat-history-title">' + escapeHtml(c.title) + '</div>' +
                '<div class="chat-history-meta">' +
                    '<span>' + date + ' ' + time + '</span>' +
                    '<span>' + c.message_count + ' msgs</span>' +
                    (c.total_cost_usd > 0 ? '<span>$' + c.total_cost_usd.toFixed(2) + '</span>' : '') +
                '</div>' +
            '</div>' +
            '<button class="chat-history-delete" onclick="event.stopPropagation();deleteConversation(\'' + c.id + '\')" title="Delete">&times;</button>' +
        '</div>';
    }).join('');
}

export function loadConversation(convId) {
    if (!agentChat.connected || agentChat.busy) return;
    document.getElementById('chatHistoryPanel').style.display = 'none';
    setChatStatus('working', 'Loading...');
    agentChat.ws.send(JSON.stringify({ type: 'load_conversation', id: convId }));
}

export function displayLoadedConversation(conv) {
    var container = document.getElementById('chatMessages');
    container.innerHTML = '';
    toolDataMap.clear();
    agentChat.currentConvId = conv.id;
    agentChat.currentBubble = null;
    document.getElementById('chatTitle').textContent = conv.title || 'Conversation';

    (conv.messages || []).forEach(function(m) {
        if (m.role === 'user') {
            appendChatBubble('user', m.content);
        } else if (m.role === 'assistant') {
            appendChatBubble('assistant', m.content);
        } else if (m.role === 'tool_use') {
            var toolName = (m.name || '').replace(/^mcp__mnemonic__/, '').replace(/^mcp__/, '');
            var toolInput = m.input || '';
            var toolId = m.tool_use_id || ('hist-' + Math.random().toString(36).slice(2, 10));
            var row = document.createElement('div');
            row.className = 'chat-tool-row';
            var pill = document.createElement('div');
            pill.className = 'chat-tool-pill';
            pill.innerHTML = '<span class="tool-icon">\u25B6</span>' + escapeHtml(toolName);
            pill.setAttribute('data-tool-use-id', toolId);
            if (toolInput) {
                toolDataMap.set(toolId, { input: toolInput, result: null });
                pill.addEventListener('click', toggleToolDetail);
            }
            row.appendChild(pill);
            container.appendChild(row);
        }
    });

    // Add continuation separator
    var sep = document.createElement('div');
    sep.className = 'chat-status-pill';
    sep.textContent = 'Continuing conversation\u2026';
    sep.style.borderTop = '1px dashed var(--border-color)';
    sep.style.paddingTop = '8px';
    sep.style.marginTop = '4px';
    container.appendChild(sep);

    scrollChatBottom();
    setChatStatus('connected', 'Ready');
}

export function deleteConversation(convId) {
    if (!agentChat.connected) return;
    agentChat.ws.send(JSON.stringify({ type: 'delete_conversation', id: convId }));
}

export function startNewConversation() {
    if (!agentChat.connected || agentChat.busy) return;
    document.getElementById('chatMessages').innerHTML = '';
    toolDataMap.clear();
    document.getElementById('chatHistoryPanel').style.display = 'none';
    agentChat.currentBubble = null;
    agentChat.ws.send(JSON.stringify({ type: 'new_conversation' }));
}

export function onModelChange(model) {
    if (!agentChat.connected) return;
    localStorage.setItem('agentModel', model);
    agentChat.ws.send(JSON.stringify({ type: 'set_model', model: model }));
}

// ── Self-Update ──
var _updateInfo = null;

export async function checkForUpdate() {
    try {
        var data = await fetchJSON('/system/update-check');
        var badge = document.getElementById('navUpdateBadge');
        var changelog = document.getElementById('navChangelogLink');
        if (data.update_available) {
            _updateInfo = data;
            badge.textContent = 'v' + data.latest_version + ' available';
            badge.style.display = 'inline-block';
            if (data.release_url) {
                changelog.href = data.release_url;
                changelog.style.display = 'inline';
            }
        } else {
            _updateInfo = null;
            badge.style.display = 'none';
            changelog.style.display = 'none';
        }
    } catch (e) {
        // Silently ignore — update check is best-effort
    }
}

export function waitForRestart(expectedVersion) {
    var attempts = 0;
    var maxAttempts = 30;
    var interval = setInterval(async function() {
        attempts++;
        try {
            var resp = await fetch(CONFIG.API_BASE + '/health', {
                headers: CONFIG.TOKEN ? { 'Authorization': 'Bearer ' + CONFIG.TOKEN } : {}
            });
            if (resp.ok) {
                var health = await resp.json();
                if (health.version === expectedVersion) {
                    clearInterval(interval);
                    showToast('Reloading with v' + expectedVersion + '...', 'success');
                    setTimeout(function() { location.reload(); }, 500);
                }
            }
        } catch (_) {
            // daemon still restarting, keep polling
        }
        if (attempts >= maxAttempts) {
            clearInterval(interval);
            showToast('Daemon took too long to restart — refresh manually', 'warning');
        }
    }, 1000);
}

export async function triggerUpdate() {
    if (!_updateInfo || !_updateInfo.update_available) return;
    if (!confirm('Update mnemonic to v' + _updateInfo.latest_version + '?\n\nThe daemon will restart automatically.')) return;

    var badge = document.getElementById('navUpdateBadge');
    badge.textContent = 'Downloading...';
    badge.className = 'update-badge updating';

    try {
        var data = await fetchJSON('/system/update', { method: 'POST' });
        if (data.status === 'updated' && data.restart_pending) {
            badge.textContent = 'Restarting...';
            showToast('Updated to v' + data.new_version + ' — restarting...', 'success');
            waitForRestart(data.new_version);
        } else if (data.status === 'updated' && !data.restart_pending) {
            badge.textContent = 'Restart needed';
            badge.className = 'update-badge';
            showToast(data.message || 'Updated — restart the daemon manually', 'warning');
        } else if (data.status === 'up_to_date') {
            badge.style.display = 'none';
            showToast('Already up to date', 'success');
        }
    } catch (e) {
        badge.textContent = 'Update failed';
        badge.className = 'update-badge';
        showToast('Update failed: ' + e.message, 'error');
        setTimeout(function() { badge.textContent = 'v' + _updateInfo.latest_version + ' available'; }, 3000);
    }
}

