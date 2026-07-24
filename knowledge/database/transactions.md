---
id: transactions
title: Transactions
tags: ["database", "transactions"]
---

# Transactions

> Status: Draft

## Problem

Một request nghiệp vụ thường phải thực hiện nhiều thay đổi dữ liệu cùng lúc — ví dụ trừ tiền tài khoản A và cộng tiền tài khoản B, hoặc tạo đơn hàng và trừ tồn kho. Nếu không có cơ chế đảm bảo các thay đổi này xảy ra "trọn vẹn hoặc không xảy ra gì cả", ứng dụng sẽ rơi vào trạng thái dữ liệu nửa vời khi có lỗi giữa chừng, crash tiến trình, hoặc mất kết nối mạng.

## Pain Points

- Trừ tiền tài khoản A thành công nhưng cộng tiền tài khoản B thất bại (do exception, timeout, mất kết nối DB) — tiền "bốc hơi" khỏi hệ thống, gây sai lệch sổ sách không thể dò ra bằng log thông thường.
- Hai request ghi đồng thời lên cùng dữ liệu mà không có transaction/isolation phù hợp dẫn tới lost update hoặc race condition — ví dụ hai đơn hàng cùng trừ tồn kho từ số dư đã đọc trước đó, kết quả tồn kho âm.
- Khi crash giữa chừng một quy trình nhiều bước (multi-step write), việc dò lại xem bước nào đã chạy, bước nào chưa, tốn rất nhiều thời gian debug và thường phải xử lý thủ công trên production.
- Chi phí vận hành tăng vọt: đội on-call phải viết script reconcile dữ liệu, chạy backfill, và giải thích với khách hàng vì sao số dư/đơn hàng không khớp.

## Solution

**Transaction** là một đơn vị công việc (unit of work) gồm một hoặc nhiều thao tác đọc/ghi được database đảm bảo tính chất **ACID**: Atomicity (tất cả hoặc không gì cả), Consistency (dữ liệu luôn hợp lệ theo ràng buộc), Isolation (các transaction chạy song song không thấy trạng thái trung gian của nhau), Durability (khi đã commit thì không mất dù crash). Ứng dụng mở transaction bằng `BEGIN`, thực hiện các câu lệnh, rồi kết thúc bằng `COMMIT` (áp dụng thay đổi) hoặc `ROLLBACK` (hủy toàn bộ thay đổi).

## How It Works

Khi `BEGIN` được gọi, database mở một transaction context gắn với connection hiện tại. Mọi thay đổi (`INSERT`/`UPDATE`/`DELETE`) sau đó được ghi vào một vùng chưa công khai (trong PostgreSQL là các phiên bản dòng mới theo cơ chế MVCC — Multi-Version Concurrency Control; trong MySQL/InnoDB là undo log kết hợp redo log). Các transaction khác, tùy isolation level, sẽ không thấy các thay đổi này cho tới khi `COMMIT` xảy ra.

`COMMIT` yêu cầu database ghi một bản ghi "commit" vào write-ahead log (WAL) trên đĩa (fsync) trước khi báo thành công cho client — đây là cơ chế đảm bảo durability: nếu server crash ngay sau đó, khi khởi động lại, database replay WAL để khôi phục đúng trạng thái đã commit. `ROLLBACK` thì ngược lại: database dùng undo log để hoàn tác các thay đổi chưa commit, giải phóng lock, và transaction coi như chưa từng xảy ra.

Isolation được kiểm soát qua **isolation level** (Read Uncommitted, Read Committed, Repeatable Read, Serializable). Level càng cao càng tránh được nhiều hiện tượng bất thường (dirty read, non-repeatable read, phantom read) nhưng càng tốn chi phí — thường bằng cách giữ lock lâu hơn hoặc phải kiểm tra conflict tại thời điểm commit (optimistic concurrency control, như trong PostgreSQL Serializable Snapshot Isolation).

Transaction boundary — điểm bắt đầu và kết thúc transaction — không nhất thiết trùng với một câu SQL. Trong ứng dụng thực tế, boundary thường được đặt ở tầng service/use-case (ví dụ một hàm `transferMoney()`), bao trọn toàn bộ logic nghiệp vụ cần tính atomic, và transaction chỉ nên tồn tại trong đúng phạm vi cần thiết — mở càng muộn, đóng càng sớm càng tốt để giảm thời gian giữ lock.

