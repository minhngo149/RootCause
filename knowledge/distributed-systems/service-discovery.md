---
id: service-discovery
title: Service Discovery
tags: ["distributed-systems"]
---

# Service Discovery

> Status: Draft

## Problem

Trong một hệ thống microservices chạy trên container hoặc VM tự động scale, địa chỉ IP của mỗi instance không cố định — pod bị reschedule sang node khác, autoscaler tăng/giảm số replica, deployment rolling update thay hoàn toàn tập instance cũ bằng tập mới. Nếu service A hardcode IP hoặc dùng một danh sách địa chỉ tĩnh để gọi service B, mọi lần B thay đổi topology (deploy, scale, crash) đều khiến A gọi vào địa chỉ chết. Service discovery giải quyết đúng bài toán "làm sao service A luôn biết được tập địa chỉ hợp lệ, còn sống, hiện tại của service B" mà không cần con người cập nhật cấu hình thủ công mỗi lần topology đổi.

## Pain Points

- Gọi vào IP chết sau mỗi lần deploy hoặc pod bị OOMKilled/reschedule, gây connection refused hoặc timeout hàng loạt cho tới khi client retry trúng instance sống.
- Load bị dồn lệch: cấu hình tĩnh không phản ánh đúng số instance thực tế đang chạy, một vài instance nhận toàn bộ traffic trong khi instance mới scale ra không nhận được gì.
- Outage dây chuyền khi rolling deploy: nếu registry không loại bỏ instance đang shutdown đúng lúc (chưa deregister nhưng đã ngừng nhận request), client tiếp tục gửi request vào và nhận lỗi trong vài giây tới vài phút mỗi lần deploy.
- Chi phí vận hành tăng vọt khi số lượng service lớn: quản lý IP thủ công qua config file hoặc load balancer rule tay chân không scale được quá vài chục service, mỗi lần thêm instance là một thao tác thủ công dễ sai.

## Solution

Service discovery là cơ chế cho phép service tự động đăng ký địa chỉ của mình (registration) và tự động tra cứu địa chỉ của service khác (resolution) tại runtime, thông qua một service registry trung tâm hoặc qua DNS. Có hai mô hình chính: server-side discovery (client gọi qua một load balancer/proxy, load balancer tra registry và forward) và client-side discovery (client tự tra registry, tự chọn instance, tự gọi thẳng). Registry luôn đi kèm health checking để đảm bảo chỉ trả về địa chỉ của instance thực sự đang sống và sẵn sàng nhận traffic.

## How It Works

**Đăng ký (registration)** có hai kiểu: self-registration (chính service instance, khi khởi động, gọi API của registry để đăng ký địa chỉ mình, ví dụ Eureka client tự gửi heartbeat mỗi 30s) hoặc third-party registration (một tiến trình bên ngoài — registrar — theo dõi container runtime/orchestrator và tự đăng ký hộ, ví dụ Kubernetes control plane tự tạo Endpoints object khi pod chuyển sang trạng thái Ready mà service code không cần biết gì về registry).

**Tra cứu (resolution)** có hai mô hình:

1. **Client-side discovery**: client query registry trực tiếp (ví dụ gọi Eureka API hoặc query DNS SRV record của Consul) để lấy về toàn bộ danh sách địa chỉ instance đang sống, sau đó tự áp dụng thuật toán load balancing (round-robin, least-connections) để chọn một instance rồi gọi thẳng. Ưu điểm là bớt một hop mạng, nhưng client phải nhúng logic discovery và load balancing — mỗi ngôn ngữ/runtime cần một thư viện client riêng (Netflix Ribbon là ví dụ kinh điển đi cùng Eureka).

2. **Server-side discovery**: client chỉ gọi vào một địa chỉ cố định của load balancer/proxy (ví dụ một Kubernetes Service ClusterIP hay một Envoy sidecar), còn chính load balancer đó mới là bên query registry và quyết định forward request tới instance nào. Client hoàn toàn không biết registry tồn tại. Kubernetes Service là ví dụ chuẩn: kube-proxy (hoặc IPVS/eBPF trong các bản mới) duy trì rule NAT/DNAT ánh xạ ClusterIP sang tập PodIP hiện tại lấy từ Endpoints/EndpointSlice, cập nhật gần như realtime mỗi khi Endpoint Controller ghi nhận thay đổi.

**DNS-based discovery** là biến thể phổ biến của server-side discovery: mỗi service có một tên DNS ổn định (ví dụ `payment-service.default.svc.cluster.local`), còn DNS server (CoreDNS trong Kubernetes) trả về A record trỏ tới ClusterIP (một tầng gián tiếp qua load balancer) hoặc trả về nhiều A record/SRV record trỏ thẳng tới từng PodIP (headless service, dùng khi client cần tự quản lý connection tới từng instance, ví dụ client Cassandra hoặc gRPC client-side load balancing). Vấn đề kỹ thuật cần lưu ý: DNS TTL và caching ở resolver — nhiều client/thư viện cache DNS response lâu hơn TTL thực tế (do JVM DNS caching mặc định là "forever" nếu không cấu hình `networkaddress.cache.ttl`), khiến client vẫn gọi vào IP cũ dù DNS đã cập nhật.

