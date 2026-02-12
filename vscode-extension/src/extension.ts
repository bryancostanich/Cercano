import * as vscode from 'vscode';
import { CercanoClient } from './client';
import { ChatProvider } from './chatProvider';

let client: CercanoClient;

export function activate(context: vscode.ExtensionContext) {
    // Initialize gRPC client
    client = new CercanoClient();

    // Register Chat Provider
    const provider = new ChatProvider(context.extensionUri, client);

    context.subscriptions.push(
        vscode.window.registerWebviewViewProvider(ChatProvider.viewType, provider)
    );
}

export function deactivate() {
    if (client) {
        client.close();
    }
}
