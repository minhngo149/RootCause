---
id: prometheus
title: Prometheus
tags: ["observability"]
---

# Prometheus

> Status: Draft

## Problem

Một hệ thống microservices chạy production cần biết request rate, error rate, latency percentile của từng service theo thời gian thực, nhưng không có cách nào tập trung thu thập số liệu từ hàng chục/hàng trăm instance đang chạy mà không làm chậm chính các instance đó. Đẩy log text vào một kho tập trung rồi parse ra số liệu là quá chậm và tốn tài nguyên cho việc trả lời câu hỏi đơn giản như "p99 latency của service X trong 5 phút qua là bao nhiêu". Cần một mô hình thu thập metric có cấu trúc, có ngôn ngữ truy vấn theo thời gian, và không đòi hỏi từng service phải tự "đẩy" (push) dữ liệu đi một cách phức tạp.

## Pain Points

- Không có time-series metric, engineer chỉ biết hệ thống "đang lỗi" qua log hoặc qua khách hàng báo, không biết xu hướng CPU/latency/error rate tăng dần trong bao lâu trước khi sự cố nổ ra.
- Thiếu cardinality control, một dashboard vô tình gắn label `user_id` hoặc `request_id` vào metric có thể làm số time-series tăng từ vài nghìn lên hàng triệu chỉ sau vài giờ, khiến Prometheus ăn hết RAM và OOM-kill chính nó.
- Không hiểu mô hình pull-based, engineer cấu hình sai network/firewall khiến Prometheus không scrape được target, dữ liệu có "lỗ hổng" (gap) trong dashboard mà không ai nhận ra cho đến khi cần điều tra sự cố và thấy graph trống.
- PromQL viết sai (vd. quên `rate()` trước khi cộng counter, hoặc dùng `avg` trên percentile đã tính sẵn) cho ra số liệu trông hợp lý nhưng sai về mặt toán học, dẫn đến quyết định vận hành dựa trên dữ liệu sai.

## Solution

Prometheus là hệ thống monitoring pull-based: thay vì mỗi service tự đẩy metric đi, Prometheus server chủ động "kéo" (scrape) dữ liệu từ một endpoint HTTP (`/metrics`) mà mỗi service tự expose theo định dạng text đơn giản, theo chu kỳ cố định (thường 15-30s). Dữ liệu được lưu dưới dạng time-series đa chiều (metric name + tập hợp key-value labels), truy vấn bằng PromQL — một ngôn ngữ hàm chuyên cho time-series, hỗ trợ rate, aggregation, join giữa các series theo label. Giải pháp cốt lõi cho vấn đề chi phí là kiểm soát cardinality: giới hạn số tổ hợp label duy nhất trên mỗi metric, tránh dùng label có giá trị không giới hạn (unbounded) như user ID hay full URL path.

## How It Works

Mỗi target (application instance) expose một endpoint HTTP trả về plain text theo Prometheus exposition format, ví dụ `http_requests_total{method="GET",status="200"} 15423`. Prometheus server, theo cấu hình `scrape_configs`, định kỳ gửi HTTP GET đến từng target (tự khám phá qua static config, Kubernetes service discovery, Consul...), đọc toàn bộ nội dung, và ghi mỗi sample (metric + labels + value + timestamp) vào TSDB nội bộ. Có 4 loại metric: **Counter** (chỉ tăng, dùng cho tổng số request/error — luôn phải bọc `rate()` hoặc `increase()` khi query vì giá trị tuyệt đối vô nghĩa và có thể reset về 0 khi process restart), **Gauge** (giá trị lên xuống tự do, vd. memory hiện tại, số connection đang mở), **Histogram** (đếm số lượng observation rơi vào các bucket biên định sẵn, cho phép tính percentile xấp xỉ qua `histogram_quantile()`), và **Summary** (tính percentile ngay tại client, không thể aggregate percentile giữa nhiều instance được). Mỗi combination duy nhất của metric name + label set tạo thành một time-series độc lập được lưu trữ và index riêng trong TSDB — đây chính là nguồn gốc của vấn đề cardinality: một metric có 5 label, mỗi label có 100 giá trị khác nhau, tạo ra tới 100^5 series tiềm năng, mỗi series chiếm bộ nhớ cho index (label matching) và chunk lưu trữ trên đĩa. Prometheus dùng mô hình lưu trữ dạng block 2 giờ (head block trong RAM, sau đó compact xuống đĩa dạng immutable block), nên cardinality cao gây áp lực trực tiếp lên cả RAM (head block index) lẫn CPU (compaction, query cần quét nhiều series). PromQL truy vấn dựa trên việc match label selector rồi áp hàm range vector (`rate`, `increase`) hoặc instant vector (`sum`, `avg`, `histogram_quantile`), và mọi vector matching (`+`, `/`, `on()`, `group_left`) đều dựa trên việc so khớp label set giữa hai series — nếu label không khớp tuyệt đối, kết quả bị drop khỏi output mà không có cảnh báo lỗi.

