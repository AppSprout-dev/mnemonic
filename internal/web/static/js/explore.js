import { state, CONFIG } from './state.js';
import { apiFetch, fetchJSON, escapeHtml, showToast, memoryType, agentProfile, safeSalience, memoryTypeAbbr, memoryTypeIcon } from './utils.js';
import { relativeTime } from './forum.js';

// ── Thread View (episode detail as forum posts) ──
export async function loadThread(episodeId) {
    state.currentView = 'thread';
    state.currentEpisodeId = episodeId;
    document.querySelectorAll('.view').forEach(v => v.classList.remove('active'));
    document.querySelectorAll('.ntab').forEach(t => t.classList.remove('active'));
    var threadView = document.getElementById('view-thread');
    if (threadView) threadView.classList.add('active');
    window.location.hash = 'thread/' + episodeId;

    // Update breadcrumbs
    var crumbs = document.getElementById('breadcrumbs');
    if (crumbs) crumbs.innerHTML = '<a href="#" onclick="switchView(\'explore\')">mnemonic</a><span class="sep">›</span> <a href="#" onclick="window._loadMemSec(\'episodes\')">Episodes</a><span class="sep">›</span> Episode';

    // Show compose box and set up a forum thread for this episode
    var compose = document.getElementById('threadCompose');
    if (compose) { compose.style.display = ''; window.ensureMentionAutocomplete('threadReplyContent'); }
    // Use episode ID as the forum thread ID so replies and @mentions work
    state.currentForumThread = 'episode-' + episodeId;

    var container = document.getElementById('threadContent');
    container.innerHTML = '<div style="padding:16px;color:var(--text-dim)">Loading thread...</div>';

    try {
        var epResp = await fetchJSON('/episodes/' + episodeId);
        var ep = epResp.episode || epResp; // endpoint wraps in {episode: {...}}
        // Fetch memories for this episode
        var memData = await fetchJSON('/memories?episode_id=' + episodeId + '&limit=100');
        var memories = memData.memories || [];

        // Fetch associations for these memories
        var assocMap = {}; // memoryID -> [{target_summary, strength, relation_type}]
        if (memories.length > 0) {
            var memIds = memories.map(function(m) { return m.id; }).join(',');
            try {
                var assocResp = await window.forumFetch('/api/v1/associations?memory_ids=' + memIds);
                var assocData = await assocResp.json();
                (assocData.associations || []).forEach(function(a) {
                    // Index by source — show what each memory links to
                    if (!assocMap[a.source_id]) assocMap[a.source_id] = [];
                    assocMap[a.source_id].push({ summary: a.target_summary, strength: a.strength, type: a.relation_type });
                    // Index by target — show what links back
                    if (!assocMap[a.target_id]) assocMap[a.target_id] = [];
                    assocMap[a.target_id].push({ summary: a.source_summary, strength: a.strength, type: a.relation_type });
                });
            } catch (e) { /* associations are non-critical */ }
        }

        // Build thread header
        var html = '<div class="thread-wrap">';
        html += '<div class="thread-top">';
        html += '<div class="thread-title-big">' + escapeHtml(ep.title || 'Untitled Episode') + '</div>';
        html += '<div class="thread-meta">';
        if (ep.emotional_tone) html += '<span><b>Mood:</b> ' + escapeHtml(ep.emotional_tone) + '</span>';
        if (ep.start_time && ep.end_time) {
            html += '<span><b>Duration:</b> ' + new Date(ep.start_time).toLocaleTimeString([], {hour:'2-digit',minute:'2-digit'}) + ' – ' + new Date(ep.end_time).toLocaleTimeString([], {hour:'2-digit',minute:'2-digit'}) + '</span>';
        }
        var rawCount = (ep.raw_memory_ids || []).length;
        html += '<span><b>Memories:</b> ' + memories.length + (rawCount > memories.length ? ' <span style="color:var(--text-dim)">(' + rawCount + ' observations)</span>' : '') + '</span>';
        if (ep.files_modified && ep.files_modified.length > 0) {
            html += '<span><b>Files:</b> ' + ep.files_modified.slice(0, 5).map(function(f) { return escapeHtml(f.split('/').pop()); }).join(', ') + '</span>';
        }
        html += '</div></div>';

        if (memories.length === 0) {
            // No encoded memories linked to this episode yet
            // Show a "Daemon" post with the episode summary as content
            var rawCount = (ep.raw_memory_ids || []).length;
            html += '<div class="post bg1"><div class="inner">';
            html += '<dl class="postprofile">';
            html += '<dt><div class="avatar-container" style="width:48px;height:48px;border-radius:6px;display:flex;align-items:center;justify-content:center;font-size:1.1rem;font-weight:bold;font-family:var(--mono);background:rgba(154,114,184,0.15);color:var(--accent-violet);border:1px solid rgba(154,114,184,0.25)">EP</div>';
            html += '<strong style="color:var(--accent-violet)">Episoding Agent</strong></dt>';
            html += '<dd class="profile-rank"><span class="rank-general">episode</span></dd>';
            html += '<dd><strong>Events:</strong> ' + rawCount + '</dd>';
            if (ep.emotional_tone) html += '<dd><strong>Mood:</strong> ' + escapeHtml(ep.emotional_tone) + '</dd>';
            if (ep.project) html += '<dd><strong>Project:</strong> ' + escapeHtml(ep.project) + '</dd>';
            html += '</dl>';
            html += '<div class="postbody">';
            html += '<h3>' + escapeHtml(ep.title || 'Episode') + '</h3>';
            html += '<p class="post-meta"><span>Posted by <strong style="color:var(--accent-violet)">Episoding Agent</strong> &middot; Episode Clustering</span><span>' + rawCount + ' raw observations</span></p>';
            if (ep.summary) html += '<div class="content">' + escapeHtml(ep.summary) + '</div>';
            if (ep.narrative) html += '<div class="content" style="margin-top:8px;color:var(--text-dim);font-style:italic">' + escapeHtml(ep.narrative) + '</div>';
            if (ep.files_modified && ep.files_modified.length > 0) {
                html += '<div class="post-tags" style="margin-top:8px">';
                ep.files_modified.forEach(function(f) { html += '<span class="forum-tag">' + escapeHtml(f.split('/').pop()) + '</span>'; });
                html += '</div>';
            }
            if (ep.concepts && ep.concepts.length > 0) {
                html += '<div class="post-tags">';
                ep.concepts.forEach(function(c) { html += '<span class="forum-tag">' + escapeHtml(c) + '</span>'; });
                html += '</div>';
            }
            html += '<div style="margin-top:12px;padding:8px;font-size:0.78rem;color:var(--text-dim);background:rgba(255,255,255,0.02);border-radius:4px">Encoded memories will appear here after consolidation links them to this episode. ' + rawCount + ' raw observations are pending encoding.</div>';
            html += '</div></div></div>';
        }
        if (memories.length > 0) {
            memories.forEach(function(m, idx) {
                var type = memoryType(m);
                var rankClass = 'rank-' + type;
                var salPct = safeSalience(m.salience);
                var memAssocs = assocMap[m.id] || [];
                var linkCount = memAssocs.length;
                var source = m.source || 'mcp';
                var agent = agentProfile(source);
                var time = m.created_at ? new Date(m.created_at).toLocaleTimeString([], {hour:'2-digit',minute:'2-digit'}) : '';
                var joinDate = m.created_at ? new Date(m.created_at).toLocaleDateString() : '';
                var concepts = m.concepts || [];
                var bgClass = idx % 2 === 0 ? 'bg1' : 'bg2';

                // phpBB-style post with agent profile sidebar
                html += '<div class="post ' + bgClass + '"><div class="inner">';

                // Agent profile sidebar
                html += '<dl class="postprofile">';
                html += '<dt><div class="avatar-container" style="width:48px;height:48px;border-radius:6px;display:flex;align-items:center;justify-content:center;font-size:1.1rem;font-weight:bold;font-family:var(--mono);background:color-mix(in srgb, ' + agent.color + ' 15%, transparent);color:' + agent.color + ';border:1px solid color-mix(in srgb, ' + agent.color + ' 25%, transparent)">' + agent.icon + '</div>';
                html += '<strong style="color:' + agent.color + '">' + escapeHtml(agent.name) + '</strong></dt>';
                html += '<dd class="profile-rank"><span class="' + rankClass + '">' + escapeHtml(type) + '</span></dd>';
                html += '<dd><strong>Salience:</strong> ' + salPct + '%</dd>';
                html += '<dd><strong>Links:</strong> ' + linkCount + '</dd>';
                if (m.project) html += '<dd><strong>Project:</strong> ' + escapeHtml(m.project) + '</dd>';
                html += '<dd><strong>Observed:</strong> ' + joinDate + '</dd>';
                html += '</dl>';

                // Post body
                html += '<div class="postbody">';
                html += '<h3>' + escapeHtml(m.summary || 'Memory') + '</h3>';
                html += '<p class="post-meta"><span>Posted by <strong style="color:' + agent.color + '">' + escapeHtml(agent.name) + '</strong> &middot; ' + escapeHtml(agent.title) + ' &middot; ' + time + '</span>';
                html += '<span style="cursor:pointer;color:var(--link)" onclick="navigator.clipboard.writeText(\'' + escapeHtml(m.id || '') + '\');showToast(\'ID copied\')">#' + (idx + 1) + '</span></p>';

                // Content
                if (m.content && m.content !== m.summary) {
                    html += '<div class="content">' + escapeHtml(m.content) + '</div>';
                } else if (m.summary) {
                    html += '<div class="content">' + escapeHtml(m.summary) + '</div>';
                }

                // Concept tags
                if (concepts.length > 0) {
                    html += '<div class="post-tags">';
                    concepts.forEach(function(c) { html += '<span class="forum-tag">' + escapeHtml(c) + '</span>'; });
                    html += '</div>';
                }

                // Association quotes (linked memories)
                if (memAssocs.length > 0) {
                    var shown = memAssocs.slice(0, 5); // cap at 5 to avoid clutter
                    shown.forEach(function(a) {
                        if (!a.summary) return;
                        var strengthPct = Math.round(a.strength * 100);
                        html += '<blockquote class="quote">';
                        html += '<div class="quote-header">\u2197 ' + escapeHtml(a.type || 'linked') + ' (' + strengthPct + '%)</div>';
                        html += '<div class="quote-body">' + escapeHtml(a.summary) + '</div>';
                        html += '</blockquote>';
                    });
                }

                html += '</div></div></div>'; // close postbody, inner, post
            });
        }

        // Episode narrative at bottom
        if (ep.narrative) {
            html += '<div style="padding:10px 12px;font-size:0.92rem;color:var(--text-dim);border-top:1px solid var(--border-color);background:var(--bg-secondary)">';
            html += '<div style="font-size:0.75rem;font-weight:bold;color:var(--text-faint);margin-bottom:4px;text-transform:uppercase;letter-spacing:0.04em">Episode Narrative</div>';
            html += escapeHtml(ep.narrative);
            html += '</div>';
        }

        html += '</div>';
        container.innerHTML = html;

        // Update breadcrumbs with title
        if (crumbs) crumbs.innerHTML = '<a href="#" onclick="switchView(\'explore\')">mnemonic</a><span class="sep">›</span> <a href="#" onclick="switchView(\'explore\')">Forum</a><span class="sep">›</span> ' + escapeHtml((ep.title || 'Episode').substring(0, 50));
    } catch (e) {
        console.error('loadThread error:', e);
        container.innerHTML = '<div style="padding:16px;color:var(--accent-red)">Failed to load episode: ' + escapeHtml(e.message || String(e)) + '</div>';
    }
}

