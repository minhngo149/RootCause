---
id: opentelemetry
title: OpenTelemetry
tags: ["observability"]
---

# OpenTelemetry

> Status: Draft

## Problem

Một hệ thống production thường có trace từ Jaeger client, metrics từ Prometheus client, log từ một logging library riêng — ba SDK độc lập, ba format dữ liệu, ba đường ống export khác nhau, và không có cách nào liên kết một request cụ thể trong trace với dòng log tương ứng của chính request đó. Khi đổi vendor (ví dụ từ Jaeger sang Datadog, hoặc từ self-hosted Prometheus sang một SaaS APM), toàn bộ code instrumentation gắn với SDK cũ phải viết lại vì mỗi vendor có API và semantic riêng. Ở kiến trúc microservices với service viết bằng nhiều ngôn ngữ (Go, Java, Node.js), việc mỗi ngôn ngữ dùng một bộ SDK khác nhau khiến trace không propagate được xuyên service, gãy chuỗi liên kết ngay tại điểm nó cần nhất.

## Pain Points

- Trace ID sinh ra ở service A không được service B hiểu đúng định dạng do hai bên dùng library propagation khác nhau (B3 header vs W3C Trace Context), khiến distributed trace bị đứt gãy giữa các service, engineer debug incident phải ghép log thủ công theo timestamp.
- Đổi vendor APM (từ New Relic sang Datadog, hoặc ngược lại) buộc phải rip-and-replace toàn bộ instrumentation code trong hàng trăm service, tốn hàng tuần công sức và rủi ro thiếu sót coverage.
- Log, metric, trace của cùng một request không có `trace_id` chung để join lại, engineer phải tìm log bằng cách đoán khoảng thời gian và service name, mất nhiều phút thay vì vài giây khi điều tra một lỗi 500 cụ thể.
- Mỗi ngôn ngữ trong hệ thống polyglot dùng một client riêng cho cùng một vendor, dẫn tới sự khác biệt về attribute naming (`http.method` vs `httpMethod` vs `method`), phá vỡ khả năng query/alert nhất quán trên toàn hệ thống.

## Solution

OpenTelemetry (OTel) là chuẩn vendor-neutral, mã nguồn mở dưới CNCF, định nghĩa API và SDK thống nhất để sinh ra ba loại tín hiệu telemetry — trace, metrics, log — cùng với một giao thức truyền tải chung (OTLP) và một tầng trung gian độc lập vendor gọi là Collector. Ứng dụng chỉ cần instrument một lần bằng OTel SDK/API; việc chọn nơi gửi dữ liệu đi đâu (Jaeger, Prometheus, Datadog, Grafana Cloud...) là cấu hình exporter ở Collector, không phải thay đổi code nghiệp vụ. Đây là lý do OTel đang dần thay thế các SDK độc quyền riêng lẻ của từng vendor: instrumentation trở thành tài sản độc lập với backend lưu trữ.

## How It Works

Kiến trúc OTel gồm bốn lớp. **API** là interface trung lập ngôn ngữ để tạo span, ghi metric, emit log — code ứng dụng chỉ phụ thuộc vào API này. **SDK** là cài đặt cụ thể của API, xử lý sampling, batching, resource attribution (gắn `service.name`, `service.version`, `host.name` vào mọi tín hiệu). **Instrumentation libraries** tự động gắn OTel vào các framework phổ biến (Express, gRPC, database driver) qua cơ chế monkey-patching hoặc middleware, sinh span mà không cần sửa code nghiệp vụ. **Exporter** chuyển dữ liệu đã thu thập sang định dạng OTLP (Protobuf qua gRPC hoặc HTTP) để gửi đi.

Propagation dùng W3C Trace Context header (`traceparent`, `tracestate`) làm chuẩn mặc định — khi service A gọi service B qua HTTP, SDK tự động chèn `trace_id` và `span_id` hiện tại vào header request, service B đọc header này để tạo child span thuộc cùng trace, nhờ vậy trace xuyên suốt được dù A và B viết bằng ngôn ngữ khác nhau. Context propagation trong-process dùng cơ chế đặc thù từng ngôn ngữ (ví dụ `context.Context` trong Go, thread-local trong Java) để giữ span hiện tại "active" xuyên qua các lệnh gọi hàm bất đồng bộ.

