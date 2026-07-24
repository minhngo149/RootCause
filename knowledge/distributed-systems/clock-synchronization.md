---
id: clock-synchronization
title: Clock Synchronization
tags: ["distributed-systems"]
---

# Clock Synchronization

> Status: Draft

## Problem

Một hệ thống order-processing chạy trên 3 node ở 3 region khác nhau (us-east-1, eu-west-1, ap-southeast-1) dùng timestamp `created_at` lấy từ `System.currentTimeMillis()` của từng node để quyết định thứ tự xử lý đơn hàng và merge dữ liệu khi có xung đột ghi. Một hôm, đồng hồ vật lý của node ở ap-southeast-1 lệch nhanh hơn 400ms so với hai node còn lại do lỗi đồng bộ NTP tạm thời với upstream server — hai đơn hàng thực tế xảy ra cách nhau 50ms theo thứ tự thời gian thật lại bị hệ thống ghi nhận sai thứ tự, khiến logic "đơn hủy phải đến sau đơn tạo" bị đảo ngược và một đơn hàng đã hủy vẫn được xử lý giao hàng. Đội kỹ sư debug hàng giờ vì log của cả 3 node đều "đúng" theo đồng hồ riêng của nó, không ai nghĩ tới khả năng bản thân đồng hồ đã lệch.

## Pain Points

- So sánh timestamp giữa các node để xác định "sự kiện nào xảy ra trước" (event ordering) cho ra kết quả sai khi clock skew vượt quá khoảng cách thời gian thực giữa hai sự kiện, dẫn tới quyết định nghiệp vụ sai (double-spend, đơn hàng xử lý sai thứ tự, session bị coi là hết hạn sớm hoặc muộn hơn thực tế).
- Distributed lock hoặc lease dùng wall clock để tính TTL (vd. `expires_at = now() + 30s`) có thể hết hạn sớm hơn hoặc muộn hơn dự kiến nếu node giữ lock có đồng hồ lệch, mở ra khoảng thời gian hai client cùng tưởng mình giữ lock hợp lệ.
- Debug incident trở nên cực kỳ tốn thời gian vì log từ nhiều node không thể sắp xếp tin cậy theo timestamp — kỹ sư phải đoán thứ tự sự kiện thực sự bằng suy luận nghiệp vụ thay vì đọc trực tiếp từ log, kéo dài MTTR.
- Chi phí vận hành tăng khi hệ thống phải retrofit cơ chế logical clock hoặc hybrid clock sau khi đã xảy ra sự cố do wall clock, thay vì thiết kế đúng từ đầu — việc thay đổi cơ chế sắp thứ tự sự kiện ở một hệ thống production đang chạy phức tạp và rủi ro hơn nhiều so với chọn đúng ngay từ thiết kế ban đầu.

## Solution

Không có đồng hồ vật lý (wall clock) nào giữa các node trong hệ phân tán là hoàn toàn đồng bộ tuyệt đối — NTP (Network Time Protocol) chỉ đồng bộ đồng hồ về một sai số chấp nhận được (thường vài mili-giây trong mạng LAN, có thể tới hàng chục-hàng trăm mili-giây qua WAN hoặc khi network congestion), không bao giờ về 0. Vì vậy, với các quyết định mà thứ tự chính xác của sự kiện quan trọng về mặt nghiệp vụ hoặc tính đúng đắn (causality), hệ thống không nên dựa vào wall clock để so sánh "cái gì xảy ra trước" giữa hai node khác nhau. Logical clock (Lamport timestamp) và vector clock là các cơ chế đánh số sự kiện dựa trên quan hệ nhân-quả (happens-before) thay vì dựa trên thời gian vật lý, cho phép xác định thứ tự sự kiện một cách chính xác và nhất quán bất kể đồng hồ vật lý của các node lệch nhau bao nhiêu.

## How It Works

