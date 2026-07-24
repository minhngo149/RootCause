---
id: two-phase-commit
title: Two-Phase Commit
tags: ["distributed-systems", "transactions"]
---

# Two-Phase Commit

> Status: Draft

## Problem

Một hệ thống đặt vé máy bay cần trừ tiền ở service `payment`, giữ chỗ ở service `booking`, và cập nhật tồn kho ghế ở service `inventory` — ba service, ba database riêng biệt, nhưng nghiệp vụ yêu cầu cả ba phải thành công cùng nhau hoặc không thành công cái nào cả. Nếu chỉ gọi tuần tự ba API và một bước giữa chừng thất bại (vd. `inventory` hết ghế sau khi `payment` đã trừ tiền thành công), hệ thống rơi vào trạng thái nửa vời: tiền đã trừ nhưng không có vé, và không có cơ chế nào tự động phát hiện hay sửa sai lệch đó. Two-Phase Commit (2PC) ra đời để giải quyết đúng vấn đề này — đảm bảo một transaction trải dài qua nhiều node/database độc lập hoặc commit toàn bộ, hoặc rollback toàn bộ, không có trạng thái lửng lơ ở giữa.

## Pain Points

- Không có cơ chế đồng bộ commit, một service thất bại giữa chừng để lại dữ liệu không nhất quán vĩnh viễn giữa các hệ thống — tiền bị trừ nhưng đơn hàng không tồn tại, và việc phát hiện ra sai lệch này thường chỉ đến từ khiếu nại của khách hàng, không phải từ hệ thống tự báo.
- Đội vận hành phải viết script reconciliation chạy định kỳ (cron job so khớp dữ liệu giữa các service) để dò tìm và vá thủ công các giao dịch dở dang — một khoản chi phí kỹ thuật âm ỉ, tốn nhân lực, và luôn trễ so với thời điểm lỗi thực sự xảy ra.
- Khi coordinator (thành phần điều phối transaction) chết đúng lúc một số participant đã lock tài nguyên chờ lệnh commit, các participant đó bị kẹt vô thời hạn — connection, row lock, hoặc resource bị giữ không giải phóng, kéo theo timeout dây chuyền cho mọi request khác cần chạm vào tài nguyên đó.
- Thiếu một giao thức rõ ràng, mỗi team tự chế ra cách xử lý riêng (retry mù quáng, compensating call viết tay không đầy đủ), dẫn tới hành vi không đồng nhất giữa các luồng nghiệp vụ và rất khó audit khi có sự cố.

## Solution

Two-Phase Commit là một giao thức đồng thuận cho phép một coordinator điều phối nhiều participant (mỗi participant thường là một database hoặc resource manager riêng biệt) để cùng commit hoặc cùng rollback một transaction phân tán, chia làm hai pha rõ rệt: **Prepare** (hỏi ý kiến tất cả participant xem có sẵn sàng commit không, và nếu có thì khóa tài nguyên lại) và **Commit** (ra lệnh commit thật sự chỉ khi tất cả đã đồng ý). Ý tưởng cốt lõi là không có participant nào được commit đơn phương — mọi quyết định cuối cùng đều đi qua coordinator, và participant chỉ hành động khi nhận lệnh rõ ràng ở pha hai. Đây là nền tảng cho XA transaction trong JTA/JDBC và cho các distributed transaction manager cổ điển, dù ngày nay thường được thay bằng các mô hình nhẹ hơn (Saga, outbox pattern) trong microservices vì lý do sẽ nói ở phần Trade-offs.

## How It Works

**Pha 1 — Prepare (voting phase):** Coordinator gửi thông điệp `PREPARE` tới tất cả participant. Mỗi participant thực hiện toàn bộ công việc của transaction cục bộ (ghi vào undo/redo log, kiểm tra ràng buộc, khóa các row/resource liên quan) nhưng **chưa commit thật** — nó chỉ đưa transaction vào trạng thái "in-doubt", ghi log để có thể phục hồi sau crash, rồi trả lời coordinator bằng `YES` (sẵn sàng commit, cam kết sẽ commit nếu được yêu cầu) hoặc `NO` (không thể commit, vd. vi phạm constraint hoặc hết resource). Điểm mấu chốt: một khi participant đã trả lời `YES`, nó **bắt buộc phải giữ lock và chờ lệnh cuối cùng từ coordinator** — nó không còn quyền tự ý rollback hay commit một mình nữa, dù có bị restart cũng phải đọc lại log để khôi phục đúng trạng thái in-doubt này.

