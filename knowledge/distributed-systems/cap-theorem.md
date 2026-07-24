---
id: cap-theorem
title: CAP Theorem
tags: ["distributed-systems"]
---

# CAP Theorem

> Status: Draft

## Problem

Một hệ thống thanh toán chạy trên 3 node database đặt ở 3 availability zone khác nhau để chống chịu sự cố hạ tầng. Một ngày, liên kết mạng giữa AZ-1 và AZ-2/AZ-3 bị đứt do lỗi router của nhà cung cấp cloud — hai phía vẫn sống, vẫn nhận được request từ client, nhưng không còn thấy nhau. Đội vận hành đứng trước một quyết định không thể trì hoãn: cho phép AZ-1 tiếp tục nhận ghi (chấp nhận có thể lệch dữ liệu so với hai AZ kia), hay chặn ghi ở AZ-1 cho tới khi mạng khôi phục (chấp nhận một phần khách hàng không giao dịch được). Không có lựa chọn thứ ba giữ được cả hai — và nếu kiến trúc không được thiết kế sẵn cho tình huống này, quyết định bị đưa ra một cách ngẫu hứng giữa lúc khủng hoảng, thường sai.

## Pain Points

- Đội ngũ chọn nhầm mô hình (ví dụ dùng database CP cho giỏ hàng e-commerce) khiến toàn bộ tính năng "thêm vào giỏ" trả lỗi 503 mỗi khi có network partition dù chỉ cần eventual consistency là đủ.
- Ngược lại, dùng mô hình AP cho số dư tài khoản ngân hàng dẫn tới hai node cho phép rút tiền đồng thời từ hai bản sao lệch nhau, gây double-spend phải xử lý thủ công sau sự cố.
- Không hiểu CAP dẫn tới kỳ vọng sai về SLA: đội phát triển hứa "always consistent và luôn sẵn sàng" trong tài liệu thiết kế — một cam kết không thể giữ được khi partition xảy ra, và khi nó xảy ra thật, hệ thống sụp đổ theo cách không ai dự đoán trước.
- Chi phí incident-response tăng vì không có runbook cho network partition — đội vận hành phải tranh luận trực tiếp trong lúc outage về việc ưu tiên consistency hay availability, thay vì hệ thống đã tự động hành xử theo thiết kế từ trước.

## Solution

CAP theorem (Eric Brewer, 2000; chứng minh hình thức bởi Gilbert và Lynch, 2002) phát biểu rằng một hệ thống dữ liệu phân tán không thể đồng thời đảm bảo cả ba tính chất: **Consistency** (mọi node đọc thấy cùng một giá trị mới nhất tại mọi thời điểm — linearizability), **Availability** (mọi request tới một node còn sống đều nhận được phản hồi, không timeout, không lỗi), và **Partition tolerance** (hệ thống vẫn hoạt động khi liên kết mạng giữa các node bị đứt). Vì trong mọi hệ phân tán thực tế, network partition là điều chắc chắn sẽ xảy ra chứ không phải nếu, câu hỏi thực sự không phải "chọn 2 trong 3" một cách tĩnh, mà là: khi partition xảy ra, hệ thống chọn hy sinh Consistency (trở thành AP) hay hy sinh Availability (trở thành CP).

## How It Works

