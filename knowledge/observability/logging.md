---
id: logging
title: Logging
tags: ["observability"]
---

# Logging

> Status: Draft

## Problem

Một request lỗi 500 ở production, và log chỉ ghi lại một dòng text tự do kiểu `console.log("error processing order")` không kèm order ID, user ID, hay bất kỳ ngữ cảnh nào. Engineer phải grep hàng chục nghìn dòng log rải rác trên nhiều instance, cố khớp thời gian bằng mắt để đoán dòng nào liên quan đến request nào. Khi hệ thống chạy trên nhiều service (order-service gọi payment-service gọi inventory-service), log của mỗi service nằm tách biệt và không có cách nào nối chúng lại thành một câu chuyện duy nhất của một request.

## Pain Points

- Log dạng free-text không thể query có cấu trúc (không filter theo field, không aggregate theo status code), nên incident response phụ thuộc vào `grep`/`Ctrl+F` may rủi thay vì truy vấn chính xác.
- Thiếu correlation ID khiến việc trace một request xuyên qua nhiều service tốn hàng giờ, engineer phải ước lượng theo timestamp và suy luận, dễ ghép nhầm log của request khác vào cùng một luồng.
- Log level dùng sai (mọi thứ đều `INFO` hoặc mọi thứ đều `ERROR`) làm log quan trọng chìm trong noise, hoặc ngược lại alert bị bỏ qua vì "lúc nào cũng đỏ".
- Log không structured không thể đẩy hiệu quả vào hệ thống tập trung (ELK, Loki, Datadog) — chi phí ingest/index tăng vọt vì phải parse regex trên text tự do thay vì đọc field JSON có sẵn.
- Log chứa dữ liệu nhạy cảm (password, token, số thẻ) do không có quy ước redact, trở thành lỗ hổng bảo mật khi log được lưu trữ hoặc chia sẻ cho bên thứ ba.

## Solution

Ba trụ cột giải quyết vấn đề trên: **structured logging** (ghi log dưới dạng key-value hoặc JSON thay vì câu văn tự do, để máy có thể parse và query), **log level** (phân loại mức độ nghiêm trọng — DEBUG, INFO, WARN, ERROR, FATAL — để lọc noise và định tuyến alert đúng chỗ), và **correlation ID** (một định danh duy nhất gắn vào mọi log của cùng một request, truyền xuyên suốt qua tất cả service nó đi qua, để nối lại thành một trace hoàn chỉnh).

## How It Works

**Structured logging** thay vì `logger.info("User " + userId + " logged in")` thì ghi `logger.info("user_login", {user_id: userId, ip: req.ip, method: "oauth"})`. Output là một dòng JSON: `{"timestamp":"...","level":"info","msg":"user_login","user_id":123,"ip":"1.2.3.4"}`. Mỗi field là một key riêng biệt trong object, không nhúng vào câu văn — nhờ vậy hệ thống log tập trung (Elasticsearch, Loki) index được từng field và cho phép query kiểu `user_id:123 AND level:error` thay vì regex trên text.

**Log level** hoạt động theo thứ tự mức độ tăng dần (thường: `TRACE < DEBUG < INFO < WARN < ERROR < FATAL`). Logger được cấu hình một ngưỡng (threshold) tại runtime — nếu ngưỡng là `INFO`, mọi log `DEBUG`/`TRACE` bị loại bỏ hoàn toàn trước khi ghi ra output, không tốn I/O. Điều quan trọng là log level không chỉ để lọc noise mà còn là tín hiệu định tuyến: `ERROR`/`FATAL` thường được cấu hình để bắn thẳng vào hệ thống alerting (PagerDuty, Opsgenie), trong khi `WARN` trở xuống chỉ lưu trữ để tra cứu khi cần. Một số logger (Pino, Winston, Zap) cho phép đổi ngưỡng log level tại runtime qua signal hoặc endpoint riêng, hữu ích khi cần tăng verbosity tạm thời để debug một service đang chạy mà không cần restart.

**Correlation ID** (còn gọi trace ID hoặc request ID) là một UUID được sinh ra tại điểm đầu tiên request đi vào hệ thống — thường ở API gateway hoặc load balancer, hoặc tại service đầu tiên nếu chưa có gateway. ID này được gắn vào mọi log entry phát sinh trong vòng đời xử lý request đó (thường qua context/thread-local storage hoặc middleware tự động inject), và quan trọng nhất: được truyền tiếp qua HTTP header (thường `X-Request-ID` hoặc `X-Correlation-ID`, hay chuẩn hóa hơn là `traceparent` theo W3C Trace Context) khi service A gọi service B. Service B đọc header này, nếu có thì tái sử dụng làm correlation ID cho toàn bộ log của chính nó khi xử lý request đó, nếu không có thì tự sinh mới (trường hợp entry point). Kết quả: khi query `correlation_id:abc-123` trên hệ thống log tập trung, ta thấy toàn bộ log từ mọi service mà request này đã đi qua, theo đúng thứ tự thời gian, dù chúng chạy trên hàng chục container khác nhau.