**Pha 2 — Commit/Abort (decision phase):** Coordinator thu thập câu trả lời từ tất cả participant. Nếu **tất cả** trả lời `YES`, coordinator ghi quyết định "commit" vào log bền vững của chính nó (đây là **commit point** — thời điểm quyết định trở thành chung cuộc), rồi gửi `COMMIT` tới toàn bộ participant. Nếu **bất kỳ** participant nào trả lời `NO` (hoặc timeout không phản hồi), coordinator gửi `ABORT` tới toàn bộ participant còn lại. Mỗi participant nhận lệnh, thực hiện commit hoặc rollback thật sự, giải phóng lock, rồi gửi `ACK` xác nhận đã hoàn tất về coordinator. Coordinator chỉ coi transaction kết thúc khi nhận đủ `ACK` từ tất cả participant; nếu thiếu, nó phải retry gửi lại lệnh cho tới khi nhận được xác nhận, vì participant có thể đã miss message do lỗi mạng thoáng qua.

**Vấn đề blocking khi coordinator chết:** Đây là điểm yếu chí mạng của 2PC cổ điển (không có timeout/recovery protocol bổ sung). Giả sử coordinator gửi `PREPARE`, mọi participant trả lời `YES` và đang giữ lock chờ lệnh cuối, nhưng ngay lúc đó coordinator crash trước khi kịp gửi `COMMIT` hoặc `ABORT`. Mỗi participant lúc này ở trạng thái in-doubt: nó biết mình đã cam kết `YES` nên không được tự ý abort (vì có thể các participant khác đã nhận lệnh `COMMIT` rồi), nhưng cũng không có thông tin để tự commit (vì có thể một participant khác đã trả lời `NO`). Participant chỉ còn cách **chờ vô thời hạn** cho tới khi coordinator sống lại và đọc log để biết quyết định thật sự là gì — trong lúc chờ, mọi lock nó giữ (row lock, table lock) vẫn nguyên vẹn, chặn mọi transaction khác cần chạm vào cùng dữ liệu. Đây gọi là **blocking problem** của 2PC: một điểm lỗi đơn (coordinator) có thể khiến toàn bộ cluster tham gia transaction đó bị treo, và thời gian treo phụ thuộc hoàn toàn vào thời gian coordinator hồi phục — có thể là vài giây, có thể là vài giờ nếu cần can thiệp thủ công. Ba-Phase Commit (3PC) được đề xuất để giảm blocking bằng cách thêm một pha trung gian (`pre-commit`) và cho phép participant tự suy luận quyết định dựa trên timeout, nhưng đổi lại thêm độ trễ và vẫn không loại bỏ hoàn toàn blocking trong điều kiện network partition bất đối xứng — vì vậy trong thực tế ít được dùng, và ngành nghiêng hẳn về các mô hình khác (Saga, consensus-based coordinator có replication) thay vì cố vá 2PC.

## Production Architecture

2PC xuất hiện rõ nhất trong các hệ quản trị giao dịch cổ điển: XA transaction trong Java (JTA — Java Transaction API) cho phép một transaction trải dài qua nhiều resource manager (vd. một Oracle DB và một MQ broker JMS) với một Transaction Manager (như Atomikos, Bitronix, hoặc built-in trong application server WebLogic/JBoss) đóng vai trò coordinator theo đúng giao thức Prepare/Commit ở trên. Trong hệ database phân tán hiện đại, 2PC vẫn là cơ chế nền cho cross-shard transaction — CockroachDB và Google Spanner dùng biến thể 2PC kết hợp với Raft/Paxos ở tầng participant để mỗi "participant" trong 2PC thực chất là một Raft group có khả năng chịu lỗi, giảm hẳn rủi ro blocking vì coordinator (transaction coordinator, chính nó cũng chạy trên Raft) hiếm khi là single point of failure theo nghĩa cổ điển. MySQL XA transaction hỗ trợ 2PC ở mức storage engine, thường thấy trong kiến trúc phân mảnh dữ liệu (sharding) khi một transaction nghiệp vụ phải ghi vào hai shard khác nhau cùng lúc. Trong kiến trúc microservices hiện đại (không dùng database dùng chung), 2PC gần như bị loại bỏ hoàn toàn khỏi tầng application-to-application — thay vào đó dùng Saga pattern (chuỗi các local transaction với compensating action) hoặc transactional outbox + message queue, chính vì vấn đề blocking và độ khớp nối chặt (tight coupling) mà 2PC đòi hỏi giữa các service không phù hợp với triết lý độc lập triển khai của microservices.

## Trade-offs

