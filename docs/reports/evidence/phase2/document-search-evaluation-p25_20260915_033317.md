# Document search evaluation p25_20260915_033317

- Dataset: `document-search-v1` (`d2fe1ecebbb66a3bb15682bc4794d3923a99a30f4cf801856d12f47676a32c50`)
- Embedding profile: `local-hash-v1`
- Top K: `5`
- Decision: **KEEP_BM25**

## Summary

| Engine | Recall@K | MRR | nDCG@K | p50 us | p95 us | max us | errors |
| --- | ---: | ---: | ---: | ---: | ---: | ---: | ---: |
| PostgreSQL BM25 | 1.0000 | 1.0000 | 1.0000 | 3807 | 139226 | 139226 | 0 |
| mixin-search shadow | 1.0000 | 0.9048 | 0.9286 | 19060 | 22220 | 22220 | 0 |

## Correctness

Permission=0, lifecycle=0, active-version=0, formal-scope=0.

## Per query

| ID | Category | BM25 R/M/N | Shadow R/M/N | BM25 us | Shadow us |
| --- | --- | --- | --- | ---: | ---: |
| q-zh | chinese | 1.00 / 1.00 / 1.00 | 1.00 / 1.00 / 1.00 | 139226 | 13619 |
| q-en | english | 1.00 / 1.00 / 1.00 | 1.00 / 1.00 / 1.00 | 3838 | 17564 |
| q-code | code | 1.00 / 1.00 / 1.00 | 1.00 / 1.00 / 1.00 | 3548 | 19893 |
| q-title | title | 1.00 / 1.00 / 1.00 | 1.00 / 0.33 / 0.50 | 3807 | 19060 |
| q-body | body | 1.00 / 1.00 / 1.00 | 1.00 / 1.00 / 1.00 | 3960 | 19526 |
| q-exact | exact_keyword | 1.00 / 1.00 / 1.00 | 1.00 / 1.00 / 1.00 | 3673 | 18946 |
| q-semantic | semantic_expression | 1.00 / 1.00 / 1.00 | 1.00 / 1.00 / 1.00 | 2579 | 22220 |

## Decision reasons

- evaluation-only local hash embedding is not eligible for a semantic read switch
