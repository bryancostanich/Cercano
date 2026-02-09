import * as vscode from 'vscode';

export function activate(context: vscode.ExtensionContext) {
	console.log('Congratulations, your extension "cercano-vscode" is now active!');

	let disposable = vscode.commands.registerCommand('cercano.generateTests', () => {
		vscode.window.showInformationMessage('Cercano: Generating tests (placeholder)...');
	});

	context.subscriptions.push(disposable);
}

export function deactivate() {}
