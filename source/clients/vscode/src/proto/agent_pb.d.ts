// package: agent
// file: agent.proto

/* tslint:disable */
/* eslint-disable */

import * as jspb from "google-protobuf";

export class StreamProcessResponse extends jspb.Message { 

    hasProgress(): boolean;
    clearProgress(): void;
    getProgress(): ProgressUpdate | undefined;
    setProgress(value?: ProgressUpdate): StreamProcessResponse;

    hasFinalResponse(): boolean;
    clearFinalResponse(): void;
    getFinalResponse(): ProcessRequestResponse | undefined;
    setFinalResponse(value?: ProcessRequestResponse): StreamProcessResponse;

    getPayloadCase(): StreamProcessResponse.PayloadCase;

    serializeBinary(): Uint8Array;
    toObject(includeInstance?: boolean): StreamProcessResponse.AsObject;
    static toObject(includeInstance: boolean, msg: StreamProcessResponse): StreamProcessResponse.AsObject;
    static extensions: {[key: number]: jspb.ExtensionFieldInfo<jspb.Message>};
    static extensionsBinary: {[key: number]: jspb.ExtensionFieldBinaryInfo<jspb.Message>};
    static serializeBinaryToWriter(message: StreamProcessResponse, writer: jspb.BinaryWriter): void;
    static deserializeBinary(bytes: Uint8Array): StreamProcessResponse;
    static deserializeBinaryFromReader(message: StreamProcessResponse, reader: jspb.BinaryReader): StreamProcessResponse;
}

export namespace StreamProcessResponse {
    export type AsObject = {
        progress?: ProgressUpdate.AsObject,
        finalResponse?: ProcessRequestResponse.AsObject,
    }

    export enum PayloadCase {
        PAYLOAD_NOT_SET = 0,
        PROGRESS = 1,
        FINAL_RESPONSE = 2,
    }

}

export class ProgressUpdate extends jspb.Message { 
    getMessage(): string;
    setMessage(value: string): ProgressUpdate;

    serializeBinary(): Uint8Array;
    toObject(includeInstance?: boolean): ProgressUpdate.AsObject;
    static toObject(includeInstance: boolean, msg: ProgressUpdate): ProgressUpdate.AsObject;
    static extensions: {[key: number]: jspb.ExtensionFieldInfo<jspb.Message>};
    static extensionsBinary: {[key: number]: jspb.ExtensionFieldBinaryInfo<jspb.Message>};
    static serializeBinaryToWriter(message: ProgressUpdate, writer: jspb.BinaryWriter): void;
    static deserializeBinary(bytes: Uint8Array): ProgressUpdate;
    static deserializeBinaryFromReader(message: ProgressUpdate, reader: jspb.BinaryReader): ProgressUpdate;
}

export namespace ProgressUpdate {
    export type AsObject = {
        message: string,
    }
}

export class ProcessRequestRequest extends jspb.Message { 
    getInput(): string;
    setInput(value: string): ProcessRequestRequest;
    getWorkDir(): string;
    setWorkDir(value: string): ProcessRequestRequest;
    getFileName(): string;
    setFileName(value: string): ProcessRequestRequest;
    getConversationId(): string;
    setConversationId(value: string): ProcessRequestRequest;

    serializeBinary(): Uint8Array;
    toObject(includeInstance?: boolean): ProcessRequestRequest.AsObject;
    static toObject(includeInstance: boolean, msg: ProcessRequestRequest): ProcessRequestRequest.AsObject;
    static extensions: {[key: number]: jspb.ExtensionFieldInfo<jspb.Message>};
    static extensionsBinary: {[key: number]: jspb.ExtensionFieldBinaryInfo<jspb.Message>};
    static serializeBinaryToWriter(message: ProcessRequestRequest, writer: jspb.BinaryWriter): void;
    static deserializeBinary(bytes: Uint8Array): ProcessRequestRequest;
    static deserializeBinaryFromReader(message: ProcessRequestRequest, reader: jspb.BinaryReader): ProcessRequestRequest;
}

export namespace ProcessRequestRequest {
    export type AsObject = {
        input: string,
        workDir: string,
        fileName: string,
        conversationId: string,
    }
}

export class UpdateConfigRequest extends jspb.Message { 
    getLocalModel(): string;
    setLocalModel(value: string): UpdateConfigRequest;
    getCloudProvider(): string;
    setCloudProvider(value: string): UpdateConfigRequest;
    getCloudModel(): string;
    setCloudModel(value: string): UpdateConfigRequest;
    getCloudApiKey(): string;
    setCloudApiKey(value: string): UpdateConfigRequest;

    serializeBinary(): Uint8Array;
    toObject(includeInstance?: boolean): UpdateConfigRequest.AsObject;
    static toObject(includeInstance: boolean, msg: UpdateConfigRequest): UpdateConfigRequest.AsObject;
    static extensions: {[key: number]: jspb.ExtensionFieldInfo<jspb.Message>};
    static extensionsBinary: {[key: number]: jspb.ExtensionFieldBinaryInfo<jspb.Message>};
    static serializeBinaryToWriter(message: UpdateConfigRequest, writer: jspb.BinaryWriter): void;
    static deserializeBinary(bytes: Uint8Array): UpdateConfigRequest;
    static deserializeBinaryFromReader(message: UpdateConfigRequest, reader: jspb.BinaryReader): UpdateConfigRequest;
}

