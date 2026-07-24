---
id: ttl-eviction-policies
title: TTL & Eviction Policies
tags: ["cache"]
---

# TTL & Eviction Policies

> Status: Draft

## Problem
Một team thêm Redis vào hệ thống order-service để cache kết quả tính giá đơn hàng, nhưng quên set TTL cho các key `pricing:{orderId}`. Sau 3 tuần chạy production, Redis instance 8GB báo `OOM command not allowed when used memory > maxmemory` — dữ liệu cứ tích lũy vô thời hạn vì không có gì tự dọn dẹp. Đội vận hành phải `FLUSHALL` khẩn cấp giữa giờ cao điểm, gây một đợt cache miss hàng loạt làm DB sập theo. Vấn đề không nằm ở việc dùng cache mà ở việc không xác định trước cache sẽ hết hạn khi nào và bị đuổi ra sao khi bộ nhớ đầy.

## Pain Points
- Không có TTL, cache tăng trưởng vô hạn theo số lượng key mới (mỗi user, mỗi order, mỗi session) cho tới khi hết RAM, dẫn tới OOM hoặc engine tự chọn eviction policy mặc định một cách bất ngờ (ví dụ Redis mặc định `noeviction` sẽ từ chối mọi lệnh ghi khi đầy bộ nhớ, gây lỗi 500 dây chuyền).
- Chọn sai eviction policy khiến cache đuổi nhầm dữ liệu nóng: dùng `allkeys-random` cho một workload có hot key rõ ràng sẽ đuổi ngẫu nhiên cả key đang được truy cập liên tục, làm hit ratio sụt không lý do rõ ràng.
- TTL quá dài cho dữ liệu biến động (giá, tồn kho, session) khiến client đọc dữ liệu cũ hàng giờ, gây sai lệch nghiệp vụ (bán hàng với giá đã hết khuyến mãi) chứ không chỉ là vấn đề hiệu năng.
- TTL quá ngắn hoặc đồng loạt cho một lượng lớn key tạo ra chu kỳ expire hàng loạt (mass expiration), gây cache stampede định kỳ và tăng tải DB theo chu kỳ dễ nhầm với sự cố hạ tầng khi debug.

## Solution
TTL (Time To Live) là cơ chế hết hạn theo thời gian: mỗi cache entry được gắn một mốc sống còn, hết thời gian đó entry tự động bị coi là không hợp lệ dù bộ nhớ còn dư. Eviction policy là cơ chế riêng biệt xử lý khi bộ nhớ cache đầy trước khi TTL kịp hết hạn — engine phải chủ động chọn đuổi bớt entry nào đó để có chỗ cho ghi mới, theo một trong các chiến lược: LRU (Least Recently Used — đuổi entry lâu nhất chưa được truy cập), LFU (Least Frequently Used — đuổi entry có tần suất truy cập thấp nhất), hoặc random (đuổi ngẫu nhiên, chi phí tính toán thấp nhất). Hai cơ chế này độc lập nhưng bổ sung cho nhau: TTL giải quyết bài toán "dữ liệu này còn đúng bao lâu", eviction giải quyết bài toán "bộ nhớ hữu hạn thì giữ gì, bỏ gì".

## How It Works
TTL trong Redis được lưu dưới dạng timestamp hết hạn (Unix time, độ chính xác mili giây) đính kèm mỗi key trong metadata, không phải một background timer riêng cho từng key. Việc dọn key hết hạn diễn ra theo 2 cơ chế song song: passive expiration (khi có lệnh `GET`/`SET` chạm vào key, Redis kiểm tra timestamp trước khi trả kết quả, nếu quá hạn thì xóa ngay và trả về nil) và active expiration (một cycle chạy nền 10 lần/giây, mỗi lần lấy mẫu ngẫu nhiên 20 key có TTL, xóa những key đã hết hạn, lặp lại nếu tỷ lệ hết hạn trong mẫu > 25% — đảm bảo key hết hạn nhưng không còn ai đọc tới vẫn được giải phóng bộ nhớ). Khi bộ nhớ chạm `maxmemory`, Redis kích hoạt eviction theo policy đã cấu hình: LRU xấp xỉ (approximated LRU) không duy trì một linked-list truy cập chính xác như LRU lý thuyết (tốn bộ nhớ) mà lấy mẫu N key ngẫu nhiên (mặc định 5), so sánh trường `idle time` (thời gian từ lần truy cập gần nhất) rồi đuổi key idle lâu nhất trong mẫu — mẫu càng lớn thì càng gần LRU chính xác nhưng tốn CPU hơn mỗi lần eviction. LFU dùng bộ đếm tần suất giảm dần theo thời gian (probabilistic counter, 8-bit, có cơ chế "decay" để một key từng hot nhưng nay nguội dần vẫn bị đuổi thay vì giữ điểm cao vĩnh viễn) để tránh trường hợp LRU đuổi nhầm một key truy cập rất thường xuyên nhưng vừa mới có một khoảng nghỉ ngắn. Policy `allkeys-*` áp dụng cho mọi key bất kể có TTL hay không; policy `volatile-*` chỉ xét các key có TTL, key không có TTL được coi là "persistent" và không bao giờ bị eviction chọn (nếu không còn key nào có TTL để đuổi, ghi mới sẽ bị từ chối như `noeviction`).

