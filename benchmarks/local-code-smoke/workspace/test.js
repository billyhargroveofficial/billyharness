const assert = require("assert");
const { add, multiply, median, slugify } = require("./src/calc");

assert.strictEqual(add(2, 3), 5);
assert.strictEqual(add(-4, 9), 5);
assert.strictEqual(multiply(6, 7), 42);
assert.strictEqual(multiply(-3, 5), -15);

const original = [9, 1, 5, 3];
assert.strictEqual(median(original), 4);
assert.deepStrictEqual(original, [9, 1, 5, 3]);
assert.strictEqual(median([7, 1, 3]), 3);
assert.throws(() => median([]), /non-empty/);

assert.strictEqual(slugify(" Hello, Fast Agent Harness! "), "hello-fast-agent-harness");
assert.strictEqual(slugify("DeepSeek   V4___Flash"), "deepseek-v4-flash");
assert.strictEqual(slugify("---Already---Sluggy---"), "already-sluggy");

console.log("ok");
