import * as vscode from 'vscode';
import { CercanoClient } from './client';

let client: CercanoClient;

export function activate(context: vscode.ExtensionContext) {
    console.log('Cercano: Activating extension...');
    
    try {
        client = new CercanoClient();
        console.log('Cercano: gRPC Client initialized.');
    } catch (err) {
        console.error('Cercano: Failed to init gRPC client:', err);
    }

    const participant = vscode.chat.createChatParticipant("cercano.chat", async (request, context, response, token) => {
        console.log('Cercano: Chat request received:', request.prompt);
        response.progress("Thinking...");
        try {
            const result = await client.process(request.prompt);
            console.log('Cercano: Received response from backend');
            response.markdown(result);
        } catch (err: any) {
            console.error('Cercano: Error processing request:', err);
            response.markdown(`Error: ${err.message || err}`);
        }
    });

    participant.iconPath = vscode.Uri.joinPath(context.extensionUri, 'media', 'icon.svg');
    context.subscriptions.push(participant);
    console.log('Cercano: Chat participant registered.');
}

export function deactivate() {
    if (client) {
        client.close();
    }
}