## Production Architecture
Trong một hệ thống thương mại điện tử dùng Redis làm cache tầng session và pricing: session key (`session:{token}`) đặt TTL 30 phút khớp với thời gian hết hạn JWT, tự động dọn dẹp session cũ mà không cần cron job riêng; pricing key (`pricing:{sku}:{region}`) đặt TTL 60 giây vì giá thay đổi theo campaign gần như real-time. Cụm Redis được cấu hình `maxmemory 12gb` và `maxmemory-policy volatile-lru` — chỉ đuổi các key có TTL (session, pricing) khi đầy bộ nhớ, giữ nguyên các key persistent như cấu hình feature-flag hay rate-limit counter dài hạn không bị đuổi nhầm. Đội SRE theo dõi `evicted_keys` và `expired_keys` qua `INFO stats` trong Prometheus/Grafana: `evicted_keys` tăng đột biến là tín hiệu sớm cho biết cache đang thiếu RAM so với working set thực tế, cần scale instance trước khi hit ratio giảm ảnh hưởng tới DB downstream. Với hệ thống dùng LFU (ví dụ cache recommendation feed nơi một số item cực kỳ hot), `maxmemory-policy allkeys-lfu` được chọn thay vì LRU vì recommendation nóng có thể có khoảng nghỉ ngắn giữa các lượt truy cập mà vẫn cần giữ lại, điều LRU thuần dễ đuổi nhầm.

## Trade-offs
LRU xấp xỉ (sampled) đánh đổi độ chính xác lấy tốc độ: sample size nhỏ (5) rẻ về CPU nhưng có xác suất đuổi nhầm key vẫn còn giá trị nếu key đó không lọt vào mẫu ngẫu nhiên; tăng sample lên 10-20 cải thiện độ chính xác nhưng tăng chi phí CPU mỗi lần eviction, ảnh hưởng latency ở tải cao. LFU phù hợp hơn cho pattern truy cập lệch (hot key rõ rệt) nhưng phức tạp hơn để tune (cần chỉnh `lfu-log-factor` và `lfu-decay-time`) và khó dự đoán hành vi hơn LRU với người vận hành chưa quen. Random eviction rẻ nhất về chi phí tính toán (không cần theo dõi thời gian truy cập hay tần suất) nhưng hoàn toàn không tối ưu hit ratio, chỉ phù hợp khi mọi key có giá trị gần như ngang nhau. TTL ngắn giảm rủi ro stale data nhưng tăng cache miss rate và tải lại DB thường xuyên hơn; TTL dài giảm tải DB nhưng tăng cửa sổ đọc dữ liệu cũ — không có giá trị TTL nào đúng cho mọi loại dữ liệu, phải set riêng theo tốc độ thay đổi thực tế của từng loại key.

## Best Practices
- Luôn set `maxmemory` và một eviction policy tường minh (không dùng mặc định `noeviction` trong production) trừ khi cố ý muốn cache từ chối ghi khi đầy thay vì đuổi dữ liệu.
- Chọn TTL theo tốc độ thay đổi thực tế của dữ liệu, không dùng một giá trị TTL chung cho toàn hệ thống — session, pricing, config cần TTL khác nhau hàng bậc.
- Dùng `volatile-lru`/`volatile-lfu` khi cache có trộn lẫn key persistent (không TTL, không được đuổi) và key tạm thời, để tránh đuổi nhầm dữ liệu quan trọng dài hạn.
- Theo dõi `evicted_keys`, `expired_keys` và hit ratio theo thời gian thực; `evicted_keys` tăng liên tục là dấu hiệu cần tăng RAM hoặc giảm working set, không phải điều chỉnh policy.
- Thêm jitter (±10-20%) vào TTL cho các key được set hàng loạt cùng lúc, tránh mass expiration đồng loạt gây stampede định kỳ.

## Common Mistakes
- Không set TTL cho cache entry vì nghĩ "invalidation logic sẽ luôn đúng" — thực tế luôn có đường ghi bị bỏ sót, dẫn tới cache tăng trưởng vô hạn và OOM.
- Để `maxmemory-policy` ở giá trị mặc định `noeviction` trong production, khiến Redis từ chối mọi lệnh ghi (bao gồm cả `SET` cache mới) khi đầy bộ nhớ thay vì tự dọn dẹp.
- Dùng `allkeys-random` hoặc LRU cho workload có hot key rõ rệt và khoảng nghỉ truy cập, đuổi nhầm dữ liệu nóng thay vì dùng LFU phù hợp hơn.
- Set TTL giống nhau hàng loạt cho một batch dữ liệu (ví dụ warm cache cùng lúc cho 100k sản phẩm với TTL 300s cố định), gây mass expiration đồng loạt sau đúng 300s và cache stampede định kỳ.
- Nhầm lẫn TTL với eviction: cho rằng set TTL dài là đủ để tránh OOM, trong khi TTL dài không giúp gì nếu tổng working set vượt quá RAM trước khi bất kỳ key nào kịp hết hạn.

