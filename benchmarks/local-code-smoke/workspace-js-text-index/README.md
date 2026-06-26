# JS Text Index Smoke Task

Implement `src/indexer.js`.

Required API:

- `tokenize(text)`: lowercase tokens, split on non-alphanumeric characters, drop empty tokens.
- `buildIndex(docs)`: docs is an array of `{ id, text }`; return an inverted index object mapping token to sorted unique doc ids.
- `search(index, query)`: return sorted doc ids that contain all query tokens. Empty query returns `[]`.
- `highlight(text, query)`: wrap case-insensitive query token matches in `[[...]]`, preserving original text case.

Run:

```bash
node test.js
```
