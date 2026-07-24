---
id: consensus
title: "Consensus (Raft/Paxos)"
tags: ["distributed-systems"]
---

# Consensus (Raft/Paxos)

> Status: Draft

## Problem

Khi một hệ thống chạy nhiều node để chịu lỗi (fault tolerance), sớm muộn các node cũng cần đồng ý với nhau về "sự thật" — leader hiện tại là ai, giá trị nào được ghi vào log, cấu hình cluster hiện tại ra sao. Nếu chỉ đơn giản để mỗi node tự quyết hoặc dùng "majority vote" ngây thơ không có thứ tự (ordering) và xử lý network partition, cluster sẽ rơi vào split-brain: hai node cùng nghĩ mình là leader, hai giá trị khác nhau cùng được coi là "đã commit". Ví dụ kinh điển: một cluster etcd 5 node bị chia đôi bởi lỗi mạng giữa hai rack, cả hai nửa đều cố bầu leader và ghi dữ liệu, dẫn đến hai lịch sử dữ liệu phân kỳ (diverged history) không thể hòa giải.

## Pain Points

- Split-brain: hai leader cùng tồn tại, mỗi bên nhận ghi (write) độc lập, dữ liệu phân kỳ và không thể merge tự động khi mạng nối lại.
- Mất dữ liệu đã "commit": nếu thuật toán không đảm bảo majority overlap giữa các quorum, một giá trị tưởng đã ghi thành công có thể bị ghi đè sau khi leader mới được bầu.
- Outage kéo dài do thuật toán tự chế (home-grown) không xử lý đúng các edge case như network partition, node restart giữa chừng, hay message bị trễ/trùng lặp — ví dụ nhiều sự cố ngoài đời với các hệ thống lock service tự viết đã dẫn đến mất quorum vĩnh viễn.
- Chi phí vận hành tăng vì đội SRE phải debug thủ công trạng thái cluster mỗi khi có network blip, thay vì hệ thống tự phục hồi.

## Solution

Consensus algorithm (Paxos, Raft) là giao thức đảm bảo một tập hợp node phân tán đồng ý về một giá trị hoặc một chuỗi giá trị (log) duy nhất, ngay cả khi tối đa `f` trong tổng số `2f+1` node bị lỗi hoặc mạng bị phân mảnh tạm thời — miễn là majority (quorum) vẫn liên lạc được với nhau. Raft được thiết kế sau Paxos với mục tiêu tường minh (understandability), chia vấn đề thành ba phần độc lập: leader election, log replication, và safety. Đây là nền tảng của hầu hết các hệ thống lưu trữ metadata/coordination production: etcd (Raft), Consul (Raft), ZooKeeper (Zab — họ hàng gần với Paxos), CockroachDB và TiDB (Raft per-range).

## How It Works

**Raft — ba cơ chế cốt lõi:**

1. Leader Election: mỗi node ở một trong ba trạng thái Follower/Candidate/Leader. Nếu Follower không nhận heartbeat từ Leader trong khoảng election timeout (thường random 150-300ms để tránh split vote), nó chuyển thành Candidate, tăng `term` (số hiệu nhiệm kỳ, monotonic), tự bỏ phiếu cho mình và gửi RequestVote RPC cho các node khác. Node nào nhận được phiếu từ majority (quá bán, ví dụ 3/5) sẽ thành Leader cho term đó.

2. Log Replication: Leader nhận request ghi từ client, append vào log local dưới dạng một entry gắn kèm `term` và `index`, sau đó gửi AppendEntries RPC tới các Follower. Khi majority Follower đã ack (ghi thành công vào log của họ), Leader coi entry đó là "committed", áp dụng vào state machine, và trả kết quả cho client. Followers commit entry theo `commitIndex` mà Leader thông báo ở heartbeat kế tiếp.

3. Safety: Raft đảm bảo Election Safety (tối đa 1 leader mỗi term), Leader Append-Only (leader không bao giờ ghi đè/xóa log của chính nó), Log Matching (nếu hai log có cùng index+term thì mọi entry trước đó giống hệt nhau), và Leader Completeness (leader mới luôn chứa mọi entry đã committed từ term trước) — nhờ quy tắc "candidate chỉ được bầu nếu log của nó ít nhất mới bằng majority", so sánh bằng `(lastLogTerm, lastLogIndex)`.

**Paxos** giải quyết cùng bài toán nhưng bằng hai pha đối xứng hơn: Prepare/Promise (proposer xin quyền đề xuất với một propose number `n`, acceptor hứa từ chối mọi đề xuất có số nhỏ hơn `n`) và Accept/Accepted (proposer gửi giá trị, acceptor chấp nhận nếu chưa hứa với `n` lớn hơn). Multi-Paxos (dùng thực tế để replicate log) thêm khái niệm leader ổn định để bỏ qua pha Prepare lặp lại — về bản chất hội tụ gần giống Raft nhưng không tách bạch rõ vai trò leader/log như Raft, khiến việc cài đặt đúng khó hơn nhiều (đây là lý do Raft ra đời — paper gốc "In Search of an Understandable Consensus Algorithm").

