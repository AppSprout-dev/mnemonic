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
