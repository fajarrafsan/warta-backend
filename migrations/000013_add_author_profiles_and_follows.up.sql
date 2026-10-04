-- Profil publik penulis: bio singkat dan foto.
ALTER TABLE users
    ADD COLUMN bio VARCHAR(300) NOT NULL DEFAULT '' AFTER email_verified_at,
    ADD COLUMN avatar_url VARCHAR(255) NULL DEFAULT NULL AFTER bio;

-- Pembaca mengikuti penulis untuk melihat artikel terbaru mereka.
CREATE TABLE IF NOT EXISTS follows (
    follower_id BIGINT UNSIGNED NOT NULL,
    author_id   BIGINT UNSIGNED NOT NULL,
    created_at  TIMESTAMP       NOT NULL DEFAULT CURRENT_TIMESTAMP,
    PRIMARY KEY (follower_id, author_id),
    KEY idx_follows_author (author_id),
    CONSTRAINT fk_follows_follower FOREIGN KEY (follower_id) REFERENCES users (id) ON DELETE CASCADE,
    CONSTRAINT fk_follows_author FOREIGN KEY (author_id) REFERENCES users (id) ON DELETE CASCADE,
    CONSTRAINT chk_follows_self CHECK (follower_id <> author_id)
) ENGINE = InnoDB DEFAULT CHARSET = utf8mb4 COLLATE = utf8mb4_unicode_ci;