**OTel Collector** là một binary độc lập chạy dạng sidecar hoặc gateway, nhận dữ liệu qua receiver (OTLP, Jaeger, Prometheus remote write...), xử lý qua chuỗi processor (batch để gom nhiều span thành một request lớn giảm overhead mạng, tail-sampling để quyết định giữ/bỏ trace dựa trên toàn bộ span đã hoàn tất thay vì quyết định ngẫu nhiên từ đầu, resource detection để tự gắn thêm metadata k8s/cloud), rồi đẩy ra qua exporter tới một hoặc nhiều backend cùng lúc. Vì Collector tách biệt hoàn toàn khỏi ứng dụng, đổi backend chỉ là đổi một dòng cấu hình exporter, không đụng tới service nào.

## Production Architecture

Trong một cluster Kubernetes, mô hình phổ biến là Collector chạy dạng DaemonSet (một instance mỗi node, nhận traffic local qua Unix socket hoặc localhost để giảm latency) đóng vai trò **agent**, rồi forward tiếp lên một tầng Collector dạng Deployment đóng vai trò **gateway** làm nhiệm vụ tail-sampling tập trung, gắn thêm attribute, và fan-out ra nhiều backend (ví dụ vừa gửi trace sang Jaeger self-hosted vừa gửi metrics sang Prometheus/Mimir). Ứng dụng chỉ cần cấu hình OTLP endpoint trỏ vào agent local, hoàn toàn không biết tới backend cuối cùng là gì. Auto-instrumentation qua OTel Operator cho Kubernetes tiêm sidecar/init container tự động vào pod dựa trên annotation, cho phép bật tracing cho hàng trăm service đã tồn tại mà không cần sửa Dockerfile hay code, một khoản đầu tư instrumentation rất lớn được khấu hao ngay lập tức trên toàn hệ thống.

## Trade-offs

- OTel Collector là một tầng hạ tầng thêm cần vận hành, monitor, và scale riêng — nếu Collector down hoặc backpressure, dữ liệu telemetry có thể mất hoặc bị buffer tràn bộ nhớ, tự nó trở thành một điểm lỗi mới trong hệ thống quan sát.
- Semantic convention của OTel (tên attribute chuẩn hóa như `http.request.method`) qua nhiều phiên bản từng đổi tên (`http.method` → `http.request.method`), gây breaking change cho dashboard/alert cũ khi nâng cấp SDK hoặc Collector.
- So với dùng thẳng SDK độc quyền của vendor (ví dụ Datadog APM agent), OTel đôi khi thiếu vài tính năng đặc thù vendor cung cấp sẵn (một số loại profiling, continuous profiler tích hợp sâu), buộc phải chờ vendor hỗ trợ OTLP đầy đủ hoặc dùng thêm exporter riêng.
- Tail-sampling ở Collector gateway cần giữ toàn bộ span của một trace trong bộ nhớ cho tới khi trace kết thúc mới quyết định giữ/bỏ, tốn RAM đáng kể và cần sticky routing (theo `trace_id`) giữa các Collector instance để mọi span của cùng trace đi cùng một gateway.

## Best Practices

- Luôn gắn `service.name` và `service.version` qua Resource attribute ngay từ đầu, vì đây là khóa để phân biệt và group dữ liệu giữa hàng trăm service trong backend.
- Dùng auto-instrumentation cho framework/database phổ biến trước, chỉ viết manual span cho logic nghiệp vụ quan trọng cần đo riêng — tránh instrument thủ công toàn bộ codebase.
- Bật head-based sampling ở tầng SDK (giữ tỷ lệ nhỏ trace ngẫu nhiên) kết hợp tail-based sampling ở Collector gateway (luôn giữ trace có lỗi hoặc latency cao), thay vì chỉ dùng một loại.
- Cấu hình batch processor với `timeout` và `send_batch_size` hợp lý ở Collector để giảm số lượng request mạng, tránh gửi từng span riêng lẻ gây overhead.
- Chuẩn hóa theo OTel Semantic Conventions ngay từ đầu thay vì tự đặt tên attribute tùy tiện, để dashboard và alert tái sử dụng được khi thêm service mới hoặc đổi backend.

## Common Mistakes

- Chạy Collector với cấu hình mặc định không giới hạn memory (`memory_limiter` processor), khiến Collector OOM khi traffic tăng đột biến và làm mất toàn bộ dữ liệu đang buffer.
- Trộn lẫn instrumentation OTel với SDK vendor cũ trong cùng một service, tạo ra hai trace ID không liên kết nhau cho cùng một request.
- Bật tracing 100% (sampling rate = 1) trên service traffic cao mà không tính overhead CPU/network, gây tăng đáng kể tải hệ thống chỉ vì instrumentation.
- Không cấu hình propagator nhất quán giữa các service (một số dùng B3, một số dùng W3C Trace Context mặc định), khiến trace bị đứt giữa chừng dù cả hai đều dùng OTel.
- Không đặt retry/queue cho OTLP exporter khi backend tạm thời không khả dụng, khiến ứng dụng nghẽn hoặc mất dữ liệu telemetry ngay khi backend restart hoặc mạng chập chờn.

