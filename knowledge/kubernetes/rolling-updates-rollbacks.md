---
id: rolling-updates-rollbacks
title: Rolling Updates & Rollbacks
tags: ["kubernetes", "deployment"]
---

# Rolling Updates & Rollbacks

> Status: Draft

## Problem

Khi deploy phiên bản mới của một service, cách đơn giản nhất — xóa hết Pod cũ rồi tạo Pod mới — tạo ra một khoảng downtime hoàn toàn trong lúc Pod mới chưa sẵn sàng, và nếu phiên bản mới bị lỗi (crash loop, bug logic, memory leak mới xuất hiện), toàn bộ traffic đã hướng vào bản lỗi trước khi ai kịp phát hiện. Đội vận hành cần một cơ chế thay thế Pod cũ bằng Pod mới theo từng bước có kiểm soát, đồng thời có đường lùi nhanh về phiên bản ổn định trước đó khi metric xấu đi ngay sau deploy — thay vì phải build lại image cũ và deploy lại từ đầu, tốn thời gian đúng lúc incident đang diễn ra.

## Pain Points

- Deploy toàn bộ Pod cùng lúc (recreate) gây downtime hoàn toàn trong khoảng thời gian Pod mới build image, start container, warm cache — có thể kéo dài hàng chục giây tới vài phút tùy ứng dụng.
- Không giới hạn tốc độ rollout khiến một bug nghiêm trọng trong phiên bản mới (ví dụ NPE ở request path chính) lan ra 100% Pod trong vài giây, biến một lỗi lẽ ra chỉ ảnh hưởng một phần traffic thành outage toàn phần.
- Rollback thủ công (tìm lại image tag cũ, sửa manifest, `kubectl apply` lại) tốn nhiều phút giữa lúc incident đang cháy, kéo dài MTTR đúng lúc cần khôi phục nhanh nhất; mỗi phút chậm rollback ở service thanh toán hoặc checkout là doanh thu mất trực tiếp.
- Rollout không theo dõi readiness thực tế của Pod mới (chỉ dựa vào "container đã start") khiến traffic bị route vào Pod chưa kết nối xong database hoặc chưa load xong cấu hình, gây một loạt lỗi 5xx ngắn nhưng lặp lại ở mỗi lần deploy.

## Solution

`RollingUpdate` là chiến lược mặc định của Deployment: thay vì đổi toàn bộ Pod cùng lúc, controller thay thế dần từng nhóm nhỏ Pod cũ bằng Pod mới, tốc độ và mức độ song song được kiểm soát bởi hai tham số `maxSurge` (số Pod mới được phép tạo thêm vượt quá `replicas`) và `maxUnavailable` (số Pod cũ được phép gỡ khỏi traffic cùng lúc). Mỗi lần thay đổi `spec.template`, Deployment tạo một ReplicaSet mới và giữ lại ReplicaSet cũ (scale về 0) làm lịch sử revision — đây chính là cơ sở để `kubectl rollout undo` có thể đưa Pod về đúng cấu hình của một revision trước đó gần như ngay lập tức, không cần build lại gì cả. Kết hợp readiness probe (Pod mới chỉ được tính "đã sẵn sàng" khi probe pass) với revision history, Kubernetes cho phép vừa deploy an toàn theo từng bước, vừa lùi lại nhanh khi phát hiện lỗi mà không cần rebuild hay redeploy thủ công.

## How It Works

