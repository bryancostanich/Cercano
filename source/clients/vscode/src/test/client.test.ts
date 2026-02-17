import * as assert from 'assert';
import * as sinon from 'sinon';
import { CercanoClient } from '../client';
import { AgentClient } from '../proto/agent_grpc_pb';
import { ProcessRequestResponse, ProcessRequestRequest } from '../proto/agent_pb';

suite('CercanoClient Test Suite', () => {
    let client: CercanoClient;
    let agentClientStub: sinon.SinonStubbedInstance<AgentClient>;

    setup(() => {
        // Stub the prototype of AgentClient to intercept calls
        agentClientStub = sinon.createStubInstance(AgentClient);
    });

    test('process returns output on success', async () => {
        client = new CercanoClient();
        (client as any).client = agentClientStub;

        const expectedOutput = "Mock Output";
        const response = new ProcessRequestResponse();
        response.setOutput(expectedOutput);

        agentClientStub.processRequest.callsFake((req, meta, options, cb: any) => {
            cb(null, response);
            return {} as any;
        });

        const result = await client.process("input");
        assert.strictEqual(result, expectedOutput);
    });

    test('process throws error on failure', async () => {
        client = new CercanoClient();
        (client as any).client = agentClientStub;

        const expectedError = new Error("gRPC Error");

        agentClientStub.processRequest.callsFake((req, meta, options, cb: any) => {
            cb(expectedError, null);
            return {} as any;
        });

        try {
            await client.process("input");
            assert.fail("Should have thrown error");
        } catch (err) {
            assert.strictEqual(err, expectedError);
        }
    });

    test('process includes provider config if provided', async () => {
        client = new CercanoClient();
        (client as any).client = agentClientStub;

        const response = new ProcessRequestResponse();
        response.setOutput("Output");

        let capturedRequest: ProcessRequestRequest | undefined;
        agentClientStub.processRequest.callsFake((req, meta, options, cb: any) => {
            capturedRequest = req;
            cb(null, response);
            return {} as any;
        });

        const providerConfig = {
            provider: "gemini",
            model: "gemini-1.5-pro",
            apiKey: "test-key"
        };

        // Now process takes 2 arguments
        await client.process("input", providerConfig);

        assert.ok(capturedRequest);
        const config = capturedRequest!.getProviderConfig();
        assert.ok(config);
        assert.strictEqual(config!.getProvider(), "gemini");
        assert.strictEqual(config!.getModel(), "gemini-1.5-pro");
        assert.strictEqual(config!.getApiKey(), "test-key");
    });
});
