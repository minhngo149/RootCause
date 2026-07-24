---
id: saga-pattern
title: Saga Pattern
tags: ["distributed-systems", "transactions"]
---

# Saga Pattern

> Status: Draft

## Problem

Một nghiệp vụ như "đặt hàng" trong hệ thống microservices thường chạm vào nhiều service độc lập — `order` tạo đơn, `payment` trừ tiền, `inventory` trừ tồn kho, `shipping` tạo vận đơn — mỗi service sở hữu database riêng. Trong monolith, toàn bộ thao tác này nằm trong một transaction ACID duy nhất, DB tự đảm bảo atomicity: hoặc tất cả commit, hoặc tất cả rollback. Khi tách thành nhiều service với nhiều DB riêng, không còn một transaction coordinator nào bao trùm được toàn bộ chuỗi thao tác — two-phase commit (2PC) trên lý thuyết có thể làm việc này nhưng khóa tài nguyên qua network trong lúc chờ coordinator, không chịu được throughput và availability yêu cầu của hệ thống production. Câu hỏi đặt ra là: nếu `payment` trừ tiền thành công nhưng `inventory` báo hết hàng, ai chịu trách nhiệm hoàn tiền, và làm sao đảm bảo hệ thống không kẹt ở trạng thái nửa vời (đơn hàng tồn tại, tiền đã trừ, hàng chưa trừ)?

## Pain Points

- Không có cơ chế bù trừ, một request giữa chừng thất bại (vd. `inventory` timeout) để lại dữ liệu không nhất quán vĩnh viễn giữa các service: tiền đã trừ ở `payment` nhưng đơn hàng không bao giờ được tạo hoàn chỉnh ở `order`.
- Đội vận hành phải xử lý thủ công hàng loạt ticket "khách bị trừ tiền nhưng không nhận được hàng" bằng cách dò log qua 4-5 service khác nhau, tốn hàng giờ mỗi lần thay vì hệ thống tự phục hồi.
- Dùng 2PC để giữ tính nhất quán mạnh khiến các participant phải giữ lock (vd. lock hàng tồn kho) suốt thời gian chờ coordinator quyết định, làm giảm throughput nghiêm trọng khi một trong các service chậm hoặc down — một service treo kéo cả chuỗi giao dịch treo theo.
- Không rõ trách nhiệm rollback thuộc về ai dẫn tới tình trạng mỗi team tự viết logic bù trừ ad-hoc, không nhất quán, thiếu idempotency, gây double-refund hoặc double-cancel khi retry.

## Solution

Saga là một mẫu quản lý giao dịch phân tán bằng cách chia một nghiệp vụ lớn thành chuỗi các **local transaction** — mỗi bước chỉ commit trong phạm vi một service/DB, dùng transaction ACID bình thường của service đó. Nếu một bước ở giữa chuỗi thất bại, saga không rollback theo kiểu DB truyền thống (vì không có transaction bao trùm để rollback) mà chạy một chuỗi **compensating action** — các thao tác nghiệp vụ ngược lại các bước đã thành công trước đó, theo thứ tự ngược, để đưa hệ thống về trạng thái nhất quán nghiệp vụ (không nhất thiết là trạng thái y hệt ban đầu). Đánh đổi cốt lõi: saga từ bỏ tính nhất quán mạnh (strong consistency, isolation) để đổi lấy tính sẵn sàng và khả năng mở rộng — hệ thống chấp nhận có khoảng thời gian dữ liệu ở trạng thái trung gian (eventual consistency) thay vì khóa toàn bộ tài nguyên chờ một quyết định tập trung.

## How It Works

Một saga là chuỗi các bước `T1, T2, ..., Tn`, mỗi `Ti` là một local transaction. Đi kèm mỗi `Ti` (trừ bước cuối) là một compensating transaction `Ci` chỉ được gọi khi có bước sau đó thất bại. Ví dụ chuỗi đặt hàng: `T1` = tạo order (status `PENDING`), `T2` = trừ tiền ở payment, `T3` = trừ tồn kho, `T4` = tạo vận đơn. Nếu `T3` thất bại (hết hàng), saga chạy `C2` (hoàn tiền) rồi `C1` (huỷ order, chuyển status `CANCELLED`) — theo đúng thứ tự ngược lại các bước đã commit thành công, không đụng tới `T3`/`T4` vì chúng chưa từng chạy thành công.

