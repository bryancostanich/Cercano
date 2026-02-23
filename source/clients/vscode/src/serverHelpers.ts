/**
 * Pure helper functions for server management, extracted to enable unit testing
 * without a live VS Code instance.
 */

import * as path from 'path';
import * as net from 'net';
import * as http from 'http';

const READY_PATTERN = /^Server listening at/;

/**
 * Resolves the path to the server binary.
 * Walks up from the extension directory to find source/server/bin/agent.
 */
export function resolveServerBinaryPath(extensionPath: string): string {
    // extensionPath is source/clients/vscode
    // server binary is at source/server/bin/agent
    return path.join(extensionPath, '..', '..', 'server', 'bin', 'agent');
}

/**
 * Returns true if the given stdout line indicates the server is ready.
 */
export function isServerReady(line: string): boolean {
    return READY_PATTERN.test(line);
}

/**
 * Checks if Ollama is reachable at the given URL.
 * Returns true if Ollama responds, false otherwise.
 */
export function checkOllamaReachable(ollamaUrl: string): Promise<boolean> {
    return new Promise((resolve) => {
        const req = http.get(`${ollamaUrl}/api/tags`, { timeout: 3000 }, (res) => {
            resolve(res.statusCode === 200);
        });
        req.on('error', () => resolve(false));
        req.on('timeout', () => {
            req.destroy();
            resolve(false);
        });
    });
}

/**
 * Checks if a port is in use by attempting a TCP connection.
 */
export function checkPortInUse(port: number): Promise<boolean> {
    return new Promise((resolve) => {
        const socket = new net.Socket();
        socket.setTimeout(500);
        socket.once('connect', () => {
            socket.destroy();
            resolve(true);
        });
        socket.once('timeout', () => {
            socket.destroy();
            resolve(false);
        });
        socket.once('error', () => {
            resolve(false);
        });
        socket.connect(port, '127.0.0.1');
    });
}
