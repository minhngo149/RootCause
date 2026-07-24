---
id: code-review-best-practices
title: Code Review Best Practices
tags: ["engineering"]
---

# Code Review Best Practices

> Status: Draft

## Problem

Một team backend 8 người merge trung bình 30 PR/ngày. Reviewer chỉ có 10-15 phút mỗi PR trước standup, nên review thực chất là đọc lướt diff, thấy format đẹp, tên biến rõ ràng, rồi bấm approve. Sau 3 tháng, một PR "đã được review" hoá ra có race condition trong đoạn cập nhật số dư ví (không có lock, không có transaction), gây double-spend cho khoảng 200 giao dịch trong một đợt traffic cao. Reviewer trước đó comment duy nhất: "nên đặt tên `updateBalance` rõ hơn thành `updateWalletBalance`". Review đã xảy ra, nhưng nó review đúng thứ vô hại nhất và bỏ qua đúng thứ nguy hiểm nhất.

## Pain Points

- Bug logic/concurrency lọt qua review vì reviewer tiêu hết thời gian vào style/naming, phải fix hotfix production lúc 2h sáng.
- PR quá lớn (2000+ dòng) khiến reviewer approve theo quán tính ("chắc tác giả tự test rồi"), giảm tỷ lệ phát hiện lỗi thực đo được xuống dưới 20% (theo dữ liệu nội bộ nhiều tổ chức, so với PR dưới 400 dòng đạt 70-90%).
- Feedback công kích cá nhân ("code này quá tệ") làm engineer sợ đề xuất thay đổi kiến trúc, dẫn đến technical debt tích tụ vì không ai dám refactor.
- Review chậm (SLA không rõ ràng) làm PR nằm chờ nhiều ngày, tác giả context-switch sang việc khác, khi quay lại review comment thì phải nhớ lại toàn bộ ngữ cảnh — tốn gấp đôi thời gian so với review trong ngày.

## Solution

Code review hiệu quả là review có phân tầng ưu tiên rõ ràng: correctness và design trước, style sau cùng (và style nên được máy tự động hoá, không phải con người tranh luận). Cụ thể theo thứ tự giảm dần mức độ quan trọng: (1) logic có đúng không, có edge case nào bị bỏ sót, (2) design có phù hợp với phần còn lại của hệ thống không, có tạo ra coupling xấu không, (3) test coverage có đủ cho nhánh rẽ quan trọng không, (4) security/data-integrity (auth, injection, race condition, rò rỉ secret), và cuối cùng mới đến style — thứ mà linter/formatter (ESLint, Prettier, gofmt, black) nên xử lý tự động trước khi con người nhìn vào diff.

## How It Works

Cơ chế then chốt là tách "máy kiểm tra được" khỏi "người kiểm tra được". CI pipeline chạy formatter và linter ở pre-commit hook hoặc CI gate — nếu style sai, PR fail check trước khi đến tay reviewer, loại bỏ hoàn toàn nhu cầu con người comment "thiếu dấu chấm phẩy". Điều này giải phóng băng thông nhận thức của reviewer (context switching cost giữa "đọc style" và "đọc logic" là có thật, được đo trong nghiên cứu về interruption cost) để dồn vào phần khó: reviewer phải tự hỏi 3 câu cho mỗi hunk thay đổi — "code này có làm đúng điều nó tuyên bố làm không", "nếu input là null/rỗng/vượt giới hạn thì sao", "6 tháng nữa người khác đọc lại có hiểu tại sao code viết thế này không". Với PR nhỏ (khuyến nghị dưới 400 dòng diff, dựa trên nghiên cứu của Cisco về defect density giảm mạnh khi vượt ngưỡng này), reviewer có thể giữ toàn bộ ngữ cảnh thay đổi trong working memory và lần theo luồng dữ liệu qua từng hàm thay vì chỉ đọc từng file riêng lẻ. Feedback mang tính xây dựng vận hành theo nguyên tắc "phê bình hành vi của code, không phải năng lực của người viết": comment dạng "hàm này không xử lý trường hợp `list` rỗng, sẽ throw ở dòng 42 khi gọi `.first()`" thay vì "code này thiếu suy nghĩ" — cái đầu tiên actionable và objective, cái sau chỉ tạo phòng thủ tâm lý và không giúp tác giả sửa gì cả.

