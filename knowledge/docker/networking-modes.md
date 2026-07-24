---
id: networking-modes
title: Docker Networking Modes
tags: ["docker"]
---

# Docker Networking Modes

> Status: Draft

## Problem

Một container không tự nhiên "biết" cách nói chuyện với container khác, với host, hay với mạng ngoài — tất cả phụ thuộc vào network mode được chọn lúc `docker run`/`docker-compose up`. Engineer thường mặc định dùng bridge network mà không hiểu nó tạo ra một NAT layer, hoặc chuyển sang `host` mode để "cho nhanh" mà không lường trước hệ quả bảo mật, hoặc triển khai multi-container trên nhiều máy vật lý mà vẫn dùng bridge network mặc định — thứ vốn chỉ hoạt động trong phạm vi một Docker host. Kết quả là service không kết nối được nhau, port bị đá (conflict), hoặc traffic đi vòng qua public internet một cách không cần thiết.

## Pain Points

- Container trong bridge network mặc định (`docker0`) không resolve được tên nhau qua DNS nếu không tạo user-defined network, buộc engineer hardcode IP nội bộ — IP này đổi mỗi lần container restart, gây lỗi kết nối ngẫu nhiên sau deploy.
- Dùng `host` network cho một service public-facing (vd. API Gateway) vô tình bỏ qua toàn bộ cách ly network namespace, khiến container có thể bind vào bất kỳ port nào của host và xung đột với process khác, đồng thời mở rộng bề mặt tấn công vì container thấy toàn bộ network interface của host.
- Triển khai Swarm/multi-host mà vẫn nghĩ theo tư duy bridge network single-host dẫn đến container trên node A không gọi được service trên node B qua tên service, phải lộ port ra ngoài và tự dựng reverse proxy thủ công — tăng độ phức tạp vận hành không cần thiết.
- NAT overhead của bridge network (mỗi packet đi qua iptables DNAT/SNAT) làm tăng latency đáng kể với workload có traffic nội bộ lớn (vd. service mesh gọi nhau hàng chục nghìn request/giây), nhưng thường không bị phát hiện cho đến khi profiling ở tải cao.

## Solution

Docker cung cấp năm network driver cốt lõi: `bridge` (mặc định, cô lập theo host, có NAT), `host` (dùng chung network namespace với host, không cô lập, không NAT), `none` (không có network interface ngoài loopback), `overlay` (mạng ảo trải rộng nhiều Docker host, dùng cho Swarm/cluster), và `macvlan` (gán MAC address riêng cho container, xuất hiện như một thiết bị vật lý trên mạng LAN). Chọn đúng driver theo yêu cầu cô lập, hiệu năng, và phạm vi triển khai (single-host hay multi-host) là quyết định kiến trúc, không phải chi tiết cấu hình phụ.

## How It Works

Mỗi container chạy trong một network namespace Linux riêng — một cơ chế kernel cô lập network stack (interface, routing table, iptables rules, port) khỏi host và khỏi container khác. Bridge network tạo một virtual switch (Linux bridge, mặc định `docker0`) trên host; mỗi container nhận một cặp veth (virtual ethernet) — một đầu gắn vào container, đầu kia gắn vào bridge — và một IP nội bộ trong subnet riêng (vd. `172.17.0.0/16`). Traffic ra ngoài đi qua iptables MASQUERADE (SNAT) để dùng IP host, traffic vào container qua published port đi qua DNAT — đây chính là nguồn gốc overhead NAT. User-defined bridge network (`docker network create`) khác bridge mặc định ở chỗ nó chạy kèm một embedded DNS server (127.0.0.11 trong container), cho phép container resolve tên nhau trực tiếp thay vì phải link thủ công.

Host mode bỏ hoàn toàn network namespace riêng — container dùng chung namespace với host, nghĩa là container thấy và bind trực tiếp vào interface, IP, port của host, không qua NAT, không qua veth. Latency thấp nhất trong các mode vì không có lớp trung gian, nhưng cũng không có cô lập port: container A và B (hoặc B và chính host) tranh nhau một port sẽ conflict ngay ở tầng OS.

None mode chỉ tạo loopback interface (`lo`), không gắn vào bridge, không có route ra ngoài — container hoàn toàn cô lập về mạng, chỉ dùng khi container không cần network (batch job xử lý file cục bộ, hoặc cấu hình network hoàn toàn thủ công qua tool bên ngoài).

