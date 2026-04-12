import * as vscode from "vscode";
import { MnemonicClient, MnemonicApiError } from "../api/client";
import type { Memory, MemoryType } from "../api/types";
import type { ConnectionMonitor } from "../components/connectionMonitor";
import { LRUCache } from "../util/cache";
import {
  MemorySectionItem,
  MemoryItem,
  MessageItem,
  PatternSectionItem,
  PatternItem,
  EpisodeSectionItem,
  EpisodeItem,
  QualityItem,
} from "./memoryTreeItems";
import type {
  Pattern,
  Episode,
  EncodingQualityWindow,
} from "../api/types";
import * as logger from "../util/logger";

const MEMORY_TYPE_ORDER: MemoryType[] = [
  "decision",
  "error",
  "insight",
  "learning",
  "general",
];

type TreeNode = MemorySectionItem | MemoryItem | MessageItem;

/**
 * TreeDataProvider for the "Related Memories" view.
 * Shows memories related to the currently active file.
 */
export class RelatedMemoriesProvider
  implements vscode.TreeDataProvider<TreeNode>
{
  private readonly _onDidChangeTreeData = new vscode.EventEmitter<
    TreeNode | undefined
  >();
  readonly onDidChangeTreeData = this._onDidChangeTreeData.event;

  private currentPath: string | undefined;
  private memories: Memory[] = [];
  private errorMessage: string | undefined;
  private readonly cache: LRUCache<string, Memory[]>;

  constructor(
    private readonly client: MnemonicClient,
    private readonly monitor: ConnectionMonitor,
    cacheSize: number = 50,
    cacheTtlMs: number = 60_000
  ) {
    this.cache = new LRUCache(cacheSize, cacheTtlMs);
  }

  async updateForFile(filePath: string | undefined): Promise<void> {
    this.currentPath = filePath;
    this.errorMessage = undefined;

    if (!filePath || this.monitor.getState() !== "connected") {
      this.memories = [];
      this._onDidChangeTreeData.fire(undefined);
      return;
    }

    // Check cache
    const cached = this.cache.get(filePath);
    if (cached) {
      this.memories = cached;
      this._onDidChangeTreeData.fire(undefined);
      return;
    }

    try {
      const resp = await this.client.getMemoriesByFile(filePath);
      const allMemories = [
        ...resp.file_results,
        ...(resp.symbol_results?.flatMap((sg) => sg.memories) ?? []),
      ];
      // Deduplicate by ID
      const seen = new Set<string>();
      this.memories = allMemories.filter((m) => {
        if (seen.has(m.id)) {
          return false;
        }
        seen.add(m.id);
        return true;
      });
      this.cache.set(filePath, this.memories);
    } catch (err) {
      if (err instanceof MnemonicApiError) {
        this.errorMessage = err.message;
      } else {
        this.errorMessage = "Failed to fetch related memories";
      }
      this.memories = [];
      logger.warn("Related memories fetch failed", filePath, err);
    }

    this._onDidChangeTreeData.fire(undefined);
  }

  refresh(): void {
    this.cache.clear();
    if (this.currentPath) {
      void this.updateForFile(this.currentPath);
    }
  }

  /** Clear cache and re-fetch current file. Called by WebSocket events. */
  invalidateAndRefresh(): void {
    this.cache.clear();
    if (this.currentPath) {
      void this.updateForFile(this.currentPath);
    }
  }

  getTreeItem(element: TreeNode): vscode.TreeItem {
    return element;
  }

  getChildren(element?: TreeNode): TreeNode[] {
    // Children of a section
    if (element instanceof MemorySectionItem) {
      return element.memories.map((m) => new MemoryItem(m));
    }

    // Root level
    if (this.monitor.getState() === "disconnected") {
      return [new MessageItem("Mnemonic daemon is not running", "warning")];
    }

    if (this.errorMessage) {
      return [new MessageItem(this.errorMessage, "error")];
    }

    if (!this.currentPath) {
      return [new MessageItem("Open a file to see related memories", "info")];
    }

    if (this.memories.length === 0) {
      return [new MessageItem("No related memories found", "info")];
    }

    // Group by type
    return groupByType(this.memories);
  }
}

type ProjectTreeNode = TreeNode | PatternSectionItem | PatternItem | EpisodeSectionItem | EpisodeItem | QualityItem;

/**
 * TreeDataProvider for the "Project Context" view.
 * Shows active patterns, current episodes, encoding quality, and top memories.
 */
