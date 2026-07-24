---
id: horizontal-pod-autoscaling
title: Horizontal Pod Autoscaling
tags: ["kubernetes"]
---

# Horizontal Pod Autoscaling

> Status: Draft

## Problem

Tải của một service production không cố định — traffic ban ngày cao gấp 5-10 lần ban đêm, batch job cuối tháng đẩy CPU lên đột biến, một campaign marketing có thể nhân traffic lên gấp đôi trong vài phút. Nếu số pod của một Deployment cố định (`replicas: 10` viết chết trong manifest), team phải chọn giữa hai thái cực tệ như nhau: provision đủ pod cho peak traffic và lãng phí tài nguyên phần lớn thời gian trong ngày, hoặc provision cho tải trung bình và bị quá tải, tăng p99 latency, thậm chí trả 5xx hàng loạt khi traffic vượt ngưỡng. HorizontalPodAutoscaler (HPA) giải quyết đúng bài toán này bằng cách tự động tăng/giảm số pod dựa trên metric thực đo được, nhưng bản thân HPA không tạo ra tài nguyên từ hư không — nó chỉ ra quyết định đúng khi có dữ liệu đầu vào đúng, cụ thể là `resource requests` chính xác trên container.

## Pain Points

- Không có HPA, on-call phải scale thủ công (`kubectl scale`) mỗi khi traffic tăng, luôn chậm hơn tốc độ tăng traffic thực tế — sự cố đã xảy ra và khách hàng đã thấy lỗi trước khi con người kịp phản ứng.
- Fixed replica count cho tải peak khiến chi phí compute lãng phí 60-80% trong giờ thấp điểm, một khoản chi phí cloud âm thầm cộng dồn hàng tháng mà không ai để ý vì không có alert cho "lãng phí", chỉ có alert cho "sập".
- Khi `resource requests` không đặt hoặc đặt sai (ví dụ request CPU 100m nhưng pod thực dùng trung bình 400m), HPA tính phần trăm sử dụng CPU dựa trên request sai này, dẫn đến quyết định scale hoàn toàn sai lệch — có thể không scale khi thực sự cần, hoặc scale ồ ạt khi không cần.
- Thiếu HPA kết hợp với thiếu resource limits hợp lý có thể khiến một pod bị quá tải kéo theo toàn bộ node bị đánh giá "under pressure", kubelet bắt đầu evict pod khác trên cùng node, biến sự cố cục bộ thành sự cố lan rộng.

## Solution

HorizontalPodAutoscaler là một control loop chạy trong control plane, định kỳ đọc metric hiện tại (CPU, memory, hoặc custom/external metric qua Metrics Server hay Prometheus Adapter) của các pod thuộc một Deployment/StatefulSet, so sánh với target đã cấu hình, rồi tính toán số replica mới cần thiết và cập nhật trực tiếp field `spec.replicas` của resource đó. Điều kiện tiên quyết bắt buộc để cơ chế này hoạt động đúng là container phải khai báo `resources.requests` chính xác — vì HPA không nhìn vào mức sử dụng tuyệt đối (ví dụ "512Mi RAM"), nó nhìn vào tỷ lệ phần trăm so với request đã khai báo, nên request sai lệch làm sai lệch toàn bộ phép tính dù usage thực tế không đổi.

## How It Works

