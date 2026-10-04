package app_test

import (
	"bytes"
	"context"
	"database/sql"
	"fmt"
	"image"
	"image/png"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"golang.org/x/crypto/bcrypt"

	"warta/internal/app"
	"warta/internal/auth"
)

// env adalah service lengkap di database baru, dengan satu admin dan satu
// kategori, untuk test fitur yang berdiri sendiri.
type env struct {
	c        client
	app      *app.App
	db       *sql.DB
	admin    tokens
	category category
}

func newEnv(t *testing.T) env {
	t.Helper()
	cfg := testConfig(t)
	migrate(t, cfg, latestVersion)
	db := connect(t, cfg)

	a, err := app.New(cfg, db, auth.BcryptHasher{Cost: bcrypt.MinCost}, &outbox{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { a.Close() })
	if err := a.Auth.EnsureAdmin(context.Background(), "Admin", "admin@warta.test", "admin12345"); err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(a.Handler)
	t.Cleanup(server.Close)

	e := env{c: client{t: t, base: server.URL}, app: a, db: db}
	e.c.do("POST", "/api/v1/auth/login", "", map[string]string{
		"email": "admin@warta.test", "password": "admin12345",
	}).expect(http.StatusOK, &e.admin)
	e.c.do("POST", "/api/v1/categories", e.admin.AccessToken, map[string]string{"name": "Teknologi"}).
		expect(http.StatusCreated, &e.category)
	return e
}

// user mendaftarkan akun; writer menjadikannya author.
func (e env) user(name string, writer bool) tokens {
	e.c.t.Helper()
	var u tokens
	email := strings.ToLower(strings.ReplaceAll(name, " ", ".")) + "@warta.test"
	e.c.do("POST", "/api/v1/auth/register", "", map[string]string{
		"name": name, "email": email, "password": "rahasia123",
	}).expect(http.StatusCreated, &u)
	if !writer {
		return u
	}
	e.c.do("PATCH", fmt.Sprintf("/api/v1/users/%d/role", u.User.ID), e.admin.AccessToken, map[string]string{"role": "author"}).
		expect(http.StatusOK, nil)
	e.c.do("POST", "/api/v1/auth/login", "", map[string]string{"email": email, "password": "rahasia123"}).
		expect(http.StatusOK, &u)
	return u
}

type scheduledArticle struct {
	article
	ScheduledAt *time.Time `json:"scheduled_at"`
	Snippet     string     `json:"snippet"`
	Author      struct {
		ID        int64   `json:"id"`
		AvatarURL *string `json:"avatar_url"`
	} `json:"author"`
}

func (e env) write(token, title, body, status string, extra map[string]any) scheduledArticle {
	e.c.t.Helper()
	payload := map[string]any{
		"title": title, "content": body, "category_id": e.category.ID, "status": status,
	}
	for k, v := range extra {
		payload[k] = v
	}
	var a scheduledArticle
	e.c.do("POST", "/api/v1/articles", token, payload).expect(http.StatusCreated, &a)
	return a
}

type profile struct {
	ID            int64   `json:"id"`
	Name          string  `json:"name"`
	Bio           string  `json:"bio"`
	AvatarURL     *string `json:"avatar_url"`
	ArticleCount  int64   `json:"article_count"`
	FollowerCount int64   `json:"follower_count"`
	ViewCount     int64   `json:"view_count"`
	Following     bool    `json:"following"`
}

func TestAuthorsAndFollow(t *testing.T) {
	e := newEnv(t)
	c := e.c
	writer := e.user("Dimas Pratama", true)
	reader := e.user("Laras", false)

	// Foto profil harus hasil upload yang ada; bio dibatasi panjangnya.
	var buf bytes.Buffer
	_ = png.Encode(&buf, image.NewRGBA(image.Rect(0, 0, 4, 4)))
	var uploaded struct{ URL string }
	c.upload(writer.AccessToken, buf.Bytes()).expect(http.StatusCreated, &uploaded)

	for body, field := range map[string]map[string]any{
		"bio":        {"bio": strings.Repeat("a", 301)},
		"avatar_url": {"avatar_url": "/uploads/" + strings.Repeat("b", 32) + ".png"},
	} {
		if fields := c.do("PATCH", "/api/v1/me", writer.AccessToken, field).
			expectError(http.StatusUnprocessableEntity, "validation_failed"); fields[body] == "" {
			t.Fatalf("%s seharusnya ditolak: %v", body, fields)
		}
	}
	var me struct {
		Name      string  `json:"name"`
		Bio       string  `json:"bio"`
		AvatarURL *string `json:"avatar_url"`
	}
	c.do("PATCH", "/api/v1/me", writer.AccessToken, map[string]any{
		"bio": "  Menulis soal backend dan kopi.  ", "avatar_url": uploaded.URL,
	}).expect(http.StatusOK, &me)
	if me.Name != "Dimas Pratama" || me.Bio != "Menulis soal backend dan kopi." || me.AvatarURL == nil {
		t.Fatalf("PATCH sebagian tidak boleh mengubah nama: %+v", me)
	}

	published := e.write(writer.AccessToken, "Tulisan pertama Dimas di Warta", content, "published", nil)
	if published.Author.AvatarURL == nil || *published.Author.AvatarURL != uploaded.URL {
		t.Fatalf("foto penulis di artikel: %+v", published.Author)
	}
	e.write(writer.AccessToken, "Draft Dimas yang belum terbit", content, "draft", nil)

	// Profil publik hanya menghitung artikel terbit.
	var p profile
	c.do("GET", fmt.Sprintf("/api/v1/authors/%d", writer.User.ID), "", nil).expect(http.StatusOK, &p)
	if p.ArticleCount != 1 || p.Bio == "" || p.Following || p.FollowerCount != 0 {
		t.Fatalf("profil: %+v", p)
	}
	// Pembaca biasa tidak punya profil publik.
	c.do("GET", fmt.Sprintf("/api/v1/authors/%d", reader.User.ID), "", nil).expectError(http.StatusNotFound, "not_found")
	c.do("GET", "/api/v1/authors/999999", "", nil).expectError(http.StatusNotFound, "not_found")

	// Mengikuti.
	path := fmt.Sprintf("/api/v1/authors/%d/follow", writer.User.ID)
	c.do("PUT", path, "", nil).expectError(http.StatusUnauthorized, "unauthorized")
	c.do("PUT", path, writer.AccessToken, nil).expectError(http.StatusBadRequest, "bad_request")
	c.do("PUT", fmt.Sprintf("/api/v1/authors/%d/follow", reader.User.ID), writer.AccessToken, nil).
		expectError(http.StatusNotFound, "not_found")

	var state struct {
		Following     bool  `json:"following"`
		FollowerCount int64 `json:"follower_count"`
	}
	c.do("PUT", path, reader.AccessToken, nil).expect(http.StatusOK, &state)
	c.do("PUT", path, reader.AccessToken, nil).expect(http.StatusOK, &state)
	if !state.Following || state.FollowerCount != 1 {
		t.Fatalf("ikuti dua kali tetap satu: %+v", state)
	}
	c.do("GET", fmt.Sprintf("/api/v1/authors/%d", writer.User.ID), reader.AccessToken, nil).expect(http.StatusOK, &p)
	if !p.Following || p.FollowerCount != 1 {
		t.Fatalf("profil bagi pengikut: %+v", p)
	}

	var following []profile
	res := c.do("GET", "/api/v1/me/following", reader.AccessToken, nil).expect(http.StatusOK, &following)
	if len(following) != 1 || following[0].ID != writer.User.ID || !following[0].Following || res.meta().Total != 1 {
		t.Fatalf("daftar diikuti: %+v", following)
	}

	// Feed hanya berisi artikel terbit dari penulis yang diikuti.
	other := e.user("Sekar Ayu", true)
	e.write(other.AccessToken, "Tulisan Sekar yang tidak diikuti", content, "published", nil)
	var feed []scheduledArticle
	c.do("GET", "/api/v1/me/feed", reader.AccessToken, nil).expect(http.StatusOK, &feed)
	if len(feed) != 1 || feed[0].ID != published.ID {
		t.Fatalf("feed: %+v", feed)
	}

	var stats struct {
		Totals struct {
			Followers int64 `json:"followers"`
		} `json:"totals"`
	}
	c.do("GET", "/api/v1/stats", writer.AccessToken, nil).expect(http.StatusOK, &stats)
	if stats.Totals.Followers != 1 {
		t.Fatalf("statistik pengikut: %+v", stats.Totals)
	}

	res = c.do("GET", "/sitemap.xml", "", nil).expect(http.StatusOK, nil)
	if !strings.Contains(string(res.body), fmt.Sprintf("/penulis/%d</loc>", writer.User.ID)) ||
		strings.Contains(string(res.body), fmt.Sprintf("/penulis/%d</loc>", reader.User.ID)) {
		t.Fatalf("sitemap profil:\n%s", res.body)
	}

	c.do("DELETE", path, reader.AccessToken, nil).expect(http.StatusOK, &state)
	if state.Following || state.FollowerCount != 0 {
		t.Fatalf("berhenti mengikuti: %+v", state)
	}
	c.do("GET", "/api/v1/me/feed", reader.AccessToken, nil).expect(http.StatusOK, &feed)
	if len(feed) != 0 {
		t.Fatalf("feed setelah berhenti mengikuti: %+v", feed)
	}
}

type revision struct {
	ID         int64    `json:"id"`
	Title      string   `json:"title"`
	Content    string   `json:"content"`
	Characters int      `json:"characters"`
	Tags       []string `json:"tags"`
	Editor     *struct {
		ID int64 `json:"id"`
	} `json:"editor"`
}

func TestSchedulingAndRevisions(t *testing.T) {
	e := newEnv(t)
	c := e.c
	writer := e.user("Dimas Pratama", true)
	reader := e.user("Laras", false)

	// Jadwal wajib di masa depan dan paling jauh setahun.
	fields := c.do("POST", "/api/v1/articles", writer.AccessToken, map[string]any{
		"title": "Artikel terjadwal tanpa waktu", "content": content, "category_id": e.category.ID, "status": "scheduled",
	}).expectError(http.StatusUnprocessableEntity, "validation_failed")
	if fields["scheduled_at"] == "" {
		t.Fatalf("scheduled_at wajib: %v", fields)
	}
	for _, at := range []time.Time{time.Now().Add(-time.Hour), time.Now().AddDate(2, 0, 0)} {
		c.do("POST", "/api/v1/articles", writer.AccessToken, map[string]any{
			"title": "Artikel terjadwal di waktu salah", "content": content, "category_id": e.category.ID,
			"status": "scheduled", "scheduled_at": at,
		}).expectError(http.StatusUnprocessableEntity, "validation_failed")
	}

	at := time.Now().Add(2 * time.Hour).UTC().Truncate(time.Second)
	scheduled := e.write(writer.AccessToken, "Artikel yang terbit nanti siang", content, "scheduled",
		map[string]any{"scheduled_at": at, "tags": []string{"golang"}})
	if scheduled.Status != "scheduled" || scheduled.ScheduledAt == nil || !scheduled.ScheduledAt.Equal(at) || scheduled.PublishedAt != nil {
		t.Fatalf("artikel terjadwal: %+v", scheduled)
	}

	// Belum terlihat publik.
	path := fmt.Sprintf("/api/v1/articles/%d", scheduled.ID)
	c.do("GET", path, reader.AccessToken, nil).expectError(http.StatusNotFound, "not_found")
	var list []scheduledArticle
	c.do("GET", "/api/v1/me/articles?status=scheduled", writer.AccessToken, nil).expect(http.StatusOK, &list)
	if len(list) != 1 || list[0].ID != scheduled.ID {
		t.Fatalf("daftar terjadwal: %+v", list)
	}

	// Waktunya belum tiba: job tidak menerbitkan apa pun.
	if n, err := e.app.Articles.PublishDue(context.Background()); err != nil || n != 0 {
		t.Fatalf("PublishDue sebelum waktunya: %d %v", n, err)
	}
	// Majukan waktu terjadwal ke masa lalu, seolah waktunya sudah tiba.
	if _, err := e.db.Exec("UPDATE articles SET scheduled_at = ? WHERE id = ?", time.Now().Add(-time.Minute).UTC(), scheduled.ID); err != nil {
		t.Fatal(err)
	}
	if n, err := e.app.Articles.PublishDue(context.Background()); err != nil || n != 1 {
		t.Fatalf("PublishDue: %d %v", n, err)
	}
	var live scheduledArticle
	c.do("GET", path, reader.AccessToken, nil).expect(http.StatusOK, &live)
	if live.Status != "published" || live.PublishedAt == nil || live.ScheduledAt != nil {
		t.Fatalf("setelah terbit: %+v", live)
	}

	// Riwayat: membuat artikel = revisi 1. Ganti status saja tidak menambah revisi.
	var revisions []revision
	c.do("GET", path+"/revisions", writer.AccessToken, nil).expect(http.StatusOK, &revisions)
	if len(revisions) != 1 || revisions[0].Editor == nil || revisions[0].Editor.ID != writer.User.ID {
		t.Fatalf("revisi awal: %+v", revisions)
	}
	c.do("PATCH", path, writer.AccessToken, map[string]any{"status": "archived"}).expect(http.StatusOK, nil)
	c.do("PATCH", path, writer.AccessToken, map[string]any{"status": "published"}).expect(http.StatusOK, nil)

	edited := content + "\n\nParagraf tambahan dari admin."
	c.do("PATCH", path, e.admin.AccessToken, map[string]any{
		"title": "Judul baru setelah disunting", "content": edited, "tags": []string{"golang", "api"},
	}).expect(http.StatusOK, nil)
	c.do("GET", path+"/revisions", writer.AccessToken, nil).expect(http.StatusOK, &revisions)
	if len(revisions) != 2 || revisions[0].Title != "Judul baru setelah disunting" || revisions[0].Editor.ID != e.admin.User.ID {
		t.Fatalf("revisi setelah sunting: %+v", revisions)
	}
	if revisions[0].Content != "" || revisions[0].Characters != len([]rune(edited)) {
		t.Fatalf("daftar revisi tanpa isi lengkap: %+v", revisions[0])
	}

	// Hanya penulis dan admin.
	c.do("GET", path+"/revisions", reader.AccessToken, nil).expectError(http.StatusForbidden, "forbidden")

	var first revision
	c.do("GET", fmt.Sprintf("%s/revisions/%d", path, revisions[1].ID), writer.AccessToken, nil).expect(http.StatusOK, &first)
	if first.Title != "Artikel yang terbit nanti siang" || first.Content != content || len(first.Tags) != 1 {
		t.Fatalf("isi revisi pertama: %+v", first)
	}
	c.do("GET", path+"/revisions/999999", writer.AccessToken, nil).expectError(http.StatusNotFound, "not_found")

	// Pulihkan: isi kembali, slug tetap (sudah pernah terbit), status tetap,
	// dan pemulihan tercatat sebagai revisi baru.
	var restored scheduledArticle
	c.do("POST", fmt.Sprintf("%s/revisions/%d/restore", path, first.ID), writer.AccessToken, nil).expect(http.StatusOK, &restored)
	if restored.Title != first.Title || restored.Content != content || restored.Status != "published" ||
		restored.Slug != scheduled.Slug || len(restored.Tags) != 1 {
		t.Fatalf("hasil pulihkan: %+v", restored)
	}
	c.do("GET", path+"/revisions", writer.AccessToken, nil).expect(http.StatusOK, &revisions)
	if len(revisions) != 3 {
		t.Fatalf("pemulihan seharusnya menambah revisi: %d", len(revisions))
	}

	// Revisi disimpan paling banyak 50 per artikel.
	for i := range 52 {
		c.do("PATCH", path, writer.AccessToken, map[string]any{"content": fmt.Sprintf("%s\n\nRevisi %d", content, i)}).
			expect(http.StatusOK, nil)
	}
	c.do("GET", path+"/revisions", writer.AccessToken, nil).expect(http.StatusOK, &revisions)
	if len(revisions) != 50 {
		t.Fatalf("jumlah revisi %d, ingin 50", len(revisions))
	}

	var stats struct {
		Totals struct {
			Scheduled int64 `json:"scheduled"`
		} `json:"totals"`
	}
	e.write(writer.AccessToken, "Artikel terjadwal kedua untuk statistik", content, "scheduled",
		map[string]any{"scheduled_at": time.Now().Add(time.Hour)})
	c.do("GET", "/api/v1/stats", writer.AccessToken, nil).expect(http.StatusOK, &stats)
	if stats.Totals.Scheduled != 1 {
		t.Fatalf("statistik terjadwal: %+v", stats.Totals)
	}
}

func TestSearch(t *testing.T) {
	e := newEnv(t)
	c := e.c
	writer := e.user("Dimas Pratama", true)

	padding := strings.Repeat("Paragraf pembuka yang tidak membahas apa pun secara khusus. ", 12)
	golang := e.write(writer.AccessToken, "Membangun REST API dengan Golang", padding+"Di bagian ini kita menulis handler pertama.", "published",
		map[string]any{"tags": []string{"golang"}})
	mention := e.write(writer.AccessToken, "Catatan harian seorang pengembang", padding+"Hari ini saya mencoba golang untuk pertama kali.", "published", nil)
	e.write(writer.AccessToken, "Resep nasi goreng kampung sederhana", padding+"Bumbu dapur dan nasi sisa semalam.", "published", nil)
	e.write(writer.AccessToken, "Draft tentang golang yang belum terbit", padding+"golang golang golang", "draft", nil)

	var results []scheduledArticle
	res := c.do("GET", "/api/v1/articles?q=golang", "", nil).expect(http.StatusOK, &results)
	// Judul berbobot lebih: artikel dengan golang di judul muncul lebih dulu.
	if len(results) != 2 || results[0].ID != golang.ID || results[1].ID != mention.ID || res.meta().Total != 2 {
		t.Fatalf("urutan relevansi: %+v", results)
	}
	// Cuplikan memuat kata yang dicari walau letaknya jauh dari awal isi.
	if !strings.Contains(results[1].Snippet, "golang untuk pertama kali") || !strings.HasPrefix(results[1].Snippet, "…") {
		t.Fatalf("cuplikan: %q", results[1].Snippet)
	}

	// Awalan kata dan semua kata wajib ada.
	c.do("GET", "/api/v1/articles?q=golan%20handler", "", nil).expect(http.StatusOK, &results)
	if len(results) != 1 || results[0].ID != golang.ID {
		t.Fatalf("awalan + AND: %+v", results)
	}
	// Kata pendek (di bawah 3 huruf) tetap bisa dicari.
	c.do("GET", "/api/v1/articles?q=di%20bagian", "", nil).expect(http.StatusOK, &results)
	if len(results) != 1 || results[0].ID != golang.ID {
		t.Fatalf("kata pendek: %+v", results)
	}
	// Operator MySQL dari pengguna tidak berpengaruh dan tidak membuat error.
	c.do("GET", "/api/v1/articles?q=%2Bgolang%20-nasi%20%22%28%29*", "", nil).expect(http.StatusOK, &results)
	c.do("GET", "/api/v1/articles?q=%2B%2B%2B", "", nil).expect(http.StatusOK, &results)
	if len(results) != 0 {
		t.Fatalf("hanya tanda baca: %+v", results)
	}
	// Urutan lain tetap bisa dipilih.
	c.do("GET", "/api/v1/articles?q=golang&sort=oldest", "", nil).expect(http.StatusOK, &results)
	if len(results) != 2 || results[0].ID != golang.ID {
		t.Fatalf("sort=oldest: %+v", results)
	}
	c.do("GET", "/api/v1/articles?sort=acak", "", nil).expectError(http.StatusBadRequest, "bad_request")

	// Saran saat mengetik.
	var suggest struct {
		Articles []struct {
			ID    int64  `json:"id"`
			Title string `json:"title"`
		} `json:"articles"`
		Categories []category `json:"categories"`
		Tags       []tag      `json:"tags"`
		Authors    []struct {
			ID int64 `json:"id"`
		} `json:"authors"`
	}
	res = c.do("GET", "/api/v1/search/suggest?q=gola", "", nil).expect(http.StatusOK, &suggest)
	if len(suggest.Articles) != 1 || suggest.Articles[0].ID != golang.ID || len(suggest.Tags) != 1 || suggest.Tags[0].Slug != "golang" {
		t.Fatalf("saran gola: %+v", suggest)
	}
	if res.header.Get("Cache-Control") == "" {
		t.Fatal("saran seharusnya boleh di-cache")
	}
	c.do("GET", "/api/v1/search/suggest?q=dimas", "", nil).expect(http.StatusOK, &suggest)
	if len(suggest.Authors) != 1 || suggest.Authors[0].ID != writer.User.ID || len(suggest.Articles) != 0 {
		t.Fatalf("saran penulis: %+v", suggest)
	}
	c.do("GET", "/api/v1/search/suggest?q=tekno", "", nil).expect(http.StatusOK, &suggest)
	if len(suggest.Categories) != 1 {
		t.Fatalf("saran kategori: %+v", suggest)
	}
	res = c.do("GET", "/api/v1/search/suggest?q=g", "", nil).expect(http.StatusOK, &suggest)
	if len(suggest.Articles)+len(suggest.Tags)+len(suggest.Authors)+len(suggest.Categories) != 0 ||
		!strings.Contains(string(res.body), `"articles":[]`) {
		t.Fatalf("satu huruf tidak memberi saran: %s", res.body)
	}
}
