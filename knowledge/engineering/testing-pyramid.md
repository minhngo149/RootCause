---
id: testing-pyramid
title: Testing Pyramid
tags: ["engineering", "testing"]
---

# Testing Pyramid

> Status: Draft

## Problem

Một team backend triển khai một service thanh toán mới, tự tin vì "coverage 85%". Nhìn kỹ thì 85% đó đến từ 40 test e2e chạy qua Cypress, mỗi test dựng cả stack (API gateway, service thanh toán, service ví, Postgres, Redis, một sandbox cổng thanh toán giả lập) rồi click qua UI để tạo giao dịch, kiểm tra số dư. Suite này chạy 55 phút trên CI, và cứ vài lần chạy lại có 2-3 test fail ngẫu nhiên vì timeout hoặc race condition trong lúc seed dữ liệu — không liên quan gì đến bug thật. Sau vài tuần, engineer bắt đầu bấm "Re-run failed jobs" theo phản xạ mà không đọc log, và một lần trong số đó, test fail lại đúng là bug thật (tính sai phí giao dịch), nhưng bị chìm giữa hàng loạt lần rerun do flaky test khác.

## Pain Points

- CI pipeline 45-60 phút vì phần lớn test là e2e nặng, làm chậm vòng lặp feedback: engineer đẩy code buổi sáng, đến trưa mới biết PR fail, ngữ cảnh đã nguội.
- Test flaky (fail ngẫu nhiên không do bug) khiến engineer hình thành thói quen rerun theo phản xạ, làm giảm độ tin cậy của toàn bộ suite — một tín hiệu fail thật dễ bị bỏ qua giữa các fail giả.
- Debug một e2e test fail tốn hàng giờ vì phải trace qua nhiều service, trong khi cùng bug đó nếu bắt bằng unit test chỉ mất vài giây để xác định đúng hàm, đúng dòng.
- Chi phí hạ tầng CI tăng tuyến tính theo số lượng e2e test (mỗi test cần dựng container, seed DB, network call thật), có team phải scale runner CI tốn thêm hàng nghìn USD/tháng chỉ để chạy đủ nhanh trước khi engineer mất kiên nhẫn.

## Solution

Testing pyramid là mô hình phân bổ số lượng test theo 3 tầng: nhiều unit test ở đáy (test một hàm/class cô lập, không I/O), ít hơn integration test ở giữa (test tương tác giữa vài component thật, ví dụ service gọi thật vào một DB test), và rất ít e2e test ở đỉnh (test toàn bộ luồng qua UI/API như người dùng thật). Tỷ lệ tham khảo phổ biến là khoảng 70% unit, 20% integration, 10% e2e — không phải con số cứng, nhưng nguyên tắc cốt lõi là: test càng chạy nhanh và cô lập càng nhiều, test càng chậm và phụ thuộc nhiều hệ thống thì càng ít, chỉ giữ lại để phủ những luồng nghiệp vụ quan trọng nhất (checkout, đăng nhập, thanh toán) chứ không phải mọi nhánh rẽ logic.

## How It Works

Cơ chế đứng sau kim tự tháp là đánh đổi giữa "độ tin cậy của tín hiệu" và "chi phí chạy". Unit test mock toàn bộ dependency bên ngoài (DB, network, filesystem), nên chạy trong milliseconds và fail chỉ vì đúng một lý do: logic trong hàm đó sai — signal-to-noise gần như tuyệt đối, cho phép chạy hàng nghìn test trong vài giây trên máy local mỗi lần save file. Integration test bỏ mock ở ranh giới quan trọng (ví dụ dùng Postgres thật trong container thay vì mock repository) để bắt lỗi mà unit test không thấy được — sai kiểu dữ liệu giữa ORM và schema thật, transaction isolation level sai, N+1 query — đổi lại chạy chậm hơn (giây thay vì millisecond) vì có I/O thật. E2e test dựng toàn bộ hệ thống và giả lập hành vi người dùng thật qua HTTP/browser, nên là lớp duy nhất bắt được lỗi ở tầng tích hợp thực sự (network timeout giữa hai service, cấu hình load balancer sai, CORS sai) nhưng cũng là lớp dễ flaky nhất vì phụ thuộc vào trạng thái nhiều hệ thống cùng lúc — một service phụ thuộc chậm khởi động, một DB chưa seed xong, một network call thật bị packet loss ngẫu nhiên đều làm test fail dù code không hề có bug. Vì chi phí (thời gian chạy, độ flaky, khó debug) tăng theo cấp số nhân từ đáy lên đỉnh trong khi khả năng cô lập lỗi giảm dần, kim tự tháp buộc phải mỏng dần ở trên để tổng chi phí CI không vượt ngưỡng chấp nhận được.

