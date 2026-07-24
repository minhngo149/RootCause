---
id: metrics
title: Metrics
tags: ["observability"]
---

# Metrics

> Status: Draft

## Problem

Một service chạy production nhưng đội vận hành chỉ biết nó "đang chạy" hay "đang sập" qua health check nhị phân — không có cách nào trả lời câu hỏi "API đang chậm dần từ bao giờ", "connection pool DB còn bao nhiêu slot trống", hay "tỷ lệ lỗi 5xx trong 5 phút qua là bao nhiêu phần trăm". Khi log là nguồn dữ liệu duy nhất, muốn biết p99 latency của một endpoint trong giờ cao điểm nghĩa là phải grep hàng triệu dòng log rồi tính toán thủ công — không thể alert theo thời gian thực, không thể vẽ dashboard, và không thể phát hiện xu hướng suy giảm trước khi nó thành outage.

## Pain Points

- Không có gauge theo dõi số connection đang mở tới DB, connection pool cạn kiệt âm thầm trong 20 phút trước khi request bắt đầu timeout hàng loạt — team chỉ biết khi user report, không biết trước.
- Thiếu histogram đo latency, đội vận hành chỉ thấy "trung bình 200ms" trong khi thực tế 5% request mất hơn 5 giây (long tail bị trung bình cộng che mất), khiến trải nghiệm một nhóm user cụ thể tệ mà không ai phát hiện.
- Không đếm được rate lỗi theo từng endpoint, một bug chỉ ảnh hưởng 2% traffic (vd. lỗi ở một edge case thanh toán) lẫn vào tổng traffic ổn định, không bao giờ vượt ngưỡng alert dựa trên log aggregation thô.
- Chi phí vận hành tăng khi phải dựa vào log để trả lời câu hỏi định lượng: query log trên Elasticsearch cho hàng tỷ dòng tốn CPU/RAM gấp hàng chục lần so với query một time-series đã được aggregate sẵn dưới dạng metric.

## Solution

Metrics là dữ liệu số được đo và tổng hợp theo thời gian, biểu diễn trạng thái hệ thống dưới dạng time-series thay vì text tự do như log. Ba kiểu cơ bản — counter, gauge, histogram — mỗi kiểu phù hợp với một loại câu hỏi khác nhau (tổng số sự kiện, giá trị hiện tại, phân bố giá trị). RED method (Rate/Errors/Duration) và USE method (Utilization/Saturation/Errors) là hai khung chuẩn hóa giúp quyết định nên đo cái gì cho từng loại thành phần trong hệ thống, tránh tình trạng đo tùy hứng, thiếu chỉ số quan trọng hoặc đo thừa những thứ không ai dùng.

## How It Works

**Counter** là giá trị chỉ tăng (hoặc reset về 0 khi process restart), dùng để đếm số sự kiện đã xảy ra — tổng số request, tổng số lỗi, tổng số message đã xử lý. Counter không tự nó có ý nghĩa trực tiếp; giá trị hữu ích là **rate of change** theo thời gian, tính bằng công thức như `rate(http_requests_total[5m])` trong PromQL — lấy đạo hàm của counter qua cửa sổ thời gian để ra request/giây. Vì counter chỉ tăng, hệ thống monitoring phải tự xử lý trường hợp counter reset về 0 (process restart) bằng cách phát hiện giá trị giảm đột ngột và bỏ qua điểm đó khi tính rate, thay vì hiểu nhầm thành traffic giảm về 0.

**Gauge** là giá trị có thể tăng hoặc giảm tự do tại một thời điểm, biểu diễn trạng thái hiện tại — số connection đang mở, dung lượng queue, nhiệt độ CPU, số goroutine đang chạy. Khác với counter, gauge không cần tính rate, giá trị tại mỗi lần scrape đã có ý nghĩa trực tiếp. Gauge phù hợp cho bất kỳ đại lượng nào có thể đi cả hai chiều, và là kiểu duy nhất phù hợp để biểu diễn "mức độ bão hòa tài nguyên" trong USE method.