Deployment controller so sánh spec hiện tại với ReplicaSet đang active; khi khác nhau, nó tạo ReplicaSet mới với `pod-template-hash` riêng và bắt đầu vòng lặp điều chỉnh số lượng Pod ở cả hai ReplicaSet để tuân thủ `maxSurge`/`maxUnavailable`. Với `replicas: 10`, `maxSurge: 25%`, `maxUnavailable: 25%` (mặc định), controller được phép có tối đa 12-13 Pod tồn tại đồng thời (10 + surge) và tối thiểu 7-8 Pod sẵn sàng tại mọi thời điểm (10 - unavailable) — nó tạo thêm vài Pod mới, chờ chúng pass readiness probe (và trải qua `minReadySeconds` nếu cấu hình) trước khi coi là "available", rồi mới gỡ tương ứng số Pod cũ, lặp lại cho tới khi ReplicaSet mới đạt đủ `replicas` và ReplicaSet cũ về 0. Nếu readiness probe của Pod mới liên tục fail, controller không có gì để thay thế nên tự động dừng tiến trình rollout ở đó (không rollback tự động), thể hiện qua `kubectl rollout status` treo và Deployment condition `Progressing` chuyển `False` sau khi vượt `progressDeadlineSeconds` (mặc định 600s).

Rollback (`kubectl rollout undo --to-revision=N`) thực chất là một rolling update ngược chiều: Deployment controller đọc `spec.template` được lưu trong ReplicaSet của revision N (annotation `deployment.kubernetes.io/revision` đánh số từng ReplicaSet), coi đó là spec mới, rồi chạy lại đúng cơ chế rolling update ở trên — scale ReplicaSet revision N lên, scale ReplicaSet hiện tại (lỗi) xuống, vẫn tuân theo `maxSurge`/`maxUnavailable` và vẫn chờ readiness probe. Vì vậy rollback không tức thời theo nghĩa "chuyển ngay lập tức" mà nhanh theo nghĩa không cần rebuild image hay tính toán lại manifest — thời gian rollback thực tế gần bằng thời gian một rolling update thông thường, có thể rút ngắn bằng cách tăng tạm `maxSurge` cho phần rollback. Số lượng revision được giữ lại kiểm soát bởi `revisionHistoryLimit` (mặc định 10) — ReplicaSet cũ hơn giới hạn này bị garbage-collected, quá đó không thể rollback về được nữa dù object Deployment vẫn còn.

## Production Architecture

Trong CI/CD pipeline (ArgoCD, Flux, hoặc GitHub Actions gọi `kubectl set image`), một lần merge code kích hoạt thay đổi image tag trong manifest, Deployment tự chạy rolling update mà không cần thao tác thủ công nào khác. Với service chịu tải cao và nhạy cảm với gián đoạn (payment, checkout), team thường siết `maxUnavailable: 0` để không bao giờ giảm capacity trong lúc rollout, chấp nhận đổi lại bằng `maxSurge` dương (cần thêm node capacity tạm thời) và gắn PodDisruptionBudget để cluster autoscaler/node drain không vô tình gỡ thêm Pod trong lúc rollout đang diễn ra. Canary hoặc progressive delivery (Argo Rollouts, Flagger) mở rộng thêm trên nền rolling update cơ bản này bằng cách chèn bước phân tích metric (error rate, p99 latency qua Prometheus) giữa các bước tăng traffic, tự động rollback nếu metric vượt ngưỡng — đây là cơ chế rollback tự động mà Deployment gốc không có sẵn (Deployment chỉ rollback khi con người hoặc pipeline chủ động gọi `rollout undo`). Trong thực tế vận hành, đội SRE thường có alert gắn với `kubectl rollout status` hoặc Deployment condition `Progressing`/`ReplicaFailure` để phát hiện rollout bị kẹt, và runbook mặc định cho on-call là `kubectl rollout undo` ngay khi error rate tăng bất thường trong vài phút sau deploy, không chờ điều tra root cause trước.

## Trade-offs

