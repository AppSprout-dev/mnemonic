import { state, CONFIG, MONTHS } from './state.js';
import { apiFetch, escapeHtml, showToast, makeDayBuckets, svgEl, linScale } from './utils.js';

// ── Timeline ──
var _timelineState = {
    items: [],
    range: 'all',
    types: { episode: true, decision: true, error: true, insight: true, learning: true, general: true },
    search: '',
    projectFilter: '', // empty = all projects
    loading: false,
    allLoaded: false,
    episodeOffset: 0,
    memoryOffset: 0,
    dayFilter: null, // Date object — when set, show only items from that day
};

export function setTimelineRange(range) {
    _timelineState.range = range;
    _timelineState.dayFilter = null;
    document.querySelector('#timelineSpark').classList.remove('day-active');
    document.querySelectorAll('.timeline-range-tab').forEach(function(t) {
        t.classList.toggle('active', t.getAttribute('data-range') === range);
    });
    renderTimelineSparkline();
    renderTimelineItems();
}

export function toggleTimelineType(btn) {
    var type = btn.getAttribute('data-type');
    _timelineState.types[type] = !_timelineState.types[type];
    btn.classList.toggle('active', _timelineState.types[type]);
    renderTimelineItems();
}

export function filterTimeline() {
    _timelineState.search = document.getElementById('timelineSearch').value.toLowerCase();
    var wrap = document.getElementById('timelineSearchWrap');
    wrap.classList.toggle('has-value', _timelineState.search.length > 0);
    renderTimelineItems();
}

export function clearTimelineSearch() {
    var input = document.getElementById('timelineSearch');
    input.value = '';
    _timelineState.search = '';
    document.getElementById('timelineSearchWrap').classList.remove('has-value');
    input.focus();
    renderTimelineItems();
}

export function populateTimelineProjects() {
    var container = document.getElementById('timelineProjectFilters');
    container.innerHTML = '';
    var projects = state.projectList || [];
    if (projects.length === 0) return;
    projects.forEach(function(p) {
        var btn = document.createElement('button');
        btn.className = 'timeline-project-btn';
        btn.textContent = p;
        btn.setAttribute('data-project', p);
        btn.onclick = function() { toggleTimelineProject(p); };
        container.appendChild(btn);
    });
}

export function toggleTimelineProject(project) {
    if (_timelineState.projectFilter === project) {
        _timelineState.projectFilter = '';
    } else {
        _timelineState.projectFilter = project;
    }
    document.querySelectorAll('.timeline-project-btn').forEach(function(btn) {
        btn.classList.toggle('active', btn.getAttribute('data-project') === _timelineState.projectFilter);
    });
    renderTimelineItems();
}

export function getTimelineRangeCutoff() {
    var now = new Date();
    switch (_timelineState.range) {
        case 'today':
            var start = new Date(now); start.setHours(0,0,0,0); return start;
        case 'week':
            return new Date(now.getTime() - 7 * 86400000);
        case 'month':
            return new Date(now.getTime() - 30 * 86400000);
        default: return null;
    }
}

export function filterTimelineItems() {
    var cutoff = getTimelineRangeCutoff();
    var search = _timelineState.search;
    var types = _timelineState.types;
    var dayFilter = _timelineState.dayFilter;
    var projectFilter = _timelineState.projectFilter;
    return _timelineState.items.filter(function(item) {
        if (!types[item._kind]) return false;
        if (projectFilter && (item._project || '') !== projectFilter) return false;
        if (dayFilter) {
            var d = new Date(item._date);
            d.setHours(0, 0, 0, 0);
            if (d.getTime() !== dayFilter.getTime()) return false;
        } else if (cutoff && item._date < cutoff) {
            return false;
        }
        if (search) {
            var haystack = (item._title + ' ' + (item._concepts || []).join(' ')).toLowerCase();
            if (haystack.indexOf(search) === -1) return false;
        }
        return true;
    });
}

export function groupByDate(items) {
    var groups = [];
    var currentLabel = '';
    var today = new Date(); today.setHours(0,0,0,0);
    var yesterday = new Date(today.getTime() - 86400000);
    items.forEach(function(item) {
        var d = new Date(item._date); d.setHours(0,0,0,0);
        var label;
        if (d.getTime() === today.getTime()) label = 'Today';
        else if (d.getTime() === yesterday.getTime()) label = 'Yesterday';
        else label = d.toLocaleDateString(undefined, { weekday: 'short', month: 'short', day: 'numeric' });
        if (label !== currentLabel) {
            groups.push({ label: label, items: [] });
            currentLabel = label;
        }
        groups[groups.length - 1].items.push(item);
    });
    return groups;
}

