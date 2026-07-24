---
id: split-brain
title: Split Brain
tags: ["distributed-systems", "production-incident"]
---

# Split Brain

> Status: Draft

## Problem

Một cluster PostgreSQL 3 node (1 primary, 2 replica) chạy trên hai rack khác nhau trong cùng một datacenter, dùng một tool tự động failover (kiểu Patroni/repmgr) để bầu primary mới khi primary cũ chết. Một hôm switch nối giữa hai rack bị lỗi firmware và rớt gói tin gián đoạn — primary cũ (rack A) vẫn sống, vẫn nhận ghi từ một phần traffic, nhưng không còn thấy được 2 replica ở rack B. Hai replica ở rack B, không còn heartbeat từ primary, tự bầu ra một primary mới và bắt đầu nhận ghi từ phần traffic còn lại. Trong vài phút mạng đứt, cả hai node đều tin mình là primary hợp lệ, cả hai đều nhận ghi độc lập — đây chính là split-brain, và hậu quả không dừng lại khi mạng nối lại: hai tập dữ liệu đã phân kỳ và không có cách nào merge tự động mà không mất mát hoặc xung đột.

## Pain Points

- Ghi trùng/ghi xung đột trên cùng một khóa chính (ví dụ hai đơn hàng khác nhau được gán cùng một `order_id` do hai sequence độc lập ở hai primary), gây lỗi constraint hoặc mất dữ liệu khi hệ thống cố merge.
- Double-spend hoặc double-booking nghiệp vụ nghiêm trọng: hai primary cùng cho phép trừ tồn kho, cùng cho phép rút tiền từ cùng một số dư, vì mỗi bên chỉ thấy trạng thái cục bộ của chính nó.
- Khi mạng khôi phục, không có cơ chế tự động nào xác định "phiên bản nào đúng" — đội vận hành phải chọn thủ công giữa việc bỏ dữ liệu của một phía (data loss) hoặc merge tay từng bản ghi xung đột, tốn hàng giờ đến hàng ngày tùy quy mô.
- Chi phí incident tăng gấp nhiều lần so với một outage thông thường, vì split-brain không chỉ là downtime — nó để lại dữ liệu sai đã được xác nhận với khách hàng (đơn hàng đã lên hóa đơn, giao dịch đã báo thành công) mà không thể âm thầm rollback.

## Solution

Split-brain xảy ra khi cơ chế bầu leader/primary của một cluster không có (hoặc mất) khái niệm **quorum** — tức không yêu cầu một phía phải chiếm đa số tuyệt đối (majority) số node trước khi được phép tự xưng là leader. Giải pháp cốt lõi là bắt buộc mọi quyết định "tôi là leader" phải được xác nhận bởi quá bán tổng số node trong cluster (ví dụ 2/3, 3/5), không phải quá bán số node còn thấy được. Khi partition xảy ra, phía nào không hội đủ majority — kể cả khi nó có đông node hơn phía kia trong khoảnh khắc đó — buộc phải từ chối làm leader hoặc từ chối ghi, chấp nhận downtime cục bộ để đổi lấy việc không bao giờ có hai leader cùng tồn tại.

## How It Works

