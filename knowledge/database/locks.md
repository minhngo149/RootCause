---
id: locks
title: Locks
tags: ["database", "concurrency"]
---

# Locks

> Status: Draft

## Problem

Khi nhiều transaction cùng đọc/ghi chung một dòng hoặc một bảng, database phải quyết định ai được thay đổi dữ liệu trước, ai phải chờ, và ai bị từ chối. Nếu không có cơ chế khóa (locking), hai transaction có thể đọc cùng một giá trị, cùng tính toán dựa trên giá trị cũ, rồi cùng ghi đè lên nhau — kết quả là mất update (lost update) mà không hề có lỗi nào được báo ra. Vấn đề không nằm ở "có lock hay không" (mọi RDBMS đều có lock ở tầng engine), mà ở việc engineer không hiểu lock đang giữ ở mức nào, giữ bao lâu, và hai giao dịch đang chờ nhau theo thứ tự nào.

## Pain Points

- Lost update: hai request `UPDATE accounts SET balance = balance - 100` chạy dựa trên số dư đọc trước đó, không phải giá trị mới nhất, dẫn tới số dư sai lệch âm thầm.
- Deadlock: hai transaction khóa hai dòng theo thứ tự ngược nhau (A khóa row 1 rồi chờ row 2, B khóa row 2 rồi chờ row 1) — DB phải chủ động rollback một bên, request đó nhận lỗi giữa chừng.
- Lock contention gây timeout hàng loạt: một transaction giữ lock quá lâu (do quên COMMIT, do gọi API bên ngoài trong transaction) khiến hàng trăm request khác xếp hàng chờ, latency tăng vọt, connection pool cạn kiệt.
- Table lock trên bảng lớn trong giờ cao điểm (ví dụ `ALTER TABLE` hoặc `LOCK TABLES` không cẩn thận) có thể đóng băng toàn bộ tính năng liên quan tới bảng đó trong vài giây tới vài phút.

## Solution

Lock là cơ chế đảm bảo tính nhất quán khi truy cập đồng thời, hoạt động ở hai chiều: **pessimistic locking** (giữ lock trước khi đọc/sửa, chấp nhận block các transaction khác) và **optimistic locking** (không giữ lock, chỉ kiểm tra xung đột tại thời điểm ghi bằng version/timestamp). Bên cạnh đó, lock còn phân theo **phạm vi** (row lock chỉ khóa dòng cụ thể, table lock khóa toàn bảng) và theo **loại quyền** (shared lock cho phép nhiều reader cùng lúc, exclusive lock chỉ cho một writer độc quyền). Chọn đúng tổ hợp phạm vi + loại + chiến lược cho từng tình huống là bản chất của việc "hiểu lock", không phải chỉ biết `SELECT ... FOR UPDATE` là gì.

## How It Works

**Row lock vs table lock**: Row lock chỉ khóa các dòng thực sự bị chạm tới (qua index hoặc scan), cho phép các transaction khác thao tác trên dòng khác cùng bảng song song — đây là hành vi mặc định của `UPDATE`/`DELETE` trong InnoDB (MySQL) hay Postgres. Table lock khóa toàn bộ bảng, chặn mọi thao tác ghi (đôi khi cả đọc) từ transaction khác — thường xảy ra khi chạy DDL (`ALTER TABLE`), thao tác không dùng index (row lock trên toàn bộ dòng quét qua có thể suy biến thành gần như table lock), hoặc gọi `LOCK TABLES` tường minh.

**Shared (S) lock vs Exclusive (X) lock**: Shared lock cho phép nhiều transaction cùng đọc một dòng đồng thời nhưng không ai được ghi (`SELECT ... FOR SHARE` / `SELECT ... LOCK IN SHARE MODE`). Exclusive lock chỉ cho một transaction giữ tại một thời điểm, chặn cả đọc lẫn ghi từ transaction khác (`SELECT ... FOR UPDATE`, hoặc ngầm định khi chạy `UPDATE`/`DELETE`). Ma trận tương thích: S-S tương thích (nhiều reader), S-X và X-X không tương thích (phải chờ). Engine dùng lock manager để duy trì bảng theo dõi lock đang giữ trên mỗi dòng/trang, và một transaction xin lock không tương thích sẽ bị đưa vào hàng chờ (wait queue) cho tới khi lock được giải phóng hoặc timeout.

**Pessimistic locking**: giữ lock ngay khi đọc dữ liệu với ý định sửa, ép các transaction khác phải chờ. Ví dụ kinh điển: `SELECT balance FROM accounts WHERE id = ? FOR UPDATE` bên trong transaction — dòng này bị khóa exclusive cho tới khi COMMIT/ROLLBACK, transaction thứ hai gọi cùng câu lệnh trên cùng dòng sẽ block cho tới khi transaction đầu kết thúc. Phù hợp khi xung đột được dự đoán xảy ra thường xuyên (ví dụ trừ tiền, giữ chỗ vé).

