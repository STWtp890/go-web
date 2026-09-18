# Document search evaluation p25_20260919_032755

- Dataset: `document-search-v1` (`d2fe1ecebbb66a3bb15682bc4794d3923a99a30f4cf801856d12f47676a32c50`)
- Embedding profile: `local-hash-v1`
- Top K: `5`
- Decision: **KEEP_BM25**

## Summary

| Engine | Recall@K | MRR | nDCG@K | p50 us | p95 us | max us | errors |
| --- | ---: | ---: | ---: | ---: | ---: | ---: | ---: |
| PostgreSQL BM25 | 1.0000 | 1.0000 | 1.0000 | 7060 | 188630 | 188630 | 0 |
| mixin-search shadow | 1.0000 | 0.9048 | 0.9286 | 3808 | 4960 | 4960 | 0 |

## Correctness

Permission=0, lifecycle=0, active-version=0, formal-scope=0.

## Per query

| ID | Category | BM25 R/M/N | Shadow R/M/N | BM25 us | Shadow us |
| --- | --- | --- | --- | ---: | ---: |
| q-zh | chinese | 1.00 / 1.00 / 1.00 | 1.00 / 1.00 / 1.00 | 187253 | 3934 |
| q-en | english | 1.00 / 1.00 / 1.00 | 1.00 / 1.00 / 1.00 | 7104 | 3928 |
| q-code | code | 1.00 / 1.00 / 1.00 | 1.00 / 1.00 / 1.00 | 188630 | 2879 |
| q-title | title | 1.00 / 1.00 / 1.00 | 1.00 / 0.33 / 0.50 | 6219 | 3552 |
| q-body | body | 1.00 / 1.00 / 1.00 | 1.00 / 1.00 / 1.00 | 7060 | 3808 |
| q-exact | exact_keyword | 1.00 / 1.00 / 1.00 | 1.00 / 1.00 / 1.00 | 6434 | 2710 |
| q-semantic | semantic_expression | 1.00 / 1.00 / 1.00 | 1.00 / 1.00 / 1.00 | 4629 | 4960 |

## Decision reasons

- evaluation-only local hash embedding is not eligible for a semantic read switch
