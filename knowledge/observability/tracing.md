---
id: tracing
title: Distributed Tracing
tags: ["observability"]
---

# Distributed Tracing

> Status: Draft

## Problem

Một request "checkout" trong hệ thống microservices đi qua `api-gateway` → `order-service` → `payment-service` → `inventory-service` → `notification-service`, mỗi service log riêng vào stdout/file của chính nó, thường được thu thập vào một hệ thống log tập trung (ELK, Loki) nhưng không có mối liên kết nào giữa các dòng log của cùng một request. Khi client báo request mất 4.2 giây (SLA là 500ms), engineer chỉ có log riêng lẻ của từng service, không biết 4.2 giây đó nằm ở đâu — có thể `payment-service` gọi ngân hàng mất 3.8 giây, có thể `inventory-service` bị deadlock, có thể chỉ là network retry giữa hai service. Không có ID nào xuyên suốt để nối các log lại theo đúng thứ tự nhân quả của một request, engineer buộc phải đoán timestamp gần nhau, dò log thủ công qua 5 service khác nhau, và thường sai vì nhiều request chạy song song có timestamp xen kẽ nhau trong cùng khung giờ.

## Pain Points

- Debug latency trong hệ thống microservices mất hàng giờ vì phải grep log theo timestamp gần đúng qua nhiều service, dễ nhầm request này với request khác chạy song song cùng thời điểm.
- Không biết chính xác service nào là bottleneck: đội `payment` và đội `inventory` đổ lỗi cho nhau vì mỗi bên chỉ nhìn thấy latency phía mình, không thấy được toàn cảnh một request đi qua bao nhiêu hop và tốn bao lâu ở mỗi hop.
- Lỗi cascading (một service downstream chậm làm timeout hàng loạt ở upstream) không thể phát hiện gốc rễ nếu chỉ nhìn metrics tổng hợp (p99 latency) — metrics cho biết "có vấn đề" nhưng không cho biết "vấn đề ở request nào, ở bước nào".
- Retry và fan-out ẩn (một request gọi 1 lần ở tầng gateway nhưng thực ra tạo ra 3 lần gọi downstream do retry hoặc gọi song song nhiều service) không thể nhìn thấy nếu không có cấu trúc cha-con giữa các lời gọi, khiến việc ước tính chi phí thực sự của một request (số lượng DB query, số lần gọi service khác) gần như không thể.

## Solution

Distributed tracing gắn một **trace ID** duy nhất cho toàn bộ vòng đời một request ngay từ điểm vào hệ thống (gateway), và truyền (propagate) ID này qua mọi lời gọi giữa các service — qua HTTP header, message queue header, hoặc gRPC metadata. Mỗi đơn vị công việc trong request (một lời gọi HTTP, một query DB, một lần xử lý message) được ghi lại thành một **span** — có tên, thời điểm bắt đầu/kết thúc, và tham chiếu tới span cha đã gọi nó. Toàn bộ các span cùng trace ID ghép lại tạo thành một cây (trace) phản ánh đúng cấu trúc nhân quả và thời gian thực tế của request, cho phép engineer nhìn thấy trực quan: request vào lúc nào, đi qua service nào theo thứ tự nào, và mỗi bước tốn bao lâu — trả lời trực tiếp câu hỏi "latency nằm ở đâu" mà log rời rạc không trả lời được.

## How It Works

Một trace bao gồm nhiều **span**, mỗi span có: `trace_id` (chung cho cả trace), `span_id` (riêng cho span này), `parent_span_id` (span nào gọi ra span này, rỗng nếu là root span), `operation_name`, `start_time`, `duration`, và `attributes` (key-value tùy ý, vd. `http.method`, `db.statement`, `http.status_code`). Khi request vào `api-gateway`, gateway tạo root span mới với `trace_id` mới (nếu chưa có) và `span_id` random. Khi gateway gọi `order-service` qua HTTP, nó chèn `trace_id` và `span_id` hiện tại vào HTTP header — chuẩn phổ biến nhất hiện nay là **W3C Trace Context** với header `traceparent: 00-{trace_id}-{parent_span_id}-{flags}`. `order-service` nhận header này, biết mình đang là con của span đó, tạo span mới cho chính nó với `parent_span_id` = span_id nhận được, rồi tiếp tục propagate xuống `payment-service` theo đúng cách. Đây gọi là **context propagation** — nếu một service trong chuỗi quên đọc header và tạo trace_id mới thay vì kế thừa, trace bị đứt gãy thành hai cây rời rạc, mất hoàn toàn khả năng nhìn thấy quan hệ cha-con qua điểm đứt đó.

