package app_test

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"image"
	"image/png"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"

	"golang.org/x/crypto/bcrypt"

	"warta/internal/app"
	"warta/internal/auth"
	"warta/internal/config"
	"warta/internal/database"
	"warta/internal/mail"
)

// Test di file ini butuh MySQL sungguhan dan dilewati bila TEST_DB_HOST
// kosong. Contoh:
//
//	TEST_DB_HOST=127.0.0.1 TEST_DB_PASSWORD=root go test ./internal/app/
//
// Setiap test membuat database sendiri dengan nama acak lalu menghapusnya.

func testConfig(t *testing.T) config.Config {
	t.Helper()

	host := os.Getenv("TEST_DB_HOST")
	if host == "" {
		t.Skip("TEST_DB_HOST kosong, test integrasi dilewati")
	}

	cfg := config.Config{
		DefaultPerPage:       10,
		MaxPerPage:           50,
		CORSOrigins:          []string{"*"},
		JWTSecret:            "rahasia-test-yang-panjangnya-lebih-dari-32",
		JWTIssuer:            "warta-test",
		AccessTokenTTL:       time.Minute,
		RefreshTokenTTL:      time.Hour,
		AuthRateLimit:        1000,
		CommentRateLimit:     1000,
		UploadRateLimit:      1000,
		CommentHideThreshold: 2,
		UploadDir:            t.TempDir(),
		MaxUploadBytes:       1 << 20,
		AppURL:               "https://warta.test",
		DBHost:               host,
		DBPort:               envOr("TEST_DB_PORT", "3306"),
		DBUser:               envOr("TEST_DB_USER", "root"),
		DBPassword:           os.Getenv("TEST_DB_PASSWORD"),
		DBName:               fmt.Sprintf("warta_test_%d", time.Now().UnixNano()),
	}

	ctx := context.Background()
	if err := database.CreateDatabase(ctx, cfg.ServerDSN(), cfg.DBName); err != nil {
		t.Fatalf("membuat database: %v", err)
	}
	t.Cleanup(func() {
		db, err := sql.Open("mysql", cfg.ServerDSN())
		if err == nil {
			_, _ = db.Exec("DROP DATABASE IF EXISTS `" + cfg.DBName + "`")
			db.Close()
		}
	})

	return cfg
}

func envOr(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

func connect(t *testing.T, cfg config.Config) *sql.DB {
	t.Helper()
	db, err := database.Connect(context.Background(), cfg.DSN())
	if err != nil {
		t.Fatalf("koneksi database: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	return db
}

func migrate(t *testing.T, cfg config.Config, target uint) {
	t.Helper()
	if err := database.MigrateTo(cfg.MigrationDSN(), cfg.DBName, target); err != nil {
		t.Fatalf("migrasi ke versi %d: %v", target, err)
	}
}

const latestVersion = 15

type client struct {
	t    *testing.T
	base string
}

type result struct {
	t      *testing.T
	status int
	header http.Header
	body   []byte
}

// upload mengirim berkas sebagai multipart/form-data di field image.
func (c client) upload(token string, data []byte) result {
	c.t.Helper()

	var body bytes.Buffer
	form := multipart.NewWriter(&body)
	part, err := form.CreateFormFile("image", "sampul.png")
	if err != nil {
		c.t.Fatal(err)
	}
	_, _ = part.Write(data)
	form.Close()

	req, err := http.NewRequest("POST", c.base+"/api/v1/uploads", &body)
	if err != nil {
		c.t.Fatal(err)
	}
	req.Header.Set("Content-Type", form.FormDataContentType())
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	return c.send(req)
}

// doFrom sama dengan do, tapi seolah-olah lewat reverse proxy yang
// meneruskan IP pengunjung di X-Forwarded-For.
func (c client) doFrom(ip, method, path, token string, body any) result {
	c.t.Helper()

	var reader io.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		reader = bytes.NewReader(b)
	}
	req, err := http.NewRequest(method, c.base+path, reader)
	if err != nil {
		c.t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Forwarded-For", ip)
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	return c.send(req)
}

func (c client) do(method, path, token string, body any) result {
	c.t.Helper()

	var reader io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			c.t.Fatal(err)
		}
		reader = bytes.NewReader(b)
	}

	req, err := http.NewRequest(method, c.base+path, reader)
	if err != nil {
		c.t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/json")
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	return c.send(req)
}

func (c client) send(req *http.Request) result {
	c.t.Helper()

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		c.t.Fatal(err)
	}
	defer resp.Body.Close()

	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		c.t.Fatal(err)
	}
	return result{t: c.t, status: resp.StatusCode, header: resp.Header, body: raw}
}

// expect memastikan status lalu mengisi dst dari field data (bila dst tidak nil).
func (r result) expect(status int, dst any) result {
	r.t.Helper()
	if r.status != status {
		r.t.Fatalf("status %d, ingin %d, body: %s", r.status, status, r.body)
	}
	if dst != nil {
		envelope := struct{ Data json.RawMessage }{}
		if err := json.Unmarshal(r.body, &envelope); err != nil {
			r.t.Fatalf("body bukan JSON: %s", r.body)
		}
		if err := json.Unmarshal(envelope.Data, dst); err != nil {
			r.t.Fatalf("data tidak cocok: %v, body: %s", err, r.body)
		}
	}
	return r
}

func (r result) meta() meta {
	r.t.Helper()
	var envelope struct{ Meta meta }
	if err := json.Unmarshal(r.body, &envelope); err != nil {
		r.t.Fatalf("body bukan JSON: %s", r.body)
	}
	return envelope.Meta
}

// expectError memastikan status dan kode error, lalu mengembalikan fields.
func (r result) expectError(status int, code string) map[string]string {
	r.t.Helper()
	if r.status != status {
		r.t.Fatalf("status %d, ingin %d, body: %s", r.status, status, r.body)
	}
	var envelope struct {
		Error struct {
			Code   string
			Fields map[string]string
		}
	}
	if err := json.Unmarshal(r.body, &envelope); err != nil {
		r.t.Fatalf("body bukan JSON: %s", r.body)
	}
	if envelope.Error.Code != code {
		r.t.Fatalf("kode error %q, ingin %q, body: %s", envelope.Error.Code, code, r.body)
	}
	return envelope.Error.Fields
}

type tokens struct {
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token"`
	User         user   `json:"user"`
}

type user struct {
	ID            int64  `json:"id"`
	Name          string `json:"name"`
	Role          string `json:"role"`
	EmailVerified bool   `json:"email_verified"`
}

// outbox menampung email yang "terkirim" selama test.
type outbox struct {
	mu   sync.Mutex
	sent []mail.Message
}

func (o *outbox) Send(_ context.Context, m mail.Message) error {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.sent = append(o.sent, m)
	return nil
}

var tokenInLink = regexp.MustCompile(`https://warta\.test(/[a-z-]+)\?token=(\S+)`)

