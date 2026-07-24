---
id: leader-election
title: Leader Election
tags: ["distributed-systems"]
---

# Leader Election

> Status: Draft

## Problem

Khi nhiều instance của cùng một service chạy song song (scale-out để chịu tải hoặc để có HA), một số tác vụ không được phép chạy đồng thời trên nhiều node — ví dụ cron job dọn dữ liệu, job đẩy batch xuống downstream, hay quá trình rebalance partition trong một message queue. Nếu không có cơ chế xác định "ai là người được ghi/được chạy", mọi node đều nghĩ mình có quyền hành động, dẫn tới việc cùng một job chạy N lần, cùng một record bị ghi đè chồng chéo bởi nhiều tiến trình. Leader election giải quyết đúng bài toán này bằng cách chỉ định chính xác một node giữ vai trò leader tại một thời điểm.

## Pain Points

- Duplicate writes: hai node cùng tưởng mình là leader, cùng ghi vào một aggregate, gây lost update hoặc dữ liệu rác (double billing, double email gửi khách hàng).
- Split-brain: mất kết nối mạng tạm thời khiến cả cluster cũ và cluster mới đều tin mình có leader, xử lý business logic mâu thuẫn nhau.
- Outage kéo dài nếu không có failover: leader chết mà không ai phát hiện, hệ thống đứng im chờ leader cũ hồi sinh thay vì bầu lại ngay.
- Chi phí vận hành tăng khi debug: log không phân biệt được node nào đang là leader tại thời điểm sự cố, khiến việc truy vết root cause mất nhiều giờ.

## Solution

Leader election là cơ chế để một tập hợp node đồng thuận (consensus) chọn ra đúng một node làm leader, và tất cả các node khác biết rõ ai là leader hiện tại để tự nhường quyền ghi/quyền điều phối cho node đó. Khi leader chết hoặc mất liên lạc, cluster phải phát hiện được (qua timeout/heartbeat) và bầu lại leader mới mà không cần can thiệp thủ công. Cơ chế này thường dựa trên các thuật toán consensus đã được chứng minh đúng như Raft, Paxos, hoặc dùng dịch vụ điều phối bên ngoài như ZooKeeper, etcd, Consul làm nơi lưu trạng thái leadership dưới dạng lease có TTL.

## How It Works

Có hai cách tiếp cận phổ biến:

1. **Consensus tự thân (Raft-based)**: mỗi node có một trong ba trạng thái — follower, candidate, leader. Khi follower không nhận được heartbeat từ leader trong một khoảng election timeout (thường random hóa 150-300ms để tránh nhiều node cùng lúc trở thành candidate), nó chuyển sang candidate, tăng term number lên 1, và gửi RequestVote tới các node khác. Node nào nhận được quá bán phiếu (majority quorum) trong term đó sẽ trở thành leader và bắt đầu gửi heartbeat (AppendEntries rỗng) để giữ quyền. Term number tăng đơn điệu đảm bảo không có hai leader hợp lệ cùng tồn tại trong cùng một term — đây là điều kiện cốt lõi để tránh split-brain.

2. **Lease-based qua coordination service (ZooKeeper/etcd)**: các node cùng cố gắng tạo một ephemeral node (ZooKeeper) hoặc acquire một lease với TTL (etcd) tại cùng một key, ví dụ `/leader-election/service-x`. Chỉ một node ghi thành công (compare-and-swap nguyên tử), các node còn lại watch key đó. Leader phải gia hạn (renew) lease định kỳ trước khi hết TTL; nếu leader crash hoặc mất kết nối, session hết hạn, ZooKeeper/etcd tự xóa ephemeral node hoặc release lease, kích hoạt watch event tới toàn bộ follower để chạy lại vòng bầu cử.

Điểm mấu chốt kỹ thuật ở cả hai cách: **fencing token**. Ngay cả khi leader mới đã được bầu, leader cũ vẫn có thể chưa nhận ra mình đã mất quyền (do GC pause, network partition) và tiếp tục ghi dữ liệu — gọi là "zombie leader". Fencing token là một số tăng đơn điệu (term number hoặc lease version) được downstream storage kiểm tra: mọi ghi kèm token cũ hơn token hiện tại đều bị từ chối, bất kể node gửi có tin mình là leader hay không.

