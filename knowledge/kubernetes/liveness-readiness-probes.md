---
id: liveness-readiness-probes
title: "Liveness & Readiness Probes"
tags: ["kubernetes"]
---

# Liveness & Readiness Probes

> Status: Draft

## Problem

Một container trong Kubernetes có thể đang chạy (process còn sống, `kubectl get pods` báo `Running`) nhưng hoàn toàn không phục vụ được request — deadlock trong event loop Node.js, thread pool cạn kiệt trong Java, connection pool tới database bị treo, hoặc ứng dụng vẫn đang khởi động và chưa load xong cache. Nếu Kubernetes không có cách nào phân biệt "process còn sống" với "process sẵn sàng nhận traffic", nó sẽ tiếp tục route request vào một pod đã chết về mặt chức năng, hoặc không bao giờ tự phục hồi một pod bị treo vĩnh viễn cho tới khi con người can thiệp.

## Pain Points

- Pod bị deadlock nhưng process vẫn chạy — Kubernetes coi là healthy, tiếp tục gửi traffic vào, toàn bộ request tới pod đó timeout hoặc treo, kéo theo cascading failure ở các service gọi vào.
- Pod mới khởi động (cold start, đang warm-up cache, đang kết nối database) nhận traffic ngay lập tức vì không có readiness probe — client nhận lỗi 502/503 trong vài giây đầu của mọi lần deploy hoặc scale up.
- Cấu hình probe sai (timeout quá ngắn, endpoint kiểm tra cả dependency ngoài) khiến pod khỏe mạnh bị kubelet coi là chết và restart liên tục — restart loop làm giảm capacity thực tế của cluster đúng lúc tải cao.
- Không phân biệt liveness và readiness dẫn tới dùng chung một endpoint cho cả hai, gây restart pod chỉ vì một dependency downstream (database, service khác) tạm thời chậm — vấn đề không nằm ở pod nhưng pod lại bị "trừng phạt".

## Solution

Kubernetes cung cấp hai loại probe độc lập với ngữ nghĩa khác nhau: **Liveness probe** trả lời câu hỏi "process này có cần được restart không" — nếu fail, kubelet kill container và tạo lại theo `restartPolicy`. **Readiness probe** trả lời câu hỏi "pod này có nên nhận traffic ngay bây giờ không" — nếu fail, pod bị gỡ khỏi danh sách Endpoints của mọi Service trỏ tới nó, nhưng container không bị kill, không bị restart. Tách hai khái niệm này cho phép một pod tạm thời không sẵn sàng (đang warm-up, đang GC pause dài, downstream chậm) mà không bị restart oan, đồng thời vẫn phát hiện được tình trạng thực sự cần restart (deadlock, memory leak không giải phóng được).

## How It Works

Kubelet chạy độc lập trên từng node, định kỳ gọi probe theo `periodSeconds` cấu hình trong pod spec, với ba cơ chế kiểm tra: `httpGet` (gọi HTTP endpoint, coi 2xx-3xx là thành công), `tcpSocket` (thử mở TCP connection), và `exec` (chạy command trong container, exit code 0 là thành công). Mỗi probe có `initialDelaySeconds` (chờ bao lâu sau khi container start mới bắt đầu check), `timeoutSeconds` (probe phải trả lời trong bao lâu), `failureThreshold` (số lần fail liên tiếp mới coi là thất bại thật), và `successThreshold` (số lần success liên tiếp để coi là phục hồi, quan trọng với readiness khi pod đang recover).

Với liveness probe: khi số lần fail liên tiếp chạm `failureThreshold`, kubelet gửi SIGTERM cho container (chờ `terminationGracePeriodSeconds`, mặc định 30s, rồi SIGKILL nếu chưa thoát), sau đó container được tạo lại theo container runtime — đây chính là nguồn gốc của restart, thể hiện qua `RESTARTS` tăng lên trong `kubectl get pods` và event `Liveness probe failed`. Với readiness probe: khi fail, kubelet cập nhật trạng thái container thành not-ready, và Endpoint Controller (chạy trên control plane, theo dõi pod thông qua label selector của Service) gỡ IP:port của pod đó khỏi object `Endpoints`/`EndpointSlice` tương ứng — kube-proxy trên mọi node sau đó cập nhật lại iptables/IPVS rules để không route traffic vào pod này nữa. Quá trình gỡ khỏi Endpoints có độ trễ nhất định (propagation qua control plane, kube-proxy sync interval), đây là lý do cần `preStop` hook hoặc graceful shutdown để xử lý các request đã lỡ route vào trong lúc chuyển tiếp.

Ngoài `httpGet`/`tcpSocket`/`exec`, từ Kubernetes 1.23+ (stable ở 1.25) còn có `startupProbe` — chạy trước, che các probe khác cho tới khi thành công, giải quyết vấn đề ứng dụng có thời gian khởi động dài và không đồng đều (ví dụ Java app JIT warm-up) mà không phải nới `initialDelaySeconds` của liveness lên quá cao gây chậm phát hiện deadlock thật sự.