Khi network partition xảy ra, cluster bị chia thành các phân vùng (partition) không còn thấy nhau. Với mỗi request ghi hoặc đọc tới một node nằm trong một phân vùng, node đó có đúng hai lựa chọn: (1) từ chối phục vụ hoặc chặn cho tới khi có thể xác nhận với quorum/majority phía bên kia — đây là lựa chọn CP, hy sinh availability để giữ consistency, vì node biết dữ liệu nó thấy có thể đã cũ hoặc không đại diện cho trạng thái mới nhất toàn cục; hoặc (2) vẫn phục vụ request bằng dữ liệu cục bộ hiện có — đây là lựa chọn AP, hy sinh consistency (dữ liệu trả về có thể stale hoặc xung đột với phân vùng kia) để giữ availability. Cơ chế cụ thể để đạt CP thường dựa trên thuật toán đồng thuận (Raft, Paxos) yêu cầu ghi phải được xác nhận bởi quá bán (majority quorum, ví dụ 2/3 hoặc 3/5 node) trước khi coi là thành công — nếu một phân vòng không hội đủ quorum (thiểu số/minority), nó buộc phải từ chối ghi, có khi cả đọc, cho tới khi majority được khôi phục. Cơ chế để đạt AP thường dựa trên vector clock hoặc last-write-wins để mỗi node vẫn tự do ghi độc lập, và khi mạng nối lại, hệ thống chạy conflict resolution (merge, CRDT, hoặc để ứng dụng tự xử lý xung đột) để hội tụ dữ liệu về một trạng thái nhất quán — quá trình này gọi là eventual consistency. Điều quan trọng cần hiểu: khi không có partition, một hệ CP hay AP đều có thể vừa consistent vừa available bình thường — CAP chỉ thực sự bắt đội ngũ phải chọn khi và chỉ khi partition đang xảy ra.

## Production Architecture

MongoDB (với replica set dùng election dựa trên majority) và ZooKeeper/etcd (dùng Raft) là các hệ CP điển hình — khi phân vùng thiểu số không thấy được primary/leader, nó tự chuyển sang trạng thái không nhận ghi cho tới khi bầu lại được leader mới hoặc majority được khôi phục; đây là lý do etcd được chọn làm nơi lưu leader election và service discovery cho Kubernetes, vì thà API server tạm thời không lấy được lock còn hơn hai node cùng tưởng mình là leader. Cassandra và DynamoDB là các hệ AP điển hình — mỗi node trong một phân vùng vẫn nhận ghi độc lập với tunable consistency level (`ONE`, `QUORUM`, `ALL` trong Cassandra), phù hợp cho dữ liệu như session, giỏ hàng, hoặc feed hoạt động mạng xã hội nơi hiển thị dữ liệu hơi cũ vài trăm mili-giây tốt hơn là trả lỗi cho người dùng. Trong kiến trúc thực tế của một sàn thương mại điện tử, số dư ví và trạng thái đơn hàng đã thanh toán thường lưu trên hệ CP (PostgreSQL với synchronous replication, hoặc CockroachDB dùng Raft), trong khi giỏ hàng, lịch sử xem sản phẩm, và cache session lưu trên hệ AP (DynamoDB, Redis Cluster) — cùng một hệ thống áp dụng CAP khác nhau cho từng loại dữ liệu tùy mức độ chấp nhận rủi ro nghiệp vụ.

## Trade-offs

- Chọn CP nghĩa là chấp nhận downtime cục bộ mỗi khi partition xảy ra — phân vùng thiểu số ngừng phục vụ hoàn toàn, kể cả khi phần lớn dữ liệu nó có vẫn đúng và người dùng phía đó không làm gì sai.
- Chọn AP nghĩa là chấp nhận dữ liệu có thể tạm thời không nhất quán giữa các node, và hệ thống (hoặc ứng dụng) phải tự xây dựng logic conflict resolution — độ phức tạp không biến mất, chỉ chuyển từ tầng hạ tầng sang tầng ứng dụng.
- Quorum-based CP (majority write) đánh đổi latency lấy consistency ngay cả khi không có partition — mọi write phải chờ xác nhận từ đa số node thay vì trả về ngay sau khi node đầu tiên ghi xong.
- PACELC (mở rộng của CAP) chỉ ra rằng ngay cả khi không có partition (else — E), hệ thống vẫn phải đánh đổi giữa latency (L) và consistency (C) — nghĩa là CAP không phải toàn bộ câu chuyện, chỉ mô tả hành vi lúc partition, còn lúc bình thường vẫn có đánh đổi riêng.
- "Chọn 2 trong 3" là cách diễn đạt phổ biến nhưng gây hiểu lầm: Partition tolerance không phải một lựa chọn tùy chọn trong hệ phân tán qua mạng thật (mạng luôn có thể đứt), nên thực chất chỉ có đúng một trục lựa chọn là C hay A khi P xảy ra.