## Production Architecture

Trong một hệ thống thanh toán, endpoint `POST /transfer` thường mở transaction ngay khi bắt đầu xử lý request: đọc số dư tài khoản nguồn với `SELECT ... FOR UPDATE` để khóa dòng, kiểm tra đủ số dư, trừ tiền, cộng tiền tài khoản đích, ghi một dòng vào bảng `ledger_entries`, rồi `COMMIT`. Toàn bộ nằm trong một database transaction duy nhất, thường chạy dưới connection pool riêng (ví dụ HikariCP, pgbouncer ở chế độ session cho transaction dài) để tránh transaction bị đứt giữa chừng do connection bị pool tái sử dụng.

Với hệ thống microservices, khi một nghiệp vụ trải dài qua nhiều service khác nhau (ví dụ order-service và inventory-service ở hai database riêng), transaction ACID truyền thống không áp dụng được xuyên service — lúc này production dùng **Saga pattern**: chuỗi các local transaction, mỗi bước có một compensating action để hoàn tác nếu bước sau thất bại (ví dụ hủy đơn hàng nếu trừ tồn kho thất bại), thường được điều phối qua message queue hoặc orchestrator (Temporal, AWS Step Functions).

## Trade-offs

Transaction giữ lock trên các dòng dữ liệu bị đụng chạm, nên transaction càng dài (gọi API bên ngoài, chờ user input bên trong transaction) càng làm tăng contention, giảm throughput, và có thể dẫn tới deadlock giữa các transaction đang chờ lock chéo nhau. Isolation level cao (Serializable) giảm bug do concurrency nhưng tăng tỷ lệ transaction bị abort do conflict, buộc ứng dụng phải có logic retry. Với hệ phân tán (nhiều database/service), không thể có transaction ACID thật sự — phải đánh đổi sang eventual consistency và chấp nhận độ phức tạp của Saga/compensating transaction, kèm rủi ro trạng thái trung gian hiển thị cho người dùng trong lúc saga chưa hoàn tất.

## Best Practices

- Giữ transaction càng ngắn càng tốt — không gọi HTTP API, không chờ I/O chậm, không xử lý logic không liên quan bên trong transaction.
- Luôn xử lý rollback tường minh khi có exception (dùng try/finally hoặc transaction decorator của framework) — không để transaction treo mở do quên đóng.
- Chọn isolation level phù hợp với nghiệp vụ thay vì mặc định dùng Serializable cho mọi thứ; Read Committed đủ cho phần lớn use case, chỉ nâng lên khi có bằng chứng race condition cụ thể.
- Với thao tác cần khóa dòng để tránh race condition (ví dụ kiểm tra rồi cập nhật số dư), dùng `SELECT ... FOR UPDATE` thay vì đọc rồi ghi mà không khóa.
- Với nghiệp vụ xuyên nhiều service/database, thiết kế Saga với compensating action rõ ràng ngay từ đầu, không cố "giả lập" transaction phân tán bằng cách gọi tuần tự không có cơ chế hoàn tác.

## Common Mistakes

- Mở transaction ở tầng thấp (repository) nhưng không kiểm soát được boundary thực sự ở tầng service, dẫn tới một request có nhiều transaction rời rạc thay vì một transaction bao trọn nghiệp vụ.
- Bắt exception rồi log lỗi nhưng quên gọi rollback, khiến connection trả về pool trong khi transaction vẫn treo, gây lock rò rỉ (lock leak) làm các request khác bị chặn.
- Đặt lệnh gọi API bên ngoài (email, webhook, thanh toán qua bên thứ ba) bên trong transaction — nếu API đó chậm hoặc timeout, transaction bị giữ lock rất lâu, kéo theo cascading timeout cho toàn hệ thống.
- Tin rằng `@Transactional` (hay tương đương ở framework khác) tự động bao mọi thứ, trong khi thực tế một số framework không propagate transaction qua lời gọi bất đồng bộ (async/thread mới), khiến phần code chạy sau nằm ngoài transaction mà không ai nhận ra.
- Nhầm lẫn transaction database với transaction phân tán — coi việc gọi tuần tự hai service khác database là "an toàn" trong khi không có compensating action nếu bước hai thất bại.

## Interview Questions

