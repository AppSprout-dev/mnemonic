import * as vscode from "vscode";
import WebSocket from "ws";
import type {
  WebSocketMessage,
  MemoryEncodedPayload,
  ConsolidationCompletedPayload,
  PatternDiscoveredPayload,
  MemoryAmendedPayload,
} from "../api/types";
import type { ConnectionMonitor } from "./connectionMonitor";
import * as logger from "../util/logger";

export type WSConnectionState = "connected" | "disconnected" | "reconnecting";

const BASE_RECONNECT_MS = 1_000;
const MAX_RECONNECT_MS = 60_000;
const JITTER_FACTOR = 0.25;
/** If no ping from server within this period, consider connection stale. */
const STALE_TIMEOUT_MS = 45_000;

/**
 * Manages a persistent WebSocket connection to the Mnemonic daemon.
 * Provides typed event emitters for specific daemon events and handles
 * automatic reconnection with exponential backoff + jitter.
 */
export class MnemonicWebSocket implements vscode.Disposable {
  private readonly _onDidChangeState = new vscode.EventEmitter<WSConnectionState>();
  readonly onDidChangeState = this._onDidChangeState.event;

  private readonly _onMemoryEncoded = new vscode.EventEmitter<MemoryEncodedPayload>();
  readonly onMemoryEncoded = this._onMemoryEncoded.event;

  private readonly _onConsolidationCompleted = new vscode.EventEmitter<ConsolidationCompletedPayload>();
  readonly onConsolidationCompleted = this._onConsolidationCompleted.event;

  private readonly _onPatternDiscovered = new vscode.EventEmitter<PatternDiscoveredPayload>();
  readonly onPatternDiscovered = this._onPatternDiscovered.event;

  private readonly _onMemoryAmended = new vscode.EventEmitter<MemoryAmendedPayload>();
  readonly onMemoryAmended = this._onMemoryAmended.event;

  private ws: WebSocket | undefined;
  private state: WSConnectionState = "disconnected";
  private reconnectAttempt = 0;
  private reconnectTimer: ReturnType<typeof setTimeout> | undefined;
  private staleTimer: ReturnType<typeof setTimeout> | undefined;
  private disposed = false;
  private monitorSub: vscode.Disposable | undefined;

  constructor(
    private wsUrl: string,
    private readonly monitor: ConnectionMonitor
  ) {
    // Coordinate with ConnectionMonitor
    this.monitorSub = this.monitor.onDidChangeState((httpState) => {
      if (httpState === "connected" && this.state !== "connected") {
        this.reconnectAttempt = 0;
        this.connect();
      } else if (httpState === "disconnected") {
        this.cancelReconnect();
        this.closeSocket();
        this.setState("disconnected");
      }
    });
  }

  updateUrl(wsUrl: string): void {
    this.wsUrl = wsUrl;
    if (this.state === "connected") {
      this.closeSocket();
      this.connect();
    }
  }

  getState(): WSConnectionState {
    return this.state;
  }

  connect(): void {
    if (this.disposed || this.ws) {
      return;
    }
    if (this.monitor.getState() !== "connected") {
      return;
    }

    this.setState("reconnecting");
    logger.debug(`WebSocket connecting to ${this.wsUrl}`);

    try {
      this.ws = new WebSocket(this.wsUrl);
    } catch {
      this.scheduleReconnect();
      return;
    }

    this.ws.on("open", () => {
      this.reconnectAttempt = 0;
      this.setState("connected");
      this.resetStaleTimer();
      logger.info("WebSocket connected");
    });

    this.ws.on("message", (data: WebSocket.Data) => {
      this.resetStaleTimer();
      try {
        const msg = JSON.parse(data.toString()) as WebSocketMessage;
        this.dispatchEvent(msg);
      } catch {
        logger.warn("Failed to parse WebSocket message");
      }
    });

    this.ws.on("ping", () => {
      this.resetStaleTimer();
    });

    this.ws.on("close", () => {
      this.ws = undefined;
      this.clearStaleTimer();
      if (!this.disposed && this.monitor.getState() === "connected") {
        this.setState("reconnecting");
        this.scheduleReconnect();
      } else {
        this.setState("disconnected");
      }
    });

    this.ws.on("error", (err: Error) => {
      logger.warn("WebSocket error", err.message);
      // 'close' event will follow — reconnect handled there
    });
  }

  private dispatchEvent(msg: WebSocketMessage): void {
    switch (msg.type) {
      case "memory_encoded":
        this._onMemoryEncoded.fire(msg.payload as MemoryEncodedPayload);
        break;
      case "consolidation_completed":
        this._onConsolidationCompleted.fire(msg.payload as ConsolidationCompletedPayload);
        break;
      case "pattern_discovered":
        this._onPatternDiscovered.fire(msg.payload as PatternDiscoveredPayload);
        break;
      case "memory_amended":
        this._onMemoryAmended.fire(msg.payload as MemoryAmendedPayload);
        break;
    }
  }

  private setState(newState: WSConnectionState): void {
    if (this.state !== newState) {
      this.state = newState;
      this._onDidChangeState.fire(newState);
    }
  }

  private scheduleReconnect(): void {
    this.cancelReconnect();
    if (this.disposed) {
      return;
    }
    const base = Math.min(BASE_RECONNECT_MS * Math.pow(2, this.reconnectAttempt), MAX_RECONNECT_MS);
    const jitter = base * (1 - JITTER_FACTOR + Math.random() * JITTER_FACTOR * 2);
    const delay = Math.round(jitter);

    logger.debug(`WebSocket reconnecting in ${delay}ms (attempt ${this.reconnectAttempt + 1})`);
    this.reconnectTimer = setTimeout(() => {
      this.reconnectTimer = undefined;
      this.reconnectAttempt++;
      this.connect();
    }, delay);
  }

  private cancelReconnect(): void {
    if (this.reconnectTimer !== undefined) {
      clearTimeout(this.reconnectTimer);
      this.reconnectTimer = undefined;
    }
  }

  private resetStaleTimer(): void {
    this.clearStaleTimer();
    this.staleTimer = setTimeout(() => {
      logger.warn("WebSocket connection stale (no ping received), reconnecting");
      this.closeSocket();
      this.scheduleReconnect();
    }, STALE_TIMEOUT_MS);
  }

  private clearStaleTimer(): void {
    if (this.staleTimer !== undefined) {
      clearTimeout(this.staleTimer);
      this.staleTimer = undefined;
    }
  }

  private closeSocket(): void {
    if (this.ws) {
      try {
        this.ws.removeAllListeners();
        this.ws.close();
      } catch {
        // Ignore close errors
      }
      this.ws = undefined;
    }
  }

  dispose(): void {
    this.disposed = true;
    this.cancelReconnect();
    this.clearStaleTimer();
    this.closeSocket();
    this.monitorSub?.dispose();
    this._onDidChangeState.dispose();
    this._onMemoryEncoded.dispose();
    this._onConsolidationCompleted.dispose();
    this._onPatternDiscovered.dispose();
    this._onMemoryAmended.dispose();
  }
}