export async function submitFeedback(quality, event) {
    event.stopPropagation();
    if (!state.lastQueryId) return;
    var bar = event.target.closest('.feedback-bar');
    bar.querySelectorAll('.feedback-btn').forEach(function(b) { b.classList.remove('selected'); });
    event.target.classList.add('selected');
    try {
        await apiFetch(CONFIG.API_BASE + '/feedback', {
            method: 'POST',
            headers: { 'Content-Type': 'application/json' },
            body: JSON.stringify({ query_id: state.lastQueryId, quality: quality }),
        });
        showToast('Feedback recorded: ' + quality, 'success');
    } catch (e) {
        showToast('Failed to submit feedback', 'error');
    }
}


// ── Explore ──
export async function loadExploreTab(tab) {
    var section = document.getElementById('section-' + tab);
    section.innerHTML = '<div class="skeleton skeleton-card"></div><div class="skeleton skeleton-card"></div><div class="skeleton skeleton-card"></div>';
    try {
        switch (tab) {
            case 'episodes': await loadEpisodes(section); break;
            case 'memories': await loadMemories(section); break;
            case 'patterns': await loadPatterns(section); break;
            case 'abstractions': await loadAbstractions(section); break;
        }
        state.exploreLoaded[tab] = true;
    } catch (e) {
        section.innerHTML = '<div class="empty-state"><div class="empty-state-icon">&#9888;</div><div class="empty-state-text">Failed to load ' + tab + '</div></div>';
    }
}