2PC đổi lấy tính đúng đắn tuyệt đối (atomicity thật sự qua nhiều node) bằng một coordinator duy nhất trở thành single point of failure và single point of blocking — nếu coordinator chết ở thời điểm xấu nhất, mọi participant treo cùng lúc, và mức độ nghiêm trọng tỷ lệ thuận với số lượng participant tham gia. Giao thức đòi hỏi mọi participant phải **sẵn sàng chờ** (blocking synchronous) trong toàn bộ thời gian giao dịch, nghĩa là latency của cả transaction bị quyết định bởi participant chậm nhất, và throughput toàn hệ thống giảm mạnh khi số participant tăng vì lock được giữ lâu hơn. So với Saga (dùng compensating transaction để "undo" theo nghiệp vụ thay vì rollback database thật), 2PC cho consistency mạnh hơn (participant không bao giờ thấy trạng thái trung gian ra bên ngoài) nhưng đổi lại coupling chặt hơn nhiều — mọi participant phải cùng tham gia một giao thức đồng bộ, cùng hỗ trợ XA, và không thể triển khai độc lập theo lịch riêng như microservices thường muốn. 3PC giảm blocking window nhưng thêm một round-trip network nữa (tăng latency) và vẫn không giải quyết được blocking hoàn toàn khi có network partition, nên trong thực tế phần lớn hệ thống chọn chấp nhận eventual consistency (Saga, outbox) hơn là cố gắng vá 2PC.

## Best Practices

- Giới hạn phạm vi transaction 2PC càng hẹp và càng ngắn càng tốt — càng nhiều participant và thời gian giữ lock càng lâu, rủi ro blocking khi coordinator chết càng cao.
- Coordinator phải ghi log bền vững (write-ahead log, ghi disk trước khi gửi message) tại mỗi bước quyết định quan trọng (đặc biệt tại commit point) để có thể phục hồi đúng trạng thái sau crash, không dựa vào memory.
- Triển khai coordinator có khả năng chịu lỗi thật sự (chạy trên cụm với leader election, như transaction coordinator trong Spanner/CockroachDB chạy trên Raft) thay vì một tiến trình đơn lẻ không có failover.
- Với hệ thống mới, cân nhắc Saga pattern hoặc transactional outbox trước khi chọn 2PC cổ điển — chỉ dùng 2PC khi thực sự cần strong consistency qua nhiều resource manager và các participant có thể chấp nhận coupling chặt.
- Giám sát và alert riêng cho các transaction ở trạng thái in-doubt kéo dài (participant đã prepare nhưng chưa nhận lệnh cuối) — đây là tín hiệu sớm của coordinator gặp sự cố, cần can thiệp trước khi lock lan rộng gây timeout dây chuyền.

## Common Mistakes

- Tự triển khai 2PC thủ công giữa các microservices qua HTTP request tuần tự mà không có coordinator log bền vững — khi một request giữa chừng thất bại, không có cách nào phục hồi đúng trạng thái, biến "2PC tự chế" thành một nguồn lỗi mới thay vì giải pháp.
- Đặt coordinator chạy trên một instance đơn không có failover, coi nhẹ rủi ro single point of failure vì "hiếm khi coordinator chết" — cho tới khi nó chết đúng lúc đang giữ hàng trăm transaction ở trạng thái in-doubt.
- Giữ transaction 2PC mở quá lâu (chờ input từ user, chờ một service bên thứ ba chậm) trong khi lock đã được giữ từ pha Prepare — kéo dài thời gian blocking không cần thiết và tăng khả năng deadlock chéo giữa các transaction.
- Nhầm 2PC với distributed lock hoặc với eventual consistency — 2PC đảm bảo atomicity đồng bộ (tất cả cùng thấy commit hoặc cùng thấy abort tại cùng thời điểm), trong khi Saga/outbox chỉ đảm bảo hội tụ cuối cùng, hai mô hình giải quyết bài toán khác nhau và không thể thay thế nhau ngầm định.
- Không viết recovery logic cho participant khi restart giữa lúc đang ở trạng thái in-doubt — participant phải đọc lại log để biết mình đang chờ quyết định gì, nếu không sẽ tự ý commit hoặc rollback sai, phá vỡ đúng tính chất atomicity mà 2PC được thiết kế để bảo vệ.

## Interview Questions

**Hỏi**: Tại sao 2PC gọi là "blocking protocol", và blocking xảy ra chính xác ở bước nào?

**Trả lời**: Blocking xảy ra khi coordinator chết sau khi đã nhận đủ `YES` từ tất cả participant (kết thúc pha Prepare) nhưng trước khi gửi được `COMMIT`/`ABORT` (pha 2). Lúc đó participant đã cam kết `YES` nên không được tự ý rollback (participant khác có thể đã nhận lệnh commit), cũng không có đủ thông tin để tự commit — nó buộc phải giữ nguyên lock và chờ coordinator hồi phục, có thể vô thời hạn.