## Production Architecture

Trong một pipeline CI/CD production điển hình, code review nằm giữa hai lớp gate tự động: trước là pre-commit hook + CI (lint, format, unit test, security scan như Semgrep/Snyk chạy tự động, không cần người), sau là merge gate (yêu cầu tối thiểu N approval, branch protection rule chặn force-push vào main, bắt buộc CI xanh). Người review chỉ can thiệp vào phần máy không làm được: đánh giá đúng-sai về mặt nghiệp vụ và kiến trúc. Ở các tổ chức lớn, PR còn được gắn CODEOWNERS để tự động route đến đúng người có domain knowledge (ví dụ thay đổi ở module thanh toán bắt buộc một reviewer từ team payments), và một số pipeline chèn thêm bot review (ví dụ dùng LLM để pre-scan diff, gắn cờ các pattern rủi ro như SQL string concatenation hay thiếu try/catch quanh network call) để reviewer con người có điểm bắt đầu thay vì đọc trắng.

## Trade-offs

- PR nhỏ giảm rủi ro nhưng tăng overhead điều phối: một tính năng lớn phải chia thành 5-10 PR nối tiếp, đòi hỏi feature flag để giữ trạng thái deployable liên tục, làm chậm tốc độ ra tính năng ngắn hạn.
- Tập trung vào correctness/design nghĩa là chấp nhận một số style inconsistency nhỏ lọt qua nếu formatter chưa cấu hình đủ rule — đánh đổi thời gian đọc với sự đồng nhất tuyệt đối về hình thức.
- Yêu cầu nhiều approval (2+) tăng chất lượng nhưng tăng latency review, đặc biệt ở team nhỏ hoặc múi giờ lệch nhau, có thể biến review thành bottleneck thực sự cho throughput của team.
- Feedback xây dựng đòi hỏi thời gian viết comment chi tiết hơn ("code này sai vì X, sửa bằng cách Y") thay vì reject ngắn gọn, tốn thời gian reviewer hơn trong ngắn hạn dù tiết kiệm về dài hạn.

## Best Practices

- Giới hạn PR ở dưới 400 dòng diff thực chất (không tính file generated/lock file); nếu lớn hơn, yêu cầu tác giả tách nhỏ theo layer (data model, business logic, API) trước khi review.
- Tự động hoá 100% style/format qua CI gate (Prettier/gofmt/black + linter) để reviewer không bao giờ cần comment về khoảng trắng hay thứ tự import.
- Luôn kèm lý do và đề xuất cụ thể trong comment ("dòng 58: nếu `user` là null, `.role` sẽ throw NPE — nên guard bằng early return") thay vì chỉ nêu vấn đề suông.
- Đặt SLA rõ ràng cho review (ví dụ: first response trong 4 giờ làm việc) để tránh PR chết trong hàng chờ và tác giả mất context.
- Reviewer tự chạy thử code ở local hoặc đọc kỹ test case mới thêm khi thay đổi động chạm đến logic tiền tệ, quyền hạn, hoặc concurrency — không review bằng mắt trên GitHub UI cho những phần rủi ro cao.

## Common Mistakes

- Approve PR chỉ vì "test đã pass" mà không đọc xem test đó có thực sự cover đúng edge case hay chỉ test happy path.
- Dành phần lớn comment cho việc đổi tên biến/style trong khi bug logic ở ngay hunk kế bên bị bỏ qua vì "nhìn phức tạp quá, chắc tác giả biết mình đang làm gì".
- Review PR khổng lồ bằng cách lướt qua trong 5 phút rồi approve vì áp lực deadline, biến review thành thủ tục hình thức thay vì kiểm soát chất lượng thực sự.
- Dùng ngôn ngữ mang tính phán xét cá nhân trong comment (ví dụ "sao lại viết code kiểu này") thay vì mô tả vấn đề khách quan, khiến tác giả phòng thủ và review thoái hoá thành tranh cãi thay vì hợp tác.
- Không yêu cầu tác giả giải thích "tại sao" chọn cách tiếp cận này trong PR description, khiến reviewer phải đoán ý định thay vì đánh giá dựa trên ngữ cảnh thật.

