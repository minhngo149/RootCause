---
id: retry-backoff
title: Retry & Exponential Backoff
tags: ["distributed-systems", "resilience"]
---

# Retry & Exponential Backoff

> Status: Draft

## Problem

Một request gọi xuống service khác (hoặc database, hoặc broker) thất bại vì lý do tạm thời — packet loss, GC pause phía server, connection reset, hay một pod đang restart. Nếu code coi mọi lỗi là vĩnh viễn và trả lỗi ngay cho caller, hệ thống mất khả năng tự phục hồi khỏi những trục trặc thoáng qua vốn chiếm phần lớn lỗi trong môi trường phân tán. Ngược lại, nếu retry mà không kiểm soát — retry vô hạn, không có delay, không có giới hạn — thì chính hành vi retry lại trở thành nguyên nhân gây sập hệ thống, đặc biệt khi hàng nghìn client cùng retry cùng lúc vào một service đang quá tải.

## Pain Points

- Retry storm: service đang chịu tải cao trả lỗi 503, hàng nghìn client retry ngay lập tức, tổng traffic tăng gấp nhiều lần tải gốc, service không bao giờ kịp phục hồi (self-inflicted DDoS).
- Thundering herd sau outage: khi service vừa hồi phục, toàn bộ client bị block cùng đồng loạt gửi lại request tại cùng một thời điểm (vì cùng retry interval cố định), tạo ra một đợt sóng tải mới đánh sập service lần nữa.
- Retry không idempotent gây side-effect kép: một API tạo đơn hàng hoặc trừ tiền bị retry do timeout (dù request gốc đã xử lý thành công phía server, chỉ response bị mất) dẫn đến tạo đơn trùng hoặc trừ tiền hai lần.
- Retry che giấu lỗi thật: latency tăng dần do retry ẩn (mỗi request thất bại âm thầm retry 3-5 lần trước khi báo lỗi) khiến alerting không phát hiện kịp sự cố downstream, đến khi retry budget cạn thì outage đã lan rộng.
- Chi phí vận hành tăng: retry vào một dependency đang trả lỗi 500 do bug logic (không phải lỗi tạm thời) chỉ nhân bản load vô ích, tốn compute và tiền cloud mà không giải quyết được gì.

## Solution

Retry an toàn dựa trên ba trụ cột: idempotency (đảm bảo gọi lại nhiều lần cho cùng một kết quả cuối cùng, không side-effect kép), exponential backoff (tăng dần thời gian chờ giữa các lần retry để giảm áp lực dồn dập lên hệ thống đang gặp sự cố), và jitter (làm nhiễu thời gian chờ để tránh các client đồng bộ hóa retry vào cùng một thời điểm). Kết hợp cả ba với retry budget (giới hạn tổng số lần retry cho phép trong một cửa sổ thời gian) và circuit breaker để dừng hẳn việc retry khi downstream đã rõ ràng là không khả dụng.

## How It Works

**Idempotency key**: client sinh một UUID duy nhất cho mỗi logical operation (ví dụ tạo đơn hàng) và gửi kèm trong header `Idempotency-Key`. Server lưu key này cùng kết quả xử lý (thường trong Redis hoặc bảng riêng với TTL 24h) — nếu nhận lại cùng key, server trả về kết quả đã cache thay vì xử lý lại. Đây là điều kiện tiên quyết trước khi bật retry cho bất kỳ API có side-effect nào (POST tạo tài nguyên, thanh toán, gửi email).

**Exponential backoff**: thời gian chờ giữa lần retry thứ n được tính `delay = min(base * 2^n, max_delay)`, ví dụ base = 100ms thì lần 1 chờ 100ms, lần 2 chờ 200ms, lần 3 chờ 400ms, lần 4 chờ 800ms, cho tới trần `max_delay` (thường 30s-60s) để tránh chờ vô hạn. Việc tăng theo cấp số nhân phản ánh giả định thực tế: nếu downstream đang quá tải, nó cần thời gian ngày càng nhiều để phục hồi, và việc dồn dập gọi lại sớm chỉ kéo dài thời gian phục hồi đó.

**Jitter**: thay vì dùng delay thuần túy đã tính, cộng thêm một thành phần ngẫu nhiên. "Full jitter" (khuyến nghị bởi AWS) là `delay = random(0, base * 2^n)` — chọn ngẫu nhiên toàn bộ khoảng từ 0 đến giá trị exponential, giúp phân tán retry của hàng nghìn client ra đều theo thời gian thay vì dồn vào cùng một mốc. "Equal jitter" là `delay = base*2^n/2 + random(0, base*2^n/2)`, giữ một sàn tối thiểu để tránh retry quá sớm. Full jitter thường cho throughput tổng thể tốt hơn trong benchmark vì phân tán retry rộng hơn.

