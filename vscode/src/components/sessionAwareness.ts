import * as vscode from "vscode";
import { MnemonicClient, MnemonicApiError } from "../api/client";
import type { ConnectionMonitor } from "./connectionMonitor";
import type { MnemonicWebSocket } from "./websocketClient";
import * as logger from "../util/logger";

/**
 * Handles session lifecycle notifications.
 * Shows a "Welcome back" notification on activate if there's recent activity,
 * and subscribes to consolidation events for live project context updates.
 */
export class SessionAwareness implements vscode.Disposable {
  private readonly disposables: vscode.Disposable[] = [];
  private shown = false;

  constructor(
    private readonly client: MnemonicClient,
    private readonly ws: MnemonicWebSocket | undefined,
    private readonly monitor: ConnectionMonitor
  ) {}

  async start(): Promise<void> {
    // Wait for connection, then show welcome notification
    if (this.monitor.getState() === "connected") {
      await this.showWelcomeBack();
    } else {
      const sub = this.monitor.onDidChangeState(async (state) => {
        if (state === "connected" && !this.shown) {
          await this.showWelcomeBack();
        }
      });
      this.disposables.push(sub);
    }
  }

  private async showWelcomeBack(): Promise<void> {
    if (this.shown) {
      return;
    }
    this.shown = true;

    try {
      const summary = await this.client.getSessionSummary("current");
      if (summary.memory_count === 0) {
        return;
      }

      const parts: string[] = [];
      const b = summary.breakdown;
      if (b.decisions > 0) {
        parts.push(`${b.decisions} decision${b.decisions > 1 ? "s" : ""}`);
      }
      if (b.errors > 0) {
        parts.push(`${b.errors} error${b.errors > 1 ? "s" : ""}`);
      }
      if (b.insights > 0) {
        parts.push(`${b.insights} insight${b.insights > 1 ? "s" : ""}`);
      }

      if (parts.length > 0) {
        const msg = `Mnemonic: Welcome back! Recent activity: ${parts.join(", ")} (${summary.memory_count} total)`;
        vscode.window.showInformationMessage(msg);
      }
    } catch (err) {
      if (err instanceof MnemonicApiError) {
        logger.debug("Session summary unavailable", err.message);
      }
    }
  }

  dispose(): void {
    for (const d of this.disposables) {
      d.dispose();
    }
  }
}
