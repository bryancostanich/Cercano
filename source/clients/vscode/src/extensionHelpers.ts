/**
 * Pure helper functions for the VS Code extension, extracted to enable unit testing
 * without a live VS Code instance.
 */

export interface ChangeMetadata {
    responseId: string;
    filePaths?: string[];
}

export interface FollowupArgs {
    responseId: string;
    filePaths: string[];
}

export interface ReplaceRange {
    startLine: number;
    startCharacter: number;
    endLine: number;
    endCharacter: number;
}

/**
 * Builds the arguments object passed to Apply/Preview/Reject command handlers.
 * Ensures filePaths is always present and never undefined.
 */
export function buildFollowupArgs(metadata: ChangeMetadata): FollowupArgs {
    return {
        responseId: metadata.responseId,
        filePaths: metadata.filePaths ?? []
    };
}

/**
 * Builds a range covering the entire content of a document given its line count.
 * Use document.lineCount rather than a hardcoded limit.
 */
export function buildReplaceRange(lineCount: number): ReplaceRange {
    return {
        startLine: 0,
        startCharacter: 0,
        endLine: lineCount,
        endCharacter: 0
    };
}

/**
 * Returns true if a tab's URI scheme and query string match a cercano preview
 * tab for the given responseId. Used to identify preview diff tabs to close on Reject.
 */
export function isPreviewTabForResponse(scheme: string, query: string, responseId: string): boolean {
    return scheme === 'cercano-preview' && query === responseId;
}