Cơ chế quorum dựa trên một tính chất toán học đơn giản nhưng chắc chắn: trong một cluster N node, không thể có hai nhóm con rời nhau mà cả hai đều chiếm quá bán N. Nếu cluster có 5 node và majority được định nghĩa là ≥3, thì khi partition chia cluster thành nhóm 3 và nhóm 2, chỉ nhóm 3 đủ điều kiện bầu hoặc giữ leader; nhóm 2 tự biết mình là minority và phải fail closed (từ chối ghi, có khi cả đọc). Các thuật toán đồng thuận như Raft và Paxos hiện thực điều này bằng cách yêu cầu: (1) một node muốn trở thành leader phải nhận được phiếu bầu (vote) từ majority trong một election term có số hiệu tăng dần (term number), và (2) một write chỉ được coi là committed khi đã được replicate và xác nhận bởi majority. Term number tăng dần là chốt chặn quan trọng: nếu leader cũ (rack A ở ví dụ trên) bị cô lập và không thể liên lạc với majority, nó không thể tăng term, trong khi phía majority bên kia bầu leader mới với term cao hơn; khi mạng nối lại, mọi node đều so sánh term và tự động chấp nhận leader có term cao hơn, buộc leader cũ phải step down (self-fencing) và tự chuyển thành replica, hủy bỏ mọi thay đổi cục bộ nó nhận trong lúc bị cô lập. Một hệ thống không quorum-based (ví dụ dùng heartbeat timeout đơn giản kiểu "không thấy leader trong X giây thì tôi tự làm leader" mà không kiểm tra số phiếu majority) chính là công thức trực tiếp dẫn tới split-brain, vì cả hai phía của một partition đối xứng đều thỏa điều kiện "không thấy leader" cùng lúc.

## Production Architecture

etcd và ZooKeeper — nền tảng lưu trạng thái cho Kubernetes control plane, Kafka (qua KRaft hoặc ZooKeeper cũ) — dùng Raft/ZAB với majority quorum bắt buộc, nên một network partition khiến cluster etcd 5 node chia 3-2 sẽ chỉ có phía 3 node tiếp tục hoạt động, phía 2 node tự treo request write cho tới khi mạng khôi phục; đây là lý do etcd luôn được triển khai với số node lẻ (3, 5, 7) — số chẵn có thể tạo ra partition 50/50 mà không bên nào có majority, khiến toàn cluster đứng yên thay vì chỉ một bên đứng yên. PostgreSQL với Patroni dùng chính etcd/Consul làm nơi lưu leader lock có TTL (distributed consensus store) thay vì để các node PostgreSQL tự thỏa thuận với nhau qua heartbeat trực tiếp — bản thân Patroni không tự implement quorum, nó mượn quorum từ etcd. Trong kiến trúc MySQL Group Replication hoặc Galera Cluster, cấu hình mặc định yêu cầu majority (`pc.weight`, `wsrep_cluster_size`) trước khi một node được ghi, và khuyến nghị luôn deploy số node lẻ hoặc thêm một **witness/arbiter node** không lưu dữ liệu chỉ để phá thế hòa khi số node vật lý là chẵn (ví dụ 2 node dữ liệu + 1 arbiter để luôn có 3 phiếu). Trong triển khai đa datacenter (multi-region), split-brain nguy hiểm hơn nhiều vì network partition giữa hai region có thể kéo dài hàng giờ (đứt cáp quang biển, lỗi BGP) chứ không phải vài giây như trong một datacenter, nên các hệ thống này thường đặt majority quorum lệch có chủ đích (ví dụ 2 node ở region chính, 1 ở region phụ) để đảm bảo region chính luôn thắng trong mọi kịch bản partition.

## Trade-offs

Yêu cầu majority quorum nghĩa là chấp nhận cluster hoàn toàn không phục vụ ghi khi không phía nào hội đủ đa số — kể cả khi phía minority có nhiều tài nguyên hơn hoặc gần client hơn, nó vẫn phải từ chối, đây chính là hy sinh availability để đổi lấy an toàn dữ liệu (chọn CP thay vì AP theo CAP theorem). Thêm witness/arbiter node để phá thế hòa giải quyết vấn đề số chẵn nhưng thêm một điểm phải vận hành và giám sát, và nếu witness node đặt sai vị trí mạng (cùng rack với một trong hai phía dữ liệu) nó có thể vô tình thiên vị một phía một cách không chủ ý. Cơ chế fencing (STONITH — Shoot The Other Node In The Head, tự động cắt điện/network của node cũ khi phát hiện bị thay thế) triệt để ngăn split-brain hơn self-fencing bằng term number, nhưng đòi hỏi tích hợp phần cứng/hạ tầng phức tạp hơn và có rủi ro riêng: fencing sai (cắt nhầm node đang healthy) tự nó gây outage. Đa số quorum lệch có chủ đích cho multi-region (ưu tiên region chính) giải quyết vấn đề chọn bên thắng nhanh, nhưng đồng nghĩa region phụ không bao giờ có thể tự phục vụ ghi ngay cả khi region chính sập hoàn toàn và region phụ vẫn khỏe mạnh — phải có quy trình thủ công hoặc bán tự động để "trao quyền" majority sang region phụ trong thảm họa thật.

