---
id: graceful-shutdown
title: Graceful Shutdown
tags: ["backend", "reliability"]
---

# Graceful Shutdown

> Status: Draft

## Problem

Khi một orchestrator (Kubernetes, ECS, systemd) cần dừng hoặc thay thế một instance — deploy phiên bản mới, scale down, node bị drain, autoscaler co cụm — nó gửi tín hiệu `SIGTERM` tới process rồi chờ một khoảng thời gian ngắn (`terminationGracePeriodSeconds`, mặc định 30s trên Kubernetes) trước khi gửi `SIGKILL` buộc chết ngay lập tức. Nếu ứng dụng không xử lý `SIGTERM`, hành vi mặc định của hầu hết runtime là thoát ngay lập tức, cắt đứt mọi request đang xử lý dở, mọi transaction DB đang mở, mọi message đang được ack — quá trình dừng lại giữa chừng chứ không kết thúc gọn gàng. Vấn đề không nằm ở việc process có dừng hay không (nó luôn dừng), mà ở việc dừng đột ngột giữa một request đang chạy khác hoàn toàn với dừng sau khi đã xử lý xong công việc dang dở.

## Pain Points

- Request đang xử lý bị cắt ngang giữa chừng, client nhận connection reset hoặc timeout thay vì response hợp lệ — trong lúc rolling deploy diễn ra liên tục (nhiều lần/ngày), đây là nguồn lỗi 5xx âm thầm mà dashboard latency/error rate không giải thích được nếu không nhìn đúng thời điểm.
- Transaction DB đang mở bị bỏ dở do connection bị đóng đột ngột, DB phải rollback hoặc để transaction treo cho tới khi hết idle timeout, chiếm connection slot vô ích và có thể gây lock chờ trên các transaction khác.
- Message đã được consumer lấy ra khỏi queue (Kafka, SQS, RabbitMQ) nhưng chưa kịp ack bị mất hoặc bị xử lý lặp lại khi consumer khác nhận lại — dẫn tới duplicate side-effect (gửi email hai lần, trừ tiền hai lần) nếu xử lý không idempotent.
- Load balancer/service mesh chưa kịp cập nhật danh sách endpoint đã loại bỏ instance đang bị kill, tiếp tục route traffic mới vào một process đang trong quá trình chết — tỷ lệ lỗi tăng đúng vào cửa sổ vài trăm mili-giây tới vài giây quanh mỗi lần deploy hoặc scale down, nhân lên theo tần suất deploy trở thành một nguồn lỗi nền liên tục.

## Solution

Graceful shutdown là quy trình xử lý tín hiệu dừng (`SIGTERM`) theo ba bước tuần tự: (1) ngừng nhận request/message mới ngay khi nhận tín hiệu, (2) chờ toàn bộ request/message đang xử lý dở hoàn tất trong một khoảng thời gian giới hạn, (3) đóng có trật tự các tài nguyên đang mở (connection pool DB, Redis client, message consumer, file handle) trước khi thoát process. Mục tiêu là để mỗi lần dừng instance trông giống một lần "nghỉ hưu có kế hoạch" của process, không phải một tai nạn giữa đường — không request nào bị cắt ngang, không tài nguyên nào bị bỏ dở ở trạng thái không nhất quán.

## How It Works

Ứng dụng đăng ký signal handler cho `SIGTERM` (và thường cả `SIGINT` để test local bằng Ctrl+C) ngay khi khởi động, trước khi bắt đầu nhận traffic. Khi handler được gọi, quy trình diễn ra theo thứ tự:

1. **Đánh dấu instance "not ready"**: cập nhật health check endpoint (`/readyz` hoặc tương đương) trả về failure ngay lập tức, để load balancer/service mesh/Kubernetes Service ngừng route request mới vào instance này. Đây là bước dễ bị bỏ sót nhất — nếu chỉ đóng server socket mà không báo readiness probe, có một cửa sổ thời gian (bằng chu kỳ polling readiness probe, thường 1-10s) load balancer vẫn gửi traffic vào instance đang chết.
2. **Ngừng nhận connection/request mới nhưng không đóng ngay**: gọi API dừng nhận kết nối mới của HTTP server trong khi vẫn giữ các connection đang xử lý hoạt động bình thường (Go: `http.Server.Shutdown(ctx)`; Node.js: `server.close()` kết hợp theo dõi connection đang mở; Java/Spring: `GracefulShutdown` qua `WebServerFactoryCustomizer`). Các request đã bắt đầu được phép chạy tiếp tới khi hoàn tất.
3. **Chờ request đang chạy hoàn tất (drain)**: process theo dõi số lượng request in-flight (qua counter hoặc callback của HTTP server) và chờ tới khi về 0, hoặc tới khi hết grace period. Với message consumer, bước tương ứng là ngừng poll message mới nhưng vẫn hoàn tất commit/ack cho message đang xử lý dở trước khi rời consumer group.
4. **Đóng tài nguyên theo đúng thứ tự phụ thuộc**: đóng connection pool DB, Redis client, gRPC client tới service khác — theo thứ tự ngược với lúc khởi tạo, đảm bảo không có tài nguyên nào bị đóng trong khi vẫn còn code path khác đang dùng nó. Với connection pool, gọi API đóng có chờ (`pool.close()` chờ connection trả về hết) thay vì hủy ngay các connection đang active.
5. **Thoát process**: nếu tất cả hoàn tất trước grace period, process thoát với exit code 0. Nếu vượt grace period, orchestrator gửi `SIGKILL`, buộc dừng ngay bất kể trạng thái — vì vậy grace period cần được đặt đủ lớn hơn thời gian xử lý request chậm nhất trong hệ thống (p99 hoặc p999 latency), không phải trung bình.

Một chi tiết hay bị hiểu sai: `SIGTERM` không đảm bảo có nghĩa "dừng ngay", nó có nghĩa "bắt đầu dừng, có thời hạn". Việc có bao nhiêu thời gian để dừng gọn gàng hoàn toàn phụ thuộc vào `terminationGracePeriodSeconds` (K8s) hoặc tham số tương đương của orchestrator, và giá trị này phải khớp với worst-case thời gian cần để drain hết request đang chạy.

## Production Architecture

Trong một service HTTP chạy trên Kubernetes, `preStop` hook thường được cấu hình để `sleep 5-10s` trước khi gửi `SIGTERM` thật — khoảng đệm này bù cho độ trễ lan truyền của việc gỡ endpoint khỏi `iptables`/`ipvs` trên các node khác, vì Kubernetes cập nhật Endpoints object và propagate xuống kube-proxy không đồng bộ tuyệt đối với việc container nhận SIGTERM. Với worker xử lý message (Kafka consumer, SQS poller), graceful shutdown nghĩa là: dừng poll message mới, hoàn tất commit offset (Kafka) hoặc `DeleteMessage`/để visibility timeout hết hạn (SQS) cho message đang xử lý, rồi mới rời consumer group — rời group sớm khi message chưa ack sẽ trigger rebalance ngay lập tức và một consumer khác lấy lại đúng message đó, gây xử lý trùng nếu logic không idempotent. Với connection pool DB (PgBouncer, HikariCP, node-postgres pool), thứ tự đóng đúng là: ngừng nhận request mới trước, để pool tự nhiên rảnh dần khi các query đang chạy hoàn tất, rồi mới gọi `pool.end()`/`pool.close()` — gọi đóng pool ngay khi nhận SIGTERM trong khi vẫn còn request đang query sẽ làm các request đó nhận lỗi "pool đã đóng" giữa chừng. Trong kiến trúc nhiều container trong cùng pod (sidecar pattern, ví dụ Istio sidecar proxy), thứ tự shutdown giữa container chính và sidecar cũng cần cân nhắc — nếu sidecar proxy tắt trước container chính, container chính có thể mất khả năng gọi network ra ngoài trong lúc vẫn cần hoàn tất request, đây là lý do Kubernetes 1.28+ hỗ trợ `sidecar containers` với thứ tự shutdown được đảm bảo tường minh.