Việc tạo span không tự động xảy ra — cần **instrumentation**, tức code (thường qua middleware/interceptor) tự động tạo span quanh mỗi lời gọi HTTP outbound/inbound, mỗi query DB, mỗi lần publish/consume message. OpenTelemetry (chuẩn hiện tại, kế thừa OpenTracing và OpenCensus) cung cấp SDK tự động instrument các thư viện phổ biến (HTTP client, gRPC, driver DB) để không phải viết tay từng span. Sau khi span kết thúc, nó được gửi (thường bất đồng bộ qua batch, không block request chính) tới một **collector** (OpenTelemetry Collector), rồi lưu vào backend lưu trữ trace (Jaeger, Tempo, Zipkin) để truy vấn và visualize dưới dạng timeline/waterfall — mỗi span là một thanh ngang, độ dài là duration, vị trí lồng nhau thể hiện quan hệ cha-con, cho phép nhìn ngay span nào chiếm phần lớn thời gian của cả trace.

## Production Architecture

Trong một hệ thống production điển hình, mỗi service chạy OpenTelemetry SDK (hoặc Datadog APM agent, hoặc New Relic agent) tự động instrument framework HTTP (Express, Spring, gRPC) và driver DB (JDBC, pg driver) mà không cần sửa business logic. Span được export tới OpenTelemetry Collector chạy dạng sidecar hoặc daemonset trong Kubernetes, collector này gom span, có thể sample (chỉ giữ lại một phần trace để giảm chi phí lưu trữ và băng thông), rồi đẩy tới backend như Jaeger hoặc Grafana Tempo — Tempo thường đi kèm Grafana để engineer nhảy trực tiếp từ dashboard metrics (thấy p99 latency tăng) sang trace cụ thể (exemplar) gây ra latency đó. Correlation giữa log và trace thường thực hiện bằng cách chèn `trace_id` vào mọi dòng log (structured logging), để từ một trace cụ thể có thể nhảy ngược lại xem log chi tiết của service tại đúng thời điểm đó — kết hợp ba trụ cột observability (metrics, logs, traces) thành một luồng điều tra liền mạch: metrics báo có vấn đề → trace chỉ ra request cụ thể và span nào chậm → log cho biết chi tiết tại sao span đó chậm (lỗi gì, giá trị nào). Với hệ thống dùng message queue (Kafka), trace context được nhúng vào message header, consumer đọc lại để nối tiếp trace qua ranh giới bất đồng bộ — nếu không làm điều này, mọi xử lý sau khi qua queue trở thành trace mới, mất liên kết với request gốc.

## Trade-offs

Instrumentation đầy đủ (mọi HTTP call, mọi DB query đều tạo span) sinh ra khối lượng dữ liệu khổng lồ ở hệ thống traffic cao — lưu 100% trace của một hệ thống hàng chục nghìn request/giây tốn chi phí lưu trữ và băng thông network đáng kể, buộc phải **sampling** (chỉ giữ 1-10% trace, hoặc giữ toàn bộ trace có lỗi/latency cao qua tail-based sampling), đánh đổi việc không phải lúc nào cũng có trace đầy đủ cho một request cụ thể khi cần điều tra sau này. Context propagation đòi hỏi mọi service trong chuỗi (kể cả service của bên thứ ba hoặc thư viện cũ không hỗ trợ OpenTelemetry) phải đọc và truyền tiếp header đúng chuẩn — chỉ cần một service ở giữa chuỗi không propagate đúng là toàn bộ downstream từ điểm đó trở thành trace rời rạc, giá trị của tracing giảm mạnh ở đúng những hệ thống phức tạp nhất (nhiều team, nhiều ngôn ngữ, nhiều thư viện) nơi tracing cần thiết nhất. Thêm middleware tạo span vào mọi lời gọi cũng có overhead runtime thực sự (dù nhỏ, thường vài microsecond mỗi span), và nếu export span đồng bộ (không qua batch/async) có thể ảnh hưởng trực tiếp tới latency của request đang được đo — tự làm sai lệch chính số liệu mình muốn quan sát.

