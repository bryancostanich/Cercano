import * as vscode from 'vscode';
import { CercanoClient } from './client';

let client: CercanoClient;

export function activate(context: vscode.ExtensionContext) {
    console.log('Cercano: Activating extension (Native Chat Mode)...');
    
    try {
        client = new CercanoClient();
        console.log('Cercano: gRPC Client initialized.');
    } catch (err) {
        console.error('Cercano: Failed to init gRPC client:', err);
    }

    const participant = vscode.chat.createChatParticipant("cercano-chat", async (request, context, response, token) => {
        console.log('Cercano: Chat request received:', request.prompt);
        response.progress("Thinking...");
        
        // 1. Gather IDE Context
        const editor = vscode.window.activeTextEditor;
        let contextText = "";
        if (editor) {
            const document = editor.document;
            const selection = editor.selection;
            const text = selection.isEmpty ? document.getText() : document.getText(selection);
            const filename = document.fileName;
            
            contextText = `\n\n--- Context from ${filename} ---\n${text}\n--- End Context ---\n`;
            console.log(`Cercano: Included context from ${filename}`);
        }

        // 2. Combine Prompt + Context
        const fullPrompt = request.prompt + contextText;

        try {
            // 3. Call gRPC backend
            const result = await client.process(fullPrompt);
            response.markdown(result);
        } catch (err: any) {
            console.error('Cercano: Error processing request:', err);
            response.markdown(`Error: ${err.message || err}`);
        }
    });

    participant.iconPath = vscode.Uri.joinPath(context.extensionUri, 'media', 'icon.svg');
    context.subscriptions.push(participant);
    console.log('Cercano: Chat participant registered (cercano-chat).');
}

export function deactivate() {
    if (client) {
        client.close();
    }
}