`maxSurge` cao rút ngắn thời gian rollout nhưng đòi hỏi cluster có capacity dư để chạy thêm Pod tạm thời — nếu node đã gần đầy, Pod mới không schedule được và rollout bị kẹt ở trạng thái nửa chừng. `maxUnavailable` cao cũng rút ngắn thời gian rollout nhưng giảm capacity phục vụ thực tế trong lúc chuyển đổi, rủi ro với service đang ở gần ngưỡng tải. Rolling update đảm bảo có Pod cũ và Pod mới chạy song song trong một khoảng thời gian — nếu thay đổi có breaking change ở schema DB hoặc contract API không tương thích ngược, hai phiên bản cùng chạy có thể gây lỗi hoặc ghi dữ liệu sai mà một chiến lược Recreate (chấp nhận downtime) sẽ tránh được. Rollback qua `rollout undo` nhanh và không cần rebuild, nhưng không giải quyết vấn đề nếu lỗi nằm ở tầng dữ liệu đã bị thay đổi (migration đã chạy, dữ liệu đã ghi sai định dạng) — revert code không tự động revert schema hay dữ liệu, đây là giới hạn thực sự của cơ chế này chứ không phải thiếu sót cấu hình.

## Best Practices

- Luôn cấu hình readiness probe phản ánh đúng trạng thái sẵn sàng thực sự (kết nối DB, load xong cache) — rolling update chỉ an toàn khi tín hiệu "Pod mới đã sẵn sàng" đáng tin cậy.
- Với service nhạy cảm downtime, đặt `maxUnavailable: 0` và bù lại bằng `maxSurge` dương, đảm bảo cluster có đủ capacity dự phòng để schedule Pod surge.
- Tách migration schema DB thành bước riêng, tương thích ngược với cả phiên bản code cũ và mới, tránh việc hai phiên bản chạy song song trong lúc rollout gây lỗi dữ liệu.
- Gắn alert theo dõi metric ngay sau mỗi lần deploy (error rate, latency) và có runbook rollback rõ ràng — quyết định rollback nên dựa trên triệu chứng quan sát được, không chờ điều tra xong root cause.
- Với thay đổi rủi ro cao, dùng canary hoặc progressive delivery (Argo Rollouts, Flagger) để giới hạn blast radius và tự động rollback dựa trên metric, thay vì rolling update toàn bộ ngay từ đầu.

## Common Mistakes

- Đặt cả `maxSurge: 0` và `maxUnavailable: 0`, khiến Deployment không có Pod nào để thay thế hoặc bổ sung, rollout bị kẹt vĩnh viễn.
- Thiếu hoặc cấu hình readiness probe hời hợt (luôn trả 200), khiến rolling update coi Pod mới sẵn sàng ngay khi container start, đưa traffic vào Pod chưa thực sự hoạt động đúng.
- Deploy breaking change ở schema DB cùng lúc với code mới mà không tính tới việc Pod cũ và Pod mới chạy song song trong lúc rolling update, gây lỗi ghi/đọc dữ liệu ở cả hai phiên bản.
- Chờ điều tra xong root cause mới rollback, kéo dài thời gian outage không cần thiết — rollback là hành động rẻ và nhanh, nên thực hiện trước rồi điều tra sau khi hệ thống đã ổn định.
- Không set `revisionHistoryLimit` hợp lý hoặc để nó về quá thấp, khiến ReplicaSet của các revision ổn định gần nhất bị garbage-collected sớm, mất khả năng rollback về đúng bản mong muốn khi cần.

## Interview Questions

**Hỏi**: `maxSurge` và `maxUnavailable` khác nhau như thế nào, và chúng ảnh hưởng gì tới tốc độ với độ an toàn của rollout?

**Trả lời**: `maxSurge` giới hạn số Pod mới được tạo thêm vượt quá `replicas` mong muốn, `maxUnavailable` giới hạn số Pod cũ được gỡ khỏi traffic cùng lúc. Tăng cả hai giúp rollout nhanh hơn nhưng `maxSurge` cao cần capacity dư trên cluster còn `maxUnavailable` cao làm giảm capacity phục vụ thực tế trong lúc chuyển đổi — với service nhạy cảm downtime nên đặt `maxUnavailable: 0` và chấp nhận `maxSurge` dương.