**Hỏi**: 3PC giải quyết blocking problem của 2PC như thế nào, và tại sao nó vẫn ít được dùng trong thực tế?

**Trả lời**: 3PC thêm một pha trung gian (`pre-commit`) giữa Prepare và Commit, cho phép participant dùng timeout để tự suy luận quyết định an toàn (nếu đã nhận pre-commit mà coordinator không phản hồi, participant có thể tự commit vì biết mọi participant khác cũng đã qua bước này). Tuy nhiên 3PC vẫn có thể blocking trong trường hợp network partition bất đối xứng (participant không phân biệt được coordinator chết hay chỉ mất kết nối), và thêm một round-trip mạng làm tăng latency, nên phần lớn hệ thống thực tế chọn Saga/outbox thay vì đầu tư vào 3PC.

**Hỏi**: Khi nào nên chọn 2PC thay vì Saga pattern cho một distributed transaction?

**Trả lời**: Chọn 2PC khi cần strong consistency thật sự — mọi bên tham gia phải cùng thấy trạng thái commit hoặc cùng thấy abort tại cùng một thời điểm, không được để lộ trạng thái trung gian ra ngoài (vd. giao dịch tài chính nội bộ giữa các database cùng một tổ chức, cross-shard transaction trong cùng một hệ quản trị database). Chọn Saga khi các participant là các service độc lập, triển khai riêng, chấp nhận eventual consistency và có thể định nghĩa được compensating action rõ ràng cho từng bước — phù hợp hơn với kiến trúc microservices vì tránh được coupling chặt và blocking mà 2PC đòi hỏi.

## Summary

Two-Phase Commit là giao thức chia một distributed transaction thành pha Prepare (hỏi ý kiến và khóa tài nguyên ở tất cả participant) và pha Commit (ra lệnh commit hoặc abort dựa trên kết quả bỏ phiếu), đảm bảo atomicity thật sự qua nhiều node độc lập. Điểm yếu cốt lõi là blocking problem: nếu coordinator chết sau khi participant đã trả lời `YES` nhưng trước khi gửi được quyết định cuối cùng, các participant bị kẹt giữ lock vô thời hạn chờ coordinator hồi phục. 3PC giảm nhưng không loại bỏ hoàn toàn blocking, với cái giá là thêm độ trễ. Trong production, 2PC vẫn tồn tại ở tầng database phân tán (XA transaction, cross-shard commit trong Spanner/CockroachDB) nhưng đã bị thay thế phần lớn bởi Saga pattern và transactional outbox trong kiến trúc microservices vì lý do coupling và blocking. Lựa chọn giữa 2PC và các mô hình nhẹ hơn phụ thuộc vào việc hệ thống có thực sự cần strong consistency đồng bộ hay có thể chấp nhận eventual consistency để đổi lấy tính độc lập và khả năng chịu lỗi tốt hơn.

## Knowledge Graph

- Consensus (Raft, Paxos) — thuật toán đồng thuận thường được dùng để làm cho chính coordinator hoặc participant trong 2PC có khả năng chịu lỗi, tránh single point of failure.
- Saga Pattern — mô hình thay thế 2PC trong microservices, dùng compensating transaction thay vì rollback đồng bộ, đánh đổi strong consistency lấy khả năng triển khai độc lập.
- CAP Theorem — 2PC về bản chất thiên về CP (ưu tiên consistency, chấp nhận mất availability khi coordinator hoặc participant không phản hồi được).
- Leader Election — cơ chế cần thiết để coordinator trong 2PC có failover thật sự thay vì là một tiến trình đơn lẻ dễ vỡ.
- Distributed Lock — participant trong pha Prepare về bản chất đang giữ một dạng lock phân tán, và blocking problem của 2PC chính là hệ quả của lock đó không được giải phóng đúng lúc.
- Write-Ahead Log — cơ chế bắt buộc để coordinator và participant có thể phục hồi đúng trạng thái transaction sau crash.

## Five Things To Remember

- 2PC có hai pha rõ rệt: Prepare (khóa và hỏi ý kiến) và Commit (ra lệnh dựa trên kết quả bỏ phiếu tất cả đồng ý).
- Một khi participant trả lời `YES` ở pha Prepare, nó bắt buộc giữ lock chờ lệnh cuối, không được tự ý quyết định.
- Coordinator chết giữa hai pha là nguyên nhân trực tiếp của blocking problem — participant treo vô thời hạn chờ hồi phục.
- 3PC giảm blocking bằng một pha trung gian nhưng thêm latency và không loại bỏ blocking hoàn toàn khi có network partition.
- Trong microservices hiện đại, Saga và transactional outbox thường được chọn thay 2PC để tránh coupling chặt và rủi ro blocking.
