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

    const showConfigMenu = async () => {
        const items: vscode.QuickPickItem[] = [
            { label: 'Set Google Gemini API Key', description: 'Store your Gemini API key securely' },
            { label: 'Set Anthropic API Key', description: 'Store your Anthropic API key securely' },
            { label: 'Select Provider', description: 'Choose between local, google, or anthropic' }
        ];

        const selection = await vscode.window.showQuickPick(items, { placeHolder: 'Cercano Configuration' });
        if (!selection) return;

        switch (selection.label) {
            case 'Set Google Gemini API Key':
                await vscode.commands.executeCommand('cercano.setGeminiKey');
                break;
            case 'Set Anthropic API Key':
                await vscode.commands.executeCommand('cercano.setAnthropicKey');
                break;
            case 'Select Provider':
                const providers = ['local', 'google', 'anthropic'];
                const provider = await vscode.window.showQuickPick(providers, { placeHolder: 'Select AI Provider' });
                if (provider) {
                    await vscode.workspace.getConfiguration('cercano').update('provider', provider, vscode.ConfigurationTarget.Global);
                    vscode.window.showInformationMessage(`Cercano: Provider set to ${provider}`);
                }
                break;
        }
    };

    context.subscriptions.push(vscode.commands.registerCommand('cercano.showConfig', showConfigMenu));

    const participant = vscode.chat.createChatParticipant("cercano-chat", async (request, contextChat, response, token) => {
        if (request.command === 'config') {
            await showConfigMenu();
            response.markdown('Configuration menu opened.');
            return;
        }

        console.log('Cercano: Chat request received:', request.prompt);
        response.progress("Routing request...");
        
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
            
            // Show markdown output
            response.markdown(result.getOutput());

            // 5. Handle File Changes via WorkspaceEdit
            const fileChanges = result.getFileChangesList();
            if (fileChanges && fileChanges.length > 0) {
                const edit = new vscode.WorkspaceEdit();
                const workspaceFolder = vscode.workspace.workspaceFolders?.[0]?.uri.fsPath;

                for (const change of fileChanges) {
                    const relativePath = change.getPath();
                    const content = change.getContent();
                    const action = change.getAction(); // This is an enum (0=CREATE, 1=UPDATE, 2=DELETE)

                    let fileUri: vscode.Uri;
                    if (workspaceFolder) {
                        fileUri = vscode.Uri.file(require('path').join(workspaceFolder, relativePath));
                    } else {
                        fileUri = vscode.Uri.file(relativePath);
                    }

                    if (action === 0) { // CREATE
                        edit.createFile(fileUri, { ignoreIfExists: true });
                        edit.insert(fileUri, new vscode.Position(0, 0), content);
                    } else if (action === 1) { // UPDATE
                        // For UPDATE, we currently replace the whole file content
                        // In a more advanced version, we might use diffing or line-based edits.
                        const document = await vscode.workspace.openTextDocument(fileUri);
                        const fullRange = new vscode.Range(
                            document.positionAt(0),
                            document.positionAt(document.getText().length)
                        );
                        edit.replace(fileUri, fullRange, content);
                    } else if (action === 2) { // DELETE
                        edit.deleteFile(fileUri);
                    }
                }

                response.markdown("\n\n---\n### 📂 Proposed File Changes\nCercano has generated file modifications. Click below to review and apply them.");
                
                // Show a button/command to apply the edits with a preview
                response.button({
                    command: "cercano.applyChanges",
                    title: "Apply Changes",
                    arguments: [edit]
                });
            }

        } catch (err: any) {
            console.error('Cercano: Error processing request:', err);
            response.markdown(`Error: ${err.message || err}`);
        }
    });

    // Register the command to apply changes with preview
    context.subscriptions.push(vscode.commands.registerCommand('cercano.applyChanges', async (edit: vscode.WorkspaceEdit) => {
        const success = await vscode.workspace.applyEdit(edit);
        if (success) {
            vscode.window.showInformationMessage("Cercano: Changes applied successfully.");
        } else {
            vscode.window.showErrorMessage("Cercano: Failed to apply changes.");
        }
    }));

    participant.iconPath = vscode.Uri.joinPath(context.extensionUri, 'media', 'icon.svg');
    context.subscriptions.push(participant);
    console.log('Cercano: Chat participant registered (cercano-chat).');
}

export function deactivate() {
    if (client) {
        client.close();
    }
}