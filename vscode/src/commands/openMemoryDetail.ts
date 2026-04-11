import * as vscode from "vscode";
import { MnemonicClient, MnemonicApiError } from "../api/client";
import type { MemoryContext } from "../api/types";

const SCHEME = "mnemonic-memory";

/**
 * Registers the memory detail command and a TextDocumentContentProvider
 * that renders memory details as a read-only markdown document.
 */
export function registerOpenMemoryDetail(
  client: MnemonicClient
): vscode.Disposable[] {
  const provider = new MemoryDocumentProvider(client);

  const providerRegistration =
    vscode.workspace.registerTextDocumentContentProvider(SCHEME, provider);

  const commandRegistration = vscode.commands.registerCommand(
    "mnemonic.openMemoryDetail",
    async (memoryId: string) => {
      if (!memoryId) {
        return;
      }
      const uri = vscode.Uri.parse(`${SCHEME}:${memoryId}.md`);
      // Invalidate cache so we always get fresh data
      provider.invalidate(uri);
      const doc = await vscode.workspace.openTextDocument(uri);
      await vscode.window.showTextDocument(doc, {
        preview: true,
        preserveFocus: false,
      });
    }
  );

  return [providerRegistration, commandRegistration];
}

class MemoryDocumentProvider implements vscode.TextDocumentContentProvider {
  private readonly _onDidChange = new vscode.EventEmitter<vscode.Uri>();
  readonly onDidChange = this._onDidChange.event;

  constructor(private readonly client: MnemonicClient) {}

  invalidate(uri: vscode.Uri): void {
    this._onDidChange.fire(uri);
  }

  async provideTextDocumentContent(uri: vscode.Uri): Promise<string> {
    const memoryId = uri.path.replace(/\.md$/, "");

    try {
      const ctx = await this.client.getMemoryContext(memoryId);
      return renderMemoryContext(ctx);
    } catch (err) {
      if (err instanceof MnemonicApiError) {
        return `# Error\n\n${err.message}`;
      }
      return `# Error\n\nFailed to load memory ${memoryId}`;
    }
  }
}

function renderMemoryContext(ctx: MemoryContext): string {
  const m = ctx.memory;
  const lines: string[] = [];

  lines.push(`# ${m.summary || m.gist || "Memory"}`);
  lines.push("");
  lines.push(`**Type:** ${m.type} | **Source:** ${m.source} | **Salience:** ${m.salience.toFixed(2)} | **State:** ${m.state}`);
  lines.push(`**Created:** ${new Date(m.created_at).toLocaleString()}`);
  if (m.project) {
    lines.push(`**Project:** ${m.project}`);
  }
  lines.push("");

  lines.push("## Content");
  lines.push("");
  lines.push(m.content);
  lines.push("");

  if (ctx.resolution) {
    lines.push("## Resolution");
    lines.push("");
    if (ctx.resolution.gist) {
      lines.push(`**Gist:** ${ctx.resolution.gist}`);
    }
    if (ctx.resolution.narrative) {
      lines.push("");
      lines.push(ctx.resolution.narrative);
    }
    lines.push("");
  }

  if (m.concepts?.length) {
    lines.push("## Concepts");
    lines.push("");
    lines.push(m.concepts.map((c) => `\`${c}\``).join(", "));
    lines.push("");
  }

  if (ctx.concept_set && Object.keys(ctx.concept_set).length > 0) {
    lines.push("## Concept Set");
    lines.push("");
    for (const [category, items] of Object.entries(ctx.concept_set)) {
      if (items.length > 0) {
        lines.push(`- **${category}:** ${items.join(", ")}`);
      }
    }
    lines.push("");
  }

  if (ctx.episode) {
    lines.push("## Episode");
    lines.push("");
    lines.push(`**Theme:** ${ctx.episode.theme} | **State:** ${ctx.episode.state}`);
    lines.push("");
  }

  lines.push("---");
  lines.push(`*Memory ID: ${m.id}*`);

  return lines.join("\n");
}
