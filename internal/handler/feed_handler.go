package handler

import (
	"encoding/xml"
	"net/http"
	"net/url"
	"strconv"
	"time"

	"warta/internal/model"
	"warta/internal/response"
	"warta/internal/service"
)

const (
	feedSize    = 20
	sitemapSize = 5000
	// feedCache membuat crawler dan pembaca RSS tidak memukul database di
	// setiap permintaan.
	feedCache = "public, max-age=900"
)

// FeedHandler menyajikan sitemap.xml dan feed.xml. Semua tautan menuju
// frontend (APP_URL), bukan ke API.
type FeedHandler struct {
	service service.FeedService
	appURL  string
}

func NewFeedHandler(s service.FeedService, appURL string) *FeedHandler {
	return &FeedHandler{service: s, appURL: appURL}
}

func (h *FeedHandler) articleURL(a model.Article) string {
	return h.appURL + "/artikel/" + url.PathEscape(a.Slug)
}

type sitemapURL struct {
	Loc     string `xml:"loc"`
	LastMod string `xml:"lastmod,omitempty"`
}

type sitemap struct {
	XMLName xml.Name     `xml:"urlset"`
	XMLNS   string       `xml:"xmlns,attr"`
	URLs    []sitemapURL `xml:"url"`
}

func (h *FeedHandler) Sitemap(w http.ResponseWriter, r *http.Request) {
	data, err := h.service.Sitemap(r.Context(), sitemapSize)
	articles := data.Articles
	if err != nil {
		response.Error(w, r, err)
		return
	}

	doc := sitemap{XMLNS: "http://www.sitemaps.org/schemas/sitemap/0.9"}
	home := sitemapURL{Loc: h.appURL + "/"}
	if len(articles) > 0 {
		home.LastMod = articles[0].UpdatedAt.UTC().Format(time.DateOnly)
	}
	doc.URLs = append(doc.URLs, home)
	for _, c := range data.Categories {
		doc.URLs = append(doc.URLs, sitemapURL{Loc: h.appURL + "/kategori/" + url.PathEscape(c.Slug)})
	}
	for _, a := range data.Authors {
		doc.URLs = append(doc.URLs, sitemapURL{Loc: h.appURL + "/penulis/" + strconv.FormatInt(a.ID, 10)})
	}
	for _, a := range articles {
		doc.URLs = append(doc.URLs, sitemapURL{Loc: h.articleURL(a), LastMod: a.UpdatedAt.UTC().Format(time.DateOnly)})
	}

	writeXML(w, "application/xml; charset=utf-8", doc)
}

type rssItem struct {
	Title       string   `xml:"title"`
	Link        string   `xml:"link"`
	GUID        rssGUID  `xml:"guid"`
	Description string   `xml:"description"`
	Author      string   `xml:"dc:creator"`
	Categories  []string `xml:"category"`
	PubDate     string   `xml:"pubDate"`
}

type rssGUID struct {
	Value       string `xml:",chardata"`
	IsPermaLink bool   `xml:"isPermaLink,attr"`
}

type rssLink struct {
	Href string `xml:"href,attr"`
	Rel  string `xml:"rel,attr"`
	Type string `xml:"type,attr"`
}

type rssChannel struct {
	Title         string    `xml:"title"`
	Link          string    `xml:"link"`
	Description   string    `xml:"description"`
	Language      string    `xml:"language"`
	Self          rssLink   `xml:"atom:link"`
	LastBuildDate string    `xml:"lastBuildDate,omitempty"`
	Items         []rssItem `xml:"item"`
}

type rss struct {
	XMLName xml.Name   `xml:"rss"`
	Version string     `xml:"version,attr"`
	Atom    string     `xml:"xmlns:atom,attr"`
	DC      string     `xml:"xmlns:dc,attr"`
	Channel rssChannel `xml:"channel"`
}

func (h *FeedHandler) RSS(w http.ResponseWriter, r *http.Request) {
	articles, err := h.service.Latest(r.Context(), feedSize)
	if err != nil {
		response.Error(w, r, err)
		return
	}

	channel := rssChannel{
		Title:       "Warta",
		Link:        h.appURL + "/",
		Description: "Tulisan terbaru di Warta",
		Language:    "id",
		Self:        rssLink{Href: h.appURL + "/feed.xml", Rel: "self", Type: "application/rss+xml"},
	}
	for _, a := range articles {
		published := a.CreatedAt
		if a.PublishedAt != nil {
			published = *a.PublishedAt
		}
		if channel.LastBuildDate == "" {
			channel.LastBuildDate = published.UTC().Format(time.RFC1123Z)
		}

		link := h.articleURL(a)
		item := rssItem{
			Title:       a.Title,
			Link:        link,
			GUID:        rssGUID{Value: link, IsPermaLink: true},
			Description: a.Excerpt,
			Author:      a.AuthorName,
			PubDate:     published.UTC().Format(time.RFC1123Z),
		}
		if a.CategoryName != "" {
			item.Categories = append(item.Categories, a.CategoryName)
		}
		item.Categories = append(item.Categories, a.TagNames()...)
		channel.Items = append(channel.Items, item)
	}

	writeXML(w, "application/rss+xml; charset=utf-8", rss{
		Version: "2.0",
		Atom:    "http://www.w3.org/2005/Atom",
		DC:      "http://purl.org/dc/elements/1.1/",
		Channel: channel,
	})
}

func writeXML(w http.ResponseWriter, contentType string, v any) {
	body, err := xml.MarshalIndent(v, "", "  ")
	if err != nil {
		http.Error(w, "gagal membuat XML", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", contentType)
	w.Header().Set("Cache-Control", feedCache)
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte(xml.Header))
	_, _ = w.Write(body)
}
