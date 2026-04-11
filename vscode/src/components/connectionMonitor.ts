import * as vscode from "vscode";
import { MnemonicClient } from "../api/client";
import type { HealthResponse } from "../api/types";
import * as logger from "../util/logger";

export type ConnectionState = "connected" | "disconnected" | "connecting";

const BASE_POLL_MS = 30_000;
const MAX_BACKOFF_MS = 120_000;

/**
 * Monitors the connection to the Mnemonic daemon via periodic health checks.
 * Emits state changes so other components can react.
 */
export class ConnectionMonitor implements vscode.Disposable {
  private readonly _onDidChangeState =
    new vscode.EventEmitter<ConnectionState>();
  readonly onDidChangeState = this._onDidChangeState.event;

  private readonly _onDidReceiveHealth =
    new vscode.EventEmitter<HealthResponse>();
  readonly onDidReceiveHealth = this._onDidReceiveHealth.event;

  private state: ConnectionState = "connecting";
  private lastHealth: HealthResponse | undefined;
  private timer: ReturnType<typeof setInterval> | undefined;
  private consecutiveFailures = 0;
  private pollIntervalMs: number;

  constructor(
    private readonly client: MnemonicClient,
    pollIntervalMs?: number
  ) {
    this.pollIntervalMs = pollIntervalMs ?? BASE_POLL_MS;
  }

  getState(): ConnectionState {
    return this.state;
  }

  getLastHealth(): HealthResponse | undefined {
    return this.lastHealth;
  }

  start(): void {
    // Initial check immediately
    void this.poll();
    this.scheduleNext();
  }

  private scheduleNext(): void {
    if (this.timer !== undefined) {
      clearTimeout(this.timer);
    }
    const delay =
      this.consecutiveFailures > 0
        ? Math.min(
            this.pollIntervalMs * Math.pow(2, this.consecutiveFailures - 1),
            MAX_BACKOFF_MS
          )
        : this.pollIntervalMs;
    this.timer = setTimeout(() => {
      void this.poll();
      this.scheduleNext();
    }, delay);
  }

  private async poll(): Promise<void> {
    try {
      const health = await this.client.health();
      this.lastHealth = health;
      this.consecutiveFailures = 0;
      if (this.state !== "connected") {
        this.state = "connected";
        this._onDidChangeState.fire(this.state);
        logger.info(
          `Connected to Mnemonic daemon v${health.version} (${health.memory_count} memories)`
        );
      }
      this._onDidReceiveHealth.fire(health);
    } catch {
      this.consecutiveFailures++;
      this.lastHealth = undefined;
      if (this.state !== "disconnected") {
        this.state = "disconnected";
        this._onDidChangeState.fire(this.state);
        logger.warn("Disconnected from Mnemonic daemon");
      }
    }
  }

  dispose(): void {
    if (this.timer !== undefined) {
      clearTimeout(this.timer);
      this.timer = undefined;
    }
    this._onDidChangeState.dispose();
    this._onDidReceiveHealth.dispose();
  }
}