// lastLink mengembalikan path dan token dari tautan di email terakhir ke to.
func (o *outbox) lastLink(t *testing.T, to string) (path, token string) {
	t.Helper()
	o.mu.Lock()
	defer o.mu.Unlock()
	for i := len(o.sent) - 1; i >= 0; i-- {
		if o.sent[i].To != to {
			continue
		}
		m := tokenInLink.FindStringSubmatch(o.sent[i].Text)
		if m == nil {
			t.Fatalf("email tanpa tautan: %s", o.sent[i].Text)
		}
		token, _ = url.QueryUnescape(m[2])
		return m[1], token
	}
	t.Fatalf("tidak ada email untuk %s", to)
	return "", ""
}

func (o *outbox) count(to string) int {
	o.mu.Lock()
	defer o.mu.Unlock()
	n := 0
	for _, m := range o.sent {
		if m.To == to {
			n++
		}
	}
	return n
}

type category struct {
	ID           int64  `json:"id"`
	Slug         string `json:"slug"`
	ArticleCount int    `json:"article_count"`
}

type tag struct {
	Name         string `json:"name"`
	Slug         string `json:"slug"`
	ArticleCount int    `json:"article_count"`
}

type article struct {
	ID           int64      `json:"id"`
	Title        string     `json:"title"`
	Slug         string     `json:"slug"`
	Excerpt      string     `json:"excerpt"`
	Content      string     `json:"content"`
	Status       string     `json:"status"`
	Author       user       `json:"author"`
	Category     category   `json:"category"`
	Tags         []tag      `json:"tags"`
	CommentCount int        `json:"comment_count"`
	PublishedAt  *time.Time `json:"published_at"`
	CoverImage   *string    `json:"cover_image"`
	LikeCount    int        `json:"like_count"`
	ViewCount    int        `json:"view_count"`
	Reading      int        `json:"reading_minutes"`
	Liked        bool       `json:"liked"`
	Bookmarked   bool       `json:"bookmarked"`
}

type engagement struct {
	Liked      bool `json:"liked"`
	Bookmarked bool `json:"bookmarked"`
	LikeCount  int  `json:"like_count"`
}

type stats struct {
	Scope  string `json:"scope"`
	Totals struct {
		Published int64 `json:"published"`
		Views     int64 `json:"views"`
		Likes     int64 `json:"likes"`
		Bookmarks int64 `json:"bookmarks"`
	} `json:"totals"`
	Users map[string]int64 `json:"users"`
	Daily []struct {
		Date  string `json:"date"`
		Views int64  `json:"views"`
	} `json:"daily"`
	TopArticles []struct {
		ID int64 `json:"id"`
	} `json:"top_articles"`
}

type comment struct {
	ID     int64  `json:"id"`
	Body   string `json:"body"`
	Author user   `json:"author"`
}

type meta struct {
	Page       int   `json:"page"`
	PerPage    int   `json:"per_page"`
	Total      int64 `json:"total"`
	TotalPages int   `json:"total_pages"`
}

var content = strings.TrimSpace(strings.Repeat("Warta menulis kabar dengan rapi dan jelas. ", 8))

