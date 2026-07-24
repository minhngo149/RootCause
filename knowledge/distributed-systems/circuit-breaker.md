---
id: circuit-breaker
title: Circuit Breaker
tags: ["distributed-systems", "resilience"]
---

# Circuit Breaker

> Status: Draft

## Problem

Service A gọi service B qua network. Khi B chậm hoặc lỗi hàng loạt (DB downstream sập, pool connection cạn, deploy lỗi...), A vẫn tiếp tục gửi request tới B như bình thường, mỗi request treo cho đến khi hết timeout. Không có cơ chế nào chủ động dừng việc gọi B lại, A tự làm cạn kiệt tài nguyên của chính mình (thread, connection, memory) để chờ những request gần như chắc chắn sẽ fail — đây là gốc rễ khiến một sự cố cục bộ ở B lan ngược thành sự cố ở A.

## Pain Points

- Thread pool hoặc connection pool của A bị chiếm hết bởi các request đang treo chờ B trả lời, khiến A không còn khả năng phục vụ cả những request không liên quan gì đến B — outage của B trở thành outage của A.
- Latency p99 của A tăng vọt bằng đúng timeout gọi B (vd. 30s), user chờ 30 giây để nhận lỗi thay vì fail nhanh trong vài mili-giây.
- Khi B hồi phục nhưng vẫn còn yếu, A đồng loạt dội lại toàn bộ traffic đã dồn ứ (retry storm), khiến B chưa kịp ổn định đã bị đánh sập lần nữa — kéo dài thời gian phục hồi thay vì rút ngắn.
- Trong kiến trúc microservices nhiều tầng, một service downstream (vd. một dịch vụ tính điểm tín dụng) sập có thể lan cascading qua 3-4 tầng gọi trung gian, biến một sự cố nhỏ ở rìa hệ thống thành outage toàn hệ thống.

## Solution

Circuit breaker là một lớp chặn đặt trước lời gọi tới service downstream, theo dõi tỷ lệ lỗi của các lời gọi gần nhất và tự động "ngắt mạch" — ngừng gọi thật tới downstream và trả lỗi ngay lập tức — khi tỷ lệ lỗi vượt ngưỡng. Ý tưởng mượn từ circuit breaker điện: khi dòng điện (lỗi) vượt ngưỡng an toàn, mạch tự ngắt để bảo vệ toàn hệ thống thay vì để thiết bị cháy dần. Nó không sửa được lỗi ở downstream, nhưng ngăn lỗi đó lan ngược và cho downstream thời gian phục hồi mà không bị dội thêm traffic.

## How It Works

Circuit breaker có 3 trạng thái chính:

- **Closed**: trạng thái bình thường, mọi request được cho đi qua tới downstream. Breaker đếm số request thành công/thất bại trong một cửa sổ trượt (sliding window, vd. 10 giây gần nhất hoặc 100 request gần nhất). Khi tỷ lệ lỗi trong cửa sổ vượt ngưỡng cấu hình (vd. >50% lỗi trên tối thiểu 20 request), breaker chuyển sang **Open**.
- **Open**: mọi request bị chặn ngay tại A, trả lỗi (`CircuitOpenException` hoặc fallback) tức thì mà không hề gửi request thật tới B. Breaker giữ trạng thái này trong một khoảng thời gian cố định (reset timeout, vd. 30 giây) — đây là khoảng "nghỉ" để B có cơ hội phục hồi mà không bị A dội thêm traffic.
- **Half-Open**: sau khi hết reset timeout, breaker cho một số lượng request giới hạn (vd. 1-5 request thăm dò) đi qua thật tới B để kiểm tra tình trạng. Nếu các request thăm dò này thành công, breaker chuyển về **Closed** và mở lại toàn bộ traffic. Nếu vẫn thất bại, breaker quay lại **Open** và reset lại đồng hồ đếm thời gian.

Việc đếm lỗi thường không chỉ dựa vào HTTP status code mà còn tính cả timeout và exception ở tầng gọi (connection refused, read timeout). Một số implementation (Resilience4j, Polly, Hystrix) còn phân biệt thêm trạng thái **Disabled/Forced Open** dùng cho vận hành thủ công, và hỗ trợ **slow call rate** — coi request quá chậm (dù cuối cùng vẫn thành công) cũng tính là "lỗi" để ngắt mạch sớm hơn, tránh trường hợp B chưa hẳn lỗi nhưng đã chậm đến mức không còn hữu ích.

