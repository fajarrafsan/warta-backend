package validation

import (
	"fmt"
	"regexp"

	"warta/internal/dto"
	"warta/internal/model"
)

// coverPattern sama dengan nama berkas buatan storage.Local.
var coverPattern = regexp.MustCompile(`^/uploads/[a-f0-9]{32}\.(jpg|png|webp|gif)$`)

// linkPattern mengenali tautan di komentar. Komentar spam hampir selalu
// membawa banyak tautan.
var linkPattern = regexp.MustCompile(`(?i)(https?://|www\.)`)

const maxCommentLinks = 2

const (
	maxTags        = 10
	maxContentLen  = 100_000
	maxCommentLen  = 2_000
	maxDescription = 255
)

func ValidateArticle(r dto.ArticleRequest) map[string]string {
	problems := make(map[string]string)

	switch n := length(r.Title); {
	case n == 0:
		problems["title"] = "title wajib diisi"
	case n < 20:
		problems["title"] = "title minimal 20 karakter"
	case n > 200:
		problems["title"] = "title maksimal 200 karakter"
	}

	switch n := length(r.Content); {
	case n == 0:
		problems["content"] = "content wajib diisi"
	case n < 200:
		problems["content"] = "content minimal 200 karakter"
	case n > maxContentLen:
		problems["content"] = fmt.Sprintf("content maksimal %d karakter", maxContentLen)
	}

	if r.CategoryID <= 0 {
		problems["category_id"] = "category_id wajib diisi"
	}

	if len(r.Tags) > maxTags {
		problems["tags"] = fmt.Sprintf("tags maksimal %d", maxTags)
	}
	for _, t := range r.Tags {
		if msg := tagProblem(t); msg != "" {
			problems["tags"] = msg
			break
		}
	}

	if r.Status == string(model.StatusScheduled) && r.ScheduledAt == nil {
		problems["scheduled_at"] = "scheduled_at wajib diisi untuk artikel terjadwal"
	}

	if r.CoverImage != "" && !coverPattern.MatchString(r.CoverImage) {
		problems["cover_image"] = "cover_image harus berupa path hasil upload"
	}

	switch {
	case r.Status == "":
		problems["status"] = "status wajib diisi"
	case !model.ArticleStatus(r.Status).Valid():
		problems["status"] = "status harus draft, scheduled, published, atau archived"
	}

	return problems
}

func ValidateCategory(r dto.CategoryRequest) map[string]string {
	problems := make(map[string]string)

	switch n := length(r.Name); {
	case n == 0:
		problems["name"] = "name wajib diisi"
	case n < 3:
		problems["name"] = "name minimal 3 karakter"
	case n > 100:
		problems["name"] = "name maksimal 100 karakter"
	}

	if length(r.Description) > maxDescription {
		problems["description"] = fmt.Sprintf("description maksimal %d karakter", maxDescription)
	}

	return problems
}

func ValidateTag(r dto.TagRequest) map[string]string {
	problems := make(map[string]string)
	if msg := tagProblem(r.Name); msg != "" {
		problems["name"] = msg
	}
	return problems
}

func ValidateComment(r dto.CommentRequest) map[string]string {
	problems := make(map[string]string)

	switch n := length(r.Body); {
	case n == 0:
		problems["body"] = "body wajib diisi"
	case n > maxCommentLen:
		problems["body"] = fmt.Sprintf("body maksimal %d karakter", maxCommentLen)
	case len(linkPattern.FindAllStringIndex(r.Body, -1)) > maxCommentLinks:
		problems["body"] = fmt.Sprintf("komentar paling banyak berisi %d tautan", maxCommentLinks)
	case shouting(r.Body):
		problems["body"] = "komentar tidak boleh ditulis seluruhnya dengan huruf besar"
	}

	return problems
}

// shouting menandai komentar panjang yang seluruh hurufnya kapital.
func shouting(body string) bool {
	letters, upper := 0, 0
	for _, r := range body {
		if r >= 'a' && r <= 'z' {
			letters++
		} else if r >= 'A' && r <= 'Z' {
			letters++
			upper++
		}
	}
	return letters >= 20 && upper == letters
}

func ValidateReport(r dto.ReportRequest) map[string]string {
	problems := make(map[string]string)
	if !model.ValidReportReason(r.Reason) {
		problems["reason"] = "reason harus spam, abusive, atau other"
	}
	return problems
}

func ValidateModeration(r dto.ModerationRequest) map[string]string {
	problems := make(map[string]string)
	if r.Action != "approve" && r.Action != "hide" {
		problems["action"] = "action harus approve atau hide"
	}
	return problems
}

func tagProblem(name string) string {
	switch n := length(name); {
	case n == 0:
		return "tag tidak boleh kosong"
	case n < 2:
		return "tag minimal 2 karakter"
	case n > 50:
		return "tag maksimal 50 karakter"
	}
	return ""
}

func length(s string) int {
	return len([]rune(s))
}