export function renderTimelineItems() {
    var body = document.getElementById('timelineBody');
    var filtered = filterTimelineItems();
    if (filtered.length === 0) {
        body.innerHTML = '<div class="timeline-empty">No items match your filters<div class="timeline-empty-sub">Try adjusting the date range or type filters</div></div>';
        return;
    }
    var groups = groupByDate(filtered);
    var html = '';
    groups.forEach(function(group) {
        html += '<div class="tl-head"><span>' + escapeHtml(group.label) + '</span><span class="tl-head-count">' + group.items.length + ' memories</span></div>';
        group.items.forEach(function(item, idx) {
            html += renderTimelineCard(item, idx);
        });
    });
    if (!_timelineState.allLoaded) {
        html += '<div class="timeline-loading" id="timelineLoadMore">Loading more...</div>';
    }
    body.innerHTML = html;
    setupTimelineScroll();
}

export function renderTimelineCard(item, idx) {
    var kind = item._kind;
    var salPct = Math.min(100, Math.round((item._salience || 0) * 100));
    var h = item._date.getHours(), m = item._date.getMinutes();
    var ampm = h >= 12 ? 'PM' : 'AM';
    h = h % 12 || 12;
    var absTime = h + ':' + (m < 10 ? '0' : '') + m + ' ' + ampm;
    var concepts = item._concepts || [];
    var source = item._source || '';
    var project = item._project || '';
    var typeAbbr = kind === 'insight' ? 'IN' : kind === 'decision' ? 'DE' : kind === 'learning' ? 'LE' : kind === 'error' ? 'ER' : kind === 'episode' ? 'EP' : 'GN';
    var iconClass = kind === 'insight' ? 'icon-in' : kind === 'decision' ? 'icon-de' : kind === 'learning' ? 'icon-le' : kind === 'error' ? 'icon-er' : kind === 'episode' ? 'icon-ep' : 'icon-gn';

    var allTags = concepts.slice();
    if (source) allTags.unshift(source);
    if (project) allTags.unshift(project);
    var bgClass = (idx || 0) % 2 === 0 ? 'bg1' : 'bg2';

    // Simple grid row — timeline doesn't need the forum dl/dt/dd pattern
    var html = '<div class="tl-row tl-card tl-' + kind + ' ' + bgClass + '" data-id="' + escapeHtml(item._id) + '" onclick="expandTimelineCard(this)" ';
    html += 'data-concepts="' + escapeHtml(allTags.join(',')) + '">';
    html += '<span class="status-icon ' + iconClass + '" style="width:20px;height:20px;font-size:0.6rem;flex-shrink:0">' + typeAbbr + '</span>';
    html += '<span class="tl-row-title">' + escapeHtml(item._title) + '</span>';

    // Concept tags inline
    if (concepts.length > 0 || source) {
        html += '<span class="tl-card-concepts tl-row-tags">';
        if (source) {
            html += '<span class="tl-source forum-tag" onclick="toggleTimelineTag(event, this.textContent)" onmouseenter="hoverTimelineTag(this.textContent)" onmouseleave="unhoverTimelineTag()">' + escapeHtml(source) + '</span>';
        }
        concepts.slice(0, 4).forEach(function(c) {
            html += '<span class="tl-concept forum-tag" onclick="toggleTimelineTag(event, this.textContent)" onmouseenter="hoverTimelineTag(this.textContent)" onmouseleave="unhoverTimelineTag()">' + escapeHtml(c) + '</span>';
        });
        html += '</span>';
    }
    html += '<span class="tl-row-time">' + absTime + '</span>';
    html += '<span class="tl-row-sal">' + salPct + '%</span>';
    html += '</div>';

    // Expandable detail inside the same flow
    html += '<div class="tl-card-detail expand-zone">';
    if (item._type === 'episode') {
        if (item._raw.summary) html += '<div style="padding:6px 16px;font-size:0.82rem;color:var(--text-secondary)">' + escapeHtml(item._raw.summary) + '</div>';
        if (item._raw.narrative) html += '<div style="padding:4px 16px;font-size:0.82rem;color:var(--text-dim)">' + escapeHtml(item._raw.narrative) + '</div>';
    } else {
        if (item._raw.content && item._raw.content !== item._raw.summary) {
            html += '<div style="padding:6px 16px;font-size:0.82rem;color:var(--text-secondary)">' + escapeHtml(item._raw.content) + '</div>';
        }
    }
    html += '</div>';
    return html;
}

export function expandTimelineCard(el) {
    var next = el.nextElementSibling;
    if (next && (next.classList.contains('expand-zone') || next.classList.contains('tl-card-detail'))) {
        next.classList.toggle('open');
    }
}

var _selectedTimelineTags = new Set();

export function toggleTimelineTag(event, concept) {
    event.stopPropagation();
    if (_selectedTimelineTags.has(concept)) {
        _selectedTimelineTags.delete(concept);
    } else {
        _selectedTimelineTags.add(concept);
    }
    applyTimelineTagHighlight();
}