export namespace UpdateConfigRequest {
    export type AsObject = {
        localModel: string,
        cloudProvider: string,
        cloudModel: string,
        cloudApiKey: string,
    }
}

export class UpdateConfigResponse extends jspb.Message { 
    getSuccess(): boolean;
    setSuccess(value: boolean): UpdateConfigResponse;
    getMessage(): string;
    setMessage(value: string): UpdateConfigResponse;

    serializeBinary(): Uint8Array;
    toObject(includeInstance?: boolean): UpdateConfigResponse.AsObject;
    static toObject(includeInstance: boolean, msg: UpdateConfigResponse): UpdateConfigResponse.AsObject;
    static extensions: {[key: number]: jspb.ExtensionFieldInfo<jspb.Message>};
    static extensionsBinary: {[key: number]: jspb.ExtensionFieldBinaryInfo<jspb.Message>};
    static serializeBinaryToWriter(message: UpdateConfigResponse, writer: jspb.BinaryWriter): void;
    static deserializeBinary(bytes: Uint8Array): UpdateConfigResponse;
    static deserializeBinaryFromReader(message: UpdateConfigResponse, reader: jspb.BinaryReader): UpdateConfigResponse;
}

export namespace UpdateConfigResponse {
    export type AsObject = {
        success: boolean,
        message: string,
    }
}

export class ProcessRequestResponse extends jspb.Message { 
    getOutput(): string;
    setOutput(value: string): ProcessRequestResponse;
    clearFileChangesList(): void;
    getFileChangesList(): Array<FileChange>;
    setFileChangesList(value: Array<FileChange>): ProcessRequestResponse;
    addFileChanges(value?: FileChange, index?: number): FileChange;

    hasRoutingMetadata(): boolean;
    clearRoutingMetadata(): void;
    getRoutingMetadata(): RoutingMetadata | undefined;
    setRoutingMetadata(value?: RoutingMetadata): ProcessRequestResponse;
    getValidationErrors(): string;
    setValidationErrors(value: string): ProcessRequestResponse;

    serializeBinary(): Uint8Array;
    toObject(includeInstance?: boolean): ProcessRequestResponse.AsObject;
    static toObject(includeInstance: boolean, msg: ProcessRequestResponse): ProcessRequestResponse.AsObject;
    static extensions: {[key: number]: jspb.ExtensionFieldInfo<jspb.Message>};
    static extensionsBinary: {[key: number]: jspb.ExtensionFieldBinaryInfo<jspb.Message>};
    static serializeBinaryToWriter(message: ProcessRequestResponse, writer: jspb.BinaryWriter): void;
    static deserializeBinary(bytes: Uint8Array): ProcessRequestResponse;
    static deserializeBinaryFromReader(message: ProcessRequestResponse, reader: jspb.BinaryReader): ProcessRequestResponse;
}

export namespace ProcessRequestResponse {
    export type AsObject = {
        output: string,
        fileChangesList: Array<FileChange.AsObject>,
        routingMetadata?: RoutingMetadata.AsObject,
        validationErrors: string,
    }
}

export class FileChange extends jspb.Message { 
    getPath(): string;
    setPath(value: string): FileChange;
    getContent(): string;
    setContent(value: string): FileChange;
    getAction(): FileAction;
    setAction(value: FileAction): FileChange;

    serializeBinary(): Uint8Array;
    toObject(includeInstance?: boolean): FileChange.AsObject;
    static toObject(includeInstance: boolean, msg: FileChange): FileChange.AsObject;
    static extensions: {[key: number]: jspb.ExtensionFieldInfo<jspb.Message>};
    static extensionsBinary: {[key: number]: jspb.ExtensionFieldBinaryInfo<jspb.Message>};
    static serializeBinaryToWriter(message: FileChange, writer: jspb.BinaryWriter): void;
    static deserializeBinary(bytes: Uint8Array): FileChange;
    static deserializeBinaryFromReader(message: FileChange, reader: jspb.BinaryReader): FileChange;
}

export namespace FileChange {
    export type AsObject = {
        path: string,
        content: string,
        action: FileAction,
    }
}

export class RoutingMetadata extends jspb.Message { 
    getModelName(): string;
    setModelName(value: string): RoutingMetadata;
    getConfidence(): number;
    setConfidence(value: number): RoutingMetadata;
    getEscalated(): boolean;
    setEscalated(value: boolean): RoutingMetadata;

    serializeBinary(): Uint8Array;
    toObject(includeInstance?: boolean): RoutingMetadata.AsObject;
    static toObject(includeInstance: boolean, msg: RoutingMetadata): RoutingMetadata.AsObject;
    static extensions: {[key: number]: jspb.ExtensionFieldInfo<jspb.Message>};
    static extensionsBinary: {[key: number]: jspb.ExtensionFieldBinaryInfo<jspb.Message>};
    static serializeBinaryToWriter(message: RoutingMetadata, writer: jspb.BinaryWriter): void;
    static deserializeBinary(bytes: Uint8Array): RoutingMetadata;
    static deserializeBinaryFromReader(message: RoutingMetadata, reader: jspb.BinaryReader): RoutingMetadata;
}

export namespace RoutingMetadata {
    export type AsObject = {
        modelName: string,
        confidence: number,
        escalated: boolean,
    }
}

export enum FileAction {
    CREATE = 0,
    UPDATE = 1,
    DELETE = 2,
}
