---
id: system-design-roadmap
title: System Design Roadmap
tags: ["roadmap", "system-design"]
---

# System Design Roadmap

> Status: Draft

## Problem

Phần lớn kỹ sư backend học được cách viết một API CRUD, tối ưu một query, hoặc fix một bug — nhưng không có ai dạy họ cách trả lời câu hỏi "hệ thống này sẽ vỡ ở đâu khi traffic tăng 10 lần, và tại sao". Khoảng trống này không phải thiếu kiến thức component (Redis, Kafka, PostgreSQL ai cũng biết tên) mà thiếu năng lực ghép các component đó lại thành một hệ thống chịu được tải thật, chịu được lỗi thật, và vẫn vận hành được khi một trong các phần phụ thuộc chết giữa chừng. Một team thiếu người có năng lực này sẽ liên tục thiết kế lại kiến trúc sau khi đã build xong và gặp sự cố, thay vì thiết kế đúng từ đầu.

## Pain Points

- Kỹ sư giỏi code nhưng không ước lượng được capacity (QPS, dung lượng dữ liệu tăng theo thời gian, kích thước connection pool cần thiết) — hệ thống chạy tốt ở demo, sập ở ngày Black Friday đầu tiên.
- Không phân biệt được khi nào cần strong consistency (số dư tài khoản) và khi nào eventual consistency là chấp nhận được (số lượt like) — dẫn đến over-engineer chỗ không cần hoặc under-engineer chỗ bắt buộc.
- Thiết kế hệ thống không có kế hoạch cho failure mode (retry storm, cascading failure khi một dependency chậm) — một service downstream chậm 2 giây kéo sập toàn bộ hệ thống phía trên do thread pool bị chiếm hết.
- Không biết trình bày trade-off bằng design doc/RFC, dẫn tới quyết định kiến trúc được đưa ra trong buổi họp bằng cảm tính, không ai chịu trách nhiệm khi sai, và không có tài liệu để tra cứu lại sau này.
- Tổ chức thiếu người ở trình độ này buộc phải thuê ngoài hoặc leader tự gánh mọi quyết định kiến trúc, tạo bottleneck và single point of failure về mặt con người.

## Solution

Lộ trình system design là quá trình xây dựng năng lực từ hiểu khái niệm nền tảng (scalability, CAP theorem, consistency models, latency vs throughput) tới khả năng thiết kế một hệ thống thực tế đáp ứng yêu cầu về scale, availability, và chi phí vận hành — rồi bảo vệ được các quyết định đó trước review. Đây là năng lực phân biệt rõ nhất giữa kỹ sư mid-level (implement đúng theo spec có sẵn) và senior/staff engineer (tự đặt ra spec đúng, lường trước hệ quả ở quy mô lớn). Nó không phải một khóa học hoàn thành một lần mà là một trục năng lực tích lũy qua từng hệ thống thực sự đã vận hành, đã gặp sự cố, và đã được rút kinh nghiệm.

## How It Works

Lộ trình chia thành bốn trục năng lực phát triển song song, không tuần tự cứng nhắc:

**Trục 1 — Nền tảng lý thuyết.** Scalability (vertical vs horizontal scaling, stateless vs stateful service), CAP theorem (khi network partition xảy ra, chọn Consistency hay Availability — PACELC mở rộng thêm trade-off latency khi không có partition), consistency models (strong, eventual, causal), và các con số latency cơ bản (RAM ~100ns, SSD ~100μs, round-trip trong cùng datacenter ~0.5ms, round-trip liên vùng ~150ms) — những con số này quyết định kiến trúc chứ không phải lý thuyết suông.

**Trục 2 — Building blocks và cách chúng thất bại.** Load balancer (L4 vs L7, thuật toán round-robin/least-connections/consistent hashing), cache (cache-aside, write-through, invalidation, thundering herd), message queue (at-least-once vs exactly-once, backpressure), database (khi nào shard, khi nào cần read replica, replication lag ảnh hưởng gì tới read-your-writes), và quan trọng nhất — mỗi thành phần này thất bại như thế nào (cache miss storm, queue backlog, replica lag đọc dữ liệu cũ) chứ không chỉ nó hoạt động như thế nào lúc bình thường.

**Trục 3 — Ước lượng và đánh đổi.** Back-of-envelope capacity estimation (QPS trung bình vs peak, storage growth theo năm, bandwidth), rồi từ estimation đó suy ra kiến trúc cần thiết — không thiết kế theo cảm tính mà theo con số. Đi kèm là khả năng trình bày trade-off rõ ràng bằng văn bản: tại sao chọn kiến trúc A thay vì B, chi phí là gì, rủi ro nằm ở đâu, ai sẽ chịu trách nhiệm nếu rủi ro đó xảy ra.

