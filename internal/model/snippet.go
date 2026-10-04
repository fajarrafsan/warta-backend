package model

import (
	"strings"
	"unicode"
)

// Snippet mengambil potongan teks sekitar width karakter yang memuat term
// pertama kali, dipotong di batas kata. fragment adalah Markdown; hasilnya
// teks biasa. Kosong bila term tidak ditemukan.
func Snippet(fragment, term string, width int) string {
	text := []rune(strings.Join(strings.Fields(PlainText(fragment)), " "))
	if term == "" || len(text) == 0 {
		return ""
	}

	at := indexFold(text, []rune(term))
	if at < 0 {
		return ""
	}

	start := max(0, at-width/3)
	end := min(len(text), start+width)
	start = max(0, end-width)

	// Geser ke batas kata supaya tidak mulai atau berhenti di tengah kata.
	if start > 0 {
		for start < at && !unicode.IsSpace(text[start-1]) {
			start++
		}
	}
	if end < len(text) {
		for end > at+len(term) && !unicode.IsSpace(text[end]) {
			end--
		}
	}

	out := strings.TrimSpace(string(text[start:end]))
	if start > 0 {
		out = "…" + out
	}
	if end < len(text) {
		out = strings.TrimRight(out, " ,.;:-") + "…"
	}
	return out
}

// indexFold mencari needle di haystack tanpa membedakan huruf besar kecil.
func indexFold(haystack, needle []rune) int {
	if len(needle) == 0 || len(needle) > len(haystack) {
		return -1
	}
	for i := 0; i+len(needle) <= len(haystack); i++ {
		match := true
		for j, r := range needle {
			if unicode.ToLower(haystack[i+j]) != unicode.ToLower(r) {
				match = false
				break
			}
		}
		if match {
			return i
		}
	}
	return -1
}