export async function loadEpisodes(section) {
    var data = await fetchJSON('/episodes?limit=50');
    var episodes = data.episodes || [];
    if (episodes.length === 0) { section.innerHTML = '<div class="empty-state"><div class="empty-state-icon">&#128218;</div><div class="empty-state-text">No episodes yet</div><div class="empty-state-sub">Episodes are created as memories accumulate</div></div>'; return; }
    // phpBB-style header + row list
    var html = '<ul class="topiclist"><li class="header"><dl class="row-item"><dt><div class="list-inner">Episode</div></dt><dd>Mem</dd><dd>Files</dd><dd class="lastpost">Last Activity</dd></dl></li></ul>';
    html += '<ul class="topiclist forums">';
    html += episodes.map(function(ep, idx) {
        var concepts = (ep.concepts || []).slice(0, 4);
        var memCount = (ep.raw_memory_ids || []).length;
        var fileCount = (ep.files_modified || []).length;
        var dateStr = ep.start_time ? new Date(ep.start_time).toLocaleTimeString([], {hour:'2-digit',minute:'2-digit'}) : '';
        var epTitle = ep.title || (ep.state === 'open' ? 'Episode in progress\u2026' : 'Untitled Episode');
        var isNew = ep.state === 'open';
        var tone = ep.emotional_tone || ep.outcome || '';
        var tags = concepts.map(function(c) { return '<span class="forum-tag">' + escapeHtml(c) + '</span>'; }).join('');
        var sub = ep.summary ? escapeHtml(ep.summary) : '';
        var expandId = 'exp-ep-' + idx;
        var bgClass = idx % 2 === 0 ? 'bg1' : 'bg2';

        var row = '<li class="row ' + bgClass + '" onclick="toggle(\'' + expandId + '\')">';
        row += '<dl class="row-item"><dt>';
        row += '<a class="row-item-link"><span class="status-icon icon-ep' + (isNew ? ' unread' : '') + '">EP</span></a>';
        row += '<div class="list-inner">';
        row += '<a class="forumtitle">' + escapeHtml(epTitle) + '</a>';
        row += '<span class="forum-desc">' + sub + ' ' + tags + '</span>';
        row += '</div></dt>';
        row += '<dd>' + memCount + ' <dfn>memories</dfn></dd>';
        row += '<dd>' + fileCount + ' <dfn>files</dfn></dd>';
        row += '<dd class="lastpost"><span class="lastpost-time">' + dateStr + '</span><br><span class="lastpost-by">' + escapeHtml(tone) + '</span></dd>';
        row += '</dl></li>';

        // Expandable zone
        row += '<div class="expand-zone" id="' + expandId + '">';
        row += '<div class="expand-header"><span>' + memCount + ' observations in this episode</span><a href="#" onclick="event.preventDefault();event.stopPropagation();loadThread(\'' + escapeHtml(ep.id) + '\')">View full thread &rarr;</a></div>';
        if (ep.narrative) {
            row += '<div style="padding:6px 10px 6px 42px;font-size:0.82rem;color:var(--text-secondary);border-bottom:1px solid var(--border-subtle)">' + escapeHtml(ep.narrative) + '</div>';
        }
        row += '</div>';
        return row;
    }).join('');
    html += '</ul>';
    section.innerHTML = html;
}

