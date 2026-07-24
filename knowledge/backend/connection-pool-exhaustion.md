---
id: connection-pool-exhaustion
title: Connection Pool Exhaustion
tags: ["backend", "database", "production-incident"]
---

# Connection Pool Exhaustion

> Status: Draft

## Problem

Mọi kết nối tới database đều đắt để tạo mới (TCP handshake, TLS negotiation, authentication, khởi tạo session ở phía DB), nên ứng dụng dùng connection pool để tái sử dụng một số lượng kết nối cố định. Khi số request cần connection vượt quá số connection khả dụng trong pool — vì connection bị giữ quá lâu (query chậm), bị giữ mãi mãi (leak, không trả lại pool), hoặc pool size cấu hình quá nhỏ so với tải thực tế — mọi request mới phải xếp hàng chờ một connection rảnh. Khi hàng chờ này vượt quá timeout của pool, request bắt đầu fail hàng loạt dù bản thân database vẫn khỏe mạnh và còn dư tài nguyên.

## Pain Points

- Toàn bộ service đột ngột trả về timeout hoặc lỗi `connection pool exhausted` hàng loạt trong khi CPU/memory của DB server vẫn thấp, khiến đội vận hành nghi ngờ sai chỗ (network, DB) thay vì tầng application.
- Một endpoint chạy query chậm (thiếu index, table scan) có thể chiếm hết pool và làm sập luôn các endpoint hoàn toàn không liên quan, vì tất cả cùng chia sẻ một pool — hiệu ứng "noisy neighbor" nội bộ trong cùng một service.
- Leak connection (không đóng sau khi dùng, quên trả về pool trong nhánh exception) tích lũy dần theo thời gian, service chạy ổn nhiều giờ rồi đột ngột sập không có thay đổi deploy nào gần đó, rất khó liên hệ nguyên nhân.
- Tăng pool size một cách cảm tính để "chữa cháy" có thể làm chính DB quá tải (mỗi connection tốn RAM và một backend process/thread ở phía DB), biến sự cố ứng dụng thành sự cố database.

## Solution

Connection pool exhaustion được giải quyết bằng ba lớp phòng thủ kết hợp: (1) sizing pool đúng theo công thức dựa trên số lượng worker/CPU và độ trễ query thực tế, không đoán mò; (2) đảm bảo connection luôn được trả về pool bằng try/finally hoặc context manager, không phụ thuộc developer nhớ đóng thủ công; (3) đặt timeout tường minh ở tầng lấy connection (`acquire timeout`) để request fail nhanh và trả lỗi rõ ràng thay vì treo vô thời hạn chờ một connection không bao giờ tới.

## How It Works

Connection pool là một cấu trúc dữ liệu quản lý một tập connection đã mở sẵn tới DB, thường gồm: `pool size` tối đa (min/max), một hàng đợi (queue) cho các request đang chờ connection, và một `acquire timeout` quy định thời gian tối đa một request được chờ trước khi bị từ chối. Khi code gọi `pool.acquire()` (hoặc tương đương, tuỳ driver — pg-pool, HikariCP, SQLAlchemy pool, database/sql của Go), pool trả ngay một connection rảnh nếu có; nếu không, request bị đẩy vào hàng đợi. Connection chỉ được trả lại pool (`release`/`close` logic ở tầng pool, không phải đóng TCP thật) khi code gọi `release()` một cách tường minh — nếu exception xảy ra giữa chừng và code không có `finally`/context manager bọc quanh, connection đó bị mất khỏi pool vĩnh viễn (leak) dù về mặt TCP nó vẫn tồn tại, chỉ là không ai biết để trả lại. Khi tất cả connection trong pool đang "busy" (đang chạy query hoặc bị leak) và hàng đợi vượt quá `acquire timeout`, driver ném lỗi (`TimeoutError: Could not acquire connection` ở pg-pool, `Connection is not available, request timed out` ở HikariCP) — đây là lỗi ở tầng ứng dụng, không phải lỗi DB, dù triệu chứng bề ngoài giống hệt DB chết. Một chi tiết quan trọng: mỗi connection ở phía DB tương ứng với một process (PostgreSQL, mô hình process-per-connection) hoặc một thread (MySQL, SQL Server) đang chiếm RAM và context ở phía server — pool size không thể tăng vô hạn vì tổng số connection từ mọi instance ứng dụng cộng lại phải nằm dưới `max_connections` của DB, nếu không chính DB sẽ từ chối kết nối mới.