## Best Practices

- Phân loại dữ liệu theo mức độ chấp nhận rủi ro nghiệp vụ trước khi chọn database — số dư tài khoản, tồn kho, trạng thái thanh toán cần CP; session, cache, feed, phân tích hành vi có thể chấp nhận AP.
- Với các database hỗ trợ tunable consistency (Cassandra, DynamoDB, CockroachDB), cấu hình consistency level theo từng loại truy vấn thay vì áp dụng một mức cho toàn hệ thống.
- Viết runbook rõ ràng cho kịch bản network partition — hệ thống nên hành xử theo thiết kế đã quyết định từ trước (fail closed hay fail open), không để đội vận hành phải quyết định giữa lúc incident.
- Đo và giám sát chỉ số quorum health (số node còn trong majority) như một signal cảnh báo sớm, vì mất quorum tạm thời thường là dấu hiệu network partition đang hình thành trước khi nó gây downtime rõ rệt.
- Với hệ AP, đầu tư vào cơ chế conflict resolution rõ ràng (CRDT, vector clock, hoặc quy tắc nghiệp vụ tường minh như "cộng dồn số lượng thay vì ghi đè") thay vì mặc định last-write-wins một cách ngầm định.

## Common Mistakes

- Coi CAP là "chọn 2 trong 3" theo nghĩa tĩnh và cố xây một hệ thống vừa CP vừa AP toàn thời gian — bỏ qua việc P không phải tùy chọn, và sự đánh đổi thật chỉ xuất hiện khi partition xảy ra.
- Dùng một database CP (ví dụ MongoDB cấu hình majority write concern) cho toàn bộ hệ thống kể cả các phần chỉ cần AP, khiến những tính năng lẽ ra không quan trọng (như đếm lượt xem) cũng bị timeout mỗi khi cluster gặp trục trặc mạng nhỏ.
- Nhầm CAP với ACID — CAP nói về hành vi hệ phân tán khi có network partition, ACID nói về tính chất transaction trên một database; một hệ CP không tự động có ACID, và một hệ AP không có nghĩa là vi phạm ACID trong phạm vi một node.
- Bỏ qua PACELC và chỉ tối ưu cho kịch bản partition (hiếm) mà quên rằng latency-consistency trade-off tồn tại thường trực ngay cả khi mạng hoàn toàn khỏe mạnh.
- Không kiểm thử hành vi hệ thống thực tế khi partition xảy ra (qua chaos engineering, network fault injection) — giả định lý thuyết về CP/AP của database không phải lúc nào cũng khớp với cấu hình thực tế đang chạy production.

## Interview Questions

**Hỏi**: CAP theorem nói "chọn 2 trong 3", vậy tại sao nói chỉ có một trục lựa chọn thực sự là C hay A?

**Trả lời**: Vì trong bất kỳ hệ phân tán nào giao tiếp qua mạng thật, network partition là điều chắc chắn xảy ra ở một thời điểm nào đó, không phải một lựa chọn thiết kế có thể bỏ qua. Do đó P luôn phải được chấp nhận là có thể xảy ra, và câu hỏi thực tế chỉ còn lại là: khi P xảy ra, hệ thống ưu tiên C (chặn phục vụ để giữ nhất quán) hay A (vẫn phục vụ nhưng chấp nhận dữ liệu có thể lệch).

**Hỏi**: PACELC bổ sung gì cho CAP mà CAP không nói tới?

**Trả lời**: CAP chỉ mô tả hành vi hệ thống khi có network partition (P). PACELC chỉ ra rằng ngay cả khi không có partition (Else — E), hệ thống vẫn phải đánh đổi giữa Latency (L) và Consistency (C) — ví dụ chờ xác nhận majority trước khi trả kết quả write luôn chậm hơn trả ngay sau khi node đầu tiên ghi xong. Nói cách khác, CAP chỉ đúng lúc khủng hoảng, PACELC mô tả đánh đổi cả lúc bình thường.

