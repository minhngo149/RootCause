---
id: eventual-consistency
title: Eventual Consistency
tags: ["distributed-systems"]
---

# Eventual Consistency

> Status: Draft

## Problem

Một hệ thống e-commerce lưu số lượng "lượt thích" và "số bình luận" của sản phẩm trên nhiều bản sao (replica) đặt ở nhiều region để giảm latency đọc cho người dùng toàn cầu. Người dùng ở Singapore bấm "thích" một sản phẩm, request ghi vào replica gần nhất (ap-southeast), nhưng người dùng ở Frankfurt load lại trang ngay sau đó vẫn thấy số lượt thích cũ vì đọc từ replica eu-central chưa nhận được bản cập nhật. Đội phát triển, quen tư duy single-database, coi đây là "bug" và cố gắng vá bằng cách buộc mọi request đọc phải đi qua node ghi gần nhất — vô tình biến toàn hệ thống thành một database tập trung duy nhất, đánh mất toàn bộ lợi ích của việc phân tán dữ liệu ra nhiều region. Vấn đề thực sự không phải là bug, mà là kỳ vọng sai: hệ thống được thiết kế cho eventual consistency, nhưng đội ngũ lại lập trình và vận hành nó như thể nó phải strongly consistent.

## Pain Points

- Race condition ẩn khi ứng dụng đọc-sửa-ghi (read-modify-write) trên dữ liệu chưa hội tụ: hai request cùng đọc một giá trị tồn kho cũ từ hai replica khác nhau, cả hai đều tính toán hợp lệ dựa trên giá trị stale, dẫn tới bán vượt số lượng tồn kho thực tế (oversell).
- Đội hỗ trợ khách hàng nhận hàng loạt ticket "dữ liệu sai" khi người dùng thao tác trên một thiết bị rồi kiểm tra kết quả gần như ngay lập tức trên thiết bị khác (khác region/replica) và thấy dữ liệu chưa cập nhật — không phải lỗi hệ thống nhưng bị hiểu nhầm là lỗi nghiêm trọng.
- Chi phí vận hành tăng khi đội ngũ không giám sát **replication lag**, để nó âm thầm giãn ra (từ vài trăm mili-giây lên hàng chục giây do quá tải mạng hoặc node chậm) mà không có cảnh báo, tới khi khách hàng report thì độ lệch dữ liệu đã quá lớn để giải thích là "eventual".
- Logic nghiệp vụ viết sai giả định (assume mọi replica luôn đồng bộ tức thời) khiến các luồng quan trọng như xác nhận thanh toán, cập nhật trạng thái đơn hàng dựa trên dữ liệu đọc từ replica stale, gây ra trạng thái nghiệp vụ mâu thuẫn (đơn hàng "đã giao" nhưng ví vẫn hiển thị "chưa trừ tiền").

## Solution

Eventual consistency là mô hình nhất quán trong đó một hệ phân tán không đảm bảo mọi bản sao dữ liệu phản ánh giá trị mới nhất ngay lập tức sau khi ghi, nhưng đảm bảo rằng nếu không có ghi mới nào xảy ra thêm, mọi bản sao rồi sẽ hội tụ (converge) về cùng một giá trị sau một khoảng thời gian hữu hạn. Đây là lựa chọn có chủ đích để đổi lấy availability và latency thấp hơn: thay vì bắt mọi request ghi phải chờ xác nhận từ tất cả (hoặc đa số) bản sao trước khi coi là thành công, hệ thống chấp nhận ghi cục bộ trước, phản hồi ngay cho client, rồi lan truyền (propagate) thay đổi tới các bản sao khác trong nền — dữ liệu "đúng cuối cùng", không phải "đúng ngay lập tức".

## How It Works

