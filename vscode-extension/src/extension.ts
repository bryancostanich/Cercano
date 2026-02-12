import * as vscode from 'vscode';
import { CercanoClient } from './client';

let client: CercanoClient;

export function activate(context: vscode.ExtensionContext) {
    client = new CercanoClient();

    const participant = vscode.chat.createChatParticipant("cercano.chat", async (request, context, response, token) => {
        response.progress("Thinking...");
        try {
            const result = await client.process(request.prompt);
            response.markdown(result);
        } catch (err: any) {
            response.markdown(`Error: ${err.message || err}`);
        }
    });

    participant.iconPath = vscode.Uri.joinPath(context.extensionUri, 'media', 'icon.svg');
    context.subscriptions.push(participant);
}

export function deactivate() {
    if (client) {
        client.close();
    }
}