## Production Architecture

Trong một hệ thống microservices điển hình, mỗi instance của một service (chạy N pod trong Kubernetes) tự duy trì một pool riêng, ví dụ pool size 20 mỗi pod. Khi HPA scale từ 5 lên 30 pod dưới tải cao, tổng số connection tới DB nhảy từ 100 lên 600 gần như tức thời — nếu `max_connections` của Postgres chỉ đặt 200, làn sóng pod mới sẽ không kết nối được, gây cascading failure đúng lúc hệ thống cần scale nhất. Kiến trúc production đúng đắn thường chèn một connection pooler ở tầng giữa (PgBouncer cho Postgres, ProxySQL cho MySQL) chạy chế độ transaction pooling — mỗi service vẫn giữ pool logic riêng nhưng PgBouncer multiplex hàng trăm connection logic xuống một số connection vật lý nhỏ hơn nhiều tới DB thật. Ngoài ra, mỗi pool cần một `statement_timeout` hoặc `query_timeout` áp ở tầng DB/driver để một query bất thường (thiếu index, deadlock, table scale lớn) không giữ connection vô thời hạn và kéo cả pool xuống theo.

## Trade-offs

Pool size lớn giảm khả năng exhaustion dưới tải cao nhưng tăng rủi ro áp đảo `max_connections` của DB khi có nhiều instance ứng dụng cùng scale — không có con số "an toàn tuyệt đối", chỉ có con số cân bằng theo tổng instance dự kiến. Acquire timeout ngắn giúp request fail nhanh, trả lỗi rõ ràng cho client thay vì treo, nhưng dưới spike tải ngắn hạn (traffic burst bình thường, không phải sự cố) có thể biến một độ trễ tạm thời thành lỗi hàng loạt không cần thiết — cần cân bằng với retry ở tầng gọi. Đặt statement_timeout chặt để bảo vệ pool khỏi query chậm có thể vô tình cắt ngang các batch job hoặc report hợp lệ nhưng cần chạy lâu, đòi hỏi tách pool riêng cho các luồng công việc có đặc tính độ trễ khác nhau.

## Best Practices

- Tính pool size dựa trên công thức thực nghiệm (vd. công thức của HikariCP: `connections = ((core_count * 2) + effective_spindle_count)`), sau đó tinh chỉnh bằng load test thực tế, không copy con số mặc định của framework.
- Luôn lấy và trả connection trong try/finally hoặc context manager (`with`, `using`, `defer`) để đảm bảo connection được trả về pool ngay cả khi exception xảy ra giữa chừng.
- Đặt `statement_timeout`/`query_timeout` ở tầng DB hoặc driver cho mọi connection, để một query treo không thể giữ pool vô thời hạn.
- Tách pool riêng cho các luồng công việc có SLA độ trễ khác nhau (OLTP request nhanh vs. batch job/report chạy lâu), tránh một job chậm làm cạn pool dùng chung cho traffic thời gian thực.
- Giám sát các metric của pool (active connections, idle connections, wait queue length, acquire time) như tín hiệu sớm, không chỉ dựa vào lỗi timeout khi đã xảy ra.

## Common Mistakes

- Mở connection thủ công trong try, quên đóng trong catch/finally, dẫn đến leak tích luỹ dần và chỉ lộ ra sau nhiều giờ hoặc nhiều ngày chạy ổn định.
- Đặt pool size bằng số connection tối đa mà DB cho phép chia đều cho từng service, không tính đến việc số instance của service sẽ tăng khi autoscale.
- Không đặt acquire timeout, khiến request treo vô thời hạn chờ connection thay vì fail nhanh với lỗi có thể retry hoặc trả về người dùng.
- Dùng chung một pool cho cả truy vấn nhanh (API request) và truy vấn chậm (report, export, batch), để một job nặng làm cạn pool ảnh hưởng toàn bộ traffic khác.
- Tăng pool size để "chữa" triệu chứng timeout mà không điều tra nguyên nhân gốc (query chậm thiếu index, transaction giữ lock lâu), khiến vấn đề chuyển từ ứng dụng sang chính database.

