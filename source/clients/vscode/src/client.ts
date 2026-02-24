import * as grpc from '@grpc/grpc-js';
import { AgentClient } from './proto/agent_grpc_pb';
import { ProcessRequestRequest, ProcessRequestResponse, StreamProcessResponse, UpdateConfigRequest } from './proto/agent_pb';

export class CercanoClient {
    private client: AgentClient;

    // Use IPv4 explicit loopback to avoid resolution issues
    constructor(address: string = '127.0.0.1:50052') {
        this.client = new AgentClient(
            address,
            grpc.credentials.createInsecure()
        );
    }

    public updateConfig(config: {
        localModel?: string,
        cloudProvider?: string,
        cloudModel?: string,
        cloudApiKey?: string
    }): Promise<{ success: boolean, message: string }> {
        return new Promise((resolve, reject) => {
            const request = new UpdateConfigRequest();
            if (config.localModel) { request.setLocalModel(config.localModel); }
            if (config.cloudProvider) { request.setCloudProvider(config.cloudProvider); }
            if (config.cloudModel) { request.setCloudModel(config.cloudModel); }
            if (config.cloudApiKey) { request.setCloudApiKey(config.cloudApiKey); }

            this.client.updateConfig(request, (error, response) => {
                if (error) {
                    return reject(error);
                }
                resolve({
                    success: response.getSuccess(),
                    message: response.getMessage()
                });
            });
        });
    }

    public processStream(
        input: string,
        workDir?: string,
        fileName?: string,
        onProgress?: (message: string) => void,
        conversationId?: string,
        onToken?: (token: string) => void
    ): Promise<ProcessRequestResponse> {
        return new Promise((resolve, reject) => {
            const request = new ProcessRequestRequest();
            request.setInput(input);

            if (workDir) { request.setWorkDir(workDir); }
            if (fileName) { request.setFileName(fileName); }
            if (conversationId) { request.setConversationId(conversationId); }

            const deadline = new Date();
            deadline.setSeconds(deadline.getSeconds() + 300);

            const metadata = new grpc.Metadata();
            const call = this.client.streamProcessRequest(request, metadata, { deadline: deadline });

            let finalResponse: ProcessRequestResponse | undefined;

            call.on('data', (response: StreamProcessResponse) => {
                if (response.hasProgress()) {
                    if (onProgress) {
                        onProgress(response.getProgress()!.getMessage());
                    }
                } else if (response.hasTokenDelta()) {
                    if (onToken) {
                        onToken(response.getTokenDelta()!.getContent());
                    }
                } else if (response.hasFinalResponse()) {
                    finalResponse = response.getFinalResponse();
                }
            });

            call.on('error', (err) => {
                reject(err);
            });

            call.on('end', () => {
                if (finalResponse) {
                    resolve(finalResponse);
                } else {
                    reject(new Error("Stream ended without final response"));
                }
            });
        });
    }

    public process(input: string, workDir?: string, fileName?: string, conversationId?: string): Promise<ProcessRequestResponse> {
        return new Promise((resolve, reject) => {
            const request = new ProcessRequestRequest();
            request.setInput(input);

            if (workDir) { request.setWorkDir(workDir); }
            if (fileName) { request.setFileName(fileName); }
            if (conversationId) { request.setConversationId(conversationId); }

            // Set a generous deadline (5 minutes) for complex AI tasks and self-correction loops
            const deadline = new Date();
            deadline.setSeconds(deadline.getSeconds() + 300);

            const metadata = new grpc.Metadata();

            this.client.processRequest(request, metadata, { deadline: deadline }, (error, response) => {
                if (error) {
                    return reject(error);
                }
                resolve(response);
            });
        });
    }

    public close() {
        this.client.close();
    }
}
