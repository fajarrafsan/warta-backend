---
title: Panduan Singkat Menulis Pesan Commit yang Berguna
author: dimas
category: Teknologi
tags: git, kode
status: published
days_ago: 25
cover: 3
---
Pesan commit adalah surat untuk diri sendiri enam bulan lagi. Saat itu kita tidak lagi ingat konteksnya, dan pesan "perbaiki bug" tidak menolong sama sekali.

## Baris pertama: apa yang berubah

Tulis singkat, sekitar lima puluh karakter, dalam bentuk perintah:

```
Tolak komentar yang berisi lebih dari dua tautan
```

## Badan pesan: mengapa

Setelah satu baris kosong, jelaskan alasan dan hal yang tidak terlihat dari diff. Misalnya keputusan yang dipertimbangkan tetapi tidak diambil, atau efek samping yang perlu diketahui.

## Satu commit, satu maksud

Bila pesannya butuh kata "dan" untuk dua hal yang tidak berhubungan, pertimbangkan memecahnya menjadi dua commit. Riwayat yang rapi membuat `git bisect` dan `git revert` jauh lebih mudah.
