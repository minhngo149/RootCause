---
id: cache-stampede
title: Cache Stampede
tags: ["cache", "production-incident"]
---

# Cache Stampede

> Status: Draft

## Problem
Một key cache chứa danh sách sản phẩm trang chủ, TTL 60s, được đọc 2000 req/s. Đúng giây key hết hạn, cả 2000 request cùng nhìn thấy cache miss, và vì logic cache-aside không phối hợp giữa các request, tất cả 2000 request đồng loạt query DB để tính lại cùng một giá trị. Query đó vốn chạy 200ms dưới tải bình thường, nhưng khi 2000 connection cùng lúc tranh chấp CPU và lock trên cùng bảng, latency vọt lên hàng giây, connection pool cạn kiệt, và DB rơi vào trạng thái không đáp ứng — trong khi lẽ ra chỉ cần một query duy nhất là đủ để phục vụ toàn bộ 2000 request đó.

## Pain Points
- DB nhận N lần tải cần thiết (N = số request miss đồng thời) chỉ để tính lại đúng một giá trị, gây CPU spike và lock contention đột biến trên chính bảng mà bình thường vẫn chạy ổn.
- Latency p99 của toàn bộ endpoint (không chỉ endpoint liên quan tới key hết hạn) tăng vọt vì connection pool bị N request kia chiếm hết, các request khác phải xếp hàng chờ connection.
- Sự cố tái diễn theo chu kỳ TTL nếu không xử lý: cứ mỗi 60s lại có một đợt dội tải, tạo pattern sawtooth dễ nhầm với "DB không ổn định" khi nhìn dashboard mà không biết nguyên nhân gốc là cache.
- Ở quy mô lớn hơn, stampede trên một hot key có thể kéo sập luôn DB dùng chung cho các service khác (noisy neighbor), biến một sự cố cache thành outage toàn hệ thống.
- Sau khi DB phục hồi từ một sự cố khác (ví dụ restart, failover), nếu toàn bộ cache cũng bị flush cùng lúc, stampede xảy ra ngay khi DB vừa lên — kéo dài thời gian downtime thay vì rút ngắn nó.

## Solution
Cache stampede (còn gọi là thundering herd hoặc dogpile effect) là hiện tượng nhiều request cùng lúc gặp cache miss trên cùng một key và cùng đi tính lại giá trị từ nguồn (DB, API upstream, computation nặng), thay vì chỉ một request làm việc đó và các request còn lại dùng chung kết quả. Giải pháp cốt lõi là phối hợp (coordination) giữa các request đang miss cùng key: dùng lock để chỉ một request được phép query nguồn, các request khác chờ hoặc dùng giá trị cũ tạm thời; kết hợp với TTL jitter để giảm xác suất nhiều key hết hạn đúng cùng một thời điểm ngay từ đầu. Hai kỹ thuật này giải quyết hai khía cạnh khác nhau của cùng một vấn đề: lock xử lý stampede khi đã xảy ra (nhiều request cùng miss một key tại một thời điểm), còn jitter giảm tần suất trùng hợp khiến nhiều key cùng miss đồng loạt.

## How It Works
**Locking / request coalescing**: khi request đầu tiên phát hiện cache miss, nó cố gắng chiếm một lock (ví dụ `SETNX lock:product:123 1 EX 10` trên Redis). Nếu chiếm được lock, request đó chịu trách nhiệm query DB, ghi kết quả vào cache, rồi release lock (hoặc để lock tự hết hạn). Các request khác miss cùng key trong lúc lock đang bị giữ có hai lựa chọn: (1) poll/chờ (sleep một khoảng ngắn rồi thử đọc lại cache, lặp lại tới khi có giá trị hoặc timeout), hoặc (2) trả về giá trị stale cũ nếu có lưu kèm "stale copy" — đây là kỹ thuật gọi là stale-while-revalidate. Trong một process đơn (single instance), coalescing có thể làm đơn giản hơn bằng in-memory mutex hoặc singleflight (gộp các request trùng key thành một lời gọi thực thi duy nhất, phát lại kết quả cho tất cả caller đang chờ) mà không cần lock phân tán qua Redis.

**TTL jitter**: thay vì set TTL cố định (ví dụ đúng 300s cho mọi key), thêm một khoảng ngẫu nhiên vào TTL tại thời điểm ghi cache, ví dụ `ttl = 300 + random(-30, 30)` giây. Kết quả là các key được ghi cùng một đợt (ví dụ sau khi warm cache toàn bộ hoặc sau một lần deploy) sẽ hết hạn rải rác trong một cửa sổ 60s thay vì cùng một thời điểm chính xác, giảm số request miss đồng thời trên cùng một key xuống mức DB có thể chịu được. Jitter không loại bỏ hoàn toàn khả năng stampede (một key đơn lẻ cực nóng vẫn có thể bị stampede khi nó hết hạn), nó chỉ giảm xác suất nhiều key cộng dồn tải cùng lúc — vì vậy jitter và lock thường đi cùng nhau, không thay thế nhau.

