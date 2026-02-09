import * as assert from 'assert';
import * as sinon from 'sinon';
import { CercanoClient } from '../client';
import { AgentClient } from '../proto/agent_grpc_pb';
import { ProcessRequestResponse } from '../proto/agent_pb';

suite('CercanoClient Test Suite', () => {
    let client: CercanoClient;
    let agentClientStub: sinon.SinonStubbedInstance<AgentClient>;

    setup(() => {
        // Stub the prototype of AgentClient to intercept calls
        agentClientStub = sinon.createStubInstance(AgentClient);
        
        // We need to inject the mock into the client. 
        // Since `client` property is private, we can cast to any or modify the class to accept a client.
        // For testing, modifying the constructor is cleaner, but let's try casting first to avoid changing production code just for tests if possible.
        // Actually, creating a subclass or just mocking the module is better, but sinon works well with instance injection.
        
        // Let's modify the class slightly to allow injection for testing, or use prototype mocking.
        // Given TypeScript, let's update client.ts to accept an optional client instance.
    });

    test('process returns output on success', async () => {
        // We will mock this test by manually overwriting the client property
        // This is a bit hacky but effective for simple TS tests without dependency injection containers
        client = new CercanoClient();
        (client as any).client = agentClientStub;

        const expectedOutput = "Mock Output";
        const response = new ProcessRequestResponse();
        response.setOutput(expectedOutput);

        agentClientStub.processRequest.callsFake((req, cb: any) => {
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

        agentClientStub.processRequest.callsFake((req, cb: any) => {
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
});