## Production Architecture

Trong một cluster Kubernetes chạy hàng chục microservice, Prometheus (thường qua kube-prometheus-stack) dùng service discovery để tự động phát hiện pod mới qua annotation (`prometheus.io/scrape: "true"`), scrape metric từ mỗi pod, đồng thời scrape thêm `kube-state-metrics` và `node-exporter` để có metric về hạ tầng. Vì một Prometheus server đơn không scale vô hạn theo số target và cardinality, kiến trúc production lớn dùng **Thanos** hoặc **Cortex/Mimir** để federation nhiều Prometheus instance thành một view toàn cục và lưu trữ dài hạn trên object storage (S3), vì Prometheus mặc định chỉ giữ dữ liệu local vài tuần. Alerting không nằm trong Prometheus server mà tách riêng qua **Alertmanager**, nhận alert rule đã trigger từ Prometheus (qua PromQL threshold, `for` duration) và lo việc dedup, group, route đến Slack/PagerDuty. Dashboard (thường Grafana) query PromQL trực tiếp vào Prometheus hoặc qua Thanos Query layer để hiển thị realtime.

## Trade-offs

Mô hình pull-based đơn giản hóa việc quản lý client (chỉ cần expose HTTP endpoint, không cần biết địa chỉ server giám sát), nhưng khó áp dụng cho batch job ngắn hạn chạy xong rồi tắt trước khi Prometheus kịp scrape — phải dùng thêm Pushgateway như một lớp trung gian, làm mất một phần lợi ích pull-based ban đầu. Lưu trữ time-series dạng local TSDB cho tốc độ query rất nhanh trong vài giờ/ngày gần nhất, nhưng không phải giải pháp lưu trữ dài hạn hay có khả năng chịu lỗi cao (single point of failure) nếu không kết hợp Thanos/Mimir, đánh đổi độ đơn giản vận hành lấy thêm một tầng hạ tầng phải duy trì. Cardinality cao cho phép truy vấn chi tiết đến từng chiều dữ liệu (per-user, per-endpoint...) nhưng đổi lại chi phí RAM/CPU tăng phi tuyến, nên production luôn phải đánh đổi giữa độ chi tiết của metric và khả năng chịu tải của chính hệ thống giám sát.

## Best Practices

- Không bao giờ gắn label có giá trị unbounded (user ID, session ID, full request path, timestamp) vào metric — dùng log hoặc trace cho những chiều dữ liệu đó, không dùng metric.
- Luôn bọc counter bằng `rate()` hoặc `increase()` trước khi cộng/so sánh, không bao giờ đọc giá trị tuyệt đối của counter trực tiếp.
- Định nghĩa histogram bucket phù hợp với phân bố latency thực tế của service (vd. nhiều bucket quanh SLO threshold), vì bucket sai làm `histogram_quantile()` cho kết quả sai lệch đáng kể.
- Theo dõi chính cardinality của Prometheus (`prometheus_tsdb_head_series`, `scrape_samples_scraped`) như một metric vận hành, đặt cảnh báo khi số series tăng bất thường.
- Đặt retention và remote-write hợp lý (Thanos/Mimir) cho dữ liệu cần giữ dài hạn, không cố tăng local retention của Prometheus vì TSDB không tối ưu cho lưu trữ nhiều tháng/năm.

## Common Mistakes

- Gắn `user_id` hoặc full URL (bao gồm query string) làm label, vô tình biến một metric từ vài trăm series thành hàng triệu series chỉ sau vài giờ chạy production.
- Dùng `avg()` để gộp percentile (p99) từ nhiều instance lại — trung bình cộng của các percentile không phải là percentile đúng của tổng thể, kết quả sai về mặt toán học.
- Quên rằng Summary không thể aggregate percentile giữa các instance (mỗi instance tự tính riêng), rồi cố `avg()` hay `sum()` các Summary quantile lại với nhau.
- Đặt scrape interval quá ngắn (vd. 1s) cho mọi target mà không cân nhắc số lượng target và cardinality, khiến CPU/network overhead tăng không cần thiết trong khi độ chi tiết thực tế không đổi nhiều so với 15s.
- Không set `for` duration trong alert rule, khiến alert trigger ngay ở một spike ngắn hạn (noise) thay vì chỉ báo động khi điều kiện thực sự kéo dài.

