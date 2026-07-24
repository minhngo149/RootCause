---
id: idempotency
title: Idempotency
tags: ["backend", "reliability"]
---

# Idempotency

> Status: Draft

## Problem

Client gọi API tạo đơn hàng, request đi tới server, server xử lý xong (trừ kho, tạo record, gửi event) nhưng response bị mất trên đường về do timeout hoặc network drop. Client, không biết server đã xử lý thành công, coi đây là lỗi và tự động retry (hoặc user bấm nút "Đặt hàng" lần nữa vì thấy trang bị treo). Nếu API không có cơ chế nào để nhận diện "đây là cùng một yêu cầu đã xử lý rồi", server sẽ xử lý request thứ hai y hệt request đầu — tạo ra hai đơn hàng, trừ tiền hai lần, hoặc gửi hai email cho cùng một hành động logic duy nhất của người dùng. Đây không phải trường hợp hiếm: trong hệ thống phân tán, timeout mơ hồ (không biết server đã xử lý hay chưa) là bản chất của giao tiếp qua mạng không tin cậy, và mọi cơ chế retry — dù ở tầng client, load balancer, hay message queue — đều tái tạo lại chính xác tình huống này.

## Pain Points

- Double charge: API thanh toán bị gọi lại do timeout, khách hàng bị trừ tiền hai lần cho một giao dịch, dẫn tới khiếu nại, hoàn tiền thủ công, và mất niềm tin.
- Đơn hàng trùng lặp: retry ở tầng client hoặc mobile app (user bấm nút submit nhiều lần vì UI không disable kịp) tạo ra 2-3 record cho cùng một đơn hàng, gây sai lệch tồn kho và báo cáo doanh thu.
- Side-effect kép ở webhook: payment gateway (Stripe, VNPay, Momo) gửi lại cùng một webhook event nhiều lần (đây là hành vi được đặc tả rõ ràng, không phải bug) — nếu handler không dedup, hệ thống ghi nhận thanh toán thành công nhiều lần cho cùng một order.
- Retry storm khuếch đại side-effect: khi kết hợp với retry+backoff ở tầng hạ tầng (service mesh, message queue consumer), một request lỗi tạm thời có thể được gọi lại 3-5 lần tự động — nếu operation không idempotent, mỗi lần retry là một side-effect mới, nhân sự cố lên gấp nhiều lần thay vì chỉ gây chậm.
- Chi phí điều tra sự cố: khi dữ liệu trùng lặp xảy ra ngẫu nhiên (phụ thuộc timing mạng), đội vận hành phải dò log, đối soát dữ liệu thủ công để tìm bản ghi trùng và hoàn tác, tốn nhiều giờ engineering cho mỗi lần xảy ra.

## Solution

Idempotency là tính chất của một operation: gọi nó một lần hay gọi nhiều lần với cùng input đều cho ra cùng một kết quả cuối cùng ở phía server, không tạo thêm side-effect ở các lần gọi lặp lại. Cơ chế phổ biến nhất để đạt được tính chất này cho các API có side-effect (POST tạo tài nguyên, thanh toán) là idempotency key: client sinh một định danh duy nhất cho mỗi logical operation, gửi kèm trong request; server dùng key này để nhận diện và loại bỏ các lần gọi lặp, trả lại đúng kết quả của lần xử lý đầu tiên thay vì thực thi lại.

## How It Works

**Phân loại theo HTTP method**: GET, PUT, DELETE về bản chất đã idempotent nếu implement đúng — GET không có side-effect, PUT ghi đè toàn bộ resource nên gọi lại nhiều lần cho cùng kết quả, DELETE xóa resource nên lần gọi thứ hai chỉ là no-op (dù có thể trả 404 thay vì 200, đây vẫn là kết quả nhất quán). POST theo đặc tả HTTP không idempotent vì mặc định là "tạo mới", nên POST cần cơ chế idempotency key rõ ràng nếu muốn an toàn khi retry — đây là phần lớn công sức implement idempotency thực tế nằm ở đâu.

