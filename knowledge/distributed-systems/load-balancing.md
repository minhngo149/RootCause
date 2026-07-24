---
id: load-balancing
title: "Load Balancing Strategies"
tags: ["distributed-systems"]
---

# Load Balancing Strategies

> Status: Draft

## Problem

Một service chạy nhiều instance để chịu tải và chịu lỗi, nhưng nếu client hoặc DNS chỉ trỏ ngẫu nhiên/tuần tự vào từng instance mà không biết instance nào đang khỏe, đang quá tải, hay đã chết, hệ thống sẽ phân phối traffic lệch tay: một vài instance nhận gấp đôi-gấp ba tải trong khi số khác gần như rảnh. Ví dụ cụ thể: một cluster 10 pod backend đứng sau DNS round-robin thuần (không health check), khi 2 pod bị OOMKilled và đang restart, DNS vẫn trả về IP của chúng cho ~20% client trong suốt thời gian pod chưa ready, khiến 20% request timeout dù cluster tổng thể còn dư capacity.

## Pain Points

- Traffic dồn lệch (hot instance) khiến một vài node bị đánh sập bởi tải vượt ngưỡng trong khi node khác vẫn rảnh, làm giảm hiệu quả sử dụng tài nguyên đã trả tiền cho toàn cluster.
- Không loại bỏ instance chết/không khỏe khỏi vòng lặp phân phối khiến một tỷ lệ request cố định luôn bị timeout hoặc lỗi 502, ngay cả khi cluster về tổng thể còn đủ capacity phục vụ.
- Thuật toán load balancing sai với đặc tính traffic (ví dụ dùng round-robin cho các request có độ dài xử lý chênh lệch lớn) làm latency đuôi (p99) tăng vọt dù latency trung bình vẫn thấp.
- Sai lựa chọn giữa L4 và L7 khiến load balancer hoặc không thấy được nội dung request để route thông minh (chọn L4 khi cần L7), hoặc tốn CPU/latency giải mã TLS và parse HTTP không cần thiết (chọn L7 khi chỉ cần forward TCP thuần).

## Solution

Load balancing là việc phân phối request tới nhiều instance backend theo một thuật toán xác định, đồng thời loại bỏ động các instance không khỏe khỏi vòng phân phối (health checking). Có hai trục quyết định độc lập: **thuật toán chọn instance** (round robin, least connections, consistent hashing...) quyết định *request nào đi tới instance nào*, và **tầng mạng vận hành** (L4 hay L7) quyết định *load balancer nhìn thấy gì trong request để ra quyết định đó*. Hai trục này kết hợp với nhau để tạo ra chiến lược phù hợp với từng loại traffic — traffic HTTP stateless đồng nhất cần khác với traffic WebSocket dài hạn hay traffic cần route theo tenant.

## How It Works

**Round Robin**: load balancer duy trì một con trỏ (index) chạy tuần tự qua danh sách instance, request thứ N đi tới instance `N mod số_instance`. Biến thể **Weighted Round Robin** gán trọng số cho từng instance (ví dụ instance mạnh gấp đôi nhận trọng số 2, nhận gấp đôi số request trong mỗi chu kỳ) để phù hợp với cluster heterogeneous (instance có cấu hình phần cứng khác nhau). Nhược điểm cốt lõi: round robin không biết instance nào đang xử lý request nặng hay nhẹ — nếu một request trước đó mất 5 giây để xử lý còn request khác chỉ mất 5ms, round robin vẫn đối xử công bằng về *số lượng* request chứ không công bằng về *tải thực tế*.

**Least Connections**: load balancer theo dõi số connection đang mở (in-flight request) tới mỗi instance theo thời gian thực, và route request mới tới instance có số connection đang mở thấp nhất. Biến thể **Weighted Least Connections** chia thêm cho trọng số capacity của instance (`connections / weight`) để so sánh công bằng giữa các instance có cấu hình khác nhau. Thuật toán này xử lý tốt trường hợp thời gian xử lý request không đồng đều (long-lived connection như WebSocket, hay request có độ phức tạp khác nhau như query báo cáo so với query đơn giản), vì nó phản ánh tải *thực tế đang diễn ra* thay vì chỉ đếm request đã gửi. Cái giá phải trả: load balancer cần trạng thái (số connection hiện tại của từng instance), phức tạp hơn round robin vốn stateless, và trong kiến trúc nhiều load balancer song song (không có shared state), mỗi LB chỉ biết connection nó tự mở, có thể route lệch nếu không đồng bộ.

