# Web Tools And Cache

Native web tools are built into billyharness:

- `web_search`
- `web_fetch`
- `web_extract`
- `web_crawl`

`web_fetch`, `web_extract`, and `web_crawl` return compact summaries by default. Large extracted text is stored out of band in `output_ref` files under `$BILLYHARNESS_HOME/tool-output`.

Very short text responses, such as weather one-liners, use `output_class="tiny_direct_answer"`. They are returned inline and do not call the model summarizer even when model summaries are enabled.

## Cache

The web cache stores compact web outputs under:

```sh
$BILLYHARNESS_HOME/web-cache
```

Default settings:

```toml
web_cache_enabled = true
web_cache_ttl_sec = 600
web_cache_max_bytes = 134217728
```

Environment overrides:

```sh
FAST_AGENT_WEB_CACHE_ENABLED=true
FAST_AGENT_WEB_CACHE_TTL_SEC=600
FAST_AGENT_WEB_CACHE_MAX_BYTES=134217728
```

Cache keys include URL, query, extraction/output budget, crawl options, and summarizer configuration. Repeated compatible fetch/extract/crawl calls can reuse the compact output instead of refetching and rerunning the model summarizer.

Inspect and clear the cache through tools:

```json
{"tool":"web_cache_status","arguments":{}}
{"tool":"web_cache_clear","arguments":{}}
```

Tool results expose `web_cache_hit`, `web_cache_miss`, `web_cache_key`, `web_cache_age_ms`, and `web_cache_ttl_ms` in metadata.
