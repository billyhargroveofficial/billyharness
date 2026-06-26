const assert = require("assert");
const { tokenize, buildIndex, search, highlight } = require("./src/indexer");

assert.deepStrictEqual(tokenize("Hello, HELLO-world_42"), ["hello", "hello", "world", "42"]);

const docs = [
  { id: "b", text: "Fast agents build apps" },
  { id: "a", text: "Agents test code fast" },
  { id: "c", text: "Slow manual testing" },
];
const index = buildIndex(docs);
assert.deepStrictEqual(index.agents, ["a", "b"]);
assert.deepStrictEqual(index.fast, ["a", "b"]);
assert.deepStrictEqual(index.testing, ["c"]);

assert.deepStrictEqual(search(index, "agents fast"), ["a", "b"]);
assert.deepStrictEqual(search(index, "agents code"), ["a"]);
assert.deepStrictEqual(search(index, "missing"), []);
assert.deepStrictEqual(search(index, ""), []);

assert.strictEqual(highlight("Fast agents are fast.", "fast agents"), "[[Fast]] [[agents]] are [[fast]].");
assert.strictEqual(highlight("No match", ""), "No match");

console.log("ok");
