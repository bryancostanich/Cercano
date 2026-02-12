import * as grpc from '@grpc/grpc-js';
import { AgentClient } from './proto/agent_grpc_pb';
import { ProcessRequestRequest, ProcessRequestResponse } from './proto/agent_pb';

export class CercanoClient {
    private client: AgentClient;

    // Use IPv4 explicit loopback to avoid resolution issues
    constructor(address: string = '127.0.0.1:50052') {
        this.client = new AgentClient(
            address,
            grpc.credentials.createInsecure()
        );
    }

    public process(input: string): Promise<string> {
        return new Promise((resolve, reject) => {
            const request = new ProcessRequestRequest();
            request.setInput(input);

            // Set a 5-second deadline to prevent hanging
            const deadline = new Date();
            deadline.setSeconds(deadline.getSeconds() + 5);

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
