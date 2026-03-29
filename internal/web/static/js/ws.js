import { state, CONFIG } from './state.js';
import { addLivePost } from './utils.js';

// ── WebSocket ──
export function connectWebSocket() {
    if (state.ws) { try { state.ws.close(); } catch(e) {} }
    var ws = new WebSocket(CONFIG.WS_URL);
    state.ws = ws;
    ws.onopen = function() { state.wsReconnectAttempts = 0; };
    ws.onmessage = function(event) {
        try {
            var msg = JSON.parse(event.data);
            var type = msg.type || 'unknown';
            var payload = msg.payload || {};
            var desc = '';
            switch (type) {
                case 'raw_memory_created': desc = 'New memory from ' + (payload.source || '?'); break;
                case 'memory_encoded': desc = 'Encoded: ' + (payload.summary || '').slice(0, 60); state.exploreLoaded.memories = false; break;
                case 'memory_accessed': desc = 'Accessed ' + (payload.memory_ids || []).length + ' memories'; break;
                case 'query_executed': desc = 'Query: "' + (payload.query_text || '').slice(0, 40) + '" (' + (payload.results_returned || 0) + ' results)'; break;
                case 'consolidation_started': desc = 'Consolidation cycle started'; break;
                case 'consolidation_completed': desc = 'Consolidated: ' + (payload.memories_processed || 0) + ' processed'; state.exploreLoaded.episodes = false; state.exploreLoaded.patterns = false; state.exploreLoaded.abstractions = false; break;
                case 'dream_cycle_completed': desc = 'Dream cycle'; state.exploreLoaded.abstractions = false; state.dreamSessionTotals.cycles++; state.dreamSessionTotals.replayed += (payload.memories_replayed || 0); state.dreamSessionTotals.strengthened += (payload.associations_strengthened || 0); state.dreamSessionTotals.newLinks += (payload.new_associations_created || 0); state.dreamSessionTotals.insights += (payload.insights_generated || 0); state.dreamSessionTotals.crossProject += (payload.cross_project_links || 0); state.dreamSessionTotals.demoted += (payload.noisy_memories_demoted || 0); break;
                case 'pattern_discovered': desc = 'Pattern: ' + (payload.title || '').slice(0, 40); state.exploreLoaded.patterns = false; break;
                case 'abstraction_created': desc = (payload.level === 3 ? 'Axiom' : 'Principle') + ': ' + (payload.title || '').slice(0, 40); state.exploreLoaded.abstractions = false; break;
                case 'episode_closed': desc = 'Episode closed: ' + (payload.title || '').slice(0, 40); state.exploreLoaded.episodes = false; break;
                case 'forum_post_created': desc = (payload.author_name || 'Someone') + ' posted in forum'; state.forumLoaded = false; break;
                default: desc = type.replace(/_/g, ' ');
            }
            window.addEvent(type, desc, msg.timestamp || new Date().toISOString(), payload);
            addLivePost(type, payload, msg.timestamp);
            if (['memory_encoded', 'consolidation_completed', 'dream_cycle_completed'].includes(type) && state.currentView === 'timeline') {
                clearTimeout(state._timelineLiveReload);
                state._timelineLiveReload = setTimeout(function() { window.loadTimelineData(false); }, 5000);
            }
            // Live-refresh: reload the active explore tab if its data was just invalidated
            if (state.currentView === 'explore') {
                var tab = state.currentExploreTab;
                if (!state.exploreLoaded[tab]) window.loadExploreTab(tab);
                if (!state.forumLoaded) window.loadForumIndex();
            }
            // Track forum notifications
            if (type === 'forum_post_created') window.onForumPostWebSocket(payload);
            // Live-insert forum post into current thread view
            if (type === 'forum_post_created' && state.currentView === 'thread' && state.currentForumThread === payload.thread_id) {
                window.appendForumPostToThread(payload);
            }
            // Refresh nav stats on memory-changing events
            if (['memory_encoded', 'raw_memory_created', 'consolidation_completed'].includes(type)) window.loadStats();
            // Refresh LLM usage if viewing that tab
            if (state.currentView === 'llm') window.loadLLMUsage();
        } catch (e) { console.error('Failed to parse WebSocket message:', e); }
    };
    ws.onclose = function() {
        var delay = Math.min(30000, 1000 * Math.pow(1.5, state.wsReconnectAttempts));
        state.wsReconnectAttempts++;
        setTimeout(connectWebSocket, delay);
    };
    ws.onerror = function() { ws.close(); };
}

