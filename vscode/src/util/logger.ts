import * as vscode from "vscode";

let channel: vscode.OutputChannel | undefined;

function getChannel(): vscode.OutputChannel {
  if (!channel) {
    channel = vscode.window.createOutputChannel("Mnemonic");
  }
  return channel;
}

function timestamp(): string {
  return new Date().toISOString();
}

export function info(message: string, ...args: unknown[]): void {
  getChannel().appendLine(`[${timestamp()}] INFO: ${message} ${args.length ? JSON.stringify(args) : ""}`);
}

export function warn(message: string, ...args: unknown[]): void {
  getChannel().appendLine(`[${timestamp()}] WARN: ${message} ${args.length ? JSON.stringify(args) : ""}`);
}

export function error(message: string, ...args: unknown[]): void {
  getChannel().appendLine(`[${timestamp()}] ERROR: ${message} ${args.length ? JSON.stringify(args) : ""}`);
}

export function debug(message: string, ...args: unknown[]): void {
  getChannel().appendLine(`[${timestamp()}] DEBUG: ${message} ${args.length ? JSON.stringify(args) : ""}`);
}

export function dispose(): void {
  channel?.dispose();
  channel = undefined;
}