**Histogram** đo phân bố của một đại lượng liên tục (thường là latency hoặc kích thước response) bằng cách chia giá trị vào các bucket có ngưỡng định trước (vd. `le="0.1"`, `le="0.5"`, `le="1"`, `le="+Inf"`), mỗi bucket là một counter đếm số quan sát có giá trị ≤ ngưỡng đó. Từ các bucket này, hệ thống query (Prometheus, VictoriaMetrics) tính ra percentile xấp xỉ (p50, p95, p99) bằng nội suy tuyến tính giữa hai bucket kề nhau — đây là lý do việc chọn bucket boundary hợp lý (bám sát SLA thực tế, vd. 50ms/100ms/250ms/500ms/1s/2.5s cho một API có SLA 500ms) quan trọng hơn nhiều so với chọn nhiều bucket dày đặc: bucket sai lệch cho ra percentile sai lệch dù công thức tính đúng. Histogram khác với **summary** (một biến thể tính percentile chính xác ngay tại client) ở chỗ histogram cho phép aggregate percentile trên nhiều instance (vì bucket counter cộng dồn được), còn summary thì không — đây là lý do Prometheus khuyến nghị histogram cho hầu hết use case multi-instance.

**RED method** áp dụng cho mọi service xử lý request (HTTP API, gRPC service, message consumer): đo **Rate** (số request/giây, counter), **Errors** (số request lỗi/giây, counter riêng theo status code hoặc theo nhãn `error="true"`), và **Duration** (phân bố thời gian xử lý, histogram). Ba chỉ số này trả lời trực tiếp câu hỏi "service có đang phục vụ tốt không" từ góc nhìn của client gọi vào nó.

**USE method** áp dụng cho tài nguyên hữu hạn (CPU, RAM, disk I/O, connection pool, thread pool): đo **Utilization** (phần trăm thời gian tài nguyên bận, gauge), **Saturation** (mức độ công việc xếp hàng chờ vượt quá khả năng xử lý, vd. độ dài queue, gauge), và **Errors** (số lỗi liên quan tài nguyên đó, vd. out-of-memory kill, disk write error, counter). USE trả lời câu hỏi "tài nguyên nào đang là nút thắt" từ góc nhìn nội bộ hệ thống, bổ trợ cho RED vốn chỉ nhìn từ góc độ request.

## Production Architecture

Trong một service Go/Node/Java điển hình, client library (`prometheus/client_golang`, `prom-client`, `micrometer`) đăng ký các metric ngay trong code — mỗi HTTP handler được bọc bởi middleware tự động tăng counter `http_requests_total{method, path, status}` và ghi nhận giá trị vào histogram `http_request_duration_seconds{method, path}`. Prometheus server định kỳ scrape endpoint `/metrics` (thường mỗi 15s) theo mô hình pull, lưu dữ liệu vào time-series database dạng append-only, mỗi time-series được định danh bởi tên metric cộng tập nhãn (label) — vd. `http_requests_total{method="POST", path="/checkout", status="500"}` là một time-series riêng biệt với `status="200"`. Grafana đọc dữ liệu qua PromQL để vẽ dashboard theo RED (một dashboard tổng quan cho từng service) và USE (một dashboard cho hạ tầng: node exporter cung cấp CPU/RAM/disk, cAdvisor cung cấp resource theo container). Alertmanager nhận rule từ Prometheus (vd. `rate(http_requests_total{status=~"5.."}[5m]) / rate(http_requests_total[5m]) > 0.05`) để bắn cảnh báo khi tỷ lệ lỗi vượt ngưỡng trong cửa sổ trượt, độc lập với dashboard hiển thị. Ở quy mô lớn, cardinality của label (vd. thêm `user_id` làm label) là rủi ro vận hành nghiêm trọng — mỗi giá trị label mới tạo ra một time-series mới, và hàng triệu user_id có thể làm nổ số time-series lên hàng chục triệu, khiến Prometheus hết RAM hoặc query timeout; đây là lý do các hệ thống lớn (Datadog, Grafana Mimir) đều giới hạn cardinality hoặc tính phí theo số time-series.

## Trade-offs