func TestAPI(t *testing.T) {
	cfg := testConfig(t)

	// Turun lalu naik lagi supaya migrasi down ikut teruji.
	migrate(t, cfg, latestVersion)
	if err := database.MigrateDown(cfg.MigrationDSN(), cfg.DBName); err != nil {
		t.Fatalf("migrasi down: %v", err)
	}
	migrate(t, cfg, latestVersion)

	db := connect(t, cfg)
	a, err := app.New(cfg, db, auth.BcryptHasher{Cost: bcrypt.MinCost}, &outbox{})
	if err != nil {
		t.Fatal(err)
	}
	if err := a.Auth.EnsureAdmin(context.Background(), "Admin", "admin@warta.test", "admin12345"); err != nil {
		t.Fatalf("membuat admin: %v", err)
	}

	server := httptest.NewServer(a.Handler)
	defer server.Close()
	c := client{t: t, base: server.URL}

	var admin, writer, reader tokens

	c.do("POST", "/api/v1/auth/login", "", map[string]string{
		"email": "admin@warta.test", "password": "admin12345",
	}).expect(http.StatusOK, &admin)

	t.Run("register dan login", func(t *testing.T) {
		c := client{t: t, base: server.URL}

		c.do("POST", "/api/v1/auth/register", "", map[string]string{
			"name": "  Penulis   Satu ", "email": "Penulis@Warta.test", "password": "rahasia123",
		}).expect(http.StatusCreated, &writer)
		if writer.User.Role != "reader" || writer.User.Name != "Penulis Satu" {
			t.Fatalf("user baru: %+v", writer.User)
		}

		fields := c.do("POST", "/api/v1/auth/register", "", map[string]string{
			"name": "Kembar", "email": "penulis@warta.test", "password": "rahasia123",
		}).expectError(http.StatusUnprocessableEntity, "validation_failed")
		if fields["email"] == "" {
			t.Fatal("email ganda seharusnya ditolak")
		}

		fields = c.do("POST", "/api/v1/auth/register", "", map[string]string{
			"name": "X", "email": "bukan-email", "password": "pendek",
		}).expectError(http.StatusUnprocessableEntity, "validation_failed")
		for _, f := range []string{"name", "email", "password"} {
			if fields[f] == "" {
				t.Fatalf("field %s seharusnya gagal validasi: %v", f, fields)
			}
		}

		c.do("POST", "/api/v1/auth/login", "", map[string]string{
			"email": "penulis@warta.test", "password": "salah-password",
		}).expectError(http.StatusUnauthorized, "unauthorized")
		c.do("POST", "/api/v1/auth/login", "", map[string]string{
			"email": "tidak-ada@warta.test", "password": "rahasia123",
		}).expectError(http.StatusUnauthorized, "unauthorized")

		c.do("POST", "/api/v1/auth/register", "", map[string]string{
			"name": "Pembaca", "email": "pembaca@warta.test", "password": "rahasia123",
		}).expect(http.StatusCreated, &reader)
	})

	t.Run("role dan refresh token", func(t *testing.T) {
		c := client{t: t, base: server.URL}

		c.do("POST", "/api/v1/articles", writer.AccessToken, map[string]any{}).
			expectError(http.StatusForbidden, "forbidden")
		c.do("GET", "/api/v1/users", writer.AccessToken, nil).
			expectError(http.StatusForbidden, "forbidden")

		var promoted user
		c.do("PATCH", fmt.Sprintf("/api/v1/users/%d/role", writer.User.ID), admin.AccessToken,
			map[string]string{"role": "author"}).expect(http.StatusOK, &promoted)
		if promoted.Role != "author" {
			t.Fatalf("role: %s", promoted.Role)
		}

		c.do("PATCH", fmt.Sprintf("/api/v1/users/%d/role", admin.User.ID), admin.AccessToken,
			map[string]string{"role": "reader"}).expectError(http.StatusForbidden, "forbidden")

		// Access token lama masih membawa role reader. Refresh menerbitkan
		// token dengan role terbaru.
		old := writer
		c.do("POST", "/api/v1/auth/refresh", "", map[string]string{"refresh_token": old.RefreshToken}).
			expect(http.StatusOK, &writer)
		if writer.User.Role != "author" || writer.RefreshToken == old.RefreshToken {
			t.Fatalf("refresh: %+v", writer.User)
		}

		// Memakai ulang refresh token yang sudah dirotasi mencabut semua sesi.
		c.do("POST", "/api/v1/auth/refresh", "", map[string]string{"refresh_token": old.RefreshToken}).
			expectError(http.StatusUnauthorized, "unauthorized")
		c.do("POST", "/api/v1/auth/refresh", "", map[string]string{"refresh_token": writer.RefreshToken}).
			expectError(http.StatusUnauthorized, "unauthorized")

		c.do("POST", "/api/v1/auth/login", "", map[string]string{
			"email": "penulis@warta.test", "password": "rahasia123",
		}).expect(http.StatusOK, &writer)

		var users []user
		res := c.do("GET", "/api/v1/users?role=reader", admin.AccessToken, nil).expect(http.StatusOK, &users)
		if len(users) != 1 || res.meta().Total != 1 {
			t.Fatalf("filter role reader: %+v", users)
		}

		c.do("GET", "/api/v1/me", "token-asal", nil).expectError(http.StatusUnauthorized, "unauthorized")
		c.do("GET", "/api/v1/me", "", nil).expectError(http.StatusUnauthorized, "unauthorized")
	})

	var tech, life category

	t.Run("kategori", func(t *testing.T) {
		c := client{t: t, base: server.URL}

		c.do("POST", "/api/v1/categories", writer.AccessToken, map[string]string{"name": "Teknologi"}).
			expectError(http.StatusForbidden, "forbidden")

		c.do("POST", "/api/v1/categories", admin.AccessToken, map[string]string{
			"name": "Teknologi", "description": "Seputar dunia teknologi",
		}).expect(http.StatusCreated, &tech)
		if tech.Slug != "teknologi" {
			t.Fatalf("slug kategori: %s", tech.Slug)
		}

		c.do("POST", "/api/v1/categories", admin.AccessToken, map[string]string{"name": "teknologi"}).
			expectError(http.StatusUnprocessableEntity, "validation_failed")

		c.do("POST", "/api/v1/categories", admin.AccessToken, map[string]string{"name": "Gaya Hidup"}).
			expect(http.StatusCreated, &life)

		var bySlug category
		c.do("GET", "/api/v1/categories/gaya-hidup", "", nil).expect(http.StatusOK, &bySlug)
		if bySlug.ID != life.ID {
			t.Fatalf("kategori berdasarkan slug: %+v", bySlug)
		}

		var renamed category
		c.do("PUT", fmt.Sprintf("/api/v1/categories/%d", life.ID), admin.AccessToken,
			map[string]string{"name": "Gaya Hidup Sehat"}).expect(http.StatusOK, &renamed)
		if renamed.Slug != "gaya-hidup-sehat" {
			t.Fatalf("slug setelah ganti nama: %s", renamed.Slug)
		}
	})

	var draft, published article

	t.Run("menulis article", func(t *testing.T) {
		c := client{t: t, base: server.URL}

		fields := c.do("POST", "/api/v1/articles", writer.AccessToken, map[string]any{
			"title": "Pendek", "content": "sedikit", "category_id": 0, "status": "terbit",
		}).expectError(http.StatusUnprocessableEntity, "validation_failed")
		for _, f := range []string{"title", "content", "category_id", "status"} {
			if fields[f] == "" {
				t.Fatalf("field %s seharusnya gagal validasi: %v", f, fields)
			}
		}

		fields = c.do("POST", "/api/v1/articles", writer.AccessToken, map[string]any{
			"title": "Kategori yang tidak pernah dibuat", "content": content, "category_id": 9999, "status": "draft",
		}).expectError(http.StatusUnprocessableEntity, "validation_failed")
		if fields["category_id"] == "" {
			t.Fatalf("kategori tidak ada seharusnya ditolak: %v", fields)
		}

		res := c.do("POST", "/api/v1/articles", writer.AccessToken, map[string]any{
			"title":       "Belajar Go dari Nol Sampai Mahir",
			"content":     content,
			"category_id": tech.ID,
			"tags":        []string{"Go", " backend ", "go"},
			"status":      "draft",
		}).expect(http.StatusCreated, &draft)
		if res.header.Get("Location") != fmt.Sprintf("/api/v1/articles/%d", draft.ID) {
			t.Fatalf("Location: %s", res.header.Get("Location"))
		}
		if draft.Slug != "belajar-go-dari-nol-sampai-mahir" || draft.PublishedAt != nil {
			t.Fatalf("draft: %+v", draft)
		}
		if len(draft.Tags) != 2 || draft.Tags[0].Name != "backend" || draft.Tags[1].Name != "go" {
			t.Fatalf("tag seharusnya dirapikan dan tidak ganda: %+v", draft.Tags)
		}
		if draft.Author.ID != writer.User.ID || draft.Category.Slug != "teknologi" {
			t.Fatalf("relasi article: %+v", draft)
		}

		// Draft tidak terlihat oleh orang lain.
		path := fmt.Sprintf("/api/v1/articles/%d", draft.ID)
		c.do("GET", path, "", nil).expectError(http.StatusNotFound, "not_found")
		c.do("GET", path, reader.AccessToken, nil).expectError(http.StatusNotFound, "not_found")
		c.do("GET", path, writer.AccessToken, nil).expect(http.StatusOK, nil)
		c.do("GET", path, admin.AccessToken, nil).expect(http.StatusOK, nil)

		// Selama belum terbit, slug mengikuti judul.
		c.do("PATCH", path, writer.AccessToken, map[string]any{"title": "Belajar Golang dari Nol Sampai Mahir"}).
			expect(http.StatusOK, &draft)
		if draft.Slug != "belajar-golang-dari-nol-sampai-mahir" || len(draft.Tags) != 2 {
			t.Fatalf("setelah ganti judul: %+v", draft)
		}

		c.do("PATCH", path, writer.AccessToken, map[string]any{"status": "published"}).
			expect(http.StatusOK, &published)
		if published.PublishedAt == nil {
			t.Fatal("published_at seharusnya terisi")
		}

		// Setelah terbit, slug tidak berubah lagi.
		c.do("PATCH", path, writer.AccessToken, map[string]any{"title": "Judul Baru Setelah Artikel Terbit", "tags": []string{"go"}}).
			expect(http.StatusOK, &published)
		if published.Slug != "belajar-golang-dari-nol-sampai-mahir" || len(published.Tags) != 1 {
			t.Fatalf("setelah terbit: %+v", published)
		}

		var bySlug article
		c.do("GET", "/api/v1/articles/"+published.Slug, "", nil).expect(http.StatusOK, &bySlug)
		if bySlug.ID != published.ID || bySlug.Content != content {
			t.Fatalf("berdasarkan slug: %+v", bySlug)
		}

		// PUT wajib lengkap.
		c.do("PUT", path, writer.AccessToken, map[string]any{"title": "Judul Saja Tanpa Field Lain"}).
			expectError(http.StatusUnprocessableEntity, "validation_failed")

		c.do("PATCH", path, reader.AccessToken, map[string]any{"status": "draft"}).
			expectError(http.StatusForbidden, "forbidden")

		// Judul yang sama menghasilkan slug berakhiran -2.
		var twin article
		c.do("POST", "/api/v1/articles", writer.AccessToken, map[string]any{
			"title": "Judul Baru Setelah Artikel Terbit", "content": content, "category_id": life.ID, "status": "published",
		}).expect(http.StatusCreated, &twin)
		if twin.Slug != "judul-baru-setelah-artikel-terbit" {
			t.Fatalf("slug twin: %s", twin.Slug)
		}
		var triplet article
		c.do("POST", "/api/v1/articles", admin.AccessToken, map[string]any{
			"title": "Judul Baru Setelah Artikel Terbit", "content": content, "category_id": life.ID, "status": "draft",
		}).expect(http.StatusCreated, &triplet)
		if triplet.Slug != "judul-baru-setelah-artikel-terbit-2" {
			t.Fatalf("slug triplet: %s", triplet.Slug)
		}
		draft = triplet
	})

	t.Run("daftar, filter, dan paging", func(t *testing.T) {
		c := client{t: t, base: server.URL}

		var list []article
		res := c.do("GET", "/api/v1/articles", "", nil).expect(http.StatusOK, &list)
		if len(list) != 2 || res.meta().Total != 2 {
			t.Fatalf("hanya article terbit yang tampil: %d", len(list))
		}
		for _, a := range list {
			if a.Content != "" || a.Excerpt == "" {
				t.Fatalf("daftar memuat cuplikan, bukan isi lengkap: %+v", a)
			}
		}

		res = c.do("GET", "/api/v1/articles?per_page=1&page=2&sort=title", "", nil).expect(http.StatusOK, &list)
		if m := res.meta(); m.Total != 2 || m.TotalPages != 2 || m.Page != 2 || len(list) != 1 {
			t.Fatalf("paging: %+v", m)
		}

		c.do("GET", "/api/v1/articles?category=teknologi&tag=go", "", nil).expect(http.StatusOK, &list)
		if len(list) != 1 || list[0].ID != published.ID {
			t.Fatalf("filter kategori dan tag: %+v", list)
		}

		c.do("GET", "/api/v1/articles?q=golang", "", nil).expect(http.StatusOK, &list)
		if len(list) != 0 {
			t.Fatalf("judul lama tidak lagi cocok: %+v", list)
		}
		c.do("GET", "/api/v1/articles?q=RAPI%20DAN", "", nil).expect(http.StatusOK, &list)
		if len(list) != 2 {
			t.Fatalf("pencarian isi tidak peka huruf besar: %d", len(list))
		}
		c.do("GET", "/api/v1/articles?q=100%25", "", nil).expect(http.StatusOK, &list)
		if len(list) != 0 {
			t.Fatalf("%% harus dicari sebagai karakter biasa: %d", len(list))
		}

		c.do("GET", "/api/v1/articles?status=draft", writer.AccessToken, nil).expectError(http.StatusForbidden, "forbidden")
		c.do("GET", "/api/v1/articles?status=all", admin.AccessToken, nil).expect(http.StatusOK, &list)
		if len(list) != 3 {
			t.Fatalf("admin melihat semua status: %d", len(list))
		}

		c.do("GET", "/api/v1/articles?sort=acak", "", nil).expectError(http.StatusBadRequest, "bad_request")
		c.do("GET", "/api/v1/articles?page=0", "", nil).expectError(http.StatusBadRequest, "bad_request")

		c.do("GET", "/api/v1/me/articles", writer.AccessToken, nil).expect(http.StatusOK, &list)
		if len(list) != 2 {
			t.Fatalf("article milik penulis: %d", len(list))
		}

		var tags []tag
		c.do("GET", "/api/v1/tags", "", nil).expect(http.StatusOK, &tags)
		if len(tags) != 2 || tags[0].Name != "go" || tags[0].ArticleCount != 1 || tags[1].ArticleCount != 0 {
			t.Fatalf("tag: %+v", tags)
		}

		var categories []category
		c.do("GET", "/api/v1/categories", "", nil).expect(http.StatusOK, &categories)
		if len(categories) != 2 {
			t.Fatalf("kategori: %+v", categories)
		}
	})

	t.Run("komentar", func(t *testing.T) {
		c := client{t: t, base: server.URL}
		path := fmt.Sprintf("/api/v1/articles/%d/comments", published.ID)

		c.do("POST", path, "", map[string]string{"body": "Halo"}).expectError(http.StatusUnauthorized, "unauthorized")
		c.do("POST", path, reader.AccessToken, map[string]string{"body": "   "}).
			expectError(http.StatusUnprocessableEntity, "validation_failed")

		var cm comment
		c.do("POST", path, reader.AccessToken, map[string]string{"body": " Tulisan yang bagus! "}).
			expect(http.StatusCreated, &cm)
		if cm.Body != "Tulisan yang bagus!" || cm.Author.ID != reader.User.ID {
			t.Fatalf("komentar: %+v", cm)
		}

		// Draft milik admin tidak terlihat oleh pembaca.
		c.do("POST", fmt.Sprintf("/api/v1/articles/%d/comments", draft.ID), reader.AccessToken,
			map[string]string{"body": "Halo"}).expectError(http.StatusNotFound, "not_found")
		// Admin bisa melihat draft itu, tapi komentar hanya untuk yang terbit.
		c.do("POST", fmt.Sprintf("/api/v1/articles/%d/comments", draft.ID), admin.AccessToken,
			map[string]string{"body": "Halo"}).expectError(http.StatusForbidden, "forbidden")

		var comments []comment
		res := c.do("GET", path, "", nil).expect(http.StatusOK, &comments)
		if len(comments) != 1 || res.meta().Total != 1 {
			t.Fatalf("daftar komentar: %+v", comments)
		}

		var withCount article
		c.do("GET", fmt.Sprintf("/api/v1/articles/%d", published.ID), "", nil).expect(http.StatusOK, &withCount)
		if withCount.CommentCount != 1 {
			t.Fatalf("comment_count: %d", withCount.CommentCount)
		}

		commentPath := fmt.Sprintf("/api/v1/comments/%d", cm.ID)
		c.do("PATCH", commentPath, writer.AccessToken, map[string]string{"body": "diubah"}).
			expectError(http.StatusForbidden, "forbidden")
		c.do("PATCH", commentPath, admin.AccessToken, map[string]string{"body": "diubah"}).
			expectError(http.StatusForbidden, "forbidden")
		c.do("PATCH", commentPath, reader.AccessToken, map[string]string{"body": "Sudah saya perbaiki"}).
			expect(http.StatusOK, &cm)
		if cm.Body != "Sudah saya perbaiki" {
			t.Fatalf("komentar diubah: %+v", cm)
		}

		c.do("DELETE", commentPath, writer.AccessToken, nil).expectError(http.StatusForbidden, "forbidden")
		c.do("DELETE", commentPath, admin.AccessToken, nil).expect(http.StatusNoContent, nil)
		c.do("DELETE", commentPath, admin.AccessToken, nil).expectError(http.StatusNotFound, "not_found")
	})

	t.Run("sampul, suka, bookmark, dibaca, statistik", func(t *testing.T) {
		c := client{t: t, base: server.URL}
		path := fmt.Sprintf("/api/v1/articles/%d", published.ID)

		var buf bytes.Buffer
		_ = png.Encode(&buf, image.NewRGBA(image.Rect(0, 0, 4, 3)))

		c.upload(reader.AccessToken, buf.Bytes()).expectError(http.StatusForbidden, "forbidden")
		c.upload(writer.AccessToken, []byte("bukan gambar")).expectError(http.StatusUnprocessableEntity, "validation_failed")

		var uploaded struct{ URL string }
		c.upload(writer.AccessToken, buf.Bytes()).expect(http.StatusCreated, &uploaded)

		res := c.do("GET", uploaded.URL, "", nil).expect(http.StatusOK, nil)
		if res.header.Get("Content-Type") != "image/png" || !bytes.Equal(res.body, buf.Bytes()) {
			t.Fatalf("berkas upload: %s", res.header.Get("Content-Type"))
		}

		for _, bad := range []string{"/uploads/../go.mod", "/uploads/" + strings.Repeat("a", 32) + ".png"} {
			c.do("PATCH", path, writer.AccessToken, map[string]any{"cover_image": bad}).
				expectError(http.StatusUnprocessableEntity, "validation_failed")
		}
		var withCover article
		c.do("PATCH", path, writer.AccessToken, map[string]any{"cover_image": uploaded.URL}).expect(http.StatusOK, &withCover)
		if withCover.CoverImage == nil || *withCover.CoverImage != uploaded.URL || withCover.Reading != 1 {
			t.Fatalf("sampul: %+v", withCover)
		}

		c.do("PUT", path+"/like", "", nil).expectError(http.StatusUnauthorized, "unauthorized")
		var state engagement
		c.do("PUT", path+"/like", reader.AccessToken, nil).expect(http.StatusOK, &state)
		c.do("PUT", path+"/like", reader.AccessToken, nil).expect(http.StatusOK, &state)
		if !state.Liked || state.LikeCount != 1 {
			t.Fatalf("suka dua kali tetap satu: %+v", state)
		}
		c.do("PUT", path+"/like", writer.AccessToken, nil).expect(http.StatusOK, &state)
		c.do("DELETE", path+"/like", reader.AccessToken, nil).expect(http.StatusOK, &state)
		if state.Liked || state.LikeCount != 1 {
			t.Fatalf("batal suka: %+v", state)
		}
		c.do("PUT", fmt.Sprintf("/api/v1/articles/%d/like", draft.ID), admin.AccessToken, nil).
			expectError(http.StatusForbidden, "forbidden")

		c.do("PUT", path+"/bookmark", reader.AccessToken, nil).expect(http.StatusOK, &state)
		var saved []article
		res = c.do("GET", "/api/v1/me/bookmarks", reader.AccessToken, nil).expect(http.StatusOK, &saved)
		if len(saved) != 1 || saved[0].ID != published.ID || res.meta().Total != 1 {
			t.Fatalf("bookmark: %+v", saved)
		}

		var seen article
		c.do("GET", path, reader.AccessToken, nil).expect(http.StatusOK, &seen)
		if seen.Liked || !seen.Bookmarked || seen.LikeCount != 1 {
			t.Fatalf("keadaan bagi pembaca: %+v", seen)
		}

		// Pengunjung anonim dihitung sekali per jendela waktu, penulis tidak dihitung.
		c.do("POST", path+"/view", "", nil).expect(http.StatusNoContent, nil)
		c.do("POST", path+"/view", "", nil).expect(http.StatusNoContent, nil)
		c.do("POST", path+"/view", writer.AccessToken, nil).expect(http.StatusNoContent, nil)
		c.do("POST", path+"/view", reader.AccessToken, nil).expect(http.StatusNoContent, nil)
		c.do("GET", path, "", nil).expect(http.StatusOK, &seen)
		if seen.ViewCount != 2 {
			t.Fatalf("view_count: %d", seen.ViewCount)
		}

		var list []article
		c.do("GET", "/api/v1/articles?sort=popular", "", nil).expect(http.StatusOK, &list)
		if len(list) != 2 || list[0].ID != published.ID {
			t.Fatalf("urutan populer: %+v", list)
		}

		c.do("GET", "/api/v1/stats", reader.AccessToken, nil).expectError(http.StatusForbidden, "forbidden")
		c.do("GET", "/api/v1/stats?days=3", writer.AccessToken, nil).expectError(http.StatusBadRequest, "bad_request")

		var mine stats
		c.do("GET", "/api/v1/stats", writer.AccessToken, nil).expect(http.StatusOK, &mine)
		if mine.Scope != "mine" || mine.Totals.Published != 2 || mine.Totals.Views != 2 ||
			mine.Totals.Likes != 1 || mine.Totals.Bookmarks != 1 || mine.Users != nil {
			t.Fatalf("statistik penulis: %+v", mine)
		}
		if len(mine.Daily) != 30 || mine.Daily[29].Views != 2 || mine.Daily[29].Date != time.Now().UTC().Format(time.DateOnly) {
			t.Fatalf("statistik harian: %+v", mine.Daily[len(mine.Daily)-1])
		}

		var all stats
		c.do("GET", "/api/v1/stats?days=7", admin.AccessToken, nil).expect(http.StatusOK, &all)
		if all.Scope != "all" || all.Users["reader"] != 1 || len(all.Daily) != 7 ||
			len(all.TopArticles) == 0 || all.TopArticles[0].ID != published.ID {
			t.Fatalf("statistik admin: %+v", all)
		}
	})

	t.Run("spam dan moderasi komentar", func(t *testing.T) {
		c := client{t: t, base: server.URL}
		path := fmt.Sprintf("/api/v1/articles/%d/comments", published.ID)

		for _, body := range []string{
			"Kunjungi https://a.example, https://b.example, dan www.c.example sekarang",
			"INI KOMENTAR YANG SELURUHNYA DITULIS KAPITAL",
		} {
			c.do("POST", path, reader.AccessToken, map[string]string{"body": body}).
				expectError(http.StatusUnprocessableEntity, "validation_failed")
		}

		var target comment
		c.do("POST", path, writer.AccessToken, map[string]string{"body": "Komentar yang nanti dilaporkan"}).
			expect(http.StatusCreated, &target)
		c.do("POST", path, writer.AccessToken, map[string]string{"body": "Komentar yang nanti dilaporkan"}).
			expectError(http.StatusConflict, "conflict")

		report := fmt.Sprintf("/api/v1/comments/%d/report", target.ID)
		c.do("POST", report, "", map[string]string{"reason": "spam"}).expectError(http.StatusUnauthorized, "unauthorized")
		c.do("POST", report, writer.AccessToken, map[string]string{"reason": "spam"}).expectError(http.StatusForbidden, "forbidden")
		c.do("POST", report, reader.AccessToken, map[string]string{"reason": "iklan"}).
			expectError(http.StatusUnprocessableEntity, "validation_failed")

		var result struct{ Reported, Hidden bool }
		c.do("POST", report, reader.AccessToken, map[string]string{"reason": "spam"}).expect(http.StatusOK, &result)
		c.do("POST", report, reader.AccessToken, map[string]string{"reason": "spam"}).expect(http.StatusOK, &result)
		if !result.Reported || result.Hidden {
			t.Fatalf("satu pelapor belum menyembunyikan komentar: %+v", result)
		}

		// Pelapor kedua mencapai ambang (2 di test ini).
		c.do("POST", report, admin.AccessToken, map[string]string{"reason": "abusive"}).expect(http.StatusOK, &result)
		if !result.Hidden {
			t.Fatalf("komentar seharusnya disembunyikan: %+v", result)
		}

		visible := func() bool {
			var comments []comment
			c.do("GET", path, "", nil).expect(http.StatusOK, &comments)
			for _, cm := range comments {
				if cm.ID == target.ID {
					return true
				}
			}
			return false
		}
		if visible() {
			t.Fatal("komentar tersembunyi masih tampil")
		}
		var art article
		c.do("GET", fmt.Sprintf("/api/v1/articles/%d", published.ID), "", nil).expect(http.StatusOK, &art)
		if art.CommentCount != 0 {
			t.Fatalf("comment_count menghitung komentar tersembunyi: %d", art.CommentCount)
		}

		c.do("GET", "/api/v1/moderation/comments", reader.AccessToken, nil).expectError(http.StatusForbidden, "forbidden")

		var queue []struct {
			ID      int64          `json:"id"`
			Hidden  bool           `json:"hidden"`
			Reports int            `json:"reports"`
			Reasons map[string]int `json:"reasons"`
			Article struct {
				Slug string `json:"slug"`
			} `json:"article"`
		}
		res := c.do("GET", "/api/v1/moderation/comments", admin.AccessToken, nil).expect(http.StatusOK, &queue)
		if len(queue) != 1 || res.meta().Total != 1 || queue[0].ID != target.ID || !queue[0].Hidden ||
			queue[0].Reports != 2 || queue[0].Reasons["spam"] != 1 || queue[0].Reasons["abusive"] != 1 ||
			queue[0].Article.Slug != published.Slug {
			t.Fatalf("antrean moderasi: %+v", queue)
		}

		moderate := fmt.Sprintf("/api/v1/moderation/comments/%d", target.ID)
		c.do("POST", moderate, admin.AccessToken, map[string]string{"action": "hapus"}).
			expectError(http.StatusUnprocessableEntity, "validation_failed")
		c.do("POST", moderate, admin.AccessToken, map[string]string{"action": "approve"}).expect(http.StatusNoContent, nil)
		if !visible() {
			t.Fatal("komentar yang disetujui seharusnya tampil lagi")
		}
		c.do("GET", "/api/v1/moderation/comments", admin.AccessToken, nil).expect(http.StatusOK, &queue)
		if len(queue) != 0 {
			t.Fatalf("laporan seharusnya dibersihkan: %+v", queue)
		}

		c.do("POST", moderate, admin.AccessToken, map[string]string{"action": "hide"}).expect(http.StatusNoContent, nil)
		if visible() {
			t.Fatal("admin seharusnya bisa menyembunyikan langsung")
		}
		c.do("DELETE", fmt.Sprintf("/api/v1/comments/%d", target.ID), admin.AccessToken, nil).expect(http.StatusNoContent, nil)
	})

	t.Run("batas per akun dan IP di balik proxy", func(t *testing.T) {
		limited := cfg
		limited.CommentRateLimit = 2
		limited.AuthRateLimit = 2
		limited.TrustedProxies = []string{"127.0.0.1", "::1"}
		a2, err := app.New(limited, db, auth.BcryptHasher{Cost: bcrypt.MinCost}, &outbox{})
		if err != nil {
			t.Fatal(err)
		}
		proxied := httptest.NewServer(a2.Handler)
		defer proxied.Close()
		c := client{t: t, base: proxied.URL}

		path := fmt.Sprintf("/api/v1/articles/%d/comments", published.ID)
		for i := 1; i <= 2; i++ {
			c.do("POST", path, reader.AccessToken, map[string]string{"body": fmt.Sprintf("Komentar cepat ke-%d", i)}).
				expect(http.StatusCreated, nil)
		}
		res := c.do("POST", path, reader.AccessToken, map[string]string{"body": "Komentar cepat ke-3"})
		res.expectError(http.StatusTooManyRequests, "too_many_requests")
		if res.header.Get("Retry-After") == "" {
			t.Fatal("Retry-After seharusnya diisi")
		}
		// Batas komentar per akun, akun lain tidak ikut terkena.
		c.do("POST", path, writer.AccessToken, map[string]string{"body": "Komentar dari akun lain"}).expect(http.StatusCreated, nil)

		// Batas login per IP asli pengunjung, bukan per IP proxy.
		wrong := map[string]string{"email": "pembaca@warta.test", "password": "salah-sekali"}
		c.doFrom("198.51.100.1", "POST", "/api/v1/auth/login", "", wrong).expectError(http.StatusUnauthorized, "unauthorized")
		c.doFrom("198.51.100.1", "POST", "/api/v1/auth/login", "", wrong).expectError(http.StatusUnauthorized, "unauthorized")
		c.doFrom("198.51.100.1", "POST", "/api/v1/auth/login", "", wrong).expectError(http.StatusTooManyRequests, "too_many_requests")
		c.doFrom("198.51.100.2", "POST", "/api/v1/auth/login", "", wrong).expectError(http.StatusUnauthorized, "unauthorized")

		// Pengunjung anonim dari IP berbeda dihitung terpisah.
		var before, after article
		view := fmt.Sprintf("/api/v1/articles/%d/view", published.ID)
		c.do("GET", fmt.Sprintf("/api/v1/articles/%d", published.ID), "", nil).expect(http.StatusOK, &before)
		c.doFrom("198.51.100.3", "POST", view, "", nil).expect(http.StatusNoContent, nil)
		c.doFrom("198.51.100.4", "POST", view, "", nil).expect(http.StatusNoContent, nil)
		c.doFrom("198.51.100.4", "POST", view, "", nil).expect(http.StatusNoContent, nil)
		c.do("GET", fmt.Sprintf("/api/v1/articles/%d", published.ID), "", nil).expect(http.StatusOK, &after)
		if after.ViewCount != before.ViewCount+2 {
			t.Fatalf("view_count %d -> %d, ingin +2", before.ViewCount, after.ViewCount)
		}
	})

	t.Run("verifikasi email dan lupa password", func(t *testing.T) {
		strict := cfg
		strict.RequireEmailVerification = true
		box := &outbox{}
		a3, err := app.New(strict, db, auth.BcryptHasher{Cost: bcrypt.MinCost}, box)
		if err != nil {
			t.Fatal(err)
		}
		srv := httptest.NewServer(a3.Handler)
		defer srv.Close()
		c := client{t: t, base: srv.URL}

		var baru tokens
		c.do("POST", "/api/v1/auth/register", "", map[string]string{
			"name": "Akun Baru", "email": "baru@warta.test", "password": "rahasia123",
		}).expect(http.StatusCreated, &baru)
		if baru.User.EmailVerified {
			t.Fatal("akun baru belum terverifikasi")
		}
		path, verifyToken := box.lastLink(t, "baru@warta.test")
		if path != "/verify-email" {
			t.Fatalf("tautan verifikasi: %s", path)
		}

		// Belum terverifikasi: tidak bisa berkomentar atau melapor.
		comments := fmt.Sprintf("/api/v1/articles/%d/comments", published.ID)
		c.do("POST", comments, baru.AccessToken, map[string]string{"body": "Halo semuanya"}).
			expectError(http.StatusForbidden, "email_not_verified")
		var target comment
		c.do("POST", comments, admin.AccessToken, map[string]string{"body": "Komentar admin untuk dilaporkan"}).
			expect(http.StatusCreated, &target)
		c.do("POST", fmt.Sprintf("/api/v1/comments/%d/report", target.ID), baru.AccessToken, map[string]string{"reason": "spam"}).
			expectError(http.StatusForbidden, "email_not_verified")

		c.do("POST", "/api/v1/auth/resend-verification", "", nil).expectError(http.StatusUnauthorized, "unauthorized")
		c.do("POST", "/api/v1/auth/resend-verification", baru.AccessToken, nil).
			expectError(http.StatusTooManyRequests, "too_many_requests")

		c.do("POST", "/api/v1/auth/verify-email", "", map[string]string{"token": "palsu"}).
			expectError(http.StatusUnprocessableEntity, "validation_failed")
		var verified user
		c.do("POST", "/api/v1/auth/verify-email", "", map[string]string{"token": verifyToken}).expect(http.StatusOK, &verified)
		if !verified.EmailVerified || verified.ID != baru.User.ID {
			t.Fatalf("hasil verifikasi: %+v", verified)
		}
		c.do("POST", "/api/v1/auth/verify-email", "", map[string]string{"token": verifyToken}).
			expectError(http.StatusUnprocessableEntity, "validation_failed")
		c.do("POST", "/api/v1/auth/resend-verification", baru.AccessToken, nil).expectError(http.StatusConflict, "conflict")

		// Langsung berlaku tanpa perlu token baru.
		c.do("POST", comments, baru.AccessToken, map[string]string{"body": "Halo semuanya"}).expect(http.StatusCreated, nil)
		var me user
		c.do("GET", "/api/v1/me", baru.AccessToken, nil).expect(http.StatusOK, &me)
		if !me.EmailVerified {
			t.Fatal("/me seharusnya terverifikasi")
		}

		// Lupa password: jawaban sama untuk email terdaftar dan tidak.
		c.do("POST", "/api/v1/auth/forgot-password", "", map[string]string{"email": "tidak-ada@warta.test"}).
			expect(http.StatusNoContent, nil)
		c.do("POST", "/api/v1/auth/forgot-password", "", map[string]string{"email": "bukan-email"}).
			expectError(http.StatusUnprocessableEntity, "validation_failed")
		if box.count("tidak-ada@warta.test") != 0 {
			t.Fatal("email tidak terdaftar tidak boleh dikirimi")
		}
		c.do("POST", "/api/v1/auth/forgot-password", "", map[string]string{"email": "Baru@Warta.test"}).
			expect(http.StatusNoContent, nil)
		c.do("POST", "/api/v1/auth/forgot-password", "", map[string]string{"email": "baru@warta.test"}).
			expect(http.StatusNoContent, nil)
		if n := box.count("baru@warta.test"); n != 2 {
			t.Fatalf("seharusnya 1 email verifikasi + 1 email reset, terkirim %d", n)
		}
		path, resetToken := box.lastLink(t, "baru@warta.test")
		if path != "/reset-password" {
			t.Fatalf("tautan reset: %s", path)
		}

		fields := c.do("POST", "/api/v1/auth/reset-password", "", map[string]string{"token": resetToken, "new_password": "pendek"}).
			expectError(http.StatusUnprocessableEntity, "validation_failed")
		if fields["new_password"] == "" {
			t.Fatalf("password pendek: %v", fields)
		}
		c.do("POST", "/api/v1/auth/reset-password", "", map[string]string{"token": resetToken, "new_password": "rahasia-baru-99"}).
			expect(http.StatusNoContent, nil)
		c.do("POST", "/api/v1/auth/reset-password", "", map[string]string{"token": resetToken, "new_password": "rahasia-lain-99"}).
			expectError(http.StatusUnprocessableEntity, "validation_failed")

		// Semua sesi lama dicabut, password baru berlaku.
		c.do("POST", "/api/v1/auth/refresh", "", map[string]string{"refresh_token": baru.RefreshToken}).
			expectError(http.StatusUnauthorized, "unauthorized")
		c.do("POST", "/api/v1/auth/login", "", map[string]string{"email": "baru@warta.test", "password": "rahasia123"}).
			expectError(http.StatusUnauthorized, "unauthorized")
		c.do("POST", "/api/v1/auth/login", "", map[string]string{"email": "baru@warta.test", "password": "rahasia-baru-99"}).
			expect(http.StatusOK, nil)
	})

	t.Run("sitemap dan RSS", func(t *testing.T) {
		c := client{t: t, base: server.URL}

		res := c.do("GET", "/sitemap.xml", "", nil).expect(http.StatusOK, nil)
		body := string(res.body)
		if !strings.HasPrefix(res.header.Get("Content-Type"), "application/xml") || res.header.Get("Cache-Control") == "" {
			t.Fatalf("header sitemap: %v", res.header)
		}
		for _, want := range []string{
			"<loc>https://warta.test/</loc>",
			"<loc>https://warta.test/artikel/" + published.Slug + "</loc>",
			"<loc>https://warta.test/kategori/" + tech.Slug + "</loc>",
		} {
			if !strings.Contains(body, want) {
				t.Errorf("sitemap tanpa %s:\n%s", want, body)
			}
		}
		if strings.Contains(body, draft.Slug) {
			t.Error("draft tidak boleh masuk sitemap")
		}

		res = c.do("GET", "/feed.xml", "", nil).expect(http.StatusOK, nil)
		body = string(res.body)
		if !strings.HasPrefix(res.header.Get("Content-Type"), "application/rss+xml") {
			t.Fatalf("header RSS: %v", res.header)
		}
		for _, want := range []string{
			`<rss version="2.0"`,
			`<atom:link href="https://warta.test/feed.xml" rel="self" type="application/rss+xml">`,
			"<link>https://warta.test/artikel/" + published.Slug + "</link>",
			"<title>" + published.Title + "</title>",
		} {
			if !strings.Contains(body, want) {
				t.Errorf("RSS tanpa %s:\n%s", want, body)
			}
		}
		if strings.Contains(body, draft.Slug) {
			t.Error("draft tidak boleh masuk RSS")
		}
	})

	t.Run("menghapus", func(t *testing.T) {
		c := client{t: t, base: server.URL}

		c.do("DELETE", fmt.Sprintf("/api/v1/categories/%d", tech.ID), admin.AccessToken, nil).
			expectError(http.StatusConflict, "conflict")

		path := fmt.Sprintf("/api/v1/articles/%d", published.ID)
		c.do("DELETE", path, reader.AccessToken, nil).expectError(http.StatusForbidden, "forbidden")
		c.do("DELETE", path, writer.AccessToken, nil).expect(http.StatusNoContent, nil)
		c.do("GET", path, writer.AccessToken, nil).expectError(http.StatusNotFound, "not_found")

		c.do("DELETE", fmt.Sprintf("/api/v1/categories/%d", tech.ID), admin.AccessToken, nil).
			expect(http.StatusNoContent, nil)
	})

	t.Run("akun sendiri", func(t *testing.T) {
		c := client{t: t, base: server.URL}

		var me user
		c.do("PATCH", "/api/v1/me", reader.AccessToken, map[string]string{"name": "Pembaca Setia"}).
			expect(http.StatusOK, &me)
		if me.Name != "Pembaca Setia" {
			t.Fatalf("nama: %s", me.Name)
		}

		c.do("PUT", "/api/v1/me/password", reader.AccessToken, map[string]string{
			"current_password": "salah-sekali", "new_password": "rahasia456",
		}).expectError(http.StatusUnprocessableEntity, "validation_failed")
		c.do("PUT", "/api/v1/me/password", reader.AccessToken, map[string]string{
			"current_password": "rahasia123", "new_password": "rahasia456",
		}).expect(http.StatusNoContent, nil)

		// Ganti password mencabut sesi lain.
		c.do("POST", "/api/v1/auth/refresh", "", map[string]string{"refresh_token": reader.RefreshToken}).
			expectError(http.StatusUnauthorized, "unauthorized")

		c.do("POST", "/api/v1/auth/login", "", map[string]string{
			"email": "pembaca@warta.test", "password": "rahasia456",
		}).expect(http.StatusOK, &reader)

		c.do("POST", "/api/v1/auth/logout", "", map[string]string{"refresh_token": reader.RefreshToken}).
			expect(http.StatusNoContent, nil)
		c.do("POST", "/api/v1/auth/refresh", "", map[string]string{"refresh_token": reader.RefreshToken}).
			expectError(http.StatusUnauthorized, "unauthorized")
	})

	t.Run("permintaan yang salah bentuk", func(t *testing.T) {
		c := client{t: t, base: server.URL}

		c.do("GET", "/api/v1/tidak-ada", "", nil).expectError(http.StatusNotFound, "not_found")
		c.do("DELETE", "/api/v1/tags", admin.AccessToken, nil).expectError(http.StatusMethodNotAllowed, "method_not_allowed")
		c.do("GET", "/api/v1/articles/abc/comments", "", nil).expectError(http.StatusBadRequest, "bad_request")
		c.do("POST", "/api/v1/auth/login", "", nil).expectError(http.StatusBadRequest, "bad_request")
		c.do("POST", "/api/v1/articles", writer.AccessToken, map[string]any{"category_id": "satu"}).
			expectError(http.StatusBadRequest, "bad_request")
		c.do("POST", "/api/v1/articles", writer.AccessToken, map[string]string{"content": strings.Repeat("a", 2<<20)}).
			expectError(http.StatusRequestEntityTooLarge, "payload_too_large")
	})
}

