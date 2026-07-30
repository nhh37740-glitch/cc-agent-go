# DeepSeek V4 tokenizer

- Source: `deepseek-ai/DeepSeek-V4-Pro/tokenizer.json`
- Revision: `b5968e9190ef611bbf34a7229255be88a0e937c1`
- SHA-256: `8f9f37ca37fdc4f5fd36d5cf4d3b0e8392edb4e894fd10cc0d70b4957c8633cf`
- Local file: `tokenizers/deepseek-v4-pro/tokenizer.json`

`modeltoken/huggingface_json_token_counter_test.go` records fixed counts produced
by the official Hugging Face `AutoTokenizer` at the revision above.

`service/token_usage_integration_test.go` compares one fixed local prepared
request with DeepSeek `usage.input_tokens`. Run it with:

```text
RUN_DEEPSEEK_TOKEN_INTEGRATION=1 go test ./service -run TestLocalDeepSeekTokenizerAndProviderUsageForFixedRequest -v
```

The local count is used before a request to protect the context window. The
provider `usage` fields remain authoritative after the request completes.

Measured on 2026-07-30 with the fixed integration-test request:

- local prepared JSON: 60 tokens
- DeepSeek `usage.input_tokens`: 22 tokens
- measured provider-minus-local difference: -38 tokens

The implementation does not add or subtract this measured value. The local
prepared JSON count is deliberately conservative before sending; the returned
DeepSeek usage replaces it in the completed-run statistics.
