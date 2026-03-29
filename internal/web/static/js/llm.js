import { state, CONFIG } from './state.js';
import { fetchJSON, escapeHtml, svgEl, linScale, svgText, fmtNum, formatBytes } from './utils.js';

// ── LLM Usage ──
var _llmRange = '24h';

export function setLLMRange(range) {
    _llmRange = range;
    document.querySelectorAll('.llm-range-tab').forEach(function(t) {
        t.classList.toggle('active', t.getAttribute('data-range') === range);
    });
    var titles = { '1h': 'Token Usage (1h)', '6h': 'Token Usage (6h)', '24h': 'Token Usage (24h)', '168h': 'Token Usage (7d)' };
    document.getElementById('llmChartTitle').textContent = titles[range] || 'Token Usage';
    loadLLMUsage();
}

export async function loadLLMUsage() {
    try {
        var limit = _llmRange === '168h' ? 500 : 200;
        var data = await fetchJSON('/llm/usage?since=' + _llmRange + '&limit=' + limit);
        state.llmLoaded = true;

        var s = data.summary || {};
        document.getElementById('llmRequests').textContent = (s.total_requests || 0).toLocaleString();
        document.getElementById('llmTokens').textContent = (s.total_tokens || 0).toLocaleString();

        // Token split sub-label + Completion %
        var prompt = s.prompt_tokens || 0;
        var completion = s.completion_tokens || 0;
        var splitEl = document.getElementById('llmTokenSplit');
        splitEl.textContent = (prompt > 0 || completion > 0) ? prompt.toLocaleString() + ' in · ' + completion.toLocaleString() + ' out' : '';
        var total = prompt + completion;
        document.getElementById('llmCompletionPct').textContent = total > 0 ? (completion / total * 100).toFixed(1) + '%' : '-';

        document.getElementById('llmCost').textContent = '$' + (data.estimated_cost_usd || 0).toFixed(4);
        document.getElementById('llmLatency').textContent = (s.avg_latency_ms || 0).toFixed(0) + 'ms';

        var errEl = document.getElementById('llmErrors');
        errEl.textContent = s.error_count || 0;
        errEl.style.color = (s.error_count || 0) > 0 ? 'var(--accent-red)' : '';

        document.getElementById('llmUpdated').textContent = 'Updated ' + new Date().toLocaleTimeString();
        var ntc = document.getElementById('navTokenCount');
        if (ntc) ntc.textContent = (s.total_tokens || 0).toLocaleString();

        var log = data.log || [];

        // Build per-agent extras (cost + avg latency) from log
        var agentExtras = {};
        log.forEach(function(r) {
            var a = r.caller || 'unknown';
            if (!agentExtras[a]) agentExtras[a] = { latencySum: 0, count: 0, cost: 0, errors: 0 };
            agentExtras[a].latencySum += (r.latency_ms || 0);
            agentExtras[a].count++;
            agentExtras[a].cost += (r.estimated_cost_usd || 0);
            if (!r.success) agentExtras[a].errors++;
        });

        // Agent table
        var agentBody = document.getElementById('llmAgentBody');
        var agents = s.by_agent || {};
        var agentKeys = Object.keys(agents).sort(function(a, b) { return (agents[b].total_tokens || 0) - (agents[a].total_tokens || 0); });
        if (agentKeys.length === 0) {
            agentBody.innerHTML = '<tr><td colspan="6" class="llm-empty">No agent data yet</td></tr>';
        } else {
            agentBody.innerHTML = agentKeys.map(function(k) {
                var a = agents[k];
                var ext = agentExtras[k] || {};
                var errors = ext.errors || 0;
                var errHtml = errors > 0 ? '<span style="color:var(--accent-red)">' + errors + '</span>' : '0';
                var cost = ext.cost ? '$' + ext.cost.toFixed(4) : '-';
                var avgLat = ext.count ? (ext.latencySum / ext.count).toFixed(0) + 'ms' : '-';
                return '<tr><td><strong>' + escapeHtml(k) + '</strong></td><td>' + (a.requests || 0) + '</td><td>' + (a.total_tokens || 0).toLocaleString() + '</td><td>' + errHtml + '</td><td>' + cost + '</td><td>' + avgLat + '</td></tr>';
            }).join('');
        }

        // Operations table (computed from log) with p50/p95
        var opStats = {};
        log.forEach(function(r) {
            var op = r.operation || 'unknown';
            if (!opStats[op]) opStats[op] = { count: 0, tokens: 0, latencies: [] };
            opStats[op].count++;
            opStats[op].tokens += (r.prompt_tokens || 0) + (r.completion_tokens || 0);
            opStats[op].latencies.push(r.latency_ms || 0);
        });
        var opsBody = document.getElementById('llmOpsBody');
        var opKeys = Object.keys(opStats).sort(function(a, b) { return opStats[b].count - opStats[a].count; });
        if (opKeys.length === 0) {
            opsBody.innerHTML = '<tr><td colspan="6" class="llm-empty">No data yet</td></tr>';
        } else {
            opsBody.innerHTML = opKeys.map(function(op) {
                var os = opStats[op];
                var sorted = os.latencies.slice().sort(function(a, b) { return a - b; });
                var p50 = sorted[Math.floor(sorted.length * 0.5)] || 0;
                var p95 = sorted[Math.floor(sorted.length * 0.95)] || 0;
                return '<tr><td>' + escapeHtml(op) + '</td><td>' + os.count + '</td><td>' + os.tokens.toLocaleString() + '</td><td>' + p50 + 'ms</td><td>' + p95 + 'ms</td></tr>';
            }).join('');
        }

        // Request log
        var logBody = document.getElementById('llmLogBody');
        if (log.length === 0) {
            logBody.innerHTML = '<tr><td colspan="7" class="llm-empty">No requests recorded yet</td></tr>';
        } else {
            logBody.innerHTML = log.map(function(r) {
                var t = r.timestamp ? new Date(r.timestamp).toLocaleTimeString() : '-';
                var statusCls = r.success ? 'llm-status-ok' : 'llm-status-err';
                var statusTxt = r.success ? 'OK' : 'ERR';
                var rowCls = r.success ? '' : ' class="llm-log-row-err"';
                var inOut = (r.prompt_tokens || 0).toLocaleString() + ' / ' + (r.completion_tokens || 0).toLocaleString();
                var model = escapeHtml((r.model || '-').replace('local-model', 'local'));
                var row = '<tr' + rowCls + '><td>' + t + '</td><td>' + escapeHtml(r.caller || '-') + '</td><td>' + escapeHtml(r.operation || '-') + '</td><td>' + model + '</td><td>' + inOut + '</td><td>' + (r.latency_ms || 0) + 'ms</td><td class="' + statusCls + '">' + statusTxt + '</td></tr>';
                if (!r.success && r.error_message) {
                    row += '<tr class="llm-log-row-err"><td colspan="7" class="llm-error-detail">' + escapeHtml(r.error_message) + '</td></tr>';
                }
                return row;
            }).join('');
        }

        renderLLMChart(data.chart_buckets || [], _llmRange);
    } catch (e) {
        console.error('loadLLMUsage error:', e);
        var el = document.getElementById('llmRequests');
        if (el) el.textContent = 'ERR';
    }
}