// TestLegacyPostsMigration memastikan database dari versi awal (tabel posts)
// naik ke skema sekarang tanpa kehilangan data.
func TestLegacyPostsMigration(t *testing.T) {
	cfg := testConfig(t)
	migrate(t, cfg, 1)

	db := connect(t, cfg)
	_, err := db.Exec(`
		INSERT INTO posts (title, content, category, created_date, updated_date, status) VALUES
		('Panduan Membangun REST API dengan Golang', ?, 'Teknologi', '2024-01-02 03:04:05', '2024-02-03 04:05:06', 'publish'),
		('Catatan Draft Tentang Indeks MySQL', ?, 'teknologi', '2024-03-01 00:00:00', '2024-03-02 00:00:00', 'draft'),
		('Artikel Lama Yang Sudah Dibuang', ?, 'Gaya Hidup', '2024-04-01 00:00:00', '2024-04-02 00:00:00', 'thrash')`,
		content, content, content)
	if err != nil {
		t.Fatalf("mengisi posts: %v", err)
	}

	migrate(t, cfg, latestVersion)

	a, err := app.New(cfg, db, auth.BcryptHasher{Cost: bcrypt.MinCost}, &outbox{})
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(a.Handler)
	defer server.Close()
	c := client{t: t, base: server.URL}

	var got article
	c.do("GET", "/api/v1/articles/1", "", nil).expect(http.StatusOK, &got)
	if got.Status != "published" || got.Slug != "panduan-membangun-rest-api-dengan-golang-1" ||
		got.Category.Slug != "teknologi" || got.Author.Name != "Arsip Warta" {
		t.Fatalf("post terbit: %+v", got)
	}
	if got.PublishedAt == nil || !got.PublishedAt.Equal(time.Date(2024, 1, 2, 3, 4, 5, 0, time.UTC)) {
		t.Fatalf("published_at seharusnya sama dengan created_date lama: %v", got.PublishedAt)
	}

	var updatedAt time.Time
	if err := db.QueryRow("SELECT updated_at FROM articles WHERE id = 1").Scan(&updatedAt); err != nil {
		t.Fatal(err)
	}
	if !updatedAt.Equal(time.Date(2024, 2, 3, 4, 5, 6, 0, time.UTC)) {
		t.Fatalf("updated_at berubah oleh migrasi: %v", updatedAt)
	}

	var statuses []string
	rows, err := db.Query("SELECT status FROM articles ORDER BY id")
	if err != nil {
		t.Fatal(err)
	}
	for rows.Next() {
		var s string
		_ = rows.Scan(&s)
		statuses = append(statuses, s)
	}
	rows.Close()
	if strings.Join(statuses, ",") != "published,draft,archived" {
		t.Fatalf("status: %v", statuses)
	}

	var categories []category
	c.do("GET", "/api/v1/categories", "", nil).expect(http.StatusOK, &categories)
	if len(categories) != 2 {
		t.Fatalf("Teknologi dan teknologi seharusnya satu kategori: %+v", categories)
	}

	// Akun arsip tidak bisa dipakai login, termasuk dengan isi password_hash-nya.
	for _, password := range []string{"!", "apa-saja-123"} {
		c.do("POST", "/api/v1/auth/login", "", map[string]string{
			"email": "arsip@warta.local", "password": password,
		}).expectError(http.StatusUnauthorized, "unauthorized")
	}

	migrate(t, cfg, 1)
	var title, category, status string
	err = db.QueryRow("SELECT title, category, status FROM posts WHERE id = 3").Scan(&title, &category, &status)
	if err != nil || category != "Gaya Hidup" || status != "thrash" {
		t.Fatalf("migrasi turun: %s %s %s %v", title, category, status, err)
	}
}
