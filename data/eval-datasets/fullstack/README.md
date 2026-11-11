# Fullstack frozen evaluation scopes

These datasets are release evidence for an already-published local snapshot, not fixture input. `snap_001-local-rag-handbook-v1` is intentionally small: it verifies the only local handbook revision currently in the fullstack bootstrap. It is sufficient for the lexical configuration repair but is not a production-wide benchmark; production promotion still requires the larger frozen release suite described in the harness.

Run only with `runtime-remote`, `snap_001-scope.json`, a real Runtime service token and the matching immutable config pair. The runner must not materialize sample data or use its TestClient.

The evaluator must reproduce all immutable generation fields, including `response_style` and `top_p`; an incomplete runner payload is an experiment-identity mismatch, not a valid baseline.
