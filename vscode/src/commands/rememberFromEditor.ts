import * as vscode from "vscode";
import { MnemonicClient, MnemonicApiError } from "../api/client";
import type { ConnectionMonitor } from "../components/connectionMonitor";
import type { MemoryType } from "../api/types";
import * as logger from "../util/logger";

/**
 * Registers the "Remember from Editor" context menu commands and
 * the "What Do I Know About This?" query command.
 */
export function registerRememberCommands(
  client: MnemonicClient,
  monitor: ConnectionMonitor
): vscode.Disposable[] {
  return [
    registerRememberCommand("mnemonic.rememberDecision", "decision", client, monitor),
    registerRememberCommand("mnemonic.rememberError", "error", client, monitor),
    registerRememberCommand("mnemonic.rememberInsight", "insight", client, monitor),
    registerWhatDoIKnowCommand(client, monitor),
  ];
}

function registerRememberCommand(
  commandId: string,
  memoryType: MemoryType,
  client: MnemonicClient,
  monitor: ConnectionMonitor
): vscode.Disposable {
  return vscode.commands.registerCommand(commandId, async () => {
    if (monitor.getState() !== "connected") {
      vscode.window.showWarningMessage("Mnemonic daemon is not running.");
      return;
    }

    const editor = vscode.window.activeTextEditor;
    if (!editor) {
      return;
    }

    const selection = editor.document.getText(editor.selection);
    if (!selection.trim()) {
      vscode.window.showWarningMessage("Select some text first.");
      return;
    }

    const filePath = editor.document.uri.fsPath;
    const languageId = editor.document.languageId;
    const workspaceName = vscode.workspace.workspaceFolders?.[0]?.name;

    const content = `[${languageId}] ${filePath}\n\n${selection}`;

    try {
      await client.createMemory({
        content,
        source: "vscode",
        type: memoryType,
        project: workspaceName,
      });
      vscode.window.showInformationMessage(
        `Mnemonic: Remembered as ${memoryType}`
      );
    } catch (err) {
      if (err instanceof MnemonicApiError) {
        vscode.window.showErrorMessage(`Mnemonic: ${err.message}`);
      } else {
        logger.error("Failed to create memory", err);
      }
    }
  });
}

function registerWhatDoIKnowCommand(
  client: MnemonicClient,
  monitor: ConnectionMonitor
): vscode.Disposable {
  return vscode.commands.registerCommand("mnemonic.whatDoIKnow", async () => {
    if (monitor.getState() !== "connected") {
      vscode.window.showWarningMessage("Mnemonic daemon is not running.");
      return;
    }

    const editor = vscode.window.activeTextEditor;
    if (!editor) {
      return;
    }

    const selection = editor.document.getText(editor.selection);
    if (!selection.trim()) {
      vscode.window.showWarningMessage("Select some text first.");
      return;
    }

    try {
      const resp = await client.query({
        query: selection,
        limit: 10,
        include_patterns: false,
        include_abstractions: false,
      });

      if (resp.memories.length === 0) {
        vscode.window.showInformationMessage(
          "Mnemonic: No related memories found."
        );
        return;
      }

      const items = resp.memories.map((r) => ({
        label: r.memory.summary || r.memory.gist || r.memory.content.slice(0, 80),
        description: `${r.memory.type} \u2022 ${r.score.toFixed(2)}`,
        detail: r.memory.content.slice(0, 120),
        memoryId: r.memory.id,
      }));

      const selected = await vscode.window.showQuickPick(items, {
        placeHolder: `Found ${resp.memories.length} related memories`,
      });

      if (selected) {
        await vscode.commands.executeCommand(
          "mnemonic.openMemoryDetail",
          selected.memoryId
        );
      }
    } catch (err) {
      if (err instanceof MnemonicApiError) {
        vscode.window.showErrorMessage(`Mnemonic: ${err.message}`);
      } else {
        logger.error("Failed to query memories", err);
      }
    }
  });
}
