---
id: cache-invalidation
title: Cache Invalidation
tags: ["cache"]
---

# Cache Invalidation

> Status: Draft

## Problem
Một hệ thống thương mại điện tử cache thông tin sản phẩm trong Redis với TTL 10 phút để giảm tải PostgreSQL. Đội vận hành cập nhật giá khuyến mãi cho một sản phẩm đang lên trang chủ, transaction DB commit thành công, nhưng 8 phút sau khách hàng vẫn thấy giá cũ vì service ghi vào DB rồi... quên xóa cache key tương ứng. Tệ hơn, hệ thống chạy 12 instance API đứng sau load balancer, một instance nhận webhook cập nhật giá và xóa cache local của chính nó, trong khi 11 instance còn lại vẫn phục vụ dữ liệu cũ từ cache riêng cho tới khi TTL hết hạn. Đây chính là lý do Phil Karlton nói "there are only two hard things in computer science: cache invalidation and naming things" — biết khi nào và làm sao xóa đúng dữ liệu cũ, trên đúng tất cả các node, khó hơn nhiều so với việc chỉ ghi dữ liệu vào cache.

## Pain Points
- Dữ liệu hiển thị sai cho khách hàng (giá, tồn kho, trạng thái đơn hàng) trong khoảng thời gian không xác định trước, gây mất niềm tin và trong trường hợp giá/tồn kho còn dẫn tới tranh chấp đơn hàng hoặc thất thoát tài chính.
- Invalidation rải rác thủ công tại nhiều điểm ghi dữ liệu (API, batch job, admin panel, migration script) khiến chỉ cần bỏ sót một đường ghi là cache stale vĩnh viễn cho tới khi TTL hết hạn hoặc restart.
- Trong kiến trúc nhiều instance/multi-region, invalidate một node không đồng nghĩa invalidate toàn hệ thống — cần một cơ chế broadcast (pub/sub, message queue) mà nếu thiếu, mỗi node tự giữ một phiên bản sự thật khác nhau.
- Invalidate quá rộng (xóa nguyên namespace thay vì đúng key) gây cache miss hàng loạt đúng lúc traffic cao, tạo thundering herd dội ngược vào DB — tức là cố sửa vấn đề nhất quán lại tạo ra sự cố hiệu năng mới.

## Solution
Cache invalidation là tập hợp các chiến lược để loại bỏ hoặc đánh dấu lỗi thời dữ liệu cache khi nguồn dữ liệu gốc (DB) thay đổi, đảm bảo lần đọc kế tiếp lấy được giá trị mới. Có ba chiến lược nền tảng, thường phối hợp với nhau chứ không loại trừ nhau: (1) invalidate theo key — xóa chính xác cache entry liên quan đến bản ghi vừa đổi; (2) invalidate theo event — phát sự kiện thay đổi qua message queue/pub-sub để mọi consumer (kể cả cache ở service khác, ở CDN, ở nhiều instance) tự xóa cache liên quan; (3) invalidate theo version — thay vì xóa, đổi cache key sang một phiên bản mới (ví dụ nhúng version hoặc hash nội dung vào key) khiến key cũ tự "chết" vì không còn ai đọc tới, không cần xóa tường minh.

## How It Works
**Invalidate theo key** là cách trực tiếp nhất: ứng dụng biết chính xác key nào bị ảnh hưởng (ví dụ `product:{sku}`) và gọi `DEL` ngay sau khi DB commit. Vấn đề nằm ở việc xác định *tập hợp đầy đủ* các key bị ảnh hưởng khi một bản ghi thay đổi kéo theo nhiều view khác nhau — ví dụ update giá sản phẩm phải invalidate cả `product:{sku}`, `category:{id}:products` (danh sách sản phẩm theo danh mục có thể đã cache kèm giá), và `search:index:{sku}`. Kỹ thuật phổ biến để quản lý tập hợp này là dùng **tagging** — lưu một mapping ngược từ entity ID sang danh sách cache key liên quan (ví dụ Redis Set `tags:product:{sku} -> {key1, key2, key3}`), khi entity đổi thì đọc set này ra và xóa toàn bộ.