export function renderLLMChart(chartBuckets, range) {
    var container = document.getElementById('llmChart');
    var tooltip = document.getElementById('llmChartTooltip');
    container.innerHTML = '';
    if (!chartBuckets || chartBuckets.length === 0) { container.innerHTML = '<div class="llm-empty">No data for chart</div>'; return; }

    // Determine label format and frequency from range
    var labelFmt, labelEvery;
    if (range === '1h') {
        labelFmt = function(d) { return String(d.getHours()).padStart(2,'0') + ':' + String(d.getMinutes()).padStart(2,'0'); };
        labelEvery = 3;
    } else if (range === '6h') {
        labelFmt = function(d) { return String(d.getHours()).padStart(2,'0') + ':' + String(d.getMinutes()).padStart(2,'0'); };
        labelEvery = 2;
    } else if (range === '168h') {
        var days = ['Sun','Mon','Tue','Wed','Thu','Fri','Sat'];
        labelFmt = function(d) { return days[d.getDay()]; };
        labelEvery = 1;
    } else { // 24h default
        labelFmt = function(d) { return String(d.getHours()).padStart(2,'0') + ':00'; };
        labelEvery = 4;
    }

    // Map server-aggregated buckets to chart data
    var buckets = chartBuckets.map(function(b, i) {
        var ts = new Date(b.timestamp);
        return {
            idx: i,
            start: ts,
            label: labelFmt(ts),
            prompt: b.prompt_tokens || 0,
            completion: b.completion_tokens || 0,
            requests: b.requests || 0,
            errors: b.errors || 0
        };
    });

    var w = container.clientWidth || 400;
    var h = 200;
    var margin = { top: 10, right: 10, bottom: 28, left: 50 };
    var iw = w - margin.left - margin.right;
    var ih = h - margin.top - margin.bottom;

    var svg = svgEl('svg', { width: w, height: h });
    container.appendChild(svg);
    var g = svgEl('g', { transform: 'translate(' + margin.left + ',' + margin.top + ')' });
    svg.appendChild(g);

    var maxTotal = Math.max.apply(null, buckets.map(function(d) { return d.prompt + d.completion; })) || 1;
    var nb = buckets.length;
    var bandStep = iw / nb;
    var bandPad = 0.25;
    var bandW = bandStep * (1 - bandPad);
    var bandOff = bandStep * bandPad / 2;

    // Y axis ticks (format: .2s — 1000->1k, 1000000->1M)
    var yTicks = 4;
    var yStep = maxTotal / yTicks;
    for (var t = 0; t <= yTicks; t++) {
        var tv = t * yStep;
        var ty = linScale(tv, 0, maxTotal, ih, 0);
        g.appendChild(svgEl('line', { x1: -4, x2: 0, y1: ty, y2: ty, stroke: 'var(--border-subtle)' }));
        g.appendChild(svgText(-8, ty, fmtNum(Math.round(tv)), { 'text-anchor': 'end', 'dominant-baseline': '0.32em', 'font-size': '10' }));
    }
    // X axis baseline
    g.appendChild(svgEl('line', { x1: 0, x2: iw, y1: ih, y2: ih, stroke: 'var(--border-subtle)' }));
    // X axis labels
    buckets.forEach(function(d) {
        if (d.idx % labelEvery !== 0) return;
        var bx = d.idx * bandStep + bandStep / 2;
        g.appendChild(svgEl('line', { x1: bx, x2: bx, y1: ih, y2: ih + 4, stroke: 'var(--border-subtle)' }));
        g.appendChild(svgText(bx, ih + 14, d.label, { 'text-anchor': 'middle', 'font-size': '10' }));
    });

    // Bars + hit areas + error markers
    buckets.forEach(function(d) {
        var bx = d.idx * bandStep + bandOff;
        var total = d.prompt + d.completion;
        // Full bar (prompt+completion, cyan)
        if (total > 0) {
            var fullY = linScale(total, 0, maxTotal, ih, 0);
            g.appendChild(svgEl('rect', { x: bx, y: fullY, width: bandW, height: ih - fullY, rx: 2, fill: 'var(--accent-cyan)', opacity: '0.8' }));
        }
        // Completion overlay (violet, bottom portion)
        if (d.completion > 0) {
            var compY = linScale(d.completion, 0, maxTotal, ih, 0);
            g.appendChild(svgEl('rect', { x: bx, y: compY, width: bandW, height: ih - compY, fill: 'var(--accent-violet)', opacity: '0.8' }));
        }
        // Hit area
        var hit = svgEl('rect', { x: bx - bandW * 0.1, y: 0, width: bandW * 1.2, height: ih, fill: 'transparent' });
        (function(dd) {
            hit.addEventListener('mouseover', function() {
                if (dd.requests === 0) return;
                var errLine = dd.errors > 0 ? '<br><span style="color:var(--accent-red)">' + dd.errors + ' error' + (dd.errors === 1 ? '' : 's') + '</span>' : '';
                tooltip.innerHTML = '<strong>' + dd.label + '</strong><br>' +
                    dd.requests + ' request' + (dd.requests === 1 ? '' : 's') + '<br>' +
                    '<span style="color:var(--accent-cyan)">' + dd.prompt.toLocaleString() + '</span> prompt \u00B7 ' +
                    '<span style="color:var(--accent-violet)">' + dd.completion.toLocaleString() + '</span> completion' + errLine;
                tooltip.style.display = 'block';
                var barX = margin.left + dd.idx * bandStep + bandStep / 2;
                var ttLeft = barX + 10;
                if (ttLeft + 180 > w) ttLeft = barX - 190;
                tooltip.style.left = ttLeft + 'px';
                tooltip.style.top = (margin.top + 2) + 'px';
            });
            hit.addEventListener('mouseout', function() { tooltip.style.display = 'none'; });
        })(d);
        g.appendChild(hit);

        // Error marker
        if (d.errors > 0) {
            var ey = linScale(total, 0, maxTotal, ih, 0) - 5;
            g.appendChild(svgEl('circle', { cx: d.idx * bandStep + bandStep / 2, cy: ey, r: 3, fill: 'var(--accent-red)', 'pointer-events': 'none' }));
        }
    });
}

// ── Tool Usage Analytics ──
var _toolRange = '24h';
var _toolLog = [];
var _analyticsRange = 14;

export function setAnalyticsRange(days) {
    _analyticsRange = days;
    document.querySelectorAll('#analyticsRangeTabs .llm-range-tab').forEach(function(t) {
        t.classList.toggle('active', t.getAttribute('data-range') === String(days));
    });
    document.getElementById('lifecycleTitle').textContent = 'Memory Lifecycle (' + days + 'd)';
    loadAnalytics();
    loadSessionTimeline();
}

export function setToolRange(range) {
    _toolRange = range;
    document.querySelectorAll('#toolRangeTabs .llm-range-tab').forEach(function(t) {
        t.classList.toggle('active', t.getAttribute('data-range') === range);
    });
    var titles = { '1h': 'Tool Calls (1h)', '6h': 'Tool Calls (6h)', '24h': 'Tool Calls (24h)', '168h': 'Tool Calls (7d)' };
    document.getElementById('toolChartTitle').textContent = titles[range] || 'Tool Calls';
    loadToolUsage();
}

