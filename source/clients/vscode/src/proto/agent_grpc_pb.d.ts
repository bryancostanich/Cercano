// package: agent
// file: agent.proto

/* tslint:disable */
/* eslint-disable */

import * as grpc from "@grpc/grpc-js";
import * as agent_pb from "./agent_pb";

interface IAgentService extends grpc.ServiceDefinition<grpc.UntypedServiceImplementation> {
    processRequest: IAgentService_IProcessRequest;
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

export const AgentService: IAgentService;

export interface IAgentServer extends grpc.UntypedServiceImplementation {
    processRequest: grpc.handleUnaryCall<agent_pb.ProcessRequestRequest, agent_pb.ProcessRequestResponse>;
}

export interface IAgentClient {
    processRequest(request: agent_pb.ProcessRequestRequest, callback: (error: grpc.ServiceError | null, response: agent_pb.ProcessRequestResponse) => void): grpc.ClientUnaryCall;
    processRequest(request: agent_pb.ProcessRequestRequest, metadata: grpc.Metadata, callback: (error: grpc.ServiceError | null, response: agent_pb.ProcessRequestResponse) => void): grpc.ClientUnaryCall;
    processRequest(request: agent_pb.ProcessRequestRequest, metadata: grpc.Metadata, options: Partial<grpc.CallOptions>, callback: (error: grpc.ServiceError | null, response: agent_pb.ProcessRequestResponse) => void): grpc.ClientUnaryCall;
}

export class AgentClient extends grpc.Client implements IAgentClient {
    constructor(address: string, credentials: grpc.ChannelCredentials, options?: Partial<grpc.ClientOptions>);
    public processRequest(request: agent_pb.ProcessRequestRequest, callback: (error: grpc.ServiceError | null, response: agent_pb.ProcessRequestResponse) => void): grpc.ClientUnaryCall;
    public processRequest(request: agent_pb.ProcessRequestRequest, metadata: grpc.Metadata, callback: (error: grpc.ServiceError | null, response: agent_pb.ProcessRequestResponse) => void): grpc.ClientUnaryCall;
    public processRequest(request: agent_pb.ProcessRequestRequest, metadata: grpc.Metadata, options: Partial<grpc.CallOptions>, callback: (error: grpc.ServiceError | null, response: agent_pb.ProcessRequestResponse) => void): grpc.ClientUnaryCall;
}
