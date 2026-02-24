import * as vscode from 'vscode';
import { CercanoClient } from './client';
import { buildFollowupArgs, buildReplaceRange, isPreviewTabForResponse } from './extensionHelpers';
import { ServerManager, getServerConfig } from './serverManager';

let client: CercanoClient;
let serverManager: ServerManager;

// Track validated contents and processed responses across turns
const validatedContents = new Map<string, string>();
const processedResponses = new Set<string>();

// Conversation ID for multi-turn history (persists for the extension session)
const conversationId: string = require('crypto').randomUUID();

/**
 * Sends current configuration to the server via UpdateConfig RPC.
 * Called on activation and whenever settings change.
 */
async function sendConfig(context: vscode.ExtensionContext): Promise<void> {
    if (!client) { return; }

    const config = vscode.workspace.getConfiguration('cercano');
    const localModel = config.get<string>('localModel', 'qwen3-coder');
    const provider = config.get<string>('provider') || 'local';
    const model = config.get<string>('model') || '';

    const updatePayload: {
        localModel?: string,
        cloudProvider?: string,
        cloudModel?: string,
        cloudApiKey?: string
    } = { localModel };

    // Include cloud config if a cloud provider is selected
    if (provider === 'google' || provider === 'anthropic') {
        const secretKey = provider === 'google' ? 'gemini-api-key' : 'anthropic-api-key';
        const apiKey = await context.secrets.get(secretKey);
        if (apiKey) {
            updatePayload.cloudProvider = provider;
            updatePayload.cloudModel = model;
            updatePayload.cloudApiKey = apiKey;
        }
    }

    try {
        const result = await client.updateConfig(updatePayload);
        console.log(`Cercano: Config sent to server: ${result.message}`);
    } catch (err) {
        console.error('Cercano: Failed to send config to server:', err);
    }
}

export async function activate(context: vscode.ExtensionContext) {
    console.log('Cercano: Activating extension (Native Chat Mode)...');

    // Read server configuration
    const serverConfig = getServerConfig();

    // Start the server (if auto-launch is enabled)
    serverManager = new ServerManager();
    context.subscriptions.push({ dispose: () => serverManager.dispose() });

    if (serverConfig.autoLaunch) {
        const serverReady = await serverManager.start(context.extensionPath, serverConfig);
        if (!serverReady) {
            vscode.window.showErrorMessage('Cercano: Server is not running. Check the "Cercano Server" output channel for details.');
        }
    }

    try {
        client = new CercanoClient(`127.0.0.1:${serverConfig.port}`);
        console.log('Cercano: gRPC Client initialized.');
    } catch (err) {
        console.error('Cercano: Failed to init gRPC client:', err);
    }

    // Send initial configuration to the server
    await sendConfig(context);

    // Watch for config changes
    context.subscriptions.push(vscode.workspace.onDidChangeConfiguration(async (e) => {
        if (e.affectsConfiguration('cercano.server.port') ||
            e.affectsConfiguration('cercano.ollama.url')) {
            // Port or Ollama URL changes require a server restart
            const newConfig = getServerConfig();
            vscode.window.showInformationMessage('Cercano: Server configuration changed, restarting server...');
            serverManager.stop();
            await serverManager.start(context.extensionPath, newConfig);
            if (e.affectsConfiguration('cercano.server.port')) {
                client = new CercanoClient(`127.0.0.1:${newConfig.port}`);
            }
            await sendConfig(context);
        } else if (e.affectsConfiguration('cercano.localModel') ||
                   e.affectsConfiguration('cercano.provider') ||
                   e.affectsConfiguration('cercano.model')) {
            // Model and provider changes are sent via UpdateConfig RPC — no restart needed
            await sendConfig(context);
            vscode.window.showInformationMessage('Cercano: Configuration updated.');
        }
    }));

    // Register secret management commands
    context.subscriptions.push(vscode.commands.registerCommand('cercano.setGeminiKey', async () => {
        const key = await vscode.window.showInputBox({
            prompt: 'Enter your Google Gemini API Key',
            password: true
        });
        if (key) {
            await context.secrets.store('gemini-api-key', key);
            vscode.window.showInformationMessage('Cercano: Gemini API Key stored securely.');
            await sendConfig(context);
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
            await sendConfig(context);
        }
    }));

    const showConfigMenu = async () => {
        const currentModel = vscode.workspace.getConfiguration('cercano').get<string>('localModel', 'qwen3-coder');
        const items: vscode.QuickPickItem[] = [
            { label: 'Set Local Model', description: `Currently: ${currentModel}` },
            { label: 'Set Google Gemini API Key', description: 'Store your Gemini API key securely' },
            { label: 'Set Anthropic API Key', description: 'Store your Anthropic API key securely' },
            { label: 'Select Cloud Provider', description: 'Choose a cloud provider for escalation (local is always default)' }
        ];

        const selection = await vscode.window.showQuickPick(items, { placeHolder: 'Cercano Configuration' });
        if (!selection) return;

        switch (selection.label) {
            case 'Set Local Model':
                const model = await vscode.window.showInputBox({
                    prompt: 'Enter the Ollama model name for local inference',
                    value: currentModel,
                    placeHolder: 'e.g., qwen3-coder, GLM-4.7-Flash'
                });
                if (model) {
                    await vscode.workspace.getConfiguration('cercano').update('localModel', model, vscode.ConfigurationTarget.Global);
                    vscode.window.showInformationMessage(`Cercano: Local model set to ${model}.`);
                }
                break;
            case 'Set Google Gemini API Key':
                await vscode.commands.executeCommand('cercano.setGeminiKey');
                break;
            case 'Set Anthropic API Key':
                await vscode.commands.executeCommand('cercano.setAnthropicKey');
                break;
            case 'Select Cloud Provider':
                const providers = ['google', 'anthropic'];
                const provider = await vscode.window.showQuickPick(providers, { placeHolder: 'Select Cloud Provider' });
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
        const responseId = Date.now().toString();

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

        // 2. Combine Prompt + Context
        const fullPrompt = request.prompt + contextText;

        try {
            // 3. Call gRPC backend with streaming (provider config is already on the server via UpdateConfig)
            const result = await client.processStream(
                fullPrompt,
                workDir,
                fileName,
                (msg) => response.progress(msg),
                conversationId
            );

            // Show markdown output
            response.markdown(result.getOutput());

            // 4. Show Routing Info
            const metadata = result.getRoutingMetadata();
            if (metadata) {
                const modelName = metadata.getModelName();
                const escalated = metadata.getEscalated();
                response.markdown(`\n\n*(Processed by: **${modelName}**${escalated ? ' [Escalated]' : ''})*`);
            }

            // 5. Show Validation Errors if any
            const validationErrors = result.getValidationErrors();
            if (validationErrors) {
                response.markdown(`\n\n---\n### ⚠️ Validation Issues\nSome issues were detected during generation:\n\n\`\`\`\n${validationErrors}\n\`\`\``);
            }

            // 6. Handle File Changes via FileTree and followups
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
    if (serverManager) {
        serverManager.stop();
    }
    if (client) {
        client.close();
    }
}