**Optimistic locking**: không khóa gì khi đọc, chỉ gắn một cột `version` (hoặc `updated_at`) vào dòng dữ liệu. Khi ghi, câu lệnh kiểm tra version chưa đổi: `UPDATE accounts SET balance = ?, version = version + 1 WHERE id = ? AND version = ?`. Nếu số dòng bị ảnh hưởng trả về 0, nghĩa là có transaction khác đã ghi đè trước đó — ứng dụng phải đọc lại và thử lại (retry), hoặc báo lỗi conflict cho người dùng. Phù hợp khi xung đột hiếm xảy ra (ví dụ sửa profile người dùng), vì tránh được chi phí giữ lock và tăng throughput đáng kể.

**Deadlock detection**: engine (InnoDB, Postgres) chạy một thuật toán phát hiện chu trình chờ (wait-for graph). Khi phát hiện A chờ lock của B, B chờ lock của A, DB chọn một transaction làm "victim" để rollback ngay lập tức và trả lỗi `deadlock detected` cho client, giải phóng transaction còn lại tiếp tục.

## Production Architecture

Trong một hệ thống thanh toán, luồng trừ tiền ví thường dùng pessimistic lock: transaction mở, `SELECT ... FOR UPDATE` trên dòng ví, kiểm tra số dư, `UPDATE`, rồi `COMMIT` — toàn bộ trong một transaction ngắn để giảm thời gian giữ lock. Ngược lại, trong một hệ thống e-commerce, cập nhật thông tin đơn hàng do nhiều service microservice cùng đọc/ghi (order-service, shipping-service, notification-service) thường dùng optimistic lock với cột `version`, vì các service này hiếm khi sửa cùng một đơn hàng cùng lúc, và pessimistic lock giữa các service qua network sẽ tạo độ trễ không chấp nhận được. Ở tầng migration/DDL, một `ALTER TABLE ADD COLUMN` trên MySQL cũ (trước 5.6 INSTANT DDL) có thể giữ table lock hàng chục phút trên bảng hàng chục triệu dòng — đây là lý do các công cụ như `gh-ost`/`pt-online-schema-change` ra đời để né table lock bằng cách tạo bảng shadow và migrate dần.

## Trade-offs

- Pessimistic lock đảm bảo đúng đắn tuyệt đối nhưng giảm throughput vì transaction phải xếp hàng — không scale tốt khi số lượng transaction đồng thời trên cùng dữ liệu tăng cao.
- Optimistic lock cho throughput cao hơn khi xung đột hiếm, nhưng khi xung đột xảy ra thường xuyên (hot row), tỷ lệ retry cao khiến trải nghiệm người dùng tệ hơn pessimistic (phải retry nhiều lần thay vì chờ một lần).
- Row lock mịn hơn table lock nhưng tốn overhead quản lý (mỗi dòng bị khóa là một entry trong lock manager); trên các thao tác quét lớn, DB có thể tự động "escalate" thành table lock để giảm overhead — điều này gây bất ngờ nếu engineer không biết.
- Giữ transaction mở càng lâu (kể cả row lock) càng tăng khả năng deadlock và block hàng loạt request khác — đánh đổi giữa "logic nghiệp vụ phức tạp trong một transaction" và "an toàn hiệu năng" luôn tồn tại.

## Best Practices

- Giữ transaction pessimistic càng ngắn càng tốt: không gọi API bên ngoài, không thực hiện I/O chậm trong lúc đang giữ lock.
- Luôn khóa dòng theo cùng một thứ tự (ví dụ theo `id` tăng dần) ở mọi nơi trong code để tránh deadlock do thứ tự khóa chéo nhau.
- Dùng optimistic locking cho dữ liệu ít xung đột (profile, settings), pessimistic cho dữ liệu tài chính hoặc tồn kho có tính tranh chấp cao.
- Luôn có cơ chế retry với backoff khi gặp lỗi optimistic conflict hoặc deadlock — đừng để lỗi này trôi thẳng ra người dùng.
- Đo lock wait time và deadlock count qua metrics của DB (`SHOW ENGINE INNODB STATUS`, `pg_locks`) để phát hiện hot row trước khi nó gây outage.

## Common Mistakes