**Retry budget**: giới hạn tỷ lệ retry trên tổng request, ví dụ tối đa 10% request được phép retry trong một cửa sổ trượt 10s. Khi vượt ngưỡng, client fail-fast thay vì tiếp tục retry — đây là cơ chế bảo vệ khi phần lớn traffic đều đang lỗi (downstream thực sự down), lúc đó retry thêm chỉ tổng lượng tải mà không tăng tỷ lệ thành công.

**Phân loại lỗi trước khi retry**: chỉ retry lỗi được xác định là transient — timeout, connection reset, HTTP 502/503/504, gRPC `UNAVAILABLE`/`DEADLINE_EXCEEDED`. Không retry lỗi 4xx (400, 401, 403, 404, 422) vì đây là lỗi logic/permission sẽ lặp lại y hệt ở lần gọi sau. Retry HTTP 429 (rate limited) cần tôn trọng header `Retry-After` nếu server trả về, thay vì dùng backoff tự tính.

## Production Architecture

Trong một hệ thống microservices thực tế, retry với backoff+jitter thường được đặt ở tầng client SDK hoặc service mesh (Istio/Envoy, Linkerd) chứ không rải rác trong business logic của từng service — Envoy hỗ trợ retry policy khai báo qua config (`retry_on`, `num_retries`, `per_try_timeout`, `retry_back_off` với base_interval/max_interval) áp dụng đồng nhất cho toàn bộ traffic giữa các service. Ở tầng gọi API bên thứ ba (ví dụ Stripe, payment gateway), retry kết hợp idempotency key là bắt buộc — Stripe API tự hỗ trợ header `Idempotency-Key` chính vì lý do này. Message queue consumer (Kafka, SQS) áp dụng retry với backoff khi xử lý message thất bại, đẩy vào dead-letter queue sau N lần retry thất bại để tránh block toàn bộ partition. Ở tầng gateway/load balancer, circuit breaker (Hystrix pattern, hoặc resilience4j) giám sát tỷ lệ lỗi theo cửa sổ trượt và tự động "open" mạch để chặn toàn bộ request (bao gồm retry) đến một downstream đang lỗi cao, cho nó thời gian phục hồi trước khi "half-open" thử lại dần.

## Trade-offs

Backoff+jitter làm tăng latency tail (p99, p999) của các request gặp lỗi thoáng qua, vì mỗi lần retry cộng thêm delay — với hệ thống yêu cầu SLA chặt (ví dụ real-time bidding <100ms), retry có thể không khả thi và phải chấp nhận fail-fast. Retry cũng nhân bản load lên downstream đúng lúc downstream yếu nhất — dù có backoff, tổng số request vẫn tăng so với không retry, nên retry budget và circuit breaker là bắt buộc đi kèm chứ không phải tùy chọn. Idempotency key đòi hỏi lưu trạng thái phía server (storage, TTL management) và làm phức tạp thêm API contract — không phải mọi endpoint đều dễ làm idempotent (đặc biệt các thao tác có side-effect với hệ thống bên ngoài không hỗ trợ dedup). Retry ở nhiều tầng chồng lên nhau (client SDK retry + service mesh retry + queue consumer retry) có thể nhân số lần thử thực tế lên gấp nhiều lần dự kiến, cần audit toàn bộ retry policy theo từng tầng để tránh retry amplification.

## Best Practices

- Luôn dùng full jitter thay vì exponential backoff thuần: `sleep = random(0, min(cap, base * 2^attempt))`.
- Chỉ retry lỗi transient đã được phân loại rõ ràng (timeout, 5xx, connection reset); không bao giờ retry 4xx.
- Đặt trần số lần retry cụ thể (thường 3-5 lần) và trần thời gian chờ tối đa (30-60s), không để retry kéo dài vô hạn.
- Bắt buộc idempotency key cho mọi API có side-effect trước khi bật retry ở tầng client.
- Kết hợp circuit breaker: khi tỷ lệ lỗi vượt ngưỡng, ngắt hẳn retry và fail-fast thay vì tiếp tục dội request vào downstream đang chết.

## Common Mistakes

