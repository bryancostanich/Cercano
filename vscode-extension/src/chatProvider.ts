import * as vscode from 'vscode';
import { CercanoClient } from './client';

export class ChatProvider implements vscode.WebviewViewProvider {
    public static readonly viewType = 'cercano.chatView';
    private _view?: vscode.WebviewView;

    constructor(
        private readonly _extensionUri: vscode.Uri,
        private readonly _client: CercanoClient
    ) { }

    public resolveWebviewView(
        webviewView: vscode.WebviewView,
        context: vscode.WebviewViewResolveContext,
        _token: vscode.CancellationToken,
    ) {
        this._view = webviewView;

        webviewView.webview.options = {
            enableScripts: true,
            localResourceRoots: [
                this._extensionUri
            ]
        };

        webviewView.webview.html = this._getHtmlForWebview(webviewView.webview);

        webviewView.webview.onDidReceiveMessage(async (data) => {
            switch (data.type) {
                case 'sendMessage':
                    {
                        const userMessage = data.value;
                        console.log('Cercano: User message received:', userMessage);
                        this._handleUserMessage(userMessage);
                        break;
                    }
            }
        });
    }

    private async _handleUserMessage(message: string) {
        if (!this._view) {
            return;
        }

        // Echo user message back to UI
        this._view.webview.postMessage({ type: 'addMessage', value: message, sender: 'user' });

        try {
            console.log('Cercano: Sending request to gRPC backend...');
            // Call gRPC backend
            const response = await this._client.process(message);
            console.log('Cercano: Response received:', response);
            this._view.webview.postMessage({ type: 'addMessage', value: response, sender: 'agent' });
        } catch (err: any) {
            console.error('Cercano: Error processing request:', err);
            this._view.webview.postMessage({ type: 'addMessage', value: `Error: ${err.message}`, sender: 'error' });
        }
    }

    private _getHtmlForWebview(webview: vscode.Webview) {
        return `<!DOCTYPE html>
        <html lang="en">
        <head>
            <meta charset="UTF-8">
            <meta http-equiv="Content-Security-Policy" content="default-src 'none'; style-src 'unsafe-inline'; script-src 'unsafe-inline';">
            <meta name="viewport" content="width=device-width, initial-scale=1.0">
            <title>Cercano Chat</title>
            <style>
                body { 
                    font-family: var(--vscode-font-family); 
                    padding: 10px; 
                    display: flex;
                    flex-direction: column;
                    height: 100vh;
                    background-color: var(--vscode-editor-background);
                    color: var(--vscode-editor-foreground);
                }
                #messages {
                    flex: 1;
                    overflow-y: auto;
                    margin-bottom: 10px;
                }
                .message { 
                    margin-bottom: 10px; 
                    padding: 8px; 
                    border-radius: 4px; 
                    word-wrap: break-word;
                    white-space: pre-wrap;
                }
                .user { 
                    background-color: var(--vscode-button-secondaryBackground); 
                    color: var(--vscode-button-secondaryForeground);
                    align-self: flex-end; 
                    margin-left: 20px;
                }
                .agent { 
                    background-color: var(--vscode-editor-inactiveSelectionBackground); 
                    align-self: flex-start; 
                    margin-right: 20px;
                }
                .error { 
                    color: var(--vscode-errorForeground); 
                    border: 1px solid var(--vscode-errorForeground);
                }
                #input-area {
                    display: flex;
                    gap: 5px;
                }
                #input-box { 
                    flex: 1; 
                    resize: vertical;
                    min-height: 40px;
                    background-color: var(--vscode-input-background);
                    color: var(--vscode-input-foreground);
                    border: 1px solid var(--vscode-input-border);
                }
                #send-btn {
                    height: 40px;
                    background-color: var(--vscode-button-background);
                    color: var(--vscode-button-foreground);
                    border: none;
                    cursor: pointer;
                }
                #send-btn:hover {
                    background-color: var(--vscode-button-hoverBackground);
                }
            </style>
        </head>
        <body>
            <div id="messages"></div>
            <div id="input-area">
                <textarea id="input-box" rows="2" placeholder="Ask Cercano..."></textarea>
                <button id="send-btn">Send</button>
            </div>

            <script>
                const vscode = acquireVsCodeApi();
                const messagesDiv = document.getElementById('messages');
                const inputBox = document.getElementById('input-box');
                const sendBtn = document.getElementById('send-btn');

                function sendMessage() {
                    const text = inputBox.value;
                    if (text.trim()) {
                        vscode.postMessage({ type: 'sendMessage', value: text });
                        inputBox.value = '';
                    }
                }

                sendBtn.addEventListener('click', sendMessage);
                
                inputBox.addEventListener('keydown', (e) => {
                    if (e.key === 'Enter' && !e.shiftKey) {
                        e.preventDefault();
                        sendMessage();
                    }
                });

                window.addEventListener('message', event => {
                    const message = event.data;
                    switch (message.type) {
                        case 'addMessage':
                            const msgElement = document.createElement('div');
                            msgElement.className = 'message ' + message.sender;
                            msgElement.innerText = message.value;
                            messagesDiv.appendChild(msgElement);
                            messagesDiv.scrollTop = messagesDiv.scrollHeight;
                            break;
                    }
                });
            </script>
        </body>
        </html>`;
    }
}