export async function loadToolUsage() {
    try {
        var limit = _toolRange === '168h' ? 500 : 200;
        var data = await fetchJSON('/tool/usage?since=' + _toolRange + '&limit=' + limit);
        state.toolsLoaded = true;

        var s = data.summary || {};
        document.getElementById('toolCalls').textContent = (s.total_calls || 0).toLocaleString();
        document.getElementById('toolLatency').textContent = (s.avg_latency_ms || 0).toFixed(0) + 'ms';

        var errEl = document.getElementById('toolErrors');
        errEl.textContent = s.error_count || 0;
        errEl.style.color = (s.error_count || 0) > 0 ? 'var(--accent-red)' : '';

        // Top tool
        var byTool = s.by_tool || {};
        var topTool = Object.keys(byTool).sort(function(a, b) { return byTool[b] - byTool[a]; })[0] || '-';
        document.getElementById('toolTopTool').textContent = topTool;

        // Project count
        var byProject = s.by_project || {};
        document.getElementById('toolProjects').textContent = Object.keys(byProject).length || 0;

        // Success rate
        var total = s.total_calls || 0;
        var errors = s.error_count || 0;
        document.getElementById('toolSuccessRate').textContent = total > 0 ? ((total - errors) / total * 100).toFixed(1) + '%' : '-';

        document.getElementById('toolUpdated').textContent = 'Updated ' + new Date().toLocaleTimeString();

        var log = data.log || [];
        _toolLog = log; // expose for session enrichment

        // Per-tool extras from log (avg latency, avg size)
        var toolExtras = {};
        log.forEach(function(r) {
            var t = r.tool_name || 'unknown';
            if (!toolExtras[t]) toolExtras[t] = { latencySum: 0, sizeSum: 0, count: 0 };
            toolExtras[t].latencySum += (r.latency_ms || 0);
            toolExtras[t].sizeSum += (r.response_size || 0);
            toolExtras[t].count++;
        });

        // By Tool table with percentiles
        var toolLatencies = {};
        log.forEach(function(r) {
            var t = r.tool_name || 'unknown';
            if (!toolLatencies[t]) toolLatencies[t] = [];
            toolLatencies[t].push(r.latency_ms || 0);
        });
        function percentile(arr, p) {
            var sorted = arr.slice().sort(function(a, b) { return a - b; });
            var idx = Math.ceil(sorted.length * p) - 1;
            return sorted[Math.max(0, idx)] || 0;
        }
        var toolBody = document.getElementById('toolByToolBody');
        var toolKeys = Object.keys(byTool).sort(function(a, b) { return byTool[b] - byTool[a]; });
        if (toolKeys.length === 0) {
            toolBody.innerHTML = '<tr><td colspan="6" class="llm-empty">No tool data yet</td></tr>';
        } else {
            toolBody.innerHTML = toolKeys.map(function(k) {
                var lats = toolLatencies[k] || [];
                var p50 = lats.length ? percentile(lats, 0.5).toFixed(0) + 'ms' : '-';
                var p95 = lats.length ? percentile(lats, 0.95).toFixed(0) + 'ms' : '-';
                var mx = lats.length ? Math.max.apply(null, lats).toFixed(0) + 'ms' : '-';
                var p95Val = lats.length ? percentile(lats, 0.95) : 0;
                var p95Style = p95Val > 2000 ? ' style="color:var(--accent-red)"' : p95Val > 500 ? ' style="color:var(--accent-yellow)"' : '';
                var ext = toolExtras[k] || {};
                var avgSize = ext.count ? formatBytes(ext.sizeSum / ext.count) : '-';
                return '<tr><td><strong>' + escapeHtml(k) + '</strong></td><td>' + byTool[k] + '</td><td>' + p50 + '</td><td' + p95Style + '>' + p95 + '</td><td>' + mx + '</td><td>' + avgSize + '</td></tr>';
            }).join('');
        }

        // Session table
        var sessionData = {};
        log.forEach(function(r) {
            var sid = r.session_id || 'unknown';
            if (!sessionData[sid]) sessionData[sid] = { calls: 0, tools: {}, first: r.timestamp, last: r.timestamp };
            sessionData[sid].calls++;
            sessionData[sid].tools[r.tool_name] = true;
            if (r.timestamp < sessionData[sid].first) sessionData[sid].first = r.timestamp;
            if (r.timestamp > sessionData[sid].last) sessionData[sid].last = r.timestamp;
        });
        var sessionBody = document.getElementById('toolSessionBody');
        var sessionKeys = Object.keys(sessionData).sort(function(a, b) { return sessionData[b].calls - sessionData[a].calls; });
        if (sessionKeys.length === 0) {
            sessionBody.innerHTML = '<tr><td colspan="4" class="llm-empty">No session data</td></tr>';
        } else {
            sessionBody.innerHTML = sessionKeys.slice(0, 10).map(function(sid) {
                var s = sessionData[sid];
                var toolCount = Object.keys(s.tools).length;
                var dur = '-';
                try {
                    var ms = new Date(s.last) - new Date(s.first);
                    if (ms > 60000) dur = Math.round(ms / 60000) + 'm';
                    else if (ms > 0) dur = Math.round(ms / 1000) + 's';
                    else dur = '<1s';
                } catch(e) {}
                return '<tr><td><strong>' + escapeHtml(sid) + '</strong></td><td>' + s.calls + '</td><td>' + toolCount + '</td><td>' + dur + '</td></tr>';
            }).join('');
        }

        // By Project table
        var projBody = document.getElementById('toolByProjectBody');
        var projKeys = Object.keys(byProject).sort(function(a, b) { return byProject[b] - byProject[a]; });
        if (projKeys.length === 0) {
            projBody.innerHTML = '<tr><td colspan="2" class="llm-empty">No project data yet</td></tr>';
        } else {
            projBody.innerHTML = projKeys.map(function(k) {
                return '<tr><td><strong>' + escapeHtml(k) + '</strong></td><td>' + byProject[k] + '</td></tr>';
            }).join('');
        }

        // Request log
        var logBody = document.getElementById('toolLogBody');
        if (log.length === 0) {
            logBody.innerHTML = '<tr><td colspan="7" class="llm-empty">No tool calls recorded yet</td></tr>';
        } else {
            logBody.innerHTML = log.map(function(r) {
                var t = r.timestamp ? new Date(r.timestamp).toLocaleTimeString() : '-';
                var statusCls = r.success ? 'llm-status-ok' : 'llm-status-err';
                var statusTxt = r.success ? 'OK' : 'ERR';
                var rowCls = r.success ? '' : ' class="llm-log-row-err"';
                var context = r.query_text || r.memory_type || r.rating || '-';
                if (context.length > 60) context = context.substring(0, 57) + '...';
                var size = r.response_size ? formatBytes(r.response_size) : '-';
                var row = '<tr' + rowCls + '><td>' + t + '</td><td>' + escapeHtml(r.tool_name || '-') + '</td><td>' + escapeHtml(r.project || '-') + '</td><td>' + escapeHtml(context) + '</td><td>' + (r.latency_ms || 0) + 'ms</td><td>' + size + '</td><td class="' + statusCls + '">' + statusTxt + '</td></tr>';
                if (!r.success && r.error_message) {
                    row += '<tr class="llm-log-row-err"><td colspan="7" class="llm-error-detail">' + escapeHtml(r.error_message) + '</td></tr>';
                }
                return row;
            }).join('');
        }

        // Memory type distribution (from remember calls)
        var memTypes = {};
        log.forEach(function(r) {
            if (r.tool_name === 'remember' && r.memory_type) {
                memTypes[r.memory_type] = (memTypes[r.memory_type] || 0) + 1;
            }
        });
        var memTypeEl = document.getElementById('memTypeChart');
        var typeColors = { decision: 'var(--accent-cyan)', error: 'var(--accent-red)', insight: 'var(--accent-purple)', learning: 'var(--accent-green)', general: 'var(--text-muted)' };
        var typeKeys = Object.keys(memTypes).sort(function(a, b) { return memTypes[b] - memTypes[a]; });
        if (typeKeys.length === 0) {
            memTypeEl.innerHTML = '<span class="llm-empty" style="font-size:0.75rem">No memories stored yet</span>';
        } else {
            var typeTotal = typeKeys.reduce(function(s, k) { return s + memTypes[k]; }, 0);
            memTypeEl.innerHTML = typeKeys.map(function(k) {
                var pct = (memTypes[k] / typeTotal * 100).toFixed(0);
                var color = typeColors[k] || 'var(--text-muted)';
                return '<div style="flex:1;min-width:80px;text-align:center;padding:10px 6px;background:var(--bg-surface);border-radius:var(--radius-sm);border-radius:var(--radius-sm)">' +
                    '<div style="font-size:1.1rem;font-weight:600;color:' + color + '">' + memTypes[k] + '</div>' +
                    '<div style="font-size:0.7rem;color:var(--text-muted)">' + k + ' (' + pct + '%)</div></div>';
            }).join('');
        }

        // Feedback quality breakdown
        var ratings = { helpful: 0, partial: 0, irrelevant: 0 };
        log.forEach(function(r) {
            if (r.tool_name === 'feedback' && r.rating) {
                ratings[r.rating] = (ratings[r.rating] || 0) + 1;
            }
        });
        var feedbackEl = document.getElementById('feedbackChart');
        var ratingColors = { helpful: 'var(--accent-green)', partial: 'var(--accent-yellow)', irrelevant: 'var(--accent-red)' };
        var ratingTotal = ratings.helpful + ratings.partial + ratings.irrelevant;
        if (ratingTotal === 0) {
            feedbackEl.innerHTML = '<span class="llm-empty" style="font-size:0.75rem">No feedback recorded yet</span>';
        } else {
            feedbackEl.innerHTML = ['helpful', 'partial', 'irrelevant'].map(function(k) {
                var pct = (ratings[k] / ratingTotal * 100).toFixed(0);
                var color = ratingColors[k];
                return '<div style="flex:1;min-width:80px;text-align:center;padding:10px 6px;background:var(--bg-surface);border-radius:var(--radius-sm);border-radius:var(--radius-sm)">' +
                    '<div style="font-size:1.1rem;font-weight:600;color:' + color + '">' + ratings[k] + '</div>' +
                    '<div style="font-size:0.7rem;color:var(--text-muted)">' + k + ' (' + pct + '%)</div></div>';
            }).join('');
        }

        renderToolChart(data.chart_buckets || [], _toolRange);
        loadAnalytics();
        loadSessionTimeline();
    } catch (e) {
        document.getElementById('toolCalls').textContent = 'ERR';
    }
}

// ── Research Analytics ──

var _raSourceColors = { mcp: 'var(--accent-green)', filesystem: 'var(--accent-cyan)', terminal: 'var(--accent-yellow)', git: 'var(--accent-violet)', clipboard: 'var(--text-muted)', ingest: 'var(--accent-green)', benchmark: 'var(--accent-red)', system: 'var(--text-muted)' };

export function _thresholdColor(value, greenAbove, yellowAbove) {
    if (value >= greenAbove) return 'var(--accent-green)';
    if (value >= yellowAbove) return 'var(--accent-yellow)';
    return 'var(--accent-red)';
}

export function _renderSparkline(container, data, color) {
    if (!data || data.length < 2) return;
    var w = 64, h = 24;
    var svg = svgEl('svg', { width: w, height: h });
    container.appendChild(svg);
    var dMin = Math.min.apply(null, data), dMax = Math.max.apply(null, data);
    if (dMin === dMax) { dMin -= 1; dMax += 1; }
    // Build points
    var pts = [];
    for (var i = 0; i < data.length; i++) {
        pts.push(linScale(i, 0, data.length - 1, 2, w - 2) + ',' + linScale(data[i], dMin, dMax, h - 2, 2));
    }
    // Area polygon (top line + bottom edge)
    var bottomPts = [];
    for (var j = data.length - 1; j >= 0; j--) {
        bottomPts.push(linScale(j, 0, data.length - 1, 2, w - 2) + ',' + h);
    }
    svg.appendChild(svgEl('polygon', { points: pts.join(' ') + ' ' + bottomPts.join(' '), fill: color, opacity: '0.1' }));
    // Line
    svg.appendChild(svgEl('polyline', { points: pts.join(' '), fill: 'none', stroke: color, 'stroke-width': '1.5' }));
    // End dot
    var lastX = linScale(data.length - 1, 0, data.length - 1, 2, w - 2);
    var lastY = linScale(data[data.length - 1], dMin, dMax, h - 2, 2);
    svg.appendChild(svgEl('circle', { cx: lastX, cy: lastY, r: '2.5', fill: color }));
}