## Best Practices

- Dùng chuẩn W3C Trace Context và OpenTelemetry SDK thay vì tự chế cơ chế propagate riêng, để tương thích với mọi thư viện, service mesh (Istio), và backend trace (Jaeger, Tempo, Datadog) mà không cần viết lại khi đổi vendor.
- Áp dụng tail-based sampling (quyết định giữ/bỏ trace sau khi trace hoàn tất) để luôn giữ lại 100% trace có lỗi hoặc latency vượt ngưỡng, chỉ sample ngẫu nhiên phần trace "bình thường" — tránh mất đúng những trace quan trọng nhất khi cần điều tra.
- Chèn `trace_id` vào structured log của mọi service để có thể nhảy giữa metrics, trace và log trong cùng một luồng điều tra, thay vì coi ba hệ thống này tách biệt.
- Đảm bảo context propagate xuyên suốt mọi ranh giới bất đồng bộ (message queue, cron job, background worker), không chỉ HTTP synchronous call — kiểm tra kỹ các điểm chuyển giao qua Kafka/SQS vì đây là nơi dễ bị đứt trace nhất.
- Đặt tên span và attributes có ý nghĩa nghiệp vụ (`order.id`, `payment.provider`) chứ không chỉ tên kỹ thuật chung chung, để khi nhìn waterfall có thể hiểu ngay request đang xử lý gì mà không cần đọc code.

## Common Mistakes

- Chỉ instrument một phần service trong chuỗi (thường do team mới join sau, hoặc service dùng ngôn ngữ ít được hỗ trợ SDK tốt), khiến trace bị đứt gãy ngay tại điểm không instrument, mất khả năng thấy toàn cảnh dù các service khác đã setup đầy đủ.
- Quên propagate context qua ranh giới bất đồng bộ (publish message vào Kafka mà không nhúng trace header), khiến mọi xử lý sau consumer trở thành trace mới hoàn toàn tách biệt với request gốc gây ra nó.
- Sample ngẫu nhiên đồng đều (head-based sampling) mà không ưu tiên giữ trace có lỗi, dẫn tới tình huống trớ trêu: đúng lúc cần trace của request bị lỗi để điều tra thì trace đó lại không được giữ lại vì bị loại bỏ ngẫu nhiên từ đầu.
- Tạo quá nhiều span chi tiết (span cho mỗi dòng code, mỗi phép tính nhỏ) làm trace trở nên rối rắm khó đọc và tăng overhead không cần thiết, thay vì chỉ span ở các ranh giới có ý nghĩa (network call, DB query, xử lý business logic quan trọng).
- Coi tracing là công cụ độc lập, không liên kết với log và metrics — mất chi phí đầu tư instrument nhưng vẫn phải dò log thủ công vì không có `trace_id` trong log để nhảy qua lại giữa hai hệ thống.

## Interview Questions

**Hỏi**: Trace và span khác nhau như thế nào, và chúng liên hệ với nhau ra sao?

**Trả lời**: Trace đại diện cho toàn bộ vòng đời của một request xuyên suốt nhiều service, xác định bằng một `trace_id` duy nhất. Span là một đơn vị công việc cụ thể trong trace đó (một lời gọi HTTP, một query DB), có `span_id` riêng và `parent_span_id` trỏ tới span đã gọi nó — nhiều span ghép lại theo quan hệ cha-con tạo thành cây, chính là trace hoàn chỉnh.

**Hỏi**: Context propagation là gì, và vì sao một service quên propagate lại làm hỏng toàn bộ giá trị của tracing?