- Dùng `SELECT ... FOR UPDATE` nhưng không set transaction timeout hợp lý, khiến một request treo kéo theo cả chuỗi request khác chờ vô thời hạn.
- Quên thêm điều kiện `WHERE version = ?` khi update — tưởng đang optimistic locking nhưng thực chất chỉ là ghi đè vô điều kiện, mất tác dụng bảo vệ.
- Chạy `UPDATE`/`DELETE` không có index phù hợp trên điều kiện `WHERE`, khiến row lock lan ra toàn bộ dòng bị quét thay vì chỉ dòng khớp điều kiện.
- Trộn lẫn thứ tự khóa dòng giữa các luồng nghiệp vụ khác nhau (luồng A khóa user rồi order, luồng B khóa order rồi user) — nguồn gốc phổ biến nhất của deadlock trong hệ thống thực tế.
- Chạy DDL (`ALTER TABLE`) trực tiếp trên bảng lớn ở production mà không dùng công cụ online schema change, gây table lock kéo dài ngoài dự tính.

## Interview Questions

**Hỏi**: Khi nào nên dùng optimistic locking thay vì pessimistic locking?

**Trả lời**: Khi xung đột ghi trên cùng dữ liệu hiếm xảy ra và throughput quan trọng hơn việc chặn sớm — optimistic locking tránh chi phí giữ lock, chỉ phát hiện xung đột tại thời điểm ghi và để ứng dụng tự retry. Pessimistic phù hợp hơn khi xung đột thường xuyên và cái giá của việc phải retry nhiều lần cao hơn cái giá của việc chờ.

**Hỏi**: Tại sao hai transaction cùng chạy đúng logic, không có bug, vẫn có thể gây deadlock?

**Trả lời**: Vì deadlock không phải lỗi logic mà là lỗi thứ tự khóa tài nguyên — khi transaction A khóa row 1 rồi xin khóa row 2, còn transaction B khóa row 2 rồi xin khóa row 1, cả hai đều đúng về nghiệp vụ nhưng tạo thành chu trình chờ vòng tròn mà DB buộc phải phá vỡ bằng cách rollback một bên.

**Hỏi**: Row lock và table lock khác nhau như thế nào về tác động tới hệ thống đang chạy?

**Trả lời**: Row lock chỉ chặn các transaction khác thao tác trên đúng những dòng bị khóa, cho phép phần còn lại của bảng vẫn hoạt động bình thường; table lock chặn toàn bộ thao tác ghi (đôi khi cả đọc) trên cả bảng, nên một table lock kéo dài trên bảng đang được truy cập liên tục có thể gây outage cục bộ cho toàn bộ tính năng phụ thuộc vào bảng đó.

## Summary

Lock là cơ chế bắt buộc để đảm bảo tính nhất quán khi nhiều transaction truy cập đồng thời, nhưng cách chọn phạm vi (row/table), loại (shared/exclusive) và chiến lược (optimistic/pessimistic) quyết định trực tiếp tới throughput và độ ổn định của hệ thống. Pessimistic locking an toàn nhưng dễ gây nghẽn và deadlock nếu giữ lock lâu hoặc khóa sai thứ tự; optimistic locking scale tốt hơn nhưng cần cơ chế retry rõ ràng khi xung đột xảy ra. Table lock — dù chủ động (`LOCK TABLES`, DDL) hay bị động (row lock escalate) — luôn là rủi ro hiệu năng lớn nhất cần theo dõi trong production. Hiểu đúng lock nghĩa là biết đo lock wait time, biết đọc log deadlock, và biết thiết kế transaction ngắn, khóa theo thứ tự nhất quán.

## Knowledge Graph

- Execution Plan — quyết định index nào được dùng, ảnh hưởng trực tiếp tới việc row lock có bị escalate thành table lock hay không.
- Covering Index — index tốt giúp thao tác UPDATE/DELETE khóa đúng dòng cần thiết thay vì quét và khóa dư thừa.
- UPDATE/DELETE Without WHERE — trường hợp cực đoan của row lock lan rộng, biến thành khóa toàn bảng vì điều kiện WHERE rỗng.
- Transaction Isolation Level — quyết định lock được giữ tới khi nào và loại read lock nào được áp dụng (liên quan trực tiếp tới shared lock trong REPEATABLE READ/SERIALIZABLE).
- Connection Pool Exhaustion — hậu quả thường gặp khi lock contention khiến nhiều connection bị giữ chờ lâu.

## Five Things To Remember

- Row lock khóa dòng cụ thể, table lock khóa cả bảng — luôn ưu tiên row lock và tránh scan không dùng index.
- Shared lock cho phép nhiều reader, exclusive lock chỉ cho một writer tại một thời điểm.
- Pessimistic locking chờ trước để an toàn, optimistic locking kiểm tra sau để nhanh — chọn theo tần suất xung đột thực tế.
- Deadlock xảy ra khi thứ tự khóa tài nguyên bị đảo ngược giữa các transaction, không phải do lỗi logic nghiệp vụ.
- Giữ transaction càng ngắn càng tốt — thời gian giữ lock tỷ lệ thuận với rủi ro nghẽn và deadlock trong hệ thống production.
