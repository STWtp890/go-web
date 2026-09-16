# Document search evaluation p25_20260914_231537

- Dataset: `document-search-v1` (`d2fe1ecebbb66a3bb15682bc4794d3923a99a30f4cf801856d12f47676a32c50`)
- Embedding profile: `local-hash-v1`
- Top K: `5`
- Decision: **KEEP_BM25**

## Summary

| Engine | Recall@K | MRR | nDCG@K | p50 us | p95 us | max us | errors |
| --- | ---: | ---: | ---: | ---: | ---: | ---: | ---: |
| PostgreSQL BM25 | 1.0000 | 1.0000 | 1.0000 | 4229 | 135085 | 135085 | 0 |
| mixin-search shadow | 1.0000 | 0.9048 | 0.9286 | 103167 | 459029 | 459029 | 0 |

## Correctness

Permission=0, lifecycle=0, active-version=0, formal-scope=0.

## Per query

| ID | Category | BM25 R/M/N | Shadow R/M/N | BM25 us | Shadow us |
| --- | --- | --- | --- | ---: | ---: |
| q-zh | chinese | 1.00 / 1.00 / 1.00 | 1.00 / 1.00 / 1.00 | 135085 | 23088 |
| q-en | english | 1.00 / 1.00 / 1.00 | 1.00 / 1.00 / 1.00 | 4186 | 61543 |
| q-code | code | 1.00 / 1.00 / 1.00 | 1.00 / 1.00 / 1.00 | 4229 | 68721 |
| q-title | title | 1.00 / 1.00 / 1.00 | 1.00 / 0.33 / 0.50 | 4333 | 141395 |
| q-body | body | 1.00 / 1.00 / 1.00 | 1.00 / 1.00 / 1.00 | 4639 | 161137 |
| q-exact | exact_keyword | 1.00 / 1.00 / 1.00 | 1.00 / 1.00 / 1.00 | 3659 | 459029 |
| q-semantic | semantic_expression | 1.00 / 1.00 / 1.00 | 1.00 / 1.00 / 1.00 | 2768 | 103167 |

## Decision reasons

- evaluation-only local hash embedding is not eligible for a semantic read switch
