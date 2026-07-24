---
id: read-replica
title: Read Replica
tags: ["database", "scalability"]
---

# Read Replica

> Status: Draft

## Problem

Một database instance duy nhất phải xử lý cả write (order, payment, update tồn kho) lẫn read (dashboard, báo cáo, API list sản phẩm) trên cùng một tập tài nguyên CPU, RAM, I/O. Khi lượng truy vấn đọc tăng lên gấp chục lần lượng ghi — trường hợp rất phổ biến ở ứng dụng thương mại điện tử hoặc mạng xã hội — các query đọc nặng (báo cáo, full-text search, export) bắt đầu tranh giành tài nguyên với các transaction ghi, khiến cả hai chậm lại. Scale-up (tăng CPU/RAM cho một instance) có giới hạn vật lý và chi phí tăng phi tuyến, trong khi ứng dụng vẫn chỉ có một điểm chịu tải duy nhất cho toàn bộ traffic.

## Pain Points

- Một báo cáo cuối ngày chạy `SELECT` quét hàng triệu dòng chiếm hết I/O bandwidth, khiến API đặt hàng timeout hàng loạt trong giờ cao điểm.
- Chi phí nâng cấp instance chính (vertical scaling) tăng theo cấp số nhân khi đã chạm trần tier cao nhất của cloud provider, trong khi phần lớn tải chỉ là đọc.
- Một truy vấn đọc lỗi (thiếu index, quét full table) hoặc một job export dữ liệu chạy sai giờ có thể kéo sập luôn cả luồng ghi vì dùng chung connection pool và tài nguyên vật lý.
- Không có khả năng cô lập tải theo mục đích sử dụng (real-time traffic vs. analytics vs. backup) nên một sự cố ở một domain lan sang toàn bộ hệ thống.

## Solution

Read replica là một hoặc nhiều bản sao dữ liệu được đồng bộ liên tục từ database chính (primary/master) thông qua cơ chế replication, chỉ phục vụ truy vấn đọc trong khi mọi thao tác ghi vẫn đi qua primary. Ứng dụng định tuyến write đến primary và định tuyến read đến replica (thường qua connection string riêng hoặc proxy như PgBouncer/ProxySQL), cho phép mở rộng khả năng đọc theo chiều ngang bằng cách thêm replica mà không cần đụng đến kiến trúc ghi.

## How It Works

Replication hoạt động dựa trên việc primary ghi mọi thay đổi vào một log tuần tự — WAL (Write-Ahead Log) trong PostgreSQL hoặc binlog trong MySQL — và replica liên tục kéo (streaming replication) hoặc nhận đẩy log này để áp dụng lại các thay đổi theo đúng thứ tự đã xảy ra trên primary. Có hai kiểu chính: **physical replication** (PostgreSQL streaming replication) sao chép byte-level các block dữ liệu, replica là bản sao vật lý y hệt primary; và **logical replication** (MySQL row-based binlog, PostgreSQL logical replication) sao chép ở mức sự kiện thay đổi dòng (insert/update/delete), cho phép replica có schema khác hoặc chỉ replicate một phần bảng.

Vì việc áp dụng log trên replica luôn xảy ra sau khi primary đã commit, luôn tồn tại một khoảng trễ gọi là **replication lag** — thời gian từ lúc dữ liệu được ghi trên primary đến lúc nó xuất hiện trên replica. Lag này phụ thuộc vào băng thông mạng giữa primary-replica, tải ghi trên primary (càng nhiều write, log càng lớn, replica càng phải xử lý nhiều), và tốc độ I/O của chính replica khi áp dụng log. Trong replication bất đồng bộ (asynchronous, mặc định của hầu hết hệ thống), primary commit và trả kết quả cho client ngay mà không chờ replica xác nhận đã nhận log — đây là lý do đọc từ replica có thể trả về dữ liệu cũ hơn vài trăm ms đến vài giây, hoặc lâu hơn nếu replica bị nghẽn I/O hoặc mạng chập chờn.

Một số hệ thống dùng **synchronous replication** cho ít nhất một replica (ví dụ `synchronous_commit = on` với `synchronous_standby_names` trong PostgreSQL) để primary chỉ commit khi replica đó đã xác nhận nhận log, loại bỏ lag nhưng đánh đổi lấy độ trễ commit tăng lên và giảm availability nếu replica đó down.