Overlay network giải quyết bài toán multi-host bằng cách dựng một mạng ảo Layer 2/3 trải trên nhiều Docker host, dùng VXLAN (Virtual Extensible LAN) để encapsulate packet Ethernet gốc vào UDP packet (port 4789 mặc định), cho phép hai container trên hai máy vật lý khác nhau nói chuyện như thể cùng một Layer 2 segment. Docker Swarm dùng overlay network kết hợp với một control plane phân tán (dựa trên Gossip protocol, cụ thể là thư viện `serf`) để đồng bộ network state — mapping container-to-IP, service discovery — giữa các node mà không cần một registry trung tâm duy nhất. Swarm cũng tự động cấu hình một built-in load balancer ở tầng IPVS (IP Virtual Server, trong kernel) cho mỗi service, phân phối traffic đến các task/container instance mà không cần reverse proxy riêng.

## Production Architecture

Trong một hệ thống microservices chạy Docker Swarm trên 5-10 node vật lý/VM, mỗi service (order-service, payment-service, inventory-service) được attach vào cùng một overlay network `backend-net`, cho phép chúng gọi nhau qua tên service (`http://payment-service:8080`) bất kể task đang chạy trên node nào — Swarm tự route traffic qua VXLAN tunnel đến đúng node vật lý đang giữ container đó. Riêng các service cần hiệu năng cực cao và ổn định (vd. một sidecar Envoy proxy xử lý hàng chục nghìn request/giây trong service mesh, hoặc một load-testing tool cần đo latency chính xác) chạy `host` mode để loại bỏ overhead NAT/veth. Ngược lại, các job nền không cần expose gì ra ngoài (vd. worker xử lý ảnh từ volume mount, không gọi network) chạy `none` mode để giảm bề mặt tấn công. Database container (Postgres, Redis) thường nằm trong một bridge network riêng (`db-net`), tách biệt khỏi `frontend-net` public-facing, để service tầng web không thể truy cập trực tiếp DB nếu không qua API tầng giữa — network segmentation theo tầng kiến trúc, không chỉ theo host.

## Trade-offs

Bridge network (kể cả user-defined) luôn trả giá bằng NAT overhead và một tầng gián tiếp qua bridge/veth, đổi lấy cô lập tốt và dễ quản lý port mapping — hợp lý cho phần lớn service thông thường nhưng không tối ưu cho workload latency-sensitive. Host mode nhanh nhất nhưng từ bỏ hoàn toàn cô lập network, tăng rủi ro bảo mật (container thấy toàn bộ interface host) và rủi ro vận hành (port conflict khó debug vì không còn namespace riêng để soi). Overlay network cho phép mở rộng ngang qua nhiều host — thứ bridge network không làm được — nhưng thêm một lớp encapsulation VXLAN (tăng latency và giảm MTU hiệu dụng do overhead header, thường cần chỉnh MTU xuống ~1450 bytes thay vì 1500), đồng thời đòi hỏi mở thêm port UDP 4789 và TCP/UDP 7946 giữa các node — một yêu cầu firewall dễ bị bỏ sót khi dựng cluster trên nhiều mạng con hoặc qua cloud provider khác nhau.

## Best Practices

- Luôn tạo user-defined bridge network thay vì dùng bridge mặc định — có DNS resolution theo tên service, dễ cô lập theo nhóm chức năng (frontend-net, backend-net, db-net).
- Chỉ dùng `host` mode khi thực sự đo được NAT overhead ảnh hưởng đến latency/throughput, không dùng mặc định vì "tiện"; nếu dùng, phải kiểm soát chặt port binding để tránh conflict.
- Với Swarm/multi-host, dùng overlay network cho mọi service cần giao tiếp cross-node, và kiểm tra MTU thực tế (`ping -M do -s <size>`) sau khi bật overlay để tránh packet bị fragment âm thầm.
- Segment network theo tầng bảo mật (public-facing, internal API, database) thay vì gộp tất cả container vào một network phẳng duy nhất.
- Mở đúng và chỉ đúng các port cần thiết cho overlay/control-plane traffic giữa các node Swarm (TCP/UDP 7946 cho gossip, UDP 4789 cho VXLAN, TCP 2377 cho manager), không mở rộng hơn mức cần thiết.

## Common Mistakes