export function _computeDelta(data) {
    if (!data || data.length < 2) return { text: '', cls: 'neutral' };
    var recent = data.slice(-Math.min(3, data.length));
    var older = data.slice(0, Math.max(1, data.length - 3));
    var avgRecent = recent.reduce(function(a, b) { return a + b; }, 0) / recent.length;
    var avgOlder = older.reduce(function(a, b) { return a + b; }, 0) / older.length;
    var delta = avgRecent - avgOlder;
    if (Math.abs(delta) < 0.5) return { text: 'stable', cls: 'neutral' };
    var sign = delta > 0 ? '+' : '';
    return { text: sign + delta.toFixed(1), cls: delta > 0 ? 'positive' : 'negative' };
}

export async function loadAnalytics() {
    try {
        var data = await fetchJSON('/analytics');
        var p = data.pipeline || {};
        var sn = data.signal_noise || {};
        var re = data.recall_effectiveness || [];
        var fb = data.feedback_trend || [];
        var sv = data.memory_survival || [];
        var ch = data.consolidation_history || [];
        var days = _analyticsRange;

        // Slice data to selected range
        var svData = sv.slice().reverse().slice(-days);
        var fbData = fb.slice().reverse().slice(-days);
        var chData = ch.slice().reverse().slice(-days);

        // ── KPI Cards ──
        var kpiContainer = document.getElementById('raKpis');

        // Pipeline Health: encoding rate
        var encodingRate = (p.encoding_rate || 0) * 100;
        var pipelineSpark = svData.map(function(d) {
            return d.created > 0 ? (d.active / d.created) * 100 : 0;
        });
        var pipelineDelta = _computeDelta(pipelineSpark);

        // MCP Survival
        var mcpSurv = ((sn.mcp || {}).survival_rate || 0) * 100;

        // Recall Learning
        var neverBucket = re.find(function(b) { return b.bucket.indexOf('never') >= 0; });
        var highBucket = re.find(function(b) { return b.bucket.indexOf('6+') >= 0; });
        var learningRatio = (neverBucket && highBucket && neverBucket.avg_salience > 0)
            ? highBucket.avg_salience / neverBucket.avg_salience : 0;

        // Recall Quality
        var qualitySpark = fbData.map(function(d) {
            var total = d.helpful + d.partial + d.irrelevant;
            return total > 0 ? (d.helpful / total) * 100 : 0;
        });
        var totalFb = fbData.reduce(function(a, d) { return a + d.helpful + d.partial + d.irrelevant; }, 0);
        var totalHelpful = fbData.reduce(function(a, d) { return a + d.helpful; }, 0);
        var qualityPct = totalFb > 0 ? (totalHelpful / totalFb) * 100 : 0;
        var qualityDelta = _computeDelta(qualitySpark);

        kpiContainer.innerHTML = '<div class="ra-kpi" id="kpiPipeline"></div>' +
            '<div class="ra-kpi" id="kpiMcp"></div>' +
            '<div class="ra-kpi" id="kpiLearning"></div>' +
            '<div class="ra-kpi" id="kpiQuality"></div>' +
            '<div class="ra-kpi" id="kpiDensity"></div>' +
            '<div class="ra-kpi" id="kpiRetrieval"></div>';

        // Pipeline Health card
        var el1 = document.getElementById('kpiPipeline');
        el1.innerHTML = '<div class="ra-kpi-label">Pipeline Health</div>' +
            '<div class="ra-kpi-row"><span class="ra-kpi-value" style="color:' + _thresholdColor(encodingRate, 50, 30) + '">' + encodingRate.toFixed(0) + '%</span><span id="sparkPipeline"></span></div>' +
            '<div class="ra-kpi-footer"><span class="ra-kpi-delta ' + pipelineDelta.cls + '">' + pipelineDelta.text + '</span><span class="ra-kpi-target">target &gt;50%</span></div>';
        _renderSparkline(document.getElementById('sparkPipeline'), pipelineSpark, _thresholdColor(encodingRate, 50, 30));

        // MCP Survival card
        var el2 = document.getElementById('kpiMcp');
        el2.innerHTML = '<div class="ra-kpi-label">MCP Survival</div>' +
            '<div class="ra-kpi-row"><span class="ra-kpi-value" style="color:' + _thresholdColor(mcpSurv, 80, 60) + '">' + mcpSurv.toFixed(0) + '%</span></div>' +
            '<div class="ra-kpi-footer"><span class="ra-kpi-delta neutral">' + ((sn.mcp || {}).active || 0) + ' / ' + ((sn.mcp || {}).total || 0) + ' active</span><span class="ra-kpi-target">target &gt;80%</span></div>';

        // Recall Learning card
        var el3 = document.getElementById('kpiLearning');
        var lColor = _thresholdColor(learningRatio, 1.5, 1.0);
        el3.innerHTML = '<div class="ra-kpi-label">Recall Learning</div>' +
            '<div class="ra-kpi-row"><span class="ra-kpi-value" style="color:' + lColor + '">' + (learningRatio > 0 ? learningRatio.toFixed(1) + 'x' : '-') + '</span></div>' +
            '<div class="ra-kpi-footer"><span class="ra-kpi-delta neutral">6+ vs never recalled</span><span class="ra-kpi-target">target &gt;1.5x</span></div>';

        // Recall Quality card
        var el4 = document.getElementById('kpiQuality');
        el4.innerHTML = '<div class="ra-kpi-label">Recall Quality</div>' +
            '<div class="ra-kpi-row"><span class="ra-kpi-value" style="color:' + _thresholdColor(qualityPct, 70, 50) + '">' + (totalFb > 0 ? qualityPct.toFixed(0) + '%' : '-') + '</span><span id="sparkQuality"></span></div>' +
            '<div class="ra-kpi-footer"><span class="ra-kpi-delta ' + qualityDelta.cls + '">' + qualityDelta.text + '</span><span class="ra-kpi-target">target &gt;70%</span></div>';
        _renderSparkline(document.getElementById('sparkQuality'), qualitySpark, _thresholdColor(qualityPct, 70, 50));

        // Network Density KPI (from /api/v1/stats, already loaded at init)
        var el5 = document.getElementById('kpiDensity');
        try {
            var statsResp = await fetchJSON('/stats');
            var avgAssoc = (statsResp.store || {}).avg_associations_per_memory || 0;
            el5.innerHTML = '<div class="ra-kpi-label">Network Density</div>' +
                '<div class="ra-kpi-row"><span class="ra-kpi-value" style="color:' + _thresholdColor(avgAssoc, 2.0, 1.0) + '">' + avgAssoc.toFixed(1) + '</span></div>' +
                '<div class="ra-kpi-footer"><span class="ra-kpi-delta neutral">' + ((statsResp.store || {}).total_associations || 0) + ' associations</span><span class="ra-kpi-target">target &gt;2.0 avg</span></div>';
        } catch(e) { el5.innerHTML = '<div class="ra-kpi-label">Network Density</div><div class="ra-kpi-row"><span class="ra-kpi-value">-</span></div>'; }

        // Retrieval Performance KPI (from new endpoint)
        var el6 = document.getElementById('kpiRetrieval');
        try {
            var retStats = await fetchJSON('/retrieval/stats');
            var avgPerQuery = retStats.avg_memories_per_query || 0;
            var totalQ = retStats.total_queries || 0;
            var avgMs = retStats.avg_synthesis_ms || 0;
            el6.innerHTML = '<div class="ra-kpi-label">Retrieval Perf</div>' +
                '<div class="ra-kpi-row"><span class="ra-kpi-value" style="color:' + _thresholdColor(avgPerQuery, 3, 1) + '">' + (totalQ > 0 ? avgPerQuery.toFixed(1) : '-') + '</span></div>' +
                '<div class="ra-kpi-footer"><span class="ra-kpi-delta neutral">' + totalQ + ' queries' + (avgMs > 0 ? ' \u00b7 ' + avgMs.toFixed(0) + 'ms avg' : '') + '</span><span class="ra-kpi-target">target &gt;3 per query</span></div>';
        } catch(e) { el6.innerHTML = '<div class="ra-kpi-label">Retrieval Perf</div><div class="ra-kpi-row"><span class="ra-kpi-value">-</span></div>'; }

        // ── Cognitive Agents Panel ──
        try {
            var absResp = await fetchJSON('/abstractions?limit=500');
            var abstractions = absResp.abstractions || absResp || [];
            if (!Array.isArray(abstractions)) abstractions = [];
            var principles = abstractions.filter(function(a) { return a.level === 2; }).length;
            var axioms = abstractions.filter(function(a) { return a.level === 3; }).length;

            var chTotals = (ch || []).reduce(function(acc, c) {
                acc.cycles++; acc.processed += (c.processed || 0); acc.merged += (c.merged || 0); acc.decayed += (c.decayed || 0);
                return acc;
            }, { cycles: 0, processed: 0, merged: 0, decayed: 0 });

            var ds = state.dreamSessionTotals;
            var cogGrid = document.getElementById('cognitiveGrid');
            cogGrid.innerHTML =
                '<div class="cognitive-card"><div class="cognitive-card-label">Encoding</div><div class="cognitive-card-value">' + (p.total_encoded || 0) + '</div><div class="cognitive-card-sub">' + (encodingRate > 0 ? encodingRate.toFixed(0) + '% rate' : 'no data') + '</div></div>' +
                '<div class="cognitive-card"><div class="cognitive-card-label">Consolidation</div><div class="cognitive-card-value">' + chTotals.cycles + '</div><div class="cognitive-card-sub">' + chTotals.merged + ' merged \u00b7 ' + chTotals.decayed + ' decayed</div></div>' +
                '<div class="cognitive-card"><div class="cognitive-card-label">Dreaming</div><div class="cognitive-card-value">' + ds.cycles + '</div><div class="cognitive-card-sub">' + ds.insights + ' insights \u00b7 ' + ds.newLinks + ' new links</div></div>' +
                '<div class="cognitive-card"><div class="cognitive-card-label">Abstraction</div><div class="cognitive-card-value">' + principles + '</div><div class="cognitive-card-sub">' + axioms + ' axioms \u00b7 ' + (principles + axioms) + ' total</div></div>';
        } catch(e) { console.error('Cognitive panel load failed:', e); }

        // ── System Analysis ──
        try {
            var briefEl = document.getElementById('raBrief');
            var statsResp2 = await fetchJSON('/stats');
            var st = statsResp2.store || {};
            var sessResp = await fetchJSON('/sessions?days=' + days + '&limit=100');
            var sessCount = sessResp.count || 0;
            var avgAssoc = st.avg_associations_per_memory || 0;

            var lines = [];

            // Overall health sentence
            var healthIssues = 0;
            if (encodingRate < 50) healthIssues++;
            if (mcpSurv < 60) healthIssues++;
            if (qualityPct < 50 && totalFb > 5) healthIssues++;
            if (learningRatio < 1.0 && learningRatio > 0) healthIssues++;

            if (healthIssues === 0) {
                lines.push('Mnemonic is performing well. <strong>' + (st.total_memories || 0) + ' memories</strong> across <strong>' + sessCount + ' sessions</strong>, with a well-connected network (' + avgAssoc.toFixed(1) + ' associations per memory).');
            } else if (healthIssues <= 2) {
                lines.push('Mnemonic is running but has <span class="ra-warn">areas that need attention</span>. <strong>' + (st.total_memories || 0) + ' memories</strong> across <strong>' + sessCount + ' sessions</strong>.');
            } else {
                lines.push('Mnemonic has <span class="ra-bad">several metrics below target</span>. <strong>' + (st.total_memories || 0) + ' memories</strong> across <strong>' + sessCount + ' sessions</strong>. This is expected early on \u2014 the system improves with use.');
            }

            // Encoding pipeline insight
            if (encodingRate >= 80) {
                lines.push('The encoding pipeline is converting ' + encodingRate.toFixed(0) + '% of observations into memories \u2014 minimal signal loss.');
            } else if (encodingRate >= 50) {
                lines.push('The encoding pipeline is at ' + encodingRate.toFixed(0) + '%. Some observations are being filtered or failing to encode.');
            } else if (encodingRate > 0) {
                lines.push('<span class="ra-bad">Encoding is struggling at ' + encodingRate.toFixed(0) + '%.</span> Check LLM availability \u2014 most observations are failing to encode.');
            }

            // MCP survival insight
            if (mcpSurv > 0 && mcpSurv < 60) {
                var mcpActive = (sn.mcp || {}).active || 0;
                var mcpTotal = (sn.mcp || {}).total || 0;
                lines.push('Only <span class="ra-warn">' + mcpSurv.toFixed(0) + '% of MCP memories survive</span> (' + mcpActive + '/' + mcpTotal + ' active). Older memories naturally decay over time \u2014 this ratio improves as you build fresh, high-quality memories through active use.');
            } else if (mcpSurv >= 60 && mcpSurv < 80) {
                lines.push('MCP survival is ' + mcpSurv.toFixed(0) + '% \u2014 some older memories have been pruned, which is healthy.');
            }

            // Recall quality insight
            if (totalFb > 5) {
                if (qualityPct >= 70) {
                    lines.push('Recall quality is <span class="ra-good">strong at ' + qualityPct.toFixed(0) + '%</span> \u2014 the system is returning useful memories most of the time.');
                } else if (qualityDelta.cls === 'positive') {
                    lines.push('Recall quality is at ' + qualityPct.toFixed(0) + '% but <span class="ra-good">trending up (' + qualityDelta.text + ')</span>. The feedback loop is working \u2014 keep rating recalls.');
                } else {
                    lines.push('Recall quality is <span class="ra-warn">below target at ' + qualityPct.toFixed(0) + '%</span>. More feedback will help the system learn which memories matter.');
                }
            } else {
                lines.push('Not enough recall feedback yet to assess quality. Rate your recalls (helpful/partial/irrelevant) to train the system.');
            }

            // Learning signal
            if (learningRatio > 1.5) {
                lines.push('Frequently recalled memories have <span class="ra-good">' + learningRatio.toFixed(1) + 'x higher salience</span> than unused ones \u2014 the system is learning what matters.');
            } else if (learningRatio > 0 && learningRatio < 1.0) {
                lines.push('Frequently recalled memories don\u2019t yet have higher salience than unused ones. This is normal early on \u2014 the recall learning signal strengthens over time with more feedback.');
            }

            // Abstraction formation
            if (principles > 0) {
                var axiomText = axioms > 0 ? ' and <strong>' + axioms + ' axiom' + (axioms !== 1 ? 's' : '') + '</strong>' : '';
                lines.push('The abstraction agent has synthesized <strong>' + principles + ' principle' + (principles !== 1 ? 's' : '') + '</strong>' + axiomText + ' from your memory network \u2014 higher-order patterns that inform future recall.');
            }

            briefEl.innerHTML = lines.join(' ');
        } catch(e) { /* analysis is optional, fail silently */ }

        // ── Memory Lifecycle Chart (D3 stacked area) ──
        try { renderLifecycleChart(svData, fbData, chData); } catch(e) { console.error('Lifecycle chart error:', e); }

        // ── Signal Quality by Source (D3 horizontal bars) ──
        try { renderSignalChart(sn); } catch(e) { console.error('Signal chart error:', e); }

        // ── Recall Learning Curve (D3 connected dots) ──
        try { renderRecallChart(re); } catch(e) { console.error('Recall chart error:', e); }

    } catch(e) {
        console.error('Analytics load failed:', e);
    }
}

