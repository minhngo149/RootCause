---
id: resource-requests-limits
title: "Resource Requests & Limits"
tags: ["kubernetes"]
---

# Resource Requests & Limits

> Status: Draft

## Problem

Khi một Pod không khai báo `resources.requests`/`resources.limits`, kube-scheduler không có căn cứ nào để biết Pod đó cần bao nhiêu CPU/memory, và node cũng không có cơ chế nào để giới hạn nó tiêu thụ tài nguyên tới đâu. Hệ quả là scheduler có thể nhồi quá nhiều Pod lên cùng một node (vì "trông có vẻ còn chỗ"), rồi khi traffic tăng, các Pod tranh giành CPU/memory lẫn nhau ngay trên cùng node vật lý mà không hề có lỗi rõ ràng nào được log ra — chỉ là latency tăng dần hoặc container bị kill đột ngột. Vấn đề không nằm ở việc "có set số hay không", mà ở việc engineer không hiểu request quyết định scheduling còn limit quyết định throttling/OOMKill, và đặt sai hai giá trị này theo hai hướng ngược nhau đều gây hại.

## Pain Points

- Noisy neighbor: một Pod không có limit CPU chiếm hết CPU khả dụng trên node, khiến các Pod khác cùng node bị CPU throttling dù request của chúng đã được "đảm bảo" trên giấy tờ.
- OOMKill hàng loạt: limit memory đặt quá sát mức sử dụng thực tế, một đợt tăng traffic nhỏ hoặc một GC pause khiến container vượt limit và bị kernel OOM killer kill ngay lập tức (exit code 137), không kịp graceful shutdown.
- Lãng phí tài nguyên: request đặt quá cao "cho chắc" khiến node báo đầy chỗ (theo góc nhìn scheduler) trong khi CPU/memory thực tế sử dụng chỉ 10-20%, dẫn tới cluster phải scale thêm node không cần thiết, tăng chi phí cloud đáng kể.
- Pod bị đánh giá `BestEffort` hoặc `Burstable` không mong muốn (do thiếu request/limit hoặc request khác limit) khiến nó nằm trong nhóm bị evict đầu tiên khi node thiếu tài nguyên, kể cả khi đó là service quan trọng.
- CPU throttling âm thầm: Pod không bị kill nhưng bị CFS quota giới hạn số chu kỳ CPU, biểu hiện ra ngoài là latency tăng đột biến theo chu kỳ (mỗi 100ms) mà không có exception hay log lỗi nào để dò.

## Solution

`requests` là con số Kubernetes dùng để **đặt chỗ** — scheduler cộng tổng request của mọi Pod trên một node để quyết định Pod mới có được xếp vào node đó hay không, và kubelet dùng nó để tính `cgroup` share tương đối khi tài nguyên bị tranh chấp. `limits` là con số Kubernetes dùng để **chặn trần** — kubelet ép container không được vượt quá giá trị này, thực thi bằng CFS quota (với CPU, dẫn tới throttling) hoặc bằng cgroup memory hard limit (với memory, dẫn tới OOMKill). Hai con số phục vụ hai mục đích hoàn toàn khác nhau: request ảnh hưởng tới việc Pod có được lên lịch hay không (bind-time decision), còn limit ảnh hưởng tới việc Pod chạy như thế nào sau khi đã lên lịch (runtime enforcement).

## How It Works

**Scheduling dựa trên request**: kube-scheduler chạy predicate `PodFitsResources`, so tổng `requests.cpu`/`requests.memory` của các Pod đang chạy trên một node với `allocatable` của node đó (capacity trừ đi phần dành cho system daemon và kubelet qua `--system-reserved`/`--kube-reserved`). Nếu tổng request cộng thêm Pod mới vượt allocatable, node đó bị loại khỏi danh sách ứng viên — hoàn toàn không quan tâm tới usage thực tế. Điều này giải thích vì sao một node có thể báo "hết chỗ" (không schedule thêm Pod được) trong khi `kubectl top node` cho thấy CPU chỉ dùng 15%: request đã bị đặt cao hơn nhiều so với usage thực.

