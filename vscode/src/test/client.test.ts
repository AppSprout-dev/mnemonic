import * as assert from "assert";
import { MnemonicClient, MnemonicApiError } from "../api/client";

describe("MnemonicClient", () => {
  it("constructs with endpoint and optional token", () => {
    const client = new MnemonicClient("http://127.0.0.1:9999", "test-token");
    assert.strictEqual(client.getEndpoint(), "http://127.0.0.1:9999");
  });

  it("strips trailing slashes from endpoint", () => {
    const client = new MnemonicClient("http://127.0.0.1:9999///");
    assert.strictEqual(client.getEndpoint(), "http://127.0.0.1:9999");
  });

  it("updateConfig changes the endpoint", () => {
    const client = new MnemonicClient("http://localhost:9999");
    client.updateConfig("http://192.168.1.5:9999", "new-token");
    assert.strictEqual(client.getEndpoint(), "http://192.168.1.5:9999");
  });

  it("throws MnemonicApiError after dispose", async () => {
    const client = new MnemonicClient("http://127.0.0.1:9999");
    client.dispose();
    await assert.rejects(
      () => client.health(),
      (err: unknown) => {
        assert.ok(err instanceof MnemonicApiError);
        assert.strictEqual(err.code, "DISPOSED");
        return true;
      }
    );
  });

  it("throws NETWORK_ERROR when daemon is unreachable", async () => {
    // Use a port that's almost certainly not listening
    const client = new MnemonicClient("http://127.0.0.1:19999");
    await assert.rejects(
      () => client.health(),
      (err: unknown) => {
        assert.ok(err instanceof MnemonicApiError);
        assert.strictEqual(err.code, "NETWORK_ERROR");
        return true;
      }
    );
  });
});

describe("MnemonicApiError", () => {
  it("has status, code, and message", () => {
    const err = new MnemonicApiError(404, "NOT_FOUND", "memory not found");
    assert.strictEqual(err.status, 404);
    assert.strictEqual(err.code, "NOT_FOUND");
    assert.strictEqual(err.message, "memory not found");
    assert.strictEqual(err.name, "MnemonicApiError");
  });
});
