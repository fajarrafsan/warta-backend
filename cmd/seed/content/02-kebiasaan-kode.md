---
title: Lima Kebiasaan Kecil yang Membuat Kode Lebih Mudah Dirawat
author: dimas
category: Teknologi
tags: kode, praktik baik
status: published
days_ago: 4
cover: 1
---
Kode yang mudah dirawat jarang lahir dari arsitektur yang megah. Biasanya ia lahir dari kebiasaan kecil yang dilakukan terus-menerus.

## 1. Beri nama yang menjawab "untuk apa"

`data`, `temp`, dan `hasil2` menunda pertanyaan ke pembaca berikutnya. Nama seperti `expiredTokens` atau `publishedAt` menjawabnya sekarang.

## 2. Fungsi pendek dengan satu alasan berubah

Bila sebuah fungsi perlu diubah karena format tanggal *dan* karena aturan diskon, ia sedang mengerjakan dua hal. Pisahkan.

## 3. Hapus kode mati hari ini juga

Kode yang dikomentari "siapa tahu nanti dipakai" hampir tidak pernah dipakai, tetapi selalu dibaca. Riwayat git sudah menyimpannya.

## 4. Komentar untuk "mengapa", bukan "apa"

Kode sudah menjelaskan apa yang terjadi. Komentar yang berharga menjelaskan keputusan yang tidak terlihat, misalnya mengapa sebuah query sengaja tidak memakai indeks.

## 5. Tinggalkan sedikit lebih rapi

Setiap kali menyentuh sebuah berkas, rapikan satu hal kecil di dekatnya. Dalam setahun, perbedaannya terasa besar tanpa pernah ada "proyek refactoring".