## Interview Questions

**Hỏi**: Vì sao connection pool exhaustion có thể xảy ra ngay cả khi database server còn dư CPU và memory?

**Trả lời**: Vì exhaustion là giới hạn ở tầng ứng dụng (số connection object trong pool logic), không phải giới hạn tài nguyên vật lý của DB. Nếu connection bị giữ lâu (query chậm) hoặc bị leak (không trả về pool), pool cạn dù DB server hoàn toàn khỏe mạnh và có thể xử lý thêm connection mới — vấn đề nằm ở cách ứng dụng quản lý vòng đời connection, không nằm ở DB.

**Hỏi**: Tại sao không nên đặt pool size cực lớn để tránh exhaustion?

**Trả lời**: Mỗi connection tới DB (đặc biệt với Postgres) tương ứng với một process/thread riêng ở phía server, tốn RAM và context switching. Nếu mỗi instance ứng dụng đều mở pool lớn và số instance tăng khi autoscale, tổng connection có thể vượt `max_connections` của DB, gây từ chối kết nối hàng loạt — biến vấn đề ứng dụng thành sự cố ở tầng database.

**Hỏi**: Connection pooler như PgBouncer giải quyết vấn đề gì mà việc tăng pool size ở tầng ứng dụng không giải quyết được?

**Trả lời**: PgBouncer multiplex nhiều connection logic từ nhiều instance ứng dụng xuống một số lượng nhỏ hơn nhiều connection vật lý thật tới DB (transaction pooling), cho phép hệ thống scale số instance ứng dụng mà không làm tăng tuyến tính số connection thật tới DB, giải quyết đúng bài toán "nhiều pod, một giới hạn max_connections cố định".

## Summary

Connection pool exhaustion xảy ra khi số request cần connection vượt quá số connection khả dụng trong pool, do query chậm giữ connection lâu, do leak khiến connection không bao giờ được trả lại, hoặc do pool size cấu hình sai so với tải thực tế. Triệu chứng là timeout hàng loạt ở tầng ứng dụng dù DB server vẫn khỏe, dễ gây chẩn đoán sai nếu không phân biệt được lỗi acquire connection với lỗi DB thật sự. Giải pháp cốt lõi là sizing pool dựa trên công thức và load test, đảm bảo connection luôn được trả về qua try/finally/context manager, và đặt timeout tường minh ở cả tầng acquire lẫn tầng query. Trong kiến trúc production nhiều instance, một connection pooler trung gian (PgBouncer, ProxySQL) là lớp bảo vệ cần thiết để tổng connection không vượt giới hạn của DB khi autoscale.

## Knowledge Graph

- Deadlocks — cả hai đều là lớp sự cố production do quản lý tài nguyên (lock/connection) thiếu kỷ luật, gây timeout hàng loạt.
- Execution Plan — query chậm thiếu index là nguyên nhân phổ biến khiến connection bị giữ lâu và làm cạn pool.
- Read Replica — tách traffic đọc sang replica giúp giảm áp lực lên pool của primary DB.
- Retry Storm — client retry ngay lập tức khi gặp lỗi acquire timeout có thể làm exhaustion trầm trọng thêm thay vì tự phục hồi.
- Circuit Breaker — cơ chế ngắt mạch ở tầng gọi giúp ngăn request tiếp tục dồn vào một pool đã cạn kiệt.
- Transactions — transaction mở lâu (chưa commit/rollback) là một dạng leak giữ connection gián tiếp phổ biến.

## Five Things To Remember

- Pool exhaustion là giới hạn ở tầng ứng dụng, không phải DB hết tài nguyên vật lý.
- Luôn trả connection về pool bằng try/finally hoặc context manager, không dựa vào việc nhớ đóng thủ công.
- Một query chậm hoặc leak có thể làm cạn pool và kéo sập cả những endpoint không liên quan dùng chung pool.
- Tính pool size theo công thức và load test, không đoán mò, và luôn tính đến số instance sẽ tăng khi autoscale.
- Đặt acquire timeout và statement timeout tường minh để request fail nhanh, có thể retry, thay vì treo vô thời hạn.
