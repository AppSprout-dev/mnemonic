import * as assert from "assert";
import { LRUCache } from "../util/cache";

describe("LRUCache", () => {
  it("stores and retrieves values", () => {
    const cache = new LRUCache<string, number>(10, 60_000);
    cache.set("a", 1);
    cache.set("b", 2);
    assert.strictEqual(cache.get("a"), 1);
    assert.strictEqual(cache.get("b"), 2);
  });

  it("returns undefined for missing keys", () => {
    const cache = new LRUCache<string, number>(10, 60_000);
    assert.strictEqual(cache.get("missing"), undefined);
  });

  it("evicts oldest entry when at capacity", () => {
    const cache = new LRUCache<string, number>(2, 60_000);
    cache.set("a", 1);
    cache.set("b", 2);
    cache.set("c", 3); // evicts "a"
    assert.strictEqual(cache.get("a"), undefined);
    assert.strictEqual(cache.get("b"), 2);
    assert.strictEqual(cache.get("c"), 3);
  });

  it("promotes recently accessed entries", () => {
    const cache = new LRUCache<string, number>(2, 60_000);
    cache.set("a", 1);
    cache.set("b", 2);
    cache.get("a"); // promote "a" — "b" is now oldest
    cache.set("c", 3); // evicts "b"
    assert.strictEqual(cache.get("a"), 1);
    assert.strictEqual(cache.get("b"), undefined);
    assert.strictEqual(cache.get("c"), 3);
  });

  it("expires entries past TTL", () => {
    // Use a very short TTL
    const cache = new LRUCache<string, number>(10, 1);
    cache.set("a", 1);

    // Busy-wait a few ms to ensure TTL expires
    const start = Date.now();
    while (Date.now() - start < 5) {
      // spin
    }

    assert.strictEqual(cache.get("a"), undefined);
  });

  it("invalidate removes a specific key", () => {
    const cache = new LRUCache<string, number>(10, 60_000);
    cache.set("a", 1);
    cache.invalidate("a");
    assert.strictEqual(cache.get("a"), undefined);
  });

  it("clear removes all entries", () => {
    const cache = new LRUCache<string, number>(10, 60_000);
    cache.set("a", 1);
    cache.set("b", 2);
    cache.clear();
    assert.strictEqual(cache.size, 0);
    assert.strictEqual(cache.get("a"), undefined);
  });
});
