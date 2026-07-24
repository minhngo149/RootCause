---
id: isolation-levels
title: Isolation Levels
tags: ["database", "transactions", "concurrency"]
---

# Isolation Levels

> Status: Draft

## Problem

Khi nhiều transaction chạy đồng thời trên cùng một dữ liệu, database phải quyết định transaction này được phép "nhìn thấy" bao nhiêu thay đổi chưa commit của transaction khác. Nếu không hiểu rõ isolation level mà connection đang dùng, engineer thường vô tình dựa vào một mức đảm bảo mạnh hơn (hoặc yếu hơn) thực tế đang có — dẫn tới bug chỉ xuất hiện dưới tải cao, khi nhiều request chạy song song thật sự.

## Pain Points

- Đọc phải dữ liệu chưa commit (dirty read) rồi transaction kia rollback — hệ thống tính toán trên số liệu chưa bao giờ thực sự tồn tại.
- Đọc lại cùng một dòng hai lần trong một transaction nhưng ra hai giá trị khác nhau (non-repeatable read), phá vỡ logic kiểm tra-rồi-hành-động (check-then-act).
- Chạy lại cùng một query aggregate hai lần trong một transaction nhưng ra số dòng khác nhau (phantom read) — báo cáo tài chính, đối soát bị sai lệch.
- Race condition dạng lost update: hai transaction cùng đọc một số dư, cùng cộng trừ, transaction commit sau ghi đè mất thay đổi của transaction trước — over-selling trong hệ thống inventory, double-spend trong hệ thống ví điện tử.

## Solution

Isolation level là hợp đồng mà database đưa ra: transaction được cách ly khỏi ảnh hưởng của các transaction đồng thời khác ở mức nào. SQL chuẩn định nghĩa 4 mức tăng dần độ an toàn và giảm dần độ song song: **Read Uncommitted**, **Read Committed**, **Repeatable Read**, **Serializable**. Mỗi mức loại bỏ thêm một loại anomaly, đổi lại bằng chi phí lock/version cao hơn và throughput thấp hơn.

## How It Works

Ba loại anomaly kinh điển:

- **Dirty read**: transaction A đọc dữ liệu mà transaction B đã ghi nhưng chưa commit. Nếu B rollback, A đã đọc dữ liệu "ma".
- **Non-repeatable read**: A đọc một dòng, B update và commit dòng đó, A đọc lại cùng dòng trong cùng transaction thì thấy giá trị khác.
- **Phantom read**: A chạy `SELECT ... WHERE amount > 1000` ra 5 dòng, B insert thêm một dòng thỏa điều kiện và commit, A chạy lại cùng query trong cùng transaction thì ra 6 dòng.

Bốn isolation level xử lý các anomaly này khác nhau:

- **Read Uncommitted**: không chặn gì cả, transaction có thể đọc cả dữ liệu chưa commit của transaction khác. Dính cả 3 loại anomaly. Postgres không thực sự implement mức này — khai báo `READ UNCOMMITTED` vẫn chạy như `READ COMMITTED`.
- **Read Committed**: chỉ đọc dữ liệu đã commit — mỗi statement lấy snapshot mới tại thời điểm statement bắt đầu chạy (không phải lúc transaction bắt đầu). Loại bỏ dirty read, nhưng vẫn dính non-repeatable read và phantom read vì hai statement khác nhau trong cùng transaction có thể thấy hai snapshot khác nhau. Đây là default của Postgres, Oracle, SQL Server.
- **Repeatable Read**: snapshot được chụp một lần tại thời điểm transaction bắt đầu (không phải mỗi statement), và giữ nguyên xuyên suốt transaction. Loại bỏ dirty read và non-repeatable read. Theo chuẩn SQL vẫn cho phép phantom read, nhưng implementation của MySQL/InnoDB dùng gap lock nên trong thực tế chặn được cả phantom read ở mức này — còn Postgres implement Repeatable Read bằng MVCC snapshot isolation nên phantom read với write thật sự bị chặn bằng cơ chế phát hiện write conflict (serialization failure), không phải bằng gap lock.
- **Serializable**: đảm bảo kết quả tương đương với việc chạy các transaction tuần tự (dù thực tế chạy song song). Loại bỏ cả 3 anomaly cộng thêm write skew. Postgres dùng SSI (Serializable Snapshot Isolation) — phát hiện xung đột dựa trên đọc/ghi chồng lấn và abort một trong hai transaction bằng lỗi `could not serialize access due to concurrent update`, buộc ứng dụng phải retry.

