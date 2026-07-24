---
id: blue-green-canary-deployment
title: Blue-Green / Canary Deployment
tags: ["engineering", "deployment"]
---

# Blue-Green / Canary Deployment

> Status: Draft

## Problem

Deploy phiên bản mới bằng cách rolling update trực tiếp lên các instance đang chạy (dừng instance cũ, khởi động instance mới, lặp lại) khiến hệ thống luôn ở trạng thái pha trộn giữa hai phiên bản trong lúc rollout, và nếu phiên bản mới có bug nghiêm trọng (crash loop, memory leak, query sai schema), toàn bộ user đang được route tới các instance mới đã bị lỗi đều bị ảnh hưởng ngay lập tức, trong khi việc rollback đòi hỏi lặp lại đúng quy trình deploy chậm chạp đó theo chiều ngược lại. Không có cách nào tách biệt rõ ràng "phiên bản đang chạy thật" khỏi "phiên bản đang được kiểm chứng", nên mọi lỗi phát hiện sau khi release đều đã kịp ảnh hưởng tới 100% traffic trước khi engineer kịp phản ứng.

## Pain Points

- Rollback bằng rolling update ngược lại mất nhiều phút tới hàng chục phút (kéo image cũ, khởi động lại instance, chờ health check), trong khi mỗi phút downtime hoặc lỗi ở production đều tính trực tiếp thành doanh thu mất và SLA vi phạm.
- Bug logic tinh vi (vd. sai công thức tính phí, off-by-one trong phân trang) không bị health check bắt được vì service vẫn "healthy" theo nghĩa hạ tầng (process sống, port mở) nhưng sai về nghiệp vụ — rolling update đẩy lỗi này ra 100% user cùng lúc trước khi ai kịp nhận ra.
- Database migration chạy song song với rolling update tạo ra cửa sổ thời gian mà instance cũ và instance mới cùng đọc/ghi vào cùng schema, gây lỗi dữ liệu nếu migration không tương thích ngược (backward incompatible).
- Thiếu khả năng test phiên bản mới với traffic thật (không phải traffic giả lập) trước khi cam kết toàn bộ, khiến những lỗi chỉ xuất hiện dưới tải thật hoặc với dữ liệu thật (edge case trong dữ liệu production) không được phát hiện cho tới khi đã ảnh hưởng toàn bộ user.

## Solution

Blue-green deployment duy trì hai môi trường production giống hệt nhau (blue = đang chạy, green = phiên bản mới), deploy và test đầy đủ trên green trong khi blue vẫn phục vụ 100% traffic thật, sau đó chuyển traffic tức thời (thường qua đổi load balancer/DNS/router) từ blue sang green — rollback cũng tức thời bằng cách chuyển traffic ngược lại. Canary deployment giải quyết cùng vấn đề theo hướng khác biệt: thay vì chuyển toàn bộ traffic một lần, nó tăng dần tỷ lệ % traffic vào phiên bản mới (vd. 1% → 5% → 25% → 100%), theo dõi metric ở mỗi bước và chỉ tiếp tục tăng nếu phiên bản mới chứng minh được ổn định, giảm thiểu số user bị ảnh hưởng nếu có lỗi. Cả hai kỹ thuật đều nhắm tới cùng mục tiêu — giảm blast radius và thời gian rollback — nhưng blue-green ưu tiên tốc độ chuyển đổi tức thời còn canary ưu tiên giảm rủi ro bằng cách giới hạn số lượng user tiếp xúc với phiên bản chưa được kiểm chứng đầy đủ.

## How It Works

**Blue-green** cần hai fleet instance chạy song song (cùng số lượng, cùng cấu hình, khác version), đứng sau một điểm chuyển traffic có thể swap gần như tức thời — thường là load balancer (đổi target group ở AWS ALB/NLB), service mesh (đổi weight ở Istio VirtualService), hoặc DNS (đổi CNAME, dù DNS có độ trễ propagation và TTL nên ít dùng cho cutover tức thời). Quy trình: deploy phiên bản mới lên toàn bộ fleet green trong khi fleet blue vẫn phục vụ 100% traffic; chạy smoke test và health check trên green qua traffic nội bộ hoặc traffic mirror (traffic thật được sao chép sang green nhưng response bị bỏ, chỉ dùng để quan sát); khi green đạt tiêu chí, chuyển toàn bộ traffic từ blue sang green tại một điểm chuyển duy nhất (thường dưới 1 giây với load balancer, lâu hơn với DNS); giữ fleet blue chạy thêm một khoảng thời gian (vd. 30-60 phút) làm phương án rollback tức thời trước khi tắt hẳn. Điểm mấu chốt kỹ thuật là traffic không bao giờ bị chia sẻ giữa hai phiên bản tại cùng một thời điểm — mọi request tại một thời điểm đều đi 100% vào blue hoặc 100% vào green, nên không tồn tại trạng thái pha trộn phiên bản như rolling update.

