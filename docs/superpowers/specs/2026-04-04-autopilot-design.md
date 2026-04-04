# Autopilot Sibling Thread Design

## Goal

Menambahkan mode `autopilot` ke `/chat` sehingga setelah `brainstorming` dan `writing-plans` selesai serta disetujui user, thread utama bisa berkonsultasi ke thread sibling yang persisten untuk menjawab ambiguity implementasi kecil dan keputusan desain kecil yang masih berada di dalam plan.

## Scope

Fitur ini hanya berlaku untuk fase eksekusi setelah plan tersedia. Autopilot tidak aktif pada fase eksplorasi, tidak membuat spec, tidak membuat plan, dan tidak menjadi executor utama.

## Core Model

Setiap `/chat` session tetap memiliki satu thread utama yang canonical dan user-facing. Saat autopilot diaktifkan, session itu memperoleh satu thread tambahan bernama `autopilot thread`.

Thread utama:
- menerima instruksi user
- tetap menjadi executor utama
- tetap menjalankan tools, edit file, test, dan commit
- tetap menjadi sumber kebenaran timeline kerja

Thread autopilot:
- persisten, tersimpan seperti thread Codex biasa
- sibling dari thread utama
- hanya aktif saat `autopilot_enabled = true`
- visible di UI lewat tab khusus
- berfungsi sebagai bounded advisor / bounded decider

## Activation Rules

Autopilot hanya boleh aktif jika seluruh syarat berikut terpenuhi:
- spec sudah approved
- implementation plan sudah approved
- user menyalakan toggle `Autopilot`

Tanpa `writing-plans`, autopilot tidak boleh aktif.

## Modes

`autopilot_mode` hanya memiliki tiga nilai:
- `off`
- `conservative`
- `normal`

Default saat user pertama kali mengaktifkan autopilot adalah `conservative`.

Perubahan mode selalu manual oleh user. Sistem tidak boleh menaikkan mode otomatis.

Makna mode:
- `off`: autopilot thread tidak dipakai untuk konsultasi
- `conservative`: autopilot boleh memberi jawaban, tapi parent codex selalu review hasilnya sebelum keputusan dipakai
- `normal`: autopilot boleh langsung dipakai untuk decision class yang lolos policy

## Policy Source Of Truth

Policy autopilot diturunkan dari tiga sumber:
- approved spec
- approved implementation plan
- session settings

Autopilot policy harus disimpan sebagai snapshot persisten saat autopilot diaktifkan, agar keputusan selama satu fase eksekusi tidak bergantung pada inferensi ulang yang berubah-ubah.

Minimum contents:
- reference ke spec yang disetujui
- reference ke plan yang disetujui
- structured plan snapshot untuk task aktif
- allowed decision classes
- forbidden decision classes
- current mode (`conservative` atau `normal`)

## Allowed Decisions

Autopilot boleh:
- menjawab pertanyaan operasional kecil
- menjawab klarifikasi implementasi kecil
- mengambil keputusan desain kecil selama masih di dalam plan

Contoh:
- memilih helper name
- memilih urutan task kecil
- memilih struktur test lokal
- memilih opsi implementasi kecil yang semuanya masih valid menurut plan

Autopilot tidak boleh:
- mengubah scope
- mengubah acceptance criteria
- menambah subsystem baru
- mengganti arsitektur besar
- mengubah policy security/sandbox/permission yang signifikan
- mengabaikan spec atau plan yang sudah disetujui

Jika keputusan berada di luar policy, thread utama harus escalate ke user.

## Runtime Flow

Flow eksekusi:
1. main thread sedang menjalankan implementasi
2. main thread menemui ambiguity kecil yang masuk decision class autopilot
3. main thread membuat `autopilot consultation`
4. main thread pause sinkron dan masuk state `waiting_for_autopilot`
5. main thread mengirim context packet ke autopilot thread
6. autopilot thread menjawab
7. jika mode `conservative`, parent codex review dulu
8. jika mode `normal`, parent codex boleh langsung memakai hasil yang lolos policy
9. keputusan yang dipakai dicatat di timeline utama
10. main thread lanjut mengeksekusi

Autopilot bukan executor kedua. Ia boleh memutuskan dalam batas policy, tetapi tindakan nyata tetap dilakukan oleh thread utama.

## Context Model

Autopilot tidak boleh membaca seluruh history thread utama secara bebas sebagai sumber kebenaran utama. Desain yang dipakai adalah:
- persistent autopilot thread untuk kontinuitas
- explicit context packet per invocation untuk relevansi

Context packet minimum:
- session id
- main thread id
- autopilot thread id
- current task
- current ambiguity
- approved spec summary
- approved plan summary
- relevant plan excerpt
- recent relevant messages
- workspace/workdir
- model/reasoning/sandbox
- autopilot policy snapshot

Pendekatan ini lebih aman dan audit-able daripada shared mutable memory bebas.

## UI Design

UI tetap menampilkan satu session utama. Saat autopilot aktif, session utama menampilkan tab tambahan:
- `Main Thread`
- `Autopilot Thread`

Autopilot thread terlihat dan bisa dibuka oleh user.

Feedback user-facing:
- status line menampilkan `Waiting for Autopilot` saat konsultasi aktif
- timeline utama menerima activity bubble audit
- audit bubble berisi ringkasan keputusan + alasan singkat + pointer ke message autopilot asal

Contoh audit bubble:
- `Autopilot decided to keep the existing message merge contract and add a helper module instead. Reason: stays within the approved plan scope. Source: Autopilot #12`

## Persistence

Tambahan state minimum per session:
- `autopilot_thread_id`
- `autopilot_enabled`
- `autopilot_mode`
- `autopilot_policy_snapshot`
- `approved_spec_ref`
- `approved_plan_ref`

Tidak diperlukan MCP khusus pada fase awal. Autopilot sharing context cukup melalui backend internal CodexSess dengan structured context packet.

## Error Handling

Jika autopilot consultation gagal:
- thread utama harus menulis audit bubble kegagalan
- status line keluar dari `Waiting for Autopilot`
- decision harus fallback ke parent codex atau escalate ke user

Jika autopilot thread hilang/korup:
- backend membuat ulang sibling thread hanya setelah user tetap mempertahankan autopilot enabled
- audit bubble harus mencatat bahwa thread autopilot di-bootstrap ulang

## Testing

Minimum acceptance testing:
- autopilot tidak bisa aktif sebelum plan approved
- toggling autopilot membuat sibling thread persisten
- conservative mode menahan keputusan sampai parent review
- normal mode langsung memakai keputusan yang lolos policy
- status line menampilkan waiting state saat consultation aktif
- audit bubble ditulis ke timeline utama dengan pointer ke message autopilot
- main thread tetap executor utama
- decision di luar plan selalu naik ke user

## Rollout

Phase 1:
- implementasikan `conservative` dan `normal`
- default activation `conservative`
- logging/audit wajib

Phase 2:
- tambah analytics sederhana untuk decision classes
- evaluasi apakah beberapa decision classes terlalu agresif untuk `normal`

## Non-Goals

Tidak termasuk dalam desain ini:
- autopilot pre-plan mode
- autopilot membuat spec atau plan
- autopilot sebagai executor paralel kedua
- MCP khusus untuk memory sharing
- shared mutable memory global lintas thread
