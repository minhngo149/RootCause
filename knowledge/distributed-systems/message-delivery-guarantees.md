---
id: message-delivery-guarantees
title: Message Delivery Guarantees
tags: ["distributed-systems", "messaging"]
---

# Message Delivery Guarantees

> Status: Draft

## Problem

Khi một service publish message qua Kafka, RabbitMQ, SQS hay bất kỳ message broker nào, network và process đều có thể fail giữa chừng — producer gửi xong nhưng không nhận được ack, consumer xử lý xong nhưng crash trước khi commit offset, broker restart giữa lúc đang deliver. Đội ngũ engineering thường mặc định broker sẽ "lo hết" và message sẽ đến đúng một lần, nhưng đó là một giả định sai gây ra bug production nghiêm trọng nhất trong hệ thống message-driven: double-charge, mất order, duplicate email. Vấn đề cốt lõi là: hệ thống phân tán không thể đồng thời đảm bảo network reliable, process reliable, và deliver đúng một lần — buộc phải chọn một trong ba mô hình delivery và thiết kế consumer tương ứng.

## Pain Points

- Duplicate processing: consumer nhận cùng một message hai lần (do retry sau timeout) và trừ tiền khách hàng hai lần nếu logic không idempotent — đây là nguyên nhân phổ biến của incident "double charge" trong hệ thống payment.
- Mất message: producer nghĩ đã gửi thành công (fire-and-forget) nhưng broker chưa persist, mất message khi broker crash — dữ liệu audit log hoặc event analytics bị thiếu vĩnh viễn, không thể phát hiện ngay vì hệ thống vẫn "chạy bình thường".
- Order bị đảo: retry logic đơn giản re-enqueue message vào cuối queue thay vì đúng vị trí, làm sai thứ tự các event phụ thuộc nhau (ví dụ `order_created` xử lý sau `order_shipped`).
- Chi phí vận hành tăng: đội SRE phải viết script reconciliation thủ công để dò duplicate/missing record sau mỗi incident, tốn hàng giờ mỗi lần thay vì xử lý ngay ở tầng thiết kế.

## Solution