**Quorum toán học**: với cluster `N` node, chịu được `f = floor((N-1)/2)` node lỗi. Cluster 5 node chịu được 2 node chết mà vẫn hoạt động (quorum = 3); cluster 3 node chỉ chịu được 1 node chết (quorum = 2). Đây là lý do etcd/Consul luôn khuyến nghị số node lẻ (3, 5, 7) — số chẵn không tăng khả năng chịu lỗi nhưng tăng chi phí network round-trip cho mỗi write.

## Production Architecture

etcd dùng Raft (thư viện `etcd-io/raft`) làm store cho toàn bộ metadata của Kubernetes: mọi object (Pod, Deployment, ConfigMap...) được ghi qua Raft log trước khi API server coi là "đã lưu". Consul dùng Raft tương tự để duy trì service catalog và KV store, với leader xử lý toàn bộ write còn follower phục vụ read (stale hoặc consistent tùy consistency mode được chọn). CockroachDB và TiKV chia dữ liệu thành hàng nghìn "range"/"region", mỗi range là một Raft group riêng với leader riêng — cho phép scale ghi theo chiều ngang vì các Raft group độc lập chạy song song trên các leader khác nhau. Trong mọi trường hợp, consensus layer chỉ chịu trách nhiệm cho phần "control plane"/metadata có tốc độ ghi thấp-vừa; dữ liệu khối lượng lớn (bulk data) thường dùng cơ chế replication khác (ví dụ Kafka dùng ISR — In-Sync Replica — riêng, không phải Raft/Paxos thuần).

## Trade-offs

- Latency mỗi write bị ràng buộc bởi round-trip tới majority node — cluster 5 node trải trên nhiều region sẽ có write latency cao hơn đáng kể so với 3 node cùng datacenter, vì phải đợi phản hồi từ node xa nhất trong quorum.
- Consensus không miễn phí: mỗi entry commit tốn ít nhất 1 network round-trip tới majority, nên các hệ thống high-throughput (event streaming, time-series ghi dày) thường không dùng Raft trực tiếp cho data plane mà chỉ dùng cho control plane.
- Thêm node không tăng tuyến tính khả năng chịu lỗi mà tăng chi phí: quorum lớn hơn nghĩa là cần nhiều ack hơn cho mỗi write, giảm throughput.
- Trong network partition kéo dài, phía minority hoàn toàn không thể ghi (unavailable) dù dữ liệu local vẫn còn nguyên — đây là lựa chọn CP (Consistency + Partition tolerance) trong CAP, đánh đổi lấy availability.
- Raft dễ hiểu hơn Paxos nhưng không tổng quát hóa tốt bằng: Paxos gốc và các biến thể (Fast Paxos, EPaxos) cho phép một số kịch bản không cần leader cố định, linh hoạt hơn trong multi-datacenter, còn Raft gắn chặt với mô hình single-leader.

## Best Practices

- Luôn dùng số node lẻ (3 hoặc 5) cho consensus cluster; không dùng 4 hay 6 vì không tăng fault tolerance nhưng tăng latency.
- Đặt các node của cùng một Raft/Paxos group đủ gần nhau về network (cùng region hoặc cùng AZ nhóm) để tránh election timeout giả do latency cao, trừ khi thiết kế multi-region có chủ đích với timeout được tune riêng.
- Giám sát leader election rate (etcd: `etcd_server_leader_changes_seen_total`) — election liên tục là dấu hiệu network không ổn định hoặc disk I/O chậm khiến node không kịp gửi heartbeat.
- Tách biệt disk cho WAL (write-ahead log) của Raft ra khỏi disk chứa dữ liệu khác; fsync chậm là nguyên nhân phổ biến nhất gây leader bị "demote" giả trong etcd.
- Không dùng consensus store (etcd/Consul/ZooKeeper) làm database chính cho ứng dụng — chúng được tối ưu cho ghi ít, đọc nhiều, kích thước dữ liệu nhỏ (thường khuyến nghị dưới vài GB).

## Common Mistakes

