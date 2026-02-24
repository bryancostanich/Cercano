import * as assert from 'assert';
import * as sinon from 'sinon';
import { CercanoClient } from '../client';
import { AgentClient } from '../proto/agent_grpc_pb';
import { ProcessRequestResponse, ProcessRequestRequest, UpdateConfigRequest, UpdateConfigResponse } from '../proto/agent_pb';

suite('CercanoClient Test Suite', () => {
    let client: CercanoClient;
    let agentClientStub: sinon.SinonStubbedInstance<AgentClient>;

    setup(() => {
        // Stub the prototype of AgentClient to intercept calls
        agentClientStub = sinon.createStubInstance(AgentClient);
    });

    test('process returns response object on success', async () => {
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
        assert.strictEqual(result.getOutput(), expectedOutput);
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

    test('process includes workDir and fileName', async () => {
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

        await client.process("input", "/tmp", "test.txt");

        assert.ok(capturedRequest);
        assert.strictEqual(capturedRequest!.getWorkDir(), "/tmp");
        assert.strictEqual(capturedRequest!.getFileName(), "test.txt");
    });

    test('updateConfig sends config to server', async () => {
        client = new CercanoClient();
        (client as any).client = agentClientStub;

        const response = new UpdateConfigResponse();
        response.setSuccess(true);
        response.setMessage("updated: [local_model=GLM-4.7-Flash]");

        let capturedRequest: UpdateConfigRequest | undefined;
        agentClientStub.updateConfig.callsFake((req, cb: any) => {
            capturedRequest = req;
            cb(null, response);
            return {} as any;
        });

        const result = await client.updateConfig({ localModel: 'GLM-4.7-Flash' });

        assert.ok(capturedRequest);
        assert.strictEqual(capturedRequest!.getLocalModel(), 'GLM-4.7-Flash');
        assert.strictEqual(result.success, true);
    });
});
