---
id: acid
title: ACID
tags: ["database", "transactions"]
---

# ACID

> Status: Draft

## Problem

Một giao dịch trong production hiếm khi là một câu lệnh SQL đơn lẻ — trừ tiền tài khoản A và cộng tiền tài khoản B là hai lệnh `UPDATE` riêng biệt, tạo đơn hàng kèm trừ tồn kho là hai bảng khác nhau. Nếu database không đảm bảo rằng nhóm thao tác đó hoặc thành công toàn bộ hoặc không thay đổi gì, và không cách ly các giao dịch chạy đồng thời khỏi nhau, hệ thống sẽ rơi vào trạng thái dữ liệu không nhất quán ngay khi có tải thật, crash giữa chừng, hoặc hai request chạy song song.

## Pain Points

- Trừ tiền tài khoản A thành công nhưng cộng tiền tài khoản B thất bại (mất kết nối, timeout) — tiền "bốc hơi" khỏi hệ thống nếu không có atomicity.
- Hai request đặt vé cùng lúc đọc thấy "còn 1 vé", cả hai đều bán được — overselling do thiếu isolation, gây tranh chấp khách hàng và hoàn tiền thủ công.
- Server crash hoặc mất điện ngay sau khi commit nhưng trước khi ghi xuống đĩa — giao dịch coi như đã thành công nhưng biến mất sau khi restart, nếu không có durability.
- Ràng buộc nghiệp vụ (số dư không được âm, tổng nợ phải khớp tổng có) bị vi phạm giữa các bước của giao dịch, để lại dữ liệu ở trạng thái không hợp lệ mà các query sau đọc phải.

## Solution

ACID là bốn tính chất mà một transaction engine phải đảm bảo để giao dịch đáng tin cậy: **Atomicity** (tất cả hoặc không gì cả), **Consistency** (dữ liệu luôn thỏa ràng buộc đã định nghĩa trước và sau giao dịch), **Isolation** (các giao dịch chạy đồng thời không thấy trạng thái trung gian của nhau), **Durability** (một khi đã commit, dữ liệu tồn tại vĩnh viễn kể cả khi crash). Đây là hợp đồng giữa ứng dụng và database engine — ứng dụng chỉ cần gọi `BEGIN`/`COMMIT`/`ROLLBACK`, phần đảm bảo còn lại engine lo.

## How It Works

**Atomicity** được cài đặt qua write-ahead log (WAL) hoặc undo/redo log: mọi thay đổi được ghi log trước khi áp dụng vào dữ liệu thật, nếu transaction rollback hoặc crash giữa chừng, engine dùng log để hoàn tác (undo) các thay đổi chưa commit. Trong PostgreSQL đây là WAL kết hợp MVCC — mỗi thay đổi tạo ra một phiên bản dòng mới (tuple) thay vì ghi đè, transaction chưa commit chỉ tồn tại dưới dạng tuple "invisible" với các transaction khác.

**Consistency** không phải cơ chế riêng của DB engine mà là kết quả của ba tính chất còn lại cộng với ràng buộc do người dùng định nghĩa (`FOREIGN KEY`, `CHECK`, `NOT NULL`, trigger). Engine đảm bảo các ràng buộc này được kiểm tra tại thời điểm commit; nếu vi phạm, toàn bộ transaction bị rollback nhờ atomicity.

**Isolation** được kiểm soát bằng isolation level, cài đặt qua locking (2-phase locking: acquire lock dần, release toàn bộ khi commit) hoặc MVCC (mỗi transaction thấy một snapshot dữ liệu tại thời điểm bắt đầu, không thấy thay đổi chưa commit của transaction khác). Bốn mức chuẩn SQL — Read Uncommitted, Read Committed, Repeatable Read, Serializable — đánh đổi giữa mức độ cô lập và concurrency: mức càng cao càng ít race condition (dirty read, non-repeatable read, phantom read) nhưng càng nhiều lock contention hoặc phải retry do serialization failure.