HPA controller chạy một vòng lặp poll theo chu kỳ mặc định 15 giây (`--horizontal-pod-autoscaler-sync-period` trên kube-controller-manager), mỗi vòng lấy metric hiện tại từ Metrics Server (cho CPU/memory chuẩn qua `metrics.k8s.io` API) hoặc từ custom/external metrics API (Prometheus Adapter, Datadog Cluster Agent) cho các metric tùy chỉnh như request-per-second hay queue depth. Công thức tính replica mong muốn là `desiredReplicas = ceil(currentReplicas * (currentMetricValue / desiredMetricValue))` — ví dụ 4 pod đang chạy trung bình 80% CPU với target 50% sẽ được scale lên `ceil(4 * 80/50) = 7` pod. Với metric kiểu `Utilization` (phần trăm), `currentMetricValue` được tính bằng cách lấy usage thực đo của từng pod chia cho `resources.requests.cpu` (hoặc memory) của chính pod đó rồi lấy trung bình toàn bộ pod — đây là lý do request sai lệch làm sai toàn bộ thuật toán: nếu request CPU đặt 100m nhưng pod thực tế cần và dùng ổn định 400m, HPA sẽ liên tục thấy "400% utilization" và scale up không ngừng dù pod hoàn toàn khỏe mạnh, ngược lại nếu request đặt quá cao (2000m cho workload chỉ cần 200m) HPA sẽ không bao giờ thấy tỷ lệ vượt ngưỡng và không scale dù pod đang quá tải thực sự. Để tránh dao động replica liên tục (flapping) khi metric dao động quanh ngưỡng, HPA áp dụng hai cơ chế: stabilization window (mặc định 0 giây cho scale-up, 300 giây cho scale-down — tức là quyết định giảm replica phải giữ ổn định trong 5 phút trước khi thực thi) và tolerance biên 10% quanh target để bỏ qua các thay đổi nhỏ không đáng scale. Từ Kubernetes 1.23 trở lên (`autoscaling/v2`), HPA hỗ trợ nhiều metric đồng thời và policy tùy chỉnh tốc độ scale (`behavior.scaleUp/scaleDown` với `policies` giới hạn số pod hoặc phần trăm thay đổi mỗi phút), cho phép ví dụ scale-up nhanh (thêm tối đa 4 pod hoặc 100% mỗi 15 giây) nhưng scale-down thận trọng (giảm tối đa 1 pod mỗi phút) để tránh thrashing.

## Production Architecture

Trong một cụm production điển hình, HPA không đứng một mình — nó nằm giữa Metrics Server (thu thập CPU/memory từ cAdvisor/kubelet mỗi node) và Cluster Autoscaler (hoặc Karpenter) ở tầng thấp hơn: khi HPA quyết định cần thêm pod nhưng cluster không còn node đủ tài nguyên để schedule, các pod mới sẽ ở trạng thái `Pending`, và chính tín hiệu Pending này kích hoạt Cluster Autoscaler thêm node mới — hai autoscaler làm việc theo hai tầng khác nhau (pod-level và node-level) nhưng phụ thuộc chuỗi vào nhau. Với hệ thống có traffic đặc thù theo request-per-second thay vì CPU-bound (ví dụ API gateway hoặc service xử lý I/O nhiều hơn compute), team thường bỏ qua CPU-based HPA và dùng custom metric qua Prometheus Adapter, target trực tiếp vào `http_requests_per_second` hoặc độ dài queue Kafka consumer lag — vì CPU utilization có thể trông thấp trong khi latency đã tăng do I/O wait. Một pattern phổ biến khác là kết hợp HPA với PodDisruptionBudget để đảm bảo khi scale-down diễn ra đồng thời với node maintenance hoặc rolling update, số pod tối thiểu vẫn được giữ để không mất khả năng phục vụ; và kết hợp với Vertical Pod Autoscaler ở chế độ "recommendation only" để liên tục tinh chỉnh giá trị `resources.requests` sát với usage thực tế theo thời gian, vì requests đặt lúc launch service thường lệch dần khi traffic pattern hoặc code thay đổi.

## Trade-offs

Scale dựa trên CPU/memory đơn giản, dùng Metrics Server có sẵn không cần cài thêm gì, nhưng phản ứng chậm với các workload mà CPU không phải chỉ báo chính xác cho tải thực (I/O-bound service, service chờ downstream), khiến HPA scale trễ so với lúc user đã bắt đầu thấy chậm. Custom metric (RPS, queue depth) phản ánh đúng tải nghiệp vụ hơn nhưng đòi hỏi hạ tầng giám sát bổ sung (Prometheus + Adapter) và một tầng phức tạp vận hành mới có thể tự nó là điểm lỗi (Adapter down = HPA không có dữ liệu, không thể scale). Stabilization window dài (mặc định 300 giây cho scale-down) tránh flapping nhưng đồng nghĩa cluster giữ dư pod trong 5 phút sau khi tải đã giảm, tốn chi phí compute không cần thiết trong khoảng thời gian đó — ngắn quá thì flapping, dài quá thì lãng phí, không có giá trị trung tính tuyệt đối cho mọi workload. Scale-up nhanh giúp hấp thụ traffic spike tốt nhưng nếu ứng dụng có cold-start chậm (JVM warm-up, kết nối DB pool khởi tạo, cache rỗng), pod mới tạo ra không phục vụ được ngay, khiến HPA scale đúng số lượng nhưng vẫn không giải quyết được latency trong vài chục giây đầu.