export async function loadMemories(section) {
    var data = await fetchJSON('/memories?limit=50&state=active');
    var memories = data.memories || [];
    if (memories.length === 0) { section.innerHTML = '<div class="empty-state"><div class="empty-state-icon">&#129504;</div><div class="empty-state-text">No memories yet</div><div class="empty-state-sub">Use the Remember panel to add your first memory</div></div>'; return; }
    var html = '<ul class="topiclist"><li class="header"><dl class="row-item"><dt><div class="list-inner">Memory</div></dt><dd>Sal</dd><dd>Links</dd><dd class="lastpost">Created</dd></dl></li></ul>';
    html += '<ul class="topiclist forums">';
    html += memories.map(function(m, idx) {
        var type = memoryType(m);
        var typeAbbr = memoryTypeAbbr(type);
        var iconClass = memoryTypeIcon(type);
        var salPct = safeSalience(m.salience);
        var salClass = salPct >= 60 ? 'sal-hi' : salPct >= 30 ? 'sal-mid' : 'sal-lo';
        var linkCount = m.association_count || 0;
        var dateStr = m.created_at ? new Date(m.created_at).toLocaleTimeString([], {hour:'2-digit',minute:'2-digit'}) : '';
        var summary = m.summary || m.content || '';
        var concepts = (m.concepts || []).slice(0, 4);
        var tags = concepts.map(function(c) { return '<span class="forum-tag">' + escapeHtml(c) + '</span>'; }).join('');
        var source = m.source || 'mcp';
        var bgClass = idx % 2 === 0 ? 'bg1' : 'bg2';

        var row = '<li class="row ' + bgClass + ' ' + salClass + '" onclick="toggleExpand(this)">';
        row += '<dl class="row-item"><dt>';
        row += '<a class="row-item-link"><span class="status-icon ' + iconClass + '">' + typeAbbr + '</span></a>';
        row += '<div class="list-inner">';
        row += '<a class="forumtitle">' + escapeHtml(summary) + '</a>';
        row += '<span class="forum-desc"><span class="forum-tags">' + tags + '</span></span>';
        row += '</div></dt>';
        row += '<dd>' + salPct + '% <dfn>salience</dfn></dd>';
        row += '<dd>' + linkCount + ' <dfn>links</dfn></dd>';
        var agent = agentProfile(source);
        row += '<dd class="lastpost"><span class="lastpost-time">' + dateStr + '</span><br><span class="lastpost-by" style="color:' + agent.color + '">' + escapeHtml(agent.name) + '</span></dd>';
        row += '</dl></li>';

        // Expandable detail
        row += '<div class="expand-zone">';
        if (m.content && m.content !== summary) {
            row += '<div style="padding:6px 10px 6px 42px;font-size:0.82rem;color:var(--text-secondary);border-bottom:1px solid var(--border-subtle)">' + escapeHtml(m.content) + '</div>';
        }
        row += '<div style="padding:4px 10px 4px 42px;font-size:0.72rem;color:var(--text-dim)">';
        row += 'ID: <span style="cursor:pointer" onclick="event.stopPropagation();navigator.clipboard.writeText(\'' + escapeHtml(m.id || '') + '\');showToast(\'ID copied\')">' + escapeHtml((m.id || '').substring(0, 8)) + '...</span>';
        if (m.project) row += ' &middot; Project: ' + escapeHtml(m.project);
        if (m.access_count > 0) row += ' &middot; Recalled ' + m.access_count + 'x';
        row += ' &middot; ' + escapeHtml(m.state || 'active');
        row += '</div></div>';
        return row;
    }).join('');
    html += '</ul>';
    section.innerHTML = html;
}

