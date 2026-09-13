-- document/search_setup.sql - BM25/jieba index for the active document projection.
CREATE INDEX idx_document_search_projection_bm25
    ON document_search_projection USING bm25 (document_id, (search_text::pdb.jieba))
    WITH (key_field = 'document_id');