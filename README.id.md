# CodexSess

[English](./README.md) | [Bahasa Indonesia](./README.id.md)

CodexSess adalah gateway manajemen akun berbasis web untuk penggunaan Codex/OpenAI.
Project ini menyediakan API kompatibel OpenAI dan console manajemen bawaan dalam satu binary.

## Apa Itu CodexSess

CodexSess dirancang sebagai lapisan antara client dan token akses Codex/OpenAI.
Tujuannya agar kamu bisa mengelola banyak akun, menentukan akun aktif untuk API dan CLI, memantau usage, dan berpindah akun dengan cepat saat limit menipis.

## Tujuan Dibuatnya Proyek Ini

CodexSess dibuat untuk:
- menyederhanakan manajemen multi-akun Codex dalam satu tempat
- memisahkan pemilihan akun aktif untuk API dan Codex CLI
- mengurangi downtime dengan perpindahan akun yang cepat
- menyediakan web console yang praktis tanpa setup yang rumit
- tetap berjalan dalam mode web di Linux/Windows dengan alur yang sama

## Fitur Utama

- Endpoint kompatibel OpenAI:
  - `POST /v1/chat/completions` (termasuk SSE streaming)
  - `GET /v1/models`
  - `POST /v1/responses`
- UI manajemen multi-akun
- Logika perpindahan akun manual dan otomatis
- Integrasi refresh/monitoring usage
- Deployment satu binary dengan SPA tertanam

## Otentikasi dan Sesi

- Login console manajemen menggunakan kredensial admin lokal
- Kredensial default saat pertama kali dijalankan:
  - username: `admin`
  - password: `hijilabs`
- Sesi login diingat 30 hari (cookie)
- Password bisa diganti lewat CLI:
  - `--changepassword`

## Cakupan Kompatibilitas API

- Route manajemen dilindungi login web
- Route kompatibilitas API pada `/v1/*` dan `/claude/v1/*` tetap memakai gaya API key

## Cara Mendapatkan

Untuk penggunaan normal, tidak perlu build manual.
Gunakan binary terbaru dari GitHub Releases:

- Rilis terbaru: https://github.com/rickicode/CodexSess/releases/latest

## Fokus Proyek

CodexSess berfokus pada keandalan operasional untuk penggunaan Codex berbasis akun:
kontrol status aktif yang jelas, perpindahan berbasis usage, dan perilaku API yang konsisten.