## Production Architecture

Trong một hệ thống microservice thương mại điện tử, request checkout đi qua API gateway → order-service → payment-service → inventory-service → notification-service. Gateway sinh correlation ID tại điểm vào, gắn vào response header để client debug được, đồng thời forward qua header nội bộ tới order-service. Mỗi service dùng middleware (Express middleware, Go net/http middleware, hoặc interceptor gRPC) tự động: đọc correlation ID từ incoming request, đưa vào logging context (dùng `AsyncLocalStorage` ở Node.js, `context.Context` ở Go, MDC — Mapped Diagnostic Context — ở Java/Logback), và tự động đính kèm vào mọi log entry phát sinh trong request đó mà code nghiệp vụ không cần truyền tay. Log JSON từ tất cả service được các agent (Fluent Bit, Vector, Filebeat) thu thập, đẩy vào một backend tập trung (Loki, Elasticsearch, hoặc Datadog Logs). Khi payment-service báo lỗi timeout tới ngân hàng, on-call chỉ cần lấy correlation ID từ alert, query trên Kibana/Grafana, và thấy ngay toàn bộ hành trình request: order-service tạo order lúc nào, payment-service gọi ngân hàng lúc nào và timeout sau bao lâu, có rollback ở inventory-service hay không — tất cả trong một view duy nhất, sắp theo timestamp.

## Trade-offs

Structured logging tăng kích thước mỗi dòng log (JSON overhead so với text thuần), và tăng CPU cho việc serialize — ở service có throughput cực cao (hàng chục nghìn request/giây), chi phí này có thể đáng kể nếu dùng logger serialize đồng bộ, chậm hơn logger bất đồng bộ/zero-allocation (như Zap ở Go, Pino ở Node.js được thiết kế riêng cho việc này). Correlation ID đòi hỏi kỷ luật truyền header xuyên suốt mọi lời gọi liên service, kể cả qua message queue (Kafka, RabbitMQ) — chỉ cần một service quên forward header là chuỗi trace bị đứt tại đó, và không có cách tự động phát hiện việc "quên" này ngoài code review hoặc middleware chuẩn hóa bắt buộc. Log level threshold quá thấp (để `DEBUG` ở production) sinh ra khối lượng log khổng lồ, tăng chi phí lưu trữ và băng thông ingest, trong khi threshold quá cao (`ERROR` only) làm mất khả năng debug khi sự cố xảy ra vì thiếu ngữ cảnh dẫn tới lỗi. Tập trung hóa log toàn hệ thống cũng tạo ra một single point of cost và đôi khi single point of failure — nếu backend log (Elasticsearch cluster) quá tải hoặc down, service không được thiết kế log bất đồng bộ/buffer sẽ bị block hoặc mất log.

## Best Practices

- Luôn log dưới dạng structured (JSON) ở production, không log câu văn tự do — dùng logger hỗ trợ sẵn (Pino, Winston, Zap, Logrus, structlog) thay vì tự ghép string.
- Sinh correlation ID tại điểm vào hệ thống (gateway/load balancer) và forward bắt buộc qua mọi lời gọi service-to-service, kể cả qua message queue (đặt vào message header/metadata, không chỉ HTTP).
- Dùng log level đúng ngữ nghĩa: `ERROR` chỉ cho lỗi cần con người can thiệp, `WARN` cho tình huống bất thường nhưng tự phục hồi được, `INFO` cho sự kiện nghiệp vụ quan trọng, `DEBUG` cho chi tiết chỉ cần khi điều tra — tránh log mọi thứ ở cùng một mức.
- Redact dữ liệu nhạy cảm (password, token, PII, số thẻ) tại tầng logger bằng danh sách field cố định hoặc middleware tự động, không dựa vào việc developer nhớ tự tay che.
- Đính kèm ngữ cảnh nghiệp vụ (user ID, order ID, tenant ID) vào log context ngay từ đầu request, để mọi log entry sau đó tự động có đủ thông tin filter mà không cần thêm bằng tay ở từng điểm log.

## Common Mistakes