**Invalidate theo event** giải quyết bài toán nhiều consumer: thay vì service ghi DB tự đi xóa cache ở mọi nơi, nó publish một event (`ProductUpdated{sku}`) lên message broker (Kafka, Redis pub/sub, SNS). Mọi service/instance có cache dữ liệu đó subscribe event này và tự xóa cache local hoặc cache dùng chung của mình. Cơ chế này tách rời (decouple) trách nhiệm invalidate khỏi service ghi dữ liệu — service ghi không cần biết ai đang cache gì, chỉ cần công bố "dữ liệu X đã đổi". Redis pub/sub qua kênh `__keyspace@0__:` hoặc dùng CDC (Change Data Capture, ví dụ Debezium đọc binlog MySQL) là hai cách hiện thực phổ biến: CDC đặc biệt đáng tin cậy hơn vì bắt được cả những thay đổi DB không đi qua tầng ứng dụng (migration, thao tác trực tiếp trên DB).

**Invalidate theo version** né tránh hoàn toàn bài toán "xóa đúng key ở đúng chỗ": thay vì `product:{sku}`, key trở thành `product:{sku}:v{version}` hoặc `product:{sku}:{content_hash}`, trong đó version tăng lên (hoặc hash đổi) mỗi khi dữ liệu gốc thay đổi. Không ai cần chủ động xóa key cũ — nó vẫn nằm trong cache nhưng không còn được truy vấn tới, và tự bị TTL/LRU dọn dẹp theo thời gian. Kỹ thuật này đặc biệt hiệu quả với cache tĩnh (asset, HTML fragment) và là nguyên lý đứng sau content-hashing trong build tool frontend (`app.a1b2c3.js`) lẫn ETag/versioned URL ở tầng CDN.

## Production Architecture
Trong một hệ thống tin tức có CDN (Cloudflare/Fastly) đứng trước, cache ứng dụng (Redis) ở giữa, và DB ở cuối, ba lớp invalidation phối hợp theo tầng: khi biên tập viên publish bài viết, service ghi DB, sau đó publish event `ArticlePublished{id}` lên Kafka; một consumer riêng lắng nghe event này để gọi CDN purge API (`purge_cache(url)`) — đây là invalidate theo event ở tầng CDN. Song song, service backend tự xóa `article:{id}` trong Redis theo key trực tiếp vì nó biết chính xác key nào cần xóa ngay tại chỗ, không cần đợi qua event queue (giảm độ trễ invalidation từ ~vài trăm ms xuống gần 0). Với dữ liệu tổng hợp tốn kém tính toán (trang chủ, top-10 bài đọc nhiều), hệ thống dùng versioned key `homepage:v{n}` và một background job tăng version định kỳ mỗi 60 giây thay vì invalidate theo sự kiện — chấp nhận độ trễ tối đa 60s để đổi lấy việc không phải tính lại trang chủ ngay tại thời điểm ghi (vốn có thể trùng giờ cao điểm). Ở quy mô nhiều region, event invalidation bắt buộc phải đi qua broker toàn cục (Kafka cross-region hoặc SNS) chứ không thể chỉ dựa vào Redis pub/sub cục bộ trong từng region, nếu không các region sẽ phân kỳ dữ liệu vĩnh viễn cho tới lần restart tiếp theo.