## Interview Questions

**Hỏi**: OpenTelemetry giải quyết vấn đề gì mà các SDK độc quyền của từng vendor (như Jaeger client, Datadog agent riêng) không giải quyết được?

**Trả lời**: Nó tách rời việc instrument code (tạo span, metric, log) khỏi việc chọn backend lưu trữ/hiển thị. Ứng dụng chỉ phụ thuộc vào OTel API/SDK một lần; đổi vendor chỉ là đổi cấu hình exporter ở Collector, không cần viết lại instrumentation trong hàng trăm service như khi dùng SDK độc quyền của từng vendor.

**Hỏi**: Vai trò của OTel Collector trong kiến trúc là gì, và tại sao không để ứng dụng export thẳng tới backend?

**Trả lời**: Collector là tầng trung gian nhận, xử lý (batch, sampling, thêm resource attribute), và fan-out dữ liệu tới nhiều backend cùng lúc. Để ứng dụng export thẳng sẽ buộc mỗi service phải biết địa chỉ và logic retry/batching của từng backend, và mọi thay đổi backend đều phải deploy lại ứng dụng; Collector cô lập hoàn toàn thay đổi đó ở một tầng hạ tầng riêng.

**Hỏi**: Head-based sampling và tail-based sampling khác nhau ở điểm nào, và vì sao production thường cần cả hai?

**Trả lời**: Head-based sampling quyết định giữ hay bỏ một trace ngay từ span đầu tiên (thường ngẫu nhiên theo tỷ lệ), rẻ về tài nguyên nhưng có thể bỏ sót trace lỗi vì quyết định quá sớm. Tail-based sampling chờ toàn bộ trace hoàn tất rồi mới quyết định dựa trên kết quả thực tế (lỗi, latency cao), đảm bảo không bỏ sót trace quan trọng nhưng tốn bộ nhớ Collector để giữ buffer. Kết hợp cả hai giúp vừa giảm chi phí lưu trữ vừa không mất trace có giá trị điều tra.

## Summary

OpenTelemetry là chuẩn vendor-neutral hợp nhất trace, metrics, log dưới một API/SDK và một giao thức truyền tải chung (OTLP), giải quyết vấn đề mỗi vendor một SDK riêng gây khóa chặt (lock-in) và gãy liên kết dữ liệu. Kiến trúc gồm API/SDK ở phía ứng dụng và Collector đóng vai trò trung gian độc lập vendor, cho phép đổi backend chỉ bằng cấu hình thay vì viết lại code. Propagation dùng W3C Trace Context giúp trace xuyên suốt qua service viết bằng nhiều ngôn ngữ khác nhau. Đánh đổi chính là thêm một tầng hạ tầng (Collector) cần vận hành riêng, và một số tính năng đặc thù vendor có thể chưa hỗ trợ đầy đủ qua OTLP. Đây là hướng đi mà ngành đang hội tụ về, dần thay thế các SDK riêng lẻ của Jaeger, Prometheus client, hay APM agent độc quyền.

## Knowledge Graph

- Distributed Tracing — khái niệm nền tảng mà OTel chuẩn hóa cách sinh và propagate span.
- W3C Trace Context — chuẩn header propagation mà OTel dùng mặc định để liên kết trace xuyên service.
- Prometheus — một trong nhiều backend metrics mà OTel Collector có thể export tới qua remote write.
- Structured Logging — log có `trace_id` đính kèm để join với trace, một phần tín hiệu OTel chuẩn hóa.
- Sampling Strategies (head-based/tail-based) — quyết định trace nào được giữ lại trước khi tới backend.
- Service Mesh (như Istio) — thường sinh span song song với OTel ở tầng network, cần hợp nhất tránh trùng lặp.

## Five Things To Remember

- OTel tách instrumentation khỏi backend — đổi vendor chỉ cần đổi exporter, không viết lại code.
- Ba tín hiệu trace/metrics/log dùng chung một SDK và một giao thức OTLP.
- W3C Trace Context là propagator mặc định giúp trace xuyên suốt qua nhiều ngôn ngữ khác nhau.
- Collector là hạ tầng cần vận hành riêng, có thể là điểm lỗi hoặc nghẽn cổ chai mới nếu không giới hạn memory.
- Kết hợp head-based và tail-based sampling để vừa tiết kiệm chi phí vừa không bỏ sót trace lỗi.
