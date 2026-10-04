DROP TABLE IF EXISTS article_revisions;

-- Artikel terjadwal kembali menjadi draft.
UPDATE articles SET status = 'draft', updated_at = updated_at WHERE status = 'scheduled';

ALTER TABLE articles DROP CHECK chk_articles_status;

ALTER TABLE articles
    DROP KEY idx_articles_status_scheduled,
    DROP COLUMN scheduled_at,
    ADD CONSTRAINT chk_articles_status CHECK (status IN ('draft', 'published', 'archived'));