## Best Practices

- Luôn triển khai cluster đồng thuận (etcd, ZooKeeper, Raft-based store) với số node lẻ; nếu hạ tầng chỉ cho phép số node chẵn, thêm một witness/arbiter node không lưu dữ liệu để đảm bảo luôn có majority rõ ràng.
- Không tự chế cơ chế leader election dựa trên heartbeat timeout đơn giản — dùng thư viện/hệ thống đã implement Raft/Paxos đúng chuẩn (etcd, Consul, ZooKeeper) thay vì tự viết logic "không thấy leader thì tôi tự làm leader".
- Bật cơ chế fencing (STONITH hoặc application-level fencing token) cho mọi hệ thống có khái niệm leader/lock, để đảm bảo leader cũ không thể tiếp tục ghi ngay cả khi nó chưa nhận ra mình đã bị thay thế.
- Với triển khai multi-region, thiết kế trước quorum lệch có chủ đích và viết runbook cho kịch bản region chính sập hoàn toàn (khi nào và ai được quyền ép chuyển majority sang region phụ).
- Giám sát chỉ số quorum health (số node trong majority, độ trễ giữa các node đồng thuận) như cảnh báo sớm; mất quorum tạm thời hoặc term number tăng bất thường là dấu hiệu split-brain đang manh nha trước khi nó gây ghi xung đột thật.

## Common Mistakes

- Chạy cluster với số node chẵn (2, 4, 6) mà không có witness node, tạo điều kiện cho partition 50/50 hoặc tệ hơn là để mỗi phía tự nghĩ mình đủ điều kiện làm leader.
- Tự implement leader election bằng heartbeat/timeout đơn giản không kiểm tra majority, coi "tôi không thấy leader" là đủ điều kiện để tự bầu mình — đây gần như luôn dẫn tới split-brain khi partition đối xứng xảy ra.
- Không bật fencing, tin rằng leader cũ sẽ "tự biết" dừng lại khi bị thay thế — trong thực tế leader cũ bị cô lập mạng không có cách nào biết nó đã bị thay thế cho tới khi mạng nối lại.
- Test failover trong điều kiện lý tưởng (kill hẳn một node) mà không bao giờ test kịch bản network partition thật (node vẫn sống nhưng mất liên lạc một phần) — đây chính là kịch bản gây split-brain, khác hẳn với crash-stop failure.
- Sau khi split-brain xảy ra, tự động merge dữ liệu bằng last-write-wins mà không rà soát nghiệp vụ, gây mất dữ liệu âm thầm (ghi đè giao dịch hợp lệ) thay vì flag lại để xử lý thủ công.

## Interview Questions

**Hỏi**: Tại sao cluster đồng thuận (etcd, ZooKeeper) luôn khuyến nghị số node lẻ?

**Trả lời**: Vì majority quorum cần một con số chiếm quá bán tổng số node. Với số lẻ (ví dụ 5), một partition chia 3-2 luôn có đúng một phía đạt majority (3), phía kia (2) tự biết mình là minority và fail closed. Với số chẵn (ví dụ 4), partition có thể chia 2-2 — không phía nào đạt majority, toàn cluster ngừng ghi, nhưng ít nhất vẫn an toàn (không split-brain) chứ không có lợi ích gì thêm so với số lẻ, nên số lẻ luôn được chọn để tối đa hóa khả năng cluster vẫn còn một phía hoạt động được.

**Hỏi**: Term number (hoặc epoch number) trong Raft giúp ngăn split-brain như thế nào?

