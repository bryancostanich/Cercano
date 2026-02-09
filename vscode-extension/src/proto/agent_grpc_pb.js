// GENERATED CODE -- DO NOT EDIT!

'use strict';
var grpc = require('@grpc/grpc-js');
var agent_pb = require('./agent_pb.js');

function serialize_agent_ProcessRequestRequest(arg) {
  if (!(arg instanceof agent_pb.ProcessRequestRequest)) {
    throw new Error('Expected argument of type agent.ProcessRequestRequest');
  }
  return Buffer.from(arg.serializeBinary());
}

function deserialize_agent_ProcessRequestRequest(buffer_arg) {
  return agent_pb.ProcessRequestRequest.deserializeBinary(new Uint8Array(buffer_arg));
}

function serialize_agent_ProcessRequestResponse(arg) {
  if (!(arg instanceof agent_pb.ProcessRequestResponse)) {
    throw new Error('Expected argument of type agent.ProcessRequestResponse');
  }
  return Buffer.from(arg.serializeBinary());
}

function deserialize_agent_ProcessRequestResponse(buffer_arg) {
  return agent_pb.ProcessRequestResponse.deserializeBinary(new Uint8Array(buffer_arg));
}


// The Agent service definition.
var AgentService = exports.AgentService = {
  // ProcessRequest handles AI requests.
processRequest: {
    path: '/agent.Agent/ProcessRequest',
    requestStream: false,
    responseStream: false,
    requestType: agent_pb.ProcessRequestRequest,
    responseType: agent_pb.ProcessRequestResponse,
    requestSerialize: serialize_agent_ProcessRequestRequest,
    requestDeserialize: deserialize_agent_ProcessRequestRequest,
    responseSerialize: serialize_agent_ProcessRequestResponse,
    responseDeserialize: deserialize_agent_ProcessRequestResponse,
  },
};

exports.AgentClient = grpc.makeGenericClientConstructor(AgentService, 'Agent');