**Early recomputation (probabilistic early expiration)**: một kỹ thuật tinh vi hơn là để mỗi request đọc cache tính một xác suất nhỏ để "coi như miss sớm" ngay cả khi cache còn hạn, dựa trên công thức liên quan tới thời gian còn lại tới khi hết hạn (thuật toán XFetch là ví dụ phổ biến). Request "trúng" xác suất đó sẽ chủ động refresh cache trước khi TTL thật sự hết hạn, trong khi các request khác vẫn đọc giá trị cũ bình thường — nhờ đó giá trị mới được nạp sẵn trước khi hàng loạt request cùng miss.

## Production Architecture
Trong một hệ thống e-commerce, endpoint trang chủ đọc key `homepage:featured-products` với TTL 5 phút và jitter ±30s. Khi key hết hạn, service dùng Redis distributed lock (`SET lock:homepage:featured NX PX 5000`) để đảm bảo chỉ một pod trong cluster Kubernetes (có thể có 20 pod cùng chạy) được query DB; 19 pod còn lại nhận request cùng thời điểm sẽ đọc thấy lock đã bị chiếm và trả về giá trị cache cũ (nếu còn lưu bản stale kèm timestamp) trong khi chờ, hoặc retry đọc cache sau 50-100ms. Đối với các job tính toán nặng hơn (ví dụ dashboard tổng hợp doanh thu chạy 5-10s), hệ thống dùng pattern refresh-ahead: một cron job nội bộ chủ động recompute và ghi lại cache trước khi TTL hết hạn (ví dụ ở giây thứ 240 trong TTL 300s), để không request người dùng nào phải chờ tính toán trực tiếp. Ở tầng hạ tầng, một số team dùng thư viện có sẵn cơ chế singleflight (như package `singleflight` của Go hoặc tương đương trong các framework khác) ngay tại tầng gọi hàm load-data, để coalescing xảy ra tự động mà không cần tự viết logic lock thủ công cho từng endpoint.

## Trade-offs
Locking thêm độ trễ và độ phức tạp: request phải chờ (thêm round-trip tới Redis để thử chiếm lock, cộng thời gian chờ nếu không chiếm được), và nếu request giữ lock bị crash hoặc timeout mà không release đúng cách, các request khác có thể bị chặn lâu hơn cần thiết cho tới khi lock tự hết hạn (TTL của lock cần ngắn hơn nhiều so với thời gian tính toán thực tế, nhưng đủ dài để không hết hạn giữa chừng khi query đang chạy). Trả giá trị stale trong lúc chờ lock đánh đổi tính nhất quán để lấy độ sẵn sàng — chấp nhận được cho danh sách sản phẩm nổi bật, nhưng không chấp nhận được cho số dư tài khoản hay trạng thái đơn hàng. TTL jitter làm giảm khả năng dự đoán chính xác thời điểm một key hết hạn, gây khó khăn hơn một chút khi debug hoặc khi cần đồng bộ chủ động việc invalidate nhiều key liên quan. Early recomputation (XFetch) tốn thêm tải tính toán rải đều theo thời gian thay vì dồn vào một thời điểm — đánh đổi hợp lý nhưng nghĩa là DB luôn có một lượng nhỏ traffic "refresh chủ động" thay vì hoàn toàn im lặng giữa các TTL.

## Best Practices
- Dùng lock hoặc singleflight cho mọi key có traffic đọc cao — số request đồng thời tại thời điểm miss chính là hệ số nhân tải lên DB nếu không coalescing.
- Kết hợp jitter (±10-20% TTL) cho mọi key được ghi hàng loạt cùng lúc (sau warm cache, sau deploy, sau restart Redis) để tránh hiệu ứng dội tải cộng dồn.
- Lưu kèm một bản "stale copy" có thời hạn dài hơn TTL chính, để request đang chờ lock có giá trị trả về ngay thay vì phải chờ hoặc trả lỗi.
- Đặt TTL cho lock ngắn hơn timeout của query nguồn nhưng đủ dài để không hết hạn giữa chừng, và luôn release lock trong khối `finally`/`defer` để tránh deadlock khi có exception.
- Với các phép tính rất nặng (dashboard, báo cáo), ưu tiên refresh-ahead chủ động (cron/scheduled job) thay vì để cache tự miss rồi mới tính lại theo yêu cầu người dùng.

## Common Mistakes
- Thêm TTL jitter nhưng không dùng lock, nghĩ rằng jitter đã đủ — jitter chỉ giảm xác suất trùng hợp giữa nhiều key, không ngăn được stampede khi một key đơn lẻ cực nóng hết hạn.
- Dùng lock nhưng không set TTL cho chính lock đó, dẫn tới nếu process giữ lock bị crash, toàn bộ các request khác bị treo vô thời hạn chờ lock được release.
- Warm toàn bộ cache bằng một job chạy set TTL giống hệt nhau cho hàng nghìn key, vô tình tạo ra chính thời điểm expire đồng loạt mà stampede protection lẽ ra phải tránh.
- Chỉ test cache logic ở tải thấp (1 request tại một thời điểm), không bao giờ test kịch bản N request đồng thời miss cùng key, nên bug thiếu coalescing chỉ lộ ra khi lên production.
- Áp dụng lock/coalescing cho mọi key kể cả những key traffic thấp, gây thêm độ trễ và độ phức tạp không cần thiết ở nơi rủi ro stampede gần như bằng không.