**NTP và giới hạn của nó**: NTP đồng bộ đồng hồ của một máy với một time server tham chiếu bằng cách gửi request và đo round-trip time (RTT), giả định độ trễ đi và về là đối xứng để ước lượng offset. Trong điều kiện lý tưởng (mạng ổn định, RTT thấp), NTP có thể đạt độ chính xác vài mili-giây; nhưng khi RTT bất đối xứng (route đi và về khác nhau, network congestion một chiều), hoặc khi client bị cô lập khỏi time server tạm thời, sai số có thể lên tới hàng chục hoặc hàng trăm mili-giây mà không có cách nào phát hiện từ phía client. NTP còn có cơ chế **step** (nhảy đồng hồ tức thời khi lệch quá lớn) và **slew** (điều chỉnh dần dần) — nếu cấu hình cho phép step, đồng hồ hệ thống có thể đột ngột nhảy lùi hoặc tiến vài trăm mili-giây tới vài giây, phá vỡ giả định "thời gian luôn tăng đơn điệu" mà nhiều đoạn code ứng dụng ngầm định (vd. tính duration bằng `end_time - start_time` có thể ra số âm). Ngay cả Google TrueTime (dùng GPS và atomic clock ở mỗi datacenter) cũng không claim đồng bộ tuyệt đối — nó trả về một khoảng bất định `[earliest, latest]` thay vì một giá trị đơn, và Spanner phải chủ động chờ hết khoảng bất định đó (`commit wait`) trước khi công nhận một transaction, chứng minh rằng ngay cả hạ tầng phần cứng chuyên dụng cũng chỉ giới hạn được sai số chứ không loại bỏ được nó.

**Vì sao không thể tin tuyệt đối wall clock giữa các node**: Ngoài sai số NTP, đồng hồ vật lý còn trôi (clock drift) do dao động thạch anh của phần cứng không hoàn hảo, tốc độ trôi có thể khác nhau giữa các máy (vài chục ppm là bình thường, tương đương vài mili-giây mỗi phút nếu không đồng bộ lại). Hai sự kiện xảy ra trên hai node khác nhau với khoảng cách thời gian thực nhỏ hơn tổng sai số đồng hồ của hai node đó là không thể so sánh tin cậy bằng timestamp — thứ tự quan sát được có thể ngược với thứ tự thực tế. Đây là lý do khái niệm "happens-before" (Lamport, 1978) được định nghĩa không dựa trên thời gian vật lý mà dựa trên quan hệ nhân quả: sự kiện A happens-before sự kiện B nếu A và B cùng xảy ra trên một tiến trình theo đúng thứ tự chương trình, hoặc A là việc gửi một message và B là việc nhận message đó, hoặc bắc cầu qua một sự kiện thứ ba.

**Logical clock (Lamport timestamp)**: Mỗi node giữ một bộ đếm số nguyên `L`, tăng lên 1 mỗi khi có sự kiện cục bộ xảy ra. Khi gửi message, node đính kèm giá trị `L` hiện tại. Khi nhận message mang timestamp `L_msg`, node cập nhật `L = max(L, L_msg) + 1`. Tính chất đảm bảo: nếu A happens-before B thì `L(A) < L(B)` — nhưng chiều ngược lại không đúng (`L(A) < L(B)` không suy ra A happens-before B, vì hai sự kiện độc lập/concurrent vẫn có thể có Lamport timestamp khác nhau do trùng hợp). Đây là giới hạn cơ bản của Lamport clock: nó cho một thứ tự toàn phần (total order) trên mọi sự kiện, đủ dùng để phá vỡ tie-break, nhưng không tự nó phân biệt được quan hệ nhân quả thật với hai sự kiện chỉ đơn thuần độc lập.

**Vector clock**: Khắc phục giới hạn trên bằng cách mỗi node trong hệ N node giữ một vector số nguyên có N phần tử `V[1..N]`, mỗi phần tử đếm số sự kiện đã biết của từng node. Khi node `i` có sự kiện cục bộ, nó tăng `V[i]`. Khi gửi message, node đính kèm toàn bộ vector hiện tại. Khi nhận message mang vector `V_msg`, node cập nhật `V[k] = max(V[k], V_msg[k])` với mọi `k`, rồi tăng `V[i]` của chính nó. So sánh hai vector clock `V_A` và `V_B`: nếu `V_A[k] <= V_B[k]` với mọi `k` (và có ít nhất một `k` mà bất đẳng thức chặt), thì A happens-before B; nếu không bên nào "nhỏ hơn hoặc bằng" bên kia theo mọi thành phần, hai sự kiện là concurrent (không có quan hệ nhân quả, xung đột thật sự cần resolve). Đây chính là cơ chế Dynamo và Riak dùng để phát hiện xung đột ghi thật (cần merge/resolve) và phân biệt với việc chỉ đơn thuần một bản ghi cũ hơn bản ghi khác.