## Production Architecture

Trong một Deployment chạy API backend, cấu hình thường tách rõ ba tầng: `startupProbe` kiểm tra endpoint `/healthz` với `failureThreshold` cao (cho phép tối đa vài phút để app load config, kết nối message queue, warm cache) trước khi hai probe kia bắt đầu hoạt động; `readinessProbe` gọi `/readyz` — endpoint này kiểm tra các dependency thiết yếu thực sự (kết nối database, kết nối cache) và trả về not-ready nếu bất kỳ dependency nào chưa sẵn sàng; `livenessProbe` gọi `/healthz` đơn giản — chỉ xác nhận event loop/thread chính còn phản hồi được, tuyệt đối không kiểm tra dependency ngoài. Trong kiến trúc có service mesh (Istio, Linkerd), sidecar proxy cũng có readiness riêng và Kubernetes chờ cả app container lẫn sidecar container đều ready trước khi pod được coi là ready toàn bộ (`minReadySeconds` trên Deployment còn cộng thêm một khoảng đệm trước khi pod mới được tính vào rolling update tiếp theo). Trong rolling update, readiness probe là cơ chế cốt lõi để Kubernetes biết khi nào pod mới đã sẵn sàng nhận traffic để tiếp tục scale down pod cũ — nếu readiness probe luôn trả về true ngay khi container start (health check giả), rolling update sẽ đưa traffic vào pod chưa thực sự sẵn sàng, gây một loạt lỗi ngắn ngay sau mỗi lần deploy.

## Trade-offs

- Readiness probe kiểm tra dependency (database, cache) giúp phát hiện sớm sự cố, nhưng nếu dependency đó down toàn cluster, mọi pod đều not-ready cùng lúc — toàn bộ service biến mất khỏi Endpoints thay vì trả lỗi có kiểm soát, đôi khi tệ hơn so với việc vẫn nhận traffic và trả lỗi 503 tường minh.
- Liveness probe càng nghiêm ngặt (timeout ngắn, failureThreshold thấp) càng phát hiện nhanh tình trạng treo thật, nhưng cũng dễ false positive khi có GC pause dài hoặc CPU throttling tạm thời, dẫn tới restart loop dù ứng dụng không thực sự lỗi.
- `exec` probe chính xác hơn `tcpSocket` (kiểm tra được logic thật thay vì chỉ port mở) nhưng tốn tài nguyên hơn (fork process trong container) và có thể tự nó bị treo nếu implementation không cẩn thận, biến probe thành nguồn gây fail thay vì công cụ phát hiện fail.
- Dùng chung một endpoint cho cả liveness và readiness đơn giản hóa cấu hình nhưng đánh mất toàn bộ lợi ích của việc tách hai khái niệm — một dependency downstream chậm sẽ khiến pod bị restart thay vì chỉ tạm thời gỡ khỏi traffic.

## Best Practices

- Tách riêng endpoint cho liveness (`/healthz`, chỉ kiểm tra process còn phản hồi) và readiness (`/readyz`, kiểm tra dependency thiết yếu) — không bao giờ dùng chung một endpoint.
- Liveness probe không được gọi ra dependency bên ngoài (database, service khác) — chỉ nên restart container khi vấn đề nằm trong chính process đó, dependency down không phải lý do để restart.
- Dùng `startupProbe` cho ứng dụng có thời gian khởi động dài/không ổn định thay vì nới `initialDelaySeconds` của liveness probe lên quá cao, tránh làm chậm phát hiện deadlock thật sự sau khi app đã chạy ổn định.
- Đặt `failureThreshold` và `periodSeconds` đủ rộng để chịu được GC pause hoặc spike tải ngắn hạn — quy tắc kinh nghiệm là tổng thời gian chịu lỗi (`failureThreshold * periodSeconds`) phải lớn hơn p99 latency của thao tác chậm nhất mà probe có thể vô tình bắt phải.
- Kết hợp `preStop` hook (sleep vài giây trước khi gửi SIGTERM) với readiness probe để che khoảng trễ giữa lúc pod bắt đầu terminate và lúc nó thực sự biến mất khỏi Endpoints, tránh mất request trong lúc rolling update hoặc scale down.

## Common Mistakes

