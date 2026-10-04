-- Artikel terjadwal terbit otomatis pada scheduled_at.
ALTER TABLE articles DROP CHECK chk_articles_status;

ALTER TABLE articles
    ADD COLUMN scheduled_at TIMESTAMP NULL DEFAULT NULL AFTER published_at,
    ADD KEY idx_articles_status_scheduled (status, scheduled_at),
    ADD CONSTRAINT chk_articles_status CHECK (status IN ('draft', 'scheduled', 'published', 'archived'));

-- Riwayat isi artikel. Setiap simpanan yang mengubah judul, isi, kategori,
-- tag, atau sampul menambah satu baris.
CREATE TABLE IF NOT EXISTS article_revisions (
    id          BIGINT UNSIGNED NOT NULL AUTO_INCREMENT,
    article_id  BIGINT UNSIGNED NOT NULL,
    editor_id   BIGINT UNSIGNED NULL DEFAULT NULL,
    title       VARCHAR(200)    NOT NULL,
    content     MEDIUMTEXT      NOT NULL,
    category_id BIGINT UNSIGNED NULL DEFAULT NULL,
    tags        JSON            NOT NULL,
    cover_image VARCHAR(255)    NULL DEFAULT NULL,
    created_at  TIMESTAMP       NOT NULL DEFAULT CURRENT_TIMESTAMP,
    PRIMARY KEY (id),
    KEY idx_article_revisions_article (article_id, id),
    CONSTRAINT fk_article_revisions_article FOREIGN KEY (article_id) REFERENCES articles (id) ON DELETE CASCADE,
    CONSTRAINT fk_article_revisions_editor FOREIGN KEY (editor_id) REFERENCES users (id) ON DELETE SET NULL,
    CONSTRAINT fk_article_revisions_category FOREIGN KEY (category_id) REFERENCES categories (id) ON DELETE SET NULL
) ENGINE = InnoDB DEFAULT CHARSET = utf8mb4 COLLATE = utf8mb4_unicode_ci;

-- Artikel yang sudah ada mendapat revisi awal berisi keadaannya sekarang.
INSERT INTO article_revisions (article_id, editor_id, title, content, category_id, tags, cover_image, created_at)
SELECT a.id, a.author_id, a.title, a.content, a.category_id,
       COALESCE((SELECT JSON_ARRAYAGG(t.name)
                 FROM article_tags at JOIN tags t ON t.id = at.tag_id
                 WHERE at.article_id = a.id), JSON_ARRAY()),
       a.cover_image, a.updated_at
FROM articles a;
