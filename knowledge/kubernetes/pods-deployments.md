---
id: pods-deployments
title: Pods & Deployments
tags: ["kubernetes"]
---

# Pods & Deployments

> Status: Draft

## Problem

Đội ngũ mới sang Kubernetes thường cố gắng chạy container trực tiếp và deploy thủ công bằng `kubectl run`, hoặc coi Pod như một đơn vị "server ảo" cần được quản lý bằng tay — sửa image, restart, scale từng cái một. Khi một node bị lỗi hoặc một container crash, không có cơ chế nào tự động phát hiện và tạo lại đúng số lượng bản sao mong muốn, và khi cần deploy phiên bản mới, không có quy trình nào đảm bảo chuyển đổi tuần tự mà không làm gián đoạn traffic. Gốc rễ là không hiểu rằng Pod là đơn vị deploy nhỏ nhất nhưng dùng một lần (ephemeral), còn việc quản lý vòng đời, số lượng bản sao và rolling update phải giao cho một controller cấp cao hơn — Deployment.

## Pain Points

- Pod chết (OOMKilled, node drain, crash loop) mà không có ReplicaSet quản lý thì không tự tạo lại — service giảm capacity âm thầm cho đến khi có người phát hiện qua alert hoặc khiếu nại từ user.
- Deploy bằng cách xóa Pod cũ rồi tạo Pod mới thủ công gây downtime hoàn toàn trong khoảng thời gian Pod mới chưa sẵn sàng — không có cơ chế đảm bảo Pod mới pass readiness probe trước khi Pod cũ bị gỡ khỏi traffic.
- Không có revision history nghĩa là khi bản deploy mới lỗi (crash loop, memory leak mới xuất hiện), không có cách rollback nhanh về phiên bản ổn định trước đó — phải tự build lại image cũ và deploy lại từ đầu, kéo dài MTTR.
- Scale thủ công từng Pod một (thêm/bớt bằng tay) không nhất quán với desired state khai báo trong Git, dẫn đến drift giữa config trong repo và trạng thái thực tế trên cluster, gây khó khăn khi audit hoặc troubleshoot.

## Solution

Pod là đơn vị deploy nhỏ nhất trong Kubernetes — một hoặc nhiều container chia sẻ network namespace (cùng IP, cùng localhost) và storage volume, được scheduler gán vào một node cụ thể và chạy như một khối thống nhất. Deployment là một controller cấp cao hơn quản lý một ReplicaSet, đảm bảo luôn có đúng số lượng Pod replica đang chạy đúng như khai báo, đồng thời điều phối rolling update — thay thế dần các Pod cũ bằng Pod mới theo một chiến lược có kiểm soát (mặc định là `RollingUpdate`) để không làm gián đoạn service. Nói cách khác, Pod trả lời câu hỏi "chạy cái gì", còn Deployment trả lời câu hỏi "chạy bao nhiêu, chạy phiên bản nào, và chuyển đổi phiên bản như thế nào".

## How It Works

Deployment không quản lý Pod trực tiếp mà tạo ra và quản lý một ReplicaSet — mỗi lần thay đổi `spec.template` (image, env, resource limit...) trong Deployment, một ReplicaSet mới được tạo ra với hash riêng (label `pod-template-hash`), còn ReplicaSet cũ được giữ lại (scale về 0) để phục vụ rollback. ReplicaSet lại là controller chịu trách nhiệm giữ đúng số lượng Pod theo `spec.replicas`: nó liên tục so sánh số Pod thực tế (match theo label selector) với số mong muốn, tạo thêm hoặc xóa bớt Pod để khớp — đây chính là cơ chế self-healing khi Pod bị xóa hoặc node chết.

Khi rolling update diễn ra, Deployment controller điều phối theo hai tham số `maxSurge` và `maxUnavailable` (mặc định mỗi cái 25%): `maxSurge` cho phép tạo thêm tối đa bao nhiêu Pod mới vượt quá `replicas` trong lúc chuyển đổi, `maxUnavailable` cho phép tối đa bao nhiêu Pod cũ được gỡ xuống cùng lúc. Pod mới chỉ được tính là "sẵn sàng nhận traffic" khi readiness probe pass và trải qua `minReadySeconds` (nếu cấu hình) — Deployment chờ tín hiệu này trước khi tiếp tục gỡ Pod cũ tiếp theo, đảm bảo tại mọi thời điểm luôn có đủ Pod sẵn sàng phục vụ. Bên trong một Pod, các container chia sẻ network namespace nên giao tiếp qua `localhost`, chia sẻ volume qua `emptyDir` hoặc volume mount chung, nhưng có filesystem và cgroup resource limit riêng — mô hình này cho phép pattern sidecar (container phụ trợ như log shipper, service mesh proxy) chạy cạnh container chính mà không cần container chính biết đến sự tồn tại của nó.