**Hỏi**: ACID là gì và Atomicity khác Isolation ở điểm nào?

**Trả lời**: ACID là bốn tính chất transaction phải đảm bảo — Atomicity (tất cả thao tác trong transaction hoặc xảy ra trọn vẹn hoặc không xảy ra gì), Consistency (dữ liệu luôn thỏa ràng buộc sau transaction), Isolation (transaction chạy song song không thấy trạng thái trung gian của nhau), Durability (đã commit thì không mất kể cả khi crash). Atomicity nói về việc một transaction có "thành công hết hay thất bại hết" hay không, còn Isolation nói về việc các transaction khác nhìn thấy transaction đang chạy dở như thế nào — hai khái niệm độc lập, một transaction có thể atomic nhưng vẫn bị dirty read nếu isolation level thấp.

**Hỏi**: Tại sao transaction dài (long-running transaction) lại nguy hiểm trong production?

**Trả lời**: Vì nó giữ lock trên các dòng/bảng bị đụng chạm trong suốt thời gian tồn tại, làm tăng contention và có thể gây deadlock; ngoài ra ở các DB dùng MVCC như PostgreSQL, transaction dài còn ngăn cơ chế vacuum dọn dẹp các phiên bản dòng cũ (dead tuples), khiến bảng phình to và hiệu năng suy giảm dần theo thời gian.

**Hỏi**: Làm sao xử lý "transaction" xuyên nhiều microservice khi mỗi service có database riêng?

**Trả lời**: Không dùng transaction ACID xuyên service được vì mỗi database quản lý transaction độc lập; giải pháp thực tế là Saga pattern — chuỗi local transaction ở từng service, mỗi bước có compensating action để hoàn tác nếu bước sau thất bại, thường điều phối qua message queue hoặc orchestrator, chấp nhận eventual consistency thay vì atomicity tức thời.

## Summary

Transaction là cơ chế database dùng để đảm bảo một nhóm thao tác đọc/ghi xảy ra trọn vẹn hoặc không xảy ra gì, thông qua ACID. Cơ chế bên trong dựa vào undo/redo log hoặc MVCC để theo dõi thay đổi chưa commit và cho phép rollback an toàn, còn durability được đảm bảo bằng fsync vào write-ahead log tại thời điểm commit. Trong ứng dụng thực tế, transaction boundary nên đặt ở tầng service, bao trọn đúng phạm vi nghiệp vụ cần atomic và giữ càng ngắn càng tốt để tránh lock contention. Với hệ phân tán nhiều database, transaction ACID không áp dụng được xuyên service, buộc phải chuyển sang Saga pattern với compensating action.

## Knowledge Graph

- Isolation Level — quyết định transaction song song thấy trạng thái của nhau tới mức nào, ảnh hưởng trực tiếp tới dirty read/phantom read.
- MVCC (Multi-Version Concurrency Control) — cơ chế PostgreSQL dùng để cho phép đọc không chặn ghi bằng cách giữ nhiều phiên bản dòng.
- Deadlock — hệ quả thường gặp khi nhiều transaction giữ lock chéo nhau, liên quan trực tiếp tới thời lượng và thứ tự thao tác trong transaction.
- Saga Pattern — giải pháp thay thế transaction ACID khi nghiệp vụ trải dài qua nhiều service/database khác nhau.
- Write-Ahead Log (WAL) — cơ chế đảm bảo Durability, transaction chỉ được coi là commit khi đã ghi WAL xuống đĩa.
- Missing WHERE Clause (knowledge/database/missing-where-clause.md) — sai sót logic bên trong một transaction vẫn commit thành công nếu không có kiểm tra, minh họa Atomicity không đồng nghĩa với đúng ý định nghiệp vụ.

## Five Things To Remember

- Transaction đảm bảo "tất cả hoặc không gì cả", không đảm bảo logic nghiệp vụ của bạn đúng.
- COMMIT chỉ an toàn khi đã fsync vào write-ahead log — đó là gốc rễ của Durability.
- Transaction càng dài, lock giữ càng lâu, rủi ro deadlock và contention càng cao.
- Không có transaction ACID thật sự xuyên nhiều database/service — dùng Saga với compensating action thay thế.
- Luôn đảm bảo rollback được gọi khi có lỗi, nếu không lock sẽ rò rỉ và chặn các request khác.
