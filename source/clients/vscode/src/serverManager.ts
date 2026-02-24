import * as vscode from 'vscode';
import * as cp from 'child_process';
import * as path from 'path';
import { resolveServerBinaryPath, isServerReady, checkPortInUse, checkOllamaReachable } from './serverHelpers';

const DEFAULT_PORT = 50052;
const STARTUP_TIMEOUT_MS = 30000;

export interface ServerConfig {
    autoLaunch: boolean;
    binaryPath: string;
    port: number;
    ollamaUrl: string;
    localModel: string;
}

export function getServerConfig(): ServerConfig {
    const config = vscode.workspace.getConfiguration('cercano');
    const serverConfig = vscode.workspace.getConfiguration('cercano.server');
    const ollamaConfig = vscode.workspace.getConfiguration('cercano.ollama');
    return {
        autoLaunch: serverConfig.get<boolean>('autoLaunch', true),
        binaryPath: serverConfig.get<string>('binaryPath', ''),
        port: serverConfig.get<number>('port', DEFAULT_PORT),
        ollamaUrl: ollamaConfig.get<string>('url', 'http://localhost:11434'),
        localModel: config.get<string>('localModel', 'qwen3-coder'),
    };
}

export class ServerManager {
    private process: cp.ChildProcess | null = null;
    private outputChannel: vscode.OutputChannel;
    private statusBarItem: vscode.StatusBarItem;
    private _running = false;
    private _port = DEFAULT_PORT;

    constructor() {
        this.outputChannel = vscode.window.createOutputChannel('Cercano Server');
        this.statusBarItem = vscode.window.createStatusBarItem(vscode.StatusBarAlignment.Right, 100);
        this.statusBarItem.command = 'cercano.showConfig';
        this.updateStatusBar('stopped');
        this.statusBarItem.show();
    }

    get running(): boolean {
        return this._running;
    }

    get port(): number {
        return this._port;
    }

    private updateStatusBar(state: 'running' | 'stopped' | 'starting' | 'error'): void {
        switch (state) {
            case 'running':
                this.statusBarItem.text = '$(check) Cercano';
                this.statusBarItem.tooltip = `Cercano server running on port ${this._port}`;
                this.statusBarItem.backgroundColor = undefined;
                break;
            case 'stopped':
                this.statusBarItem.text = '$(circle-slash) Cercano';
                this.statusBarItem.tooltip = 'Cercano server stopped';
                this.statusBarItem.backgroundColor = undefined;
                break;
            case 'starting':
                this.statusBarItem.text = '$(sync~spin) Cercano';
                this.statusBarItem.tooltip = 'Cercano server starting...';
                this.statusBarItem.backgroundColor = undefined;
                break;
            case 'error':
                this.statusBarItem.text = '$(error) Cercano';
                this.statusBarItem.tooltip = 'Cercano server error — click to configure';
                this.statusBarItem.backgroundColor = new vscode.ThemeColor('statusBarItem.errorBackground');
                break;
        }
    }

