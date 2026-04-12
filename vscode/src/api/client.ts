import type {
  HealthResponse,
  QueryParams,
  QueryResponse,
  ByFileResponse,
  MemoryContext,
  CreateMemoryRequest,
  CreateMemoryResponse,
  FeedbackRequest,
  FeedbackResponse,
  ActivityResponse,
  BatchQueryRequest,
  BatchQueryResponse,
  AmendResponse,
  ArchiveResponse,
  SessionSummaryResponse,
} from "./types";

export class MnemonicApiError extends Error {
  constructor(
    public readonly status: number,
    public readonly code: string,
    message: string
  ) {
    super(message);
    this.name = "MnemonicApiError";
  }
}

export class MnemonicClient {
  private endpoint: string;
  private token: string;
  private disposed = false;

  constructor(endpoint: string, token: string = "") {
    this.endpoint = endpoint.replace(/\/+$/, "");
    this.token = token;
  }

  updateConfig(endpoint: string, token: string = ""): void {
    this.endpoint = endpoint.replace(/\/+$/, "");
    this.token = token;
  }

  dispose(): void {
    this.disposed = true;
  }

  async health(): Promise<HealthResponse> {
    return this.get<HealthResponse>("/api/v1/health");
  }

  async query(params: QueryParams): Promise<QueryResponse> {
    return this.post<QueryResponse>("/api/v1/query", params, 60_000);
  }

  async batchQuery(request: BatchQueryRequest): Promise<BatchQueryResponse> {
    return this.post<BatchQueryResponse>(
      "/api/v1/query/batch",
      request,
      60_000
    );
  }

  async getMemoriesByFile(
    path: string,
    symbols?: string[]
  ): Promise<ByFileResponse> {
    const params = new URLSearchParams({ path });
    if (symbols) {
      for (const sym of symbols) {
        params.append("symbols", sym);
      }
    }
    return this.get<ByFileResponse>(
      `/api/v1/memories/by-file?${params.toString()}`
    );
  }

  async getMemoryContext(id: string): Promise<MemoryContext> {
    return this.get<MemoryContext>(`/api/v1/memories/${id}/context`);
  }

  async createMemory(request: CreateMemoryRequest): Promise<CreateMemoryResponse> {
    return this.post<CreateMemoryResponse>("/api/v1/memories", request);
  }

  async submitFeedback(request: FeedbackRequest): Promise<FeedbackResponse> {
    return this.post<FeedbackResponse>("/api/v1/feedback", request);
  }

  async getActivity(): Promise<ActivityResponse> {
    return this.get<ActivityResponse>("/api/v1/activity");
  }

  async amendMemory(id: string, correctedContent: string): Promise<AmendResponse> {
    return this.post<AmendResponse>(`/api/v1/memories/${id}/amend`, {
      corrected_content: correctedContent,
    });
  }

  async archiveMemory(id: string): Promise<ArchiveResponse> {
    return this.post<ArchiveResponse>(`/api/v1/memories/${id}/archive`, {});
  }

  async getSessionSummary(sessionId: string = "current"): Promise<SessionSummaryResponse> {
    return this.get<SessionSummaryResponse>(`/api/v1/sessions/${sessionId}/summary`);
  }

  getEndpoint(): string {
    return this.endpoint;
  }

  private async get<T>(path: string, timeoutMs: number = 10_000): Promise<T> {
    return this.request<T>("GET", path, undefined, timeoutMs);
  }

  private async post<T>(
    path: string,
    body: unknown,
    timeoutMs: number = 10_000
  ): Promise<T> {
    return this.request<T>("POST", path, body, timeoutMs);
  }

  private async request<T>(
    method: string,
    path: string,
    body: unknown,
    timeoutMs: number
  ): Promise<T> {
    if (this.disposed) {
      throw new MnemonicApiError(0, "DISPOSED", "Client has been disposed");
    }

    const url = `${this.endpoint}${path}`;
    const headers: Record<string, string> = {
      "Content-Type": "application/json",
    };
    if (this.token) {
      headers["Authorization"] = `Bearer ${this.token}`;
    }

    const controller = new AbortController();
    const timeout = setTimeout(() => controller.abort(), timeoutMs);

    try {
      const resp = await fetch(url, {
        method,
        headers,
        body: body ? JSON.stringify(body) : undefined,
        signal: controller.signal,
      });

      if (!resp.ok) {
        const errorBody = await resp.json().catch(() => ({}));
        throw new MnemonicApiError(
          resp.status,
          (errorBody as Record<string, string>).code || "UNKNOWN",
          (errorBody as Record<string, string>).error ||
            `HTTP ${resp.status}: ${resp.statusText}`
        );
      }

      return (await resp.json()) as T;
    } catch (err: unknown) {
      if (err instanceof MnemonicApiError) {
        throw err;
      }
      // AbortController abort throws an error with name "AbortError"
      if (err instanceof Error && err.name === "AbortError") {
        throw new MnemonicApiError(0, "TIMEOUT", `Request timed out after ${timeoutMs}ms`);
      }
      throw new MnemonicApiError(
        0,
        "NETWORK_ERROR",
        `Failed to connect to ${this.endpoint}: ${err instanceof Error ? err.message : "unknown error"}`
      );
    } finally {
      clearTimeout(timeout);
    }
  }
}
