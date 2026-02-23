import * as assert from 'assert';
import { resolveServerBinaryPath, isServerReady } from '../serverHelpers';
import * as path from 'path';

suite('ServerManager Test Suite', () => {

    suite('resolveServerBinaryPath', () => {
        test('resolves from extension path to server binary', () => {
            const extensionPath = '/workspace/source/clients/vscode';
            const result = resolveServerBinaryPath(extensionPath);
            // Should resolve to /workspace/source/server/bin/agent
            assert.strictEqual(result, path.join('/workspace/source/server/bin/agent'));
        });

        test('handles trailing slash in extension path', () => {
            const extensionPath = '/workspace/source/clients/vscode/';
            const result = resolveServerBinaryPath(extensionPath);
            assert.ok(result.endsWith(path.join('server', 'bin', 'agent')));
        });
    });

    suite('isServerReady', () => {
        test('returns true for exact server listening line', () => {
            assert.strictEqual(isServerReady('Server listening at [::]:50052'), true);
        });

        test('returns true for server listening with different address', () => {
            assert.strictEqual(isServerReady('Server listening at 0.0.0.0:50052'), true);
        });

        test('returns false for startup message', () => {
            assert.strictEqual(isServerReady('Starting Cercano AI Agent gRPC server...'), false);
        });

        test('returns false for empty string', () => {
            assert.strictEqual(isServerReady(''), false);
        });

        test('returns false for unrelated output', () => {
            assert.strictEqual(isServerReady('Main: Detected GEMINI_API_KEY.'), false);
        });

        test('returns true when ready line is in multiline output', () => {
            // stdout data chunks may contain multiple lines
            assert.strictEqual(isServerReady('Server listening at [::]:50052\n'), true);
        });
    });
});
