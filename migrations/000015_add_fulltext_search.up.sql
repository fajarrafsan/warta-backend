-- Pencarian berdasarkan relevansi. InnoDB hanya bisa membuat satu indeks
-- FULLTEXT per ALTER. Judul punya indeks sendiri supaya kecocokan di judul
-- bisa diberi bobot lebih.
ALTER TABLE articles ADD FULLTEXT KEY ft_articles_title (title);

ALTER TABLE articles ADD FULLTEXT KEY ft_articles_search (title, content);