export async function loadPatterns(section) {
    var data = await fetchJSON('/patterns?limit=20');
    var patterns = data.patterns || [];
    if (patterns.length === 0) { section.innerHTML = '<div class="empty-state"><div class="empty-state-icon">&#128200;</div><div class="empty-state-text">No patterns discovered yet</div><div class="empty-state-sub">Patterns emerge after consolidation cycles</div></div>'; return; }
    var html = '<ul class="topiclist"><li class="header"><dl class="row-item"><dt><div class="list-inner">Pattern</div></dt><dd>Str</dd><dd>Evidence</dd><dd class="lastpost">Discovered</dd></dl></li></ul>';
    html += '<ul class="topiclist forums">';
    html += patterns.map(function(p, idx) {
        var strengthPct = Math.round((p.strength || 0) * 100);
        var concepts = (p.concepts || []).slice(0, 4);
        var evidenceCount = (p.evidence_ids || []).length;
        var age = p.created_at ? relativeTime(p.created_at) : '';
        var tags = concepts.map(function(c) { return '<span class="forum-tag">' + escapeHtml(c) + '</span>'; }).join('');
        var bgClass = idx % 2 === 0 ? 'bg1' : 'bg2';

        var row = '<li class="row ' + bgClass + ' sticky pattern-card" data-pattern-id="' + escapeHtml(p.id) + '">';
        row += '<dl class="row-item"><dt>';
        row += '<a class="row-item-link"><span class="status-icon icon-ep" style="background:rgba(208,152,46,0.12);color:var(--decision);border-color:rgba(208,152,46,0.2)">PT</span></a>';
        row += '<div class="list-inner">';
        row += '<a class="forumtitle">' + escapeHtml(p.title || 'Untitled') + '</a>';
        row += '<span class="forum-desc">' + escapeHtml(p.description || '') + ' ' + tags + '</span>';
        row += '</div></dt>';
        row += '<dd>' + strengthPct + '%</dd>';
        row += '<dd>' + evidenceCount + '</dd>';
        row += '<dd class="lastpost"><span class="lastpost-time">' + age + '</span><br><button class="btn-forum" style="font-size:0.68rem;padding:1px 6px" onclick="event.stopPropagation();archivePattern(\'' + escapeHtml(p.id) + '\', this)">Archive</button></dd>';
        row += '</dl></li>';
        return row;
    }).join('');
    html += '</ul>';
    section.innerHTML = html;
}