## Production Architecture

Trong một hệ thống thực tế như Kafka, mỗi partition có một broker giữ vai trò leader (partition leader), được bầu bởi controller thông qua ZooKeeper (các phiên bản cũ) hoặc KRaft consensus (Kafka 3.x trở lên) — chỉ leader mới nhận write, các replica follower chỉ đọc và đồng bộ. Trong Kubernetes, các controller quan trọng như kube-scheduler hay cert-manager chạy nhiều replica cho HA nhưng dùng Lease object (`coordination.k8s.io/v1`) trong etcd để đảm bảo chỉ một pod thực sự chạy reconcile loop tại một thời điểm — các pod còn lại ở chế độ standby, sẵn sàng nhận leadership ngay khi lease hết hạn. PostgreSQL với Patroni dùng etcd/Consul để bầu primary trong cluster streaming replication, tự động failover khi primary chết và fence node cũ bằng cách STONITH (Shoot The Other Node In The Head) để tránh nó tiếp tục nhận write.

## Trade-offs

- Consensus tự thân (Raft) cho latency thấp và không phụ thuộc external service, nhưng team phải tự implement và vận hành đúng — một lỗi nhỏ trong log replication hay term comparison có thể gây split-brain âm thầm, rất khó phát hiện qua test.
- Coordination service (ZooKeeper/etcd) giảm độ phức tạp implement nhưng thêm một single point of failure mới cần vận hành riêng (chính ZooKeeper/etcd cũng cần HA), và độ trễ failover phụ thuộc session timeout của dịch vụ đó — timeout ngắn thì false positive khi network chập chờn, timeout dài thì downtime kéo dài khi leader thật sự chết.
- Fencing token bắt buộc phải được downstream storage tôn trọng; nếu chỉ có ở tầng ứng dụng mà storage không kiểm tra, zombie leader vẫn ghi được — đây là trade-off giữa độ an toàn và độ phức tạp tích hợp.
- Bầu lại leader luôn có một khoảng "no leader" (unavailability window) — hệ thống buộc phải chọn giữa consistency (chờ bầu xong mới ghi) và availability (chấp nhận đọc stale trong lúc chưa có leader), đúng tinh thần CAP theorem.

## Best Practices

- Luôn dùng fencing token (term/lease version tăng đơn điệu) và bắt buộc storage layer kiểm tra token thay vì chỉ tin vào "tôi nghĩ tôi là leader".
- Random hóa election timeout giữa các node để tránh nhiều candidate cùng lúc tranh cử liên tục (dẫn tới split vote vô hạn).
- Theo dõi và alert trên số lần leader election xảy ra trong một khoảng thời gian ngắn — election churn là dấu hiệu sớm của network flakiness hoặc GC pause bất thường.
- Thiết kế follower/standby phải có khả năng nhận leadership ngay lập tức (warm standby), tránh cold-start làm kéo dài thời gian gián đoạn.
- Không tự chế lại thuật toán consensus; dùng thư viện/dịch vụ đã được kiểm chứng rộng rãi (Raft libraries, etcd, ZooKeeper) trừ khi có lý do kỹ thuật rất rõ ràng.

## Common Mistakes

- Dùng heartbeat/ping đơn giản không kèm term/epoch number để phát hiện leader cũ, dẫn tới zombie leader vẫn ghi dữ liệu sau khi đã bị thay thế.
- Đặt session/lease timeout quá ngắn so với GC pause thực tế của JVM hoặc độ trễ mạng thực tế, gây false failover liên tục dù leader vẫn khỏe.
- Không test kịch bản network partition (chỉ test crash), bỏ sót toàn bộ lớp lỗi split-brain — đây là nguyên nhân phổ biến nhất của sự cố leader election trong production.
- Coi leader election là "set and forget": không có dashboard/alert cho current leader, khi sự cố xảy ra không ai biết node nào đang giữ quyền để debug.
- Để business logic phụ thuộc cứng vào "tôi là leader nên chắc chắn đúng" mà không có idempotency ở downstream, khiến một lần zombie leader ghi sai là không thể rollback sạch.

