---
id: health-check
title: Health Check / Readiness Endpoint
tags: ["backend", "reliability"]
---

# Health Check / Readiness Endpoint

> Status: Draft

## Problem

Một service chạy trên Kubernetes hoặc sau load balancer cần một cách để hạ tầng biết "instance này còn sống không" và "instance này có sẵn sàng nhận traffic không". Team thường implement nhanh một endpoint `/health` trả về `200 OK` cứng (hardcode, không kiểm tra gì cả) chỉ để pass health check của orchestrator, rồi coi như xong. Vấn đề là orchestrator hoàn toàn tin vào con số đó: nếu endpoint luôn trả 200 dù DB connection pool đã cạn hay Redis đã mất kết nối, Kubernetes hay load balancer vẫn tiếp tục định tuyến traffic vào một pod thực chất không xử lý được request nào — hệ thống giám sát tưởng khỏe trong khi user đang nhận toàn lỗi 500.

## Pain Points

- Pod bị treo ở trạng thái "zombie": process còn chạy, port còn mở, health check trả 200, nhưng mọi request nghiệp vụ đều fail vì DB/downstream đã mất kết nối — Kubernetes không bao giờ restart hay loại bỏ pod này khỏi service vì theo health check nó "khỏe mạnh".
- Khi rolling deploy, pod mới chưa kết nối xong DB pool hoặc chưa load xong cache warm-up đã được đưa vào nhận traffic ngay (vì readiness trả 200 sớm), gây một đợt lỗi 500/timeout ngắn nhưng đều đặn mỗi lần deploy.
- Liveness probe trả 200 dù event loop đã bị block hoàn toàn (deadlock, GC pause kéo dài) khiến orchestrator không bao giờ restart container bị treo — outage kéo dài đến khi con người phát hiện và can thiệp thủ công.
- Trộn lẫn liveness và readiness thành một endpoint duy nhất: khi một dependency phụ (không thiết yếu) bị lỗi, endpoint trả 503 cho cả liveness lẫn readiness, khiến Kubernetes restart toàn bộ pod thay vì chỉ rút pod đó khỏi load balancing — biến một sự cố cục bộ nhỏ thành một đợt restart hàng loạt không cần thiết.

## Solution

Health check không phải một endpoint duy nhất mà là hai khái niệm tách biệt phục vụ hai câu hỏi khác nhau: **liveness** ("process này có cần bị giết và khởi động lại không?") và **readiness** ("instance này có nên nhận traffic ngay bây giờ không?"). Liveness chỉ nên kiểm tra process còn phản hồi được (không deadlock, không treo), còn readiness phải kiểm tra thực chất khả năng phục vụ request — bao gồm kết nối tới các dependency thiết yếu như DB, cache, message queue. Một health check "nông" (shallow, luôn trả 200) không cung cấp tín hiệu gì cho orchestrator, biến toàn bộ cơ chế tự phục hồi của hạ tầng (auto-restart, auto-remove-from-LB) thành vô dụng.

## How It Works

Liveness probe (Kubernetes gọi định kỳ, vd. mỗi 10s, timeout 1-2s) nên trả lời cực nhanh và không phụ thuộc vào bất kỳ external call nào — chỉ kiểm tra process còn xử lý được HTTP request cơ bản (vd. handler trả `200 OK` tĩnh, hoặc kiểm tra event loop không bị block quá X ms). Nếu liveness thất bại N lần liên tiếp (`failureThreshold`), kubelet sẽ **kill và restart container** — đây là hành động phá hủy, chỉ nên dùng cho trường hợp process thực sự cần khởi động lại (deadlock, memory leak nghiêm trọng), không nên dùng để phản ánh tình trạng dependency.

Readiness probe kiểm tra sâu hơn: gọi thử kết nối tới DB (vd. `SELECT 1` hoặc ping connection pool), kiểm tra Redis còn phản hồi PING, kiểm tra kết nối tới message broker, và có thể kiểm tra các điều kiện nghiệp vụ như "cache đã warm-up xong chưa" sau khi khởi động. Nếu readiness trả về không healthy (503), Kubernetes **không** restart pod — nó chỉ rút pod đó ra khỏi danh sách endpoints của Service (không route traffic mới vào), trong khi liveness vẫn tiếp tục pass nên pod không bị kill. Đây chính là điểm khác biệt cốt lõi: liveness quyết định sống/chết, readiness quyết định có/không nhận traffic, và tách hai khái niệm này ra cho phép hệ thống tự phục hồi đúng cách — một dependency tạm thời lỗi (DB failover mất 5 giây) chỉ cần rút pod khỏi LB tạm thời, không cần giết cả process.