- Cho readiness probe kiểm tra toàn bộ dependency downstream (kể cả dependency không thiết yếu) khiến một service phụ bị chậm kéo theo toàn bộ pod bị gỡ khỏi traffic dù chức năng chính vẫn hoạt động bình thường.
- Đặt `timeoutSeconds` quá ngắn (1-2 giây) cho service có p99 latency cao hơn, khiến probe tự fail do chính nó chậm chứ không phải ứng dụng thực sự lỗi, gây restart loop dưới tải cao — đúng lúc cần pod nhất.
- Không cấu hình `initialDelaySeconds`/`startupProbe` phù hợp cho ứng dụng khởi động chậm, khiến liveness probe fail và restart container ngay trong lúc nó đang khởi động bình thường, tạo vòng lặp không bao giờ start xong được.
- Health check endpoint tự viết trả về 200 cứng (hardcoded) mà không thực sự kiểm tra gì, khiến probe hoàn toàn vô nghĩa — pod được coi là ready dù chưa kết nối được database.
- Thiếu `preStop` hook khi có readiness probe, khiến trong lúc pod terminate vẫn có một khoảng thời gian ngắn nhận request đã bị route vào trước khi Endpoints kịp cập nhật, gây một tỷ lệ lỗi nhỏ nhưng đều đặn ở mỗi lần deploy.

## Interview Questions

**Hỏi**: Sự khác biệt cốt lõi giữa liveness probe fail và readiness probe fail là gì?

**Trả lời**: Liveness probe fail khiến kubelet kill và restart container (tăng `RESTARTS` count). Readiness probe fail chỉ gỡ pod khỏi Endpoints của Service, ngừng nhận traffic mới, nhưng container tiếp tục chạy không bị đụng tới — cho phép nó tự phục hồi rồi được đưa lại vào traffic mà không cần restart.

**Hỏi**: Tại sao cấu hình liveness probe kiểm tra kết nối database lại là một anti-pattern?

**Trả lời**: Vì khi database down, mọi pod của service đều fail liveness probe cùng lúc và bị restart hàng loạt, dù vấn đề không nằm ở chính các pod đó — restart không giải quyết được gì (database vẫn down) mà còn gây thêm cold-start storm và có thể làm nặng thêm tải lên database khi tất cả pod cùng cố kết nối lại. Đây là việc mà readiness probe nên xử lý, không phải liveness.

**Hỏi**: `startupProbe` giải quyết vấn đề gì mà `initialDelaySeconds` của liveness probe không giải quyết được tốt?

**Trả lời**: `initialDelaySeconds` là một giá trị cố định áp dụng cho mọi lần start, nên phải đặt đủ lớn cho trường hợp khởi động chậm nhất — làm chậm việc phát hiện deadlock thật ở những lần start bình thường và nhanh. `startupProbe` cho phép chờ tới khi ứng dụng thực sự sẵn sàng (không phải một khoảng thời gian cố định) rồi mới kích hoạt liveness/readiness probe với cấu hình timeout chặt hơn, phù hợp cho giai đoạn steady-state.

## Summary

Liveness probe và readiness probe giải quyết hai vấn đề khác nhau: liveness quyết định khi nào một container cần bị kill và restart, readiness quyết định khi nào một pod nên nhận traffic. Kubelet thực hiện probe định kỳ qua `httpGet`/`tcpSocket`/`exec`, và kết quả readiness ảnh hưởng trực tiếp tới object Endpoints mà kube-proxy dùng để route traffic. Cấu hình sai — đặc biệt là gộp chung hai loại probe, hoặc để liveness probe phụ thuộc vào dependency ngoài — là nguyên nhân phổ biến của restart loop, nơi pod khỏe mạnh bị restart liên tục do lỗi cấu hình chứ không phải lỗi ứng dụng thật. `startupProbe` bổ sung một tầng bảo vệ cho ứng dụng khởi động chậm, tách biệt rõ giai đoạn "đang khởi động" khỏi giai đoạn "đang chạy ổn định".

## Knowledge Graph

- Rolling Update — dựa vào readiness probe để biết khi nào pod mới đủ điều kiện tiếp tục thay thế pod cũ.
- Endpoints/EndpointSlice — object mà readiness probe tác động trực tiếp, được kube-proxy dùng để route traffic.
- Graceful Shutdown & preStop Hook — cơ chế bổ sung để tránh mất request trong khoảng trễ giữa lúc terminate và lúc gỡ khỏi Endpoints.
- Service Mesh Sidecar Readiness — trường hợp readiness của pod phụ thuộc vào cả app container lẫn sidecar container.
- Circuit Breaker — khái niệm tương tự ở tầng ứng dụng, ngăn không gọi tiếp dependency đang lỗi, bổ trợ cho readiness probe.
- Deadlock — loại lỗi mà liveness probe được thiết kế để phát hiện và khắc phục bằng restart.

## Five Things To Remember

- Liveness fail thì restart container; readiness fail thì chỉ gỡ khỏi traffic, không restart.
- Liveness probe không bao giờ được kiểm tra dependency bên ngoài như database.
- Restart loop thường là hậu quả của cấu hình probe sai, không phải lỗi ứng dụng thật.
- `startupProbe` tách riêng giai đoạn khởi động chậm khỏi giai đoạn giám sát steady-state.
- Luôn dùng hai endpoint khác nhau cho liveness và readiness, không bao giờ dùng chung.