## Interview Questions

**Hỏi**: Tại sao Raft cần majority quorum (quá bán) để bầu leader, thay vì chỉ cần nhiều phiếu nhất?

**Trả lời**: Majority quorum đảm bảo bất kỳ hai tập hợp majority nào trong cùng cluster đều phải giao nhau ít nhất một node. Nhờ đó, tại một term, không thể có hai node cùng nhận đủ majority để trở thành leader — đảm bảo tính duy nhất của leader (safety), tránh split-brain.

**Hỏi**: Fencing token giải quyết vấn đề gì mà heartbeat/timeout thông thường không giải quyết được?

**Trả lời**: Heartbeat/timeout chỉ giúp follower phát hiện leader "có vẻ" đã chết để bầu lại, nhưng không ngăn được leader cũ (bị GC pause hoặc network delay) tỉnh lại và tiếp tục ghi dữ liệu như một zombie leader. Fencing token là số tăng đơn điệu mà downstream storage kiểm tra, từ chối mọi ghi mang token cũ hơn token hiện tại, bất kể node gửi tin mình là leader hay không.

**Hỏi**: Nếu dùng ZooKeeper cho leader election, điều gì xảy ra khi cluster ZooKeeper tự nó bị network partition?

**Trả lời**: ZooKeeper tự nó cũng chạy trên Zab consensus với majority quorum; phía minority partition sẽ không còn quorum nên không thể phục vụ write, các session ephemeral node ở phía đó sẽ hết hạn — buộc leader ở phía minority (nếu có) mất leadership, chỉ phía majority mới có thể tiếp tục bầu và giữ leader hợp lệ.

## Summary

Leader election chọn đúng một node chịu trách nhiệm điều phối/ghi tại một thời điểm, dựa trên thuật toán consensus (Raft/Paxos) hoặc coordination service có lease/ephemeral node (ZooKeeper, etcd, Consul). Cơ chế cốt lõi là majority quorum để đảm bảo tính duy nhất của leader, và fencing token để chặn zombie leader ghi dữ liệu sau khi đã mất quyền. Trade-off chính là giữa độ phức tạp tự implement consensus và việc phụ thuộc một coordination service bên ngoài, cùng với khoảng gián đoạn không thể tránh khỏi khi bầu lại leader. Trong production, leader election xuất hiện ở Kafka partition leader, Kubernetes controller lease, và Patroni cho PostgreSQL failover.

## Knowledge Graph

- Consensus algorithm (Raft/Paxos) — nền tảng thuật toán mà leader election dựa vào để đảm bảo safety.
- Split-brain — hậu quả trực tiếp khi leader election thất bại hoặc thiếu fencing.
- Distributed lock — cùng dùng coordination service (ZooKeeper/etcd) và cùng gặp vấn đề zombie holder như leader election.
- Heartbeat / failure detection — cơ chế phát hiện leader chết để kích hoạt bầu lại.
- Quorum — điều kiện toán học (majority) đảm bảo tính duy nhất của leader.
- CAP theorem — giải thích vì sao luôn có khoảng unavailability khi ưu tiên consistency trong lúc bầu lại leader.

## Five Things To Remember

- Chỉ một leader hợp lệ tại một thời điểm nhờ majority quorum, không phải nhờ "ai nhanh hơn".
- Fencing token là bắt buộc để chặn zombie leader, không phải tùy chọn.
- Random hóa election timeout để tránh split vote liên tục.
- Leader chết luôn tạo ra một khoảng gián đoạn ngắn — không thể loại bỏ hoàn toàn, chỉ có thể rút ngắn.
- Đừng tự chế thuật toán consensus; dùng Raft/ZooKeeper/etcd đã được kiểm chứng.
