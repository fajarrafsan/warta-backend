// Command seed mengisi database kosong dengan data contoh: kategori, penulis
// dengan foto dan bio, artikel bersampul, komentar, suka, pengikut, dan
// hitungan dibaca 30 hari terakhir. Berguna untuk mencoba Warta tanpa harus
// menulis semuanya sendiri.
//
//	go run ./cmd/seed
//	docker compose exec api warta-seed
//
// Data dimasukkan lewat API yang sama dengan yang dipakai frontend (dijalankan
// di dalam proses), jadi semua aturan validasi tetap berlaku. Hanya tanggal
// yang digeser lewat SQL supaya grafik dashboard terisi.
package main

import (
	"bytes"
	"context"
	"database/sql"
	"embed"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"math/rand/v2"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"sort"
	"strconv"
	"strings"
	"time"

	"golang.org/x/crypto/bcrypt"

	"warta/internal/app"
	"warta/internal/auth"
	"warta/internal/config"
	"warta/internal/database"
	"warta/internal/mail"
)

//go:embed content/*.md
var contentFS embed.FS

// password dipakai semua akun contoh, supaya mudah dicoba.
const password = "warta12345"

type person struct {
	key, name, email, bio string
	writer                bool
	palette               int
}

var people = []person{
	{"dimas", "Dimas Pratama", "dimas@warta.local", "Menulis tentang backend, kode yang rapi, dan sesekali sains populer.", true, 0},
	{"sekar", "Sekar Ayu", "sekar@warta.local", "Mencatat kebiasaan kecil, perjalanan murah, dan usaha rumahan.", true, 2},
	{"bima", "Bima Santoso", "bima@warta.local", "Penasaran pada angka di balik bisnis kecil dan bumi yang bergerak.", true, 6},
	{"laras", "Laras", "laras@warta.local", "", false, 3},
	{"yoga", "Yoga", "yoga@warta.local", "", false, 4},
	{"nadia", "Nadia", "nadia@warta.local", "", false, 5},
	{"rizky", "Rizky", "rizky@warta.local", "", false, 7},
}

var categories = []struct{ name, description string }{
	{"Teknologi", "Perangkat lunak, internet, dan cara kita bekerja dengannya"},
	{"Bisnis", "Usaha kecil, pasar, dan ekonomi sehari-hari"},
	{"Gaya Hidup", "Kebiasaan, kota, dan hal-hal yang membuat hari lebih baik"},
	{"Sains", "Temuan dan penjelasan dari dunia penelitian"},
}

var comments = []string{
	"Penjelasannya runtut, terima kasih sudah menulis ini.",
	"Bagian terakhir paling mengena buat saya.",
	"Saya coba praktikkan minggu ini, nanti saya kabari hasilnya.",
	"Ada rekomendasi bacaan lanjutan untuk topik ini?",
	"Setuju. Hal kecil seperti ini sering terlewat.",
	"Baru tahu soal ini, menarik sekali.",
	"Tulisan seperti ini yang bikin betah membaca Warta.",
	"Contohnya membantu sekali untuk pemula seperti saya.",
}

func main() {
	allowProduction := flag.Bool("allow-production", false, "izinkan berjalan saat APP_ENV=production")
	flag.Parse()

	slog.SetDefault(slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelWarn})))
	if err := run(*allowProduction); err != nil {
		fmt.Fprintln(os.Stderr, "seed gagal:", err)
		os.Exit(1)
	}
}

func run(allowProduction bool) error {
	cfg, err := config.Load()
	if err != nil {
		return err
	}
	if cfg.Env == config.EnvProduction && !allowProduction {
		return errors.New("APP_ENV=production; data contoh tidak untuk situs sungguhan (pakai -allow-production bila yakin)")
	}
	if cfg.AdminEmail == "" {
		return errors.New("ADMIN_EMAIL dan ADMIN_PASSWORD wajib diisi; admin dipakai untuk membuat kategori dan menaikkan role penulis")
	}

	ctx := context.Background()
	if err := database.CreateDatabase(ctx, cfg.ServerDSN(), cfg.DBName); err != nil {
		return err
	}
	if err := database.MigrateUp(cfg.MigrationDSN(), cfg.DBName); err != nil {
		return err
	}
	db, err := database.Connect(ctx, cfg.DSN())
	if err != nil {
		return err
	}
	defer db.Close()

	var articles int
	if err := db.QueryRowContext(ctx, "SELECT COUNT(*) FROM articles").Scan(&articles); err != nil {
		return err
	}
	if articles > 0 {
		return fmt.Errorf("database %s sudah berisi %d artikel; data contoh hanya untuk database kosong", cfg.DBName, articles)
	}

	// Batas permintaan dilonggarkan dan email tidak dikirim: semua akun
	// contoh memakai alamat @warta.local yang tidak ada.
	cfg.AuthRateLimit, cfg.CommentRateLimit, cfg.UploadRateLimit = 100000, 100000, 100000
	cfg.RequireEmailVerification = false
	cfg.RedisURL = ""
	a, err := app.New(cfg, db, auth.BcryptHasher{Cost: bcrypt.DefaultCost}, discard{})
	if err != nil {
		return err
	}
	defer a.Close()
	if err := a.Auth.EnsureAdmin(ctx, cfg.AdminName, cfg.AdminEmail, cfg.AdminPassword); err != nil {
		return err
	}

	s := &seeder{api: a.Handler, db: db, rng: rand.New(rand.NewPCG(2026, 10))}
	if err := s.seed(cfg.AdminEmail, cfg.AdminPassword); err != nil {
		return err
	}

	fmt.Println("Data contoh siap.")
	fmt.Printf("  admin    %s (password dari ADMIN_PASSWORD)\n", cfg.AdminEmail)
	for _, p := range people {
		role := "pembaca"
		if p.writer {
			role = "penulis"
		}
		fmt.Printf("  %-8s %s / %s\n", role, p.email, password)
	}
	return nil
}

