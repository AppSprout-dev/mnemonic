// TypeScript interfaces mirroring the Mnemonic daemon's Go JSON structs.

export interface HealthResponse {
  status: string;
  version: string;
  llm_available: boolean;
  llm_model: string;
  store_healthy: boolean;
  memory_count: number;
  tool_count: number;
  uptime_seconds: number;
  db_size_mb: number;
  timestamp: string;
}

export interface Memory {
  id: string;
  raw_id: string;
  type: MemoryType;
  content: string;
  summary: string;
  gist: string;
  concepts: string[];
  salience: number;
  source: string;
  project: string;
  state: string;
  session_id: string;
  created_at: string;
  accessed_at: string;
}

export type MemoryType = "decision" | "error" | "insight" | "learning" | "general";

export interface RetrievalResult {
  memory: Memory;
  score: number;
  explanation?: string;
}

export interface Pattern {
  id: string;
  description: string;
  strength: number;
  occurrences: number;
  state: string;
}

export interface Abstraction {
  id: string;
  content: string;
  level: string;
  confidence: number;
  state: string;
}

export interface QueryResponse {
  query_id: string;
  memories: RetrievalResult[];
  patterns?: Pattern[];
  abstractions?: Abstraction[];
  synthesis?: string;
  took_ms: number;
}

export interface QueryParams {
  query: string;
  limit?: number;
  synthesize?: boolean;
  include_reasoning?: boolean;
  project?: string;
  source?: string;
  type?: string;
  types?: string[];
  state?: string;
  min_salience?: number;
  include_patterns?: boolean;
  include_abstractions?: boolean;
  time_from?: string;
  time_to?: string;
}

export interface ByFileResponse {
  path: string;
  file_results: Memory[];
  symbol_results?: SymbolMatchGroup[];
  total_results: number;
}

export interface SymbolMatchGroup {
  symbol: string;
  memories: Memory[];
}

export interface MemoryContext {
  memory: Memory;
  resolution?: {
    gist: string;
    narrative: string;
  };
  concept_set?: Record<string, string[]>;
  attributes?: Record<string, unknown>;
  episode?: {
    id: string;
    theme: string;
    state: string;
  };
  timestamp: string;
}

export interface CreateMemoryRequest {
  content: string;
  source: string;
  type?: MemoryType;
  project?: string;
}

export interface CreateMemoryResponse {
  id: string;
  timestamp: string;
  source: string;
}

export interface FeedbackRequest {
  query_id: string;
  quality: "helpful" | "partial" | "irrelevant";
}

export interface FeedbackResponse {
  status: string;
  adjustments: number;
}

export interface ActivityResponse {
  concepts: Record<string, string>;
  window_minutes: number;
}

export interface BatchQueryRequest {
  queries: BatchQueryItem[];
}

export interface BatchQueryItem {
  query: string;
  limit?: number;
  project?: string;
  source?: string;
  type?: string;
  types?: string[];
  state?: string;
  min_salience?: number;
  include_patterns?: boolean;
  include_abstractions?: boolean;
}

export interface BatchQueryResultItem {
  query: string;
  query_id?: string;
  memories?: RetrievalResult[];
  patterns?: Pattern[];
  abstractions?: Abstraction[];
  took_ms?: number;
  error?: string;
}

export interface BatchQueryResponse {
  results: BatchQueryResultItem[];
}

// --- Phase 2 types ---

// WebSocket event payloads
export interface MemoryEncodedPayload {
  memory_id: string;
  raw_id: string;
  concepts: string[];
  associations_created: number;
  timestamp: string;
}

export interface ConsolidationCompletedPayload {
  duration_ms: number;
  memories_processed: number;
  memories_decayed: number;
  patterns_extracted: number;
  timestamp: string;
}

export interface PatternDiscoveredPayload {
  pattern_id: string;
  title: string;
  pattern_type: string;
  project?: string;
  evidence_count: number;
  timestamp: string;
}

export interface MemoryAmendedPayload {
  memory_id: string;
  new_summary: string;
  timestamp: string;
}

export interface WebSocketMessage {
  type: string;
  timestamp: string;
  payload: unknown;
}

// Session summary response
export interface SessionSummaryResponse {
  session_id: string;
  episode?: {
    id: string;
    summary?: string;
    event_count: number;
  };
  memory_count: number;
  breakdown: {
    decisions: number;
    errors: number;
    insights: number;
    learnings: number;
    general: number;
  };
  recent_items: Array<{
    id: string;
    summary: string;
    type?: string;
    timestamp: string;
  }>;
  timestamp: string;
}

// Amend / Archive responses
export interface AmendResponse {
  status: string;
  memory_id: string;
}

export interface ArchiveResponse {
  status: string;
  memory_id: string;
}

// Project recall response
export interface RecallProjectResponse {
  project: string;
  summary?: Record<string, unknown>;
  patterns?: Pattern[];
  memories: Memory[];
  timestamp: string;
}

// Proactive context response
export interface ContextSuggestion {
  id: string;
  summary: string;
  concepts: string[];
  source: string;
  type: string;
  salience: number;
  created_at: string;
}

export interface ContextResponse {
  recent_events: number;
  themes: string[];
  suggestions: ContextSuggestion[];
  timestamp: string;
}

// Analytics / encoding quality response
export interface EncodingQualityWindow {
  mean_epr: number;
  ted_rate: number;
  flagged_rate: number;
  sample_count: number;
}

export interface EncodingQualityEntry {
  memory_id: string;
  summary: string;
  source: string;
  epr: number;
  fr: number;
  flags: string[];
  salience: number;
  created_at: string;
}

export interface AnalyticsResponse {
  pipeline: {
    total_raw: number;
    total_encoded: number;
    encoding_rate: number;
  };
  encoding_quality: EncodingQualityWindow;
  recent_encodings: EncodingQualityEntry[];
  timestamp: string;
}

// Episodes response
export interface Episode {
  id: string;
  title: string;
  summary: string;
  state: string;
  project: string;
  concepts: string[];
  duration_sec: number;
}

export interface EpisodesResponse {
  episodes: Episode[];
  count: number;
  timestamp: string;
}

// Patterns response
export interface PatternsResponse {
  patterns: Pattern[];
  count: number;
  timestamp: string;
}
