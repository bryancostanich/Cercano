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

    // Register secret management commands
    context.subscriptions.push(vscode.commands.registerCommand('cercano.setGeminiKey', async () => {
        const key = await vscode.window.showInputBox({
            prompt: 'Enter your Google Gemini API Key',
            password: true
        });
        if (key) {
            await context.secrets.store('gemini-api-key', key);
            vscode.window.showInformationMessage('Cercano: Gemini API Key stored securely.');
        }
    }));

    context.subscriptions.push(vscode.commands.registerCommand('cercano.setAnthropicKey', async () => {
        const key = await vscode.window.showInputBox({
            prompt: 'Enter your Anthropic API Key',
            password: true
        });
        if (key) {
            await context.secrets.store('anthropic-api-key', key);
            vscode.window.showInformationMessage('Cercano: Anthropic API Key stored securely.');
        }
    }));

    const participant = vscode.chat.createChatParticipant("cercano-chat", async (request, contextChat, response, token) => {
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

        // 2. Resolve Provider Configuration
        const config = vscode.workspace.getConfiguration('cercano');
        const provider = config.get<string>('provider') || 'local';
        const model = config.get<string>('model') || '';
        
        let providerConfig: { provider: string, model: string, apiKey: string } | undefined;

        if (provider === 'google' || provider === 'anthropic') {
            const secretKey = provider === 'google' ? 'gemini-api-key' : 'anthropic-api-key';
            const apiKey = await context.secrets.get(secretKey);
            
            if (apiKey) {
                providerConfig = {
                    provider: provider,
                    model: model,
                    apiKey: apiKey
                };
                console.log(`Cercano: Using cloud provider: ${provider}, model: ${model}`);
            } else {
                response.markdown(`Warning: No API key found for **${provider}**. Please run the "Cercano: Set ${provider === 'google' ? 'Gemini' : 'Anthropic'} API Key" command. Falling back to default routing.`);
            }
        }

        // 3. Combine Prompt + Context
        const fullPrompt = request.prompt + contextText;

        try {
            // 4. Call gRPC backend
            const result = await client.process(fullPrompt, providerConfig);
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