type discard struct{}

func (discard) Send(context.Context, mail.Message) error { return nil }

type seeder struct {
	api   http.Handler
	db    *sql.DB
	rng   *rand.Rand
	admin string
	users map[string]account
}

type account struct {
	id    int64
	token string
}

type article struct {
	id     int64
	author string
	status string
	days   int
}

func (s *seeder) seed(adminEmail, adminPassword string) error {
	var err error
	if s.admin, _, err = s.login(adminEmail, adminPassword); err != nil {
		return fmt.Errorf("login admin: %w", err)
	}

	categoryIDs := map[string]int64{}
	for _, c := range categories {
		var created struct{ ID int64 }
		if err := s.call("POST", "/api/v1/categories", s.admin, map[string]string{"name": c.name, "description": c.description}, &created); err != nil {
			return fmt.Errorf("kategori %s: %w", c.name, err)
		}
		categoryIDs[c.name] = created.ID
	}

	s.users = map[string]account{}
	for _, p := range people {
		acc, err := s.person(p)
		if err != nil {
			return fmt.Errorf("akun %s: %w", p.email, err)
		}
		s.users[p.key] = acc
	}

	posts, err := s.articles(categoryIDs)
	if err != nil {
		return err
	}
	if err := s.engagement(posts); err != nil {
		return err
	}
	return s.history(posts)
}

// person mendaftarkan akun. Penulis dinaikkan rolenya oleh admin lalu diberi
// foto dan bio.
func (s *seeder) person(p person) (account, error) {
	var created struct {
		User struct{ ID int64 } `json:"user"`
	}
	err := s.call("POST", "/api/v1/auth/register", "", map[string]string{
		"name": p.name, "email": p.email, "password": password,
	}, &created)
	if err != nil {
		// Seed sebelumnya mungkin berhenti di tengah setelah akun dibuat.
		_, id, loginErr := s.login(p.email, password)
		if loginErr != nil {
			return account{}, err
		}
		created.User.ID = id
	}
	// Akun contoh dianggap sudah memverifikasi email.
	if _, err := s.db.Exec("UPDATE users SET email_verified_at = NOW() WHERE id = ?", created.User.ID); err != nil {
		return account{}, err
	}

	if p.writer {
		path := fmt.Sprintf("/api/v1/users/%d/role", created.User.ID)
		if err := s.call("PATCH", path, s.admin, map[string]string{"role": "author"}, nil); err != nil {
			return account{}, err
		}
	}

	token, id, err := s.login(p.email, password)
	if err != nil {
		return account{}, err
	}

	profile := map[string]string{}
	if p.bio != "" {
		profile["bio"] = p.bio
	}
	if p.writer {
		url, err := s.upload(token, avatar(palettes[p.palette]))
		if err != nil {
			return account{}, err
		}
		profile["avatar_url"] = url
	}
	if len(profile) > 0 {
		if err := s.call("PATCH", "/api/v1/me", token, profile, nil); err != nil {
			return account{}, err
		}
	}
	return account{id: id, token: token}, nil
}

