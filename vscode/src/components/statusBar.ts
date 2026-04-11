import * as vscode from "vscode";
import type { ConnectionMonitor } from "./connectionMonitor";
import type { HealthResponse } from "../api/types";

/** Priority for the status bar item (lower = further right). */
const STATUS_BAR_PRIORITY = 50;

/**
 * Manages the status bar item that shows Mnemonic connection state.
 */
export class StatusBarManager implements vscode.Disposable {
  private readonly item: vscode.StatusBarItem;
  private readonly disposables: vscode.Disposable[] = [];

  constructor(monitor: ConnectionMonitor) {
    this.item = vscode.window.createStatusBarItem(
      vscode.StatusBarAlignment.Left,
      STATUS_BAR_PRIORITY
    );
    this.item.command = "mnemonic.showSidebar";
    this.item.name = "Mnemonic";

    this.disposables.push(
      monitor.onDidChangeState((state) => {
        if (state === "connected") {
          const health = monitor.getLastHealth();
          this.showConnected(health);
        } else if (state === "disconnected") {
          this.showDisconnected();
        } else {
          this.showConnecting();
        }
      })
    );

    this.disposables.push(
      monitor.onDidReceiveHealth((health) => {
        if (monitor.getState() === "connected") {
          this.showConnected(health);
        }
      })
    );

    // Initial state
    this.showConnecting();
    this.item.show();
  }

  private showConnected(health: HealthResponse | undefined): void {
    const count = health?.memory_count ?? 0;
    const formatted = count.toLocaleString();
    this.item.text = `$(brain) ${formatted}`;
    this.item.tooltip = health
      ? `Mnemonic v${health.version}\n${formatted} memories\nUptime: ${formatUptime(health.uptime_seconds)}\nLLM: ${health.llm_available ? health.llm_model || "available" : "unavailable"}`
      : "Mnemonic: Connected";
    this.item.backgroundColor = undefined;
  }

  private showDisconnected(): void {
    this.item.text = "$(brain) Offline";
    this.item.tooltip = "Mnemonic daemon is not running. Click to open sidebar.";
    this.item.backgroundColor = new vscode.ThemeColor(
      "statusBarItem.warningBackground"
    );
  }

  private showConnecting(): void {
    this.item.text = "$(brain) ...";
    this.item.tooltip = "Connecting to Mnemonic daemon...";
    this.item.backgroundColor = undefined;
  }

  dispose(): void {
    this.item.dispose();
    for (const d of this.disposables) {
      d.dispose();
    }
  }
}

const SECONDS_PER_MINUTE = 60;
const SECONDS_PER_HOUR = 3600;

function formatUptime(seconds: number): string {
  if (seconds < SECONDS_PER_MINUTE) {
    return `${Math.floor(seconds)}s`;
  }
  if (seconds < SECONDS_PER_HOUR) {
    return `${Math.floor(seconds / SECONDS_PER_MINUTE)}m`;
  }
  const hours = Math.floor(seconds / SECONDS_PER_HOUR);
  const mins = Math.floor((seconds % SECONDS_PER_HOUR) / SECONDS_PER_MINUTE);
  return `${hours}h ${mins}m`;
}
