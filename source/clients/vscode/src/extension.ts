import * as vscode from 'vscode';
import { CercanoClient } from './client';
import { buildFollowupArgs, buildReplaceRange, isPreviewTabForResponse } from './extensionHelpers';

let client: CercanoClient;

// Track validated contents and processed responses across turns
const validatedContents = new Map<string, string>();
const processedResponses = new Set<string>();

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

    const participant = vscode.chat.createChatParticipant("cercano-chat", async (request: vscode.ChatRequest, contextChat: vscode.ChatContext, response: vscode.ChatResponseStream, token: vscode.CancellationToken): Promise<vscode.ChatResult> => {
        if (request.command === 'config') {
            await showConfigMenu();
            response.markdown('Configuration menu opened.');
            return {};
        }

        console.log('Cercano: Chat request received:', request.prompt);
        const responseId = Date.now().toString(); // Simple unique ID for this response
        
        // 1. Gather IDE Context
        const editor = vscode.window.activeTextEditor;
        let contextText = "";
        let workDir = "";
        let fileName = "";

        if (editor) {
            const document = editor.document;
            const selection = editor.selection;
            const text = selection.isEmpty ? document.getText() : document.getText(selection);
            
            fileName = require('path').basename(document.fileName);
            workDir = require('path').dirname(document.fileName);
            
            contextText = `\n\n--- Context from ${fileName} ---\n${text}\n--- End Context ---\n`;
            console.log(`Cercano: Included context from ${fileName} in ${workDir}`);
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
                console.log(`Cercano: Sending cloud preference to agent: ${provider} (${model})`);
            } else {
                response.markdown(`Warning: No API key found for **${provider}**. Please run the "Cercano: Set ${provider === 'google' ? 'Gemini' : 'Anthropic'} API Key" command. Falling back to default routing.`);
            }
        }

        // 3. Combine Prompt + Context
        const fullPrompt = request.prompt + contextText;

        try {
            // 4. Call gRPC backend with streaming
            const result = await client.processStream(
                fullPrompt, 
                workDir, 
                fileName, 
                providerConfig,
                (msg) => response.progress(msg)
            );
            
            // Show markdown output
            response.markdown(result.getOutput());

            // 5. Show Routing Info
            const metadata = result.getRoutingMetadata();
            if (metadata) {
                const modelName = metadata.getModelName();
                const escalated = metadata.getEscalated();
                response.markdown(`\n\n*(Processed by: **${modelName}**${escalated ? ' [Escalated]' : ''})*`);
            }

            // 6. Show Validation Errors if any
            const validationErrors = result.getValidationErrors();
            if (validationErrors) {
                response.markdown(`\n\n---\n### ⚠️ Validation Issues\nSome issues were detected during generation:\n\n\`\`\`\n${validationErrors}\n\`\`\``);
            }

            // 7. Handle File Changes via FileTree and followups
            const fileChanges = result.getFileChangesList();
            if (fileChanges && fileChanges.length > 0) {
                const edit = new vscode.WorkspaceEdit();
                const workspaceFolder = vscode.workspace.workspaceFolders?.[0]?.uri.fsPath;
                const fileTreeItems: vscode.ChatResponseFileTree[] = [];
                const paths: string[] = [];

                for (const change of fileChanges) {
                    const relativePath = change.getPath();
                    const content = change.getContent();
                    
                    let fileUri: vscode.Uri;
                    if (workspaceFolder) {
                        fileUri = vscode.Uri.file(require('path').join(workspaceFolder, relativePath));
                    } else {
                        fileUri = vscode.Uri.file(relativePath);
                    }

                    fileTreeItems.push({ name: relativePath });
                    paths.push(relativePath);

                    // Store the validated content for this specific file/response
                    const changeId = `${responseId}:${relativePath}`;
                    validatedContents.set(changeId, content);
                }

                // Show rich file tree
                if (workspaceFolder) {
                    response.filetree(fileTreeItems, vscode.Uri.file(workspaceFolder));
                }

                response.markdown("\n\nCercano has proposed modifications to the files listed above.");

                // Inline action buttons — pass full args (responseId + filePaths) directly
                const buttonArgs = buildFollowupArgs({ responseId, filePaths: paths });
                response.button({ title: 'Apply Changes', command: 'cercano.applyChanges', arguments: [buttonArgs] });
                response.button({ title: 'Preview Changes', command: 'cercano.previewChanges', arguments: [buttonArgs] });
                response.button({ title: 'Reject', command: 'cercano.rejectChanges', arguments: [{ responseId }] });
            }

        } catch (err: any) {
            console.error('Cercano: Error processing request:', err);
            response.markdown(`Error: ${err.message || err}`);
        }

        return {};
    });

    // Register the command to preview changes
    context.subscriptions.push(vscode.commands.registerCommand('cercano.previewChanges', async (args: { responseId: string, filePaths: string[] }) => {
        for (const relativePath of args.filePaths) {
            const changeId = `${args.responseId}:${relativePath}`;
            const newContent = validatedContents.get(changeId);
            if (newContent === undefined) continue;

            const workspaceFolder = vscode.workspace.workspaceFolders?.[0]?.uri.fsPath;
            const fileUri = workspaceFolder ? vscode.Uri.file(require('path').join(workspaceFolder, relativePath)) : vscode.Uri.file(relativePath);
            
            // Check if file exists for original content
            let originalUri = fileUri;
            let fileExists = true;
            try {
                await vscode.workspace.fs.stat(fileUri);
            } catch {
                fileExists = false;
                // If it doesn't exist, we'll diff against an empty temporary file
                originalUri = vscode.Uri.parse(`cercano-empty:empty`);
                const emptyProvider = new class implements vscode.TextDocumentContentProvider {
                    provideTextDocumentContent() { return ""; }
                };
                context.subscriptions.push(vscode.workspace.registerTextDocumentContentProvider('cercano-empty', emptyProvider));
            }

            const previewUri = vscode.Uri.parse(`cercano-preview:${fileUri.path}?${args.responseId}`);
            
            const provider = new class implements vscode.TextDocumentContentProvider {
                provideTextDocumentContent() { return newContent; }
            };
            context.subscriptions.push(vscode.workspace.registerTextDocumentContentProvider('cercano-preview', provider));

            const title = fileExists ? `Cercano Preview: ${relativePath}` : `Cercano Preview: ${relativePath} (New File)`;
            await vscode.commands.executeCommand('vscode.diff', originalUri, previewUri, title);
        }
    }));

    // Register the command to apply changes
    context.subscriptions.push(vscode.commands.registerCommand('cercano.applyChanges', async (args: { responseId: string, filePaths: string[] }) => {
        if (processedResponses.has(args.responseId)) {
            vscode.window.showInformationMessage("Cercano: These changes have already been handled.");
            return;
        }

        const edit = new vscode.WorkspaceEdit();
        const workspaceFolder = vscode.workspace.workspaceFolders?.[0]?.uri.fsPath;
        const fs = require('fs');

        for (const relativePath of args.filePaths) {
            const changeId = `${args.responseId}:${relativePath}`;
            const content = validatedContents.get(changeId);
            if (content === undefined) continue;

            const fileUri = workspaceFolder ? vscode.Uri.file(require('path').join(workspaceFolder, relativePath)) : vscode.Uri.file(relativePath);
            
            if (!fs.existsSync(fileUri.fsPath)) {
                edit.createFile(fileUri, { ignoreIfExists: true });
                edit.insert(fileUri, new vscode.Position(0, 0), content);
            } else {
                const doc = await vscode.workspace.openTextDocument(fileUri);
                const r = buildReplaceRange(doc.lineCount);
                const range = new vscode.Range(
                    new vscode.Position(r.startLine, r.startCharacter),
                    new vscode.Position(r.endLine, r.endCharacter)
                );
                edit.replace(fileUri, range, content);
            }
        }

        const success = await vscode.workspace.applyEdit(edit);
        
        if (success) {
            processedResponses.add(args.responseId);
            if (args.filePaths.length > 0) {
                const firstUri = workspaceFolder ? vscode.Uri.file(require('path').join(workspaceFolder, args.filePaths[0])) : vscode.Uri.file(args.filePaths[0]);
                await vscode.window.showTextDocument(firstUri);
                vscode.window.showInformationMessage("Cercano: Changes applied.");
            }
        } else {
            vscode.window.showErrorMessage("Cercano: Failed to apply changes.");
        }
    }));

    context.subscriptions.push(vscode.commands.registerCommand('cercano.rejectChanges', async (args: { responseId: string }) => {
        processedResponses.add(args.responseId);

        // Close any open preview diff tabs for this response
        for (const tabGroup of vscode.window.tabGroups.all) {
            for (const tab of tabGroup.tabs) {
                if (tab.input instanceof vscode.TabInputTextDiff) {
                    const modified = tab.input.modified;
                    if (isPreviewTabForResponse(modified.scheme, modified.query, args.responseId)) {
                        await vscode.window.tabGroups.close(tab);
                    }
                }
            }
        }

        vscode.window.showInformationMessage("Cercano: Changes rejected.");
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