export async function archivePattern(id, btn) {
    try {
        var resp = await fetch(CONFIG.API_BASE + '/patterns/' + id, {
            method: 'PATCH',
            headers: {'Content-Type': 'application/json'},
            body: JSON.stringify({state: 'archived'})
        });
        if (!resp.ok) { showToast('Failed to archive pattern', 'error'); return; }
        var card = btn.closest('.pattern-card');
        if (card) { card.style.transition = 'opacity 0.3s'; card.style.opacity = '0'; setTimeout(function() { card.remove(); }, 300); }
        showToast('Pattern archived');
    } catch(e) { showToast('Failed to archive pattern', 'error'); }
}

export async function loadAbstractions(section) {
    var data = await fetchJSON('/abstractions?limit=20');
    var abstractions = data.abstractions || [];
    if (abstractions.length === 0) { section.innerHTML = '<div class="empty-state"><div class="empty-state-icon">&#128161;</div><div class="empty-state-text">No abstractions yet</div><div class="empty-state-sub">Abstractions form from patterns during dream cycles</div></div>'; return; }
    var html = '<ul class="topiclist"><li class="header"><dl class="row-item"><dt><div class="list-inner">Abstraction</div></dt><dd>Conf</dd><dd>Sources</dd><dd class="lastpost">Discovered</dd></dl></li></ul>';
    html += '<ul class="topiclist forums">';
    html += abstractions.map(function(a, idx) {
        var levelLabel = a.level === 3 ? 'Axiom' : a.level === 2 ? 'Principle' : 'L' + a.level;
        var confPct = Math.round((a.confidence || 0) * 100);
        var concepts = (a.concepts || []).slice(0, 4);
        var sourceCount = (a.source_pattern_ids || []).length;
        var age = a.created_at ? relativeTime(a.created_at) : '';
        var tags = concepts.map(function(c) { return '<span class="forum-tag">' + escapeHtml(c) + '</span>'; }).join('');
        var bgClass = idx % 2 === 0 ? 'bg1' : 'bg2';
        var iconLabel = a.level === 3 ? 'AX' : a.level === 2 ? 'PR' : 'AB';

        var row = '<li class="row ' + bgClass + ' announce abstraction-card">';
        row += '<dl class="row-item"><dt>';
        row += '<a class="row-item-link"><span class="status-icon" style="background:rgba(154,114,184,0.12);color:var(--insight);border:1px solid rgba(154,114,184,0.2)">' + iconLabel + '</span></a>';
        row += '<div class="list-inner">';
        row += '<a class="forumtitle">' + escapeHtml(a.title || 'Untitled') + ' <span style="font-size:0.72rem;color:var(--insight);font-weight:normal">[' + levelLabel + ']</span></a>';
        row += '<span class="forum-desc">' + escapeHtml(a.description || '') + ' ' + tags + '</span>';
        row += '</div></dt>';
        row += '<dd>' + confPct + '%</dd>';
        row += '<dd>' + sourceCount + '</dd>';
        row += '<dd class="lastpost"><span class="lastpost-time">' + age + '</span></dd>';
        row += '</dl></li>';
        return row;
    }).join('');
    html += '</ul>';
    section.innerHTML = html;
}

export function filterExplore() {
    var query = document.getElementById('exploreSearch').value.toLowerCase();
    var section = document.getElementById('section-' + state.currentExploreTab);
    var rows = section.querySelectorAll('li.row, .pattern-card, .abstraction-card');
    rows.forEach(function(row) { row.style.display = row.textContent.toLowerCase().includes(query) ? '' : 'none'; });
}