Bên trong implementation, readiness thường nên có timeout ngắn (vd. 2-3s) cho từng dependency check để tránh chính health check bị treo khi dependency chậm, và nên phân biệt dependency "thiết yếu" (DB chính — mất kết nối thì readiness phải fail) với dependency "phụ" (một service gợi ý không quan trọng — mất kết nối chỉ nên log warning, không kéo readiness xuống 503).

## Production Architecture

Trong một service Node.js/Express chạy trên Kubernetes, cấu hình probe thường tách rõ hai path: `livenessProbe` trỏ tới `/healthz` (handler tĩnh, không query gì), `readinessProbe` trỏ tới `/readyz` (handler kiểm tra DB pool, Redis, kèm cache trạng thái vài giây để tránh spam dependency mỗi lần probe gọi). Đằng sau load balancer AWS ALB/NLB, health check target group cũng cần cấu hình endpoint riêng biệt tương tự readiness, với `HealthyThresholdCount`/`UnhealthyThresholdCount` đủ để tránh flapping khi có network jitter ngắn. Trong hệ thống dùng service mesh (Istio), sidecar Envoy còn có thêm outlier detection dựa trên tỷ lệ lỗi thực tế của traffic (không chỉ dựa vào health check endpoint), bổ trợ thêm một lớp tín hiệu độc lập. Khi service khởi động (cold start), Kubernetes còn dùng thêm `startupProbe` với `failureThreshold` cao hơn để cho phép thời gian khởi động dài (vd. load model ML, warm cache) mà không bị liveness probe giết nhầm trước khi kịp sẵn sàng.

## Trade-offs

Readiness check càng sâu (kiểm tra nhiều dependency) thì tín hiệu càng chính xác nhưng đổi lại chi phí: mỗi lần probe gọi (thường mỗi vài giây) tạo thêm tải lên DB/Redis, và nếu không cache kết quả trong vài giây, hàng chục pod cùng probe liên tục có thể tự tạo ra một dạng traffic tấn công nhẹ lên chính dependency đang được kiểm tra. Ngược lại, readiness quá nông (chỉ kiểm tra process sống) thì mất hết giá trị cảnh báo sớm. Việc quyết định dependency nào là "thiết yếu" (fail thì readiness phải fail) và dependency nào là "phụ" (fail thì chỉ log) là một quyết định nghiệp vụ, không có công thức chung — chọn sai khiến hệ thống hoặc quá nhạy cảm (rút pod khỏi LB vì một service không quan trọng) hoặc quá chai lì (vẫn nhận traffic dù DB chính đã chết).

## Best Practices

- Tách rõ liveness và readiness thành hai endpoint riêng, không dùng chung một handler cho cả hai mục đích.
- Liveness chỉ kiểm tra process còn phản hồi, không gọi external dependency — tránh một DB chậm kéo theo cả process bị kill oan.
- Readiness phải kiểm tra thực chất các dependency thiết yếu (DB, cache, queue), phân biệt rõ dependency thiết yếu và dependency phụ.
- Cache kết quả check dependency trong vài giây để tránh health check tự tạo tải dội lên chính dependency đang được kiểm tra.
- Dùng `startupProbe` riêng cho service có thời gian khởi động dài, tránh liveness probe giết pod trước khi nó kịp sẵn sàng.

## Common Mistakes

- Health check trả `200 OK` cứng không kiểm tra gì, khiến orchestrator mất hoàn toàn khả năng phát hiện instance không lành mạnh.
- Dùng chung một endpoint cho cả liveness và readiness, khiến một dependency phụ lỗi kéo theo restart cả pod thay vì chỉ rút khỏi load balancing.
- Readiness check gọi trực tiếp DB mỗi lần probe mà không cache/timeout, khiến chính cơ chế health check trở thành nguồn tải phụ lên hệ thống.
- Đặt `failureThreshold`/`periodSeconds` của liveness quá nhạy, khiến pod bị restart liên tục khi chỉ có một đợt GC pause hoặc traffic spike ngắn hạn.
- Không có `startupProbe` cho service khởi động chậm, khiến liveness probe giết pod ngay trong lúc nó đang warm-up hợp lệ.

## Interview Questions

**Hỏi**: Liveness probe và readiness probe khác nhau ở điểm nào, và hậu quả khi trộn lẫn chúng là gì?

