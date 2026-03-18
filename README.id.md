# CodexSess Console

<div align="center">
  <img src="./codexsess.png" alt="Logo CodexSess" width="120" height="120">

  <h3>Control Plane Web-First untuk Operasi Akun Codex</h3>
  <p>Kelola routing multi-akun API/CLI, otomasi berbasis usage, dan endpoint proxy kompatibel OpenAI dalam satu binary.</p>

  <p>
    <a href="https://github.com/rickicode/CodexSess/releases/latest">
      <img src="https://img.shields.io/github/v/release/rickicode/CodexSess?style=flat-square" alt="Rilis Terbaru">
    </a>
    <img src="https://img.shields.io/badge/Backend-Go-00ADD8?style=flat-square" alt="Go">
    <img src="https://img.shields.io/badge/Frontend-Svelte-FF3E00?style=flat-square" alt="Svelte">
    <img src="https://img.shields.io/badge/Mode-Web--First-2f855a?style=flat-square" alt="Web First">
    <img src="https://img.shields.io/badge/Platform-Linux%20%7C%20Windows-3b82f6?style=flat-square" alt="Platform">
  </p>

  <p>
    <a href="./README.md">English</a> |
    <a href="./README.id.md"><strong>Bahasa Indonesia</strong></a>
  </p>

  <p>
    <a href="#ringkasan">Ringkasan</a> •
    <a href="#fitur-utama">Fitur Utama</a> •
    <a href="#workflow-github-code-review">GitHub Code Review</a> •
    <a href="#pratinjau-ui">Pratinjau UI</a> •
    <a href="#autentikasi--sesi">Autentikasi</a> •
    <a href="#variabel-lingkungan">Environment</a> •
    <a href="#instalasi-linux">Instalasi</a>
  </p>
</div>

## Ringkasan

CodexSess adalah gateway akun berbasis web untuk penggunaan Codex/OpenAI.

Dirancang untuk operator yang membutuhkan:
- perpindahan akun yang cepat
- pemisahan akun aktif untuk API dan Codex CLI
- otomasi berbasis usage (alert + auto switch)
- surface API kompatibel OpenAI untuk penggunaan produksi

Untuk penggunaan normal, unduh binary/package dari halaman rilis terbaru:
- https://github.com/rickicode/CodexSess/releases/latest

## Kenapa CodexSess Dibuat

CodexSess dibuat untuk menyederhanakan operasi multi-akun Codex tanpa memecah tool.

Daripada mengelola script terpisah, edit token manual, dan dashboard yang berbeda, CodexSess memusatkan:
- kontrol akun API aktif
- kontrol akun CLI aktif
- visibilitas usage akun
- keputusan fallback otomatis saat limit menipis

## Fitur Utama

- Endpoint proxy kompatibel OpenAI dan Claude:
  - `POST /v1/chat/completions` (termasuk SSE streaming)
  - `GET /v1/models`
  - `POST /v1/responses`
  - `POST /claude/v1/messages`
- Pemisahan status akun aktif:
  - akun API aktif
  - akun CLI (Codex) aktif
- Refresh usage dan otomasi:
  - threshold alert
  - perilaku auto-switch yang bisa dikonfigurasi
- Web console + API proxy tertanam dalam satu binary

## Pratinjau UI

<p align="center">
  <img src="./screenshots/codexsess-dashboard.png" alt="CodexSess Dashboard" width="92%">
</p>

<p align="center">
  <img src="./screenshots/codexsess-settings.png" alt="CodexSess Settings" width="92%">
</p>

<p align="center">
  <img src="./screenshots/codexsess-apilogs.png" alt="CodexSess API Logs" width="92%">
</p>

<p align="center">
  <img src="./screenshots/codexsess-about.png" alt="CodexSess About" width="92%">
</p>

## Autentikasi & Sesi

- Console manajemen membutuhkan login.
- Kredensial default saat first-run:
  - username: `admin`
  - password: `hijilabs`
- Durasi remember session: 30 hari.
- Ganti password via CLI:
  - `--changepassword`