**Trả lời**: Mỗi lần bầu leader mới, term number tăng lên. Leader cũ bị cô lập mạng không thể biết đã có leader mới với term cao hơn được bầu ở phía majority. Khi mạng nối lại, mọi node so sánh term number và luôn chấp nhận leader có term cao hơn là hợp lệ; leader cũ nhận ra term của nó đã lỗi thời và tự step down (self-fencing), hủy các thay đổi nó nhận được trong lúc bị cô lập nếu chưa được majority xác nhận.

**Hỏi**: Fencing (STONITH) khác gì so với việc chỉ dựa vào term number/self-fencing?

**Trả lời**: Self-fencing dựa vào giả định leader cũ sẽ tự phát hiện và tự dừng khi mạng nối lại — nhưng trong lúc bị cô lập, nếu leader cũ vẫn nhận ghi từ một phần client (ví dụ client cũng ở cùng phía mạng bị cô lập), dữ liệu xung đột đã xảy ra trước khi self-fencing kịp can thiệp. STONITH chủ động cắt điện hoặc cách ly hoàn toàn node cũ ở tầng hạ tầng (network, power) ngay khi phát hiện nó bị thay thế, ngăn nó tiếp tục nhận bất kỳ ghi nào thay vì chờ nó tự nhận ra và tự dừng.

## Summary

Split-brain xảy ra khi network partition khiến một cluster chia thành nhiều nhóm, và từ hai nhóm trở lên đồng thời tin mình là leader/primary hợp lệ, dẫn tới ghi dữ liệu xung đột không thể tự động hòa giải. Nguyên nhân gốc luôn là thiếu cơ chế majority quorum bắt buộc trong bầu leader — hệ thống dựa vào heartbeat timeout đơn giản mà không kiểm tra "tôi có thực sự chiếm đa số không" trước khi tự xưng leader. Giải pháp là dùng thuật toán đồng thuận đã được chứng minh (Raft, Paxos) với term number tăng dần và majority quorum bắt buộc, kết hợp fencing để triệt để ngăn leader cũ tiếp tục ghi sau khi bị thay thế. Trong production, luôn triển khai số node lẻ hoặc thêm witness node, và với multi-region cần thiết kế trước quorum lệch có chủ đích cùng runbook cho thảm họa toàn region. Hậu quả của split-brain không dừng ở downtime — nó để lại dữ liệu đã phân kỳ cần merge thủ công, thường tốn kém và rủi ro hơn nhiều so với một outage thông thường.

## Knowledge Graph

- CAP Theorem — split-brain là hậu quả cụ thể khi một hệ đáng lẽ CP bị vận hành/cấu hình sai thành hành xử như AP lúc partition.
- Consensus — Raft/Paxos với majority quorum và term number là cơ chế kỹ thuật chính để ngăn split-brain.
- Leader Election — split-brain chính là thất bại của leader election khi thiếu ràng buộc majority.
- Distributed Locking — lock bị giữ đồng thời bởi hai bên tưởng mình là owner hợp lệ là một dạng split-brain ở tầng lock thay vì tầng leader.
- Quorum — khái niệm toán học nền tảng (quá bán N) đảm bảo không thể có hai nhóm rời nhau cùng đạt majority.
- Fencing/STONITH — cơ chế bổ trợ ngăn leader cũ tiếp tục hành động sau khi bị thay thế, độc lập với self-fencing qua term number.

## Five Things To Remember

- Split-brain xảy ra khi hai phía của một partition đều tưởng mình là leader và cùng nhận ghi độc lập.
- Nguyên nhân gốc luôn là thiếu majority quorum bắt buộc trong cơ chế bầu leader.
- Luôn dùng số node lẻ hoặc thêm witness node để đảm bảo majority luôn xác định được một phía duy nhất.
- Term number tăng dần giúp self-fencing, nhưng fencing chủ động (STONITH) mới triệt để ngăn leader cũ tiếp tục ghi.
- Test failover phải bao gồm kịch bản network partition thật, không chỉ crash-stop, vì split-brain chỉ xảy ra khi node vẫn sống nhưng mất liên lạc.
