import * as grpc from '@grpc/grpc-js';
import { AgentClient } from './proto/agent_grpc_pb';
import { ProcessRequestRequest, ProcessRequestResponse, CloudProviderConfig, StreamProcessResponse } from './proto/agent_pb';

export class CercanoClient {
    private client: AgentClient;

    // Use IPv4 explicit loopback to avoid resolution issues
    constructor(address: string = '127.0.0.1:50052') {
        this.client = new AgentClient(
            address,
            grpc.credentials.createInsecure()
        );
    }

    public processStream(
        input: string,
        workDir?: string,
        fileName?: string,
        providerConfig?: { provider: string, model: string, apiKey: string },
        onProgress?: (message: string) => void
    ): Promise<ProcessRequestResponse> {
        return new Promise((resolve, reject) => {
            const request = new ProcessRequestRequest();
            request.setInput(input);

            if (workDir) { request.setWorkDir(workDir); }
            if (fileName) { request.setFileName(fileName); }

            if (providerConfig) {
                const config = new CloudProviderConfig();
                config.setProvider(providerConfig.provider);
                config.setModel(providerConfig.model);
                config.setApiKey(providerConfig.apiKey);
                request.setProviderConfig(config);
            }

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

    public process(input: string, workDir?: string, fileName?: string, providerConfig?: { provider: string, model: string, apiKey: string }): Promise<ProcessRequestResponse> {
        return new Promise((resolve, reject) => {
            const request = new ProcessRequestRequest();
            request.setInput(input);

            if (workDir) { request.setWorkDir(workDir); }
            if (fileName) { request.setFileName(fileName); }

            if (providerConfig) {
                const config = new CloudProviderConfig();
                config.setProvider(providerConfig.provider);
                config.setModel(providerConfig.model);
                config.setApiKey(providerConfig.apiKey);
                request.setProviderConfig(config);
            }

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