export function hoverTimelineTag(concept) {
    if (_selectedTimelineTags.size > 0) return; // don't hover-highlight when tags are pinned
    highlightTimelineCards(new Set([concept]));
}

export function unhoverTimelineTag() {
    if (_selectedTimelineTags.size > 0) return; // keep pinned selection
    clearAllTimelineHighlight();
}

export function applyTimelineTagHighlight() {
    if (_selectedTimelineTags.size === 0) {
        clearAllTimelineHighlight();
        return;
    }
    highlightTimelineCards(_selectedTimelineTags);
    // Mark selected tag pills
    document.querySelectorAll('.tl-concept, .tl-source, .tl-card-project').forEach(function(tag) {
        if (_selectedTimelineTags.has(tag.textContent)) {
            tag.classList.add('concept-selected');
        } else {
            tag.classList.remove('concept-selected');
        }
    });
}

export function highlightTimelineCards(activeTags) {
    var cards = document.querySelectorAll('.tl-card');
    cards.forEach(function(card) {
        var cardConcepts = (card.getAttribute('data-concepts') || '').split(',');
        var match = true;
        activeTags.forEach(function(tag) {
            if (cardConcepts.indexOf(tag) === -1) match = false;
        });
        if (match) {
            card.classList.add('highlighted');
            card.classList.remove('dimmed');
        } else {
            card.classList.add('dimmed');
            card.classList.remove('highlighted');
        }
    });
    document.querySelectorAll('.tl-concept, .tl-source, .tl-card-project').forEach(function(tag) {
        if (activeTags.has(tag.textContent)) {
            tag.classList.add('concept-active');
        } else {
            tag.classList.remove('concept-active');
        }
    });
}

export function clearAllTimelineHighlight() {
    document.querySelectorAll('.tl-card').forEach(function(card) {
        card.classList.remove('dimmed', 'highlighted');
    });
    document.querySelectorAll('.tl-concept, .tl-source, .tl-card-project').forEach(function(tag) {
        tag.classList.remove('concept-active', 'concept-selected');
    });
}

export async function loadTimelineData(append) {
    if (_timelineState.loading) return;
    _timelineState.loading = true;
    try {
        var epLimit = 50;
        var memLimit = 50;
        if (!append) {
            _timelineState.items = [];
            _timelineState.episodeOffset = 0;
            _timelineState.memoryOffset = 0;
            _timelineState.allLoaded = false;
        }
        var epUrl = CONFIG.API_BASE + '/episodes?state=closed&limit=' + epLimit + '&offset=' + _timelineState.episodeOffset;
        var memUrl = CONFIG.API_BASE + '/memories?state=active&limit=' + memLimit + '&offset=' + _timelineState.memoryOffset;
        var results = await Promise.all([
            apiFetch(epUrl).then(function(r) { return r.json(); }),
            apiFetch(memUrl).then(function(r) { return r.json(); })
        ]);
        var episodes = results[0].episodes || [];
        var memories = results[1].memories || [];

        episodes.forEach(function(ep) {
            if (!ep.title || !ep.title.trim()) return;
            _timelineState.items.push({
                _id: ep.id,
                _type: 'episode',
                _kind: 'episode',
                _title: ep.title,
                _date: new Date(ep.start_time || ep.end_time),
                _concepts: ep.concepts || [],
                _tone: ep.emotional_tone || '',
                _salience: 0,
                _fileCount: (ep.files_modified || []).length,
                _eventCount: (ep.raw_memory_ids || []).length,
                _project: ep.project || '',
                _raw: ep,
            });
        });
        memories.forEach(function(m) {
            if (!m.summary || !m.summary.trim()) return;
            var kind = m.type || m.memory_type || 'general';
            if (!_timelineState.types.hasOwnProperty(kind)) kind = 'general';
            _timelineState.items.push({
                _id: m.id,
                _type: 'memory',
                _kind: kind,
                _title: m.summary,
                _date: new Date(m.timestamp || m.created_at),
                _concepts: m.concepts || [],
                _source: m.source || '',
                _tone: '',
                _salience: m.salience || 0,
                _fileCount: 0,
                _eventCount: 0,
                _project: m.project || '',
                _raw: m,
            });
        });

        _timelineState.items.sort(function(a, b) { return b._date.getTime() - a._date.getTime(); });

        var seen = {};
        _timelineState.items = _timelineState.items.filter(function(item) {
            if (seen[item._id]) return false;
            seen[item._id] = true;
            return true;
        });

        _timelineState.episodeOffset += episodes.length;
        _timelineState.memoryOffset += memories.length;
        if (episodes.length < epLimit && memories.length < memLimit) {
            _timelineState.allLoaded = true;
        }

        renderTimelineItems();
        renderTimelineSparkline();
        state.timelineInitialized = true;
    } catch (e) {
        console.error('Failed to load timeline:', e);
        showToast('Failed to load timeline', 'error');
    }
    _timelineState.loading = false;
}

