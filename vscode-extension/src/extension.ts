import * as vscode from 'vscode';
import { CercanoClient } from './client';
import { ChatProvider } from './chatProvider';

let client: CercanoClient;

export function activate(context: vscode.ExtensionContext) {
    console.log('Cercano: Activating extension (Sidebar Mode)...');
    
    try {
        client = new CercanoClient();
        console.log('Cercano: gRPC Client initialized.');
    } catch (err) {
        console.error('Cercano: Failed to init gRPC client:', err);
    }

    const provider = new ChatProvider(context.extensionUri, client);

    context.subscriptions.push(
        vscode.window.registerWebviewViewProvider(ChatProvider.viewType, provider)
    );
    console.log('Cercano: Sidebar Chat Provider registered.');
}

export function deactivate() {
    if (client) {
        client.close();
    }
}
