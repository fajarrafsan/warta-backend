---
title: Membangun REST API yang Tahan Banting dengan Go
author: dimas
category: Teknologi
tags: golang, api, backend
status: published
days_ago: 1
cover: 0
---
Banyak tutorial berhenti di "API sudah bisa dipanggil". Padahal pekerjaan sebenarnya baru dimulai setelah itu: bagaimana API tetap waras ketika datanya salah, servernya lambat, atau penggunanya iseng.

## Mulai dari bentuk error

Sebelum menulis endpoint pertama, sepakati bentuk error. Klien jauh lebih mudah ditulis bila setiap kegagalan punya struktur yang sama:

```json
{"error": {"code": "validation_failed", "message": "validasi gagal", "fields": {"title": "title minimal 20 karakter"}}}
```

Kode yang stabil (`validation_failed`, `not_found`) dipakai program, pesan dipakai manusia, dan `fields` menunjuk kolom mana yang salah.

## Pisahkan lapisan

Susunan yang membosankan justru paling awet:

- **handler** membaca HTTP dan menulis response,
- **service** memegang aturan bisnis dan hak akses,
- **repository** satu-satunya yang tahu SQL.

Dengan begitu aturan seperti "draft hanya terlihat oleh penulisnya" cukup ditulis sekali di service, bukan tersebar di setiap query.

## Batasi semuanya

Setiap body permintaan diberi batas ukuran, setiap daftar diberi batas halaman, dan setiap endpoint login diberi batas percobaan per menit. Batas bukan soal pelit, melainkan supaya satu klien yang salah tidak menjatuhkan semua orang.

> API yang baik bukan yang tidak pernah gagal, tetapi yang gagal dengan cara yang bisa ditebak.

Terakhir, tulis test yang memanggil API lewat HTTP sungguhan dengan database sungguhan. Test semacam itu lebih lambat, tetapi menangkap kesalahan yang tidak pernah terlihat di unit test.
