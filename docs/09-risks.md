# Risk Assessment — RootCause

Bổ sung cho `rootcause_lean_blueprint_v2.md`, dựa trên research thực tế (không phải suy đoán) về thị trường, kỹ thuật, và tính bền vững OSS. Mục tiêu: liệt kê rủi ro cụ thể + hành động giảm thiểu, để đưa quyết định vào blueprint trước khi build.

---

## 1. Rủi ro về tên & thương hiệu (mới, nghiêm trọng — nên xử lý TRƯỚC KHI code)

- **"RootCause" đã bị chiếm bởi 1 startup có vốn đầu tư**: [rootcause.ai](https://rootcause.ai) (còn gọi "Perceptura") — SaaS causal-AI/BI, được rót vốn bởi Cloudberry Ventures & Plug and Play, có đội ngũ (~16 người), đang chủ động marketing dưới đúng tên "RootCause". Đây là va chạm thật, không phải lý thuyết — sẽ mất SEO, có rủi ro tranh chấp nhãn hiệu nếu dự án lớn lên.
- **Trùng tên trên GitHub**: [github.com/yindia/rootcause](https://github.com/yindia/rootcause) — một MCP server OSS (Go) chẩn đoán sự cố/Kubernetes bằng ngôn ngữ tự nhiên, đang active. Khác domain (K8s ops vs SQL) nhưng ai search "rootcause github" sẽ va phải cả hai.
- **Cụm từ "root cause analysis" đã bị các ông lớn observability chiếm dụng làm feature marketing**: Datadog, New Relic, Dynatrace, Honeycomb, và riêng Causely.io (causal-AI cho SRE) đều dùng cụm này. Không sở hữu được search intent cho "root cause".

**Khuyến nghị**: cân nhắc đổi tên trước khi xây community, hoặc nếu giữ tên, chấp nhận rõ ràng sẽ không rank được cho các từ khóa "root cause" chung và phải differentiate bằng tên miền/kênh riêng (vd. gắn chặt với SQL/DB ngay trong tên).

---

## 2. Rủi ro cạnh tranh — thị trường không trống như blueprint giả định

Không có tool nào hiện tại làm đúng 100% những gì RootCause đề xuất, nhưng **từng mảnh giá trị đã bị các tool trưởng thành hơn phủ**:

| Mảnh giá trị RootCause đề xuất | Ai đã làm, mức độ trưởng thành |
|---|---|
| Rule DSL bằng YAML, plugin-based, community mở rộng | **Bytebase** (OSS, Apache-2.0) — đã có 100+ rule YAML, 20+ engine, SQL review advisor. Đây là overlap kiến trúc gần nhất và active. |
| "Dạy WHY" khi phân tích EXPLAIN, không chỉ báo lỗi | **pgMustard** (trả phí) đã làm chính xác điều này cho Postgres — chấm điểm optimizability + giải thích kèm bài viết liên kết. |
| AI-powered root cause cho slow query | **Datadog Database Monitoring** vừa thêm "AI-powered root cause + auto recommendation" — một incumbent lớn đang đi vào đúng ngách này. |
| Visualize/giải thích EXPLAIN plan miễn phí | explain.dalibo.com, explain.depesz.com — free, đã hoạt động nhiều năm. |
| Nội dung "dạy tại sao" (ACID, index, N+1...) | use-the-index-luke.com, roadmap.sh, sách High Performance MySQL — miễn phí, SEO mạnh, được trích dẫn rộng rãi. |
| AI copilot gắn thẳng vào DB | Supabase AI Assistant, Neon/PlanetScale — nền tảng lớn tự tích hợp AI vào chính sản phẩm DB, giảm nhu cầu dùng tool ngoài. |

**Kết luận quan trọng**: Knowledge base tự nó **không phải là moat** vì nội dung dạng "giải thích ACID/index/deadlock" đã được cover miễn phí, SEO tốt, ở nơi khác. Cái thực sự khác biệt (nếu có) là: **kiến thức được nối trực tiếp vào kết quả rule trong workflow CLI của chính dev**, không phải nội dung tự thân. Nên viết lại phần "Moat" trong blueprint để nhấn đúng chỗ này, tránh overclaim.

- **Cảnh báo bổ sung**: OtterTune (ML-based DB auto-tuning startup) đã đóng cửa năm 2026 — một tín hiệu rằng monetize "công cụ tối ưu DB" không dễ ngay cả khi có công nghệ tốt và vốn đầu tư.

---

## 3. Rủi ro kỹ thuật / kiến trúc

### 3.1. Phân mảnh dialect EXPLAIN — nghiêm trọng hơn blueprint thể hiện

EXPLAIN **không hề chuẩn hóa** giữa các DB: Postgres/MySQL dùng `EXPLAIN` (nhiều format: text/JSON/XML/YAML), Oracle cần `EXPLAIN PLAN FOR` + `DBMS_XPLAN.DISPLAY`, SQL Server dùng XML/graphical showplan riêng. Cost model cũng khác (Postgres có startup+total cost; Oracle chỉ total cost). Không có schema trung lập nào.

- **Postgres**: có `pg_query_go` (wrap parser C thật của Postgres qua cgo) — độ chính xác cao nhất nhưng ít người dùng (~60 sao GitHub), nghĩa là ít edge-case đã được cộng đồng test.
- **MySQL**: `vitess/go/vt/sqlparser` — trưởng thành, production-hardened (chạy trong PlanetScale/Vitess), nhưng chỉ hiểu dialect MySQL.
- **Cross-dialect**: không có thư viện Go nào tương đương. Gần nhất là `sqlglot` (Python, 30+ dialect) — dùng từ Go nghĩa là phải bridge qua FFI/subprocess, thêm 1 thành phần vận hành.
- **Oracle/SQL Server**: chỉ có grammar ANTLR cộng đồng, không chính thức, bảo trì thất thường.

**Ước lượng thực tế**: làm tốt riêng Postgres = vài tuần công sức nhóm nhỏ (khả thi). Thêm MySQL = một tích hợp gần như tách biệt hoàn toàn (parser khác, schema plan JSON khác). Oracle/SQL Server = mỗi cái là dự án nhiều tháng riêng, **không phải "mở rộng thêm" như blueprint ngụ ý** — cần cập nhật roadmap để phản ánh đúng chi phí này, tránh cam kết "multi-dialect" quá sớm.

### 3.2. Rule engine false positive — rủi ro mất niềm tin, có pattern xử lý đã biết

- Ngưỡng chấp nhận của dev: ~90% chấp nhận false-positive rate ≤5%; chỉ 24% chịu được ≥20%.
- Case study Google Tricorder: một linter HTML noise cao, low signal — bị **tắt hẳn** thay vì được tinh chỉnh, vì niềm tin không phục hồi được sau khi mất.
- Semgrep có pattern hay: rule mới ship ở chế độ "monitoring" (chỉ log, không block), chỉ promote lên blocking sau khi đo được precision thật trên dữ liệu thực. **Nên áp dụng pattern này cho 20 SQL Rules đầu tiên** thay vì để tất cả block/flag ngay từ v1.

### 3.3. "AI Provider Agnostic" — dễ nói khó làm đúng

- Behavior drift: prompt tối ưu cho 1 model (format, instruction-following) thường giảm chất lượng khi đổi sang model khác — cần eval suite riêng theo từng provider, không chỉ đổi interface là xong.
- Cost/latency: cloud API cộng thêm ~300ms+ network overhead so với Ollama local gần như tức thời; nhưng self-host có chi phí cố định (~$126–233/tháng hạ tầng) chỉ hoà vốn khi lưu lượng đủ lớn. Pattern thực dụng 2026: hybrid (local baseline + cloud overflow).
- **Privacy** — rủi ro thực chất nhất, blueprint hoàn toàn chưa đề cập: gửi SQL/schema/log production thật lên API bên thứ ba có nguy cơ bị log/train lại, và có thể vi phạm GDPR-class data nếu log chứa PII. Cần: redact/tokenize dữ liệu trước khi đưa vào prompt, và mặc định ưu tiên local/self-hosted inference cho input nhạy cảm.

---

## 4. Rủi ro bền vững OSS (thường bị bỏ qua ở giai đoạn ý tưởng)

- **Maintainer burnout là nguyên nhân bỏ dự án phổ biến nhất**: khảo sát gần đây cho thấy burnout ảnh hưởng 44% maintainer, 61% maintainer không lương làm một mình — nghĩa là dự án solo/small-team không có redundancy. Thành công còn làm nặng thêm: nhiều user hơn → nhiều issue/feature request hơn → burnout nhanh hơn.
- **Single point of failure có tiền lệ nghiêm trọng**: "succession deadlock" (chỉ 1 người có quyền publish) là chính xác điều kiện đã dẫn tới vụ backdoor XZ Utils (2022) — maintainer kiệt sức bị kẻ xấu lợi dụng.
- **"Build it and they will come" là ngộ nhận đã được ghi nhận nhiều lần**: ví dụ `ftrace` của Linux — kỹ thuật tốt, code tốt, docs tốt, nhưng gần như không ai biết đến (2014) vì không có chiến lược phân phối/positioning. Roadmap hiện tại của RootCause dừng ở "Sprint 7: GitHub Release" — **không có sprint nào cho distribution/marketing**.
- **Nội dung Knowledge Base là gánh nặng biên tập liên tục, không phải deliverable một lần**:
  - Research về docs OSS chỉ ra 3 vấn đề phổ biến nhất: thiếu nội dung, nội dung sai, nội dung lỗi thời — vì maintainer không review định kỳ.
  - Tiền lệ trực tiếp: tính năng "Documentation" của Stack Overflow (2016) — một KB do cộng đồng viết, curated, rất giống ý tưởng `knowledge/` của RootCause — bị đóng cửa sau ~1 năm vì chất lượng/coverage không đều, trùng lặp docs chính thức, và không đạt engagement như kỳ vọng, **dù có sẵn cộng đồng khổng lồ**.
  - Hàm ý: viết 17 → 100+ bài không phải là "Sprint 2: import Markdown" — đó là cam kết biên tập dài hạn, cần chủ sở hữu nội dung, quy trình review, cơ chế phát hiện lỗi thời (EXPLAIN plan có thể đổi hành vi theo version DB).

---

## 5. Rủi ro mô hình kinh doanh dài hạn (Open-core / Cloud)

Blueprint nhắc "Cloud" ở phần Long-term Vision nhưng chưa có ranh giới rõ core-vs-cloud. Tiền lệ cho thấy đây là điểm dễ gây phản ứng ngược nhất nếu làm sai thứ tự:

- MongoDB (SSPL) → bị OSI/Debian/Red Hat/Fedora từ chối, loại khỏi repo chính thức.
- Elastic (license đổi) → AWS fork thành OpenSearch (nay do Linux Foundation quản lý); Elastic sau đó quay lại AGPL năm 2024.
- HashiCorp (BSL) → cộng đồng fork thành OpenTofu.
- Redis (relicense) → fork thành Valkey, được AWS/Google/Oracle hậu thuẫn.
- **Không có bằng chứng các cú đổi license này thực sự tăng trưởng doanh thu tốt hơn** — tăng trưởng MongoDB có từ trước SSPL; Elastic tăng trưởng giảm sau đổi license; HashiCorp cuối cùng bị IBM mua lại thay vì tự lớn mạnh độc lập.
- Gốc rễ phản ứng ngược: cộng đồng adopt OSS với kỳ vọng nó **ở lại** mở mãi mãi — đổi luật chơi sau khi đã có người dùng bị đọc là phản bội, bất kể lý lẽ pháp lý.

**Khuyến nghị**: chốt và công bố công khai ranh giới core-vs-cloud (cái gì mãi mãi OSS, cái gì chỉ có ở bản Cloud) **trước khi** xây cộng đồng, không phải retrofit sau — đây chính là pattern gây ra mọi vụ backlash kể trên.

---

## 6. Tổng hợp hành động đề xuất (ưu tiên trước khi code Sprint 1)

1. **Quyết định lại tên dự án** — hoặc chấp nhận rủi ro SEO/trùng tên với rootcause.ai và github.com/yindia/rootcause.
2. **Viết lại phần "Moat"** trong blueprint: bỏ claim "Knowledge Base tự nó là moat"; thay bằng "tích hợp Knowledge vào rule output trong workflow CLI" là điểm khác biệt thật.
3. **Giới hạn phạm vi kỹ thuật MVP rõ ràng**: chỉ 1 dialect (khuyến nghị Postgres — có `pg_query_go` chất lượng cao nhất) cho tới khi có traction thật; không hứa multi-dialect trong roadmap gần.
4. **Thêm sprint "Rule Validation / Eval Corpus"** trước GitHub Release — dùng pattern "monitoring mode trước, blocking sau" như Semgrep.
5. **Thêm kế hoạch xử lý dữ liệu nhạy cảm** cho AI layer: redact/tokenize trước khi gửi ra ngoài, mặc định local-first cho input production thật.
6. **Thêm sprint/kế hoạch "Distribution"**: không chỉ code — cần nội dung, launch, kênh phân phối (Show HN, dev communities...) trước khi coi Sprint 7 là "xong".
7. **Giải quyết bus-factor**: xác định từ đầu ai có quyền publish/admin ngoài bản thân, kể cả ở giai đoạn solo.
8. **Chốt ranh giới open-core/Cloud công khai** trong docs trước khi kêu gọi contributor đầu tiên, không để ngỏ như hiện tại.
9. **Lập kế hoạch bảo trì nội dung Knowledge Base**: chủ sở hữu theo từng bài, review định kỳ, cơ chế đánh dấu "có thể lỗi thời theo version DB" — coi đây là chi phí vận hành liên tục, không phải sprint một lần.

---

*Nguồn tham khảo chính: pganalyze, pgMustard, Bytebase SQL Review, sqlfluff, Percona pt-query-digest, Datadog Database Monitoring, OtterTune, rootcause.ai, github.com/yindia/rootcause, Causely.io, use-the-index-luke.com, roadmap.sh, pg_query_go, vitess sqlparser, sqlglot, Semgrep case studies, Google Tricorder (abseil.io), Stack Overflow Documentation feature retrospective, Socket.dev maintainer survey, nesbitt.io OSS failure analysis, SoftwareSeni open-source relicensing timeline.*