**Luồng xử lý idempotency key ở server**: client sinh UUID v4 cho mỗi logical operation (không sinh lại khi retry cùng operation) và gửi trong header `Idempotency-Key`. Server, khi nhận request, thực hiện một thao tác atomic: kiểm tra key đã tồn tại trong storage (Redis, hoặc bảng `idempotency_keys` trong cùng database transaction với business logic) chưa. Nếu chưa tồn tại, ghi nhận key với trạng thái `processing` (dùng `INSERT ... ON CONFLICT DO NOTHING` hoặc `SETNX` để đảm bảo chỉ một request thắng nếu có race), tiến hành xử lý business logic, rồi cập nhật key sang `completed` kèm response đã serialize. Nếu key đã tồn tại và ở trạng thái `completed`, trả ngay response đã lưu mà không chạm vào business logic. Nếu key tồn tại nhưng đang ở trạng thái `processing` (request gốc chưa xử lý xong, request thứ hai tới do client retry quá sớm), trả lỗi 409 Conflict hoặc để client chờ và retry sau — không được xử lý song song vì sẽ tái tạo đúng race condition mà cơ chế này muốn ngăn.

**Idempotency key phải bao gồm cả request fingerprint**: chỉ lưu key không đủ — cần lưu kèm hash của request body (hoặc toàn bộ payload) để phát hiện trường hợp client tái sử dụng cùng key nhưng gửi payload khác (bug ở client, hoặc key bị cache sai). Nếu key trùng nhưng payload khác, server phải trả lỗi 422 thay vì âm thầm trả kết quả cũ hoặc xử lý payload mới với key cũ — Stripe API implement chính xác theo cách này.

**TTL và storage**: idempotency key thường lưu trong Redis với TTL 24h-7 ngày (đủ dài để bao trùm mọi kịch bản retry hợp lý của client, kể cả retry sau khi user đóng app và mở lại), không lưu vĩnh viễn vì sẽ phình storage vô ích. Với các API có yêu cầu consistency chặt (thanh toán), key nên lưu trong cùng database transaction với business logic (bảng riêng, cùng transaction với việc tạo order/payment) thay vì Redis riêng biệt, để tránh tình huống record được tạo nhưng key ghi Redis thất bại (hoặc ngược lại) làm mất tính atomic.

**Idempotency tự nhiên qua thiết kế dữ liệu**: một cách khác để đạt idempotency không cần key riêng là dùng unique constraint ở tầng database dựa trên business key sẵn có — ví dụ đơn hàng có `order_reference` do client sinh với unique index, insert lần hai sẽ bị database từ chối do vi phạm constraint, server bắt lỗi này và trả về record đã tồn tại. Cách này đơn giản hơn nhưng chỉ áp dụng được khi nghiệp vụ có sẵn một khóa tự nhiên đóng vai trò định danh logical operation.

## Production Architecture

Trong hệ thống thanh toán, idempotency key là bắt buộc theo hợp đồng API chứ không phải tùy chọn — Stripe, PayPal, VNPay đều yêu cầu (hoặc khuyến nghị mạnh) header `Idempotency-Key` cho mọi request tạo charge, và tự động dedup ở phía họ trong 24h. Ở tầng webhook consumer (nhận sự kiện từ payment gateway hoặc message queue như Kafka/SQS), idempotency được implement bằng cách lưu `event_id` đã xử lý vào bảng riêng hoặc dùng unique constraint, kiểm tra trước khi apply side-effect — vì các hệ thống này cam kết "at-least-once delivery", nghĩa là consumer luôn phải tự chịu trách nhiệm dedup, không được giả định mỗi message chỉ đến một lần. Ở tầng API gateway hoặc BFF (backend-for-frontend), idempotency key thường được sinh tự động phía client (mobile app, SPA) ngay khi user bắt đầu một hành động (bấm nút submit) và giữ nguyên qua toàn bộ vòng đời retry của request đó cho tới khi nhận được response thành công hoặc user hủy hành động. Trong kiến trúc microservices có Saga pattern để xử lý transaction phân tán, mỗi bước của saga (mỗi service call) cũng cần idempotent vì saga orchestrator sẽ tự retry bước lỗi — nếu bước "trừ kho" hay "tạo shipment" không idempotent, retry của saga sẽ nhân đôi side-effect ở đúng bước đó.

## Trade-offs