## Production Architecture

Trong kiến trúc microservices dạng API gateway gọi xuống các service nghiệp vụ (vd. gateway gọi service `pricing`, `inventory`, `recommendation`), mỗi client HTTP tới từng downstream được bọc bởi một circuit breaker riêng — không dùng chung một breaker cho tất cả downstream, vì lỗi ở `recommendation` (service không thiết yếu) không nên ảnh hưởng tới cách gọi `pricing` (service thiết yếu). Circuit breaker luôn đi kèm một **fallback path**: khi breaker ở Open, thay vì trả lỗi trắng cho user, hệ thống trả dữ liệu cache cũ, giá trị mặc định, hoặc ẩn hẳn phần UI phụ thuộc (vd. ẩn widget "sản phẩm gợi ý" nếu service recommendation đang Open). Ở tầng hạ tầng, service mesh (Istio, Linkerd) cung cấp circuit breaking ở tầng sidecar proxy áp dụng cho toàn bộ traffic giữa các service mà không cần sửa code ứng dụng, cấu hình qua `outlier detection` (số lỗi liên tiếp, thời gian eject). Circuit breaker luôn được triển khai cùng với timeout hợp lý và retry có giới hạn (thường retry chỉ hoạt động khi breaker đang Closed) — ba cơ chế này bổ trợ nhau chứ không thay thế nhau.

## Trade-offs

Circuit breaker đánh đổi tính sẵn sàng cục bộ lấy tính ổn định toàn cục: khi breaker Open, A chủ động từ chối cả những request có thể đã thành công nếu được gửi thật, nghĩa là chấp nhận một số false negative để đổi lấy việc không làm B sập nặng hơn. Chọn ngưỡng lỗi và reset timeout sai lệch gây hai thái cực đều tệ — ngưỡng quá nhạy (dễ ngắt) làm breaker chớp tắt (flapping) ngay cả khi downstream chỉ chậm nhất thời, còn ngưỡng quá lì (khó ngắt) làm breaker không kịp bảo vệ hệ thống trước khi cascading xảy ra. Fallback logic (trả cache cũ, giá trị mặc định) thêm một lớp phức tạp cần thiết kế và test riêng, và nếu làm ẩu (vd. fallback trả dữ liệu sai ngữ cảnh, giá cũ hiển thị như giá hiện tại) có thể gây lỗi nghiệp vụ nghiêm trọng hơn cả việc trả lỗi rõ ràng cho user.

## Best Practices

- Đặt circuit breaker riêng cho từng downstream dependency, không dùng chung một breaker cho nhiều service khác nhau.
- Luôn định nghĩa fallback path rõ ràng khi breaker Open (cache, default value, degrade UI) thay vì chỉ trả lỗi 500 trắng.
- Tính cả timeout và slow call vào tỷ lệ lỗi, không chỉ dựa vào HTTP status code hay exception cứng.
- Giới hạn số request thăm dò ở Half-Open (thường 1-5 request) để tránh dội lại traffic lớn ngay khi downstream chưa thực sự ổn định.
- Expose trạng thái breaker (Closed/Open/Half-Open) như một metric/dashboard, vì breaker chuyển Open là tín hiệu sớm cực kỳ giá trị để alert trước khi user report sự cố.

## Common Mistakes

- Đặt reset timeout quá ngắn khiến breaker liên tục Open/Half-Open/Open (flapping) mà downstream còn chưa kịp phục hồi thực sự.
- Không giới hạn số request thăm dò ở Half-Open, để toàn bộ traffic dội lại cùng lúc, đánh sập downstream lần thứ hai ngay khi vừa hồi phục.
- Dùng chung một circuit breaker cho nhiều downstream khác nhau, khiến lỗi ở một dependency không quan trọng làm ngắt luôn cả dependency thiết yếu.
- Fallback trả về dữ liệu cũ/sai mà không đánh dấu rõ (staleness flag) cho tầng gọi phía trên, khiến lỗi dữ liệu bị che giấu thay vì được xử lý đúng.
- Coi circuit breaker là giải pháp thay thế cho timeout và retry, trong khi thực tế ba cơ chế cần phối hợp: timeout giới hạn thời gian chờ một request, retry xử lý lỗi thoáng qua, circuit breaker ngăn cascading khi lỗi trở thành hệ thống.

## Interview Questions

**Hỏi**: Circuit breaker khác gì với việc chỉ đặt timeout cho request?

