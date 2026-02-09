import * as vscode from 'vscode';
import { CercanoClient } from './client';

let client: CercanoClient;

export function activate(context: vscode.ExtensionContext) {
    // Initialize the gRPC client
    // TODO: Make address configurable
    client = new CercanoClient(); 

    let disposable = vscode.commands.registerCommand('cercano.generateTests', async () => {
        const editor = vscode.window.activeTextEditor;
        if (!editor) {
            vscode.window.showErrorMessage('No active editor found.');
            return;
        }

        const selection = editor.selection;
        let text = editor.document.getText(selection);

        // If no selection, use the entire file
        if (!text) {
             text = editor.document.getText();
        }

        if (!text) {
             vscode.window.showErrorMessage('File is empty.');
             return;
        }

        await vscode.window.withProgress({
            location: vscode.ProgressLocation.Notification,
            title: "Cercano: Generating unit tests...",
            cancellable: false
        }, async (progress) => {
            try {
                const generatedTests = await client.process(text);
                
                // Open a new document with the generated tests
                const doc = await vscode.workspace.openTextDocument({
                    content: generatedTests,
                    language: 'go' 
                });
                await vscode.window.showTextDocument(doc, {
                    viewColumn: vscode.ViewColumn.Beside
                });
            } catch (err: any) {
                vscode.window.showErrorMessage(`Failed to generate tests: ${err.message || err}`);
                console.error(err);
            }
        });
    });

    context.subscriptions.push(disposable);
}

export function deactivate() {
    if (client) {
        client.close();
    }
}