Cơ chế nền phổ biến nhất hiện nay là **MVCC** (Multi-Version Concurrency Control, dùng trong Postgres, MySQL/InnoDB, Oracle): thay vì lock đọc, mỗi transaction thấy một "phiên bản" dữ liệu tại một thời điểm xác định, còn writer tạo phiên bản mới thay vì ghi đè. Điều này cho phép read không bao giờ block write và ngược lại, nhưng đổi lại cần cơ chế dọn rác (VACUUM ở Postgres) để xóa các phiên bản cũ không còn transaction nào tham chiếu.

## Production Architecture

Trong một hệ thống thanh toán dùng Postgres, luồng trừ số dư ví thường chạy ở `REPEATABLE READ` hoặc dùng `SELECT ... FOR UPDATE` ở `READ COMMITTED` để lock hàng cần update, tránh lost update khi hai request rút tiền chạy song song trên cùng một tài khoản. Ngược lại, các dashboard báo cáo chỉ đọc (read-only, không cần chính xác tuyệt đối theo mili giây) thường để mặc định `READ COMMITTED` vì throughput quan trọng hơn tính nhất quán tuyệt đối. Với hệ thống dùng connection pool (PgBouncer ở chế độ transaction pooling), cần đặc biệt cẩn thận: `SET` isolation level cho session không đảm bảo áp dụng đúng connection vật lý ở lần transaction tiếp theo, nên phải set isolation level trong chính câu lệnh `BEGIN` hoặc qua driver ORM (`@Transactional(isolation = SERIALIZABLE)` ở Spring, hay `isolationLevel` option ở Prisma/TypeORM) thay vì set rời rạc.

## Trade-offs

- Isolation càng cao, throughput càng giảm — Serializable ở Postgres có thể abort một tỷ lệ đáng kể transaction dưới tải cao (serialization failure), buộc ứng dụng phải có logic retry, nếu không hệ thống sẽ báo lỗi cho người dùng thay vì tự phục hồi.
- Repeatable Read/Serializable giữ snapshot lâu hơn nghĩa là MVCC phải giữ lại nhiều phiên bản dữ liệu cũ hơn, làm bloat bảng và tăng thời gian VACUUM ở Postgres.
- Read Committed rẻ và đủ dùng cho phần lớn CRUD thông thường, nhưng là cái bẫy phổ biến nhất cho logic dạng "đọc số dư, kiểm tra điều kiện, rồi update" nếu không khóa dòng tường minh (`FOR UPDATE`) — nhiều engineer nhầm tưởng transaction tự động an toàn.
- Serializable không miễn phí về mặt kiến trúc: nó yêu cầu ứng dụng xử lý được lỗi serialization failure một cách graceful (retry với backoff), nếu không đây trở thành nguồn lỗi 500 khó tái hiện trong môi trường dev vì dev hiếm khi có đủ tải đồng thời để kích hoạt conflict.

## Best Practices

- Mặc định dùng Read Committed cho hầu hết luồng CRUD; chỉ nâng lên Repeatable Read/Serializable cho các luồng có logic đọc-rồi-ghi nhạy cảm (số dư, tồn kho, đặt chỗ).
- Với luồng cần đọc-rồi-ghi trên cùng một dòng, ưu tiên `SELECT ... FOR UPDATE` tường minh ở Read Committed thay vì nâng cả transaction lên Serializable — rẻ hơn và dễ debug hơn.
- Luôn viết logic retry (exponential backoff, giới hạn số lần) cho mọi transaction chạy ở Serializable, vì serialization failure là hành vi được thiết kế trước, không phải lỗi bất thường.
- Đo throughput thực tế dưới tải đồng thời trước khi chọn isolation level cho một luồng mới — hành vi anomaly chỉ lộ ra khi có concurrency thật, benchmark single-thread sẽ không phát hiện được.
- Kiểm tra isolation level mặc định của driver/ORM đang dùng — nhiều ORM âm thầm dùng isolation level khác với default của DB nếu cấu hình connection pool không rõ ràng.

## Common Mistakes

- Tin rằng "transaction" tự động nghĩa là an toàn khỏi race condition, trong khi Read Committed (default phổ biến nhất) vẫn dính lost update nếu không dùng lock tường minh.
- Dùng `SELECT` để kiểm tra điều kiện rồi `UPDATE` riêng biệt (không nằm trong cùng transaction hoặc không lock), tạo ra khoảng hở TOCTOU (time-of-check to time-of-use) giữa hai câu lệnh.
- Nâng toàn bộ ứng dụng lên Serializable "cho chắc" mà không đo tác động throughput, rồi bất ngờ khi production báo nhiều serialization failure dưới tải cao.
- Không xử lý lỗi serialization failure ở tầng ứng dụng, khiến lỗi vốn "có thể retry được" bị trả thẳng về người dùng như một lỗi hệ thống nghiêm trọng.
- Nhầm lẫn giữa Repeatable Read theo chuẩn SQL (cho phép phantom read) với implementation cụ thể của từng DB (MySQL/InnoDB chặn phantom read bằng gap lock, Postgres chặn bằng cơ chế conflict detection) — dẫn tới giả định sai khi migrate giữa các DB engine.