export function renderLifecycleChart(svData, fbData, chData) {
    var container = document.getElementById('lifecycleChart');
    var legendEl = document.getElementById('lifecycleLegend');
    container.innerHTML = '';
    if (!svData || svData.length === 0) {
        container.innerHTML = '<div class="llm-empty">No lifecycle data yet</div>';
        legendEl.innerHTML = '';
        return;
    }

    var w = container.clientWidth || 600;
    var h = 220;
    var margin = { top: 10, right: 50, bottom: 28, left: 45 };
    var iw = w - margin.left - margin.right;
    var ih = h - margin.top - margin.bottom;

    var svg = svgEl('svg', { width: w, height: h });
    container.appendChild(svg);
    var g = svgEl('g', { transform: 'translate(' + margin.left + ',' + margin.top + ')' });
    svg.appendChild(g);

    // Prepare stack data
    var stackKeys = ['active', 'merged', 'fading', 'archived'];
    var stackColors = { active: 'var(--accent-green)', merged: 'var(--accent-cyan)', fading: 'var(--accent-yellow)', archived: 'var(--text-dim)' };

    var nd = svData.length;
    var bandStep = iw / nd;
    var bandPad = 0.1;
    var bandW = bandStep * (1 - bandPad);
    var bandOff = bandStep * bandPad / 2;

    var maxY = Math.max.apply(null, svData.map(function(d) { return (d.active || 0) + (d.merged || 0) + (d.fading || 0) + (d.archived || 0); })) || 1;

    // Manual stack calculation
    var stackData = svData.map(function(d) {
        var y0 = 0;
        var layers = {};
        stackKeys.forEach(function(k) {
            var v = d[k] || 0;
            layers[k] = { y0: y0, y1: y0 + v };
            y0 += v;
        });
        return layers;
    });

    // Draw stacked area polygons per layer
    stackKeys.forEach(function(key) {
        var pts = [];
        // Top edge (left to right)
        for (var i = 0; i < nd; i++) {
            var cx = i * bandStep + bandStep / 2;
            pts.push(cx + ',' + linScale(stackData[i][key].y1, 0, maxY, ih, 0));
        }
        // Bottom edge (right to left)
        for (var j = nd - 1; j >= 0; j--) {
            var cx2 = j * bandStep + bandStep / 2;
            pts.push(cx2 + ',' + linScale(stackData[j][key].y0, 0, maxY, ih, 0));
        }
        g.appendChild(svgEl('polygon', { points: pts.join(' '), fill: stackColors[key], opacity: key === 'archived' ? '0.3' : '0.6' }));
    });

    // Feedback quality overlay line (secondary Y axis)
    if (fbData && fbData.length > 0) {
        var fbMap = {};
        fbData.forEach(function(d) { var t = d.helpful + d.partial + d.irrelevant; fbMap[d.date] = t > 0 ? (d.helpful / t) * 100 : null; });
        var fbPoints = [];
        svData.forEach(function(d, i) {
            var q = fbMap[d.date];
            if (q !== null && q !== undefined) {
                fbPoints.push({ idx: i, quality: q, date: d.date });
            }
        });

        if (fbPoints.length > 1) {
            var linePts = fbPoints.map(function(p) {
                return (p.idx * bandStep + bandStep / 2) + ',' + linScale(p.quality, 0, 100, ih, 0);
            });
            g.appendChild(svgEl('polyline', { points: linePts.join(' '), fill: 'none', stroke: 'var(--accent-violet)', 'stroke-width': 2, 'stroke-dasharray': '4,2' }));

            // Right axis label
            g.appendChild(svgText(iw + 8, linScale(50, 0, 100, ih, 0), 'quality %', { fill: 'var(--text-dim)', 'font-size': '0.6rem', 'dominant-baseline': 'middle' }));
        }
    }

    // Consolidation diamond markers
    if (chData && chData.length > 0) {
        var chMap = {};
        chData.forEach(function(d) { if (d.processed > 0) chMap[d.date] = d.processed; });
        svData.forEach(function(d, i) {
            if (chMap[d.date]) {
                var cx = i * bandStep + bandStep / 2;
                var dy = linScale(maxY, 0, maxY, ih, 0) - 2;
                g.appendChild(svgEl('path', { d: 'M' + cx + ',' + dy + ' l4,6 l-4,6 l-4,-6 z', fill: 'var(--accent-orange)', opacity: '0.7' }));
            }
        });
    }

    // X axis
    var labelEvery = Math.max(1, Math.ceil(nd / 7));
    var months = ['Jan','Feb','Mar','Apr','May','Jun','Jul','Aug','Sep','Oct','Nov','Dec'];
    g.appendChild(svgEl('line', { x1: 0, x2: iw, y1: ih, y2: ih, stroke: 'var(--border-subtle)' }));
    svData.forEach(function(d, i) {
        if (i % labelEvery !== 0) return;
        var bx = i * bandStep + bandStep / 2;
        g.appendChild(svgEl('line', { x1: bx, x2: bx, y1: ih, y2: ih + 4, stroke: 'var(--border-subtle)' }));
        var parts = d.date.split('-');
        g.appendChild(svgText(bx, ih + 14, months[parseInt(parts[1]) - 1] + ' ' + parseInt(parts[2]), { 'text-anchor': 'middle', 'font-size': '0.6rem' }));
    });

    // Y axis ticks
    var yTicks = 4;
    var yStep = maxY / yTicks;
    for (var t = 0; t <= yTicks; t++) {
        var tv = Math.round(t * yStep);
        var ty = linScale(tv, 0, maxY, ih, 0);
        g.appendChild(svgEl('line', { x1: -4, x2: 0, y1: ty, y2: ty, stroke: 'var(--border-subtle)' }));
        g.appendChild(svgText(-8, ty, String(tv), { 'text-anchor': 'end', 'dominant-baseline': '0.32em', 'font-size': '0.6rem' }));
    }

    // Tooltip on hover
    var tooltipLine = svgEl('line', { stroke: 'var(--text-dim)', 'stroke-dasharray': '2,2', y1: 0, y2: ih, display: 'none' });
    g.appendChild(tooltipLine);
    var tooltipDiv = document.getElementById('lifecycleTooltip');
    var hitRect = svgEl('rect', { width: iw, height: ih, fill: 'transparent' });
    hitRect.addEventListener('mousemove', function(event) {
        var rect = hitRect.getBoundingClientRect();
        var mx = event.clientX - rect.left;
        var idx = Math.round(mx / iw * (nd - 1));
        idx = Math.max(0, Math.min(idx, nd - 1));
        var d = svData[idx];
        var cx = idx * bandStep + bandStep / 2;
        tooltipLine.setAttribute('x1', cx);
        tooltipLine.setAttribute('x2', cx);
        tooltipLine.setAttribute('display', '');
        var fb = fbData.find(function(f) { return f.date === d.date; });
        var qStr = '';
        if (fb) { var tt = fb.helpful + fb.partial + fb.irrelevant; qStr = tt > 0 ? ' \u00B7 quality: ' + ((fb.helpful / tt) * 100).toFixed(0) + '%' : ''; }
        tooltipDiv.style.display = 'block';
        tooltipDiv.style.left = (event.offsetX + 12) + 'px';
        tooltipDiv.style.top = (event.offsetY - 10) + 'px';
        tooltipDiv.innerHTML = '<strong>' + d.date + '</strong><br>Active: ' + (d.active || 0) + ' \u00B7 Merged: ' + (d.merged || 0) + ' \u00B7 Fading: ' + (d.fading || 0) + ' \u00B7 Archived: ' + (d.archived || 0) + qStr;
    });
    hitRect.addEventListener('mouseleave', function() {
        tooltipLine.setAttribute('display', 'none');
        tooltipDiv.style.display = 'none';
    });
    g.appendChild(hitRect);

    // Legend
    legendEl.innerHTML = stackKeys.map(function(k) {
        return '<span style="display:flex;align-items:center;gap:3px"><span style="width:8px;height:8px;border-radius:2px;background:' + stackColors[k] + ';opacity:0.6"></span>' + k + '</span>';
    }).join('') + '<span style="display:flex;align-items:center;gap:3px"><span style="width:12px;height:2px;background:var(--accent-violet);border-radius:1px"></span>quality</span>' +
    '<span style="display:flex;align-items:center;gap:3px"><span style="width:0;height:0;border-left:4px solid transparent;border-right:4px solid transparent;border-bottom:6px solid var(--accent-orange)"></span>consolidation</span>';
}