## Trade-offs
Invalidate theo key nhanh và chính xác nhất nhưng đòi hỏi ứng dụng phải biết (và duy trì đúng) toàn bộ tập key bị ảnh hưởng — độ phức tạp này tăng phi tuyến khi số lượng cached view tăng, và một mapping tag bị thiếu sót là một lớp bug âm thầm khó phát hiện qua test. Invalidate theo event giải quyết được vấn đề nhiều consumer/nhiều node nhưng đánh đổi bằng độ trễ (event phải qua broker, có thể mất vài chục đến vài trăm ms) và thêm một hạ tầng phải vận hành đúng (nếu Kafka consumer lag hoặc down, invalidation im lặng không xảy ra mà không có lỗi rõ ràng nào báo về). Invalidate theo version tránh hoàn toàn race condition và độ trễ broadcast nhưng lãng phí bộ nhớ (key cũ vẫn nằm trong cache cho tới khi bị evict) và không phù hợp khi cần invalidate ngay lập tức một entity cụ thể mà không muốn đổi version của cả một namespace lớn. Không có chiến lược nào là "đúng" tuyệt đối — hệ thống production thực tế luôn phối hợp cả ba tùy theo entity: key-based cho dữ liệu đơn lẻ cần chính xác tức thời, event-based cho dữ liệu cần đồng bộ nhiều node/service, version-based cho dữ liệu tổng hợp tốn kém tính toán lại.

## Best Practices
- Xây dựng tag/dependency mapping tường minh (entity → cache keys liên quan) thay vì để mỗi developer tự nhớ và invalidate thủ công rải rác trong code.
- Ưu tiên CDC (đọc trực tiếp từ binlog/WAL) cho các luồng invalidation quan trọng, vì nó bắt được mọi đường ghi dữ liệu kể cả những đường không đi qua tầng ứng dụng (migration, batch job, thao tác DB trực tiếp).
- Luôn giữ TTL như lưới an toàn cuối cùng ngay cả khi đã có event-based invalidation — event có thể bị mất, consumer có thể down, TTL đảm bảo dữ liệu không stale vĩnh viễn.
- Với dữ liệu tổng hợp/tốn kém tính toán lại, cân nhắc versioned key thay vì invalidate trực tiếp để tránh phải tính lại đúng lúc traffic cao.
- Đo và alert độ trễ invalidation (thời gian từ lúc DB commit tới lúc cache thực sự bị xóa/cập nhật) như một SLO riêng, không chỉ đo cache hit ratio.

## Common Mistakes
- Chỉ invalidate cache ở instance nhận request ghi, quên rằng các instance khác trong cụm vẫn giữ cache riêng và sẽ tiếp tục trả dữ liệu cũ cho tới khi TTL hết hạn.
- Invalidate quá rộng (xóa cả namespace hoặc pattern `product:*`) để "cho chắc", gây cache miss hàng loạt và thundering herd vào DB thay vì chỉ xóa đúng key bị ảnh hưởng.
- Quên invalidate các view phái sinh (danh sách, aggregate, search index) khi chỉ nghĩ tới cache của chính bản ghi vừa đổi, dẫn tới dữ liệu không nhất quán giữa trang chi tiết và trang danh sách.
- Đặt logic invalidate trước khi DB transaction commit thay vì sau, khiến một request đọc xen giữa có thể nạp lại đúng giá trị cũ vào cache ngay sau khi vừa xóa.
- Dựa hoàn toàn vào event-based invalidation mà không có TTL dự phòng, nên khi message queue gặp sự cố (consumer lag, mất message), cache stale vĩnh viễn mà không có tín hiệu lỗi nào.

## Interview Questions
**Hỏi**: Sự khác biệt giữa invalidate theo key, theo event và theo version là gì, và khi nào dùng cái nào?
**Trả lời**: Theo key là xóa trực tiếp đúng cache entry, phù hợp khi ứng dụng biết chính xác key bị ảnh hưởng và cần độ trễ thấp nhất. Theo event là publish thay đổi qua message broker để nhiều consumer/node tự xóa cache của mình, phù hợp cho hệ thống nhiều instance/service cùng cache một dữ liệu. Theo version là đổi cache key (thêm version/hash) thay vì xóa, phù hợp cho dữ liệu tổng hợp tốn kém tính toán lại, chấp nhận độ trễ để tránh phải tính lại ngay lập tức.