Có ba mô hình delivery guarantee: **at-most-once** (gửi tối đa một lần, chấp nhận mất message, không retry), **at-least-once** (retry cho đến khi có ack, chấp nhận duplicate), và **exactly-once** (mỗi message được xử lý đúng một lần, không mất không trùng). Về mặt lý thuyết phân tán (liên quan Two Generals' Problem), exactly-once delivery thuần túy ở tầng network là bất khả thi khi không có shared state giữa producer và consumer. Giải pháp thực dụng và được dùng rộng rãi trong production là: xây dựng trên nền **at-least-once delivery + idempotency ở tầng xử lý (consumer hoặc storage)** để đạt hiệu ứng "exactly-once processing" — message có thể đến nhiều lần, nhưng effect chỉ xảy ra một lần.

## How It Works

At-most-once: producer gửi message và không chờ ack, hoặc consumer commit offset trước khi xử lý xong — nếu crash giữa chừng, message mất vĩnh viễn nhưng không bao giờ duplicate. Đây là mặc định của UDP-style fire-and-forget hoặc Kafka producer với `acks=0`.

At-least-once: producer chờ ack từ broker trước khi coi là thành công, và consumer chỉ commit offset (hoặc ack message) SAU KHI xử lý xong toàn bộ side-effect. Nếu network timeout xảy ra ở bất kỳ bước nào — producer không biết broker đã nhận hay chưa, consumer crash sau khi xử lý nhưng trước khi commit — bên gửi sẽ retry. Retry là nguồn gốc của duplicate: broker có thể đã nhận message lần đầu nhưng ack bị mất trên đường về, producer retry gửi lần hai, broker giờ có hai bản copy của cùng một message.

Exactly-once processing (không phải exactly-once delivery) được dựng bằng cách kết hợp at-least-once với một trong các cơ chế idempotency: (1) deduplication key — consumer lưu message ID đã xử lý (trong Redis, DB table `processed_messages`, hoặc dùng unique constraint) và bỏ qua nếu đã thấy; (2) idempotent write tự nhiên — dùng `INSERT ... ON CONFLICT DO NOTHING`, `UPDATE SET status = 'shipped' WHERE status != 'shipped'`, hoặc thiết kế state machine mà việc áp dụng lại cùng một transition không đổi kết quả; (3) transactional outbox + idempotency key xuyên suốt toàn bộ chain (ví dụ Stripe idempotency key ở API layer). Kafka's exactly-once semantics (EOS, từ 0.11+) là một trường hợp đặc biệt: nó dùng producer idempotence (sequence number per partition, broker dedupe theo `producer_id + sequence`) kết hợp transactional writes để đảm bảo exactly-once TRONG PHẠM VI Kafka-to-Kafka (consume-transform-produce), nhưng nếu consumer cuối cùng ghi ra một external system (DB, HTTP call) thì vẫn cần tự implement idempotency ở đó — EOS của Kafka không lan ra ngoài Kafka.

## Production Architecture

Trong một hệ thống order processing thực tế: API gateway nhận request tạo order, ghi vào DB và publish event `order.created` vào Kafka trong cùng transaction (transactional outbox pattern) để tránh dual-write inconsistency. Consumer (inventory service) đọc event với `acks=all` và `enable.auto.commit=false`, xử lý trừ kho, rồi mới commit offset — nếu consumer crash giữa lúc trừ kho và commit, Kafka rebalance sẽ redeliver message này cho consumer khác, và bản ghi `processed_event_ids` (unique index trên `event_id` trong cùng DB transaction với thao tác trừ kho) đảm bảo trừ kho không bị lặp. Ở tầng downstream xa hơn — gọi ra payment gateway bên thứ ba như Stripe — service tự sinh `idempotency_key = order_id + event_version` và truyền cho Stripe API, để dù retry ở tầng network HTTP cũng không tạo charge trùng. Toàn bộ chain "at-least-once + idempotency key ở mỗi hop" là kiến trúc chuẩn cho các hệ thống fintech, e-commerce order pipeline.

## Trade-offs

At-most-once đơn giản, latency thấp, không cần state lưu dedupe key, nhưng chấp nhận mất dữ liệu — chỉ chấp nhận được cho use case như metrics/telemetry không critical. At-least-once đảm bảo không mất dữ liệu nhưng đẩy toàn bộ gánh nặng "xử lý an toàn" sang consumer — nếu consumer không idempotent thì hệ thống sai, và việc lưu dedupe key (kể cả TTL ngắn) tốn thêm storage, thêm một round-trip kiểm tra trước mỗi lần xử lý, làm tăng latency. Kafka EOS (transactional) giảm được việc tự implement idempotency logic nhưng giảm throughput đáng kể (thường 20-30% do transaction coordinator overhead) và chỉ có hiệu lực trong phạm vi Kafka, không bảo vệ được side-effect ra ngoài (gọi API, ghi DB khác). Không có lựa chọn nào miễn phí — chọn mô hình nào phải khớp với mức độ chấp nhận rủi ro của business logic tương ứng.

## Best Practices

- Mặc định thiết kế mọi consumer theo at-least-once + idempotent write, đừng cố tin vào "exactly-once" ở tầng network.
- Dùng idempotency key sinh từ business logic (ví dụ `order_id + operation_type`) thay vì message ID của broker, vì broker message ID có thể đổi khi replay từ DLQ hoặc reprocessing thủ công.
- Đặt commit offset / ack SAU KHI toàn bộ side-effect (DB write, external API call) đã thành công, không commit trước.
- Dùng unique constraint ở tầng DB làm lớp bảo vệ cuối cùng, không chỉ dựa vào check-then-act ở application layer (tránh race condition giữa nhiều consumer instance).
- Với external call tới bên thứ ba, luôn truyền idempotency key xuyên suốt toàn bộ chain retry, đừng tạo key mới mỗi lần retry.

## Common Mistakes

- Commit offset trước khi xử lý xong (để tối ưu throughput) — dẫn đến at-most-once trá hình, mất message khi consumer crash mà tưởng đang dùng at-least-once.
- Coi Kafka "exactly-once semantics" là exactly-once toàn hệ thống, quên rằng nó không bảo vệ side-effect ra ngoài Kafka (gọi HTTP, ghi DB khác).
- Dedupe dựa vào message ID của broker (Kafka offset, SQS message ID) thay vì business key — sai khi message được replay từ DLQ với ID khác nhưng cùng nội dung nghiệp vụ.
- Không có TTL hoặc cleanup cho bảng `processed_messages`, khiến bảng phình to vô hạn và làm chậm dần query kiểm tra dedupe theo thời gian.
- Retry logic đơn giản chỉ re-enqueue mà không kiểm tra idempotency, gây duplicate side-effect ngay cả khi hệ thống được thiết kế "để retry an toàn".

## Interview Questions

**Hỏi**: Exactly-once delivery có thực sự tồn tại trong hệ thống phân tán không?

**Trả lời**: Exactly-once delivery thuần túy ở tầng network là bất khả thi (liên quan Two Generals' Problem) khi có thể mất message hoặc mất ack. Cái thực sự đạt được trong production là "exactly-once processing/semantics" — xây trên at-least-once delivery kết hợp idempotency ở tầng xử lý, để dù message đến nhiều lần thì effect chỉ xảy ra một lần.

**Hỏi**: Tại sao commit Kafka offset trước khi xử lý message lại nguy hiểm?

**Trả lời**: Nếu consumer crash sau khi commit offset nhưng trước khi xử lý xong side-effect (ví dụ ghi DB), message coi như đã "biến mất" khỏi Kafka theo góc nhìn của consumer group — không ai retry lại nó, dẫn đến mất dữ liệu (biến at-least-once thành at-most-once trên thực tế).

**Hỏi**: Idempotency key nên sinh ở đâu và dựa trên gì?

**Trả lời**: Nên sinh từ business logic (ví dụ `order_id + operation_type` hoặc `order_id + version`) chứ không phải từ broker message ID, vì message có thể được replay (từ DLQ, reprocessing) với ID khác nhau nhưng cùng ý nghĩa nghiệp vụ — dùng broker ID sẽ khiến dedupe thất bại đúng lúc cần nhất.

## Summary

At-most-once, at-least-once, exactly-once là ba mô hình delivery với đánh đổi khác nhau giữa mất dữ liệu và duplicate. Exactly-once delivery thuần túy không khả thi trong hệ thống phân tán do giới hạn cơ bản của network không tin cậy; cái production thực sự dùng là at-least-once delivery cộng idempotency ở tầng xử lý để đạt "exactly-once processing". Idempotency nên được implement bằng dedupe key theo business logic, unique constraint ở DB, hoặc idempotent write tự nhiên (upsert, conditional update). Kafka EOS là một cơ chế mạnh nhưng chỉ bảo vệ trong phạm vi Kafka-to-Kafka, không lan ra được side-effect bên ngoài. Thiết kế đúng ngay từ consumer logic tránh được toàn bộ lớp bug duplicate-charge, mất dữ liệu, và sai thứ tự event trong hệ thống production.

## Knowledge Graph

- Idempotency Key Pattern — cơ chế cụ thể để biến at-least-once thành exactly-once processing.
- Transactional Outbox Pattern — đảm bảo publish event và DB write atomic, tránh dual-write inconsistency ở producer.
- Kafka Consumer Offset Management — cơ chế commit offset quyết định delivery guarantee thực tế của consumer.
- Dead Letter Queue (DLQ) — nơi message lỗi được đưa vào và có thể replay, ảnh hưởng đến cách thiết kế dedupe key.
- Two Generals' Problem — nền tảng lý thuyết giải thích vì sao exactly-once delivery không thể đạt được tuyệt đối.
- Distributed Transaction / Saga Pattern — liên quan khi side-effect của message xử lý trải trên nhiều service.

## Five Things To Remember

- Exactly-once delivery không tồn tại thuần túy; thực chất là at-least-once cộng idempotency.
- Luôn commit offset/ack sau khi xử lý xong side-effect, không commit trước.
- Sinh idempotency key từ business logic, không dựa vào message ID của broker.
- Dùng unique constraint ở DB làm lớp bảo vệ cuối cùng chống race condition.
- Kafka EOS chỉ bảo vệ trong phạm vi Kafka, không tự động bảo vệ side-effect ra hệ thống ngoài.