**Hỏi**: `kubectl rollout undo` thực chất làm gì bên trong, và tại sao nó không phải là thao tác tức thời?

**Trả lời**: Nó đọc `spec.template` từ ReplicaSet của revision đích, coi đó là spec mới của Deployment, rồi chạy lại đúng cơ chế rolling update thông thường (vẫn tuân theo `maxSurge`/`maxUnavailable`, vẫn chờ readiness probe) để scale ReplicaSet cũ lên và ReplicaSet hiện tại xuống. Vì vậy thời gian rollback gần bằng một lần rolling update bình thường, không phải một cú chuyển đổi tức thì.

**Hỏi**: Tại sao rollback bằng `rollout undo` không giải quyết được lỗi gây ra bởi một migration schema DB đã chạy?

**Trả lời**: `rollout undo` chỉ đưa Pod về chạy lại image/code của revision cũ, nó không revert schema hay dữ liệu đã bị migration thay đổi. Nếu migration đã breaking change và dữ liệu đã bị ghi theo format mới, code cũ chạy lại có thể đọc sai hoặc lỗi với dữ liệu đó — cần chiến lược migration tương thích ngược hoặc rollback riêng ở tầng dữ liệu, tách biệt khỏi rollback code.

## Summary

Rolling update thay thế Pod cũ bằng Pod mới theo từng bước nhỏ, kiểm soát bởi `maxSurge` và `maxUnavailable`, dựa vào readiness probe để biết khi nào Pod mới đủ điều kiện nhận traffic và tiếp tục gỡ Pod cũ. Mỗi lần thay đổi spec tạo một ReplicaSet mới và giữ lại lịch sử revision, đây là nền tảng để rollback qua `kubectl rollout undo` diễn ra nhanh mà không cần rebuild image. Rollback về bản chất là một rolling update chạy ngược chiều, nên vẫn tốn thời gian tương đương, và nó chỉ khôi phục code chứ không tự động khôi phục schema hay dữ liệu đã thay đổi. Đánh đổi cốt lõi là giữa tốc độ rollout/rollback và mức độ an toàn (capacity dự phòng, khả năng chịu breaking change tạm thời khi hai phiên bản chạy song song). Canary và progressive delivery là lớp mở rộng thêm cơ chế phân tích metric và rollback tự động trên nền rolling update cơ bản này.

## Knowledge Graph

- Liveness & Readiness Probes — readiness probe là tín hiệu cốt lõi để Deployment biết khi nào Pod mới sẵn sàng tiếp tục rolling update.
- Pods & Deployments — ReplicaSet và revision history là cơ chế nền tảng mà rolling update và rollback vận hành trên đó.
- PodDisruptionBudget — giới hạn số Pod bị gỡ đồng thời, tương tác trực tiếp với `maxUnavailable` khi node drain xảy ra song song với rollout.
- Canary Deployment / Progressive Delivery — lớp mở rộng thêm phân tích metric và rollback tự động trên nền rolling update.
- Database Schema Migration — nguồn gốc của rủi ro breaking change khi hai phiên bản Pod chạy song song trong lúc rollout.
- HorizontalPodAutoscaler — có thể tương tác (và xung đột) với rolling update khi cả hai cùng thay đổi số lượng Pod tại một thời điểm.

## Five Things To Remember

- `maxSurge` kiểm soát Pod thêm, `maxUnavailable` kiểm soát Pod gỡ — cả hai không nên cùng bằng 0.
- Readiness probe đáng tin cậy là điều kiện tiên quyết để rolling update an toàn.
- Mỗi thay đổi spec tạo một ReplicaSet mới, đây là cơ sở cho rollback nhanh.
- Rollback là một rolling update ngược chiều, không phải một cú chuyển đổi tức thời.
- Rollback code không tự động khôi phục schema hay dữ liệu đã bị migration thay đổi.