**Trả lời**: Liveness quyết định process có bị kill-restart hay không, chỉ nên kiểm tra process còn phản hồi. Readiness quyết định pod có nhận traffic mới hay không, cần kiểm tra sâu các dependency thiết yếu. Nếu dùng chung một endpoint, một dependency phụ tạm thời lỗi sẽ khiến cả liveness lẫn readiness fail cùng lúc, dẫn tới Kubernetes restart cả pod (hành động phá hủy) trong khi lẽ ra chỉ cần rút pod khỏi load balancing tạm thời là đủ.

**Hỏi**: Vì sao một health check luôn trả 200 lại nguy hiểm hơn là không có health check?

**Trả lời**: Không có health check thì team biết rõ mình không có cơ chế tự phục hồi và phải giám sát thủ công. Có health check nhưng luôn trả 200 tạo ra ảo giác an toàn — orchestrator, dashboard, on-call đều tin rằng instance khỏe mạnh trong khi thực chất nó đã mất kết nối dependency, khiến sự cố kéo dài lâu hơn vì không ai được cảnh báo cho đến khi user report hàng loạt.

**Hỏi**: Readiness probe nên timeout bao lâu cho mỗi dependency check, và tại sao không nên để nó chờ vô hạn?

**Trả lời**: Nên đặt timeout ngắn (vài giây) cho từng dependency check bên trong readiness handler. Nếu để chờ vô hạn, một dependency đang treo (không lỗi hẳn, chỉ chậm) sẽ làm chính bản thân readiness probe bị treo theo, khiến orchestrator không nhận được phản hồi đúng hạn của probe và có thể coi đó là fail chậm trễ hơn cần thiết, làm chậm phản ứng rút pod khỏi load balancing.

## Summary

Health check thực chất là hai cơ chế riêng biệt: liveness (process có cần restart không) và readiness (instance có nên nhận traffic không), và việc gộp chung hoặc làm nông cả hai (luôn trả 200) khiến orchestrator mất khả năng phát hiện instance không lành mạnh. Liveness nên kiểm tra tối thiểu, không phụ thuộc external call, để tránh dependency chậm kéo theo restart oan. Readiness cần kiểm tra thực chất các dependency thiết yếu, có timeout và cache hợp lý để không tự tạo tải phụ. Trong production, readiness còn quyết định pod mới có bị đưa vào nhận traffic quá sớm trong lúc rolling deploy hay không, và startupProbe giúp phân biệt "chưa sẵn sàng vì đang khởi động" với "không sẵn sàng vì lỗi thật". Thiết kế đúng hai probe này là điều kiện tiên quyết để cơ chế tự phục hồi của Kubernetes/load balancer thực sự hoạt động.

## Knowledge Graph

- Circuit Breaker — cùng mục tiêu ngăn traffic đi vào một thành phần không lành mạnh, nhưng circuit breaker hoạt động ở tầng gọi downstream còn health check hoạt động ở tầng orchestrator/load balancer.
- Load Balancing — readiness probe là tín hiệu đầu vào trực tiếp để load balancer quyết định đưa instance vào hay rút khỏi danh sách phục vụ.
- Rolling Deployment — readiness probe quyết định thời điểm pod mới được coi là đủ điều kiện nhận traffic trong quá trình deploy tuần tự.
- Retry & Backoff — khi readiness rút một instance khỏi LB, các request đang retry cần backoff hợp lý để không dồn hết sang các instance còn lại cùng lúc.
- Connection Pool — trạng thái pool (cạn kiệt hay còn khả dụng) thường là tiêu chí cốt lõi để readiness handler quyết định pass/fail.
- Service Mesh (Istio/Linkerd) — bổ sung outlier detection dựa trên tỷ lệ lỗi thực tế của traffic, độc lập với health check endpoint do ứng dụng tự expose.

## Five Things To Remember

- Liveness quyết định sống/chết của process; readiness quyết định có/không nhận traffic — không dùng chung một endpoint cho cả hai.
- Health check luôn trả 200 cứng nguy hiểm hơn không có health check, vì nó tạo ảo giác an toàn cho cả orchestrator lẫn con người.
- Liveness không nên gọi external dependency; readiness thì bắt buộc phải kiểm tra dependency thiết yếu.
- Luôn đặt timeout ngắn và cache kết quả cho readiness check để tránh chính nó trở thành nguồn tải hoặc điểm treo.
- Dùng startupProbe riêng cho service khởi động chậm để tránh liveness probe giết pod trong lúc warm-up hợp lệ.