**CPU limit và CFS quota**: khi container có `limits.cpu`, kubelet chuyển giá trị này thành `cpu.cfs_quota_us` và `cpu.cfs_period_us` trong cgroup (mặc định period 100ms, quota = limit × period). Linux CFS scheduler theo dõi số microsecond CPU container đã dùng trong mỗi period; nếu chạm quota trước khi period kết thúc, container bị **throttle** — tất cả thread trong container bị treo (không được cấp thêm CPU time) cho tới period tiếp theo. Đây là lý do một service multi-threaded với limit "500m" vẫn có thể bị throttle nặng dù usage trung bình thấp: nếu nó burst dùng hết 50ms CPU trong 10ms đầu của period (do nhiều thread chạy song song), nó bị đóng băng 90ms còn lại dù usage trung bình cả period chỉ 50%.

**Memory limit và OOMKill**: memory limit được set trực tiếp thành `memory.limit_in_bytes` (cgroup v1) hoặc `memory.max` (cgroup v2). Khác với CPU, memory không "throttle" được — nếu container cố cấp phát vượt quá limit, kernel OOM killer can thiệp ngay lập tức, chọn process có `oom_score_adj` cao nhất trong cgroup đó (thường chính là process chính của container) để kill bằng SIGKILL. Container bị kill với exit code 137, không có cơ hội chạy cleanup handler, không flush log buffer — đây là lý do OOMKill luôn khó debug hơn crash thông thường.

**QoS Class quyết định thứ tự evict**: Kubernetes gán mỗi Pod vào một trong ba class dựa trên cách khai báo request/limit. `Guaranteed`: mọi container đều có request = limit cho cả CPU lẫn memory. `Burstable`: có ít nhất một request được khai báo nhưng không bằng limit (hoặc thiếu limit). `BestEffort`: không khai báo request/limit nào cả. Khi node gặp áp lực tài nguyên (memory pressure), kubelet evict Pod theo thứ tự `BestEffort` trước, `Burstable` (ưu tiên Pod dùng vượt request nhiều nhất) tiếp theo, `Guaranteed` bị evict cuối cùng và chỉ khi hệ thống thực sự cạn kiệt.

**Request khác limit tạo ra "overcommit"**: khi `limits` tổng trên node lớn hơn `allocatable` (Burstable Pod được phép limit cao hơn request), node đang overcommit — hoạt động bình thường khi không phải Pod nào cũng dùng tới limit cùng lúc, nhưng khi nhiều Pod đồng loạt burst, tổng usage thực tế có thể vượt tài nguyên vật lý của node, dẫn tới CPU throttling lan rộng hoặc memory pressure toàn node dù từng Pod riêng lẻ vẫn "trong limit" của chính nó.

## Production Architecture

Trong một cluster chạy microservices, các service chịu tải chính (API gateway, service xử lý thanh toán) thường được cấu hình `Guaranteed` QoS (request = limit) để tránh bị throttle bất ngờ và được ưu tiên giữ lại khi node thiếu tài nguyên, đổi lại chấp nhận hiệu suất sử dụng tài nguyên thấp hơn (phải reserve đúng bằng peak load). Các batch job hoặc worker xử lý nền (report generation, image resize) thường dùng `Burstable` với request thấp, limit cao hơn nhiều lần — tận dụng CPU/memory rảnh rỗi trên node khi các service chính không dùng hết, chấp nhận rủi ro bị throttle hoặc evict khi cạnh tranh tài nguyên. Ở tầng cluster autoscaling, Cluster Autoscaler và Vertical Pod Autoscaler đều đọc `requests` (không phải usage thực tế) để quyết định có cần thêm node hay điều chỉnh request — nếu request bị đặt sai từ đầu (quá cao do "đặt cho chắc", quá thấp do chưa load test), toàn bộ chuỗi autoscaling phía sau đều sai theo, dẫn tới cluster có nhiều node nhàn rỗi (over-request) hoặc liên tục OOMKill/throttle (under-request). Nhiều tổ chức dùng VPA ở chế độ "recommendation only" kết hợp với metrics lịch sử từ Prometheus để định kỳ điều chỉnh request/limit theo usage p95/p99 thực tế thay vì đoán một lần rồi giữ nguyên mãi mãi.

## Trade-offs