- Retry ngay lập tức không delay (busy retry loop), biến một lỗi tạm thời thành DDoS tự gây ra.
- Dùng backoff cố định không có jitter, khiến nhiều client đồng bộ hóa retry vào cùng thời điểm (thundering herd).
- Retry mọi loại lỗi kể cả 400/404/422 — lỗi logic sẽ lặp lại y hệt, retry chỉ tốn tài nguyên vô ích.
- Retry request không idempotent (POST tạo tài nguyên) mà không có idempotency key, gây trùng lặp dữ liệu khi network timeout nhưng server đã xử lý xong.
- Không giới hạn tổng retry budget toàn hệ thống, để mỗi client tự retry độc lập dẫn đến retry amplification khi outage xảy ra trên diện rộng.

## Interview Questions

**Hỏi**: Tại sao cần jitter, exponential backoff thuần túy chưa đủ?
**Trả lời**: Vì backoff thuần túy vẫn khiến các client bắt đầu retry cùng lúc (do cùng gặp lỗi cùng thời điểm) sẽ tiếp tục đồng bộ ở mọi lần retry sau đó, tạo ra các đợt sóng tải lặp lại tại đúng các mốc 2^n. Jitter phá vỡ sự đồng bộ này bằng cách ngẫu nhiên hóa thời điểm retry thực tế của từng client, phân tán tải đều theo thời gian.

**Hỏi**: Idempotency được đảm bảo như thế nào ở tầng server khi client retry một request POST?
**Trả lời**: Server yêu cầu client gửi kèm một idempotency key duy nhất cho mỗi logical operation; khi nhận request, server kiểm tra key này trong storage (thường Redis với TTL), nếu đã tồn tại và đã xử lý xong thì trả về response đã lưu thay vì thực thi lại side-effect, nếu đang xử lý dở thì trả lỗi conflict hoặc chờ.

**Hỏi**: Retry budget khác circuit breaker ở điểm nào?
**Trả lời**: Retry budget giới hạn tỷ lệ retry cho phép trên tổng request trong một cửa sổ thời gian, áp dụng per-request; circuit breaker giám sát tỷ lệ lỗi tổng thể của một downstream và khi vượt ngưỡng sẽ chặn toàn bộ request (kể cả request mới, không chỉ retry) đến downstream đó trong một khoảng thời gian, cho phép nó phục hồi trước khi thử lại.

## Summary

Retry là cơ chế cần thiết để hệ thống phân tán tự phục hồi khỏi lỗi tạm thời, nhưng retry không kiểm soát lại chính là nguyên nhân gây ra outage thứ cấp thông qua retry storm và thundering herd. Ba yếu tố bắt buộc để retry an toàn là idempotency (tránh side-effect kép), exponential backoff (giảm áp lực dồn dập theo thời gian), và jitter (phân tán retry giữa các client). Retry budget và circuit breaker là lớp bảo vệ bổ sung, ngăn hệ thống tiếp tục retry khi downstream đã thực sự không khả dụng. Trong kiến trúc production, retry policy nên được tập trung hóa ở tầng client SDK hoặc service mesh thay vì rải rác trong business logic để tránh retry amplification qua nhiều tầng.

## Knowledge Graph

- Circuit Breaker — cơ chế bổ trợ, ngắt hẳn traffic (kể cả retry) khi downstream lỗi cao vượt ngưỡng.
- Idempotency Key — điều kiện tiên quyết để retry an toàn cho các request có side-effect.
- Timeout — retry luôn phải đi kèm timeout hợp lý cho mỗi lần thử (per-try timeout), nếu không retry sẽ chồng chất các request treo.
- Rate Limiting — retry vào một service đang bị rate limit (429) cần tôn trọng `Retry-After` thay vì tự tính backoff.
- Dead Letter Queue — nơi message được đẩy vào sau khi retry vượt quá số lần cho phép trong hệ thống message queue.
- Bulkhead Pattern — cô lập tài nguyên (connection pool, thread pool) để retry vào một downstream lỗi không làm cạn kiệt tài nguyên dùng cho các downstream khác.

## Five Things To Remember

- Không có idempotency, retry là một máy tạo dữ liệu trùng lặp.
- Backoff không có jitter vẫn gây thundering herd.
- Chỉ retry lỗi transient, không bao giờ retry lỗi 4xx.
- Giới hạn số lần retry và trần thời gian chờ, không để retry kéo dài vô hạn.
- Retry cần đi kèm circuit breaker, nếu không nó sẽ khuếch đại chính sự cố nó đang cố khắc phục.
