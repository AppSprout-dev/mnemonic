import * as vscode from "vscode";
import { MnemonicClient, MnemonicApiError } from "../api/client";
import type { ConnectionMonitor } from "../components/connectionMonitor";
import type { RetrievalResult } from "../api/types";

const TYPE_ICONS: Record<string, string> = {
  decision: "$(milestone)",
  error: "$(error)",
  insight: "$(lightbulb)",
  learning: "$(mortar-board)",
  general: "$(note)",
};

const INPUT_DEBOUNCE_MS = 200;

/**
 * Quick Recall command — opens a QuickPick for semantic memory search.
 */
export function registerQuickRecall(
  client: MnemonicClient,
  monitor: ConnectionMonitor
): vscode.Disposable {
  return vscode.commands.registerCommand("mnemonic.quickRecall", async () => {
    if (monitor.getState() !== "connected") {
      vscode.window.showWarningMessage(
        "Mnemonic daemon is not running. Start the daemon and try again."
      );
      return;
    }

    const quickPick = vscode.window.createQuickPick<RecallQuickPickItem>();
    quickPick.placeholder = "Search memories...";
    quickPick.matchOnDescription = true;
    quickPick.matchOnDetail = true;

    let debounceTimer: ReturnType<typeof setTimeout> | undefined;
    let lastQueryId: string | undefined;

    quickPick.onDidChangeValue((value) => {
      if (debounceTimer) {
        clearTimeout(debounceTimer);
      }
      if (!value.trim()) {
        quickPick.items = [];
        return;
      }

      quickPick.busy = true;
      debounceTimer = setTimeout(async () => {
        try {
          const resp = await client.query({
            query: value,
            limit: 15,
            include_patterns: false,
            include_abstractions: false,
          });
          lastQueryId = resp.query_id;
          quickPick.items = resp.memories.map(
            (r) => new RecallQuickPickItem(r)
          );
        } catch (err) {
          if (err instanceof MnemonicApiError) {
            quickPick.items = [
              {
                label: `$(warning) ${err.message}`,
                description: "",
                alwaysShow: true,
                result: undefined,
              } as RecallQuickPickItem,
            ];
          }
        } finally {
          quickPick.busy = false;
        }
      }, INPUT_DEBOUNCE_MS);
    });

    quickPick.onDidAccept(async () => {
      const selected = quickPick.selectedItems[0];
      if (!selected?.result) {
        return;
      }

      quickPick.hide();

      const action = await vscode.window.showQuickPick(
        [
          { label: "$(eye) View Detail", action: "detail" as const },
          { label: "$(clippy) Copy to Clipboard", action: "copy" as const },
          { label: "$(comment) Insert as Comment", action: "comment" as const },
        ],
        { placeHolder: "What would you like to do with this memory?" }
      );

      if (!action) {
        return;
      }

      const memory = selected.result.memory;
      const text = memory.summary || memory.gist || memory.content;

      switch (action.action) {
        case "detail":
          await vscode.commands.executeCommand(
            "mnemonic.openMemoryDetail",
            memory.id
          );
          break;
        case "copy":
          await vscode.env.clipboard.writeText(text);
          vscode.window.showInformationMessage("Memory copied to clipboard");
          break;
        case "comment": {
          const editor = vscode.window.activeTextEditor;
          if (editor) {
            const position = editor.selection.active;
            await editor.edit((edit) => {
              edit.insert(position, `// ${text}\n`);
            });
          }
          break;
        }
      }

      // Submit feedback if we had a query
      if (lastQueryId) {
        try {
          await client.submitFeedback({
            query_id: lastQueryId,
            quality: "helpful",
          });
        } catch {
          // Best-effort feedback
        }
      }
    });

    quickPick.onDidHide(() => {
      if (debounceTimer) {
        clearTimeout(debounceTimer);
      }
      quickPick.dispose();
    });

    quickPick.show();
  });
}

class RecallQuickPickItem implements vscode.QuickPickItem {
  label: string;
  description: string;
  detail: string;
  alwaysShow = true;
  result: RetrievalResult | undefined;

  constructor(result: RetrievalResult) {
    const mem = result.memory;
    const icon = TYPE_ICONS[mem.type] || TYPE_ICONS.general;
    this.label = `${icon} ${mem.summary || mem.gist || truncate(mem.content, 80)}`;
    this.description = `${mem.type} \u2022 ${result.score.toFixed(2)}`;
    this.detail = truncate(mem.content, 120);
    this.result = result;
  }
}

function truncate(text: string, maxLen: number): string {
  if (text.length <= maxLen) {
    return text;
  }
  return text.slice(0, maxLen - 1) + "\u2026";
}
