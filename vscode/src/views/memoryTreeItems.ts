import * as vscode from "vscode";
import type { Memory, MemoryType } from "../api/types";

const TYPE_ICONS: Record<MemoryType, vscode.ThemeIcon> = {
  decision: new vscode.ThemeIcon("milestone"),
  error: new vscode.ThemeIcon("error"),
  insight: new vscode.ThemeIcon("lightbulb"),
  learning: new vscode.ThemeIcon("mortar-board"),
  general: new vscode.ThemeIcon("note"),
};

const TYPE_LABELS: Record<MemoryType, string> = {
  decision: "Decisions",
  error: "Errors",
  insight: "Insights",
  learning: "Learnings",
  general: "General",
};

/**
 * Collapsible section header that groups memories by type.
 */
export class MemorySectionItem extends vscode.TreeItem {
  readonly memories: Memory[];

  constructor(type: MemoryType, memories: Memory[]) {
    super(
      `${TYPE_LABELS[type]} (${memories.length})`,
      vscode.TreeItemCollapsibleState.Expanded
    );
    this.memories = memories;
    this.iconPath = TYPE_ICONS[type];
    this.contextValue = "memorySection";
  }
}

/**
 * Leaf node representing a single memory.
 */
export class MemoryItem extends vscode.TreeItem {
  readonly memoryId: string;

  constructor(memory: Memory, score?: number) {
    const label = memory.summary || memory.gist || truncate(memory.content, 80);
    super(label, vscode.TreeItemCollapsibleState.None);

    this.memoryId = memory.id;
    this.iconPath = TYPE_ICONS[memory.type] || TYPE_ICONS.general;
    this.description = memory.type;
    this.tooltip = buildTooltip(memory, score);
    this.contextValue = "memoryItem";

    this.command = {
      command: "mnemonic.openMemoryDetail",
      title: "View Memory Detail",
      arguments: [memory.id],
    };
  }
}

/**
 * Message item shown when the sidebar has no data or is in an error/offline state.
 */
export class MessageItem extends vscode.TreeItem {
  constructor(message: string, icon?: string) {
    super(message, vscode.TreeItemCollapsibleState.None);
    if (icon) {
      this.iconPath = new vscode.ThemeIcon(icon);
    }
    this.contextValue = "message";
  }
}

function truncate(text: string, maxLen: number): string {
  if (text.length <= maxLen) {
    return text;
  }
  return text.slice(0, maxLen - 1) + "\u2026";
}

function buildTooltip(memory: Memory, score?: number): string {
  const lines = [];
  if (memory.summary) {
    lines.push(memory.summary);
  }
  if (memory.gist && memory.gist !== memory.summary) {
    lines.push(memory.gist);
  }
  lines.push(`Type: ${memory.type}`);
  lines.push(`Source: ${memory.source}`);
  lines.push(`Salience: ${memory.salience.toFixed(2)}`);
  if (score !== undefined) {
    lines.push(`Relevance: ${score.toFixed(2)}`);
  }
  if (memory.concepts?.length) {
    lines.push(`Concepts: ${memory.concepts.join(", ")}`);
  }
  lines.push(`Created: ${new Date(memory.created_at).toLocaleString()}`);
  return lines.join("\n");
}