- Đặt request = limit (`Guaranteed`) giảm rủi ro bị throttle/evict bất ngờ nhưng buộc phải reserve tài nguyên cho peak load, khiến utilization trung bình của cluster thấp và chi phí cao hơn.
- Đặt request thấp hơn limit nhiều (`Burstable` rộng) tận dụng tài nguyên nhàn rỗi tốt hơn, nhưng khi nhiều Pod cùng burst đồng thời, node dễ rơi vào tranh chấp tài nguyên (CPU throttling lan rộng, memory pressure) mà từng Pod riêng lẻ không hề "sai" gì.
- Không đặt limit CPU giúp Pod không bao giờ bị throttle khi cần burst ngắn, nhưng đánh đổi bằng rủi ro nó chiếm hết CPU node, gây noisy neighbor cho các Pod khác — nhiều tổ chức cố tình không set CPU limit vì lý do này nhưng vẫn giữ CPU request để đảm bảo scheduling công bằng.
- Memory limit gần như bắt buộc phải đặt (khác CPU), vì không có limit, một Pod leak memory có thể chiếm hết memory node và kéo theo kernel OOM killer chọn nhầm process khác để kill — nhưng đặt limit quá sát usage thực tế lại biến một đợt tăng traffic bình thường thành sự cố OOMKill.
- VPA tự động điều chỉnh request/limit giảm việc đoán sai thủ công, nhưng việc restart Pod để áp dụng giá trị mới (VPA không resize in-place ở hầu hết version) gây gián đoạn ngắn, không phù hợp với service cần zero-downtime tuyệt đối.

## Best Practices

- Luôn đặt memory `limit`, kể cả khi không chắc chắn 100% — không có nó, một memory leak trong một Pod có thể kéo sập cả node qua kernel OOM killer chọn nhầm nạn nhân.
- Đặt `requests` dựa trên dữ liệu usage thực tế (p95/p99 từ Prometheus/metrics-server qua vài tuần), không phải con số đoán một lần khi viết YAML lần đầu.
- Với service chịu tải chính, latency-sensitive, ưu tiên `Guaranteed` QoS (request = limit) để tránh CPU throttling và được bảo vệ khỏi eviction trước tiên.
- Không đặt CPU limit quá sát request cho service có traffic dao động mạnh — cho phép biên độ Burstable hợp lý để tránh throttling gây latency spike theo chu kỳ.
- Theo dõi định kỳ `container_cpu_cfs_throttled_periods_total` và số lần OOMKill (`kube_pod_container_status_last_terminated_reason="OOMKilled"`) qua Prometheus để phát hiện request/limit đặt sai trước khi nó gây incident.

## Common Mistakes

- Không đặt request/limit gì cả, để Pod rơi vào `BestEffort` — Pod bị evict đầu tiên khi node thiếu tài nguyên, kể cả khi đó là service quan trọng.
- Đặt request cao hơn nhiều so với usage thực tế "cho an toàn", khiến scheduler từ chối xếp thêm Pod vào node dù usage thực tế còn rất nhiều dư địa, gây lãng phí và ép cluster autoscaler thêm node không cần thiết.
- Đặt memory limit bằng đúng usage trung bình quan sát được, không chừa margin cho spike hoặc GC pause, dẫn tới OOMKill định kỳ mỗi khi traffic tăng nhẹ.
- Copy-paste giá trị request/limit từ service khác mà không đo lại — mỗi service có pattern CPU/memory khác nhau (I/O-bound vs CPU-bound, có GC hay không), giá trị phù hợp cho service này có thể sai hoàn toàn cho service khác.
- Nhầm lẫn "usage thấp" với "request đặt đúng" — Pod dùng 10% CPU vẫn có thể bị throttle nặng nếu nó burst hết quota trong vài millisecond đầu mỗi period, điều mà biểu đồ usage trung bình theo phút không bao giờ thể hiện ra.

## Interview Questions

**Hỏi**: Sự khác biệt cốt lõi giữa `requests` và `limits` là gì, và tại sao gộp chung hai khái niệm này lại dễ gây hiểu lầm?