## Production Architecture

Trong một cụm database phân tán kiểu Dynamo (Riak, Voldemort, hay chính Amazon DynamoDB thế hệ đầu), mỗi giá trị được lưu kèm một vector clock; khi client đọc thấy nhiều phiên bản có vector clock concurrent (không so sánh được), hệ thống trả về cả các phiên bản xung đột cho ứng dụng tự quyết định cách merge (ví dụ hợp nhất giỏ hàng thay vì chọn một bản ghi đè lên bản kia) thay vì âm thầm chọn theo last-write-wins dựa trên wall clock — vốn có thể mất dữ liệu nếu clock của node ghi sau thực ra lại chậm hơn node ghi trước. Google Spanner giải quyết bài toán tương tự ở tầng transaction bằng TrueTime API kết hợp GPS/atomic clock để có khoảng bất định cực nhỏ (thường vài mili-giây), rồi dùng cơ chế `commit wait` — chờ đủ lâu để đảm bảo khoảng bất định của transaction đã trôi qua trước khi công nhận commit — cho phép Spanner đạt external consistency (linearizability) trên quy mô toàn cầu mà vẫn dựa trên wall clock, đánh đổi bằng latency thêm vài mili-giây mỗi write và đầu tư phần cứng atomic clock chuyên dụng ở mỗi datacenter. Ở tầng ứng dụng phổ biến hơn, các hệ thống log tập trung (distributed tracing như Jaeger, Zipkin) gắn `trace_id`/`span_id` theo cấu trúc cây nhân quả (parent-child span) thay vì chỉ dựa vào timestamp để dựng lại thứ tự request đi qua nhiều service — bản chất đây là một dạng logical ordering tương tự happens-before, chỉ khác biểu diễn.

## Trade-offs

Lamport clock rẻ (chỉ một số nguyên, chi phí truyền tải gần như bằng 0) nhưng chỉ cho biết thứ tự toàn phần chứ không phân biệt được nhân quả thật với ngẫu nhiên trùng hợp — không đủ để phát hiện xung đột ghi thật sự cần resolve. Vector clock giải quyết được vấn đề đó nhưng kích thước vector tăng tuyến tính theo số node tham gia (N phần tử cho N node), gây chi phí lưu trữ và băng thông đáng kể trong hệ thống có hàng nghìn node hoặc khi client động (client cũng tham gia vector) khiến N không cố định — đây là lý do các hệ thống production lớn thường dùng biến thể rút gọn như dotted version vector hoặc giới hạn kích thước vector bằng cách prune các entry cũ. TrueTime/Spanner đạt độ chính xác cao nhất nhưng đòi hỏi đầu tư phần cứng chuyên dụng (GPS receiver, atomic clock) ở mọi datacenter — không khả thi cho phần lớn tổ chức ngoài các nhà cung cấp cloud lớn, và vẫn phải trả giá bằng latency chờ (`commit wait`) trên mọi transaction. Hybrid Logical Clock (HLC) là điểm cân bằng phổ biến trong thực tế — kết hợp wall clock (để timestamp vẫn có ý nghĩa gần đúng về thời gian thực, tiện cho con người debug) với một logical counter (để đảm bảo tính đơn điệu và capture causality) — nhưng vẫn không tránh được hoàn toàn giới hạn về kích thước thông tin cần truyền khi so sánh nhân quả chính xác tuyệt đối như vector clock đầy đủ.

## Best Practices