## Production Architecture

Trong một hệ thống thương mại điện tử điển hình, kiến trúc thường có một primary xử lý toàn bộ order/payment/inventory update, kèm 2-3 read replica: một replica phục vụ API đọc sản phẩm/danh mục cho traffic người dùng thật, một replica riêng cho team data/BI chạy báo cáo và query phân tích nặng, và đôi khi một replica ở region khác để giảm latency đọc cho người dùng xa. Application layer dùng router (ORM hỗ trợ read/write splitting, hoặc proxy như PgBouncer, ProxySQL, AWS RDS Proxy) để tự động điều hướng: write luôn đi primary, read mặc định đi replica trừ khi request được đánh dấu "cần dữ liệu mới nhất" (ví dụ ngay sau khi user vừa tạo đơn hàng, đọc lại đơn đó phải đi primary hoặc chờ replica bắt kịp). Các cloud managed database (AWS RDS, Google Cloud SQL, Azure Database) đều có tính năng tạo read replica có sẵn, tự động hóa việc thiết lập streaming replication và cho phép promote một replica thành primary mới khi cần failover.

## Trade-offs

- Replication lag là chi phí không thể loại bỏ hoàn toàn trong asynchronous replication — chấp nhận đọc dữ liệu cũ một khoảng thời gian để đổi lấy khả năng scale đọc và không ảnh hưởng latency ghi.
- Synchronous replication loại bỏ lag nhưng tăng latency commit trên primary (phải chờ mạng round-trip đến replica) và giảm availability ghi nếu replica đồng bộ bị down hoặc chậm.
- Thêm replica tăng chi phí hạ tầng và độ phức tạp vận hành (giám sát lag, xử lý failover, đảm bảo consistency giữa các tầng đọc) — không miễn phí về mặt kỹ thuật dù giải quyết được bài toán tải.
- Read replica không giải quyết được bottleneck ghi — nếu nghẽn cổ chai là write throughput trên primary, thêm bao nhiêu replica cũng không giúp ích, phải xử lý bằng sharding hoặc partitioning.

## Best Practices

- Giám sát replication lag liên tục (ví dụ `pg_stat_replication` trong PostgreSQL, `SHOW REPLICA STATUS` trong MySQL) và đặt alert khi lag vượt ngưỡng chấp nhận được của nghiệp vụ.
- Định tuyến các luồng nghiệp vụ nhạy với "read-after-write" (đọc lại ngay sau khi ghi) về primary thay vì replica, hoặc dùng cơ chế "read your own writes" (session sticky đến primary một khoảng thời gian ngắn sau khi ghi).
- Tách riêng replica phục vụ traffic ứng dụng và replica phục vụ báo cáo/BI, tránh để một query phân tích nặng làm chậm cả tầng đọc phục vụ người dùng thật.
- Kiểm thử kịch bản failover (promote replica thành primary) định kỳ thay vì chỉ tin vào cấu hình, vì đây là lúc dễ phát hiện lag dữ liệu hoặc cấu hình sai lệch.
- Đặt timeout và retry hợp lý ở tầng ứng dụng khi replica bị lag quá xa hoặc mất kết nối, tránh degrade toàn bộ trải nghiệm đọc khi chỉ một replica gặp sự cố.

## Common Mistakes

- Đọc dữ liệu từ replica ngay sau khi ghi vào primary trong cùng luồng nghiệp vụ (ví dụ tạo đơn hàng xong redirect sang trang chi tiết đơn) mà không tính đến replication lag, khiến người dùng thấy lỗi "not found" hoặc dữ liệu cũ.
- Không giám sát lag cho đến khi nó âm thầm tăng lên vài phút do một job ghi hàng loạt (bulk import, migration), làm sai lệch toàn bộ báo cáo dựa trên replica mà không ai nhận ra.
- Coi read replica là giải pháp cho bottleneck ghi, trong khi replica chỉ tăng khả năng đọc, không giúp gì cho throughput ghi trên primary.
- Route toàn bộ traffic đọc — kể cả các nghiệp vụ cần strong consistency (kiểm tra số dư trước khi trừ tiền) — sang replica mà không phân loại, dẫn đến race condition hoặc quyết định nghiệp vụ dựa trên dữ liệu cũ.
- Không có chiến lược failover rõ ràng: khi primary down, không biết replica nào nên được promote, dẫn đến split-brain hoặc mất dữ liệu ghi giữa lúc chuyển đổi.