func (s *seeder) articles(categoryIDs map[string]int64) ([]article, error) {
	files, err := contentFS.ReadDir("content")
	if err != nil {
		return nil, err
	}
	sort.Slice(files, func(i, j int) bool { return files[i].Name() < files[j].Name() })

	var posts []article
	for i, f := range files {
		raw, err := contentFS.ReadFile("content/" + f.Name())
		if err != nil {
			return nil, err
		}
		meta, body, err := frontMatter(string(raw))
		if err != nil {
			return nil, fmt.Errorf("%s: %w", f.Name(), err)
		}

		author, ok := s.users[meta["author"]]
		if !ok {
			return nil, fmt.Errorf("%s: penulis %q tidak dikenal", f.Name(), meta["author"])
		}
		paletteIndex, _ := strconv.Atoi(meta["cover"])
		coverURL, err := s.upload(author.token, cover(palettes[paletteIndex%len(palettes)], i))
		if err != nil {
			return nil, fmt.Errorf("%s: sampul: %w", f.Name(), err)
		}

		payload := map[string]any{
			"title":       meta["title"],
			"content":     body,
			"category_id": categoryIDs[meta["category"]],
			"tags":        splitTags(meta["tags"]),
			"status":      meta["status"],
			"cover_image": coverURL,
		}
		if meta["status"] == "scheduled" {
			days, _ := strconv.Atoi(meta["days_ahead"])
			payload["scheduled_at"] = time.Now().Add(time.Duration(days)*24*time.Hour + 3*time.Hour).UTC()
		}

		var created struct{ ID int64 }
		if err := s.call("POST", "/api/v1/articles", author.token, payload, &created); err != nil {
			return nil, fmt.Errorf("%s: %w", f.Name(), err)
		}
		days, _ := strconv.Atoi(meta["days_ago"])
		posts = append(posts, article{id: created.ID, author: meta["author"], status: meta["status"], days: days})
	}
	return posts, nil
}

// engagement mengisi pengikut, suka, simpanan, dan komentar dari pembaca.
func (s *seeder) engagement(posts []article) error {
	readers := []string{"laras", "yoga", "nadia", "rizky"}
	writers := []string{"dimas", "sekar", "bima"}

	for _, r := range append(readers, writers...) {
		for _, w := range writers {
			if r == w || s.rng.IntN(10) < 3 {
				continue
			}
			if err := s.call("PUT", fmt.Sprintf("/api/v1/authors/%d/follow", s.users[w].id), s.users[r].token, nil, nil); err != nil {
				return fmt.Errorf("ikuti: %w", err)
			}
		}
	}

	next := 0
	for _, p := range posts {
		if p.status != "published" {
			continue
		}
		path := fmt.Sprintf("/api/v1/articles/%d", p.id)
		for _, r := range readers {
			if s.rng.IntN(10) < 6 {
				if err := s.call("PUT", path+"/like", s.users[r].token, nil, nil); err != nil {
					return fmt.Errorf("suka: %w", err)
				}
			}
			if s.rng.IntN(10) < 3 {
				if err := s.call("PUT", path+"/bookmark", s.users[r].token, nil, nil); err != nil {
					return fmt.Errorf("simpan: %w", err)
				}
			}
		}
		for range 1 + s.rng.IntN(3) {
			r := readers[s.rng.IntN(len(readers))]
			body := comments[next%len(comments)]
			next++
			if err := s.call("POST", path+"/comments", s.users[r].token, map[string]string{"body": body}, nil); err != nil {
				return fmt.Errorf("komentar: %w", err)
			}
		}
	}
	return nil
}