- Không dùng wall clock timestamp để quyết định thứ tự sự kiện có ý nghĩa nghiệp vụ (ai ghi trước, ai hủy trước) khi các sự kiện đến từ nhiều node khác nhau — dùng Lamport timestamp, vector clock, hoặc ít nhất là một sequence number tập trung (do một node/service duy nhất phát hành).
- Với distributed lock/lease, tính TTL dựa trên thời gian tương đối đo trên cùng một node (monotonic clock, ví dụ `System.nanoTime()` trong Java hay `CLOCK_MONOTONIC` trong Linux) thay vì dựa vào wall clock có thể bị step ngược bất cứ lúc nào.
- Giám sát NTP offset và sync status như một metric hạ tầng cấp production (`ntpq -p`, `chronyc tracking`, hoặc CloudWatch/Datadog clock skew alert) — coi một node mất đồng bộ NTP quá ngưỡng là sự cố cần alert, không phải chi tiết vận hành có thể bỏ qua.
- Khi cần phát hiện xung đột ghi thật sự trong hệ multi-master, dùng vector clock hoặc CRDT thay vì last-write-wins dựa trên timestamp — LWW đơn giản nhưng âm thầm mất dữ liệu khi clock skew đủ lớn.
- Với hệ thống log/tracing phân tán, ưu tiên cấu trúc nhân quả tường minh (parent span ID, causal metadata) thay vì chỉ dựa vào timestamp để sắp xếp lại thứ tự sự kiện khi debug.

## Common Mistakes

- Giả định NTP giữ đồng hồ đồng bộ gần như tuyệt đối và dùng `now()` để so sánh thứ tự sự kiện giữa các node như thể chúng chia sẻ một đồng hồ chung — sai số NTP qua WAN hoàn toàn có thể vượt quá khoảng cách thời gian thực giữa hai sự kiện liên quan.
- Dùng wall clock (không phải monotonic clock) để đo duration hoặc TTL trong cùng một tiến trình, khiến kết quả có thể âm hoặc sai lệch lớn khi NTP step xảy ra giữa lúc đo.
- Triển khai last-write-wins dựa trên timestamp cho hệ multi-master mà không kiểm tra khả năng clock skew giữa các node ghi — mất dữ liệu âm thầm vì bản ghi "mới hơn" theo đồng hồ sai lại đè lên bản ghi thực sự mới hơn.
- Nhầm lẫn Lamport timestamp giống nhau hoặc `L(A) < L(B)` với quan hệ nhân quả thật — hai sự kiện concurrent hoàn toàn có thể có Lamport timestamp khác nhau mà không có quan hệ happens-before nào giữa chúng.
- Dùng vector clock đầy đủ cho hệ thống có số lượng client/node không giới hạn hoặc tăng liên tục (ví dụ mỗi client di động là một thành phần vector) mà không có cơ chế prune, khiến vector phình to vô hạn theo thời gian.

## Interview Questions

**Hỏi**: Tại sao không thể dùng `System.currentTimeMillis()` để xác định sự kiện nào xảy ra trước trong một hệ phân tán nhiều node?

**Trả lời**: Vì đồng hồ vật lý của các node không bao giờ đồng bộ tuyệt đối — NTP chỉ giảm sai số xuống một mức chấp nhận được (vài mili-giây tới vài trăm mili-giây tùy điều kiện mạng), không loại bỏ hoàn toàn. Nếu khoảng cách thời gian thực giữa hai sự kiện nhỏ hơn tổng sai số đồng hồ của hai node phát sinh sự kiện đó, thứ tự quan sát qua timestamp có thể sai so với thứ tự thực tế, dẫn tới quyết định nghiệp vụ sai.

**Hỏi**: Lamport timestamp và vector clock khác nhau ở điểm nào, và tại sao đôi khi cần vector clock dù Lamport clock rẻ hơn?

**Trả lời**: Lamport timestamp cho một thứ tự toàn phần trên mọi sự kiện (`L(A) < L(B)` khi A happens-before B) nhưng không đủ để suy ngược — hai sự kiện Lamport timestamp khác nhau vẫn có thể hoàn toàn concurrent (không có quan hệ nhân quả). Vector clock, với một bộ đếm riêng cho mỗi node, cho phép so sánh chính xác: nếu vector này không "nhỏ hơn hoặc bằng" vector kia theo mọi thành phần và ngược lại cũng vậy, hai sự kiện chắc chắn concurrent — cần thiết khi hệ thống phải phân biệt xung đột ghi thật sự (cần merge) với việc chỉ đơn thuần một bản ghi cũ hơn.

