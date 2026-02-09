import * as grpc from '@grpc/grpc-js';
import { AgentClient } from './proto/agent_grpc_pb';
import { ProcessRequestRequest, ProcessRequestResponse } from './proto/agent_pb';

export class CercanoClient {
    private client: AgentClient;

    constructor(address: string = 'localhost:50051') {
        this.client = new AgentClient(
            address,
            grpc.credentials.createInsecure()
        );
    }

    public process(input: string): Promise<string> {
        return new Promise((resolve, reject) => {
            const request = new ProcessRequestRequest();
            request.setInput(input);

            this.client.processRequest(request, (error, response) => {
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
