package dto

type ArticleSuggestion struct {
	ID       int64  `json:"id"`
	Slug     string `json:"slug"`
	Title    string `json:"title"`
	Category string `json:"category"`
}

type AuthorSuggestion struct {
	ID        int64   `json:"id"`
	Name      string  `json:"name"`
	AvatarURL *string `json:"avatar_url"`
}

// Suggestions adalah saran pencarian; setiap daftar selalu berupa array.
type Suggestions struct {
	Articles   []ArticleSuggestion `json:"articles"`
	Categories []CategoryRef       `json:"categories"`
	Tags       []TagRef            `json:"tags"`
	Authors    []AuthorSuggestion  `json:"authors"`
}