## Interview Questions
**Hỏi**: Cache stampede khác gì với cache miss thông thường?
**Trả lời**: Cache miss thông thường là một request không tìm thấy giá trị trong cache và phải đọc từ nguồn — bình thường và không có vấn đề gì. Cache stampede là trường hợp đặc biệt khi nhiều request cùng miss trên cùng một key tại cùng một thời điểm (thường do TTL hết hạn đồng loạt hoặc key mới bị flush), khiến nguồn dữ liệu nhận N lần tải trùng lặp thay vì một lần.

**Hỏi**: Tại sao chỉ dùng TTL jitter mà không dùng lock vẫn có thể bị stampede?
**Trả lời**: Vì jitter chỉ phân tán thời điểm hết hạn giữa nhiều key khác nhau, giảm khả năng chúng cộng dồn tải cùng lúc; nhưng một key đơn lẻ, dù có jitter hay không, vẫn chỉ có một thời điểm hết hạn duy nhất, và tại đúng thời điểm đó mọi request đang đọc key này vẫn có thể cùng miss nếu traffic đủ cao — jitter không phối hợp giữa các request cùng miss một key, chỉ lock/coalescing mới làm được việc đó.

**Hỏi**: Stale-while-revalidate hoạt động thế nào và tại sao nó giúp giảm latency so với việc bắt request chờ lock?
**Trả lời**: Stale-while-revalidate lưu thêm một bản sao giá trị cũ có thời hạn dài hơn TTL chính; khi cache miss và một request khác đang giữ lock để tính lại giá trị mới, các request còn lại trả ngay bản stale đó cho client thay vì block chờ, giữ latency thấp và ổn định; đánh đổi là client có thể nhận dữ liệu cũ trong một khoảng thời gian ngắn cho tới khi bản mới sẵn sàng.

## Summary
Cache stampede xảy ra khi nhiều request cùng lúc miss chung một cache key và cùng dội tải xuống nguồn dữ liệu để tính lại đúng một giá trị, biến một lần tính toán cần thiết thành N lần lãng phí. Nguyên nhân gốc thường là TTL hết hạn đồng loạt trên các key được ghi cùng lúc, hoặc một hot key đơn lẻ có traffic đủ cao để tự gây stampede khi hết hạn. Giải pháp gồm hai lớp bổ sung nhau: lock/singleflight để chỉ một request tính lại giá trị tại một thời điểm, và TTL jitter để giảm khả năng nhiều key cộng dồn hết hạn cùng lúc ngay từ đầu. Production system cần thêm stale-while-revalidate hoặc refresh-ahead để giảm latency chờ đợi và tránh tính toán nặng chạy đồng bộ trong request path. Hiểu đúng sự khác biệt giữa "giảm xác suất trùng hợp" (jitter) và "phối hợp khi đã trùng hợp" (lock) là điều quyết định một cache layer có thực sự chống được stampede hay chỉ trông có vẻ vậy.

## Knowledge Graph
- Cache Aside — pattern nền cung cấp cơ chế đọc/ghi cache mà cache stampede là rủi ro vận hành phát sinh trực tiếp từ nó.
- Distributed Lock — công cụ chính để hiện thực request coalescing, đảm bảo chỉ một request tính lại giá trị khi cache miss.
- TTL (Time To Live) — tham số cấu hình mà jitter tác động vào để phân tán thời điểm hết hạn giữa các key.
- Thundering Herd — tên gọi khác/khái niệm tổng quát hơn, áp dụng cho mọi hệ thống có nhiều tiến trình cùng phản ứng với một sự kiện (không riêng cache).
- Circuit Breaker — cơ chế bảo vệ bổ sung ở tầng gọi DB, hữu ích khi stampede vẫn xảy ra và DB cần được bảo vệ khỏi quá tải kéo dài.
- Backpressure — kỹ thuật liên quan để giới hạn số request đồng thời được phép đi tới nguồn dữ liệu, bổ trợ cho lock khi traffic vượt ngưỡng chịu đựng.

## Five Things To Remember
- Cache stampede là N request cùng miss một key và cùng tính lại giá trị đáng lẽ chỉ cần tính một lần.
- Lock/singleflight giải quyết việc phối hợp giữa các request đang miss cùng key tại cùng thời điểm.
- TTL jitter giảm xác suất nhiều key khác nhau cộng dồn hết hạn cùng lúc, không thay thế được lock.
- Stale-while-revalidate cho phép trả dữ liệu cũ tạm thời để tránh block request trong lúc chờ giá trị mới.
- Luôn set TTL ngắn cho chính lock để tránh deadlock khi tiến trình giữ lock bị crash.