**Trả lời**: Context propagation là việc truyền `trace_id` và `span_id` hiện tại qua header (HTTP, message queue) khi một service gọi service khác, để service nhận biết mình là con của span nào trong trace nào. Nếu một service quên đọc header và tự tạo `trace_id` mới, mọi lời gọi downstream từ đó trở thành một trace tách biệt, mất hoàn toàn quan hệ nhân quả với request gốc — trace bị đứt gãy đúng tại điểm cần theo dõi nhất.

**Hỏi**: Tại sao không nên lưu 100% trace ở hệ thống traffic cao, và tail-based sampling giải quyết vấn đề gì mà head-based sampling không giải quyết được?

**Trả lời**: Lưu toàn bộ trace ở traffic cao (chục nghìn request/giây) tốn chi phí lưu trữ và băng thông không tương xứng với giá trị thu được, vì phần lớn request bình thường không cần điều tra. Head-based sampling quyết định giữ/bỏ ngay từ đầu request nên không biết trước request nào sẽ lỗi hay chậm, dễ bỏ sót đúng trace cần thiết; tail-based sampling chờ trace hoàn tất rồi mới quyết định, cho phép luôn giữ lại 100% trace có lỗi hoặc latency cao trong khi vẫn sample ngẫu nhiên phần còn lại.

## Summary

Distributed tracing giải quyết bài toán không thể xác định latency nằm ở đâu trong một request đi qua nhiều service, bằng cách gắn `trace_id` xuyên suốt và ghi lại mỗi đơn vị công việc thành một span có quan hệ cha-con rõ ràng. Context propagation qua header (chuẩn W3C Trace Context, thường qua OpenTelemetry SDK) là cơ chế cốt lõi, và chỉ cần đứt propagation ở một điểm là mất toàn bộ giá trị quan sát từ điểm đó trở đi. Trong production, trace thường lưu ở Jaeger/Tempo, liên kết với log qua `trace_id` nhúng trong structured log, và với metrics qua exemplar, tạo thành một luồng điều tra liền mạch. Đánh đổi chính là chi phí lưu trữ và overhead runtime ở traffic cao, giải quyết bằng sampling — ưu tiên tail-based sampling để không bỏ sót trace có lỗi. Tracing chỉ phát huy giá trị đầy đủ khi instrument nhất quán trên toàn bộ chuỗi service, kể cả qua ranh giới bất đồng bộ như message queue.

## Knowledge Graph

- OpenTelemetry — chuẩn instrumentation và SDK phổ biến nhất hiện nay để tạo span và propagate context.
- Structured Logging — kết hợp với `trace_id` để nhảy qua lại giữa log và trace trong cùng một luồng điều tra.
- Saga Pattern — chuỗi giao dịch phân tán qua nhiều service mà tracing giúp quan sát được thứ tự và latency từng bước thực thi thực tế.
- Service Mesh (Istio/Envoy) — có thể tự động inject và propagate trace header ở tầng network mà không cần sửa code ứng dụng.
- Circuit Breaker — quyết định ngắt mạch dựa trên latency/error rate mà trace giúp xác định chính xác service nào gây ra.
- Metrics và Alerting — cặp đôi với tracing theo mô hình "metrics báo có vấn đề, trace chỉ ra request và bước nào gây ra vấn đề".

## Five Things To Remember

- Trace ID xuyên suốt một request, span đại diện một đơn vị công việc cụ thể với quan hệ cha-con tới span khác.
- Context propagation qua header là cơ chế sống còn — đứt propagation ở một service là mất trace từ điểm đó trở đi.
- Instrumentation cần nhất quán trên toàn bộ chuỗi, kể cả qua ranh giới bất đồng bộ như message queue, không chỉ HTTP synchronous.
- Tail-based sampling giữ lại trace có lỗi/latency cao tốt hơn head-based sampling vì quyết định sau khi biết kết quả trace.
- Tracing phát huy giá trị lớn nhất khi liên kết được với log (qua trace_id) và metrics (qua exemplar) thành một luồng điều tra duy nhất.