## Interview Questions

**Hỏi**: Phân biệt dirty read, non-repeatable read và phantom read.

**Trả lời**: Dirty read là đọc dữ liệu chưa commit (có thể bị rollback). Non-repeatable read là đọc lại cùng một dòng trong cùng transaction ra hai giá trị khác nhau vì transaction khác đã update và commit. Phantom read là chạy lại cùng một query ra tập dòng khác nhau vì transaction khác đã insert/delete dòng thỏa điều kiện.

**Hỏi**: Tại sao Read Committed vẫn có thể gây lost update dù đang chạy trong transaction?

**Trả lời**: Vì Read Committed chỉ đảm bảo không đọc dữ liệu chưa commit, không khóa dòng khi đọc. Hai transaction cùng đọc một giá trị, cùng tính toán dựa trên giá trị đó, transaction commit sau sẽ ghi đè kết quả của transaction commit trước mà không biết. Cần `SELECT FOR UPDATE` hoặc nâng isolation level để chặn.

**Hỏi**: MVCC giải quyết vấn đề gì mà locking truyền thống (2PL) không giải quyết tốt bằng?

**Trả lời**: MVCC cho phép reader không bao giờ chặn writer và ngược lại, vì mỗi transaction đọc một phiên bản snapshot riêng thay vì phải chờ lock được giải phóng. Đổi lại phải trả chi phí lưu trữ nhiều phiên bản và cần cơ chế dọn rác (VACUUM) để loại bỏ phiên bản không còn ai tham chiếu.

## Summary

Isolation level là hợp đồng xác định transaction đồng thời có thể ảnh hưởng lẫn nhau tới mức nào, biểu hiện qua ba loại anomaly: dirty read, non-repeatable read, phantom read. Bốn mức chuẩn — Read Uncommitted, Read Committed, Repeatable Read, Serializable — loại bỏ dần từng anomaly nhưng đổi lại bằng throughput và khả năng abort transaction cao hơn. Read Committed là mặc định phổ biến và đủ dùng cho phần lớn trường hợp, nhưng logic đọc-rồi-ghi nhạy cảm cần lock tường minh hoặc isolation level cao hơn. MVCC là cơ chế nền hiện đại giúp đọc không chặn ghi, nhưng cách các DB engine cụ thể (Postgres, MySQL/InnoDB) implement từng level lại khác nhau về chi tiết, đặc biệt với phantom read. Chọn isolation level đúng là việc đánh đổi có chủ đích giữa tính đúng đắn và hiệu năng, không phải chọn mức cao nhất "cho an toàn".

## Knowledge Graph

- MVCC (Multi-Version Concurrency Control) — cơ chế nền hiện thực hóa hầu hết isolation level ở Postgres/MySQL/Oracle.
- Lock (row lock, gap lock, `SELECT FOR UPDATE`) — cơ chế thay thế/bổ sung cho snapshot khi cần chặn lost update tường minh.
- Lost update / write skew — anomaly nâng cao chỉ Serializable mới đảm bảo loại bỏ hoàn toàn.
- Execution Plan — optimizer có thể chọn plan khác nhau tùy isolation level ảnh hưởng tới lock/scan.
- Connection Pooling (PgBouncer transaction mode) — ảnh hưởng tới việc set isolation level per-session có hoạt động đúng hay không.
- ACID — isolation là chữ "I", luôn được cân nhắc cùng Atomicity, Consistency, Durability khi thiết kế transaction.

## Five Things To Remember

- Dirty read đọc dữ liệu chưa commit, non-repeatable read đọc lại ra giá trị khác, phantom read đọc lại ra tập dòng khác.
- Read Committed là default phổ biến nhất nhưng không chặn được lost update nếu thiếu lock tường minh.
- Repeatable Read và Serializable chặn thêm anomaly nhưng đổi lại bằng throughput và khả năng bị abort transaction.
- Serializable ở Postgres có thể trả lỗi serialization failure — ứng dụng bắt buộc phải có logic retry.
- Isolation level cao nhất không phải lựa chọn mặc định an toàn — cần đo tải thực tế trước khi quyết định.
