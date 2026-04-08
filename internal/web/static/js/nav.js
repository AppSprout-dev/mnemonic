import { state } from './state.js';

export function setTheme(name) {
    document.documentElement.setAttribute('data-theme', name);
    localStorage.setItem('mnemonic-theme', name);
    var sel = document.getElementById('themeSelect');
    if (sel) sel.value = name;
}

// ── View Switching ──
export function switchView(name) {
    state.currentView = name;
    document.querySelectorAll('.view').forEach(v => v.classList.remove('active'));
    document.querySelectorAll('.nav-tab, .ntab').forEach(t => t.classList.remove('active'));
    var viewEl = document.getElementById('view-' + name);
    if (viewEl) viewEl.classList.add('active');
    var tab = document.querySelector('.ntab[data-view="' + name + '"]') || document.querySelector('.nav-tab[data-view="' + name + '"]');
    if (tab) tab.classList.add('active');
    window.location.hash = name;
    // Update breadcrumbs
    var crumbMap = { recall: 'Search', explore: 'Forum', timeline: 'Timeline', agent: 'SDK', llm: 'LLM Usage', tools: 'Tools', models: 'Models' };
    var bc = document.getElementById('breadcrumbs');
    if (bc && crumbMap[name]) bc.innerHTML = '<a href="#" onclick="switchView(\'explore\')">mnemonic</a><span class="sep"> › </span>' + crumbMap[name];
    if (name === 'explore') {
        window.loadForumIndex();
        var wp = document.getElementById('forumWelcome');
        if (wp) wp.style.display = '';
    }
    if (name === 'timeline' && !state.timelineInitialized) { window.populateTimelineProjects(); window.loadTimelineData(false); }
    if (name === 'agent' && !state.agentLoaded) window.loadAgentData();
    if (name === 'llm' && !state.llmLoaded) window.loadLLMUsage();
    if (name === 'tools' && !state.toolsLoaded) window.loadToolUsage();
    if (name === 'models' && !state.modelsLoaded) window.loadModels();
}

export function switchExploreTab(tab) {
    // Forum view shows all blocks simultaneously (no tabs)
    // This function now just triggers a lazy load if needed
    state.currentExploreTab = tab;
    if (!state.exploreLoaded[tab]) window.loadExploreTab(tab);
}

export function handleHash() {
    var hash = window.location.hash.replace('#', '');
    if (hash.startsWith('thread/')) {
        var epId = hash.substring(7);
        if (epId) window.loadThread(epId);
        return;
    }
    if (hash.startsWith('forum-thread/')) {
        var threadId = hash.substring(13);
        if (threadId) window.loadForumThread(threadId);
        return;
    }
    if (hash.startsWith('memory-section/')) {
        var secId = hash.substring(15);
        var secNames = { episodes: 'Episodes', memories: 'Recent Memories', patterns: 'Discovered Patterns', abstractions: 'Abstractions & Principles' };
        if (secId && secNames[secId]) window.loadMemorySection(secId, secNames[secId]);
        return;
    }
    if (hash.startsWith('forum-group/')) {
        var groupType = hash.substring(12);
        if (groupType) window.loadForumGroup(groupType);
        return;
    }
    if (hash.startsWith('forum-category/')) {
        var catId = hash.substring(15);
        if (catId) window.loadForumCategory(catId, catId);
        return;
    }
    if (['recall', 'explore', 'timeline', 'agent', 'llm', 'tools', 'models'].includes(hash)) switchView(hash);
}
window.addEventListener('hashchange', handleHash);

// ── Keyboard Shortcuts ──
document.addEventListener('keydown', function(e) {
    var tag = document.activeElement.tagName;
    if (tag === 'INPUT' || tag === 'TEXTAREA' || tag === 'SELECT') { if (e.key === 'Escape') document.activeElement.blur(); return; }
    switch (e.key) {
        case '/': e.preventDefault(); switchView('recall'); document.getElementById('recallInput').focus(); break;
        case 'Escape': if (state.drawerOpen) window.toggleDrawer(); break;
        case '1': switchView('recall'); break;
        case '2': switchView('explore'); break;
        case '3': switchView('timeline'); break;
        case '4': switchView('agent'); break;
        case '5': switchView('llm'); break;
        case '6': switchView('tools'); break;
        case '7': switchView('models'); break;
    }
});