## Best Practices

- Luôn đặt `resources.requests` dựa trên profiling/load test thực tế (không copy giá trị mặc định hay đoán mò), vì đây là mẫu số của mọi phép tính utilization mà HPA dựa vào.
- Cấu hình `behavior.scaleUp` và `behavior.scaleDown` tường minh thay vì dùng mặc định, để scale-up đủ nhanh hấp thụ spike và scale-down đủ thận trọng tránh flapping theo đặc tính traffic thực của từng service.
- Với service I/O-bound hoặc có SLA latency nghiêm ngặt, dùng custom/external metric (RPS, queue lag) qua Prometheus Adapter thay vì chỉ dựa vào CPU/memory utilization.
- Luôn đặt `minReplicas` >= 2 cho service chịu traffic thật để tránh single point of failure khi scale-down, và kết hợp với PodDisruptionBudget để bảo vệ availability trong lúc node maintenance/rolling update.
- Định kỳ chạy Vertical Pod Autoscaler ở chế độ recommendation-only (không auto-apply) để phát hiện lệch giữa `requests` khai báo và usage thực tế, rồi cập nhật thủ công có kiểm soát.

## Common Mistakes

- Không đặt `resources.requests` hoặc đặt một giá trị tùy tiện copy từ service khác, khiến HPA scale dựa trên tỷ lệ phần trăm vô nghĩa so với nhu cầu thực.
- Chỉ dùng CPU utilization làm metric cho một service I/O-bound (gọi API bên ngoài, chờ DB), nơi CPU thấp dù latency đã tăng cao, khiến HPA không bao giờ scale khi thực sự cần.
- Đặt `minReplicas: 1`, khiến quá trình scale-down có thể đưa service về đúng một pod duy nhất, mất khả năng chịu lỗi và tolerate rolling update.
- Không tính đến cold-start time của ứng dụng khi cấu hình `scaleUp.policies`, khiến pod mới được tạo đúng lúc traffic tăng nhưng chưa sẵn sàng phục vụ, HPA "trông như hoạt động" nhưng latency vẫn cao trong lúc pod khởi động.
- Quên rằng HPA phụ thuộc vào khả năng cluster có đủ node để schedule pod mới — không kết hợp với Cluster Autoscaler/Karpenter, HPA tăng `desiredReplicas` nhưng pod kẹt ở `Pending` vô thời hạn.

## Interview Questions

**Hỏi**: Vì sao `resources.requests` cấu hình sai lại làm HPA hoạt động sai, dù usage thực tế của pod không đổi?

**Trả lời**: Với metric kiểu Utilization, HPA tính tỷ lệ phần trăm bằng usage thực đo chia cho `resources.requests` đã khai báo, không phải giá trị tuyệt đối. Nếu request đặt quá thấp so với nhu cầu thực, tỷ lệ phần trăm luôn cao bất thường và HPA scale up liên tục dù pod không thực sự quá tải; nếu request đặt quá cao, tỷ lệ luôn thấp và HPA không bao giờ scale dù pod đã quá tải thực sự. Request chính xác là điều kiện tiên quyết để phép tính này phản ánh đúng tải thực.

**Hỏi**: Tại sao scale-up và scale-down thường được cấu hình với tốc độ khác nhau (nhanh cho lên, chậm cho xuống)?

**Trả lời**: Scale-up cần nhanh để hấp thụ traffic spike trước khi user chịu ảnh hưởng, còn scale-down cần chậm và có stabilization window dài (mặc định 300 giây) để tránh flapping — nếu giảm ngay khi metric tụt xuống tạm thời rồi phải tăng lại ngay sau đó, cluster liên tục tạo/hủy pod gây lãng phí và có thể ảnh hưởng đến các pod đang phục vụ do rolling terminate liên tục.