## Production Architecture

Trong một pipeline CI/CD production điển hình, ba tầng test chạy ở ba giai đoạn khác nhau với mức độ chặn khác nhau: unit test chạy trên mọi commit, mọi PR, thậm chí pre-commit hook local, và bắt buộc pass 100% trước khi cho merge (thường dưới 2-3 phút cho hàng nghìn test nhờ chạy song song). Integration test chạy trong CI với service thật được dựng bằng Docker Compose hoặc Testcontainers (ví dụ spin lên một Postgres container thật, chạy migration, seed vài row, rồi test repository layer gọi thật vào đó), thường mất 5-10 phút và cũng là gate bắt buộc trước merge. E2e test — ví dụ Cypress/Playwright chạy qua một môi trường staging đầy đủ, hoặc Postman/REST-assured test gọi qua toàn bộ chuỗi API gateway → service → DB — thường chỉ chạy trên một tập nhỏ smoke test khi merge vào main, và một suite đầy đủ hơn chạy theo lịch (nightly) hoặc trước khi deploy production, không chặn mọi PR vì quá chậm và quá dễ flaky để làm gate tức thời. Một số tổ chức còn thêm một tầng "test contract" (Pact, OpenAPI schema validation) giữa unit và integration để verify hai service tuân thủ đúng API contract mà không cần dựng cả hai lên chạy thật.

## Trade-offs

- Unit test chạy nhanh và ổn định nhưng mock quá nhiều khiến test pass dù tích hợp thật giữa các module bị vỡ — "coverage cao" không đồng nghĩa "hệ thống hoạt động đúng khi ráp lại".
- Integration test bắt lỗi thật hơn nhưng cần hạ tầng test (container, test DB) phức tạp hơn để maintain, và chạy chậm hơn unit test hàng chục đến hàng trăm lần.
- Ít e2e test nghĩa là chấp nhận rủi ro một số lỗi chỉ xuất hiện ở tầng tích hợp thực sự (network, cấu hình hạ tầng) sẽ không bị bắt cho đến khi lên staging hoặc production — đánh đổi tốc độ CI lấy độ trễ phát hiện lỗi tầng hệ thống.
- Đảo ngược kim tự tháp (nhiều e2e, ít unit) tạo cảm giác an toàn giả — "test qua UI thật thì chắc chắn đúng" — trong khi thực tế chi phí bảo trì tăng vọt và feedback loop chậm đến mức engineer né tránh viết test mới.

## Best Practices

- Viết unit test cho toàn bộ logic nghiệp vụ thuần (tính phí, validate input, state machine) trước khi viết bất kỳ integration/e2e test nào cho cùng luồng đó.
- Giới hạn e2e test chỉ cho các luồng nghiệp vụ quan trọng nhất (critical path: đăng nhập, checkout, thanh toán) — không viết e2e test cho từng biến thể validate input, để dành việc đó cho unit test.
- Dùng Testcontainers hoặc tương đương để integration test chạy với dependency thật (DB, message queue) nhưng vẫn tái lập được và cô lập giữa các lần chạy, không chia sẻ trạng thái với môi trường staging.
- Đo và theo dõi flaky rate của từng test theo thời gian (nhiều CI hệ thống hiện đại có báo cáo này sẵn); bất kỳ test nào flaky quá một ngưỡng (ví dụ fail ngẫu nhiên hơn 2% số lần chạy) phải được quarantine hoặc sửa ngay, không để chung với suite chính.
- Chạy unit + integration test bắt buộc trên mọi PR, nhưng đẩy phần lớn e2e suite sang chạy theo lịch (nightly) hoặc gate riêng trước khi deploy, không chặn vòng lặp review PR hàng ngày.

## Common Mistakes

- Đảo ngược kim tự tháp: viết e2e test cho mọi tính năng vì "nhìn giống người dùng thật nhất", dẫn đến CI suite mất hàng chục phút và flaky liên tục.
- Coi coverage phần trăm là thước đo chất lượng duy nhất, không phân biệt 85% coverage từ unit test cô lập tốt với 85% coverage từ vài chục e2e test chậm chạp, giòn.
- Không tách retry logic hợp lý khỏi flaky test thật: bật auto-retry 3 lần cho mọi e2e test để "che" flaky thay vì tìm root cause (thường là race condition trong test setup hoặc thiếu wait-for-condition đúng).
- Viết integration test nhưng mock một phần dependency quan trọng (ví dụ mock response thời gian, mock lỗi network) khiến test không còn phản ánh hành vi thật của hệ thống tích hợp.
- Để e2e test chặn merge của mọi PR trong khi suite đó chạy 40-60 phút, khiến engineer chờ đợi hoặc bypass CI gate bằng cách force-merge khi áp lực deadline.