export function renderSignalChart(sn) {
    var container = document.getElementById('signalNoiseChart');
    container.innerHTML = '';
    var sources = Object.keys(sn).sort(function(a, b) { return (sn[b].total || 0) - (sn[a].total || 0); });
    if (sources.length === 0) { container.innerHTML = '<div class="llm-empty">No source data</div>'; return; }

    var avgSurv = 0;
    var totalAll = 0;
    sources.forEach(function(s) { avgSurv += (sn[s].survival_rate || 0) * (sn[s].total || 0); totalAll += (sn[s].total || 0); });
    avgSurv = totalAll > 0 ? avgSurv / totalAll : 0;

    var w = container.clientWidth || 400;
    var h = sources.length * 36 + 10;
    var margin = { top: 4, right: 12, bottom: 4, left: 80 };
    var iw = w - margin.left - margin.right;
    var ih = h - margin.top - margin.bottom;

    var svg = svgEl('svg', { width: w, height: h });
    container.appendChild(svg);
    var g = svgEl('g', { transform: 'translate(' + margin.left + ',' + margin.top + ')' });
    svg.appendChild(g);

    // Band scale params
    var bandStep = ih / sources.length;
    var bandPadding = 0.25;
    var bandW = bandStep * (1 - bandPadding);
    var bandOffset = bandStep * bandPadding / 2;

    sources.forEach(function(src, i) {
        var yPos = i * bandStep + bandOffset;
        var survPct = (sn[src].survival_rate || 0) * 100;
        var barW = Math.max(2, linScale(survPct, 0, 100, 0, iw));
        // Background track
        g.appendChild(svgEl('rect', { x: 0, y: yPos, width: iw, height: bandW, fill: 'var(--bg-tertiary)', rx: 3 }));
        // Bar
        g.appendChild(svgEl('rect', { x: 0, y: yPos, width: barW, height: bandW, fill: _raSourceColors[src] || 'var(--text-muted)', opacity: '0.7', rx: 3 }));
        // Left label
        var label = svgText(-6, yPos + bandW / 2, src, { 'text-anchor': 'end', 'dominant-baseline': 'middle', fill: _raSourceColors[src] || 'var(--text-muted)', 'font-size': '0.72rem', 'font-weight': '600' });
        g.appendChild(label);
        // Value label
        var s = sn[src];
        var valStr = s.active + '/' + s.total + ' (' + survPct.toFixed(0) + '%)' + (s.avg_salience ? ' \u00B7 sal ' + s.avg_salience.toFixed(2) : '');
        var valLabel = svgText(barW + 4, yPos + bandW / 2, valStr, { 'dominant-baseline': 'middle', fill: 'var(--text-muted)', 'font-size': '0.65rem' });
        g.appendChild(valLabel);
    });

    // Average line
    var avgX = linScale(avgSurv * 100, 0, 100, 0, iw);
    g.appendChild(svgEl('line', { x1: avgX, x2: avgX, y1: 0, y2: ih, stroke: 'var(--text-muted)', 'stroke-dasharray': '3,3', 'stroke-width': 1 }));
}

