DROP TABLE IF EXISTS follows;

ALTER TABLE users
    DROP COLUMN avatar_url,
    DROP COLUMN bio;