Cơ chế cốt lõi gồm ba phần phối hợp với nhau. Thứ nhất, **write acknowledgment cục bộ**: khi client ghi, node nhận request xác nhận thành công ngay sau khi ghi vào bản sao cục bộ (hoặc một số lượng tối thiểu bản sao, tùy tunable consistency level), không chờ toàn bộ cluster xác nhận — đây là điểm khác biệt cơ bản so với hệ đồng bộ (synchronous replication) nơi write phải chờ quorum. Thứ hai, **lan truyền bất đồng bộ (async replication)**: thay đổi được đẩy tới các bản sao khác qua các cơ chế như gossip protocol (Cassandra, DynamoDB dùng gossip để trao đổi trạng thái giữa các node), change-data-capture log, hoặc replication log dạng append-only — quá trình này có độ trễ (replication lag) phụ thuộc băng thông mạng, tải hệ thống, và khoảng cách địa lý giữa các node. Thứ ba, **conflict resolution khi có ghi đồng thời từ nhiều nơi**: vì không có điều phối trung tâm chặn ghi song song, hai bản sao có thể nhận hai giá trị khác nhau cho cùng một key gần như đồng thời — hệ thống cần một quy tắc xác định (deterministic) để cả hai bên hội tụ về cùng kết quả khi so sánh lại, phổ biến nhất là **last-write-wins (LWW)** dựa trên timestamp (đơn giản nhưng có thể mất dữ liệu nếu đồng hồ lệch — clock skew), **vector clock** để phát hiện và biểu diễn quan hệ nhân quả giữa các phiên bản (causal ordering, dùng trong Dynamo gốc và Riak), hoặc **CRDT (Conflict-free Replicated Data Type)** — cấu trúc dữ liệu được thiết kế toán học để việc merge hai phiên bản bất kỳ luôn cho kết quả nhất quán và không mất thông tin (ví dụ G-Counter chỉ cộng dồn, không bao giờ ghi đè). Một số hệ thống còn dùng **read repair** (khi client đọc, nếu phát hiện các bản sao trả về giá trị khác nhau, hệ thống âm thầm đồng bộ lại bản cũ ngay trong lúc đọc) và **anti-entropy process** (Merkle tree so sánh định kỳ giữa các bản sao để phát hiện và sửa sai lệch không được phát hiện qua đường ghi bình thường) để rút ngắn thời gian hội tụ thay vì chỉ dựa vào replication tự nhiên.

## Production Architecture

DynamoDB và Cassandra là ví dụ điển hình: mỗi write được ghi vào N bản sao (replication factor, thường N=3), nhưng client có thể chọn write concern `ONE` (chỉ cần 1 bản sao xác nhận, latency thấp nhất, độ trễ hội tụ với các bản sao còn lại có thể vài chục tới vài trăm mili-giây) thay vì `QUORUM` hay `ALL`. Trong kiến trúc CDN và cache toàn cầu (Cloudflare, Akamai, hoặc CDN tự triển khai trên nhiều edge location), nội dung cập nhật ở origin server được đẩy dần ra các edge node — người dùng ở các vùng khác nhau có thể thấy phiên bản cũ trong vài giây tới vài phút cho tới khi cache invalidation lan hết, đây là eventual consistency áp dụng ở tầng caching. DNS toàn cầu cũng vận hành theo mô hình này: một bản ghi DNS cập nhật cần thời gian lan truyền qua các nameserver đệ quy trên toàn thế giới (giới hạn bởi TTL), nên trong khoảng thời gian propagation, các client khác nhau có thể resolve ra hai địa chỉ IP khác nhau cho cùng một domain. Trong hệ thống mạng xã hội, bộ đếm lượt thích/lượt xem thường dùng CRDT dạng counter để nhiều node có thể tăng giảm độc lập mà không cần điều phối, trong khi số dư ví hoặc trạng thái đơn hàng đã thanh toán vẫn buộc phải đặt trên một hệ strongly consistent (thường là cùng một database CP, xem thêm CAP Theorem) — cùng một sản phẩm áp dụng eventual consistency cho phần không quan trọng và strong consistency cho phần liên quan tới tiền.

## Trade-offs

Eventual consistency đổi lấy availability và latency ghi thấp hơn bằng cách chấp nhận một cửa sổ thời gian (đôi khi không xác định trước, phụ thuộc tải hệ thống) trong đó các client khác nhau có thể thấy dữ liệu khác nhau cho cùng một key — ứng dụng phải tự chịu trách nhiệm xử lý tính không chắc chắn này thay vì phó mặc cho database. Conflict resolution (LWW, vector clock, CRDT) không miễn phí: LWW đơn giản nhưng có thể âm thầm mất dữ liệu khi đồng hồ hệ thống lệch nhau (clock skew) giữa các node ghi đồng thời; vector clock chính xác hơn về mặt nhân quả nhưng kích thước metadata tăng theo số node ghi và cần ứng dụng tự xử lý conflict khi phát hiện (không tự động hội tụ); CRDT giải quyết được conflict một cách toán học nhưng chỉ áp dụng được cho một số cấu trúc dữ liệu nhất định (counter, set, map), không phải mọi logic nghiệp vụ tùy ý đều biểu diễn được dưới dạng CRDT. Ứng dụng phải chấp nhận và thiết kế cho khả năng đọc thấy dữ liệu cũ (stale read) — bất kỳ luồng nghiệp vụ nào giả định "đọc ngay sau ghi luôn thấy giá trị mới" (read-after-write consistency) trên một hệ eventual consistency thuần túy đều tiềm ẩn lỗi, trừ khi hệ thống cung cấp riêng cơ chế đảm bảo đó (session consistency, sticky routing về node vừa ghi).