## Production Architecture

Trong một cluster production điển hình, mỗi service backend (vd. `payment-service`, `order-service`) được khai báo là một Deployment riêng với `replicas` tối thiểu 2-3 để chịu được node failure hoặc voluntary disruption (node drain khi upgrade Kubernetes). Deployment đi kèm `readinessProbe` (kiểm tra endpoint `/healthz` hoặc kết nối DB) để Service/Ingress chỉ route traffic tới Pod thực sự sẵn sàng, và `livenessProbe` để kubelet tự restart container bị treo (deadlock, memory leak không crash nhưng không phản hồi). Resource `requests`/`limits` được set trên container để scheduler đặt Pod vào node có đủ tài nguyên và để tránh một Pod ăn hết CPU/memory của node ảnh hưởng Pod khác cùng node. HorizontalPodAutoscaler (HPA) gắn vào Deployment để tự động scale `replicas` theo CPU/memory hoặc custom metric (vd. queue length), còn PodDisruptionBudget (PDB) giới hạn số Pod tối đa được gỡ đồng thời khi node drain hoặc cluster autoscaler hoạt động, tránh rolling update hoặc bảo trì hạ tầng vô tình đưa service về dưới ngưỡng chịu tải. Trong pipeline CI/CD (ArgoCD, Flux, hoặc `kubectl set image`), thay đổi image tag trong Deployment manifest chính là trigger duy nhất để kích hoạt rolling update — không có bước "restart thủ công" nào cần thiết.

## Trade-offs

Rolling update mặc định ưu tiên zero-downtime bằng cách chạy song song cả phiên bản cũ và mới trong một khoảng thời gian, nghĩa là trong lúc chuyển đổi, hai phiên bản code khác nhau đồng thời phục vụ traffic — nếu có thay đổi schema DB không tương thích ngược (backward-incompatible), phiên bản cũ có thể crash hoặc ghi dữ liệu sai định dạng trước khi rollout hoàn tất. `maxSurge` cao giúp rollout nhanh hơn nhưng tốn tài nguyên tạm thời (cần capacity dư trên node để chạy thêm Pod), còn `maxUnavailable` cao giúp rollout nhanh hơn nhưng giảm capacity phục vụ tạm thời trong lúc chuyển đổi — đây là đánh đổi trực tiếp giữa tốc độ deploy và độ an toàn. Pod là ephemeral theo thiết kế (IP đổi mỗi lần tạo lại, filesystem local mất khi Pod chết), phù hợp cho stateless workload nhưng đòi hỏi kiến trúc riêng (StatefulSet, volume claim bền vững) cho workload có state — cố gắng ép state vào Deployment thường dẫn đến mất dữ liệu khi Pod bị reschedule sang node khác.

## Best Practices

- Luôn khai báo cả `readinessProbe` và `livenessProbe` riêng biệt — probe chung dễ gây vòng lặp restart khi container chỉ đang bận (chưa sẵn sàng) chứ không phải đã chết.
- Set `resources.requests` và `resources.limits` cho mọi container, tránh để Pod không có giới hạn chiếm hết tài nguyên node và ảnh hưởng Pod khác.
- Cấu hình PodDisruptionBudget cho service quan trọng để giới hạn số Pod bị gỡ đồng thời khi node drain hoặc cluster maintenance.
- Dùng `kubectl rollout status` và `kubectl rollout undo` như quy trình chuẩn khi deploy và rollback, không xóa/tạo lại Deployment thủ công.
- Giữ `replicas` tối thiểu từ 2 trở lên cho service production để chịu được một Pod/node fail mà không mất hoàn toàn khả năng phục vụ.

## Common Mistakes

- Chạy một Pod trần (không qua Deployment) cho service production — Pod chết không ai tạo lại, không có rolling update, không có rollback.
- Đặt `maxUnavailable: 0` và `maxSurge: 0` cùng lúc, khiến Deployment không có cách nào thực hiện rolling update (không được tạo thêm cũng không được gỡ bớt).
- Thiếu readinessProbe khiến Deployment coi Pod mới "sẵn sàng" ngay khi container start xong, trong khi ứng dụng bên trong còn đang warm up hoặc kết nối DB, dẫn tới request lỗi ngay sau khi traffic được route tới.
- Thay đổi schema DB breaking change cùng lúc với rollout code mới mà không tính đến việc hai phiên bản Pod chạy song song trong lúc chuyển đổi.
- Nhầm lẫn giữa xóa Deployment và scale về 0 — xóa Deployment xóa luôn revision history, không thể rollback về các bản trước.