- Dùng `console.log`/`print` trực tiếp ở code production thay vì logger có cấu hình level, structured output, và khả năng redact.
- Sinh correlation ID mới ở mỗi service thay vì kế thừa từ header của service gọi trước, làm mất khả năng nối trace xuyên suốt.
- Log toàn bộ payload request/response (bao gồm cả trường nhạy cảm) để "phòng khi cần debug", vô tình lưu trữ dữ liệu nhạy cảm không mã hóa trong hệ thống log.
- Đặt log level `DEBUG` làm mặc định ở production rồi quên đổi lại, khiến chi phí lưu trữ log tăng đột biến mà không ai để ý cho đến khi nhận hóa đơn.
- Log lỗi nhưng không kèm stack trace hoặc không kèm correlation ID của request gây ra lỗi, khiến log tồn tại nhưng vô dụng khi cần điều tra.

## Interview Questions

**Hỏi**: Structured logging khác gì so với logging văn bản thông thường, và tại sao nó quan trọng ở production?

**Trả lời**: Structured logging ghi log dưới dạng dữ liệu có schema (thường JSON key-value) thay vì câu văn tự do, cho phép hệ thống log tập trung parse và index từng field để query chính xác (vd. `status:500 AND service:payment`) thay vì phải regex trên text. Ở production với khối lượng log lớn từ nhiều service, đây là điều kiện bắt buộc để incident response nhanh và để dashboard/alert tự động dựa trên field cụ thể.

**Hỏi**: Correlation ID được truyền qua các service như thế nào trong một kiến trúc microservice?

**Trả lời**: Correlation ID được sinh ra tại điểm đầu tiên request đi vào hệ thống (gateway hoặc service đầu tiên), sau đó gắn vào logging context của service đó và được forward qua HTTP header (vd. `X-Request-ID`, hoặc chuẩn W3C `traceparent`) khi service này gọi service tiếp theo. Mỗi service nhận request đọc header này để tái sử dụng làm correlation ID của chính nó, nhờ vậy mọi log từ mọi service liên quan đến cùng một request đều mang chung một ID để truy vấn nối lại thành một trace.

**Hỏi**: Vì sao log level threshold `DEBUG` không nên bật mặc định ở production?

**Trả lời**: Log level `DEBUG` sinh ra khối lượng log rất lớn (chi tiết từng bước xử lý nội bộ), làm tăng chi phí lưu trữ, băng thông ingest vào hệ thống log tập trung, và có thể ảnh hưởng hiệu năng do I/O logging tăng ở service có throughput cao. Threshold hợp lý ở production thường là `INFO` trở lên, và `DEBUG` chỉ bật tạm thời (qua cấu hình runtime) khi đang điều tra một sự cố cụ thể.

## Summary

Logging hiệu quả ở production dựa trên ba trụ cột: structured logging để log có thể query như dữ liệu thay vì đọc bằng mắt, log level để phân loại mức độ nghiêm trọng và định tuyến alert đúng chỗ, và correlation ID để nối log của cùng một request xuyên suốt nhiều service thành một trace hoàn chỉnh. Correlation ID hoạt động bằng cách sinh tại điểm vào và forward qua header ở mọi lời gọi liên service, được middleware tự động gắn vào context logging mà code nghiệp vụ không cần can thiệp thủ công. Đánh đổi chính là chi phí lưu trữ/serialize tăng theo mức độ chi tiết của log, và yêu cầu kỷ luật kỹ thuật để không làm đứt chuỗi correlation ID hoặc rò rỉ dữ liệu nhạy cảm vào log. Một hệ thống log thiếu cả ba yếu tố này biến incident response production thành việc mò mẫm thay vì truy vấn có định hướng.

## Knowledge Graph

- Distributed Tracing — mở rộng khái niệm correlation ID thành span/trace đầy đủ với thời gian và quan hệ cha-con giữa các lời gọi.
- Metrics — bổ sung cho logging bằng dữ liệu định lượng tổng hợp (rate, latency percentile) thay vì từng sự kiện rời rạc.
- Alerting — log level `ERROR`/`FATAL` thường là nguồn kích hoạt trực tiếp cho hệ thống alerting production.
- Circuit Breaker — log/metric từ log level cao thường là tín hiệu để circuit breaker quyết định mở mạch.
- Retry Storm — log không có correlation ID khiến việc phát hiện retry storm xuyên service khó khăn hơn nhiều.
- PII Redaction — quy tắc bắt buộc khi thiết kế structured logging để tránh log dữ liệu nhạy cảm.

## Five Things To Remember

- Log ở production phải là structured (JSON key-value), không phải câu văn tự do khó query.
- Log level phải phản ánh đúng mức độ nghiêm trọng: ERROR để alert người, DEBUG chỉ để điều tra chi tiết.
- Correlation ID phải được sinh một lần tại điểm vào và forward bắt buộc qua mọi service kế tiếp, kể cả qua message queue.
- Một service quên forward correlation ID sẽ làm đứt trace tại đúng điểm đó, không có cách nào tự động vá lại sau này.
- Không bao giờ log password, token, hay PII dạng thô — redact tại tầng logger, không dựa vào trí nhớ developer.