Metrics đánh đổi độ chi tiết lấy hiệu năng và chi phí lưu trữ: một histogram với nhiều bucket cho percentile chính xác hơn nhưng tăng số time-series tuyến tính theo số bucket, còn gộp nhiều nhãn (label) để phân tích chi tiết hơn (theo user, theo region, theo tenant) lại tăng cardinality theo cấp số nhân — đây là đánh đổi cốt lõi giữa khả năng debug chi tiết và chi phí vận hành hệ thống metrics. Metrics cũng mất thông tin theo thiết kế: một counter tăng lên biết "có bao nhiêu lỗi" nhưng không biết "lỗi gì, ở request nào, với input nào" — muốn biết chi tiết đó vẫn phải quay lại log hoặc trace, nghĩa là metrics không thay thế được logging mà chỉ bổ trợ cho một lớp câu hỏi khác (định lượng, xu hướng, alert) so với log (định tính, chi tiết từng sự kiện). Mô hình pull-based như Prometheus đơn giản và dễ vận hành nhưng không phù hợp cho job ngắn hạn (batch job, cron job kết thúc trước khi bị scrape) — buộc phải dùng thêm Pushgateway, vốn tự nó lại là một single point of failure và không tự động dọn dữ liệu cũ nếu cấu hình sai.

## Best Practices

- Chọn đúng kiểu metric cho đúng câu hỏi: counter cho "đã xảy ra bao nhiêu lần", gauge cho "hiện tại là bao nhiêu", histogram cho "phân bố ra sao" — không dùng gauge để đếm sự kiện cộng dồn.
- Đặt bucket boundary của histogram bám sát SLA thực tế của endpoint đó (vd. nếu SLA là 300ms thì cần bucket quanh 100ms/250ms/300ms/500ms), không dùng bucket mặc định của thư viện một cách máy móc cho mọi service.
- Giới hạn nghiêm ngặt các giá trị được dùng làm label — chỉ dùng nhãn có tập giá trị hữu hạn và ổn định (method, status code, region), tuyệt đối không dùng user_id, request_id, hay email làm label.
- Áp dụng RED method cho mọi service hướng request và USE method cho mọi tài nguyên hữu hạn như một checklist tối thiểu, trước khi nghĩ tới các metric nghiệp vụ tùy chỉnh.
- Alert dựa trên rate và tỷ lệ theo cửa sổ trượt (vd. tỷ lệ lỗi trong 5 phút), không alert trên giá trị tuyệt đối tức thời vốn dễ gây báo động giả khi có một spike ngắn.

## Common Mistakes

- Dùng user_id, session_id, hoặc IP làm label của metric, gây "cardinality explosion" khiến Prometheus hết bộ nhớ hoặc query chậm bất thường sau vài tuần chạy production.
- Nhầm gauge và counter — dùng counter để biểu diễn số connection đang mở (giá trị có thể giảm) khiến rate() cho ra kết quả vô nghĩa vì counter không được thiết kế để giảm.
- Chỉ đo giá trị trung bình (average) của latency thay vì histogram/percentile, che mất long tail latency ảnh hưởng tới một nhóm nhỏ nhưng quan trọng của user.
- Không đo Saturation trong USE method, chỉ đo Utilization — một CPU ở 70% utilization có vẻ ổn nhưng nếu queue độ dài request đang tăng liên tục (saturation cao), hệ thống sắp sập dù utilization chưa chạm 100%.
- Để metrics và alert threshold không đồng bộ với SLA thực tế của sản phẩm — alert ở p99 > 1s trong khi SLA cam kết với khách hàng là 500ms, khiến team phát hiện vi phạm SLA sau khi khách hàng đã report.

## Interview Questions

**Hỏi**: Khi nào dùng histogram thay vì summary để đo latency?

**Trả lời**: Histogram lưu counter theo bucket nên có thể cộng dồn (aggregate) giữa nhiều instance của cùng một service để tính percentile toàn cụm, trong khi summary tính percentile ngay tại client và các giá trị percentile đó không cộng dồn được giữa các instance. Trong môi trường có nhiều replica (gần như luôn đúng trong production), histogram là lựa chọn đúng; summary chỉ phù hợp khi chỉ cần percentile chính xác của một instance đơn lẻ và không cần aggregate.

