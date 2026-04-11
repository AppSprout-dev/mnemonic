import * as vscode from "vscode";
import { MnemonicClient } from "./api/client";
import { ConnectionMonitor } from "./components/connectionMonitor";
import { StatusBarManager } from "./components/statusBar";
import {
  RelatedMemoriesProvider,
  ProjectContextProvider,
} from "./views/memoryTreeProvider";
import { registerQuickRecall } from "./commands/quickRecall";
import { registerOpenMemoryDetail } from "./commands/openMemoryDetail";
import { debounce } from "./util/debounce";
import * as logger from "./util/logger";

export function activate(context: vscode.ExtensionContext): void {
  logger.info("Mnemonic extension activating");

  // Read configuration
  const config = vscode.workspace.getConfiguration("mnemonic");
  const endpoint = config.get<string>("endpoint", "http://127.0.0.1:9999");
  const token = config.get<string>("token", "");
  const pollIntervalMs = config.get<number>("healthPollIntervalMs", 30_000);
  const debounceMs = config.get<number>("fileChangeDebounceMs", 400);

  // Create API client
  const client = new MnemonicClient(endpoint, token);

  // Connection monitor
  const monitor = new ConnectionMonitor(client, pollIntervalMs);

  // Status bar
  const statusBar = new StatusBarManager(monitor);

  // Sidebar tree providers
  const relatedProvider = new RelatedMemoriesProvider(client, monitor);
  const projectProvider = new ProjectContextProvider(client, monitor);

  const relatedTree = vscode.window.createTreeView(
    "mnemonic.relatedMemories",
    {
      treeDataProvider: relatedProvider,
      showCollapseAll: true,
    }
  );

  const projectTree = vscode.window.createTreeView("mnemonic.projectContext", {
    treeDataProvider: projectProvider,
    showCollapseAll: true,
  });

  // Debounced file change handler
  const onFileChange = debounce((editor: vscode.TextEditor | undefined) => {
    const filePath = editor?.document.uri.fsPath;
    void relatedProvider.updateForFile(filePath);
  }, debounceMs);

  // Register event listeners
  context.subscriptions.push(
    vscode.window.onDidChangeActiveTextEditor((editor) => {
      onFileChange(editor);
    })
  );

  // Register commands
  context.subscriptions.push(
    registerQuickRecall(client, monitor),
    ...registerOpenMemoryDetail(client),
    vscode.commands.registerCommand("mnemonic.refreshMemories", () => {
      relatedProvider.refresh();
      projectProvider.refresh();
    }),
    vscode.commands.registerCommand("mnemonic.showSidebar", () => {
      relatedTree.reveal(undefined as never, { focus: true }).then(
        () => {},
        () => {
          // Tree might be empty — just focus the view
          void vscode.commands.executeCommand(
            "mnemonic.relatedMemories.focus"
          );
        }
      );
    })
  );

  // Hot-reload configuration changes
  context.subscriptions.push(
    vscode.workspace.onDidChangeConfiguration((e) => {
      if (e.affectsConfiguration("mnemonic")) {
        const newConfig = vscode.workspace.getConfiguration("mnemonic");
        const newEndpoint = newConfig.get<string>(
          "endpoint",
          "http://127.0.0.1:9999"
        );
        const newToken = newConfig.get<string>("token", "");
        client.updateConfig(newEndpoint, newToken);
        logger.info("Configuration updated", newEndpoint);
      }
    })
  );

  // Push all disposables
  context.subscriptions.push(
    statusBar,
    monitor,
    projectProvider,
    relatedTree,
    projectTree,
    { dispose: () => onFileChange.cancel() },
    { dispose: () => client.dispose() },
    { dispose: () => logger.dispose() }
  );

  // Start monitoring and fetch initial data
  monitor.start();
  projectProvider.start();

  // Trigger initial file context if an editor is already open
  if (vscode.window.activeTextEditor) {
    void relatedProvider.updateForFile(
      vscode.window.activeTextEditor.document.uri.fsPath
    );
  }

  logger.info("Mnemonic extension activated");
}

export function deactivate(): void {
  // All cleanup handled by subscriptions disposal
}