**Health checking** là phần không thể tách rời: registry phải liên tục loại bỏ instance không còn healthy bằng active check (registry tự gọi `/healthz` định kỳ) hoặc passive/heartbeat check (instance tự báo còn sống, hết TTL mà không thấy heartbeat thì bị coi là chết) — thiếu phần này, discovery chỉ trả về "instance đã từng tồn tại" chứ không phải "instance đang sẵn sàng nhận traffic".

## Production Architecture

Trong Kubernetes, service discovery là server-side + DNS-based kết hợp: mỗi Service có một tên DNS ổn định qua CoreDNS, ClusterIP cố định, và kube-proxy duy trì iptables/IPVS rule để load balance across pod IP lấy từ EndpointSlice — toàn bộ vòng đời registration/deregistration được Kubelet và Endpoint Controller tự động hóa qua readiness probe, service code không cần biết registry tồn tại. Trong kiến trúc service mesh như Istio hay Linkerd, mỗi pod có một Envoy sidecar; sidecar này mới là bên thực sự query control plane (Istiod) để lấy danh sách endpoint và làm client-side load balancing ở tầng L7 (round-robin, least-request), cho phép áp cả retry, circuit breaking, mTLS ngay tại tầng discovery mà application code không đổi gì. Netflix (kiến trúc kinh điển tiền-Kubernetes) dùng Eureka làm registry với self-registration qua client heartbeat, kết hợp Ribbon cho client-side load balancing — mỗi service instance biết toàn bộ topology của các service phụ thuộc và tự chọn instance để gọi. HashiCorp Consul phổ biến trong hạ tầng VM-based (không container hóa) nhờ hỗ trợ cả DNS interface (SRV/A record) lẫn HTTP API, kèm health check đa dạng (script, HTTP, TCP, TTL) phù hợp với các service không chạy trên orchestrator quản lý sẵn.

## Trade-offs

- Client-side discovery giảm một hop mạng và cho phép load balancing thông minh hơn ở tầng application (biết được latency/error rate của từng instance), nhưng buộc mọi ngôn ngữ/runtime trong hệ thống phải có thư viện client tương thích với registry — khó cho polyglot stack và khó nâng cấp logic load balancing đồng loạt.
- Server-side discovery (qua load balancer/proxy tập trung) đơn giản hóa client tối đa và tương thích mọi ngôn ngữ, nhưng thêm một network hop và biến load balancer/proxy thành điểm cần scale, cần HA riêng — nếu proxy đó bão hòa, toàn bộ traffic đi qua nó bị ảnh hưởng.
- DNS-based discovery tận dụng được một chuẩn đã có sẵn ở mọi runtime (không cần thư viện riêng), nhưng bị giới hạn bởi DNS caching/TTL ở nhiều tầng (OS resolver, JVM, application), khiến failover chậm hơn so với registry API push-based hoặc watch-based (như etcd watch, Consul blocking query).
- Registry tập trung (Eureka, Consul, ZooKeeper) là single point of dependency: nếu registry down, discovery mới hoàn toàn thất bại — dù các instance hiện tại vẫn có thể tiếp tục phục vụ với danh sách endpoint cache cũ (đây là lý do Eureka theo triết lý AP — ưu tiên trả về dữ liệu cũ còn hơn không trả gì, chấp nhận eventual consistency).

## Best Practices

- Luôn có health check gắn liền với registration — instance chỉ nên được coi là discoverable khi readiness probe pass, không phải ngay khi process khởi động.
- Xử lý graceful shutdown đúng thứ tự: deregister khỏi registry trước, đợi một khoảng grace period cho traffic đang inflight thoát hết, rồi mới tắt process — tránh gửi SIGTERM và tắt kết nối trước khi client ngừng gửi request mới.
- Với DNS-based discovery, kiểm soát rõ TTL và cấu hình caching ở tầng application (đặc biệt JVM cần set `networkaddress.cache.ttl` tường minh), không để mặc định "cache forever".
- Thiết kế client resilient trước discovery stale: kết hợp connection-level health check hoặc circuit breaker ở tầng gọi, không phụ thuộc 100% vào registry luôn đúng tức thời.
- Theo dõi độ trễ giữa "instance thay đổi trạng thái" và "registry phản ánh đúng trạng thái đó" (propagation lag) như một metric riêng — đây là chỉ số trực tiếp phản ánh chất lượng discovery trong production.

## Common Mistakes