**Trục 4 — Vận hành thực tế.** Observability (làm sao biết hệ thống đang hỏng trước khi khách hàng report), incident response (runbook, rollback plan), và feedback loop từ production trở lại thiết kế — đây là trục chỉ tích lũy được qua thời gian thực sự on-call và thực sự sửa sự cố, không học được từ sách.

## Production Architecture

Trong một tổ chức trưởng thành, năng lực system design thể hiện qua design doc/RFC bắt buộc trước khi build bất kỳ hệ thống nào có ảnh hưởng rộng (thêm service mới, đổi schema database chính, thay đổi giao thức giữa các service) — tài liệu này được review bởi staff/principal engineer hoặc architecture review board trước khi cấp phép triển khai. Kỹ sư ở trình độ system design tốt thường là người viết RFC, dẫn dắt buổi design review, và là người được kéo vào khi có sự cố lớn để phân tích root cause ở tầng kiến trúc chứ không chỉ tầng code. Họ làm việc trực tiếp với engineering manager để cân bằng giữa deadline và nợ kỹ thuật, với SRE/platform team để hiểu giới hạn hạ tầng thực tế, và với sản phẩm để hiểu yêu cầu nghiệp vụ sẽ scale theo hướng nào trong 12-18 tháng tới — vì thiết kế đúng cho quy mô hiện tại nhưng sai hướng cho quy mô tương lai vẫn là thiết kế phải làm lại.

## Trade-offs

- Đầu tư sâu vào system design nghĩa là ít thời gian hands-on code hơn — đổi lấy phạm vi ảnh hưởng rộng hơn (một quyết định kiến trúc sai ảnh hưởng cả hệ thống, một dòng code sai chỉ ảnh hưởng một tính năng).
- Thiết kế cho khả năng scale tương lai (over-engineering có chủ đích) tốn thời gian và tiền bạc ngay bây giờ, đổi lấy rủi ro không bao giờ cần đến nếu sản phẩm không đạt quy mô dự đoán.
- Càng giỏi trade-off reasoning càng dễ rơi vào "phân tích liệt" (analysis paralysis) — cân nhắc quá nhiều lựa chọn trong khi deadline thực tế không cho phép, phải học cách quyết định đủ tốt trong thời gian có hạn thay vì quyết định hoàn hảo.
- Năng lực này khó đo lường bằng KPI ngắn hạn (không giống viết được bao nhiêu tính năng/sprint) nên dễ bị đánh giá thấp trong tổ chức chỉ nhìn output ngắn hạn, dù giá trị thực sự chỉ lộ ra sau 1-2 năm khi hệ thống phải scale hoặc khi sự cố lớn xảy ra.

## Best Practices

- Học từ hệ thống thật đã vận hành (post-mortem, case study production thực tế) thay vì chỉ học lý thuyết CAP/scalability suông không gắn với hệ quả cụ thể.
- Luôn bắt đầu bằng ước lượng capacity bằng con số (QPS, dung lượng, băng thông) trước khi vẽ kiến trúc — thiết kế theo con số, không theo cảm tính.
- Tập viết design doc ngắn cho mọi thay đổi có ảnh hưởng rộng, kể cả khi không ai bắt buộc — thói quen này rèn khả năng trình bày trade-off rõ ràng, và tạo tài liệu tra cứu khi cần.
- Chủ động tham gia on-call và phân tích incident thật, vì đây là nguồn học nhanh nhất về cách hệ thống thất bại trong thực tế, thứ sách vở không dạy được.
- Học cách nói "tôi không biết, để tôi ước lượng" thay vì đoán mò con số trong buổi phỏng vấn hoặc design review — sự trung thực về giới hạn hiểu biết là một phần của năng lực này.

## Common Mistakes

- Học thuộc lòng kiến trúc của các hệ thống nổi tiếng (Twitter, Uber) để tái hiện y hệt trong phỏng vấn hoặc thiết kế thực tế, mà không hiểu ràng buộc riêng của bài toán đang giải (traffic pattern, ngân sách, đội ngũ vận hành khác nhau hoàn toàn).
- Luôn chọn kiến trúc phức tạp nhất (microservices, event-driven, đa vùng) cho mọi bài toán để "trông chuyên nghiệp", trong khi một monolith với một database duy nhất đủ tốt cho quy mô hiện tại và dễ vận hành hơn nhiều.
- Bỏ qua estimation, nhảy thẳng vào vẽ sơ đồ kiến trúc — dẫn tới thiết kế không khớp với tải thực tế, hoặc lãng phí tài nguyên cho quy mô chưa từng đạt tới.
- Chỉ tập trung vào happy path khi thiết kế, không dành thời gian tương đương cho failure mode (retry, timeout, circuit breaker, fallback) — hệ thống chạy tốt lúc demo nhưng sụp đổ khi dependency đầu tiên chậm hoặc chết.
- Coi system design là năng lực chỉ cần cho vòng phỏng vấn senior, không luyện tập liên tục sau khi có việc — năng lực này mai một nhanh nếu không được áp dụng và cập nhật theo hệ thống thực tế đang thay đổi.