export function setupTimelineScroll() {
    var body = document.getElementById('timelineBody');
    body.onscroll = function() {
        if (_timelineState.allLoaded || _timelineState.loading) return;
        if (body.scrollTop + body.clientHeight >= body.scrollHeight - 200) {
            loadTimelineData(true);
        }
    };
}

export function renderTimelineSparkline() {
    var container = document.getElementById('timelineSpark');
    var items = _timelineState.items;
    if (items.length === 0) {
        container.innerHTML = '<div class="timeline-spark-empty">No activity data</div>';
        return;
    }

    var now = new Date();
    var dayMs = 86400000;
    var buckets = makeDayBuckets(30, { count: 0 });
    items.forEach(function(item) {
        var d = new Date(item._date);
        d.setHours(0, 0, 0, 0);
        var diff = Math.floor((now.getTime() - d.getTime()) / dayMs);
        if (diff >= 0 && diff < 30) {
            buckets[29 - diff].count++;
        }
    });

    var maxCount = Math.max.apply(null, buckets.map(function(b) { return b.count; }));
    if (maxCount === 0) maxCount = 1;

    container.innerHTML = '';
    var w = container.clientWidth;
    var h = 40;

    // Tooltip element
    var tooltip = document.createElement('div');
    tooltip.className = 'spark-tooltip';
    container.appendChild(tooltip);

    var svg = svgEl('svg', { width: w, height: h });
    svg.style.display = 'block';
    container.appendChild(svg);

    var nb = buckets.length;
    var bandStep = w / nb;
    var bandPad = 0.3;
    var bandW = bandStep * (1 - bandPad);
    var bandOff = bandStep * bandPad / 2;

    var dayFilter = _timelineState.dayFilter;

    function fmtDate(d) {
        return MONTHS[d.getMonth()] + ' ' + d.getDate();
    }

    buckets.forEach(function(d, i) {
        var bx = i * bandStep + bandOff;
        var barY = d.count > 0 ? linScale(d.count, 0, maxCount, h - 2, 4) : h - 2;
        var barH = d.count > 0 ? h - 2 - barY : 2;

        // Compute fill and opacity
        var fill, opacity;
        if (dayFilter && dayFilter.getTime() === d.date.getTime()) {
            fill = 'var(--accent-cyan)';
            opacity = 1;
        } else if (dayFilter) {
            fill = d.count > 0 ? 'var(--accent-cyan)' : 'var(--border-subtle)';
            opacity = d.count > 0 ? 0.2 : 0.15;
        } else {
            fill = d.count > 0 ? 'var(--accent-cyan)' : 'var(--border-subtle)';
            var isToday = d.idx === 29;
            if (d.count > 0) opacity = isToday ? 1 : 0.3 + 0.7 * (d.count / maxCount);
            else opacity = isToday ? 0.25 : 0.15;
        }

        // Visual bar
        svg.appendChild(svgEl('rect', { x: bx, y: barY, width: bandW, height: barH, rx: 1, fill: fill, opacity: String(opacity), 'pointer-events': 'none' }));

        // Hit area
        var hit = svgEl('rect', { x: bx - bandW * 0.15, y: 0, width: bandW * 1.3, height: h, fill: 'transparent', cursor: d.count > 0 ? 'pointer' : 'default' });
        (function(dd) {
            hit.addEventListener('mouseover', function() {
                if (dd.count === 0) return;
                tooltip.textContent = fmtDate(dd.date) + ' \u00B7 ' + dd.count + ' memor' + (dd.count === 1 ? 'y' : 'ies');
                tooltip.style.display = 'block';
                var cx = dd.idx * bandStep + bandStep / 2;
                tooltip.style.left = Math.min(cx - tooltip.offsetWidth / 2, w - tooltip.offsetWidth - 4) + 'px';
            });
            hit.addEventListener('mouseout', function() { tooltip.style.display = 'none'; });
            hit.addEventListener('click', function() {
                if (dd.count === 0) return;
                var isActive = dayFilter && dayFilter.getTime() === dd.date.getTime();
                if (isActive) {
                    _timelineState.dayFilter = null;
                    dayFilter = null;
                    container.classList.remove('day-active');
                } else {
                    _timelineState.dayFilter = dd.date;
                    dayFilter = dd.date;
                    container.classList.add('day-active');
                }
                tooltip.style.display = 'none';
                renderTimelineSparkline();
                renderTimelineItems();
            });
        })(d);
        svg.appendChild(hit);
    });
}

