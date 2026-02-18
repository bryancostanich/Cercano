// package: agent
// file: agent.proto

/* tslint:disable */
/* eslint-disable */

import * as jspb from "google-protobuf";

export class ProcessRequestRequest extends jspb.Message { 
    getInput(): string;
    setInput(value: string): ProcessRequestRequest;

    hasProviderConfig(): boolean;
    clearProviderConfig(): void;
    getProviderConfig(): CloudProviderConfig | undefined;
    setProviderConfig(value?: CloudProviderConfig): ProcessRequestRequest;
    getWorkDir(): string;
    setWorkDir(value: string): ProcessRequestRequest;
    getFileName(): string;
    setFileName(value: string): ProcessRequestRequest;

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
        providerConfig?: CloudProviderConfig.AsObject,
        workDir: string,
        fileName: string,
    }
}

export class CloudProviderConfig extends jspb.Message { 
    getProvider(): string;
    setProvider(value: string): CloudProviderConfig;
    getModel(): string;
    setModel(value: string): CloudProviderConfig;
    getApiKey(): string;
    setApiKey(value: string): CloudProviderConfig;

    serializeBinary(): Uint8Array;
    toObject(includeInstance?: boolean): CloudProviderConfig.AsObject;
    static toObject(includeInstance: boolean, msg: CloudProviderConfig): CloudProviderConfig.AsObject;
    static extensions: {[key: number]: jspb.ExtensionFieldInfo<jspb.Message>};
    static extensionsBinary: {[key: number]: jspb.ExtensionFieldBinaryInfo<jspb.Message>};
    static serializeBinaryToWriter(message: CloudProviderConfig, writer: jspb.BinaryWriter): void;
    static deserializeBinary(bytes: Uint8Array): CloudProviderConfig;
    static deserializeBinaryFromReader(message: CloudProviderConfig, reader: jspb.BinaryReader): CloudProviderConfig;
}

export namespace CloudProviderConfig {
    export type AsObject = {
        provider: string,
        model: string,
        apiKey: string,
    }
}

export class ProcessRequestResponse extends jspb.Message { 
    getOutput(): string;
    setOutput(value: string): ProcessRequestResponse;

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
    }
}
