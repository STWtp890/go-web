# Document search evaluation p25_20260917_042755

- Dataset: `document-search-v1` (`d2fe1ecebbb66a3bb15682bc4794d3923a99a30f4cf801856d12f47676a32c50`)
- Embedding profile: `local-hash-v1`
- Top K: `5`
- Decision: **KEEP_BM25**

## Summary

| Engine | Recall@K | MRR | nDCG@K | p50 us | p95 us | max us | errors |
| --- | ---: | ---: | ---: | ---: | ---: | ---: | ---: |
| PostgreSQL BM25 | 1.0000 | 1.0000 | 1.0000 | 4787 | 151902 | 151902 | 0 |
| mixin-search shadow | 1.0000 | 0.9048 | 0.9286 | 19908 | 22364 | 22364 | 0 |

## Correctness

Permission=0, lifecycle=0, active-version=0, formal-scope=0.

## Per query

| ID | Category | BM25 R/M/N | Shadow R/M/N | BM25 us | Shadow us |
| --- | --- | --- | --- | ---: | ---: |
| q-zh | chinese | 1.00 / 1.00 / 1.00 | 1.00 / 1.00 / 1.00 | 151902 | 14252 |
| q-en | english | 1.00 / 1.00 / 1.00 | 1.00 / 1.00 / 1.00 | 5242 | 21253 |
| q-code | code | 1.00 / 1.00 / 1.00 | 1.00 / 1.00 / 1.00 | 4929 | 18578 |
| q-title | title | 1.00 / 1.00 / 1.00 | 1.00 / 0.33 / 0.50 | 4315 | 19747 |
| q-body | body | 1.00 / 1.00 / 1.00 | 1.00 / 1.00 / 1.00 | 4787 | 19908 |
| q-exact | exact_keyword | 1.00 / 1.00 / 1.00 | 1.00 / 1.00 / 1.00 | 4444 | 22364 |
| q-semantic | semantic_expression | 1.00 / 1.00 / 1.00 | 1.00 / 1.00 / 1.00 | 2932 | 21426 |

## Decision reasons

- evaluation-only local hash embedding is not eligible for a semantic read switch
