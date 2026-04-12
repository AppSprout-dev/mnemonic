import * as vscode from "vscode";
import { MnemonicClient } from "./api/client";
import { ConnectionMonitor } from "./components/connectionMonitor";
import { StatusBarManager } from "./components/statusBar";
import { MnemonicWebSocket } from "./components/websocketClient";
import { SessionAwareness } from "./components/sessionAwareness";
import { TerminalErrorMonitor } from "./components/terminalErrorMonitor";
import {
  RelatedMemoriesProvider,
  ProjectContextProvider,
} from "./views/memoryTreeProvider";
import { registerQuickRecall } from "./commands/quickRecall";
import { registerOpenMemoryDetail } from "./commands/openMemoryDetail";
import { registerRememberCommands } from "./commands/rememberFromEditor";
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
  const wsEnabled = config.get<boolean>("websocketEnabled", true);
  const terminalEnabled = config.get<boolean>("terminalErrorMonitoring", true);

  // Create API client
  const client = new MnemonicClient(endpoint, token);

  // Connection monitor
  const monitor = new ConnectionMonitor(client, pollIntervalMs);

  // WebSocket client (optional, coordinated with connection monitor)
  let wsClient: MnemonicWebSocket | undefined;
  if (wsEnabled) {
    const wsUrl = endpoint.replace(/^http/, "ws") + "/ws";
    wsClient = new MnemonicWebSocket(wsUrl, monitor);
  }

  // Status bar (with optional WS indicator)
  const statusBar = new StatusBarManager(monitor, wsClient);

  // Sidebar tree providers
  const relatedProvider = new RelatedMemoriesProvider(client, monitor);
  const projectProvider = new ProjectContextProvider(client, monitor);

  // Wire WebSocket events to cache invalidation and notifications
  if (wsClient) {
    context.subscriptions.push(
      wsClient.onMemoryEncoded(() => relatedProvider.invalidateAndRefresh()),
      wsClient.onConsolidationCompleted(() => projectProvider.refreshFromEvent()),
      wsClient.onPatternDiscovered((p) => {
        vscode.window.showInformationMessage(
          `Mnemonic: New pattern discovered — "${p.title}"`
        );
      }),
      wsClient.onMemoryAmended(() => relatedProvider.invalidateAndRefresh())
    );
  }

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

  // Session awareness
  const sessionAwareness = new SessionAwareness(client, wsClient, monitor);

  // Terminal error monitoring
  let terminalMonitor: TerminalErrorMonitor | undefined;
  if (terminalEnabled) {
    terminalMonitor = new TerminalErrorMonitor(client, monitor);
    terminalMonitor.start();
  }

  // Register commands
  context.subscriptions.push(
    registerQuickRecall(client, monitor),
    ...registerOpenMemoryDetail(client),
    ...registerRememberCommands(client, monitor),
    vscode.commands.registerCommand("mnemonic.refreshMemories", () => {
      relatedProvider.refresh();
      projectProvider.refresh();
    }),
    vscode.commands.registerCommand("mnemonic.showSidebar", () => {
      relatedTree.reveal(undefined as never, { focus: true }).then(
        () => {},
        () => {
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
        if (wsClient) {
          wsClient.updateUrl(newEndpoint.replace(/^http/, "ws") + "/ws");
        }
        logger.info("Configuration updated", newEndpoint);
      }
    })
  );

  // Push all disposables
  context.subscriptions.push(
    statusBar,
    monitor,
    sessionAwareness,
    projectProvider,
    relatedTree,
    projectTree,
    { dispose: () => onFileChange.cancel() },
    { dispose: () => client.dispose() },
    { dispose: () => logger.dispose() }
  );
  if (wsClient) {
    context.subscriptions.push(wsClient);
  }
  if (terminalMonitor) {
    context.subscriptions.push(terminalMonitor);
  }

  // Start monitoring and fetch initial data
  monitor.start();
  projectProvider.start();
  void sessionAwareness.start();

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