**Hỏi**: HPA và Cluster Autoscaler khác nhau ở điểm nào, và tại sao cần cả hai trong một hệ thống production co giãn thực sự?

**Trả lời**: HPA scale số lượng pod của một workload dựa trên metric ứng dụng (CPU, memory, custom metric), còn Cluster Autoscaler scale số lượng node vật lý/VM của cluster dựa trên việc có pod nào đang `Pending` vì thiếu tài nguyên schedule. Nếu chỉ có HPA mà không có Cluster Autoscaler, HPA có thể quyết định đúng cần thêm pod nhưng các pod đó kẹt ở trạng thái Pending vô thời hạn vì cluster không đủ node — hai cơ chế phải hoạt động cùng nhau ở hai tầng khác nhau để scale thực sự diễn ra.

## Summary

HorizontalPodAutoscaler tự động điều chỉnh số replica của một Deployment dựa trên metric CPU, memory, hoặc custom/external metric, giải quyết bài toán tải biến động mà fixed replica count không thể xử lý hiệu quả về cả chi phí lẫn khả năng chịu tải. Cơ chế cốt lõi là so sánh tỷ lệ phần trăm usage/request hiện tại với target đã cấu hình, nên `resources.requests` chính xác là điều kiện tiên quyết bắt buộc — sai request dẫn đến quyết định scale hoàn toàn sai lệch dù usage thực tế không đổi. Trong production, HPA thường kết hợp với Cluster Autoscaler (scale node khi pod Pending vì thiếu tài nguyên), PodDisruptionBudget (bảo vệ availability khi scale-down), và Vertical Pod Autoscaler ở chế độ recommendation để liên tục hiệu chỉnh requests theo usage thực tế. Trade-off chính nằm ở tốc độ phản ứng (scale-up nhanh vs. scale-down thận trọng để tránh flapping) và loại metric sử dụng (CPU đơn giản nhưng có thể không phản ánh đúng tải I/O-bound). Cấu hình đúng đòi hỏi profiling thực tế, không đoán mò, và hiểu rõ HPA chỉ là một tầng trong chuỗi autoscaling nhiều tầng của Kubernetes.

## Knowledge Graph

- Cluster Autoscaler — tầng scale node vật lý bên dưới, HPA không có tác dụng nếu cluster không đủ node để schedule pod mới.
- Vertical Pod Autoscaler — công cụ bổ trợ để hiệu chỉnh chính xác `resources.requests`, đầu vào bắt buộc cho HPA hoạt động đúng.
- Pod Disruption Budget — bảo vệ số lượng pod tối thiểu khi HPA scale-down trùng với node maintenance hoặc rolling update.
- Connection Pool Exhaustion — khi HPA scale số pod tăng đột ngột, tổng số connection tới DB có thể vượt `max_connections` nếu không có connection pooler trung gian.
- Backpressure — cơ chế bổ sung ở tầng ứng dụng để chịu tải trong khoảng thời gian trễ giữa lúc traffic tăng và lúc pod mới sẵn sàng phục vụ.
- Health Check — readiness probe quyết định khi nào pod mới do HPA tạo ra thực sự nhận traffic, ảnh hưởng trực tiếp đến hiệu quả scale-up.

## Five Things To Remember

- HPA đọc metric theo chu kỳ mặc định 15 giây và tính replica mới dựa trên tỷ lệ hiện tại so với target.
- `resources.requests` chính xác là điều kiện bắt buộc, vì Utilization được tính bằng usage chia cho request.
- Scale-down có stabilization window mặc định 300 giây để tránh flapping, scale-up mặc định phản ứng ngay.
- HPA scale pod, Cluster Autoscaler scale node — cần cả hai để scale thực sự thành công khi cluster thiếu tài nguyên.
- Với workload I/O-bound, ưu tiên custom metric (RPS, queue lag) thay vì chỉ dựa vào CPU utilization.