**Canary** thực hiện traffic splitting có kiểm soát dựa trên tỷ lệ %, thường triển khai ở tầng L7 load balancer hoặc service mesh có khả năng route theo weight (Istio VirtualService với `weight`, Envoy weighted cluster, AWS ALB weighted target group, hoặc Kubernetes với hai Deployment cùng label selector nhưng số replica khác nhau để xấp xỉ tỷ lệ). Quy trình: deploy canary với 1 hoặc vài instance chạy phiên bản mới bên cạnh fleet cũ (stable); route một tỷ lệ nhỏ traffic (1-5%) vào canary, phần còn lại vẫn vào stable; thu thập metric của canary (error rate, latency p50/p95/p99, business metric như conversion rate) trong một cửa sổ quan sát đủ dài để có ý nghĩa thống kê; nếu metric của canary nằm trong ngưỡng chấp nhận so với baseline của stable (so sánh tương đối, không chỉ ngưỡng tuyệt đối), tăng dần % traffic theo các bước định trước; nếu metric xấu đi ở bất kỳ bước nào, tự động hoặc thủ công rollback bằng cách đưa % traffic canary về 0. Việc so sánh canary với baseline (không phải so với ngưỡng tĩnh) là phần kỹ thuật quan trọng nhất — công cụ như Flagger hay Argo Rollouts tự động hoá bước này bằng cách query Prometheus/Datadog định kỳ và áp dụng phân tích thống kê (t-test hoặc so sánh ngưỡng động) để quyết định promote hay rollback.

## Production Architecture

Trong kiến trúc Kubernetes, blue-green thường triển khai bằng hai Deployment độc lập (`app-blue`, `app-green`) và một Service trỏ tới một trong hai qua label selector — chuyển traffic là đổi selector của Service, gần như tức thời vì chỉ là thay đổi trong etcd/kube-proxy. Canary trên Kubernetes thường dùng Argo Rollouts hoặc Flagger, cả hai đều tích hợp trực tiếp với service mesh (Istio, Linkerd) hoặc ingress controller (NGINX, Traefik) để điều chỉnh weight theo từng bước, tự động query metric từ Prometheus và tự động rollback nếu vi phạm ngưỡng cấu hình trong `AnalysisTemplate` (Argo) hoặc `canaryAnalysis` (Flagger). Ở tầng CDN/edge, các nền tảng lớn dùng canary cho chính hạ tầng edge của họ — route một phần nhỏ traffic khu vực địa lý cụ thể vào phiên bản mới của edge worker trước khi rollout toàn cầu. Database và schema migration luôn là điểm phức tạp nhất trong cả hai mô hình: vì blue và green (hoặc stable và canary) chạy song song và cùng truy cập một database, mọi migration phải tương thích ngược (expand-contract pattern — thêm cột mới trước, để cả hai version đọc/ghi được, xoá cột cũ sau khi cutover hoàn tất) chứ không thể coi database như một phần được "chuyển" cùng lúc với traffic. Feature flag thường được kết hợp với canary ở tầng ứng dụng để tách rời việc deploy code khỏi việc kích hoạt tính năng, cho phép kiểm soát rollout mịn hơn cả tầng infrastructure.

## Trade-offs

