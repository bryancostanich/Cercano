// package: agent
// file: agent.proto

/* tslint:disable */
/* eslint-disable */

import * as grpc from "@grpc/grpc-js";
import * as agent_pb from "./agent_pb";

interface IAgentService extends grpc.ServiceDefinition<grpc.UntypedServiceImplementation> {
    processRequest: IAgentService_IProcessRequest;
    streamProcessRequest: IAgentService_IStreamProcessRequest;
    updateConfig: IAgentService_IUpdateConfig;
}

interface IAgentService_IProcessRequest extends grpc.MethodDefinition<agent_pb.ProcessRequestRequest, agent_pb.ProcessRequestResponse> {
    path: "/agent.Agent/ProcessRequest";
    requestStream: false;
    responseStream: false;
    requestSerialize: grpc.serialize<agent_pb.ProcessRequestRequest>;
    requestDeserialize: grpc.deserialize<agent_pb.ProcessRequestRequest>;
    responseSerialize: grpc.serialize<agent_pb.ProcessRequestResponse>;
    responseDeserialize: grpc.deserialize<agent_pb.ProcessRequestResponse>;
}
interface IAgentService_IStreamProcessRequest extends grpc.MethodDefinition<agent_pb.ProcessRequestRequest, agent_pb.StreamProcessResponse> {
    path: "/agent.Agent/StreamProcessRequest";
    requestStream: false;
    responseStream: true;
    requestSerialize: grpc.serialize<agent_pb.ProcessRequestRequest>;
    requestDeserialize: grpc.deserialize<agent_pb.ProcessRequestRequest>;
    responseSerialize: grpc.serialize<agent_pb.StreamProcessResponse>;
    responseDeserialize: grpc.deserialize<agent_pb.StreamProcessResponse>;
}
interface IAgentService_IUpdateConfig extends grpc.MethodDefinition<agent_pb.UpdateConfigRequest, agent_pb.UpdateConfigResponse> {
    path: "/agent.Agent/UpdateConfig";
    requestStream: false;
    responseStream: false;
    requestSerialize: grpc.serialize<agent_pb.UpdateConfigRequest>;
    requestDeserialize: grpc.deserialize<agent_pb.UpdateConfigRequest>;
    responseSerialize: grpc.serialize<agent_pb.UpdateConfigResponse>;
    responseDeserialize: grpc.deserialize<agent_pb.UpdateConfigResponse>;
}

export const AgentService: IAgentService;

export interface IAgentServer extends grpc.UntypedServiceImplementation {
    processRequest: grpc.handleUnaryCall<agent_pb.ProcessRequestRequest, agent_pb.ProcessRequestResponse>;
    streamProcessRequest: grpc.handleServerStreamingCall<agent_pb.ProcessRequestRequest, agent_pb.StreamProcessResponse>;
    updateConfig: grpc.handleUnaryCall<agent_pb.UpdateConfigRequest, agent_pb.UpdateConfigResponse>;
}

export interface IAgentClient {
    processRequest(request: agent_pb.ProcessRequestRequest, callback: (error: grpc.ServiceError | null, response: agent_pb.ProcessRequestResponse) => void): grpc.ClientUnaryCall;
    processRequest(request: agent_pb.ProcessRequestRequest, metadata: grpc.Metadata, callback: (error: grpc.ServiceError | null, response: agent_pb.ProcessRequestResponse) => void): grpc.ClientUnaryCall;
    processRequest(request: agent_pb.ProcessRequestRequest, metadata: grpc.Metadata, options: Partial<grpc.CallOptions>, callback: (error: grpc.ServiceError | null, response: agent_pb.ProcessRequestResponse) => void): grpc.ClientUnaryCall;
    streamProcessRequest(request: agent_pb.ProcessRequestRequest, options?: Partial<grpc.CallOptions>): grpc.ClientReadableStream<agent_pb.StreamProcessResponse>;
    streamProcessRequest(request: agent_pb.ProcessRequestRequest, metadata?: grpc.Metadata, options?: Partial<grpc.CallOptions>): grpc.ClientReadableStream<agent_pb.StreamProcessResponse>;
    updateConfig(request: agent_pb.UpdateConfigRequest, callback: (error: grpc.ServiceError | null, response: agent_pb.UpdateConfigResponse) => void): grpc.ClientUnaryCall;
    updateConfig(request: agent_pb.UpdateConfigRequest, metadata: grpc.Metadata, callback: (error: grpc.ServiceError | null, response: agent_pb.UpdateConfigResponse) => void): grpc.ClientUnaryCall;
    updateConfig(request: agent_pb.UpdateConfigRequest, metadata: grpc.Metadata, options: Partial<grpc.CallOptions>, callback: (error: grpc.ServiceError | null, response: agent_pb.UpdateConfigResponse) => void): grpc.ClientUnaryCall;
}

export class AgentClient extends grpc.Client implements IAgentClient {
    constructor(address: string, credentials: grpc.ChannelCredentials, options?: Partial<grpc.ClientOptions>);
    public processRequest(request: agent_pb.ProcessRequestRequest, callback: (error: grpc.ServiceError | null, response: agent_pb.ProcessRequestResponse) => void): grpc.ClientUnaryCall;
    public processRequest(request: agent_pb.ProcessRequestRequest, metadata: grpc.Metadata, callback: (error: grpc.ServiceError | null, response: agent_pb.ProcessRequestResponse) => void): grpc.ClientUnaryCall;
    public processRequest(request: agent_pb.ProcessRequestRequest, metadata: grpc.Metadata, options: Partial<grpc.CallOptions>, callback: (error: grpc.ServiceError | null, response: agent_pb.ProcessRequestResponse) => void): grpc.ClientUnaryCall;
    public streamProcessRequest(request: agent_pb.ProcessRequestRequest, options?: Partial<grpc.CallOptions>): grpc.ClientReadableStream<agent_pb.StreamProcessResponse>;
    public streamProcessRequest(request: agent_pb.ProcessRequestRequest, metadata?: grpc.Metadata, options?: Partial<grpc.CallOptions>): grpc.ClientReadableStream<agent_pb.StreamProcessResponse>;
    public updateConfig(request: agent_pb.UpdateConfigRequest, callback: (error: grpc.ServiceError | null, response: agent_pb.UpdateConfigResponse) => void): grpc.ClientUnaryCall;
    public updateConfig(request: agent_pb.UpdateConfigRequest, metadata: grpc.Metadata, callback: (error: grpc.ServiceError | null, response: agent_pb.UpdateConfigResponse) => void): grpc.ClientUnaryCall;
    public updateConfig(request: agent_pb.UpdateConfigRequest, metadata: grpc.Metadata, options: Partial<grpc.CallOptions>, callback: (error: grpc.ServiceError | null, response: agent_pb.UpdateConfigResponse) => void): grpc.ClientUnaryCall;
}