    /**
     * Starts the server binary. If a server is already running on the port, reuses it.
     * Returns true if the server is ready (started or already running).
     */
    async start(extensionPath: string, config: ServerConfig): Promise<boolean> {
        this._port = config.port;

        // Check if server is already running on the port
        const portInUse = await checkPortInUse(this._port);
        if (portInUse) {
            this.outputChannel.appendLine(`Cercano: Server already running on port ${this._port}, reusing.`);
            this._running = true;
            this.updateStatusBar('running');
            return true;
        }

        // Check if Ollama is reachable before starting the server
        const ollamaReachable = await checkOllamaReachable(config.ollamaUrl);
        if (!ollamaReachable) {
            this.outputChannel.appendLine(`Cercano: Ollama is not reachable at ${config.ollamaUrl}`);
            this.updateStatusBar('error');
            const action = await vscode.window.showErrorMessage(
                `Cercano: Ollama is not running at ${config.ollamaUrl}. The server requires Ollama to function.`,
                'Download Ollama',
                'Open Settings'
            );
            if (action === 'Download Ollama') {
                vscode.env.openExternal(vscode.Uri.parse('https://ollama.com/'));
            } else if (action === 'Open Settings') {
                vscode.commands.executeCommand('workbench.action.openSettings', 'cercano.ollama');
            }
            return false;
        }

        const binaryPath = config.binaryPath || resolveServerBinaryPath(extensionPath);
        this.outputChannel.appendLine(`Cercano: Starting server from ${binaryPath} on port ${this._port}`);
        this.outputChannel.show(true);
        this.updateStatusBar('starting');

        return new Promise<boolean>((resolve) => {
            let resolved = false;

            try {
                this.process = cp.spawn(binaryPath, [], {
                    cwd: path.dirname(path.dirname(binaryPath)), // source/server/
                    stdio: ['ignore', 'pipe', 'pipe'],
                    env: {
                        ...process.env,
                        // eslint-disable-next-line @typescript-eslint/naming-convention
                        CERCANO_PORT: String(this._port),
                        // eslint-disable-next-line @typescript-eslint/naming-convention
                        OLLAMA_URL: config.ollamaUrl,
                        // eslint-disable-next-line @typescript-eslint/naming-convention
                        CERCANO_LOCAL_MODEL: config.localModel,
                    },
                });
            } catch (err: any) {
                this.outputChannel.appendLine(`Cercano: Failed to spawn server: ${err.message}`);
                this.updateStatusBar('error');
                resolve(false);
                return;
            }

            // Timeout if server doesn't become ready
            const timeout = setTimeout(() => {
                if (!resolved) {
                    resolved = true;
                    this.outputChannel.appendLine('Cercano: Server startup timed out.');
                    this.updateStatusBar('error');
                    vscode.window.showErrorMessage('Cercano: Server failed to start within 30 seconds.');
                    resolve(false);
                }
            }, STARTUP_TIMEOUT_MS);

            // Parse stdout for readiness
            this.process.stdout?.on('data', (data: Buffer) => {
                const text = data.toString();
                this.outputChannel.append(text);

                if (!resolved && isServerReady(text)) {
                    resolved = true;
                    clearTimeout(timeout);
                    this._running = true;
                    this.updateStatusBar('running');
                    this.outputChannel.appendLine('Cercano: Server is ready.');
                    resolve(true);
                }
            });

            this.process.stderr?.on('data', (data: Buffer) => {
                this.outputChannel.append(data.toString());
            });

            this.process.on('error', (err) => {
                this.outputChannel.appendLine(`Cercano: Server process error: ${err.message}`);
                if (!resolved) {
                    resolved = true;
                    clearTimeout(timeout);
                    this.updateStatusBar('error');
                    vscode.window.showErrorMessage(`Cercano: Server failed to start: ${err.message}`);
                    resolve(false);
                }
            });

            this.process.on('exit', (code, signal) => {
                this._running = false;
                this.outputChannel.appendLine(`Cercano: Server exited (code=${code}, signal=${signal}).`);

                if (!resolved) {
                    resolved = true;
                    clearTimeout(timeout);
                    this.updateStatusBar('error');
                    resolve(false);
                } else {
                    this.updateStatusBar('stopped');
                }

                // Unexpected crash after successful start
                if (code !== null && code !== 0) {
                    this.updateStatusBar('error');
                    vscode.window.showWarningMessage(`Cercano: Server crashed (exit code ${code}). Restart VS Code or run "Cercano: Show Configuration Menu".`);
                }

                this.process = null;
            });
        });
    }

    /**
     * Stops the server process with graceful shutdown (SIGTERM, then SIGKILL after 3s).
     */
    stop(): void {
        if (!this.process) {
            this._running = false;
            this.updateStatusBar('stopped');
            return;
        }

        this.outputChannel.appendLine('Cercano: Stopping server...');
        const proc = this.process;
        this.process = null;
        this._running = false;

        proc.kill('SIGTERM');

        // Fallback to SIGKILL if SIGTERM doesn't work within 3 seconds
        const killTimeout = setTimeout(() => {
            try {
                proc.kill('SIGKILL');
                this.outputChannel.appendLine('Cercano: Server did not respond to SIGTERM, sent SIGKILL.');
            } catch {
                // Process already dead
            }
        }, 3000);

        proc.on('exit', () => {
            clearTimeout(killTimeout);
            this.updateStatusBar('stopped');
            this.outputChannel.appendLine('Cercano: Server stopped.');
        });
    }

    dispose(): void {
        this.stop();
        this.outputChannel.dispose();
        this.statusBarItem.dispose();
    }
}