// history menggeser tanggal ke masa lalu dan mengisi hitungan dibaca per
// hari, supaya beranda, "terpopuler", dan grafik dashboard tidak kosong.
// Satu artikel juga disunting dua kali agar riwayat revisinya terisi.
func (s *seeder) history(posts []article) error {
	now := time.Now().UTC()
	for _, p := range posts {
		published := now.Add(-time.Duration(p.days)*24*time.Hour - time.Duration(s.rng.IntN(8)+1)*time.Hour)
		created := published.Add(-time.Duration(s.rng.IntN(48)+2) * time.Hour)
		if p.status != "published" {
			created = now.Add(-time.Duration(s.rng.IntN(72)+1) * time.Hour)
		}

		if _, err := s.db.Exec("UPDATE articles SET created_at = ?, updated_at = ? WHERE id = ?", created, created, p.id); err != nil {
			return err
		}
		if _, err := s.db.Exec("UPDATE article_revisions SET created_at = ? WHERE article_id = ?", created, p.id); err != nil {
			return err
		}
		if p.status != "published" {
			continue
		}
		if _, err := s.db.Exec("UPDATE articles SET published_at = ?, updated_at = ? WHERE id = ?", published, published, p.id); err != nil {
			return err
		}
		if _, err := s.db.Exec(`
			UPDATE comments SET created_at = ? + INTERVAL FLOOR(RAND() * 20 + 1) HOUR, updated_at = created_at
			WHERE article_id = ?`, published, p.id); err != nil {
			return err
		}

		// Artikel baru ramai di hari-hari pertama lalu melandai.
		total := 0
		base := 20 + s.rng.IntN(60)
		for d := 0; d <= min(p.days, 29); d++ {
			day := published.AddDate(0, 0, d)
			if day.After(now) {
				break
			}
			views := base/(d+1) + s.rng.IntN(12)
			total += views
			if _, err := s.db.Exec(`
				INSERT INTO article_daily_views (article_id, day, views) VALUES (?, ?, ?)
				ON DUPLICATE KEY UPDATE views = views + VALUES(views)`,
				p.id, day.Format(time.DateOnly), views); err != nil {
				return err
			}
		}
		if _, err := s.db.Exec("UPDATE articles SET view_count = ?, updated_at = updated_at WHERE id = ?", total, p.id); err != nil {
			return err
		}
	}

	// Riwayat revisi: artikel pertama disunting penulisnya lalu admin.
	first := posts[0]
	var current struct {
		Content string `json:"content"`
	}
	path := fmt.Sprintf("/api/v1/articles/%d", first.id)
	if err := s.call("GET", path, s.users[first.author].token, nil, &current); err != nil {
		return err
	}
	edits := []struct {
		token string
		body  string
	}{
		{s.users[first.author].token, current.Content + "\n\n*Diperbarui: menambahkan catatan tentang batas ukuran body.*"},
		{s.admin, current.Content + "\n\n*Diperbarui: penyuntingan kecil oleh redaksi.*"},
	}
	for _, e := range edits {
		if err := s.call("PATCH", path, e.token, map[string]string{"content": e.body}, nil); err != nil {
			return fmt.Errorf("sunting: %w", err)
		}
	}
	return nil
}

func (s *seeder) login(email, pass string) (token string, id int64, err error) {
	var out struct {
		AccessToken string             `json:"access_token"`
		User        struct{ ID int64 } `json:"user"`
	}
	err = s.call("POST", "/api/v1/auth/login", "", map[string]string{"email": email, "password": pass}, &out)
	return out.AccessToken, out.User.ID, err
}

func (s *seeder) upload(token string, image []byte) (string, error) {
	var body bytes.Buffer
	form := multipart.NewWriter(&body)
	part, err := form.CreateFormFile("image", "gambar.png")
	if err != nil {
		return "", err
	}
	if _, err := part.Write(image); err != nil {
		return "", err
	}
	if err := form.Close(); err != nil {
		return "", err
	}

	req := httptest.NewRequest("POST", "/api/v1/uploads", &body)
	req.Header.Set("Content-Type", form.FormDataContentType())
	var out struct{ URL string }
	err = s.send(req, token, &out)
	return out.URL, err
}

// call memanggil API di dalam proses dan membaca field data dari response.
func (s *seeder) call(method, path, token string, payload, dst any) error {
	var body io.Reader
	if payload != nil {
		raw, err := json.Marshal(payload)
		if err != nil {
			return err
		}
		body = bytes.NewReader(raw)
	}
	req := httptest.NewRequest(method, path, body)
	if payload != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	return s.send(req, token, dst)
}

func (s *seeder) send(req *http.Request, token string, dst any) error {
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	rec := httptest.NewRecorder()
	s.api.ServeHTTP(rec, req)
	if rec.Code >= 300 {
		return fmt.Errorf("%s %s: %d %s", req.Method, req.URL.Path, rec.Code, strings.TrimSpace(rec.Body.String()))
	}
	if dst == nil {
		return nil
	}
	envelope := struct{ Data json.RawMessage }{}
	if err := json.Unmarshal(rec.Body.Bytes(), &envelope); err != nil {
		return err
	}
	return json.Unmarshal(envelope.Data, dst)
}

// frontMatter memisahkan blok "---" di awal berkas (kunci: nilai per baris)
// dari isi Markdown.
func frontMatter(raw string) (map[string]string, string, error) {
	rest, ok := strings.CutPrefix(raw, "---\n")
	if !ok {
		return nil, "", errors.New("tidak diawali ---")
	}
	header, body, ok := strings.Cut(rest, "\n---\n")
	if !ok {
		return nil, "", errors.New("blok --- tidak ditutup")
	}

	meta := map[string]string{}
	for _, line := range strings.Split(header, "\n") {
		key, value, ok := strings.Cut(line, ":")
		if ok {
			meta[strings.TrimSpace(key)] = strings.TrimSpace(value)
		}
	}
	return meta, strings.TrimSpace(body), nil
}

func splitTags(raw string) []string {
	var tags []string
	for _, t := range strings.Split(raw, ",") {
		if t = strings.TrimSpace(t); t != "" {
			tags = append(tags, t)
		}
	}
	return tags
}