Idempotency key đòi hỏi thêm một lớp storage và logic ở cả client (sinh và giữ key qua vòng đời retry) lẫn server (kiểm tra, lưu, TTL), làm tăng độ phức tạp API contract — không phải team nào cũng sẵn sàng đầu tư cho mọi endpoint, nên thường chỉ áp dụng cho các API có side-effect quan trọng (thanh toán, tạo đơn) chứ không phải toàn bộ hệ thống. Việc kiểm tra key trước khi xử lý thêm một round-trip tới storage (Redis hoặc DB) vào critical path của mọi request, tăng latency nhẹ (thường 1-5ms với Redis) — chấp nhận được so với rủi ro double-processing nhưng vẫn là chi phí thật. Nếu idempotency key lưu ở storage riêng (Redis) không cùng transaction với business logic, tồn tại khoảng hở nhỏ giữa lúc ghi kết quả business và lúc cập nhật trạng thái key — trong khoảng hở đó, một request trùng tới có thể vẫn lọt qua và xử lý lại; giải pháp triệt để (cùng transaction) lại đánh đổi bằng việc khóa idempotency logic vào cùng một database với business logic, khó tái sử dụng chung cho nhiều service. Idempotency cũng không miễn phí về UX: nếu server trả lại response cache của lần xử lý trước, response đó phải phản ánh đúng trạng thái tại thời điểm xử lý gốc, không phải trạng thái hiện tại — với dữ liệu thay đổi nhanh (giá, tồn kho), điều này có thể khiến response "cũ" gây nhầm lẫn nếu client không hiểu rõ ngữ nghĩa.

## Best Practices

- Bắt buộc `Idempotency-Key` (client-generated UUID) cho mọi API POST có side-effect quan trọng: thanh toán, tạo đơn hàng, gửi thông báo không thể thu hồi.
- Lưu kèm hash của request body cùng key, trả lỗi rõ ràng (422) nếu key trùng nhưng payload khác, thay vì âm thầm trả kết quả cũ.
- Xử lý kiểm tra-và-ghi key bằng thao tác atomic ở storage (SETNX, INSERT ON CONFLICT), không bao giờ dùng read-then-write riêng lẻ vì sẽ có race condition giữa các request đồng thời.
- Với webhook/consumer từ hệ thống bên ngoài (payment gateway, message queue), luôn dedup theo `event_id` — coi at-least-once delivery là mặc định, không giả định exactly-once.
- Đặt TTL hợp lý cho idempotency key (24h-7 ngày) đủ bao trùm kịch bản retry thực tế của client, tránh phình storage vô thời hạn.

## Common Mistakes

- Chỉ dựa vào idempotency key ở tầng ứng dụng mà không có unique constraint ở database, khiến race condition (hai request đồng thời đều "chưa thấy key") vẫn tạo ra bản ghi trùng.
- Sinh idempotency key mới cho mỗi lần retry thay vì giữ nguyên key qua toàn bộ vòng đời của một logical operation — điều này vô hiệu hóa hoàn toàn cơ chế dedup.
- Coi PUT hoặc DELETE là "tự động idempotent" mà không kiểm tra thực tế implement — ví dụ PUT có side-effect phụ (gửi email thông báo mỗi lần update) thì gọi lại vẫn gây side-effect kép dù chính resource được ghi đè đúng.
- Không xử lý trường hợp request đang ở trạng thái `processing` khi request trùng tới trong lúc request gốc chưa xử lý xong, dẫn đến xử lý song song thay vì chờ hoặc từ chối.
- Áp dụng idempotency key ở client nhưng không truyền xuyên suốt qua các tầng retry trung gian (service mesh, queue consumer tự retry với key khác), khiến tầng hạ tầng vô tình phá vỡ đảm bảo idempotent đã xây ở tầng ứng dụng.

## Interview Questions

**Hỏi**: Idempotency key khác gì với việc chỉ dùng unique constraint ở database?
**Trả lời**: Unique constraint chỉ ngăn được việc tạo dữ liệu trùng dựa trên một business key sẵn có, nhưng không lưu lại response gốc — nếu request trùng tới, server vẫn phải bắt lỗi constraint violation rồi tự truy vấn lại record cũ để trả response nhất quán, phức tạp hơn cho các business logic nhiều bước. Idempotency key là cơ chế tổng quát hơn: nó lưu cả trạng thái xử lý lẫn response đã serialize, cho phép trả lại chính xác kết quả của lần gọi đầu tiên cho bất kỳ operation nào, không phụ thuộc việc nghiệp vụ có sẵn khóa tự nhiên hay không.