export function renderRecallChart(re) {
    var container = document.getElementById('recallChart');
    container.innerHTML = '';
    if (!re || re.length === 0) { container.innerHTML = '<div class="llm-empty">No recall data yet</div>'; return; }

    var w = container.clientWidth || 400;
    var h = 180;
    var margin = { top: 12, right: 16, bottom: 36, left: 45 };
    var iw = w - margin.left - margin.right;
    var ih = h - margin.top - margin.bottom;

    var svg = svgEl('svg', { width: w, height: h });
    container.appendChild(svg);
    var g = svgEl('g', { transform: 'translate(' + margin.left + ',' + margin.top + ')' });
    svg.appendChild(g);

    var bucketNames = re.map(function(b) { return b.bucket; });
    var nb = bucketNames.length;
    // scalePoint: evenly spaced points with padding
    function xScale(val) {
        var idx = bucketNames.indexOf(val);
        if (idx < 0) return 0;
        var usable = (iw - 40); // range [20, iw-20]
        if (nb <= 1) return 20 + usable / 2;
        var padStep = 0.5;
        var step = usable / (nb - 1 + padStep * 2);
        return 20 + (padStep + idx) * step;
    }

    var maxSal = Math.max.apply(null, re.map(function(b) { return b.avg_salience; })) || 1;
    var yMax = Math.max(maxSal * 1.1, 0.5);
    var maxCount = Math.max.apply(null, re.map(function(b) { return b.count; })) || 1;

    // scaleSqrt for circle sizing
    function rScale(count) {
        var t = maxCount > 0 ? count / maxCount : 0;
        return 4 + Math.sqrt(Math.max(0, t)) * (12 - 4);
    }

    // Reference line at first bucket's salience
    if (re.length > 1) {
        var refY = linScale(re[0].avg_salience, 0, yMax, ih, 0);
        g.appendChild(svgEl('line', { x1: 0, x2: iw, y1: refY, y2: refY, stroke: 'var(--border-subtle)', 'stroke-dasharray': '3,3' }));
    }

    // Connecting polyline
    var isLearning = re.length >= 2 && re[re.length - 1].avg_salience > re[0].avg_salience;
    var linePts = re.map(function(b) {
        return xScale(b.bucket) + ',' + linScale(b.avg_salience, 0, yMax, ih, 0);
    });
    g.appendChild(svgEl('polyline', { points: linePts.join(' '), fill: 'none', stroke: isLearning ? 'var(--accent-green)' : 'var(--accent-red)', 'stroke-width': 2 }));

    // Dots (sized by count) + labels
    re.forEach(function(b) {
        var cx = xScale(b.bucket);
        var cy = linScale(b.avg_salience, 0, yMax, ih, 0);
        var r = rScale(b.count);
        var color = b.bucket.indexOf('never') >= 0 ? 'var(--accent-red)' : b.bucket.indexOf('6+') >= 0 ? 'var(--accent-green)' : 'var(--accent-cyan)';
        g.appendChild(svgEl('circle', { cx: cx, cy: cy, r: r, fill: color, opacity: '0.7' }));
        g.appendChild(svgText(cx, cy - r - 5, b.avg_salience.toFixed(2), { 'text-anchor': 'middle', fill: 'var(--text-secondary)', 'font-size': '0.68rem', 'font-weight': '600' }));
    });

    // X axis
    g.appendChild(svgEl('line', { x1: 0, x2: iw, y1: ih, y2: ih, stroke: 'var(--border-subtle)' }));
    bucketNames.forEach(function(name) {
        var bx = xScale(name);
        g.appendChild(svgEl('line', { x1: bx, x2: bx, y1: ih, y2: ih + 4, stroke: 'var(--border-subtle)' }));
        g.appendChild(svgText(bx, ih + 14, name, { 'text-anchor': 'middle', 'font-size': '0.65rem' }));
    });

    // Y axis
    var yTicks = 4;
    var yStep = yMax / yTicks;
    for (var t = 0; t <= yTicks; t++) {
        var tv = t * yStep;
        var ty = linScale(tv, 0, yMax, ih, 0);
        g.appendChild(svgEl('line', { x1: -4, x2: 0, y1: ty, y2: ty, stroke: 'var(--border-subtle)' }));
        g.appendChild(svgText(-8, ty, tv.toFixed(1), { 'text-anchor': 'end', 'dominant-baseline': '0.32em', 'font-size': '0.6rem' }));
    }

    // Verdict text
    var ratio = (re.length >= 2 && re[0].avg_salience > 0) ? (re[re.length - 1].avg_salience / re[0].avg_salience) : 0;
    var verdictColor = isLearning ? 'var(--accent-green)' : 'var(--accent-red)';
    var verdictText = isLearning
        ? 'Learning: ' + ratio.toFixed(1) + 'x salience increase from "' + re[0].bucket + '" to "' + re[re.length - 1].bucket + '"'
        : 'No clear learning signal \u2014 salience flat or declining';
    svg.appendChild(svgText(margin.left, h - 2, verdictText, { fill: verdictColor, 'font-size': '0.65rem' }));
}

// ── Session Activity (expandable rows with quality enrichment) ──

export function _buildSessionEnrichment() {
    var enrichment = {};
    _toolLog.forEach(function(r) {
        var sid = r.session_id || '';
        if (!sid) return;
        if (!enrichment[sid]) enrichment[sid] = { tools: {}, types: {}, fbH: 0, fbP: 0, fbI: 0 };
        var e = enrichment[sid];
        var tool = r.tool_name || r.tool || '';
        if (tool) e.tools[tool] = (e.tools[tool] || 0) + 1;
        if ((tool === 'remember') && r.memory_type) e.types[r.memory_type] = (e.types[r.memory_type] || 0) + 1;
        if (tool === 'feedback') {
            var rating = r.rating || r.quality || '';
            if (rating === 'helpful') e.fbH++;
            else if (rating === 'partial') e.fbP++;
            else if (rating === 'irrelevant') e.fbI++;
        }
    });
    return enrichment;
}

export function toggleSession(sessionId) {
    var detail = document.getElementById('detail-' + sessionId);
    var chevron = document.getElementById('chevron-' + sessionId);
    if (detail) detail.classList.toggle('open');
    if (chevron) chevron.classList.toggle('open');
}