## Interview Questions

**Hỏi**: Tại sao nên ưu tiên review correctness/design hơn là style trong code review?

**Trả lời**: Vì style có thể và nên được kiểm tra tự động bởi formatter/linter trong CI, không tốn băng thông nhận thức của con người. Correctness (logic sai, edge case bị bỏ sót, race condition) và design (coupling, khả năng mở rộng) chỉ con người có đủ ngữ cảnh nghiệp vụ để đánh giá được — đây là nơi review con người tạo ra giá trị thực sự, còn nếu tiêu hết thời gian vào style thì phần quan trọng nhất bị bỏ qua.

**Hỏi**: Vì sao PR nhỏ lại giúp phát hiện bug tốt hơn PR lớn, và có nghiên cứu/dữ liệu nào chứng minh không?

**Trả lời**: Với PR lớn, reviewer không đủ working memory để theo dõi luồng dữ liệu xuyên suốt nhiều file, dẫn đến review nông và bỏ sót lỗi; nghiên cứu về defect density (ví dụ dữ liệu Cisco code review) cho thấy tỷ lệ phát hiện lỗi giảm mạnh khi diff vượt quá vài trăm dòng, vì thời gian review hiệu quả trên mỗi dòng giảm dần theo kích thước diff (attention decay), trong khi PR nhỏ giữ được review sâu và nhanh.

**Hỏi**: Làm sao đưa ra feedback "mang tính xây dựng" mà không làm loãng vấn đề kỹ thuật cần sửa?

**Trả lời**: Tách rõ nhận xét về code khỏi nhận xét về người viết — mô tả cụ thể hành vi sai (input nào, dòng nào, hậu quả gì) kèm đề xuất sửa hoặc câu hỏi mở ("bạn đã cân nhắc trường hợp X chưa?") thay vì kết luận chủ quan. Điều này giữ review tập trung vào sự kiện khách quan, dễ actionable, và không kích hoạt phản ứng phòng thủ khiến tác giả bỏ qua phản hồi thay vì sửa.

## Summary

Code review hiệu quả đặt đúng thứ vào đúng lớp: máy xử lý style/format, con người xử lý correctness/design/security. PR nhỏ (dưới ~400 dòng) giữ được chất lượng review vì nằm trong giới hạn nhận thức của reviewer, còn PR lớn gần như luôn dẫn đến review hình thức. Feedback xây dựng — cụ thể, khách quan, kèm đề xuất — giữ được văn hoá hợp tác và khiến người viết thực sự sửa lỗi thay vì phòng thủ. Thiếu bất kỳ yếu tố nào trong ba yếu tố này (phân tầng ưu tiên, kích thước PR, chất lượng feedback), review vẫn "xảy ra" về mặt thủ tục nhưng không còn tạo ra giá trị kiểm soát chất lượng thực sự.

## Knowledge Graph

- CI/CD Pipeline — lớp gate tự động (lint, test, security scan) chạy trước khi PR đến tay reviewer con người.
- Feature Flags — công cụ cho phép chia PR lớn thành nhiều PR nhỏ mà vẫn giữ hệ thống deployable liên tục.
- Static Analysis / Linting — công cụ tự động hoá phần style để reviewer tập trung vào logic.
- Race Condition & Concurrency Bugs — loại lỗi điển hình mà review hời hợt (chỉ nhìn style) dễ bỏ sót nhất.
- Postmortem / Incident Review — quy trình liên quan khi một bug lọt qua review gây sự cố production.
- Trunk-Based Development — mô hình làm việc khuyến khích PR nhỏ, merge thường xuyên, giảm rủi ro tích luỹ.

## Five Things To Remember

- Để máy lo style, để người lo logic và design.
- PR càng nhỏ, bug càng dễ bị bắt.
- Comment phải chỉ ra dòng nào, sai gì, hậu quả gì — không chỉ nói "chưa ổn".
- Approve khi thực sự hiểu code, không phải khi hết thời gian.
- Phê bình code, không phê bình người viết code.
