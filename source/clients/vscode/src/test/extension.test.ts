import * as assert from 'assert';
import { buildFollowupArgs, buildReplaceRange, isPreviewTabForResponse } from '../extensionHelpers';

suite('Extension Helpers Test Suite', () => {

    suite('buildFollowupArgs', () => {
        test('includes filePaths from metadata', () => {
            const args = buildFollowupArgs({
                responseId: 'abc123',
                filePaths: ['src/foo.go', 'src/bar.go']
            });
            assert.strictEqual(args.responseId, 'abc123');
            assert.deepStrictEqual(args.filePaths, ['src/foo.go', 'src/bar.go']);
        });

        test('defaults filePaths to empty array when missing from metadata', () => {
            const args = buildFollowupArgs({ responseId: 'abc123' });
            assert.strictEqual(args.responseId, 'abc123');
            assert.deepStrictEqual(args.filePaths, []);
        });

        test('defaults filePaths to empty array when metadata filePaths is undefined', () => {
            const args = buildFollowupArgs({ responseId: 'abc123', filePaths: undefined });
            assert.deepStrictEqual(args.filePaths, []);
        });
    });

    suite('buildReplaceRange', () => {
        test('starts at line 0 character 0', () => {
            const range = buildReplaceRange(42);
            assert.strictEqual(range.startLine, 0);
            assert.strictEqual(range.startCharacter, 0);
        });

        test('ends at the actual document line count', () => {
            const range = buildReplaceRange(42);
            assert.strictEqual(range.endLine, 42);
            assert.strictEqual(range.endCharacter, 0);
        });

        test('handles single-line documents', () => {
            const range = buildReplaceRange(1);
            assert.strictEqual(range.endLine, 1);
        });

        test('does not use hardcoded 100000 line limit', () => {
            const range = buildReplaceRange(5);
            assert.notStrictEqual(range.endLine, 100000);
        });
    });

    suite('isPreviewTabForResponse', () => {
        test('returns true for a cercano-preview URI matching the responseId', () => {
            assert.strictEqual(isPreviewTabForResponse('cercano-preview', 'abc123', 'abc123'), true);
        });

        test('returns false when scheme does not match', () => {
            assert.strictEqual(isPreviewTabForResponse('file', 'abc123', 'abc123'), false);
        });

        test('returns false when responseId does not match', () => {
            assert.strictEqual(isPreviewTabForResponse('cercano-preview', 'abc123', 'differentId'), false);
        });

        test('returns false when both scheme and responseId do not match', () => {
            assert.strictEqual(isPreviewTabForResponse('file', 'abc123', 'differentId'), false);
        });
    });
});