## Interview Questions

**Hỏi**: Tại sao quá nhiều e2e test lại làm CI chậm và giòn (flaky), thay vì chỉ đơn giản là "test kỹ hơn"?

**Trả lời**: E2e test phụ thuộc vào trạng thái của nhiều hệ thống chạy đồng thời (DB, network, service khác, đôi khi cả trình duyệt thật), nên có nhiều điểm hỏng ngẫu nhiên không liên quan đến bug thật — timeout, race condition trong seed data, service phụ thuộc chưa sẵn sàng. Mỗi e2e test cũng cần dựng toàn bộ stack nên chạy chậm hơn unit test hàng trăm lần; nhân với số lượng test lớn, tổng thời gian CI và tỷ lệ fail giả đều tăng, làm giảm độ tin cậy của tín hiệu CI.

**Hỏi**: Nếu một bug chỉ bị bắt bởi e2e test chứ không phải unit/integration test, điều đó nói lên điều gì về test suite hiện tại?

**Trả lời**: Thường nghĩa là bug nằm ở tầng tích hợp thực sự (network, cấu hình, contract giữa service) mà integration test hiện tại không phủ đến — nên hành động đúng là bổ sung một integration test hẹp hơn để bắt lại đúng lớp lỗi đó với chi phí thấp hơn, chứ không phải kết luận "cần viết thêm e2e test", vì e2e chỉ nên là lưới an toàn cuối cùng, không phải công cụ chẩn đoán chính.

**Hỏi**: Tỷ lệ 70/20/10 của testing pyramid có nên áp dụng cứng nhắc cho mọi dự án không?

**Trả lời**: Không, tỷ lệ chỉ là kim chỉ nam định hướng, không phải quy tắc cứng; một hệ thống nhiều logic nghiệp vụ phức tạp, ít tích hợp bên ngoài sẽ nghiêng hẳn về unit test, còn một hệ thống chủ yếu là orchestration giữa nhiều service (ví dụ API gateway mỏng) có thể cần tỷ lệ integration test cao hơn tương ứng. Nguyên tắc bất biến là: lớp test nào chậm và giòn hơn thì phải ít hơn, và phải phủ đúng phần giá trị cao nhất chứ không phủ đều.

## Summary

Testing pyramid giải quyết vấn đề CI chậm và giòn bằng cách phân bổ test theo chi phí: nhiều unit test rẻ và tin cậy ở đáy, integration test vừa đủ để bắt lỗi tích hợp thật ở giữa, và rất ít e2e test đắt đỏ chỉ dành cho luồng nghiệp vụ quan trọng nhất ở đỉnh. Đảo ngược kim tự tháp tạo cảm giác an toàn giả trong khi thực tế làm chậm feedback loop và khiến engineer mất niềm tin vào CI vì flaky test. Chìa khoá là hiểu rõ mỗi tầng bắt được loại lỗi gì và trả giá bằng gì, rồi phân bổ số lượng test tương ứng chứ không viết test theo phản xạ "test càng giống thật càng tốt". Coverage phần trăm không thay thế được việc đánh giá test đó nằm ở tầng nào và tín hiệu nó tạo ra có đáng tin hay không.

## Knowledge Graph

- Test Doubles (Mock, Stub, Fake) — công cụ giúp unit test cô lập khỏi dependency bên ngoài, nền tảng của tầng đáy kim tự tháp.
- Testcontainers — công cụ dựng dependency thật (DB, queue) tái lập được cho integration test mà không cần môi trường staging thật.
- Flaky Test Detection — quy trình đo và cách ly test fail ngẫu nhiên, trực tiếp liên quan đến vấn đề "e2e giòn" trong bài này.
- Contract Testing (Pact) — kỹ thuật kiểm tra tương thích API giữa hai service mà không cần dựng cả hai lên chạy e2e đầy đủ.
- CI/CD Pipeline — nơi ba tầng test được sắp xếp thành các gate khác nhau với mức độ chặn merge khác nhau.
- Code Review Best Practices — thực hành liên quan: test coverage đủ ở đúng tầng là một trong các tiêu chí reviewer cần kiểm tra trước khi approve.

## Five Things To Remember

- Unit test nhiều vì rẻ và tín hiệu sạch, e2e test ít vì đắt và dễ giòn.
- Tỷ lệ 70/20/10 chỉ là kim chỉ nam, không phải luật cứng.
- Coverage phần trăm không nói lên test đó đáng tin hay không.
- Flaky test không được rerun cho qua, phải tìm root cause và cách ly.
- E2e chỉ nên phủ luồng nghiệp vụ quan trọng nhất, không phủ mọi nhánh logic.
