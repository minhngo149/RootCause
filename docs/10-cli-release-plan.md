# CLI Release Plan — Deploy RootCause CLI lên GitHub (giai đoạn 1)

Mục tiêu giai đoạn này: **chỉ CLI**, chưa VSCode Extension / GitHub Action / Cloud. Ưu tiên để người dùng cài đặt và chạy được `rootcause doctor` thật nhanh, thu phản hồi sớm, chưa cần hoàn thiện 100+ bài Knowledge hay 20 Rule — cần đủ để chứng minh giá trị.

Kế thừa các rủi ro đã ghi ở [09-risks.md](09-risks.md): quyết định tên trước khi public repo, không hứa multi-dialect, có kế hoạch distribution (không chỉ "GitHub Release" là xong), và chốt ranh giới open-core trước khi gọi contributor.

---

## Giai đoạn 0 — Quyết định trước khi tạo repo public (chặn cứng)

- [ ] **Chốt tên cuối cùng.** `rootcause` đã trùng với startup `rootcause.ai` và 1 project GitHub khác. Nếu giữ tên, chấp nhận rebrand sau; nếu đổi, đổi ngay từ đầu (rẻ hơn nhiều so với đổi sau khi có user). Kiểm tra tên GitHub org/repo, PyPI/npm/Homebrew namespace, domain `.dev`/`.sh` còn trống không.
- [ ] **Chọn license.** Khuyến nghị **MIT** hoặc **Apache-2.0** cho core (không BSL/SSPL ở giai đoạn build cộng đồng — xem tiền lệ MongoDB/Elastic/HashiCorp trong risks doc). Ghi rõ trong README: phần nào mãi mãi OSS, phần nào (nếu có Cloud sau này) sẽ không.
- [ ] **Xác định bus factor.** Ít nhất thêm 1 người khác có quyền admin/publish trên GitHub org + npm/Homebrew tap, kể cả khi đang solo — tránh kịch bản "chỉ 1 người giữ chìa khóa" (đã dẫn tới sự cố XZ Utils).
- [ ] **Xác định "đủ tốt để launch" là gì** — khuyến nghị: 1 dialect (Postgres), ≥10 rule thật có ích (không phải rule demo), ≥1 use case đầu-cuối chạy được trên log/EXPLAIN thật. Đừng launch với `doctor` mà không có rule nào hữu ích.

---

## Giai đoạn 1 — Chuẩn hoá repo trước khi bật public