**Consistent Hashing**: dùng một hàm hash (thường MD5 hoặc SHA-1 cho tốc độ và phân bố đều) băm một key ổn định của request (session ID, user ID, cache key) thành một điểm trên một vòng tròn hash không gian cố định (ví dụ 0 đến 2^32-1). Mỗi instance backend cũng được băm thành một hoặc nhiều điểm trên cùng vòng tròn đó (thường dùng **virtual nodes** — mỗi instance vật lý ánh xạ thành 100-300 điểm ảo để phân bố đều hơn, tránh trường hợp một instance vô tình chiếm một cung quá lớn của vòng tròn). Request được route tới instance mà điểm hash của nó là điểm gần nhất theo chiều kim đồng hồ trên vòng tròn. Điểm mấu chốt: khi thêm hoặc bớt một instance, chỉ các key nằm trong cung ảnh hưởng trực tiếp của instance đó bị route lại (trung bình `K/N` key dịch chuyển với `K` là tổng số key, `N` là số instance), thay vì toàn bộ `K` key bị đảo lộn như khi dùng `hash(key) mod N` thuần (vì `N` thay đổi làm phép mod đổi kết quả cho gần như mọi key).

**L4 (Transport Layer) Load Balancing**: hoạt động ở tầng TCP/UDP, load balancer chỉ nhìn thấy IP nguồn/đích và port, không giải mã hay đọc nội dung payload (kể cả khi payload là HTTP hay TLS-encrypted). Nó forward packet dựa trên connection tuple (source IP, source port, dest IP, dest port), giữ nguyên hoặc NAT lại địa chỉ, và duy trì một connection table ánh xạ session tới backend đã chọn trong suốt vòng đời TCP connection. Vì không cần parse nội dung, L4 LB xử lý được throughput rất cao với latency cực thấp (thường chỉ vài chục microsecond overhead), phù hợp cho traffic không cần route theo nội dung (database traffic, traffic đã terminate TLS ở tầng khác).

**L7 (Application Layer) Load Balancing**: hoạt động ở tầng HTTP/gRPC, load balancer terminate connection từ client, đọc và parse toàn bộ header + có thể cả body, rồi mở một connection riêng biệt tới backend đã chọn (nghĩa là có 2 TCP connection riêng: client-to-LB và LB-to-backend). Vì thấy được nội dung request, L7 LB route được theo path (`/api/v1` vs `/api/v2`), theo header (route theo `tenant-id`, theo version của mobile app qua `User-Agent`), làm được retry ở tầng LB khi backend trả lỗi, terminate TLS tập trung, và áp dụng rate limiting/circuit breaking theo route cụ thể. Cái giá: mỗi request tốn thêm CPU để parse HTTP và (nếu terminate TLS) giải mã/mã hóa lại, và độ trễ thêm một round-trip thiết lập connection riêng tới backend.

## Production Architecture

Trong một kiến trúc web điển hình, traffic đi qua nhiều tầng load balancing xếp chồng: ở biên (edge), một L4 LB (AWS NLB, hoặc phần cứng như F5) nhận traffic thô từ internet và forward tới một layer L7 LB/API Gateway (AWS ALB, Nginx, Envoy) — vì L4 xử lý được lưu lượng lớn với chi phí thấp trước khi lọc bớt bằng L7 để route thông minh theo path/header. L7 LB sau đó phân phối request tới các pod ứng dụng, thường dùng round robin hoặc least connections tùy cấu hình Ingress Controller (Nginx Ingress mặc định round robin, có thể đổi sang `least_conn` qua annotation). Với hệ thống có cache phân tán (Redis Cluster, Memcached) hoặc cần sticky session (giỏ hàng, WebSocket chat), tầng route vào các node cache/session dùng consistent hashing — ví dụ client library của Memcached (`libketama`) băm cache key qua consistent hashing để đảm bảo cùng một key luôn đi tới cùng node dù các node khác trong cluster có thêm/bớt. Trong service mesh (Istio/Envoy), load balancing xảy ra ở tầng sidecar proxy giữa các service nội bộ, hỗ trợ nhiều thuật toán (round robin, least request, ring hash — biến thể consistent hashing) cấu hình per-route qua `DestinationRule`, cho phép mỗi service chọn thuật toán phù hợp với đặc tính traffic riêng của nó thay vì dùng một chính sách chung toàn hệ thống.