**Hỏi**: Vì sao etcd/ZooKeeper được thiết kế là CP thay vì AP, trong khi đó lại là thành phần hạ tầng cần rất cao availability?

**Trả lời**: etcd/ZooKeeper thường dùng để lưu trạng thái cần tuyệt đối duy nhất và không mâu thuẫn — ví dụ leader election, distributed lock, service discovery. Nếu chọn AP, một network partition có thể khiến hai phân vùng cùng tưởng mình đang giữ lock hoặc cùng bầu ra hai leader khác nhau (split-brain), hậu quả nghiêm trọng hơn nhiều so với việc tạm thời từ chối phục vụ một phần request cho tới khi quorum được khôi phục. Availability toàn cục của hệ thống lớn hơn (như Kubernetes) chấp nhận phụ thuộc vào một etcd cluster CP nhỏ, vì cái giá của một split-brain cao hơn cái giá của một khoảng downtime ngắn cục bộ.

## Summary

CAP theorem phát biểu rằng khi network partition xảy ra trong hệ phân tán, hệ thống buộc phải chọn giữa Consistency (chặn phục vụ để giữ dữ liệu nhất quán) và Availability (vẫn phục vụ nhưng chấp nhận dữ liệu có thể lệch) — vì Partition tolerance không phải một lựa chọn, nó là thực tế không tránh khỏi của mọi hệ thống giao tiếp qua mạng. Hệ CP (MongoDB, etcd, ZooKeeper, CockroachDB) dùng quorum/consensus để đảm bảo mọi node thấy cùng dữ liệu, đổi lấy downtime cục bộ khi thiểu số mất kết nối; hệ AP (Cassandra, DynamoDB) giữ mọi node luôn phục vụ được, đổi lấy khả năng dữ liệu tạm thời không đồng nhất cho tới khi hội tụ. Lựa chọn đúng phụ thuộc vào mức độ chấp nhận rủi ro của từng loại dữ liệu nghiệp vụ, không phải một quyết định áp dụng chung cho toàn hệ thống. PACELC mở rộng CAP bằng cách chỉ ra đánh đổi latency-consistency vẫn tồn tại ngay cả khi không có partition. Runbook và kiểm thử chủ động cho kịch bản partition quan trọng hơn việc chỉ hiểu lý thuyết suông, vì quyết định sai giữa lúc incident thường tốn kém hơn nhiều so với thiết kế đúng từ đầu.

## Knowledge Graph

- PACELC — mở rộng CAP, mô tả thêm đánh đổi latency-consistency ngay cả khi không có partition.
- Consensus Algorithms (Raft, Paxos) — cơ chế kỹ thuật để đạt CP thông qua majority quorum.
- Eventual Consistency — mô hình nhất quán điển hình của các hệ chọn AP.
- ACID — tính chất transaction trên một database đơn, khác trục với CAP vốn nói về hệ phân tán nhiều node.
- Sharding — kỹ thuật phân tán dữ liệu qua nhiều instance, mỗi shard/replica set có thể tự áp dụng lựa chọn CAP riêng.
- Split-Brain — hậu quả cụ thể khi một hệ đáng lẽ CP bị cấu hình hoặc vận hành sai thành hành xử như AP trong lúc partition.

## Five Things To Remember

- Partition tolerance không phải lựa chọn — mạng thật luôn có thể đứt, nên trục lựa chọn thực sự chỉ là C hay A.
- CP hy sinh availability để giữ dữ liệu nhất quán tuyệt đối; AP hy sinh nhất quán tức thời để luôn phục vụ được request.
- Không có một câu trả lời đúng cho toàn hệ thống — phân loại dữ liệu theo rủi ro nghiệp vụ rồi chọn CP hay AP cho từng phần.
- PACELC nhắc rằng đánh đổi latency-consistency vẫn tồn tại kể cả khi không có partition.
- Viết sẵn runbook cho network partition, đừng để đội vận hành quyết định C hay A giữa lúc khủng hoảng.