### 1.1 Cấu trúc & file bắt buộc
- [ ] `README.md` — phần quan trọng nhất, quyết định 80% ấn tượng đầu. Cấu trúc: 1 dòng tagline → GIF/asciinema demo 15-30s → lệnh cài đặt copy-paste được ngay → 1 ví dụ input/output thật → link docs.
- [ ] `LICENSE`
- [ ] `CONTRIBUTING.md` — hướng dẫn thêm 1 Rule mới bằng YAML (đây là kênh đóng góp chính, phải cực dễ copy-paste theo mẫu).
- [ ] `CODE_OF_CONDUCT.md`
- [ ] `SECURITY.md` — vì tool sẽ đọc log/SQL người dùng, cần nói rõ cách report lỗ hổng và cam kết không gửi dữ liệu người dùng ra ngoài nếu không có AI layer bật.
- [ ] `.github/ISSUE_TEMPLATE/` — tách 2 loại: **Bug report** và **Rule request / Knowledge request** (khuyến khích đúng loại đóng góp mong muốn).
- [ ] `.github/PULL_REQUEST_TEMPLATE.md`
- [ ] `CHANGELOG.md` theo format [Keep a Changelog](https://keepachangelog.com/).

### 1.2 CI trước khi mở public
- [ ] GitHub Actions: `lint` (golangci-lint) + `test` + `build` chạy trên mọi PR — public repo không có CI xanh ngay từ đầu sẽ mất uy tín với contributor kỹ thuật.
- [ ] Branch protection trên `main`: yêu cầu CI pass trước khi merge.

---

## Giai đoạn 2 — Build & Release pipeline (phân phối binary)

Đây là phần blueprint hiện tại **chưa có kế hoạch cụ thể** — "Sprint 7: GitHub Release" quá mơ hồ để thực thi.

### 2.1 Công cụ
- [ ] Dùng **[GoReleaser](https://goreleaser.com/)** — chuẩn công nghiệp cho Go CLI OSS, tự động build cross-platform (Linux/macOS/Windows, amd64/arm64), tạo GitHub Release, checksum, và archive.
- [ ] Workflow: push git tag `vX.Y.Z` → GitHub Actions trigger `goreleaser release` → tự sinh binary + release notes từ CHANGELOG.

### 2.2 Kênh cài đặt — ưu tiên theo độ dễ cho user (làm dần, không cần đủ ngay v1)
1. **`go install github.com/<org>/rootcause@latest`** — kênh rẻ nhất, làm ngay từ v1 (chỉ cần `go.mod` đúng chuẩn).
2. **Install script**: `curl -sSL https://.../install.sh | sh` — tải binary đúng OS/arch từ GitHub Release. Cần thiết vì nhiều user không có Go toolchain.
3. **Homebrew tap** (`brew install <org>/tap/rootcause`) — GoReleaser hỗ trợ tự động generate Formula, publish sang 1 repo tap riêng (`homebrew-tap`). Ưu tiên cao vì phổ biến với dev macOS/Linux.
4. **Scoop (Windows)** — làm sau khi có traction, GoReleaser cũng hỗ trợ.
5. **Docker image** (`docker run rootcause doctor ...`) — hữu ích cho CI/CD sau này (gắn với GitHub Action ở giai đoạn 2 của roadmap), chưa cần v1.
6. **apt/deb, Nix, AUR** — để cộng đồng tự đóng góp package sau, không tự làm giai đoạn 1.

### 2.3 Versioning
- [ ] Semver nghiêm ngặt. Trước khi đủ ổn định, dùng `v0.x.y` để báo hiệu breaking change có thể xảy ra — tránh cam kết API sớm khi rule schema còn có thể đổi.

---

## Giai đoạn 3 — Trải nghiệm người dùng lần đầu (First-run UX)

- [ ] `rootcause --help` và `rootcause <command> --help` phải tự giải thích được, không cần đọc docs.
- [ ] Lệnh đầu tiên user gõ nên có phản hồi hữu ích **ngay cả khi không có input thật** — vd. `rootcause doctor` không có file → gợi ý `rootcause doctor --help` hoặc thử với example có sẵn (`rootcause doctor --example slow-query`).
- [ ] Output nên có màu sắc/format rõ ràng (dùng lipgloss/termenv) phân biệt: Finding → Rule → Knowledge link → khuyến nghị. Đây chính là chỗ thể hiện triết lý "Explain WHY", nên đầu tư UX output kỹ hơn các phần khác.
- [ ] Cân nhắc `rootcause version` in kèm link changelog/update check (không tự động phone-home nếu chưa có chính sách privacy rõ ràng).

---

## Giai đoạn 4 — Launch / Distribution (phần blueprint đang thiếu hoàn toàn)

Rủi ro đã ghi trong risks doc: dự án tốt vẫn có thể chết vì không ai biết tới ("ftrace" case). Cần kế hoạch phân phối tách biệt khỏi việc code xong.

- [ ] **Thời điểm launch**: chỉ launch rộng khi đã có ≥1 ví dụ "wow" thật — vd. 1 câu chuyện ngắn "SELECT * gây ra full table scan → RootCause phát hiện + giải thích tại sao + link tới covering-index.md".
- [ ] **Kênh launch** (chọn 2-3, không dàn trải hết):
  - Show HN (Hacker News) — phù hợp với dev tool kỹ thuật sâu.
  - Reddit: r/programming, r/PostgreSQL, r/golang, r/devops.
  - Cộng đồng Việt Nam: nơi user đang hoạt động (Discord/Zalo/Facebook group Go/Backend VN) — lợi thế ngôn ngữ.
  - Dev.to / blog cá nhân: 1 bài viết kỹ thuật kể câu chuyện thật ("Tôi tìm ra full table scan production như thế nào"), không phải bài PR sản phẩm.
- [ ] **1 bài blog launch** thay vì chỉ đăng release note — nội dung nên chứng minh triết lý "teach WHY" bằng ví dụ cụ thể, không liệt kê feature.
- [ ] Bật **GitHub Discussions** để hỏi-đáp, tách khỏi Issues (Issues chỉ dành cho bug/feature).

---

## Giai đoạn 5 — Feedback loop & vận hành sau launch

- [ ] Quy trình triage issue hàng tuần (dù ít issue, giữ thói quen từ đầu — tránh backlog chết dần, nguyên nhân phổ biến khiến OSS project bị đánh giá "đã chết").
- [ ] Nếu cân nhắc thêm usage telemetry: phải **opt-in rõ ràng**, chỉ đếm lệnh được gọi (không gửi nội dung SQL/log), và công bố công khai thu thập gì — nhất quán với lo ngại privacy đã nêu ở phần AI layer trong risks doc.
- [ ] Định kỳ release nhỏ đều đặn (vd. 2 tuần/lần) tốt hơn release lớn không đều — giữ tín hiệu "dự án còn sống" với người mới ghé repo.

---

## Checklist rút gọn theo thứ tự thực thi

1. Chốt tên + license + bus factor (Giai đoạn 0)
2. Chuẩn hoá repo: README, templates, CI xanh (Giai đoạn 1)
3. GoReleaser + `go install` + install script (Giai đoạn 2, tối thiểu để release v0.1.0)
4. Polish CLI first-run UX (Giai đoạn 3)
5. Chuẩn bị 1 ví dụ "wow" thật + bài blog + chọn 2-3 kênh (Giai đoạn 4)
6. Launch v0.1.0
7. Thiết lập nhịp triage + release đều đặn (Giai đoạn 5)

Homebrew tap, Docker image, Scoop, apt/deb: làm sau khi có tín hiệu traction thật từ bước 6, không cần đủ ngay từ v1.