## Interview Questions

**Hỏi**: Vì sao Prometheus dùng mô hình pull thay vì push, và khi nào mô hình pull gặp khó khăn?

**Trả lời**: Pull-based giúp Prometheus server kiểm soát tần suất và tải scrape tập trung, dễ phát hiện target down (scrape thất bại), và client chỉ cần expose endpoint đơn giản không cần biết địa chỉ giám sát. Mô hình này gặp khó với batch job ngắn hạn chạy xong tắt máy trước khi kịp bị scrape — phải dùng Pushgateway làm trung gian lưu tạm giá trị để Prometheus pull lại sau.

**Hỏi**: Cardinality cao gây ra vấn đề gì cụ thể trong Prometheus, và làm sao phát hiện sớm?

**Trả lời**: Mỗi tổ hợp label duy nhất là một time-series riêng, chiếm bộ nhớ cho index trong head block (đang ở RAM) và chunk lưu trữ; cardinality tăng đột biến (vd. do label unbounded) làm RAM tăng phi tuyến và có thể OOM-kill Prometheus. Phát hiện sớm qua theo dõi `prometheus_tsdb_head_series` hoặc `scrape_samples_scraped` như một metric cảnh báo, thay vì chỉ nhận ra khi Prometheus đã sập.

**Hỏi**: Tại sao phải dùng `rate()` với counter thay vì đọc giá trị trực tiếp?

**Trả lời**: Counter chỉ tăng và bị reset về 0 khi process restart, nên giá trị tuyệt đối tại một thời điểm không phản ánh throughput mà chỉ là tổng lũy kế ngẫu nhiên tùy thời điểm đo. `rate()` tính tốc độ tăng trung bình trong một range vector, tự động xử lý counter reset, cho ra số liệu có ý nghĩa như request/giây.

## Summary

Prometheus thu thập metric theo mô hình pull-based, định kỳ scrape endpoint HTTP mà mỗi service tự expose, lưu dữ liệu dưới dạng time-series đa chiều theo metric name và label set. PromQL cho phép truy vấn, tính rate, aggregate và join giữa các series theo label, nhưng đòi hỏi hiểu đúng ngữ nghĩa của từng loại metric (Counter/Gauge/Histogram/Summary) để không tính sai. Vấn đề vận hành lớn nhất là cardinality: mỗi tổ hợp label mới là một series mới, và label có giá trị unbounded có thể làm bùng nổ số series, tốn RAM/CPU đến mức Prometheus tự sập. Kiến trúc production thường kết hợp Thanos hoặc Mimir để lưu trữ dài hạn và scale ngang, cùng Alertmanager để xử lý alert tách biệt khỏi Prometheus server.

## Knowledge Graph

- Grafana — lớp visualization phổ biến nhất query trực tiếp PromQL từ Prometheus.
- Alertmanager — tầng xử lý alert (dedup, group, route) nhận trigger từ Prometheus, tách biệt khỏi việc lưu trữ metric.
- Thanos/Cortex/Mimir — giải pháp mở rộng lưu trữ dài hạn và federation nhiều Prometheus instance.
- Distributed Tracing — bổ sung cho Prometheus khi cần điều tra chi tiết một request cụ thể thay vì số liệu tổng hợp.
- Kubernetes Service Discovery — cơ chế Prometheus dùng để tự động phát hiện target scrape trong cluster.
- Time-Series Database — nền tảng lưu trữ mà Prometheus TSDB triển khai, chia sẻ các vấn đề về cardinality với mọi TSDB khác (InfluxDB, VictoriaMetrics...).

## Five Things To Remember

- Prometheus scrape (pull) metric từ endpoint HTTP theo chu kỳ, không phải service tự push dữ liệu đi.
- Luôn dùng `rate()`/`increase()` với Counter, không bao giờ đọc giá trị tuyệt đối trực tiếp.
- Mỗi tổ hợp label duy nhất là một time-series riêng — label unbounded (user ID, full path) là nguyên nhân hàng đầu gây bùng nổ cardinality và OOM.
- Không `avg()` các percentile từ nhiều instance lại với nhau, kết quả sai về mặt toán học.
- Prometheus không phải kho lưu trữ dài hạn mặc định — cần Thanos/Mimir cho retention dài và high availability.