## Trade-offs

Round robin đơn giản, không trạng thái, dễ scale ngang chính load balancer, nhưng bỏ qua tải thực tế của từng instance — không phù hợp khi thời gian xử lý request chênh lệch lớn. Least connections phản ánh tải thực tế tốt hơn nhưng đòi hỏi load balancer duy trì trạng thái theo thời gian thực, tốn thêm bộ nhớ/CPU để track, và trong kiến trúc nhiều LB song song không chia sẻ state, mỗi LB chỉ có view cục bộ nên quyết định có thể không tối ưu toàn cục. Consistent hashing giải quyết tốt bài toán cache locality và giảm thiểu resharding khi cluster thay đổi kích thước, nhưng phân phối tải không hoàn toàn đều (một số instance có thể nhận nhiều hơn nếu virtual node phân bố không đủ dày), và không phù hợp cho traffic cần cân bằng tải tuyệt đối vì thuật toán ưu tiên tính ổn định của ánh xạ hơn là cân bằng tức thời. L4 nhanh và rẻ về tài nguyên nhưng "mù" về nội dung — không route được theo logic nghiệp vụ và không tự retry được khi backend lỗi ở tầng ứng dụng (chỉ biết TCP connection còn sống hay không). L7 linh hoạt và thông minh hơn nhiều nhưng tốn CPU hơn (đặc biệt nếu terminate TLS), thêm một hop network (double connection: client-LB và LB-backend) làm tăng latency, và bản thân L7 LB có thể trở thành điểm nghẽn throughput nếu không scale đúng cách.

## Best Practices

- Chọn thuật toán theo đặc tính traffic thực tế: round robin cho request đồng nhất về thời gian xử lý, least connections cho traffic có độ dài xử lý chênh lệch (long-lived connection, query nặng-nhẹ khác nhau), consistent hashing khi cần cache locality hoặc sticky routing.
- Luôn gắn health check chủ động (active health check, không chỉ dựa vào lỗi request thực tế) để loại instance chết khỏi vòng phân phối trước khi nó nhận traffic mới, tránh độ trễ phát hiện qua request thất bại.
- Dùng virtual node đủ dày (100+ điểm ảo mỗi instance vật lý) khi triển khai consistent hashing để tránh phân phối lệch do phân bố ngẫu nhiên không đều trên vòng hash.
- Xếp lớp L4 trước L7 ở quy mô lớn — dùng L4 LB ở biên để chịu throughput cao chi phí thấp, chỉ đưa vào L7 khi thực sự cần route theo nội dung request.
- Giám sát phân phối tải thực tế giữa các instance (request count, CPU, connection count per instance) để phát hiện sớm hiện tượng hot instance, không chỉ tin vào giả định "thuật toán X sẽ tự cân bằng".

## Common Mistakes

- Dùng DNS round-robin thuần làm cơ chế load balancing chính mà không có health check, khiến client vẫn nhận IP của instance đã chết trong suốt TTL cache DNS.
- Dùng `hash(key) mod N` thay vì consistent hashing thực sự cho cache/session routing, khiến gần như toàn bộ key bị route lại (cache miss hàng loạt) mỗi khi thêm/bớt một node.
- Chọn least connections nhưng chạy nhiều instance load balancer không chia sẻ state, khiến mỗi LB "least connections" theo view cục bộ của riêng nó, dẫn tới phân phối không tối ưu như kỳ vọng.
- Terminate TLS ở tầng L7 LB cho traffic cực lớn mà không tính đến chi phí CPU của TLS handshake/decrypt, gây nghẽn cổ chai ở chính load balancer thay vì ở backend.
- Áp dụng cùng một thuật toán load balancing cho mọi loại traffic trong hệ thống (ví dụ dùng round robin cho cả traffic API ngắn lẫn traffic WebSocket dài hạn) thay vì tách theo route/service với đặc tính riêng.

## Interview Questions

**Hỏi**: Vì sao consistent hashing giảm thiểu được số key bị route lại khi thêm/bớt node, so với `hash(key) mod N`?