- Chạy cluster với số node chẵn hoặc scale cluster từ 3 lên 4 "cho chắc" mà không hiểu quorum, vô tình giảm khả năng chịu lỗi tương đối.
- Tự viết thuật toán "đồng thuận" đơn giản hóa (ví dụ chỉ dùng lock qua database, hoặc "majority ping" không có term/log) tưởng là đủ nhưng không xử lý được network partition hay node restart giữa chừng, dẫn đến split-brain.
- Đặt các node consensus cross-region với latency cao mà không tăng election timeout tương ứng, gây leader election liên tục ("thrashing") làm cluster gần như không dùng được.
- Hiểu nhầm rằng dữ liệu "committed" trong Raft nghĩa là đã ghi ra disk bền vững ở mọi node — thực ra chỉ cần majority ack, một số follower có thể vẫn đang catch-up.
- Bỏ qua việc backup snapshot của Raft log/state, dẫn tới trường hợp mất quorum vĩnh viễn (ví dụ 2/3 node etcd bị xóa ổ đĩa) mà không có cách khôi phục ngoài rebuild từ snapshot cũ.

## Interview Questions

**Hỏi**: Tại sao Raft lại "dễ hiểu hơn" Paxos, và điều đó có đánh đổi gì không?

**Trả lời**: Raft tách rõ ba vấn đề độc lập (leader election, log replication, safety) và luôn duy trì đúng một leader chịu trách nhiệm ghi log theo thứ tự tuyến tính, nên dễ implement và debug đúng. Đánh đổi là Raft ràng buộc chặt vào mô hình single-leader nên kém linh hoạt hơn Paxos/EPaxos trong các kịch bản muốn tránh single point of coordination, ví dụ multi-leader multi-datacenter.

**Hỏi**: Một cluster Raft 5 node, 2 node bị crash cùng lúc. Cluster có còn hoạt động không? Nếu crash thêm 1 node nữa thì sao?

**Trả lời**: Với 5 node, quorum cần là 3. Mất 2 node còn lại 3 node vẫn đạt quorum nên cluster vẫn hoạt động bình thường (đọc/ghi được). Nếu mất thêm 1 node nữa (tổng 3/5 chết), chỉ còn 2 node — không đủ majority — cluster mất khả năng bầu leader mới và ngừng nhận ghi cho tới khi có node phục hồi.

**Hỏi**: Vì sao Raft cần random election timeout thay vì timeout cố định?

**Trả lời**: Nếu mọi Follower dùng cùng timeout cố định, khi Leader chết, tất cả sẽ trở thành Candidate cùng lúc và chia phiếu bầu (split vote) lặp đi lặp lại, không ai đạt majority. Randomize timeout (ví dụ 150-300ms) làm giảm xác suất hai Candidate khởi động bầu cử cùng lúc, giúp hội tụ về một leader nhanh hơn.

## Summary

Consensus algorithm giải quyết bài toán cốt lõi của hệ phân tán: làm sao nhiều node đồng ý về một giá trị/thứ tự sự kiện duy nhất dù có node lỗi hoặc mạng phân mảnh, miễn là majority (quorum) còn liên lạc được. Raft đạt mục tiêu này bằng cách tách bạch leader election, log replication và safety, khiến nó dễ hiểu và triển khai đúng hơn Paxos — lý do nó trở thành lựa chọn mặc định cho etcd, Consul, CockroachDB. Cái giá phải trả là latency mỗi write phụ thuộc vào round-trip tới majority và hệ thống chọn nhất quán (CP) thay vì khả dụng tuyệt đối (AP) khi xảy ra partition. Vận hành đúng đòi hỏi hiểu quorum toán học, theo dõi leader election rate, và không nhầm lẫn consensus store với database ứng dụng thông thường.

## Knowledge Graph

- CAP theorem — Raft/Paxos là ví dụ điển hình của lựa chọn CP (Consistency + Partition tolerance), hy sinh Availability khi partition.
- Quorum / Majority write — cơ chế toán học nền tảng quyết định fault tolerance của consensus cluster.
- Leader election — thành phần con của Raft, cũng xuất hiện độc lập trong các hệ thống dùng ZooKeeper/etcd để chọn leader ứng dụng.
- Write-Ahead Log (WAL) — cấu trúc lưu trữ mà Raft log dựa vào để đảm bảo durability trước khi commit.
- Split-brain — hậu quả trực tiếp khi thiếu consensus đúng đắn hoặc quorum bị vi phạm.
- ZooKeeper Zab protocol — thuật toán đồng thuận họ hàng với Paxos, dùng trong Kafka (cho tới trước KRaft) và HBase.

## Five Things To Remember

- Consensus cần majority (quorum), không cần tất cả node sống để hoạt động.
- Số node nên là số lẻ — chẵn không tăng fault tolerance nhưng tăng chi phí.
- Raft = leader election + log replication + safety, tách bạch rõ ràng hơn Paxos.
- Mỗi write tốn ít nhất một round-trip tới majority — đây là chi phí cố hữu, không phải bug.
- Consensus store (etcd/Consul/ZooKeeper) dùng cho metadata/coordination, không dùng làm database chính.
