package repository

import (
	"strings"
	"unicode"
	"unicode/utf8"
)

const (
	// minIndexedTerm sama dengan innodb_ft_min_token_size bawaan MySQL. Kata
	// yang lebih pendek tidak masuk indeks FULLTEXT, jadi dicari dengan LIKE.
	minIndexedTerm = 3
	maxSearchTerms = 8
	maxTermRunes   = 40
)

// SearchTerms memecah kata kunci menjadi kata huruf kecil yang unik. Semua
// selain huruf dan angka dibuang, termasuk operator mode boolean MySQL
// (+ - * " ( ) < > ~ @), sehingga masukan pengguna tidak bisa mengubah arti
// query.
func SearchTerms(query string) []string {
	fields := strings.FieldsFunc(strings.ToLower(query), func(r rune) bool {
		return !unicode.IsLetter(r) && !unicode.IsDigit(r)
	})

	seen := make(map[string]bool, len(fields))
	terms := make([]string, 0, len(fields))
	for _, f := range fields {
		if utf8.RuneCountInString(f) > maxTermRunes {
			f = string([]rune(f)[:maxTermRunes])
		}
		if seen[f] {
			continue
		}
		seen[f] = true
		terms = append(terms, f)
		if len(terms) == maxSearchTerms {
			break
		}
	}
	return terms
}

// search adalah kata kunci yang sudah dipilah: kata panjang lewat FULLTEXT,
// kata pendek lewat LIKE. Semua kata wajib ada (AND).
type search struct {
	terms    []string
	fulltext string
	short    []string
}

func newSearch(query string) search {
	s := search{terms: SearchTerms(query)}
	var parts []string
	for _, t := range s.terms {
		if utf8.RuneCountInString(t) >= minIndexedTerm {
			// Awalan: "golan" juga menemukan "golang".
			parts = append(parts, "+"+t+"*")
		} else {
			s.short = append(s.short, t)
		}
	}
	s.fulltext = strings.Join(parts, " ")
	return s
}

func (s search) empty() bool {
	return len(s.terms) == 0
}

func (s search) conditions() ([]string, []any) {
	var conditions []string
	var args []any
	if s.fulltext != "" {
		conditions = append(conditions, "MATCH(a.title, a.content) AGAINST(? IN BOOLEAN MODE)")
		args = append(args, s.fulltext)
	}
	for _, t := range s.short {
		pattern := "%" + escapeLike(t) + "%"
		conditions = append(conditions, "(a.title LIKE ? OR a.content LIKE ?)")
		args = append(args, pattern, pattern)
	}
	return conditions, args
}

// score memberi bobot tiga kali lipat untuk kecocokan di judul. Kosong bila
// semua kata pendek (tidak ada skor FULLTEXT).
func (s search) score() (string, []any) {
	if s.fulltext == "" {
		return "", nil
	}
	return "(3 * MATCH(a.title) AGAINST(? IN BOOLEAN MODE) + MATCH(a.title, a.content) AGAINST(? IN BOOLEAN MODE))",
		[]any{s.fulltext, s.fulltext}
}

// snippetTerm adalah kata terpanjang, yang paling khas untuk dicari letaknya
// di isi artikel.
func (s search) snippetTerm() string {
	best := ""
	for _, t := range s.terms {
		if utf8.RuneCountInString(t) > utf8.RuneCountInString(best) {
			best = t
		}
	}
	return best
}
