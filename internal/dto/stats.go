package dto

type StatsTotals struct {
	Articles  int64 `json:"articles"`
	Published int64 `json:"published"`
	Draft     int64 `json:"draft"`
	Scheduled int64 `json:"scheduled"`
	Archived  int64 `json:"archived"`
	Views     int64 `json:"views"`
	Likes     int64 `json:"likes"`
	Comments  int64 `json:"comments"`
	Bookmarks int64 `json:"bookmarks"`
	Followers int64 `json:"followers"`
}

type DailyStat struct {
	Date      string `json:"date"`
	Views     int64  `json:"views"`
	Comments  int64  `json:"comments"`
	Published int64  `json:"published"`
}

type TopArticle struct {
	ID       int64  `json:"id"`
	Title    string `json:"title"`
	Slug     string `json:"slug"`
	Views    int64  `json:"views"`
	Likes    int64  `json:"likes"`
	Comments int64  `json:"comments"`
}

type StatsResponse struct {
	// Scope "all" untuk admin (semua penulis), "mine" untuk author.
	Scope       string           `json:"scope"`
	Days        int              `json:"days"`
	Totals      StatsTotals      `json:"totals"`
	Users       map[string]int64 `json:"users,omitempty"`
	Daily       []DailyStat      `json:"daily"`
	TopArticles []TopArticle     `json:"top_articles"`
}
