// ── Mnemonic Dashboard — Entry Point ──
// Imports all modules and wires exported functions to window for HTML onclick handlers.

import { state, CONFIG } from './state.js';
import { apiFetch, fetchJSON, makeDayBuckets, showToast, svgEl, linScale, svgText, fmtNum,
         escapeHtml, memoryType, memoryTypeAbbr, memoryTypeIcon, safeSalience, agentProfile,
         addLivePost, simpleMarkdown } from './utils.js';
import { setTheme, switchView, switchExploreTab, handleHash } from './nav.js';
import { performRecall, renderResults, toggleExpand, toggle, toggleRemember, submitRemember } from './recall.js';
import { loadThread, submitFeedback, loadExploreTab, loadEpisodes, loadMemories,
         loadPatterns, archivePattern, loadAbstractions, filterExplore } from './explore.js';
import { setTimelineRange, toggleTimelineType, filterTimeline, clearTimelineSearch,
         populateTimelineProjects, toggleTimelineProject, filterTimelineItems,
         renderTimelineItems, renderTimelineCard, expandTimelineCard, toggleTimelineTag,
         hoverTimelineTag, unhoverTimelineTag, loadTimelineData, setupTimelineScroll,
         renderTimelineSparkline } from './timeline.js';
import { toggleDrawer, loadActivityConcepts, updateBadge, addEvent, clearEvents,
         triggerConsolidation, formatInsightDetail, loadInsights, loadStats, loadProjects } from './drawer.js';
import { connectWebSocket } from './ws.js';
import { setLLMRange, loadLLMUsage, setAnalyticsRange, setToolRange, loadToolUsage,
         loadAnalytics, toggleSession, loadSessionTimeline } from './llm.js';
import { forumFetch, loadForumIndex, loadMemorySection, loadForumGroup, loadForumCategory,
         loadForumThread, renderForumPost, appendForumPostToThread, showNewThreadForm,
         submitNewThread, submitThreadReply, quotePostById, insertTagInReply,
         ensureMentionAutocomplete, subscribeToThread, markThreadRead, onForumPostWebSocket,
         updateNotificationBadge, internalizePost, toggleToolDetail, relativeTime } from './forum.js';
import { loadAgentData, refreshAgentData, renderAgentDashboard, sendChatMessage,
         toggleChatHistory, startNewConversation, onModelChange,
         checkForUpdate, triggerUpdate, renderMarkdown } from './agent.js';
import { formatBytes } from './llm.js';

// ── Wire to window for HTML onclick handlers ──
Object.assign(window, {
    // State (needed by some inline refs)
    state, CONFIG,
    // Nav
    setTheme, switchView, switchExploreTab, handleHash,
    // Recall + Remember
    performRecall, renderResults, toggleExpand, toggle, toggleRemember, submitRemember, submitFeedback,
    // Explore
    loadThread, loadExploreTab, loadEpisodes, loadMemories, loadPatterns, archivePattern,
    loadAbstractions, filterExplore,
    // Timeline
    setTimelineRange, toggleTimelineType, filterTimeline, clearTimelineSearch,
    populateTimelineProjects, toggleTimelineProject, toggleTimelineTag, hoverTimelineTag,
    unhoverTimelineTag, expandTimelineCard, loadTimelineData, renderTimelineItems,
    // Drawer
    toggleDrawer, clearEvents, triggerConsolidation, addEvent, loadStats,
    // LLM + Tools
    setLLMRange, loadLLMUsage, setAnalyticsRange, setToolRange, loadToolUsage,
    loadAnalytics, toggleSession, loadSessionTimeline,
    // Forum
    loadForumIndex, loadMemorySection, loadForumGroup, loadForumCategory, loadForumThread,
    showNewThreadForm, submitNewThread, submitThreadReply, quotePostById, insertTagInReply,
    ensureMentionAutocomplete, subscribeToThread, markThreadRead, onForumPostWebSocket,
    updateNotificationBadge, internalizePost,
    // Agent
    loadAgentData, refreshAgentData, renderAgentDashboard, sendChatMessage,
    toggleChatHistory, startNewConversation, onModelChange,
    checkForUpdate, triggerUpdate,
    // Utils (used by some inline HTML)
    escapeHtml, showToast, relativeTime, simpleMarkdown, toggleToolDetail,
    agentProfile, memoryType, safeSalience, renderMarkdown, formatBytes,
});

// ── Initialize ──
// Sync theme dropdown (modules run after DOM is ready, so no DOMContentLoaded needed)
var _themeSel = document.getElementById('themeSelect');
if (_themeSel) _themeSel.value = localStorage.getItem('mnemonic-theme') || 'midnight';

async function initializeApp() {
    loadStats();
    loadInsights();
    loadProjects();
    connectWebSocket();
    checkForUpdate();
    setInterval(loadStats, CONFIG.STATS_POLL);
    setInterval(loadInsights, CONFIG.INSIGHTS_POLL);
    setInterval(checkForUpdate, 3600000);
    setInterval(function() {
        document.querySelectorAll('.event-time[data-ts]').forEach(function(el) {
            el.textContent = new Date(el.getAttribute('data-ts')).toLocaleString();
        });
    }, 30000);
    handleHash();
}

initializeApp();
