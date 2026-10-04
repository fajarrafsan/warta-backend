package model

import (
	"strings"
	"testing"
)

func TestSnippet(t *testing.T) {
	long := strings.Repeat("kata pembuka ", 30) + "Di sini **Golang** dipakai untuk backend. " + strings.Repeat("kalimat penutup ", 30)

	got := Snippet(long, "golang", 80)
	if !strings.Contains(got, "Golang dipakai") || !strings.HasPrefix(got, "…") || !strings.HasSuffix(got, "…") {
		t.Fatalf("cuplikan: %q", got)
	}
	if n := len([]rune(got)); n > 82 {
		t.Fatalf("terlalu panjang: %d", n)
	}
	for _, word := range strings.Fields(strings.Trim(got, "…")) {
		if word != "kata" && word != "pembuka" && word != "kalimat" && word != "penutup" &&
			!strings.Contains("Di sini Golang dipakai untuk backend.", word) {
			t.Fatalf("terpotong di tengah kata: %q dalam %q", word, got)
		}
	}

	if got := Snippet("Golang di awal kalimat.", "GOLANG", 80); got != "Golang di awal kalimat." {
		t.Fatalf("teks pendek: %q", got)
	}
	if got := Snippet("tidak ada di sini", "rust", 80); got != "" {
		t.Fatalf("tidak ditemukan: %q", got)
	}
}