**Trả lời**: `requests` là con số dùng ở thời điểm scheduling — scheduler dùng nó để quyết định Pod được xếp vào node nào, hoàn toàn không liên quan tới việc container chạy ra sao sau đó. `limits` là con số dùng ở runtime — kubelet ép container không vượt quá nó bằng CFS quota (CPU) hoặc cgroup memory limit (memory). Nhầm lẫn hai khái niệm này khiến engineer nghĩ đặt limit cao là "an toàn" trong khi thực chất request mới là thứ ảnh hưởng tới việc Pod có được lên lịch hay không.

**Hỏi**: Vì sao một container có CPU limit cao hơn nhiều so với usage trung bình vẫn có thể bị throttle?

**Trả lời**: Vì CFS quota được tính theo period ngắn (mặc định 100ms), không theo trung bình dài hạn. Nếu container burst dùng gần hết quota trong vài chục millisecond đầu của period (do nhiều thread chạy song song hoặc một tác vụ tốn CPU ngắn), nó bị đóng băng phần thời gian còn lại của period đó dù usage trung bình cả period vẫn thấp — hiện tượng này không hiện rõ trên biểu đồ CPU usage theo phút.

**Hỏi**: QoS Class của Kubernetes được xác định như thế nào, và nó ảnh hưởng gì tới hành vi của Pod khi node thiếu tài nguyên?

**Trả lời**: `Guaranteed` khi mọi container có request = limit cho cả CPU lẫn memory; `Burstable` khi có khai báo request nhưng khác limit hoặc thiếu limit; `BestEffort` khi không khai báo gì. Khi node gặp áp lực tài nguyên, kubelet evict theo thứ tự `BestEffort` trước, `Burstable` (ưu tiên Pod vượt request nhiều nhất) tiếp theo, `Guaranteed` bị evict cuối cùng — nên service quan trọng cần được đặt `Guaranteed` để tránh bị evict oan khi node căng thẳng tài nguyên.

## Summary

`requests` quyết định Pod được scheduler xếp vào node nào, còn `limits` quyết định Pod bị giới hạn ra sao khi đang chạy — CPU limit dẫn tới throttling qua CFS quota, memory limit dẫn tới OOMKill qua kernel OOM killer. Đặt request quá cao gây lãng phí tài nguyên và ép cluster scale không cần thiết; đặt request quá thấp hoặc limit quá sát usage thực tế gây throttling/OOMKill và noisy neighbor giữa các Pod cùng node. QoS Class (`Guaranteed`/`Burstable`/`BestEffort`) được suy ra trực tiếp từ cách khai báo request/limit và quyết định thứ tự Pod bị evict khi node thiếu tài nguyên. Giá trị request/limit đúng đắn phải dựa trên dữ liệu usage thực tế đo được (p95/p99), không phải con số đoán một lần rồi để nguyên mãi mãi. Theo dõi throttling và OOMKill qua metrics là cách duy nhất để phát hiện request/limit sai trước khi nó gây incident thực sự.

## Knowledge Graph

- Quality of Service Class (QoS) — được suy ra trực tiếp từ cách khai báo request/limit, quyết định thứ tự Pod bị evict.
- Horizontal Pod Autoscaler — dùng usage thực tế so với request để quyết định scale số replica.
- Vertical Pod Autoscaler — tự động đề xuất/điều chỉnh request/limit dựa trên lịch sử usage.
- Cluster Autoscaler — đọc tổng request (không phải usage) trên node để quyết định thêm/bớt node.
- cgroup & CFS Scheduler — cơ chế Linux bên dưới thực thi CPU throttling và memory hard limit mà Kubernetes dựa vào.
- Node Pressure Eviction — cơ chế kubelet evict Pod theo QoS Class khi node thiếu CPU/memory/disk.

## Five Things To Remember

- Request quyết định Pod được lên lịch ở đâu, limit quyết định Pod bị giới hạn ra sao khi chạy.
- CPU limit gây throttling qua CFS quota, memory limit gây OOMKill qua kernel OOM killer — hai cơ chế hoàn toàn khác nhau.
- Luôn đặt memory limit, vì thiếu nó một Pod leak memory có thể kéo theo OOM killer chọn nhầm nạn nhân trên cả node.
- Request = limit cho QoS `Guaranteed`, được ưu tiên giữ lại cuối cùng khi node thiếu tài nguyên.
- Đặt request/limit dựa trên usage thực tế đo được, không phải con số đoán một lần rồi giữ mãi mãi.