Có hai cách điều phối chuỗi này:

- **Choreography**: không có nhạc trưởng, mỗi service tự lắng nghe event từ service trước và tự quyết định hành động tiếp theo, publish event của chính nó lên message broker (Kafka, RabbitMQ). `order` publish `OrderCreated` → `payment` lắng nghe, xử lý, publish `PaymentCompleted` hoặc `PaymentFailed` → `inventory` lắng nghe `PaymentCompleted`, xử lý, publish `InventoryReserved` hoặc `InventoryFailed` → mỗi service tự biết cần compensate khi thấy event `*Failed` của bước liền sau. Ưu điểm là không có single point of failure, các service độc lập hoàn toàn; nhược điểm là không ai nhìn thấy toàn cảnh chuỗi giao dịch, muốn biết đơn hàng #123 đang ở bước nào phải trace qua log của 4 service khác nhau, và thêm một bước mới vào giữa chuỗi đòi sửa logic lắng nghe ở nhiều service cùng lúc.
- **Orchestration**: có một orchestrator (saga coordinator) trung tâm, gọi tuần tự từng service, tự quản lý state machine của cả chuỗi và tự quyết định khi nào gọi compensating action. Orchestrator gọi `payment.charge()`, nhận kết quả, nếu thành công thì gọi `inventory.reserve()`, nếu bước này fail thì orchestrator tự gọi `payment.refund()`. Ưu điểm là logic điều phối tập trung một chỗ, dễ trace, dễ thêm bước mới (chỉ sửa orchestrator); nhược điểm là orchestrator trở thành điểm phối hợp bắt buộc phải có mặt và đúng — nó không phải single point of failure theo nghĩa cứng (có thể chạy nhiều instance, lưu state ngoài), nhưng là nơi tập trung độ phức tạp nghiệp vụ, và các service tham gia phải expose API cho orchestrator gọi thay vì chỉ publish event.

Một điểm bắt buộc cho cả hai mô hình: mọi bước `Ti` và `Ci` phải **idempotent**, vì network timeout khiến orchestrator hoặc consumer có thể gọi lại cùng một bước nhiều lần (retry) — nếu `payment.charge()` không idempotent, retry sau timeout có thể trừ tiền hai lần dù request đầu thực ra đã thành công.

## Production Architecture

Trong một hệ thống thương mại điện tử, saga orchestration thường triển khai bằng workflow engine chuyên dụng (Temporal, Camunda, AWS Step Functions, hoặc Netflix Conductor) thay vì tự viết state machine — engine này đảm nhiệm việc lưu trạng thái saga bền vững (durable execution), tự động retry theo policy cấu hình, và tiếp tục đúng chỗ dở dang nếu orchestrator process chết giữa chừng. Saga log (bảng `saga_instances` lưu step hiện tại, trạng thái, payload) được ghi vào DB riêng của orchestrator, tách biệt với DB nghiệp vụ của từng service, để có thể replay hoặc audit lại toàn bộ lịch sử giao dịch. Với choreography, event thường đi qua Kafka với schema registry (Avro/Protobuf) để đảm bảo hợp đồng event ổn định giữa các team, và mỗi consumer group xử lý event của mình với offset commit sau khi transaction cục bộ + compensating logic hoàn tất, tránh mất event khi consumer crash giữa chừng. Nhiều hệ thống production kết hợp cả hai: dùng orchestration cho luồng nghiệp vụ lõi (checkout, thanh toán) cần audit chặt và dễ debug, dùng choreography cho các luồng phụ (gửi email xác nhận, cập nhật analytics) không cần đồng bộ chặt với luồng chính.

## Trade-offs