**Hỏi**: Google Spanner vẫn dùng wall clock (TrueTime) để đạt external consistency toàn cầu — vậy nó tránh vấn đề clock uncertainty như thế nào?

**Trả lời**: TrueTime không claim biết chính xác thời gian hiện tại, mà trả về một khoảng bất định `[earliest, latest]` được giới hạn chặt nhờ GPS và atomic clock chuyên dụng ở mỗi datacenter. Trước khi công nhận một transaction đã commit, Spanner chủ động chờ (`commit wait`) cho tới khi chắc chắn khoảng bất định đó đã trôi qua, đảm bảo timestamp gán cho transaction phản ánh đúng thứ tự thực tế dù đồng hồ vật lý vẫn có sai số — đánh đổi bằng latency thêm vài mili-giây mỗi write.

## Summary

Đồng hồ vật lý giữa các node trong hệ phân tán không bao giờ đồng bộ tuyệt đối — NTP chỉ giảm sai số xuống một mức chấp nhận được, và sai số này có thể đủ lớn để đảo ngược thứ tự quan sát của các sự kiện thực sự gần nhau về thời gian. Vì vậy, các quyết định phụ thuộc vào thứ tự sự kiện chính xác (ai ghi trước, phát hiện xung đột ghi, distributed lock TTL) không nên dựa trên wall clock timestamp một cách ngây thơ. Lamport timestamp cho một thứ tự toàn phần dựa trên happens-before với chi phí thấp nhưng không phân biệt được nhân quả thật với trùng hợp; vector clock giải quyết vấn đề đó với chi phí không gian tăng theo số node. Các hệ thống production như Dynamo/Riak dùng vector clock để phát hiện xung đột ghi thật, còn Spanner đầu tư phần cứng atomic clock (TrueTime) và cơ chế commit wait để vẫn dùng được wall clock một cách an toàn ở quy mô toàn cầu. Lựa chọn cơ chế đúng — logical clock, vector clock, hay hybrid logical clock — phụ thuộc vào việc hệ thống cần thứ tự chính xác đến đâu và sẵn sàng trả giá bao nhiêu về độ phức tạp và chi phí hạ tầng.

## Knowledge Graph

- CAP Theorem — network partition cũng là nguyên nhân khiến các node mất đồng bộ cả về dữ liệu lẫn về thời gian nhận biết trạng thái của nhau.
- Consensus (Raft, Paxos) — nhiều thuật toán đồng thuận dùng logical term/epoch number thay vì wall clock để xác định thứ tự leader hợp lệ.
- Distributed Locking — TTL của lock/lease là điểm mà lỗi giả định về wall clock gây hậu quả trực tiếp nhất (hai client cùng tưởng giữ lock).
- Eventual Consistency — vector clock là cơ chế phổ biến để phát hiện và resolve xung đột trong các hệ theo đuổi eventual consistency.
- CRDT (Conflict-free Replicated Data Type) — cấu trúc dữ liệu tự động merge xung đột, thường dùng cùng hoặc thay thế vector clock trong hệ multi-master.
- Distributed Tracing — cấu trúc parent-child span là một dạng biểu diễn khác của quan hệ nhân quả happens-before, phục vụ debug thứ tự request qua nhiều service.

## Five Things To Remember

- NTP giảm sai số đồng hồ chứ không loại bỏ hoàn toàn — luôn tồn tại clock skew giữa các node.
- Không dùng wall clock để so sánh thứ tự sự kiện giữa các node khi tính đúng đắn nghiệp vụ phụ thuộc vào thứ tự đó.
- Lamport timestamp cho thứ tự toàn phần nhưng không suy ngược được quan hệ nhân quả; vector clock thì có thể.
- Vector clock phát hiện được xung đột ghi thật sự (concurrent events), điều mà last-write-wins theo timestamp không làm được.
- TTL của lock/lease nên dùng monotonic clock trên cùng một node, không dùng wall clock có thể bị step ngược bất cứ lúc nào.