## Interview Questions

**Hỏi**: Deployment, ReplicaSet và Pod quan hệ với nhau như thế nào?

**Trả lời**: Deployment quản lý ReplicaSet, ReplicaSet quản lý Pod. Mỗi lần thay đổi template Pod trong Deployment tạo ra một ReplicaSet mới; ReplicaSet chịu trách nhiệm giữ đúng số lượng Pod đang chạy theo `replicas` khai báo bằng cách tạo/xóa Pod khi cần. Deployment thêm lớp điều phối rolling update và revision history phía trên ReplicaSet.

**Hỏi**: Vì sao Pod bị coi là ephemeral, và điều này ảnh hưởng gì tới thiết kế ứng dụng?

**Trả lời**: Pod có thể bị xóa và tạo lại bất kỳ lúc nào (node fail, scale down, rolling update), mỗi lần tạo lại nhận IP mới và mất toàn bộ dữ liệu ghi vào filesystem local. Ứng dụng chạy trong Pod cần được thiết kế stateless — state phải lưu ở nơi bền vững ngoài Pod (DB, object storage, hoặc PersistentVolume nếu dùng StatefulSet) chứ không phụ thuộc vào việc Pod tồn tại lâu dài.

**Hỏi**: `maxSurge` và `maxUnavailable` khác nhau như thế nào trong chiến lược RollingUpdate?

**Trả lời**: `maxSurge` giới hạn số Pod mới được phép tạo thêm vượt quá `replicas` mong muốn trong lúc rollout, còn `maxUnavailable` giới hạn số Pod cũ được phép gỡ xuống cùng lúc. Tăng `maxSurge` giúp rollout nhanh hơn nhưng cần capacity dư trên cluster; tăng `maxUnavailable` cũng giúp rollout nhanh hơn nhưng chấp nhận giảm capacity phục vụ tạm thời.

## Summary

Pod là đơn vị deploy nhỏ nhất trong Kubernetes, đóng gói một hoặc nhiều container chia sẻ network và storage, nhưng bản thân Pod không tự phục hồi khi chết. Deployment giải quyết vấn đề đó bằng cách quản lý một ReplicaSet giữ đúng số lượng Pod mong muốn, đồng thời điều phối rolling update qua `maxSurge`/`maxUnavailable` để chuyển đổi phiên bản mà không gián đoạn service. Cơ chế này dựa trên readiness probe để xác định khi nào Pod mới thực sự sẵn sàng nhận traffic, và giữ lại ReplicaSet cũ để hỗ trợ rollback nhanh. Trong production, Deployment luôn đi kèm resource limits, probes, PodDisruptionBudget và HPA để đảm bảo vừa tự phục hồi vừa scale đúng tải. Việc hiểu đúng ranh giới Pod/ReplicaSet/Deployment là nền tảng để tránh thao tác thủ công gây downtime hoặc mất khả năng rollback.

## Knowledge Graph

- Service (Kubernetes) — cung cấp một endpoint ổn định và load balancing tới các Pod do Deployment quản lý, bù đắp cho việc Pod IP thay đổi liên tục.
- StatefulSet — controller thay thế Deployment khi workload cần identity ổn định và storage bền vững, dùng cho database hoặc message broker.
- HorizontalPodAutoscaler — tự động điều chỉnh `replicas` của Deployment dựa trên metric, hoạt động phía trên cơ chế self-healing của ReplicaSet.
- PodDisruptionBudget — giới hạn số Pod bị gỡ đồng thời trong voluntary disruption, bảo vệ capacity tối thiểu trong lúc rollout hoặc node maintenance.
- Readiness & Liveness Probe — tín hiệu health check quyết định khi nào Pod nhận traffic và khi nào container cần bị restart.
- Rolling Update / Blue-Green Deployment — chiến lược chuyển đổi phiên bản khác nhau, rolling update là mặc định của Deployment còn blue-green cần thiết kế thêm ở tầng traffic routing.

## Five Things To Remember

- Pod là đơn vị deploy nhỏ nhất nhưng ephemeral — không tự phục hồi, không nên chạy trần trong production.
- Deployment quản lý ReplicaSet, ReplicaSet giữ đúng số lượng Pod mong muốn bằng cách tạo/xóa liên tục.
- Rolling update thay Pod cũ bằng Pod mới dần dần, kiểm soát bằng `maxSurge` và `maxUnavailable`.
- Readiness probe quyết định khi nào Pod mới được tính là sẵn sàng nhận traffic trong lúc rollout.
- Revision history của Deployment cho phép rollback nhanh — xóa Deployment thay vì scale về 0 sẽ mất khả năng đó.
