import * as grpc from '@grpc/grpc-js';
import { AgentClient } from './proto/agent_grpc_pb';
import { ProcessRequestRequest, ProcessRequestResponse, CloudProviderConfig } from './proto/agent_pb';

export class CercanoClient {
    private client: AgentClient;

    // Use IPv4 explicit loopback to avoid resolution issues
    constructor(address: string = '127.0.0.1:50052') {
        this.client = new AgentClient(
            address,
            grpc.credentials.createInsecure()
        );
    }

    public process(input: string, providerConfig?: { provider: string, model: string, apiKey: string }): Promise<string> {
        return new Promise((resolve, reject) => {
            const request = new ProcessRequestRequest();
            request.setInput(input);

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
                resolve(response.getOutput());
            });
        });
    }

    public close() {
        this.client.close();
    }
}