**Hỏi**: Tại sao GET và DELETE được coi là idempotent theo đặc tả HTTP, còn POST thì không?
**Trả lời**: GET không thay đổi trạng thái server nên gọi bao nhiêu lần cũng cho cùng kết quả quan sát được; DELETE dù xóa resource, nhưng gọi lại lần hai trên resource đã xóa vẫn dẫn tới cùng trạng thái cuối (resource không tồn tại), dù mã trả về có thể khác (200 rồi 404). POST theo ngữ nghĩa mặc định là "tạo mới một thực thể", nên hai lần gọi POST giống hệt nhau về đặc tả sẽ tạo ra hai thực thể khác nhau — đây là lý do POST cần cơ chế idempotency key bổ sung nếu muốn an toàn khi retry.

**Hỏi**: Idempotency key nên lưu ở Redis hay cùng transaction với database chính, khi nào chọn cái nào?
**Trả lời**: Nếu operation yêu cầu consistency chặt (thanh toán, tạo đơn hàng), nên lưu key trong cùng transaction với bảng business chính để đảm bảo tính atomic giữa việc ghi business record và cập nhật trạng thái key, tránh khoảng hở gây xử lý trùng. Nếu chỉ cần dedup nhanh, không yêu cầu consistency tuyệt đối (ví dụ dedup request log, rate limiting theo request), Redis với TTL là đủ và nhanh hơn nhiều so với thêm bảng vào transaction chính.

## Summary

Idempotency là tính chất đảm bảo gọi một operation nhiều lần với cùng input cho ra cùng kết quả cuối cùng, không tạo thêm side-effect ở các lần gọi lặp — đây là điều kiện tiên quyết bắt buộc trước khi bật retry cho bất kỳ API nào có tác dụng phụ thật sự (thanh toán, tạo tài nguyên). Cơ chế phổ biến nhất là idempotency key: client sinh định danh duy nhất cho mỗi logical operation, server dùng key này (kết hợp thao tác atomic ở storage) để nhận diện và loại bỏ các lần gọi lặp, trả lại đúng kết quả của lần xử lý đầu tiên. Idempotency cần được thiết kế xuyên suốt các tầng — client sinh và giữ key, server kiểm tra atomic, webhook/consumer dedup theo event ID — vì chỉ cần một tầng bỏ sót, đảm bảo idempotent ở các tầng còn lại đều vô nghĩa. Trade-off chính là thêm độ phức tạp storage, một round-trip vào critical path, và rủi ro response "cũ" nếu dữ liệu nghiệp vụ thay đổi nhanh giữa các lần gọi. Trong production, đây không phải tính năng tùy chọn cho các API tiền bạc và tài nguyên quan trọng, mà là một phần bắt buộc của API contract.

## Knowledge Graph

- Retry & Exponential Backoff — idempotency là điều kiện tiên quyết để retry an toàn cho các request có side-effect.
- Saga Pattern — mỗi bước trong saga cần idempotent vì orchestrator tự động retry bước lỗi khi thực thi transaction phân tán.
- Message Delivery Guarantees — at-least-once delivery của message queue/webhook bắt buộc consumer phải tự dedup, tức tự implement idempotency.
- Distributed Locking — cơ chế atomic check-and-set (SETNX) dùng để đảm bảo idempotency key race-free tương tự cách distributed lock hoạt động.
- Circuit Breaker — bảo vệ downstream khỏi retry storm, hoạt động cùng lớp với idempotency trong chiến lược resilience toàn diện.
- Unique Constraint (database) — cách hiện thực hóa idempotency đơn giản hơn khi nghiệp vụ có sẵn khóa tự nhiên định danh operation.

## Five Things To Remember

- Idempotency nghĩa là gọi nhiều lần cho cùng kết quả cuối, không phải "không có side-effect".
- Không có idempotency, mọi retry đều là một cách tạo dữ liệu trùng lặp tiềm tàng.
- Idempotency key phải được giữ nguyên qua toàn bộ vòng đời retry của một logical operation, không sinh mới mỗi lần.
- Kiểm tra key phải là thao tác atomic; read-then-write riêng lẻ sẽ có race condition.
- Webhook và message queue là at-least-once theo mặc định — dedup là trách nhiệm của consumer, không phải của nhà cung cấp.
