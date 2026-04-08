import { state } from './state.js';
import { fetchJSON, escapeHtml } from './utils.js';

var _swapLog = [];

function appendSwapLog(msg) {
    _swapLog.push('[' + new Date().toLocaleTimeString() + '] ' + msg);
    var el = document.getElementById('modelSwapLog');
    var panel = document.getElementById('modelSwapStatus');
    if (el && panel) {
        panel.style.display = '';
        el.textContent = _swapLog.join('\n');
        el.scrollTop = el.scrollHeight;
    }
}

export async function loadModels() {
    try {
        var data = await fetchJSON('/models');
        state.modelsLoaded = true;

        if (!data.enabled) {
            document.getElementById('modelChatName').textContent = 'N/A';
            document.getElementById('modelEmbedName').textContent = 'N/A';
            document.getElementById('modelStatus').textContent = 'Not available';
            document.getElementById('modelStatus').style.color = 'var(--text-dim)';
            document.getElementById('modelDir').textContent = '-';
            document.getElementById('modelModeToggle').innerHTML = '';
            document.getElementById('modelsTableBody').innerHTML = '<tr><td colspan="5" class="llm-empty">Embedded provider not active. Set llm.provider: "embedded" in config.yaml and rebuild with make build-embedded</td></tr>';
            document.getElementById('modelsUpdated').textContent = 'Updated ' + new Date().toLocaleTimeString();
            return;
        }

        var active = data.active || {};
        var mode = data.mode || active.mode || 'embedded';
        var isAPI = mode === 'api';

        // Mode toggle button
        var toggleEl = document.getElementById('modelModeToggle');
        if (isAPI) {
            document.getElementById('modelChatName').textContent = active.api_model || 'Gemini';
            document.getElementById('modelEmbedName').textContent = 'API';
            document.getElementById('modelStatus').textContent = 'Cloud API';
            document.getElementById('modelStatus').style.color = 'var(--accent-cyan)';
            toggleEl.innerHTML = '<button class="agent-refresh-btn" style="padding:4px 12px" onclick="switchProviderMode(\'embedded\')">Switch to Local Model</button>';
        } else {
            document.getElementById('modelChatName').textContent = active.chat_model || 'none';
            document.getElementById('modelEmbedName').textContent = active.embed_model || '(using chat model)';
            if (active.loaded) {
                document.getElementById('modelStatus').textContent = 'Loaded';
                document.getElementById('modelStatus').style.color = 'var(--accent-green)';
            } else {
                document.getElementById('modelStatus').textContent = 'Not loaded';
                document.getElementById('modelStatus').style.color = 'var(--accent-red)';
            }
            toggleEl.innerHTML = '<button class="agent-refresh-btn" style="padding:4px 12px" onclick="switchProviderMode(\'api\')">Switch to Gemini</button>';
        }
        document.getElementById('modelDir').textContent = active.models_dir || '-';

        var models = data.models || [];
        var tbody = document.getElementById('modelsTableBody');

        if (models.length === 0) {
            tbody.innerHTML = '<tr><td colspan="5" class="llm-empty">No models in models.json.</td></tr>';
        } else {
            tbody.innerHTML = models.map(function(m) {
                var isChatActive = !isAPI && m.filename === active.chat_model;
                var isEmbedActive = !isAPI && m.filename === active.embed_model;
                var status = '';
                if (isChatActive) status = '<span style="color:var(--accent-green);font-weight:bold">active</span>';
                else if (isEmbedActive) status = '<span style="color:var(--accent-cyan);font-weight:bold">active</span>';
                else if (isAPI) status = '<span style="color:var(--text-dim)">standby</span>';

                var roleLabel = m.role || '-';
                var detail = [m.quantize, m.version].filter(Boolean).join(' / ');

                var actions = '';
                if (!isAPI && m.role === 'chat' && !isChatActive) {
                    actions += '<button class="agent-refresh-btn" style="font-size:0.7rem;padding:2px 8px" onclick="swapChatModel(\'' + escapeHtml(m.filename) + '\')">Load</button>';
                }
                if (!isAPI && m.role === 'embedding' && !isEmbedActive) {
                    actions += '<button class="agent-refresh-btn" style="font-size:0.7rem;padding:2px 8px" onclick="swapEmbedModel(\'' + escapeHtml(m.filename) + '\')">Load</button>';
                }
                if (isChatActive || isEmbedActive) actions = '-';

                return '<tr>' +
                    '<td><strong>' + escapeHtml(m.filename) + '</strong>' + (detail ? '<br><span style="font-size:0.75rem;color:var(--text-dim)">' + escapeHtml(detail) + '</span>' : '') + '</td>' +
                    '<td>' + roleLabel + '</td>' +
                    '<td>' + m.size_mb + ' MB</td>' +
                    '<td>' + status + '</td>' +
                    '<td>' + actions + '</td>' +
                    '</tr>';
            }).join('');
        }

        document.getElementById('modelsUpdated').textContent = 'Updated ' + new Date().toLocaleTimeString();
    } catch (e) {
        document.getElementById('modelsTableBody').innerHTML = '<tr><td colspan="5" class="llm-empty">Error: ' + escapeHtml(e.message) + '</td></tr>';
    }
}

export async function switchProviderMode(mode) {
    var label = mode === 'api' ? 'Gemini (API)' : 'Embedded (local)';
    appendSwapLog('Switching to ' + label + '...');
    try {
        var resp = await fetch('/api/v1/models/active', {
            method: 'POST',
            headers: { 'Content-Type': 'application/json' },
            body: JSON.stringify({ mode: mode })
        });
        var data = await resp.json();
        if (!resp.ok) {
            appendSwapLog('ERROR: ' + (data.error || 'unknown error'));
            return;
        }
        appendSwapLog('Switched to ' + label);
        loadModels();
    } catch (e) {
        appendSwapLog('ERROR: ' + e.message);
    }
}

export async function swapChatModel(filename) {
    appendSwapLog('Swapping chat model to ' + filename + '...');
    try {
        var resp = await fetch('/api/v1/models/active', {
            method: 'POST',
            headers: { 'Content-Type': 'application/json' },
            body: JSON.stringify({ chat_model: filename })
        });
        var data = await resp.json();
        if (!resp.ok) {
            appendSwapLog('ERROR: ' + (data.error || 'unknown error'));
            return;
        }
        appendSwapLog('Chat model swapped to ' + filename);
        loadModels();
    } catch (e) {
        appendSwapLog('ERROR: ' + e.message);
    }
}

export async function swapEmbedModel(filename) {
    appendSwapLog('Swapping embed model to ' + filename + '...');
    try {
        var resp = await fetch('/api/v1/models/active', {
            method: 'POST',
            headers: { 'Content-Type': 'application/json' },
            body: JSON.stringify({ embed_model: filename })
        });
        var data = await resp.json();
        if (!resp.ok) {
            appendSwapLog('ERROR: ' + (data.error || 'unknown error'));
            return;
        }
        appendSwapLog('Embed model swapped to ' + filename);
        loadModels();
    } catch (e) {
        appendSwapLog('ERROR: ' + e.message);
    }
}