- Hardcode IP hoặc dùng file cấu hình tĩnh cho địa chỉ service nội bộ, rồi coi service discovery là "việc sẽ làm sau" — đến khi scale lên vài chục service mới nhận ra chi phí bảo trì đã vượt tầm kiểm soát.
- Không đợi grace period khi shutdown: pod nhận SIGTERM, đóng port ngay lập tức trong khi registry/load balancer chưa kịp loại bỏ nó khỏi danh sách, gây một loạt lỗi 502/connection refused mỗi lần deploy.
- Tin tưởng DNS resolver cache đã tự invalidate đúng theo TTL mà không kiểm chứng, dẫn tới client tiếp tục gọi vào IP cũ hàng phút sau khi Service/Endpoint đã đổi.
- Chỉ test discovery trong điều kiện ổn định (steady state), không test kịch bản registry tạm thời không phản hồi hoặc trả dữ liệu partial — che giấu lỗi tới khi có sự cố thật.
- Để client-side load balancing logic tự chế, không dùng thư viện đã kiểm chứng, dẫn tới các thuật toán chọn instance không tính tới trọng số (weight), health, hay zone-awareness, gây lệch tải giữa các availability zone.

## Interview Questions

**Hỏi**: Sự khác biệt cốt lõi giữa client-side discovery và server-side discovery là gì, và Kubernetes Service thuộc loại nào?

**Trả lời**: Client-side discovery: chính client query registry, tự chọn instance và gọi thẳng. Server-side discovery: client chỉ gọi vào một địa chỉ cố định (load balancer/proxy), chính bên đó mới query registry và forward request. Kubernetes Service (qua kube-proxy/iptables và ClusterIP) là server-side discovery kết hợp DNS-based — client chỉ biết tên DNS/ClusterIP ổn định, không biết registry (Endpoints/EndpointSlice) tồn tại.

**Hỏi**: Vì sao DNS-based service discovery lại có failover chậm hơn so với registry dùng watch/push API như etcd hay Consul blocking query?

**Trả lời**: DNS vốn thiết kế cho caching ở nhiều tầng (OS resolver, thư viện ngôn ngữ, application) dựa trên TTL — client chỉ biết địa chỉ đã đổi khi TTL hết hạn và nó chủ động query lại (pull-based). Trong khi đó etcd watch hay Consul blocking query đẩy thay đổi (push-based) gần như ngay khi registry cập nhật, không phụ thuộc vào một cache TTL trung gian nào mà client không kiểm soát được.

**Hỏi**: Tại sao graceful shutdown lại quan trọng với service discovery, và thứ tự đúng khi một instance tắt là gì?

**Trả lời**: Nếu instance tắt port ngay khi nhận SIGTERM mà chưa deregister, registry/load balancer vẫn còn nghĩ nó sống và tiếp tục forward request vào, gây lỗi connection refused. Thứ tự đúng: deregister khỏi registry (hoặc để readiness probe fail để bị loại khỏi Endpoints) trước, đợi một grace period cho request đang inflight xử lý xong và để traffic mới ngừng được gửi tới, sau đó mới đóng process.

## Summary

Service discovery giải quyết bài toán tìm địa chỉ instance còn sống của một service trong hệ thống mà topology liên tục thay đổi, thông qua registration (self hoặc third-party) và resolution (client-side hoặc server-side), luôn đi kèm health checking. DNS-based discovery tận dụng chuẩn có sẵn nhưng bị giới hạn bởi caching/TTL; client-side discovery cho load balancing thông minh hơn nhưng đòi hỏi thư viện riêng cho từng runtime; server-side discovery đơn giản cho client nhưng thêm một hop và một điểm cần scale riêng. Kubernetes minh họa mô hình server-side + DNS-based với kube-proxy và CoreDNS, còn service mesh (Istio/Linkerd) đẩy thêm client-side load balancing thông minh xuống tầng sidecar mà application không cần biết. Trade-off cốt lõi luôn xoay quanh tốc độ phản ánh thay đổi (propagation lag) đổi lấy độ đơn giản và khả năng tương thích đa ngôn ngữ.

## Knowledge Graph

- Leader election — cùng thường dựa vào các coordination service registry như ZooKeeper/etcd/Consul để lưu trạng thái.
- Load balancing — bước ngay sau resolution trong client-side discovery, hoặc được thực hiện bởi chính tầng server-side discovery.
- Health check / heartbeat — điều kiện tiên quyết để registry chỉ trả về instance thực sự sẵn sàng.
- Service mesh (sidecar proxy) — mô hình hiện đại đẩy cả discovery lẫn load balancing xuống tầng hạ tầng, tách khỏi application code.
- CAP theorem — giải thích lựa chọn AP của Eureka (chấp nhận dữ liệu cũ) khi registry mất kết nối một phần.
- DNS caching / TTL — nguyên nhân kỹ thuật chính gây độ trễ trong DNS-based discovery.

## Five Things To Remember

- Service discovery gồm hai nửa: registration (đăng ký địa chỉ) và resolution (tra cứu địa chỉ), thiếu health check thì cả hai đều vô nghĩa.
- Client-side discovery gọi thẳng sau khi tự tra registry; server-side discovery luôn đi qua một load balancer/proxy trung gian.
- DNS-based discovery đơn giản và phổ quát nhưng luôn có độ trễ do TTL/caching, không phản ánh thay đổi tức thời.
- Graceful shutdown (deregister trước, tắt sau) là điều kiện bắt buộc để tránh lỗi mỗi lần deploy.
- Kubernetes Service = server-side discovery; Eureka + Ribbon = client-side discovery; đây là hai mô hình kinh điển cần phân biệt được.