## Best Practices

- Chỉ áp dụng eventual consistency cho dữ liệu mà nghiệp vụ thực sự chấp nhận được độ trễ hội tụ (lượt thích, lượt xem, cache, feed hoạt động) — không áp dụng cho số dư tài khoản, tồn kho giới hạn, hoặc trạng thái giao dịch tài chính.
- Nếu cần trải nghiệm "đọc thấy ngay dữ liệu vừa ghi" cho một số luồng cụ thể (ví dụ người dùng vừa cập nhật profile), dùng read-after-write consistency hoặc sticky session route về đúng node vừa ghi, thay vì chuyển toàn hệ thống sang strong consistency.
- Chọn cấu trúc dữ liệu dạng CRDT (counter, set, map) cho các trường hợp cần merge tự động không cần điều phối trung tâm, thay vì tự viết logic conflict resolution thủ công dễ sai sót.
- Giám sát chủ động **replication lag** như một metric hạ tầng cốt lõi (không phải chỉ theo dõi khi có khiếu nại), đặt alert khi lag vượt ngưỡng chấp nhận được của nghiệp vụ.
- Thiết kế logic nghiệp vụ dạng idempotent và commutative (cộng dồn thay vì ghi đè) bất cứ khi nào có thể, để giảm rủi ro khi hai bản ghi đồng thời cần hội tụ lại với nhau.

## Common Mistakes

- Giả định ngầm rằng đọc ngay sau ghi luôn thấy giá trị mới nhất trên một hệ eventual consistency, dẫn tới logic nghiệp vụ đọc-sửa-ghi (read-modify-write) dựa trên dữ liệu stale mà không hay biết.
- Dùng last-write-wins mặc định cho dữ liệu quan trọng mà không kiểm soát đồng bộ đồng hồ (clock sync qua NTP) giữa các node, khiến bản ghi mới hơn về mặt thời gian thực tế bị bản ghi cũ hơn (nhưng có timestamp lớn hơn do lệch đồng hồ) ghi đè mất.
- Không giám sát replication lag cho tới khi khách hàng report dữ liệu sai, lúc đó độ lệch đã lớn tới mức không còn giải thích được bằng "đang hội tụ".
- Áp dụng eventual consistency cho toàn bộ hệ thống một cách đồng nhất thay vì phân loại theo từng loại dữ liệu — đặt cả số dư ví lẫn lượt thích sản phẩm trên cùng một mô hình nhất quán, dẫn tới hoặc rủi ro tài chính hoặc lãng phí hiệu năng không cần thiết.
- Cố "vá" vấn đề stale read bằng cách ép mọi request đọc phải đi qua node/replica ghi gần nhất, vô tình triệt tiêu toàn bộ lợi ích phân tán (latency thấp, chịu tải cao) mà kiến trúc ban đầu nhắm tới.

## Interview Questions

**Hỏi**: Eventual consistency đảm bảo điều gì và không đảm bảo điều gì?

**Trả lời**: Nó đảm bảo rằng nếu không có ghi mới nào xảy ra thêm, mọi bản sao dữ liệu rồi sẽ hội tụ về cùng một giá trị sau một khoảng thời gian hữu hạn. Nó không đảm bảo thời điểm chính xác dữ liệu hội tụ, không đảm bảo đọc ngay sau ghi thấy giá trị mới (read-after-write), và không đảm bảo các client khác nhau thấy dữ liệu giống nhau tại cùng một thời điểm.

**Hỏi**: Last-write-wins và vector clock khác nhau thế nào khi giải quyết xung đột ghi đồng thời?

