export const state = {
    currentView: 'recall',
    currentExploreTab: 'episodes',
    lastQueryId: null,
    lastQueryResults: [],
    events: [],
    unreadEvents: 0,
    drawerOpen: false,
    rememberOpen: false,
    timelineInitialized: false,
    previousStats: {},
    ws: null,
    wsReconnectAttempts: 0,
    exploreLoaded: { episodes: false, memories: false, patterns: false, abstractions: false },
    projectList: [],
    agentLoaded: false,
    agentData: null,
    llmLoaded: false,
    toolsLoaded: false,
    dreamSessionTotals: { cycles: 0, replayed: 0, strengthened: 0, newLinks: 0, insights: 0, crossProject: 0, demoted: 0 },
    forumLoaded: false,
    currentForumThread: null,
    currentEpisodeId: '',
};

export const CONFIG = {
    API_BASE: `${window.location.origin}/api/v1`,
    WS_URL: `${window.location.protocol === 'https:' ? 'wss:' : 'ws:'}//${window.location.host}/ws`,
    STATS_POLL: 30000,
    INSIGHTS_POLL: 60000,
    MAX_EVENTS: 100,
    TOKEN: new URLSearchParams(window.location.search).get('token') || '',
};

export var MONTHS = ['Jan','Feb','Mar','Apr','May','Jun','Jul','Aug','Sep','Oct','Nov','Dec'];