**Durability** đảm bảo bằng `fsync` xuống đĩa: khi transaction commit, engine không trả về thành công cho client cho tới khi WAL record đã được ghi xuống storage bền vững (không chỉ nằm trong OS page cache, vốn có thể mất khi mất điện). Đây là lý do `fsync` là một trong những thao tác chậm nhất trong transaction commit, và cũng là lý do một số hệ thống cho phép tắt nó (`synchronous_commit = off` trong PostgreSQL) để đổi lấy throughput, chấp nhận rủi ro mất vài giao dịch cuối nếu crash.

## Production Architecture

Trong một hệ thống thanh toán, một giao dịch chuyển tiền thường được bọc trong `BEGIN ... COMMIT` với isolation level `Repeatable Read` hoặc `Serializable`, kèm `SELECT ... FOR UPDATE` để khóa dòng số dư trước khi trừ/cộng, tránh hai giao dịch đọc cùng số dư cũ rồi ghi đè lẫn nhau (lost update). WAL của PostgreSQL hoặc binlog của MySQL không chỉ phục vụ durability nội bộ mà còn được stream sang replica để phục vụ read replica và point-in-time recovery — cùng một cơ chế đảm bảo ACID cũng là nền tảng cho replication. Ở kiến trúc microservices, khi một "giao dịch" trải rộng qua nhiều service (mỗi service một database riêng), ACID chỉ áp dụng được trong phạm vi một database — buộc phải dùng Saga pattern hoặc outbox pattern để mô phỏng atomicity ở cấp hệ thống phân tán, đánh đổi lấy eventual consistency thay vì ACID thật.

## Trade-offs

- Isolation level càng cao (Serializable) càng tốn chi phí: nhiều lock hơn, nhiều serialization failure phải retry hơn, throughput giảm dưới tải cao.
- Đảm bảo durability tuyệt đối (`fsync` mỗi commit) giới hạn số transaction/giây; nhiều hệ thống chấp nhận "gần durability" (async commit, replication lag vài trăm ms) để đổi lấy tốc độ.
- ACID chỉ đảm bảo trong một database instance/cluster — không tự động mở rộng sang kiến trúc phân tán nhiều database, nơi phải trả giá bằng độ phức tạp (2PC, Saga) hoặc từ bỏ tính atomicity toàn cục.
- MVCC (cách phổ biến để cài Isolation) tốn thêm dung lượng và cần vacuum/garbage collection định kỳ để dọn phiên bản dòng cũ, nếu không sẽ phình bảng (bloat) và chậm dần theo thời gian.

## Best Practices

- Chọn isolation level theo đúng nhu cầu nghiệp vụ thay vì mặc định dùng Serializable cho mọi thứ — Read Committed đủ cho phần lớn API đọc thông thường.
- Giữ transaction càng ngắn càng tốt: không gọi API bên ngoài, không chờ user input, trong lúc transaction đang mở — giảm thời gian giữ lock.
- Dùng `SELECT ... FOR UPDATE` tường minh khi cần đọc-rồi-ghi trên cùng dòng dữ liệu để tránh lost update, thay vì tin vào isolation level mặc định.
- Luôn có retry logic cho lỗi serialization failure/deadlock ở tầng ứng dụng khi dùng isolation level cao, vì đây là hành vi mong đợi chứ không phải bug.
- Với giao dịch trải nhiều service, dùng outbox pattern hoặc Saga thay vì cố gắng giả lập ACID bằng 2PC phân tán, vốn dễ gây deadlock toàn hệ thống và giảm availability.

## Common Mistakes

- Bọc toàn bộ request HTTP (bao gồm gọi API bên thứ ba) trong một transaction, giữ lock/connection quá lâu và gây connection pool exhaustion khi tải tăng.
- Mặc định tin rằng "có transaction là an toàn" mà không kiểm tra isolation level thực tế — Read Committed (mặc định của PostgreSQL và nhiều DB khác) vẫn cho phép non-repeatable read và lost update trong một số kịch bản.
- Không xử lý serialization failure/deadlock exception, khiến giao dịch âm thầm thất bại và mất dữ liệu ở tầng ứng dụng dù DB đã làm đúng việc của nó.
- Coi ACID của database local là đủ cho toàn bộ luồng nghiệp vụ trải qua nhiều microservices, dẫn đến dữ liệu lệch nhau giữa các service khi một bước giữa chừng thất bại.
- Tắt `fsync`/synchronous commit để tăng performance mà không đánh giá rủi ro mất dữ liệu khi crash, đặc biệt trên hệ thống tài chính.