## Trade-offs

Graceful shutdown đánh đổi thời gian deploy lấy tính đúng đắn: mỗi lần dừng instance mất thêm vài giây tới vài chục giây chờ drain thay vì kill ngay, nhân lên với tần suất deploy/scale trong ngày sẽ cộng dồn thành thời gian rollout tổng thể dài hơn đáng kể, đặc biệt với rolling update có nhiều batch. Grace period đặt quá dài (để chắc chắn đủ thời gian drain) làm chậm toàn bộ quy trình deploy và autoscaling phản ứng chậm khi cần scale down gấp; đặt quá ngắn thì quay lại đúng vấn đề ban đầu — request bị cắt ngang khi vượt quá grace period và bị `SIGKILL`. Với request chạy rất lâu (streaming response, long-polling, WebSocket, batch job), graceful shutdown thuần túy "chờ hết" là không khả thi vì có thể không bao giờ về 0 trong grace period hợp lý — những trường hợp này cần thêm cơ chế riêng (đóng gói lại kết nối, chuyển tiếp sang instance khác, hoặc chấp nhận cắt có kiểm soát với thông báo rõ cho client) chứ graceful shutdown không tự giải quyết được.

## Best Practices

- Tách rõ hai bước: đánh dấu "not ready" (health check fail) trước, đóng server socket sau — không dồn hai việc thành một, vì có độ trễ propagate readiness cần được tôn trọng.
- Đặt `terminationGracePeriodSeconds` (hoặc tương đương) lớn hơn p99 thời gian xử lý request chậm nhất trong hệ thống, không dựa vào latency trung bình.
- Với message consumer, luôn ưu tiên hoàn tất commit/ack message đang xử lý trước khi rời consumer group, và thiết kế xử lý idempotent để chịu được trường hợp message bị xử lý lặp do rebalance.
- Đóng tài nguyên theo đúng thứ tự phụ thuộc ngược với lúc khởi tạo (network client trước, connection pool DB/cache sau cùng), không đóng song song không kiểm soát.
- Log rõ từng bước của quy trình shutdown (nhận SIGTERM, số request in-flight, thời điểm drain xong, thời điểm đóng từng tài nguyên) để có thể debug khi một lần deploy gây tăng lỗi bất thường.

## Common Mistakes

- Không đăng ký signal handler cho `SIGTERM`, để runtime mặc định kill process ngay lập tức, cắt ngang mọi request đang chạy dở.
- Đóng connection pool DB/Redis ngay khi nhận SIGTERM mà không chờ request đang chạy hoàn tất trước, khiến các request đó nhận lỗi "connection closed" giữa chừng thay vì hoàn tất bình thường.
- Chỉ đóng server socket mà quên cập nhật readiness probe, tạo ra cửa sổ thời gian load balancer vẫn gửi traffic mới vào instance đang trong quá trình dừng.
- Đặt grace period quá ngắn so với thời gian xử lý thực tế của request chậm nhất, khiến `SIGKILL` xảy ra trước khi drain xong — về bản chất vô hiệu hóa toàn bộ nỗ lực graceful shutdown.
- Với worker message queue, rời consumer group hoặc đóng connection trước khi ack xong message đang xử lý, gây mất message hoặc xử lý trùng khi logic không idempotent.

## Interview Questions

**Hỏi**: Vì sao chỉ đóng HTTP server (ngừng nhận connection mới) là chưa đủ để graceful shutdown đúng cách trên Kubernetes?

**Trả lời**: Vì việc gỡ endpoint của instance khỏi danh sách route của Service/kube-proxy không đồng bộ tức thời với việc container nhận SIGTERM — có độ trễ propagate. Nếu chỉ đóng server mà không đánh dấu readiness probe fail trước và không có `preStop` hook đệm thời gian, load balancer vẫn có thể gửi request mới vào instance trong lúc nó đang đóng, gây lỗi connection refused hoặc reset ở phía client.

