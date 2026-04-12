import * as assert from "assert";
import { debounce } from "../util/debounce";

describe("debounce", () => {
  it("delays execution", (done) => {
    let calls = 0;
    const fn = debounce(() => {
      calls++;
    }, 50);

    fn();
    fn();
    fn();

    // Should not have been called yet
    assert.strictEqual(calls, 0);

    setTimeout(() => {
      assert.strictEqual(calls, 1);
      done();
    }, 100);
  });

  it("cancel prevents execution", (done) => {
    let calls = 0;
    const fn = debounce(() => {
      calls++;
    }, 50);

    fn();
    fn.cancel();

    setTimeout(() => {
      assert.strictEqual(calls, 0);
      done();
    }, 100);
  });
});