Route kompatibilitas API di `/v1/*` dan `/claude/v1/*` tetap route bergaya API key dan tidak diblok alur login web UI.
Artinya client OpenAI maupun client bergaya Claude sama-sama bisa diarahkan lewat CodexSess.

## Workflow GitHub Code Review

Untuk menggunakan review/autofix PR:

- Gunakan file workflow: `.github/workflows/code-review.yml`
- Tambahkan GitHub repository secret wajib:
  - `CODEXSESS_URL`
  - `CODEXSESS_API_KEY`
- Trigger:
  - otomatis saat event `pull_request`
  - manual lewat `workflow_dispatch`
- Input manual (`workflow_dispatch`):
  - `target_ref` (opsional, branch/tag/sha; default `main`)
  - `review_scope` (`diff` atau `full`)
  - `review_focus` (opsional, area fokus review)

Catatan:
- `review_scope=full` membuat review mencakup keseluruhan repository (tidak tergantung diff commit saja).
- `review_focus` dipakai untuk mengarahkan review manual ke area tertentu (contoh: `auth`, `api`, `performance`, `tests`).
- Run manual akan membuat branch baru otomatis jika ada autofix yang aman untuk di-push.

## Variabel Lingkungan

| Variabel | Default | Contoh | Deskripsi |
|---|---|---|---|
| `PORT` | `3061` | `PORT=8080` | Port HTTP server saat `CODEXSESS_BIND_ADDR` tidak di-set. |
| `CODEXSESS_BIND_ADDR` | `0.0.0.0:<PORT>` | `CODEXSESS_BIND_ADDR=0.0.0.0:3061` | Override bind address penuh (`host:port`) untuk HTTP server. |
| `CODEXSESS_NO_OPEN_BROWSER` | `false` | `CODEXSESS_NO_OPEN_BROWSER=true` | Menonaktifkan auto-open browser saat startup. Nilai truthy: `1`, `true`, `yes`. |
| `CODEXSESS_CODEX_SANDBOX` | `workspace-write` | `CODEXSESS_CODEX_SANDBOX=read-only` | Mode sandbox yang diteruskan ke `codex exec`. |
| `CODEXSESS_CLEAN_EXEC` | `true` | `CODEXSESS_CLEAN_EXEC=false` | Jalankan eksekusi Codex dalam mode isolasi (`true`) atau environment normal (`false`). |

Catatan:
- Di Windows, direktori data default adalah `%APPDATA%\\codexsess` jika `APPDATA` tersedia.
- `CODEX_HOME` diatur internal per akun aktif dan bukan switch runtime eksternal untuk CodexSess.

## Instalasi (Linux)

Gunakan installer dari raw script repository:

```bash
curl -fsSL https://raw.githubusercontent.com/rickicode/CodexSess/main/scripts/install.sh | bash
```

Contoh mode:

```bash
# auto (default)
curl -fsSL https://raw.githubusercontent.com/rickicode/CodexSess/main/scripts/install.sh | bash -s -- --mode auto

# install package GUI (.deb/.rpm)
curl -fsSL https://raw.githubusercontent.com/rickicode/CodexSess/main/scripts/install.sh | bash -s -- --mode gui

# install server / cli
curl -fsSL https://raw.githubusercontent.com/rickicode/CodexSess/main/scripts/install.sh | bash -s -- --mode server

# update tipe instalasi yang sudah ada (auto-detect gui/server)
curl -fsSL https://raw.githubusercontent.com/rickicode/CodexSess/main/scripts/install.sh | bash -s -- --mode update
```

Instalasi Windows:
- Unduh file `.exe` langsung dari:
  - https://github.com/rickicode/CodexSess/releases/latest

## Cakupan Proyek

Fokus CodexSess adalah keandalan operasional untuk penggunaan akun Codex:
- pemilihan akun yang konsisten
- visibilitas status aktif yang jelas
- otomasi usage-aware dan fallback
- surface integrasi kompatibel OpenAI