Saga đánh đổi tính nhất quán mạnh lấy tính sẵn sàng: trong khoảng thời gian giữa các bước, hệ thống ở trạng thái tạm thời không nhất quán (order tồn tại nhưng chưa có vận đơn) mà mọi client đọc dữ liệu trong khoảng này phải chấp nhận nhìn thấy trạng thái trung gian — không có isolation như transaction ACID. Compensating action không phải là rollback thật: nếu `T3` đã gửi email xác nhận cho khách trước khi `T4` fail, không thể "un-send" email, saga chỉ có thể gửi thêm một email khác báo huỷ — nghĩa là thiết kế compensating logic đòi hỏi tư duy nghiệp vụ, không phải chỉ đảo ngược thao tác kỹ thuật. Choreography giảm coupling nhưng tăng độ khó debug và dễ tạo ra cyclic dependency ẩn giữa các service qua event nếu không thiết kế cẩn thận; orchestration dễ trace nhưng tập trung độ phức tạp và tạo coupling giữa orchestrator với API của từng service tham gia. Ngoài ra, sagas dài (nhiều bước) tăng xác suất một bước nào đó fail giữa chừng, kéo theo chuỗi compensating dài — mỗi compensating action cũng có thể fail, đòi hỏi retry with backoff và đôi khi cần can thiệp thủ công (dead-letter cho saga không tự phục hồi được).

## Best Practices

- Thiết kế mọi local transaction và compensating action idempotent ngay từ đầu, vì retry sau timeout/crash là điều chắc chắn sẽ xảy ra trong production.
- Lưu trạng thái saga (step hiện tại, kết quả từng bước) bền vững ở một nơi tách biệt để có thể resume đúng chỗ khi orchestrator hoặc consumer crash giữa chừng.
- Dùng correlation ID xuyên suốt toàn bộ chuỗi (order ID hoặc saga instance ID) để trace được một giao dịch qua log của nhiều service.
- Với choreography, định nghĩa rõ event schema (versioned, backward-compatible) qua schema registry để tránh vỡ hợp đồng khi một service thay đổi event mà không báo các consumer khác.
- Với các bước không thể compensate hoàn hảo (đã gửi email, đã in hoá đơn), thiết kế rõ hành vi thay thế (gửi thông báo huỷ) thay vì bỏ qua bước compensate và để lại trạng thái mập mờ cho khách hàng.

## Common Mistakes

- Viết compensating action không idempotent, dẫn tới hoàn tiền hai lần hoặc huỷ đơn hai lần khi orchestrator retry sau timeout.
- Coi saga tương đương transaction ACID và mong đợi isolation — code phía trên đọc dữ liệu giữa các bước saga mà không xử lý trường hợp thấy trạng thái trung gian, gây hiển thị sai cho user (vd. hiển thị "đã thanh toán" trước khi tồn kho được xác nhận).
- Thiết kế saga quá dài (7-8 bước) mà không tách nhỏ thành các saga con, khiến một lỗi ở bước cuối kéo theo chuỗi compensate dài và khó debug khi có sự cố.
- Với choreography, không version hoá event schema, khiến một service đổi format event làm vỡ toàn bộ consumer khác mà không ai phát hiện kịp thời (silent failure).
- Không xử lý trường hợp chính compensating action fail — thiếu retry with backoff và dead-letter queue cho saga "kẹt", khiến hệ thống âm thầm giữ dữ liệu không nhất quán vô thời hạn.

## Interview Questions

**Hỏi**: Saga khác gì với 2-phase commit (2PC), và vì sao microservices thường chọn saga thay vì 2PC?

**Trả lời**: 2PC dùng một coordinator giữ lock trên tất cả participant cho tới khi mọi bên xác nhận sẵn sàng commit, đảm bảo strong consistency nhưng khoá tài nguyên qua network trong lúc chờ, giảm throughput và availability nghiêm trọng khi một participant chậm/down. Saga chia nghiệp vụ thành các local transaction độc lập, mỗi bước commit ngay không chờ các bước khác, đổi lấy eventual consistency và dùng compensating action để xử lý khi có bước thất bại giữa chừng — phù hợp hơn với microservices vì không đòi hỏi coordinator giữ lock phân tán liên service.