export class ProjectContextProvider
  implements vscode.TreeDataProvider<ProjectTreeNode>
{
  private readonly _onDidChangeTreeData = new vscode.EventEmitter<
    ProjectTreeNode | undefined
  >();
  readonly onDidChangeTreeData = this._onDidChangeTreeData.event;

  private memories: Memory[] = [];
  private patterns: Pattern[] = [];
  private episodes: Episode[] = [];
  private quality: EncodingQualityWindow | undefined;
  private errorMessage: string | undefined;
  private refreshTimer: ReturnType<typeof setInterval> | undefined;

  constructor(
    private readonly client: MnemonicClient,
    private readonly monitor: ConnectionMonitor,
    private readonly refreshIntervalMs: number = 60_000
  ) {}

  start(): void {
    void this.fetchContext();
    this.refreshTimer = setInterval(() => {
      if (this.monitor.getState() === "connected") {
        void this.fetchContext();
      }
    }, this.refreshIntervalMs);
  }

  private async fetchContext(): Promise<void> {
    if (this.monitor.getState() !== "connected") {
      return;
    }

    this.errorMessage = undefined;

    // Fetch all data sources in parallel
    const [memoriesResult, patternsResult, episodesResult, analyticsResult] =
      await Promise.allSettled([
        this.client.query({
          query: "recent decisions and insights",
          limit: 10,
          include_patterns: false,
          include_abstractions: false,
          min_salience: 0.3,
          types: ["decision", "insight"],
        }),
        this.client.getPatterns(),
        this.client.getEpisodes("open"),
        this.client.getAnalytics(),
      ]);

    // Process memories
    if (memoriesResult.status === "fulfilled") {
      this.memories = memoriesResult.value.memories.map((r) => r.memory);
    } else {
      this.memories = [];
      logger.warn("Project context memories fetch failed", memoriesResult.reason);
    }

    // Process patterns
    if (patternsResult.status === "fulfilled") {
      this.patterns = patternsResult.value.patterns ?? [];
    } else {
      this.patterns = [];
    }

    // Process episodes
    if (episodesResult.status === "fulfilled") {
      this.episodes = episodesResult.value.episodes ?? [];
    } else {
      this.episodes = [];
    }

    // Process analytics/quality
    if (analyticsResult.status === "fulfilled") {
      this.quality = analyticsResult.value.encoding_quality;
    } else {
      this.quality = undefined;
    }

    // Set error only if all fetches failed
    if (
      memoriesResult.status === "rejected" &&
      patternsResult.status === "rejected"
    ) {
      const err = memoriesResult.reason;
      this.errorMessage =
        err instanceof MnemonicApiError
          ? err.message
          : "Failed to fetch project context";
    }

    this._onDidChangeTreeData.fire(undefined);
  }

  refresh(): void {
    void this.fetchContext();
  }

  /** Called by WebSocket consolidation_completed events for live refresh. */
  refreshFromEvent(): void {
    void this.fetchContext();
  }

  getTreeItem(element: ProjectTreeNode): vscode.TreeItem {
    return element;
  }

  getChildren(element?: ProjectTreeNode): ProjectTreeNode[] {
    // Children of sections
    if (element instanceof MemorySectionItem) {
      return element.memories.map((m) => new MemoryItem(m));
    }
    if (element instanceof PatternSectionItem) {
      return element.patterns.map((p) => new PatternItem(p));
    }
    if (element instanceof EpisodeSectionItem) {
      return element.episodes.map((e) => new EpisodeItem(e));
    }

    // Root level
    if (this.monitor.getState() === "disconnected") {
      return [new MessageItem("Mnemonic daemon is not running", "warning")];
    }

    if (this.errorMessage) {
      return [new MessageItem(this.errorMessage, "error")];
    }

    const items: ProjectTreeNode[] = [];

    // Encoding quality summary (Gap 5)
    if (this.quality && this.quality.sample_count > 0) {
      items.push(new QualityItem(this.quality));
    }

    // Active patterns
    if (this.patterns.length > 0) {
      items.push(new PatternSectionItem(this.patterns));
    }

    // Open episodes
    if (this.episodes.length > 0) {
      items.push(new EpisodeSectionItem(this.episodes));
    }

    // Recent memories grouped by type
    if (this.memories.length > 0) {
      items.push(...groupByType(this.memories));
    }

    if (items.length === 0) {
      return [new MessageItem("No project context available", "info")];
    }

    return items;
  }

  dispose(): void {
    if (this.refreshTimer !== undefined) {
      clearInterval(this.refreshTimer);
      this.refreshTimer = undefined;
    }
    this._onDidChangeTreeData.dispose();
  }
}

function groupByType(memories: Memory[]): MemorySectionItem[] {
  const groups = new Map<MemoryType, Memory[]>();
  for (const m of memories) {
    const type = (MEMORY_TYPE_ORDER.includes(m.type) ? m.type : "general") as MemoryType;
    const list = groups.get(type) ?? [];
    list.push(m);
    groups.set(type, list);
  }

  const sections: MemorySectionItem[] = [];
  for (const type of MEMORY_TYPE_ORDER) {
    const list = groups.get(type);
    if (list && list.length > 0) {
      sections.push(new MemorySectionItem(type, list));
    }
  }
  return sections;
}
