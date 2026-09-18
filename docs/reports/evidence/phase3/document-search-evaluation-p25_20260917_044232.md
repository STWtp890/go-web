# Document search evaluation p25_20260917_044232

- Dataset: `document-search-v1` (`d2fe1ecebbb66a3bb15682bc4794d3923a99a30f4cf801856d12f47676a32c50`)
- Embedding profile: `local-hash-v1`
- Top K: `5`
- Decision: **KEEP_BM25**

## Summary

| Engine | Recall@K | MRR | nDCG@K | p50 us | p95 us | max us | errors |
| --- | ---: | ---: | ---: | ---: | ---: | ---: | ---: |
| PostgreSQL BM25 | 1.0000 | 1.0000 | 1.0000 | 5385 | 162077 | 162077 | 0 |
| mixin-search shadow | 1.0000 | 0.9048 | 0.9286 | 32861 | 42710 | 42710 | 0 |

## Correctness

Permission=0, lifecycle=0, active-version=0, formal-scope=0.

## Per query

| ID | Category | BM25 R/M/N | Shadow R/M/N | BM25 us | Shadow us |
| --- | --- | --- | --- | ---: | ---: |
| q-zh | chinese | 1.00 / 1.00 / 1.00 | 1.00 / 1.00 / 1.00 | 162077 | 23173 |
| q-en | english | 1.00 / 1.00 / 1.00 | 1.00 / 1.00 / 1.00 | 6989 | 42710 |
| q-code | code | 1.00 / 1.00 / 1.00 | 1.00 / 1.00 / 1.00 | 6000 | 35112 |
| q-title | title | 1.00 / 1.00 / 1.00 | 1.00 / 0.33 / 0.50 | 5385 | 32861 |
| q-body | body | 1.00 / 1.00 / 1.00 | 1.00 / 1.00 / 1.00 | 5381 | 27901 |
| q-exact | exact_keyword | 1.00 / 1.00 / 1.00 | 1.00 / 1.00 / 1.00 | 5375 | 34155 |
| q-semantic | semantic_expression | 1.00 / 1.00 / 1.00 | 1.00 / 1.00 / 1.00 | 3248 | 31396 |

## Decision reasons

- evaluation-only local hash embedding is not eligible for a semantic read switch