**Hỏi**: Choreography và orchestration khác nhau ở điểm nào, và khi nào nên chọn cái nào?

**Trả lời**: Choreography để mỗi service tự lắng nghe event và tự quyết định hành động kế tiếp, không có nhạc trưởng trung tâm — phù hợp cho chuỗi ngắn, ít bước, ưu tiên loose coupling. Orchestration có một coordinator trung tâm gọi tuần tự từng service và tự quản lý toàn bộ state machine — phù hợp cho chuỗi dài, cần audit chặt và dễ trace, chấp nhận đánh đổi coupling giữa orchestrator và API các service tham gia.

**Hỏi**: Vì sao mọi bước trong saga bắt buộc phải idempotent?

**Trả lời**: Vì network không đáng tin cậy, orchestrator hoặc message consumer có thể gọi lại cùng một bước nhiều lần sau timeout dù request trước đó thực ra đã xử lý thành công ở phía service nhận — nếu bước đó không idempotent, retry sẽ gây tác dụng phụ trùng lặp (trừ tiền hai lần, trừ tồn kho hai lần), biến cơ chế phục hồi lỗi thành nguồn gây lỗi mới.

## Summary

Saga giải quyết bài toán giao dịch phân tán qua nhiều service/DB độc lập bằng cách chia nghiệp vụ lớn thành chuỗi local transaction, mỗi bước commit ngay trong phạm vi service của nó, thay vì dùng một transaction coordinator bao trùm như 2PC. Khi một bước giữa chừng thất bại, saga chạy compensating action theo thứ tự ngược để đưa hệ thống về trạng thái nhất quán nghiệp vụ, chấp nhận đánh đổi strong consistency lấy availability và eventual consistency. Có hai mô hình điều phối: choreography (các service tự lắng nghe event, không nhạc trưởng, loose coupling nhưng khó trace) và orchestration (coordinator trung tâm quản lý state machine, dễ trace nhưng tập trung độ phức tạp). Idempotency ở mọi bước là điều kiện bắt buộc vì retry sau timeout là chuyện chắc chắn xảy ra trong production. Trong thực tế, nhiều hệ thống kết hợp cả hai mô hình tuỳ theo mức độ quan trọng và độ dài của từng luồng nghiệp vụ.

## Knowledge Graph

- Two-Phase Commit (2PC) — cơ chế giao dịch phân tán strong consistency mà saga thay thế để đổi lấy availability.
- Eventual Consistency — mô hình nhất quán mà saga chấp nhận trong khoảng thời gian giữa các bước chưa hoàn tất.
- Idempotency — điều kiện bắt buộc cho mọi local transaction và compensating action để retry an toàn.
- Event Sourcing — thường đi cùng choreography saga, dùng event log làm nguồn sự thật cho trạng thái chuỗi giao dịch.
- Circuit Breaker — cơ chế bảo vệ từng lời gọi trong một bước saga khi service đó đang lỗi hàng loạt.
- Outbox Pattern — đảm bảo publish event và commit local transaction là atomic trong cùng một service, tránh mất event khi service crash ngay sau commit.

## Five Things To Remember

- Saga thay thế transaction ACID phân tán bằng chuỗi local transaction cộng compensating action, không có transaction bao trùm thật sự.
- Compensating action là bù trừ nghiệp vụ, không phải rollback kỹ thuật — một số hành động (đã gửi email, đã in hoá đơn) không thể "undo" mà chỉ có thể bù bằng hành động khác.
- Choreography loại bỏ nhạc trưởng trung tâm nhưng khó trace; orchestration dễ trace nhưng tập trung độ phức tạp vào coordinator.
- Mọi bước saga phải idempotent vì retry sau timeout là điều chắc chắn xảy ra trong hệ thống phân tán.
- Saga đổi strong consistency lấy availability — hệ thống phải chấp nhận có khoảng thời gian dữ liệu ở trạng thái trung gian.
