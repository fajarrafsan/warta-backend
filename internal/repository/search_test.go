package repository

import (
	"reflect"
	"strings"
	"testing"
)

func TestSearchTerms(t *testing.T) {
	got := SearchTerms(`  REST-API "golang" +go -drop* (mysql) mysql @x ~y <z> `)
	want := []string{"rest", "api", "golang", "go", "drop", "mysql", "x", "y"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("SearchTerms = %v, ingin %v", got, want)
	}
	if len(SearchTerms(strings.Repeat("kata ", 3)+"a b c d e f g h i j")) != maxSearchTerms {
		t.Fatal("jumlah kata seharusnya dibatasi")
	}
	if len(SearchTerms("+-*\"()")) != 0 {
		t.Fatal("hanya operator berarti kosong")
	}
}

func TestSearchSplitsShortTerms(t *testing.T) {
	s := newSearch("Go REST api")
	if s.fulltext != "+rest* +api*" || !reflect.DeepEqual(s.short, []string{"go"}) {
		t.Fatalf("fulltext %q, short %v", s.fulltext, s.short)
	}
	conditions, args := s.conditions()
	if len(conditions) != 2 || len(args) != 3 {
		t.Fatalf("conditions %v args %v", conditions, args)
	}
	if s.snippetTerm() != "rest" {
		t.Fatalf("snippetTerm %q", s.snippetTerm())
	}

	if score, _ := newSearch("go ai").score(); score != "" {
		t.Fatal("tanpa kata panjang tidak ada skor relevansi")
	}
}
