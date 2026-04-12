import * as vscode from "vscode";
import { MnemonicClient, MnemonicApiError } from "../api/client";
import type { ConnectionMonitor } from "./connectionMonitor";
import * as logger from "../util/logger";

const ERROR_PATTERNS = [
  /(?:^|\n)\s*(?:Error|ERROR|error)[:\s]/,
  /(?:^|\n)\s*(?:panic|PANIC)[:\s]/,
  /(?:^|\n)\s*(?:FAIL|FAILED|Failed)[:\s]/,
  /(?:^|\n)\s*Traceback \(most recent call last\)/,
  /(?:^|\n)\s*goroutine\s+\d+\s+\[/,
  /(?:^|\n)\s*Caused by:/,
  /(?:^|\n)\s*npm ERR!/,
  /(?:^|\n)\s*(?:ModuleNotFoundError|ImportError|SyntaxError):/,
];

/** Maximum buffer size per terminal (bytes). */
const BUFFER_MAX = 4096;

/** Debounce delay after detecting an error before querying (ms). */
const ERROR_DEBOUNCE_MS = 500;

/** Maximum length of error text sent in a query. */
const MAX_QUERY_LEN = 300;

/**
 * Monitors terminal output for error patterns and queries Mnemonic
 * for related error memories. Uses vscode.window.onDidWriteTerminalData
 * which is available since VS Code 1.93.
 */
export class TerminalErrorMonitor implements vscode.Disposable {
  private readonly buffers = new Map<number, string>();
  private readonly debounceTimers = new Map<number, ReturnType<typeof setTimeout>>();
  private readonly disposables: vscode.Disposable[] = [];
  private terminalIdCounter = 0;
  private readonly terminalIds = new WeakMap<vscode.Terminal, number>();

  constructor(
    private readonly client: MnemonicClient,
    private readonly monitor: ConnectionMonitor
  ) {}

  start(): void {
    // Runtime-detect the API — not available before VS Code 1.93
    // eslint-disable-next-line @typescript-eslint/no-explicit-any
    const windowAny = vscode.window as any;
    if (typeof windowAny.onDidWriteTerminalData !== "function") {
      logger.info(
        "Terminal error monitoring disabled: onDidWriteTerminalData not available (requires VS Code 1.93+)"
      );
      return;
    }

    this.disposables.push(
      windowAny.onDidWriteTerminalData(
        (e: { terminal: vscode.Terminal; data: string }) => {
          this.onTerminalData(e.terminal, e.data);
        }
      )
    );

    logger.info("Terminal error monitoring enabled");
  }

  private getTerminalId(terminal: vscode.Terminal): number {
    let id = this.terminalIds.get(terminal);
    if (id === undefined) {
      id = this.terminalIdCounter++;
      this.terminalIds.set(terminal, id);
    }
    return id;
  }

  private onTerminalData(terminal: vscode.Terminal, data: string): void {
    const id = this.getTerminalId(terminal);

    // Accumulate in ring buffer
    let buffer = (this.buffers.get(id) ?? "") + data;
    if (buffer.length > BUFFER_MAX) {
      buffer = buffer.slice(buffer.length - BUFFER_MAX);
    }
    this.buffers.set(id, buffer);

    // Check for error patterns
    if (!this.matchesErrorPattern(buffer)) {
      return;
    }

    // Debounce — errors often span multiple writes
    const existing = this.debounceTimers.get(id);
    if (existing !== undefined) {
      clearTimeout(existing);
    }
    this.debounceTimers.set(
      id,
      setTimeout(() => {
        this.debounceTimers.delete(id);
        void this.queryRelatedErrors(id, buffer);
      }, ERROR_DEBOUNCE_MS)
    );
  }

  private matchesErrorPattern(text: string): boolean {
    return ERROR_PATTERNS.some((re) => re.test(text));
  }

  private async queryRelatedErrors(terminalId: number, buffer: string): Promise<void> {
    if (this.monitor.getState() !== "connected") {
      return;
    }

    // Extract the most relevant error text (last portion of buffer)
    const errorText = buffer.slice(-MAX_QUERY_LEN).trim();
    if (!errorText) {
      return;
    }

    // Clear buffer to avoid re-triggering on the same error
    this.buffers.set(terminalId, "");

    try {
      const resp = await this.client.query({
        query: errorText,
        limit: 5,
        type: "error",
        include_patterns: false,
        include_abstractions: false,
      });

      if (resp.memories.length > 0) {
        const action = await vscode.window.showInformationMessage(
          `Mnemonic: Found ${resp.memories.length} related error ${resp.memories.length === 1 ? "memory" : "memories"}`,
          "View",
          "Dismiss"
        );

        if (action === "View") {
          // Show memories in a quick pick
          const items = resp.memories.map((r) => ({
            label: r.memory.summary || r.memory.gist || r.memory.content.slice(0, 80),
            description: `${r.memory.type} \u2022 ${r.score.toFixed(2)}`,
            detail: r.memory.content.slice(0, 120),
            memoryId: r.memory.id,
          }));

          const selected = await vscode.window.showQuickPick(items, {
            placeHolder: "Select a memory to view details",
          });

          if (selected) {
            await vscode.commands.executeCommand(
              "mnemonic.openMemoryDetail",
              selected.memoryId
            );
          }
        }
      }
    } catch (err) {
      if (err instanceof MnemonicApiError) {
        logger.debug("Terminal error query failed", err.message);
      }
    }
  }

  dispose(): void {
    for (const timer of this.debounceTimers.values()) {
      clearTimeout(timer);
    }
    this.debounceTimers.clear();
    this.buffers.clear();
    for (const d of this.disposables) {
      d.dispose();
    }
  }
}