Blue-green cần gấp đôi tài nguyên hạ tầng trong suốt thời gian chuyển đổi (hai fleet đầy đủ chạy song song), tốn kém hơn đáng kể so với rolling update hay canary vốn chỉ cần thêm một số ít instance canary. Vì traffic chuyển toàn bộ trong một bước, blue-green vẫn để 100% user tiếp xúc với phiên bản mới ngay khi cutover xảy ra — nếu bug không bị bắt trong giai đoạn test trên green, cutover vẫn là một sự kiện rủi ro toàn bộ (all-or-nothing), khác với canary vốn giới hạn rủi ro ở vài % traffic đầu tiên. Canary đánh đổi tốc độ release lấy độ an toàn — một canary rollout đầy đủ các bước tăng dần với cửa sổ quan sát hợp lý ở mỗi bước có thể mất hàng giờ tới cả ngày, chậm hơn nhiều so với blue-green cutover tức thời, và đòi hỏi hệ thống observability đủ tốt (metric theo version, đủ độ chi tiết và độ trễ thấp) để ra quyết định đúng ở mỗi bước — nếu observability yếu, canary chỉ tạo cảm giác an toàn giả mà không thực sự phát hiện được lỗi. Cả hai mô hình đều gặp cùng một giới hạn cơ bản với stateful service và database schema: traffic có thể chuyển tức thời hoặc tăng dần, nhưng dữ liệu đã ghi bởi phiên bản cũ không thể "rollback" theo cùng tốc độ nếu schema đã thay đổi không tương thích ngược.

## Best Practices

- Áp dụng expand-contract pattern cho mọi migration schema khi dùng blue-green/canary: thêm thay đổi tương thích ngược trước, cutover/rollout traffic, rồi mới dọn dẹp (xoá cột/bảng cũ) sau khi chắc chắn không còn phiên bản nào cần chúng.
- Chọn metric quyết định rollback/promote canary dựa trên cả metric hạ tầng (error rate, latency) lẫn business metric (conversion rate, checkout success rate), vì lỗi logic nghiêm trọng nhất thường không làm tăng error rate HTTP mà làm giảm hiệu quả nghiệp vụ.
- Giữ fleet cũ (blue hoặc stable) chạy thêm một khoảng thời gian sau cutover/promote hoàn tất thay vì tắt ngay, để đảm bảo có đường rollback tức thời nếu lỗi chỉ xuất hiện sau một độ trễ nhất định.
- Tự động hoá quyết định promote/rollback canary bằng công cụ (Argo Rollouts, Flagger) thay vì để engineer theo dõi dashboard thủ công và tự quyết định, vì tốc độ phản ứng thủ công không đủ nhanh khi lỗi lan rộng.
- Đảm bảo cả hai phiên bản (blue/green hoặc stable/canary) đọc/ghi cùng schema database một cách an toàn trong suốt thời gian traffic bị chia sẻ hoặc chưa cutover hoàn tất.

## Common Mistakes

- Chạy migration schema không tương thích ngược cùng lúc với rollout, khiến phiên bản cũ (vẫn đang phục vụ một phần hoặc toàn bộ traffic) gặp lỗi vì schema đã đổi.
- Coi health check hạ tầng (process sống, HTTP 200) là đủ điều kiện để promote canary hoặc cutover blue-green, trong khi bug logic nghiêm trọng không hề làm health check thất bại.
- Tắt fleet cũ ngay lập tức sau khi cutover hoặc promote 100%, không giữ lại buffer thời gian, khiến rollback khi phát hiện lỗi trễ phải quay lại deploy từ đầu thay vì chuyển traffic tức thời.
- Đặt bước tăng traffic canary đầu tiên quá lớn (vd. nhảy thẳng 50%) hoặc cửa sổ quan sát quá ngắn, khiến canary không thực sự giảm được blast radius hay phát hiện được vấn đề trước khi ảnh hưởng phần lớn traffic.
- Không đồng bộ session/cache giữa hai môi trường trong blue-green, khiến user bị chuyển giữa blue và green trong lúc cutover mất session hoặc gặp dữ liệu cache không nhất quán.

## Interview Questions

**Hỏi**: Khi nào nên chọn blue-green thay vì canary, và ngược lại?

**Trả lời**: Blue-green phù hợp khi cần rollback tức thời và có thể chấp nhận chi phí gấp đôi tài nguyên tạm thời, đặc biệt với hệ thống mà việc chia traffic theo % khó thực hiện an toàn (vd. stateful service, batch job). Canary phù hợp khi muốn giảm blast radius tối đa và có đủ observability để đánh giá metric ở từng bước tăng traffic, chấp nhận đổi lại thời gian rollout dài hơn.