## Interview Questions

**Hỏi**: Isolation level nào là mặc định của PostgreSQL và MySQL, và nó ngăn được những anomaly nào?

**Trả lời**: PostgreSQL mặc định Read Committed — ngăn dirty read nhưng vẫn có thể gặp non-repeatable read và phantom read. MySQL (InnoDB) mặc định Repeatable Read — ngăn cả dirty read và non-repeatable read nhờ MVCC, nhưng theo chuẩn SQL vẫn có thể gặp phantom read (dù InnoDB dùng next-key locking để giảm bớt trường hợp này).

**Hỏi**: Atomicity và Durability khác nhau ở điểm nào, vì sao cần cả hai?

**Trả lời**: Atomicity đảm bảo một nhóm thao tác hoặc thực hiện trọn vẹn hoặc không thực hiện gì — xử lý trường hợp lỗi giữa chừng. Durability đảm bảo một khi đã commit thành công, kết quả đó không biến mất kể cả khi crash ngay sau đó — xử lý trường hợp lỗi sau khi hoàn tất. Thiếu atomicity thì dữ liệu dở dang; thiếu durability thì dữ liệu đã "xong" vẫn có thể mất, cả hai đều cần thiết để giao dịch đáng tin cậy.

**Hỏi**: Tại sao nói ACID không tự động áp dụng được cho kiến trúc microservices?

**Trả lời**: Vì transaction engine chỉ đảm bảo ACID trong phạm vi một database (thường là một instance/cluster). Khi một nghiệp vụ trải qua nhiều service với database riêng, không có engine chung nào bọc toàn bộ thao tác trong một `BEGIN/COMMIT` — phải dùng pattern ở tầng ứng dụng như Saga (chuỗi bước kèm compensating action) để mô phỏng atomicity, đánh đổi lấy eventual consistency.

## Summary

ACID là bốn tính chất — Atomicity, Consistency, Isolation, Durability — mà database engine dùng để đảm bảo giao dịch đáng tin cậy dù có crash hay chạy đồng thời. Atomicity và Durability dựa trên write-ahead logging và fsync để đảm bảo "tất cả hoặc không gì" và "đã lưu thì không mất". Isolation dựa trên locking hoặc MVCC, với mức độ cô lập càng cao thì càng tốn chi phí concurrency. Consistency là hệ quả của ba tính chất kia cộng với ràng buộc nghiệp vụ do người dùng định nghĩa. ACID chỉ đảm bảo trong phạm vi một database, nên kiến trúc phân tán cần thêm pattern như Saga hoặc outbox để đạt được sự đảm bảo tương đương ở quy mô lớn hơn.

## Knowledge Graph

- MVCC (Multi-Version Concurrency Control) — cơ chế phổ biến để cài đặt Isolation mà không cần lock đọc.
- Write-Ahead Log (WAL) — nền tảng cài đặt cả Atomicity và Durability.
- Isolation Levels (Read Committed, Repeatable Read, Serializable) — chi tiết hóa cách Isolation được áp dụng thực tế.
- Saga Pattern — cách mô phỏng atomicity khi giao dịch trải nhiều microservices/database.
- Deadlock — hệ quả trực tiếp của locking dùng để đảm bảo Isolation.
- Execution Plan — liên quan tới cách engine thực thi các thao tác bên trong một transaction.

## Five Things To Remember

- Atomicity nghĩa là tất cả hoặc không gì cả, không có trạng thái nửa vời.
- Durability được đảm bảo bằng fsync xuống đĩa tại thời điểm commit, không phải khi ghi vào cache.
- Isolation level cao hơn giảm race condition nhưng cũng giảm throughput.
- Consistency là hệ quả của ba tính chất còn lại cộng ràng buộc nghiệp vụ, không phải cơ chế riêng.
- ACID chỉ đúng trong một database — hệ phân tán cần Saga hoặc outbox pattern để đạt hiệu quả tương đương.