**Hỏi**: Tại sao trong một cụm nhiều instance, chỉ invalidate cache ở một node là chưa đủ?
**Trả lời**: Vì mỗi instance thường giữ cache riêng (in-process) hoặc kết nối tới các cache node khác nhau; xóa ở một node không lan truyền sang các node còn lại, cần cơ chế broadcast như pub/sub hoặc message queue để mọi node cùng nhận tín hiệu và tự invalidate.

**Hỏi**: Vì sao Phil Karlton coi cache invalidation là một trong hai bài toán khó nhất của khoa học máy tính?
**Trả lời**: Vì việc xác định chính xác *khi nào* và *cái gì* cần invalidate đòi hỏi hiểu toàn bộ đồ thị phụ thuộc giữa dữ liệu gốc và mọi view/derived data được cache từ nó; sai một chỗ trong đồ thị đó tạo ra bug âm thầm (stale data) rất khó phát hiện qua test vì hệ thống vẫn "chạy đúng", chỉ trả sai dữ liệu trong một cửa sổ thời gian không cố định.

## Summary
Cache invalidation là bài toán đảm bảo cache không trả về dữ liệu cũ sau khi nguồn gốc đã thay đổi, và độ khó nằm ở việc xác định đúng phạm vi ảnh hưởng cũng như lan truyền invalidation tới mọi nơi đang giữ bản sao dữ liệu. Ba chiến lược nền tảng — theo key, theo event, theo version — giải quyết ba khía cạnh khác nhau của vấn đề và thường được phối hợp trong cùng một hệ thống chứ không loại trừ nhau. Invalidate theo key nhanh nhưng đòi hỏi biết chính xác tập key bị ảnh hưởng; theo event lan truyền đúng tới nhiều node/service nhưng thêm độ trễ và hạ tầng; theo version né tránh race condition nhưng lãng phí bộ nhớ. TTL luôn phải tồn tại song song như lưới an toàn cuối cùng, bất kể chiến lược invalidation chủ động nào được dùng. Hiểu đúng đồ thị phụ thuộc giữa dữ liệu gốc và các cache view phái sinh là yếu tố quyết định một hệ thống cache đáng tin cậy hay âm thầm trả dữ liệu sai.

## Knowledge Graph
- Cache Aside — pattern đọc/ghi cache phổ biến nhất, phụ thuộc trực tiếp vào invalidation đúng đắn để tránh stale data.
- Cache Stampede / Thundering Herd — rủi ro phát sinh khi invalidation quá rộng hoặc đồng loạt gây cache miss hàng loạt.
- TTL (Time To Live) — cơ chế tự phục hồi bắt buộc phải song song với mọi chiến lược invalidation chủ động.
- Change Data Capture (CDC) — kỹ thuật đọc trực tiếp binlog/WAL để hiện thực invalidate theo event một cách đáng tin cậy, bắt được cả thay đổi ngoài tầng ứng dụng.
- Write-Through Cache — pattern thay thế giảm nhu cầu invalidate bằng cách ghi đồng bộ cache và DB, nhưng vẫn cần invalidation khi có nhiều writer/nhiều cache layer.
- Distributed Pub/Sub — cơ chế hạ tầng dùng để broadcast sự kiện invalidation tới nhiều node/instance trong cụm.

## Five Things To Remember
- Cache invalidation khó vì phải xác định đúng toàn bộ tập key bị ảnh hưởng, không chỉ key của bản ghi vừa đổi.
- Invalidate theo key nhanh nhưng cục bộ; theo event lan truyền tới nhiều node nhưng có độ trễ; theo version né race condition nhưng tốn bộ nhớ.
- Luôn giữ TTL như lưới an toàn cuối cùng dù đã có invalidation chủ động.
- Invalidate sau khi DB commit, không phải trước, để tránh nạp lại giá trị cũ vào cache.
- Trong cụm nhiều instance, invalidation phải được broadcast, không chỉ áp dụng cho node xử lý request ghi.