**Trả lời**: Timeout giới hạn thời gian chờ của một request đơn lẻ nhưng vẫn gửi request đó đi thật mỗi lần, không học được từ lịch sử lỗi gần đây. Circuit breaker theo dõi tỷ lệ lỗi qua nhiều request và khi vượt ngưỡng thì chặn hẳn, không gửi request thật nữa trong một khoảng thời gian — timeout xử lý độ trễ của một lần gọi, circuit breaker xử lý xu hướng lỗi liên tục để ngăn cascading failure.

**Hỏi**: Vì sao Half-Open lại chỉ cho một số ít request đi qua thay vì mở lại toàn bộ ngay lập tức?

**Trả lời**: Nếu mở lại toàn bộ traffic ngay khi hết reset timeout, và downstream trên thực tế vẫn chưa ổn định hoàn toàn, lượng traffic dồn ứ lớn sẽ đánh sập downstream lần nữa ngay khi nó vừa có dấu hiệu hồi phục. Giới hạn số request thăm dò giúp kiểm tra sức khỏe downstream một cách an toàn trước khi cam kết mở lại toàn bộ traffic.

**Hỏi**: Circuit breaker nên tính "lỗi" dựa trên những tiêu chí nào ngoài HTTP 5xx?

**Trả lời**: Cần tính cả timeout, connection refused/reset, và slow call rate (request thành công nhưng vượt ngưỡng thời gian coi là chấp nhận được), vì một downstream đang quá tải có thể vẫn trả 200 nhưng chậm đến mức không còn hữu ích cho use case gọi nó — chỉ dựa vào status code sẽ bỏ sót dấu hiệu suy giảm sớm này.

## Summary

Circuit breaker là lớp chặn đặt trước lời gọi tới downstream, theo dõi tỷ lệ lỗi qua một cửa sổ trượt và tự động ngắt gọi thật khi lỗi vượt ngưỡng, thay vì để mỗi request tự treo chờ timeout. Nó vận hành qua 3 trạng thái: Closed (gọi bình thường), Open (chặn hẳn, trả lỗi/fallback ngay), và Half-Open (thăm dò với số lượng request giới hạn để quyết định đóng lại hay mở tiếp). Cơ chế này không sửa lỗi ở downstream mà ngăn lỗi đó lan ngược làm cạn kiệt tài nguyên của service gọi, đồng thời cho downstream không gian để phục hồi mà không bị dội thêm traffic. Trong production, mỗi downstream cần một breaker riêng đi kèm fallback path rõ ràng, và cấu hình ngưỡng/reset timeout sai có thể gây flapping hoặc chậm phản ứng trước cascading failure. Circuit breaker luôn cần phối hợp với timeout và retry có giới hạn, không thay thế chúng.

## Knowledge Graph

- Retry Storm — retry không kiểm soát khi downstream đang lỗi là nguyên nhân trực tiếp circuit breaker cần ngăn chặn.
- Bulkhead Pattern — cô lập tài nguyên (thread pool, connection pool) theo từng dependency, bổ trợ circuit breaker để ngăn một downstream chiếm hết resource dùng chung.
- Timeout — xác định thời gian chờ tối đa cho một lời gọi, là tín hiệu đầu vào để circuit breaker đếm lỗi.
- Cascading Failure — hệ quả trực tiếp khi không có circuit breaker, lỗi từ một service lan ngược qua nhiều tầng gọi.
- Service Mesh (Istio/Linkerd) — triển khai circuit breaking ở tầng hạ tầng (sidecar proxy) mà không cần sửa code ứng dụng.
- Rate Limiting — kiểm soát traffic đi vào một service, khác hướng với circuit breaker (kiểm soát traffic đi ra tới downstream lỗi).

## Five Things To Remember

- Circuit breaker ngắt gọi tới downstream khi tỷ lệ lỗi vượt ngưỡng, thay vì để mỗi request tự treo chờ timeout.
- Ba trạng thái Closed/Open/Half-Open vận hành như một vòng lặp tự kiểm tra: gọi bình thường, ngắt hẳn, rồi thăm dò trước khi mở lại.
- Luôn có fallback path rõ ràng khi breaker Open, không để user nhận lỗi trắng không xử lý.
- Mỗi downstream dependency cần một breaker riêng, không dùng chung một breaker cho nhiều service khác nhau.
- Circuit breaker không thay thế timeout và retry mà phối hợp cùng chúng để ngăn cascading failure toàn diện.