**Trả lời**: Last-write-wins so sánh timestamp của hai bản ghi và giữ lại bản có timestamp lớn hơn, đơn giản nhưng rủi ro mất dữ liệu nếu đồng hồ hệ thống giữa các node lệch nhau. Vector clock theo dõi quan hệ nhân quả (causal ordering) giữa các phiên bản bằng một bộ đếm cho mỗi node ghi, cho phép phát hiện chính xác khi nào hai bản ghi thực sự xung đột (concurrent, không có quan hệ nhân quả) thay vì chỉ dựa vào thời gian tuyệt đối — nhưng vector clock không tự động chọn ra một bản thắng, ứng dụng phải tự xử lý khi phát hiện xung đột thật.

**Hỏi**: Vì sao CRDT được coi là giải pháp "không cần điều phối" (coordination-free) cho eventual consistency?

**Trả lời**: CRDT là cấu trúc dữ liệu được thiết kế sao cho phép merge (kết hợp) của hai trạng thái bất kỳ luôn cho ra một kết quả xác định và nhất quán, bất kể thứ tự hay số lần merge — về mặt toán học nó thỏa mãn tính chất giao hoán, kết hợp và lũy đẳng (commutative, associative, idempotent). Nhờ vậy các node có thể nhận ghi độc lập hoàn toàn không cần khóa hay điều phối trung tâm, và khi đồng bộ lại với nhau, kết quả luôn hội tụ đúng mà không cần logic conflict resolution thủ công.

## Summary

Eventual consistency là mô hình nhất quán chấp nhận độ trễ giữa lúc ghi và lúc mọi bản sao dữ liệu phản ánh đúng giá trị đó, đổi lại hệ thống đạt được availability cao và latency ghi thấp vì không phải chờ xác nhận từ toàn bộ (hoặc đa số) node trước khi trả lời client. Cơ chế cốt lõi gồm write cục bộ xác nhận nhanh, lan truyền bất đồng bộ qua gossip hoặc replication log, và conflict resolution (LWW, vector clock, CRDT) để đảm bảo các bản sao hội tụ đúng khi có ghi đồng thời. Trong production, nó phù hợp cho dữ liệu chấp nhận được độ trễ hội tụ (lượt thích, cache, CDN, DNS) nhưng không phù hợp cho dữ liệu tài chính hoặc tồn kho giới hạn cần strong consistency. Cái giá phải trả là ứng dụng phải tự thiết kế cho khả năng đọc thấy dữ liệu cũ và tự chọn chiến lược giải quyết xung đột phù hợp, không thể phó mặc hoàn toàn cho database. Giám sát replication lag chủ động và phân loại dữ liệu theo mức độ chấp nhận rủi ro là yếu tố quyết định giữa một hệ thống vận hành trơn tru và một hệ thống liên tục gây hiểu lầm cho người dùng lẫn đội vận hành.

## Knowledge Graph

- CAP Theorem — eventual consistency là mô hình nhất quán điển hình của các hệ chọn AP khi network partition xảy ra.
- CRDT (Conflict-free Replicated Data Type) — cấu trúc dữ liệu giúp merge các bản sao phân kỳ một cách xác định, không cần điều phối trung tâm.
- Consensus (Raft/Paxos) — hướng tiếp cận đối lập, ưu tiên strong consistency bằng cách yêu cầu majority quorum trước khi commit.
- Vector Clock — cơ chế theo dõi quan hệ nhân quả giữa các phiên bản dữ liệu để phát hiện xung đột ghi đồng thời.
- Read Repair / Anti-Entropy — cơ chế chủ động rút ngắn thời gian hội tụ giữa các bản sao thay vì chỉ dựa vào replication tự nhiên.
- Sharding — kỹ thuật phân tán dữ liệu qua nhiều node, thường kết hợp với eventual consistency để đạt độ sẵn sàng và hiệu năng đọc/ghi cao.

## Five Things To Remember

- Eventual consistency đảm bảo dữ liệu hội tụ về cùng giá trị cuối cùng, không đảm bảo đọc ngay sau ghi thấy giá trị mới.
- Đây là lựa chọn AP có chủ đích trong CAP theorem — đổi consistency tức thời lấy availability và latency thấp.
- Conflict resolution (LWW, vector clock, CRDT) là phần bắt buộc phải thiết kế, không phải chi tiết có thể bỏ qua.
- Chỉ dùng cho dữ liệu chấp nhận được độ trễ hội tụ; dữ liệu tài chính và tồn kho giới hạn cần strong consistency.
- Giám sát replication lag chủ động, đừng đợi khách hàng report dữ liệu sai mới phát hiện vấn đề.