**Trả lời**: Với `hash(key) mod N`, khi `N` thay đổi (thêm/bớt node), gần như mọi giá trị hash cho ra kết quả mod khác trước, khiến gần như toàn bộ key bị ánh xạ lại. Consistent hashing đặt cả key và node lên cùng một vòng hash cố định — khi thêm/bớt một node, chỉ các key nằm trong cung ảnh hưởng trực tiếp của node đó (trung bình K/N key) bị dịch chuyển, các key còn lại vẫn giữ nguyên ánh xạ tới node cũ.

**Hỏi**: Khi nào nên dùng L4 load balancing thay vì L7, và ngược lại?

**Trả lời**: Dùng L4 khi cần throughput cực cao với overhead thấp và không cần route theo nội dung request — ví dụ traffic database, hoặc làm tầng biên trước khi vào L7 LB. Dùng L7 khi cần route theo path/header/nội dung request (API versioning, routing theo tenant), cần retry hoặc circuit breaking ở tầng LB, hoặc cần terminate TLS tập trung — đổi lại chấp nhận thêm CPU và một hop network.

**Hỏi**: Tại sao least connections có thể route không tối ưu trong kiến trúc có nhiều load balancer chạy song song?

**Trả lời**: Vì mỗi load balancer chỉ biết số connection nó tự mở tới từng backend, không biết connection mà các LB khác đã mở. Nếu không có shared state (ví dụ qua một service registry trung tâm hoặc cơ chế đồng bộ), một backend có thể đang nhận tải cao từ LB khác nhưng vẫn được một LB thứ ba coi là "ít connection nhất" và tiếp tục route thêm request vào.

## Summary

Load balancing gồm hai quyết định độc lập: thuật toán chọn instance (round robin cho traffic đồng nhất, least connections cho traffic tải không đều, consistent hashing cho nhu cầu cache locality/sticky routing) và tầng mạng vận hành (L4 nhanh nhưng mù nội dung, L7 thông minh nhưng tốn tài nguyên hơn). Health check chủ động là điều kiện bắt buộc để bất kỳ thuật toán nào hoạt động đúng, vì thuật toán chỉ tối ưu việc phân phối giữa các instance được coi là khỏe. Kiến trúc production thực tế thường xếp lớp nhiều tầng load balancing (L4 ở biên, L7 ở gateway, consistent hashing ở tầng cache/session) thay vì chọn một giải pháp duy nhất cho toàn hệ thống. Sai lầm phổ biến nhất là dùng một thuật toán/tầng cho mọi loại traffic mà không xét đặc tính riêng của từng loại, hoặc thiếu shared state giữa các load balancer khiến quyết định "cân bằng" chỉ đúng cục bộ.

## Knowledge Graph

- Consistent Hashing — nền tảng của cả sharding dữ liệu (distributed cache, distributed database) lẫn load balancing khi cần ổn định ánh xạ key-to-node.
- Circuit Breaker — thường triển khai ngay tại tầng L7 load balancer/proxy để ngắt route tới backend đang lỗi hàng loạt.
- Service Mesh (Istio/Linkerd) — cung cấp load balancing L7 ở tầng sidecar proxy giữa các service nội bộ, tách biệt khỏi code ứng dụng.
- Health Check / Liveness-Readiness Probe — cơ chế xác định instance nào đủ điều kiện nhận traffic, điều kiện tiên quyết cho mọi thuật toán load balancing.
- Sharding — chia dữ liệu theo key tương tự cách consistent hashing chia traffic theo key, hai bài toán dùng chung kỹ thuật hash nền tảng.
- Rate Limiting — thường triển khai cùng tầng với L7 load balancer, kiểm soát traffic đi vào thay vì chỉ phân phối traffic đã được chấp nhận.

## Five Things To Remember

- Round robin không biết tải thực tế của instance, least connections thì có nhưng cần trạng thái.
- Consistent hashing chỉ dịch chuyển trung bình K/N key khi cluster đổi kích thước, thay vì đảo lộn toàn bộ như `hash mod N`.
- L4 nhanh và rẻ nhưng mù nội dung request; L7 thông minh nhưng tốn CPU và thêm một hop network.
- Không có health check chủ động, mọi thuật toán load balancing đều vô nghĩa vì vẫn route tới instance đã chết.
- Production thực tế luôn xếp lớp nhiều tầng load balancing, không dùng một giải pháp duy nhất cho toàn hệ thống.
</content>