- Dùng bridge network mặc định (`docker0`) rồi hardcode IP container trong config, IP này thay đổi sau mỗi lần container bị recreate, gây lỗi kết nối "ngẫu nhiên" sau deploy.
- Chuyển sang `host` mode để "fix" một vấn đề kết nối mà không hiểu nguyên nhân gốc (thường là thiếu publish port hoặc sai tên service), mang theo rủi ro bảo mật không cần thiết.
- Triển khai Swarm nhiều node nhưng vẫn dùng network mode `bridge` cho service cần giao tiếp cross-node, khiến các container chỉ gọi được nhau khi tình cờ cùng một node.
- Không tính đến overhead MTU của overlay network, dẫn đến packet lớn bị fragment hoặc drop âm thầm, biểu hiện như lỗi timeout ngắt quãng khó tái hiện.
- Gộp tất cả service (kể cả database) vào một network phẳng duy nhất, mất khả năng giới hạn truy cập theo tầng khi có service bị compromise.

## Interview Questions

**Hỏi**: Vì sao container trong cùng bridge network mặc định của Docker không resolve được tên nhau, còn user-defined bridge network thì có?

**Trả lời**: Bridge mặc định (`docker0`) không chạy embedded DNS server, chỉ cấp IP qua DHCP nội bộ, nên container phải biết IP nhau hoặc dùng `--link` (đã deprecated). User-defined bridge network chạy kèm DNS server nội bộ (127.0.0.11) tự động đăng ký tên container/service, cho phép resolve theo tên ngay khi container join network.

**Hỏi**: Overlay network giải quyết bài toán gì mà bridge network không giải quyết được, và bằng cơ chế nào?

**Trả lời**: Overlay network cho phép container trên các Docker host vật lý khác nhau giao tiếp như thể cùng một Layer 2 segment, trong khi bridge network chỉ hoạt động trong phạm vi một host. Cơ chế là VXLAN — encapsulate frame Ethernet gốc vào UDP packet để tunnel qua mạng underlay (thường là mạng IP giữa các node), kết hợp với một control plane phân tán (gossip protocol) để đồng bộ thông tin container-to-node.

**Hỏi**: Khi nào nên dùng `host` network mode thay vì bridge, và rủi ro chính là gì?

**Trả lời**: Dùng khi cần loại bỏ overhead NAT/veth cho workload cực nhạy với latency hoặc cần xử lý số lượng connection rất lớn (vd. proxy hiệu năng cao, tool đo network chính xác). Rủi ro chính là mất hoàn toàn cô lập network namespace — container thấy mọi interface của host và có thể xung đột port trực tiếp với host hoặc container khác cũng chạy host mode.

## Summary

Docker network mode quyết định cách container giao tiếp với nhau, với host, và với mạng ngoài, thông qua network namespace của Linux kernel. Bridge network (nên dùng bản user-defined) là lựa chọn mặc định hợp lý cho phần lớn service single-host nhờ cô lập tốt và DNS resolution theo tên, đổi lại chịu overhead NAT. Host mode bỏ cô lập để đổi lấy hiệu năng tối đa, còn none mode cô lập hoàn toàn cho workload không cần mạng. Overlay network, dùng VXLAN và control plane phân tán, là lựa chọn bắt buộc khi triển khai multi-host qua Docker Swarm, cho phép service gọi nhau qua tên bất kể chạy trên node vật lý nào. Chọn sai network mode không gây lỗi ngay lập tức mà thường biểu hiện dưới dạng latency bất thường, port conflict, hoặc lỗi kết nối chỉ xuất hiện khi scale ra nhiều node.

## Knowledge Graph

- Service Discovery — overlay network của Swarm dựa trên cùng nguyên lý service discovery phân tán để container tìm nhau qua tên.
- Gossip Protocol — control plane của Swarm overlay network dùng gossip để đồng bộ trạng thái network giữa các node.
- Load Balancing — Swarm dùng IPVS (kernel-level load balancer) tích hợp sẵn cho traffic giữa các task của một service qua overlay network.
- CAP Theorem — control plane phân tán của Swarm phải đánh đổi giữa tính nhất quán và khả năng chịu lỗi khi đồng bộ network state giữa nhiều node.
- Circuit Breaker — khi latency overlay network tăng bất thường (vd. do MTU fragment), circuit breaker ở tầng ứng dụng là lớp phòng vệ bổ sung.

## Five Things To Remember

- Bridge network là mặc định hợp lý cho single-host, nhưng luôn dùng bản user-defined để có DNS resolution theo tên.
- Host mode nhanh nhất vì bỏ NAT/veth, nhưng cũng mất toàn bộ cô lập network namespace.
- None mode chỉ có loopback, dùng cho workload thực sự không cần mạng.
- Overlay network dùng VXLAN để nối container qua nhiều Docker host, là nền tảng bắt buộc cho Swarm/multi-host.
- Luôn kiểm tra MTU và mở đúng port control-plane (7946, 4789, 2377) khi triển khai overlay network trên cluster thật.
