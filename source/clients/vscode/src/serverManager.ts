import * as vscode from 'vscode';
import * as cp from 'child_process';
import * as path from 'path';
import { resolveServerBinaryPath, isServerReady, checkPortInUse } from './serverHelpers';

const SERVER_PORT = 50052;
const STARTUP_TIMEOUT_MS = 30000;

export class ServerManager {
    private process: cp.ChildProcess | null = null;
    private outputChannel: vscode.OutputChannel;
    private _running = false;

    constructor() {
        this.outputChannel = vscode.window.createOutputChannel('Cercano Server');
    }

    get running(): boolean {
        return this._running;
    }

    /**
     * Starts the server binary. If a server is already running on the port, reuses it.
     * Returns true if the server is ready (started or already running).
     */
    async start(extensionPath: string): Promise<boolean> {
        // Check if server is already running on the port
        const portInUse = await checkPortInUse(SERVER_PORT);
        if (portInUse) {
            this.outputChannel.appendLine(`Cercano: Server already running on port ${SERVER_PORT}, reusing.`);
            this._running = true;
            return true;
        }

        const binaryPath = resolveServerBinaryPath(extensionPath);
        this.outputChannel.appendLine(`Cercano: Starting server from ${binaryPath}`);
        this.outputChannel.show(true); // Show but don't steal focus

        return new Promise<boolean>((resolve) => {
            let resolved = false;

            try {
                this.process = cp.spawn(binaryPath, [], {
                    cwd: path.dirname(path.dirname(binaryPath)), // source/server/
                    stdio: ['ignore', 'pipe', 'pipe'],
                });
            } catch (err: any) {
                this.outputChannel.appendLine(`Cercano: Failed to spawn server: ${err.message}`);
                resolve(false);
                return;
            }

            // Timeout if server doesn't become ready
            const timeout = setTimeout(() => {
                if (!resolved) {
                    resolved = true;
                    this.outputChannel.appendLine('Cercano: Server startup timed out.');
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
                    resolve(false);
                }

                // Unexpected crash after successful start
                if (code !== null && code !== 0 && resolved) {
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
            this.outputChannel.appendLine('Cercano: Server stopped.');
        });
    }

    dispose(): void {
        this.stop();
        this.outputChannel.dispose();
    }
}