## Interview Questions

**Hỏi**: Bạn sẽ ước lượng capacity cho một hệ thống rút gọn URL (URL shortener) như thế nào trước khi thiết kế kiến trúc?

**Trả lời**: Bắt đầu từ giả định traffic (ví dụ 100 triệu URL mới/tháng, tỷ lệ đọc/ghi 100:1), suy ra QPS trung bình và peak (thường gấp 2-3 lần trung bình), ước lượng dung lượng lưu trữ (kích thước một record x số record x số năm giữ dữ liệu), và băng thông cần thiết. Từ các con số này mới quyết định có cần cache không, có cần shard database không, và mức độ redundancy cần thiết — thay vì thiết kế trước rồi mới biện minh sau.

**Hỏi**: CAP theorem nói gì, và tại sao trong thực tế người ta thường nói tới PACELC thay vì chỉ CAP?

**Trả lời**: CAP nói rằng khi có network partition, hệ thống phân tán phải chọn giữa Consistency (mọi node thấy cùng dữ liệu) và Availability (mọi request đều được trả lời) — không thể có cả hai đồng thời. PACELC mở rộng thêm: ngay cả khi không có partition (P), hệ thống vẫn phải đánh đổi giữa Latency (L) và Consistency (C) trong vận hành bình thường, vì replicate dữ liệu để đảm bảo consistency luôn tốn thời gian. PACELC thực tế hơn vì network partition là sự kiện hiếm, còn latency-consistency trade-off xảy ra ở mọi request.

**Hỏi**: Làm sao bạn biết một thiết kế đã "đủ tốt" để triển khai, thay vì tiếp tục tối ưu?

**Trả lời**: Thiết kế đủ tốt khi nó đáp ứng được yêu cầu capacity đã ước lượng (kèm biên an toàn hợp lý, ví dụ 2-3 lần peak dự kiến), có kế hoạch xử lý ít nhất các failure mode phổ biến nhất (dependency chậm/chết, dữ liệu trùng lặp), và trade-off đã được viết ra, review, và có người chấp nhận rủi ro còn lại. Tiếp tục tối ưu sau điểm đó thường là đánh đổi thời gian ra thị trường lấy một mức an toàn không tương xứng với rủi ro thực tế đang có.

## Summary

Lộ trình system design là quá trình xây dựng năng lực ghép các building block (load balancer, cache, queue, database) thành một hệ thống chịu tải và chịu lỗi thật, dựa trên nền tảng lý thuyết (CAP, consistency, latency) và kỹ năng ước lượng bằng con số cụ thể. Đây là năng lực phân biệt kỹ sư implement theo spec với kỹ sư tự đặt ra spec đúng ở quy mô lớn, và chỉ tích lũy thật sự qua các hệ thống đã vận hành, đã gặp sự cố, và đã rút kinh nghiệm. Trong tổ chức, năng lực này thể hiện qua design doc/RFC, vai trò dẫn dắt design review, và tham gia phân tích incident ở tầng kiến trúc. Đánh đổi lớn nhất là ít thời gian code hơn để đổi lấy phạm vi ảnh hưởng rộng hơn, và rủi ro rơi vào phân tích quá mức nếu không cân bằng với deadline thực tế. Năng lực này cần luyện tập liên tục, không phải một khóa học hoàn thành một lần rồi thôi.

## Knowledge Graph

- ACID — nền tảng consistency ở tầng một database, tiền đề để hiểu trade-off consistency ở tầng hệ phân tán rộng hơn.
- CAP Theorem — nguyên lý nền tảng bắt buộc phải hiểu trước khi thiết kế bất kỳ hệ thống phân tán nào.
- Blue-Green/Canary Deployment — kỹ thuật vận hành thể hiện tư duy giảm rủi ro triển khai, một phần của trục "vận hành thực tế" trong lộ trình.
- Technical Debt Management — kỹ năng đi kèm để cân bằng giữa thiết kế lý tưởng và deadline thực tế.
- Testing Pyramid — năng lực bổ trợ để đảm bảo hệ thống được thiết kế tốt vẫn hoạt động đúng khi thay đổi.
- API Versioning — một quyết định thiết kế cụ thể thường xuất hiện khi hệ thống đã có nhiều consumer phụ thuộc.

## Five Things To Remember

- System design là năng lực ghép component thành hệ thống chịu tải và chịu lỗi, không phải học thuộc tên công nghệ.
- Luôn ước lượng bằng con số trước khi vẽ kiến trúc, đừng thiết kế theo cảm tính.
- CAP/PACELC là công cụ ra quyết định trade-off, không phải câu trả lời đúng-sai tuyệt đối.
- Thiết kế cho failure mode quan trọng ngang thiết kế cho happy path.
- Năng lực này chỉ tích lũy thật sự qua hệ thống đã vận hành và sự cố đã trải qua, không phải qua lý thuyết suông.