**Hỏi**: Vì sao đo utilization CPU ở mức 70% chưa đủ để kết luận hệ thống khỏe mạnh?

**Trả lời**: Utilization chỉ cho biết tài nguyên bận bao nhiêu phần trăm thời gian, không cho biết công việc có đang xếp hàng chờ hay không. Cần đo thêm Saturation (độ dài queue, số request đang chờ xử lý) — một CPU 70% nhưng với queue đang tăng liên tục nghĩa là tốc độ công việc đến vượt tốc độ xử lý, hệ thống sẽ suy giảm dần dù utilization chưa chạm ngưỡng cao.

**Hỏi**: Cardinality của metric là gì và vì sao nó nguy hiểm ở quy mô lớn?

**Trả lời**: Cardinality là số tổ hợp giá trị nhãn (label) khác nhau của một metric — mỗi tổ hợp tạo ra một time-series riêng trong hệ thống lưu trữ. Dùng nhãn có tập giá trị không giới hạn (user_id, request_id) khiến số time-series tăng không kiểm soát, làm hệ thống monitoring hết RAM hoặc query timeout, đây là nguyên nhân phổ biến nhất khiến hệ thống Prometheus sập ở production.

## Summary

Metrics biểu diễn trạng thái hệ thống dưới dạng time-series định lượng, dùng ba kiểu cơ bản: counter (đếm cộng dồn), gauge (giá trị hiện tại tăng giảm tự do), và histogram (phân bố giá trị qua các bucket). RED method (Rate/Errors/Duration) chuẩn hóa cách đo cho mọi service hướng request, còn USE method (Utilization/Saturation/Errors) chuẩn hóa cách đo cho mọi tài nguyên hữu hạn, hai khung này bổ trợ nhau để trả lời cả góc nhìn từ client lẫn góc nhìn nội bộ hệ thống. Metrics không thay thế log — nó trả lời câu hỏi định lượng và xu hướng, còn log trả lời câu hỏi chi tiết từng sự kiện. Cardinality của label là rủi ro vận hành lớn nhất khi triển khai metrics ở quy mô production, và chọn sai kiểu metric hoặc sai bucket boundary khiến dữ liệu thu thập được tuy tồn tại nhưng vô dụng cho việc ra quyết định.

## Knowledge Graph

- Distributed Tracing — trả lời câu hỏi "request cụ thể này đi qua những service nào và chậm ở đâu", bổ trợ cho metrics vốn chỉ cho biết xu hướng tổng thể.
- Logging — cung cấp chi tiết định tính cho từng sự kiện, dùng để điều tra sâu sau khi metric/alert đã chỉ ra có vấn đề.
- Circuit Breaker — trạng thái Closed/Open/Half-Open của breaker thường được expose như một gauge metric để alert sớm trước khi user report sự cố.
- SLA/SLO/SLI — percentile từ histogram latency và tỷ lệ lỗi từ counter là nguồn dữ liệu trực tiếp để tính SLI, so sánh với SLO đã cam kết.
- Prometheus/Grafana — cặp công cụ phổ biến nhất triển khai mô hình pull-based scraping và truy vấn PromQL cho toàn bộ khái niệm counter/gauge/histogram nêu trên.
- Alerting — rule alert luôn được xây trên rate hoặc tỷ lệ tính từ metric theo cửa sổ trượt, không alert trực tiếp trên giá trị tuyệt đối tức thời.

## Five Things To Remember

- Counter chỉ tăng và cần tính rate; gauge tăng giảm tự do; histogram đo phân bố qua bucket để ra percentile.
- RED method (Rate/Errors/Duration) dùng cho service hướng request; USE method (Utilization/Saturation/Errors) dùng cho tài nguyên hữu hạn.
- Percentile (p95, p99) phản ánh trải nghiệm thực tế tốt hơn giá trị trung bình vốn che mất long tail latency.
- Không bao giờ dùng giá trị không giới hạn (user_id, request_id) làm label — cardinality explosion là nguyên nhân sập hệ thống monitoring phổ biến nhất.
- Metrics trả lời "có vấn đề không và xu hướng ra sao", log và trace trả lời "vấn đề cụ thể ở đâu" — ba lớp này luôn cần phối hợp.
</content>