## Interview Questions
**Hỏi**: TTL và eviction policy khác nhau ở điểm nào, và tại sao một hệ thống cần cả hai?
**Trả lời**: TTL xử lý việc dữ liệu hết hạn theo thời gian dù bộ nhớ còn dư, đảm bảo tính đúng đắn (freshness) của dữ liệu; eviction policy xử lý việc bộ nhớ đầy trước khi TTL kịp hết hạn, đảm bảo cache không OOM. Một hệ thống chỉ có TTL vẫn có thể OOM nếu tốc độ ghi vượt tốc độ hết hạn; chỉ có eviction mà không có TTL thì dữ liệu có thể sống mãi cho tới khi bị đuổi ngẫu nhiên, không kiểm soát được độ tươi của dữ liệu.

**Hỏi**: Redis triển khai "LRU" thực tế như thế nào, và vì sao nó chỉ là xấp xỉ chứ không chính xác tuyệt đối?
**Trả lời**: Redis không duy trì một cấu trúc dữ liệu theo dõi thứ tự truy cập chính xác (như doubly linked list) cho toàn bộ key vì tốn bộ nhớ; thay vào đó mỗi lần cần eviction, nó lấy mẫu ngẫu nhiên N key (mặc định 5), so sánh idle time và đuổi key idle lâu nhất trong mẫu đó. Vì chỉ xét trong mẫu ngẫu nhiên chứ không phải toàn bộ keyspace, key thực sự lâu nhất chưa truy cập có thể không lọt vào mẫu và không bị đuổi — đây là lý do gọi là "approximated LRU".

**Hỏi**: Khi nào nên chọn LFU thay vì LRU cho eviction policy?
**Trả lời**: Nên chọn LFU khi pattern truy cập có hot key rõ rệt nhưng truy cập không liên tục tuyệt đối — ví dụ một sản phẩm bán chạy được xem nhiều lần/phút nhưng có khoảng nghỉ vài phút giữa các đợt traffic. LRU thuần có thể đuổi nhầm key này nếu khoảng nghỉ đó trùng lúc eviction diễn ra, trong khi LFU vẫn giữ điểm tần suất cao cho key đó nhờ bộ đếm tích lũy theo thời gian dài hơn.

## Summary
TTL và eviction policy là hai cơ chế độc lập nhưng bổ sung nhau để quản lý vòng đời dữ liệu trong cache: TTL quyết định dữ liệu còn đúng bao lâu, eviction quyết định giữ gì bỏ gì khi bộ nhớ hữu hạn bị đầy trước khi TTL kịp xử lý. Redis dùng cả passive expiration (kiểm tra khi đọc) và active expiration (cycle nền lấy mẫu) để dọn key hết hạn, còn eviction dùng LRU xấp xỉ, LFU hoặc random tùy `maxmemory-policy` cấu hình. Không có giá trị TTL hay policy nào đúng cho mọi trường hợp — phải chọn theo tốc độ thay đổi dữ liệu và pattern truy cập thực tế của từng loại key. Bỏ qua một trong hai cơ chế (không set TTL, hoặc để `noeviction` mặc định) là nguyên nhân phổ biến của OOM và stale data trong production. Giám sát `evicted_keys`/`expired_keys` là cách sớm nhất để phát hiện cache đang thiếu RAM hoặc TTL cấu hình sai trước khi ảnh hưởng tới downstream.

## Knowledge Graph
- Cache Aside — pattern set TTL tại bước ghi cache, phụ thuộc trực tiếp vào TTL để tự phục hồi khi invalidation bị bỏ sót.
- Cache Stampede / Thundering Herd — hệ quả trực tiếp của mass expiration khi nhiều key có cùng TTL hết hạn đồng loạt.
- Cache Invalidation — cơ chế chủ động xóa cache, hoạt động song song với TTL như một lớp bảo hiểm bị động.
- Redis Memory Management (`maxmemory`) — tham số cấu hình quyết định khi nào eviction được kích hoạt.
- Hot Key Problem — pattern truy cập lệch khiến việc chọn LFU thay vì LRU trở nên quan trọng.
- Distributed Lock — công cụ thường dùng cùng TTL ngắn để tự giải phóng lock nếu client giữ lock bị crash.

## Five Things To Remember
- TTL xử lý "dữ liệu còn đúng bao lâu", eviction xử lý "bộ nhớ đầy thì đuổi gì" — hai cơ chế độc lập, cần cả hai.
- Không set TTL nghĩa là cache tăng trưởng vô hạn cho tới khi OOM hoặc eviction ngẫu nhiên can thiệp.
- Redis LRU là xấp xỉ (sampled), không phải LRU chính xác tuyệt đối theo lý thuyết.
- Dùng LFU khi có hot key với khoảng nghỉ truy cập, dùng LRU khi pattern truy cập đều và tuần tự hơn.
- `maxmemory-policy` mặc định là `noeviction` — luôn cấu hình tường minh trong production để tránh từ chối ghi khi đầy bộ nhớ.