**Hỏi**: Vì sao migration schema lại là phần khó nhất khi áp dụng blue-green hoặc canary?

**Trả lời**: Vì traffic có thể chuyển đổi tức thời hoặc theo tỷ lệ, nhưng cả hai phiên bản code (cũ và mới) đều truy cập cùng một database trong một khoảng thời gian — nếu migration không tương thích ngược, phiên bản cũ sẽ lỗi ngay khi schema đổi dù chưa hề bị tắt, nên phải dùng expand-contract pattern để tách rời thời điểm thay đổi schema khỏi thời điểm chuyển traffic.

**Hỏi**: Canary dùng tiêu chí gì để quyết định tự động rollback ở một bước traffic cụ thể?

**Trả lời**: So sánh metric của canary (error rate, latency percentile, business metric) với baseline của phiên bản stable đang chạy song song trong cùng cửa sổ thời gian, dùng ngưỡng động hoặc phân tích thống kê thay vì ngưỡng tĩnh tuyệt đối, vì baseline tự nhiên đã biến động theo thời gian trong ngày và cần loại trừ nhiễu đó khỏi quyết định rollback.

## Summary

Blue-green và canary đều giải quyết cùng một vấn đề gốc của rolling update truyền thống — thiếu khả năng kiểm chứng phiên bản mới với traffic thật trước khi cam kết toàn bộ, và rollback chậm khi phát hiện lỗi. Blue-green duy trì hai fleet đầy đủ song song và chuyển 100% traffic tức thời tại một điểm chuyển duy nhất, ưu tiên tốc độ cutover và rollback nhưng tốn gấp đôi tài nguyên và vẫn là quyết định all-or-nothing. Canary tăng dần % traffic vào phiên bản mới theo từng bước có kiểm soát, dựa trên so sánh metric với baseline để quyết định tiếp tục hay rollback, ưu tiên giảm blast radius nhưng đòi hỏi observability tốt và thời gian rollout dài hơn. Cả hai đều gặp chung thách thức với database schema migration, buộc phải dùng expand-contract pattern để tách rời thay đổi schema khỏi thời điểm chuyển traffic. Lựa chọn giữa hai mô hình phụ thuộc vào việc hệ thống ưu tiên tốc độ rollback tức thời hay giảm thiểu số user tiếp xúc với rủi ro.

## Knowledge Graph

- Circuit Breaker — cùng thuộc nhóm cơ chế giảm blast radius khi lỗi xảy ra, nhưng phản ứng ở tầng runtime call thay vì tầng deploy traffic.
- Feature Flag — thường kết hợp với canary để tách rời việc deploy code khỏi việc kích hoạt tính năng, cho phép kiểm soát rollout mịn hơn ở tầng ứng dụng.
- Load Balancing — cơ chế hạ tầng thực hiện việc chuyển hoặc chia traffic theo weight trong cả blue-green và canary.
- Service Mesh (Istio/Linkerd) — cung cấp khả năng traffic splitting theo % và weighted routing mà canary rollout dựa vào.
- Rolling Update — chiến lược deploy truyền thống mà blue-green và canary được thiết kế để thay thế nhằm giảm rủi ro và thời gian rollback.
- Expand-Contract Pattern — kỹ thuật migration schema tương thích ngược bắt buộc phải áp dụng khi hai phiên bản code cùng truy cập một database trong lúc rollout.

## Five Things To Remember

- Blue-green chuyển 100% traffic tức thời giữa hai fleet đầy đủ, ưu tiên tốc độ rollback nhưng tốn gấp đôi tài nguyên.
- Canary tăng dần % traffic theo từng bước có kiểm soát, ưu tiên giảm blast radius nhưng cần observability tốt để quyết định đúng.
- Health check hạ tầng không đủ để phát hiện bug logic nghiệp vụ, phải theo dõi cả business metric khi promote canary hoặc cutover blue-green.
- Migration schema phải tương thích ngược (expand-contract) vì cả hai phiên bản code cùng truy cập database trong lúc rollout.
- Luôn giữ phiên bản cũ chạy thêm một khoảng thời gian sau cutover/promote để có đường rollback tức thời.