export async function loadSessionTimeline() {
    try {
        var resp = await fetch(CONFIG.API_BASE + '/sessions?days=' + _analyticsRange + '&limit=15');
        if (!resp.ok) return;
        var data = await resp.json();
        var sessions = data.sessions || [];
        var el = document.getElementById('sessionTimeline');
        if (sessions.length === 0) {
            el.innerHTML = '<span class="llm-empty" style="font-size:0.75rem">No sessions found</span>';
            return;
        }

        var activeSessions = sessions.filter(function(s) { return s.memory_count > 0; });
        if (activeSessions.length === 0) {
            el.innerHTML = '<span class="llm-empty" style="font-size:0.75rem">No sessions with memories</span>';
            return;
        }

        var enrichment = _buildSessionEnrichment();
        var maxCount = Math.max.apply(null, activeSessions.map(function(s) { return s.memory_count; }));
        var colors = ['var(--accent-cyan)', 'var(--accent-green)', 'var(--accent-violet)', 'var(--accent-blue)', 'var(--accent-pink)', 'var(--accent-yellow)'];
        var now = new Date();
        var today = now.toDateString();
        var yesterday = new Date(now.getTime() - 86400000).toDateString();
        var months = ['Jan','Feb','Mar','Apr','May','Jun','Jul','Aug','Sep','Oct','Nov','Dec'];

        var rows = activeSessions.map(function(s, i) {
            var st = new Date(s.start_time);
            var et = new Date(s.end_time);
            var durMin = Math.round((et - st) / 60000);
            var durStr = durMin >= 60 ? Math.floor(durMin / 60) + 'h ' + (durMin % 60) + 'm' : durMin + 'm';
            if (durMin === 0) durStr = '<1m';
            var dateStr = st.toDateString() === today ? 'Today' : st.toDateString() === yesterday ? 'Yesterday' : months[st.getMonth()] + ' ' + st.getDate();
            var timeStr = String(st.getHours()).padStart(2, '0') + ':' + String(st.getMinutes()).padStart(2, '0');

            var barPct = Math.max((s.memory_count / maxCount) * 100, 8);
            var color = colors[i % colors.length];

            // Enrichment from tool log
            var e = enrichment[s.session_id] || { tools: {}, types: {}, fbH: 0, fbP: 0, fbI: 0 };
            var totalFb = e.fbH + e.fbP + e.fbI;
            var qualityDotColor = 'var(--text-dim)';
            var fbBadge = '';
            if (totalFb > 0) {
                var helpfulPct = (e.fbH / totalFb) * 100;
                qualityDotColor = helpfulPct >= 70 ? 'var(--accent-green)' : helpfulPct >= 50 ? 'var(--accent-yellow)' : 'var(--accent-red)';
                fbBadge = '<span class="session-feedback-badge">' + e.fbH + '/' + totalFb + ' helpful</span>';
            }

            // Bar color reflects quality
            if (totalFb > 0) {
                var hp = (e.fbH / totalFb) * 100;
                color = hp >= 70 ? 'var(--accent-green)' : hp >= 50 ? 'var(--accent-yellow)' : 'var(--accent-cyan)';
            }

            var concepts = (s.top_concepts || []).slice(0, 2);
            var pillsHtml = concepts.map(function(c) { return '<span class="session-concept-pill">' + escapeHtml(c) + '</span>'; }).join('');

            var sid = s.session_id.replace(/[^a-zA-Z0-9]/g, '');

            // Expanded detail
            var toolStr = Object.keys(e.tools).map(function(t) { return t + '(' + e.tools[t] + ')'; }).join(' ');
            var typeStr = Object.keys(e.types).map(function(t) { return t + '(' + e.types[t] + ')'; }).join(' ');
            var allConcepts = (s.top_concepts || []).map(function(c) { return '<span class="session-concept-pill">' + escapeHtml(c) + '</span>'; }).join('');

            var detailHtml = '<div class="session-detail" id="detail-' + sid + '">';
            if (toolStr) detailHtml += '<div class="session-detail-row"><span class="session-detail-label">Tools: </span>' + toolStr + '</div>';
            if (typeStr) detailHtml += '<div class="session-detail-row"><span class="session-detail-label">Types: </span>' + typeStr + '</div>';
            if (allConcepts) detailHtml += '<div class="session-detail-row"><span class="session-detail-label">Topics: </span><div class="session-detail-pills">' + allConcepts + '</div></div>';
            detailHtml += '</div>';

            return '<div class="session-row session-row-expandable" onclick="toggleSession(\'' + sid + '\')" title="' + escapeHtml(s.session_id) + '">' +
                '<span class="session-quality-dot" style="background:' + qualityDotColor + '"></span>' +
                '<span class="session-when"><span class="session-date">' + dateStr + '</span><span class="session-time">' + timeStr + '</span></span>' +
                '<span class="session-bar-col"><span class="session-bar-inline" style="width:' + barPct.toFixed(0) + '%;background:' + color + '"></span><span class="session-count">' + s.memory_count + '</span></span>' +
                '<span class="session-dur">' + durStr + '</span>' +
                fbBadge +
                '<span class="session-concepts">' + pillsHtml + '</span>' +
                '<span class="session-chevron" id="chevron-' + sid + '">&#9656;</span>' +
                '</div>' + detailHtml;
        }).join('');

        el.innerHTML = rows;
    } catch(e) { /* ignore */ }
}

export function formatBytes(bytes) {
    if (bytes < 1024) return Math.round(bytes) + 'B';
    if (bytes < 1024 * 1024) return (bytes / 1024).toFixed(1) + 'KB';
    return (bytes / (1024 * 1024)).toFixed(1) + 'MB';
}

export function renderToolChart(chartBuckets, range) {
    var container = document.getElementById('toolChart');
    var tooltip = document.getElementById('toolChartTooltip');
    container.innerHTML = '';
    if (!chartBuckets || chartBuckets.length === 0) { container.innerHTML = '<div class="llm-empty">No data for chart</div>'; return; }

    var labelFmt, labelEvery;
    if (range === '1h') {
        labelFmt = function(d) { return String(d.getHours()).padStart(2,'0') + ':' + String(d.getMinutes()).padStart(2,'0'); };
        labelEvery = 3;
    } else if (range === '6h') {
        labelFmt = function(d) { return String(d.getHours()).padStart(2,'0') + ':' + String(d.getMinutes()).padStart(2,'0'); };
        labelEvery = 2;
    } else if (range === '168h') {
        var days = ['Sun','Mon','Tue','Wed','Thu','Fri','Sat'];
        labelFmt = function(d) { return days[d.getDay()]; };
        labelEvery = 1;
    } else {
        labelFmt = function(d) { return String(d.getHours()).padStart(2,'0') + ':00'; };
        labelEvery = 4;
    }

    var buckets = chartBuckets.map(function(b, i) {
        var ts = new Date(b.timestamp);
        return { idx: i, start: ts, label: labelFmt(ts), calls: b.calls || 0, errors: b.errors || 0 };
    });

    var w = container.clientWidth || 400;
    var h = 200;
    var margin = { top: 10, right: 10, bottom: 28, left: 50 };
    var iw = w - margin.left - margin.right;
    var ih = h - margin.top - margin.bottom;

    var svg = svgEl('svg', { width: w, height: h });
    container.appendChild(svg);
    var g = svgEl('g', { transform: 'translate(' + margin.left + ',' + margin.top + ')' });
    svg.appendChild(g);

    var maxCalls = Math.max.apply(null, buckets.map(function(d) { return d.calls; })) || 1;
    var n = buckets.length;
    var bandStep = iw / n;
    var bandPad = 0.25;
    var bandW = bandStep * (1 - bandPad);
    var bandOff = bandStep * bandPad / 2;

    // Y axis ticks
    var yTicks = 4;
    var yStep = maxCalls / yTicks;
    for (var t = 0; t <= yTicks; t++) {
        var tv = Math.round(t * yStep);
        var ty = linScale(tv, 0, maxCalls, ih, 0);
        g.appendChild(svgEl('line', { x1: -4, x2: 0, y1: ty, y2: ty, stroke: 'var(--border-subtle)' }));
        g.appendChild(svgText(-8, ty, String(tv), { 'text-anchor': 'end', 'dominant-baseline': '0.32em', 'font-size': '10' }));
    }
    // X axis baseline
    g.appendChild(svgEl('line', { x1: 0, x2: iw, y1: ih, y2: ih, stroke: 'var(--border-subtle)' }));
    // X axis labels
    buckets.forEach(function(d) {
        if (d.idx % labelEvery !== 0) return;
        var bx = d.idx * bandStep + bandStep / 2;
        g.appendChild(svgEl('line', { x1: bx, x2: bx, y1: ih, y2: ih + 4, stroke: 'var(--border-subtle)' }));
        g.appendChild(svgText(bx, ih + 14, d.label, { 'text-anchor': 'middle', 'font-size': '10' }));
    });

    // Bars
    buckets.forEach(function(d) {
        var bx = d.idx * bandStep + bandOff;
        if (d.calls > 0) {
            var barY = linScale(d.calls, 0, maxCalls, ih, 0);
            g.appendChild(svgEl('rect', { x: bx, y: barY, width: bandW, height: ih - barY, rx: 2, fill: 'var(--accent-cyan)', opacity: '0.8' }));
        }
        // Hit area
        var hit = svgEl('rect', { x: bx - bandW * 0.1, y: 0, width: bandW * 1.2, height: ih, fill: 'transparent' });
        (function(dd) {
            hit.addEventListener('mouseover', function(event) {
                if (dd.calls === 0) return;
                var errLine = dd.errors > 0 ? '<br><span style="color:var(--accent-red)">' + dd.errors + ' error' + (dd.errors === 1 ? '' : 's') + '</span>' : '';
                tooltip.innerHTML = '<strong>' + dd.label + '</strong><br>' + dd.calls + ' call' + (dd.calls === 1 ? '' : 's') + errLine;
                tooltip.style.display = 'block';
                var barX = margin.left + dd.idx * bandStep + bandStep / 2;
                var ttLeft = barX + 10;
                if (ttLeft + 180 > w) ttLeft = barX - 190;
                tooltip.style.left = ttLeft + 'px';
                tooltip.style.top = (margin.top + 2) + 'px';
            });
            hit.addEventListener('mouseout', function() { tooltip.style.display = 'none'; });
        })(d);
        g.appendChild(hit);

        // Error marker
        if (d.errors > 0) {
            var ey = linScale(d.calls, 0, maxCalls, ih, 0) - 5;
            g.appendChild(svgEl('circle', { cx: d.idx * bandStep + bandStep / 2, cy: ey, r: 3, fill: 'var(--accent-red)', 'pointer-events': 'none' }));
        }
    });
}