## Interview Questions

**Hỏi**: Replication lag là gì và tại sao nó luôn tồn tại trong asynchronous replication?

**Trả lời**: Replication lag là khoảng thời gian từ khi dữ liệu được commit trên primary đến khi nó được áp dụng xong trên replica. Trong asynchronous replication, primary không chờ replica xác nhận trước khi commit, nên luôn có độ trễ do thời gian truyền log qua mạng và thời gian replica áp dụng lại các thay đổi, đặc biệt tăng lên khi tải ghi cao hoặc mạng/I/O chậm.

**Hỏi**: Khi nào nên dùng synchronous replication thay vì asynchronous, và cái giá phải trả là gì?

**Trả lời**: Dùng synchronous khi nghiệp vụ không chấp nhận mất dữ liệu committed nếu primary down đột ngột (ví dụ hệ thống tài chính cần đảm bảo không mất giao dịch đã xác nhận). Cái giá là latency commit trên primary tăng (phải chờ replica xác nhận qua mạng) và giảm availability ghi nếu replica đồng bộ đó gặp sự cố hoặc chậm.

**Hỏi**: Làm sao xử lý bài toán "đọc lại ngay sau khi ghi" (read-after-write) khi hệ thống dùng read replica?

**Trả lời**: Có ba cách phổ biến: (1) route request đọc-ngay-sau-ghi về thẳng primary trong một khoảng thời gian ngắn (session sticky), (2) chờ replica bắt kịp bằng cách kiểm tra LSN/GTID đã đồng bộ trước khi trả kết quả đọc, hoặc (3) chấp nhận eventual consistency ở tầng UI (ví dụ hiển thị dữ liệu vừa ghi từ cache/response của chính request ghi đó thay vì query lại).

## Summary

Read replica giải quyết bài toán tách read/write traffic bằng cách nhân bản dữ liệu từ primary sang một hoặc nhiều instance chỉ phục vụ đọc, cho phép scale khả năng đọc theo chiều ngang mà không ảnh hưởng đến luồng ghi. Cơ chế nền tảng là streaming/logical replication dựa trên WAL hoặc binlog, và vì việc áp dụng log trên replica luôn diễn ra sau primary, replication lag là chi phí không thể loại bỏ hoàn toàn trong asynchronous replication. Kiến trúc production thường kết hợp nhiều replica cho các mục đích khác nhau (traffic thật, báo cáo, cross-region) cùng logic định tuyến ở tầng ứng dụng hoặc proxy. Read replica không giải quyết bottleneck ghi và đòi hỏi giám sát lag, chiến lược failover, và phân loại nghiệp vụ nào chấp nhận đọc dữ liệu cũ. Lựa chọn asynchronous hay synchronous replication là đánh đổi trực tiếp giữa độ trễ commit, availability ghi, và mức độ tươi mới của dữ liệu đọc.

## Knowledge Graph

- ACID — Durability và Atomicity trên primary là nền tảng để log ghi (WAL/binlog) có thể replicate đáng tin cậy sang replica.
- Isolation Levels — quyết định replica có thể phục vụ read với mức nhất quán nào so với primary tại cùng thời điểm.
- Sharding — giải pháp bổ sung khi bottleneck là ghi chứ không phải đọc, read replica không thay thế được.
- Eventual Consistency — mô hình nhất quán mà read replica vận hành theo trong chế độ asynchronous.
- Connection Pooling / Proxy (PgBouncer, ProxySQL) — lớp hạ tầng thường dùng để định tuyến read/write splitting tới đúng instance.
- Failover — kịch bản promote replica thành primary khi primary gặp sự cố, liên quan trực tiếp đến độ tin cậy của kiến trúc replica.

## Five Things To Remember

- Read replica chỉ phục vụ đọc, mọi thao tác ghi vẫn phải đi qua primary.
- Replication lag luôn tồn tại trong asynchronous replication, không thể loại bỏ hoàn toàn.
- Đọc dữ liệu ngay sau khi ghi cần route về primary hoặc chờ replica bắt kịp, không được mặc định đi qua replica.
- Read replica giải quyết bottleneck đọc, không giải quyết bottleneck ghi.
- Synchronous replication đổi lag lấy latency commit cao hơn và availability ghi thấp hơn.