**Hỏi**: Grace period nên được tính dựa trên số liệu nào, và vì sao?

**Trả lời**: Dựa trên p99 (hoặc p999) latency của request chậm nhất trong hệ thống, không phải latency trung bình. Nếu dựa vào trung bình, các request nằm ở đuôi phân phối (tail latency) — vốn luôn tồn tại trong hệ thống production — sẽ liên tục bị cắt ngang bởi SIGKILL mỗi lần deploy, dù phần lớn request khác hoàn tất kịp.

**Hỏi**: Với một consumer đang xử lý message từ Kafka, thứ tự đúng khi nhận SIGTERM là gì?

**Trả lời**: Ngừng poll message mới trước, hoàn tất xử lý và commit offset cho message đang xử lý dở, sau đó mới rời consumer group và đóng connection. Rời group trước khi commit xong sẽ trigger rebalance ngay, khiến một consumer khác nhận lại đúng message chưa được ack, dẫn tới xử lý trùng nếu side-effect không idempotent.

## Summary

Graceful shutdown là quy trình xử lý `SIGTERM` theo ba bước: ngừng nhận việc mới, chờ việc đang chạy hoàn tất trong một grace period giới hạn, rồi đóng tài nguyên theo đúng thứ tự trước khi thoát process. Nếu bỏ qua, mỗi lần deploy/scale down trở thành một nguồn lỗi 5xx, transaction dở, và message xử lý trùng lặp âm thầm tích lũy theo tần suất deploy. Cơ chế này đòi hỏi phối hợp giữa ứng dụng (signal handler, drain logic, đóng resource theo thứ tự) và orchestrator (readiness probe, `preStop` hook, `terminationGracePeriodSeconds`) — thiếu một phía thì phía còn lại không đủ để đảm bảo dừng sạch. Grace period cần dựa trên tail latency thực tế của hệ thống, không phải giá trị mặc định hay trung bình. Với các loại kết nối dài (streaming, WebSocket), graceful shutdown thuần túy không đủ và cần thiết kế bổ sung riêng.

## Knowledge Graph

- Circuit Breaker — cùng nhóm cơ chế resilience nhưng xử lý hướng ngược lại: circuit breaker bảo vệ caller khỏi downstream lỗi, graceful shutdown bảo vệ request đang chạy khỏi chính instance đang dừng.
- Readiness Probe / Liveness Probe — cơ chế Kubernetes dùng để quyết định có route traffic vào instance hay không, là bước bắt buộc đi kèm graceful shutdown.
- Idempotency — yêu cầu bắt buộc để xử lý an toàn trường hợp message bị xử lý lặp do rebalance xảy ra giữa lúc shutdown.
- Connection Pool — tài nguyên cần được đóng theo đúng thứ tự và đúng thời điểm (sau khi drain) trong quy trình shutdown.
- Rolling Deployment — quy trình deploy gọi graceful shutdown liên tục trên từng instance, khiến lỗi thiếu graceful shutdown lộ ra ở tần suất cao.
- Message Delivery Guarantees — xác định hành vi ack/commit đúng cho consumer khi dừng, liên quan trực tiếp tới bước drain của graceful shutdown cho worker queue.

## Five Things To Remember

- SIGTERM nghĩa là "bắt đầu dừng có thời hạn", không phải "dừng ngay lập tức".
- Đánh dấu not-ready (health check fail) trước, đóng server socket sau — không dồn làm một.
- Grace period phải lớn hơn p99 latency của request chậm nhất, không phải latency trung bình.
- Đóng tài nguyên theo thứ tự ngược lúc khởi tạo, chỉ sau khi request đang chạy đã hoàn tất.
- Consumer message queue phải ack/commit xong việc đang xử lý trước khi rời group, nếu không sẽ mất hoặc xử lý trùng message.
