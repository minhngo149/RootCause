---
id: technical-debt-management
title: Technical Debt Management
tags: ["engineering"]
---

# Technical Debt Management

> Status: Draft

## Problem

Một team backend nhận yêu cầu launch tính năng thanh toán trong 3 tuần cho một deadline khách hàng lớn. Để kịp, engineer hardcode logic tính phí theo một loại tiền tệ duy nhất thay vì dùng module đa tiền tệ đã có sẵn, bỏ qua idempotency key cho API charge, và note lại trong Slack "sẽ sửa sau launch". Sáu tháng sau, công ty mở rộng sang thị trường Singapore, và không ai còn nhớ đoạn hardcode đó nằm ở đâu — cho đến khi một khách hàng trả bằng SGD bị tính phí theo tỷ giá USD, và một retry từ client do timeout mạng gây double-charge vì không có idempotency key. Nợ kỹ thuật không được ghi nhận ở đâu ngoài một dòng chat đã trôi mất trong lịch sử kênh, nên nó không được trả — nó chỉ nằm im chờ phát nổ.

## Pain Points

- Nợ kỹ thuật không được track biến thành "known unknown" cho người viết ra nó nhưng là "unknown unknown" cho mọi người khác — khi engineer đó nghỉ việc, không ai biết rủi ro nằm ở đâu cho đến khi outage xảy ra.
- Chi phí trả nợ tăng phi tuyến theo thời gian: sửa một hardcode ngay sau khi viết tốn 30 phút, sửa cùng đoạn đó sau khi 5 module khác đã phụ thuộc vào hành vi sai của nó có thể tốn nhiều tuần refactor và regression testing.
- Không phân biệt nợ cố ý và vô ý khiến team hoặc trả nợ không cần thiết (tốn effort refactor code không ai động vào), hoặc bỏ sót nợ nguy hiểm (không refactor đúng chỗ đang tích luỹ rủi ro cao).
- "Feature freeze để trả nợ" là lựa chọn thường bị business từ chối hoặc chỉ chấp thuận một lần rồi không lặp lại, khiến nợ tích luỹ liên tục cho đến khi velocity của team giảm rõ rệt và mọi feature mới đều chậm gấp đôi vì phải né tránh vùng code mục nát.

## Solution

Quản lý nợ kỹ thuật là phân loại nợ theo hai trục — cố ý (deliberate) và vô ý (inadvertent) — rồi ghi nhận nó thành một artifact có thể theo dõi (ticket, không phải comment TODO hay tin nhắn Slack), gắn cho nó một "lãi suất" ước lượng (chi phí sẽ tăng bao nhanh nếu không trả), và trả nó theo một tỷ lệ cố định song song với feature work thay vì chờ một đợt "big bang" refactor riêng biệt. Nợ cố ý là quyết định có ý thức đánh đổi tốc độ lấy chất lượng ("mình biết cách này chưa tối ưu nhưng cần ship kịp deadline") — loại này cần ai đó chấp nhận rủi ro và ghi lại lý do. Nợ vô ý là nợ phát sinh từ thiếu hiểu biết hoặc thay đổi requirement mà không ai chủ động chọn ("lúc viết không biết sẽ cần scale đến mức này") — loại này thường chỉ lộ ra qua code review, incident, hoặc khi engineer mới đọc code và hỏi "tại sao lại làm thế này".

## How It Works

Cơ chế cốt lõi là biến nợ từ trạng thái ngầm (implicit, chỉ tồn tại trong đầu người viết code hoặc một comment rải rác) thành trạng thái tường minh (explicit, có ticket, có owner, có severity). Mỗi khi một quyết định đánh đổi được đưa ra — bỏ qua test coverage cho một nhánh hiếm gặp, dùng polling thay vì webhook vì chưa có thời gian implement đúng, hardcode một giá trị cấu hình thay vì đưa vào config service — engineer tạo một ticket "tech debt" ngay tại thời điểm đó, gắn nhãn phân loại (ví dụ theo ma trận của Martin Fowler: reckless/prudent × deliberate/inadvertent), ước lượng "lãi suất" là mức độ tăng chi phí nếu để lâu (ví dụ: nợ ở code path chạy 10 triệu lần/ngày có lãi suất cao hơn nhiều so với nợ ở một cron job chạy 1 lần/tháng), và liên kết ticket đó với đúng dòng code qua comment kiểu `// TECH-DEBT: TICKET-1234 — hardcoded USD, cần multi-currency trước khi launch SG`. Việc trả nợ không dừng feature work mà được nhúng vào chính vòng đời feature: mỗi sprint dành một tỷ lệ cố định (phổ biến 15-20% capacity, tương tự nguyên tắc "20% time" nhưng áp dụng cho nợ thay vì đổi mới) để trả các ticket có lãi suất cao nhất, và mỗi khi engineer chạm vào một vùng code có nợ đã biết (do đang implement feature liên quan), họ trả một phần nợ đó luôn trong cùng PR thay vì tạo thêm nợ chồng lên nợ cũ — nguyên tắc "boy scout rule" áp dụng có chủ đích.

## Production Architecture

Trong một hệ thống production thực tế, tech debt được track song song với hai luồng riêng biệt trong cùng một issue tracker (Jira, Linear): luồng feature/bug thông thường, và một label/component riêng `tech-debt` có trường bắt buộc là "lãi suất ước lượng" (ví dụ severity: critical/high/medium/low dựa trên tần suất code path bị chạm và mức độ rủi ro nếu fail) và "ngày phát hiện". Một dashboard (ví dụ query JQL định kỳ hoặc báo cáo tự động vào kênh Slack hàng tuần) hiển thị tổng số nợ đang mở theo severity, tuổi trung bình của mỗi ticket, và tốc độ trả nợ theo sprint — cho engineering manager thấy debt có đang tăng ròng hay giảm ròng. SonarQube hoặc CodeClimate chấm điểm "code smell" và "cyclomatic complexity" tự động, sinh ra một phần nợ vô ý được phát hiện máy móc (không cần con người nhớ ghi lại) và tính ra chỉ số "technical debt ratio" (tỷ lệ thời gian ước tính để sửa hết smell so với thời gian đã viết code). Ở review kiến trúc định kỳ (ví dụ quarterly architecture review), các khoản nợ cố ý lớn (ví dụ "đang dùng monolith cho module X, cần tách microservice khi traffic vượt ngưỡng Y") được escalate lên roadmap cấp cao thay vì nằm mãi trong backlog kỹ thuật của một team.

## Trade-offs

- Dành 15-20% sprint capacity cho trả nợ nghĩa là feature velocity ngắn hạn chậm hơn so với việc dồn 100% vào feature — đây là khoản đầu tư có lợi tức trễ, khó chứng minh giá trị tức thời cho stakeholder chỉ nhìn roadmap quý này.
- Ghi nhận nợ tường minh (ticket cho mọi đánh đổi) tạo overhead quy trình: engineer phải dừng lại viết ticket, ước lượng lãi suất, thay vì chỉ viết code và tiếp tục — với team nhỏ hoặc dưới áp lực deadline gắt, bước này dễ bị bỏ qua đầu tiên.
- Phân loại cố ý/vô ý không phải lúc nào cũng rõ ràng: một quyết định "cố ý" ở thời điểm viết (dựa trên thông tin lúc đó) có thể trông giống "vô ý" 1 năm sau khi requirement đã đổi hoàn toàn — khiến việc quy trách nhiệm hoặc ưu tiên trả nợ trở nên tranh cãi.
- "Boy scout rule" (trả nợ khi chạm vào code) làm PR to hơn và khó review hơn vì trộn lẫn thay đổi feature với refactor không liên quan trực tiếp, vi phạm nguyên tắc PR nhỏ và tập trung một mục đích.

## Best Practices

- Viết ticket tech-debt ngay tại thời điểm quyết định đánh đổi, không chờ "khi nào rảnh sẽ ghi lại" — nợ không ghi ngay gần như chắc chắn bị quên trong vòng vài tuần.
- Gắn nợ với mức độ ảnh hưởng đo được (số lượng request/ngày đi qua code path đó, số incident liên quan) thay vì chỉ đánh giá cảm tính "code này xấu", để ưu tiên đúng nợ nguy hiểm nhất trước.
- Dành một tỷ lệ sprint cố định và bảo vệ nó khỏi bị cắt khi có áp lực deadline — nếu tỷ lệ này linh hoạt tuỳ ý, nó luôn bị hy sinh đầu tiên và về 0 theo thời gian.
- Với mọi nợ cố ý, yêu cầu người quyết định ghi rõ điều kiện "khi nào phải trả" (ví dụ: trước khi traffic vượt X req/s, trước khi launch thị trường Y) thay vì để mở vô thời hạn.
- Review danh sách nợ đang mở định kỳ (ví dụ hàng quý) ở cấp kiến trúc, không chỉ ở cấp sprint planning, để các khoản nợ lớn có tác động chiến lược không bị chôn trong backlog của một team.

## Common Mistakes

- Coi mọi nợ kỹ thuật là như nhau và xử lý theo thứ tự "cái nào cũ nhất trước" thay vì theo lãi suất thực tế — sửa một nợ vô hại đã 2 năm trong khi bỏ qua một nợ mới 2 tuần đang nằm trên code path xử lý giao dịch tiền.
- Đề xuất "feature freeze 2 tuần để trả nợ" như giải pháp duy nhất — business gần như luôn từ chối hoặc chỉ chấp nhận một lần, và trong lúc đó nợ mới vẫn tiếp tục phát sinh từ các team khác không bị ảnh hưởng bởi freeze.
- Dùng comment `// TODO: fix later` rải rác trong code thay vì ticket có structure — comment này không có owner, không severity, không hạn, và biến mất khỏi tầm nhìn của mọi người trừ người vô tình mở đúng file đó.
- Trộn lẫn "tôi không thích cách code này được viết" (sở thích cá nhân) với nợ kỹ thuật thực sự (rủi ro đo được) — làm danh sách nợ phình to với các mục không có tác động thực, khiến người ưu tiên mất niềm tin vào toàn bộ danh sách.
- Không bao giờ trả nợ vì luôn ưu tiên feature 100% capacity, dẫn đến một thời điểm bắt buộc phải rewrite toàn bộ module vì không còn ai dám thêm code vào đó nữa — chi phí rewrite luôn cao hơn nhiều so với trả nợ dần.

## Interview Questions

**Hỏi**: Phân biệt nợ kỹ thuật cố ý và vô ý, và tại sao sự phân biệt này quan trọng trong cách quản lý?

**Trả lời**: Nợ cố ý là quyết định có ý thức đánh đổi tốc độ lấy chất lượng, có người chịu trách nhiệm và biết rõ đang đánh đổi gì (ví dụ bỏ qua edge case hiếm để kịp deadline). Nợ vô ý phát sinh từ thiếu hiểu biết hoặc requirement thay đổi mà không ai chủ động chọn lúc viết code. Phân biệt này quan trọng vì cách xử lý khác nhau: nợ cố ý cần điều kiện trả rõ ràng được thống nhất từ đầu, còn nợ vô ý cần cơ chế phát hiện (code review, static analysis, incident) vì không ai chủ động biết nó tồn tại.

**Hỏi**: Làm sao trả nợ kỹ thuật mà không dừng feature work, khi business luôn ưu tiên tính năng mới?

**Trả lời**: Dành một tỷ lệ sprint capacity cố định (thường 15-20%) cho trả nợ và bảo vệ nó khỏi bị cắt bởi áp lực deadline, thay vì xin một đợt "feature freeze" riêng biệt mà business gần như luôn từ chối. Đồng thời áp dụng nguyên tắc trả nợ tại chỗ khi chạm vào code có nợ đã biết trong lúc làm feature liên quan, biến việc trả nợ thành một phần tự nhiên của luồng công việc thay vì một dự án riêng cần xin phê duyệt.

**Hỏi**: Làm sao ưu tiên nợ nào cần trả trước khi danh sách nợ có hàng trăm ticket?

**Trả lời**: Ưu tiên theo "lãi suất" thực tế — ước lượng dựa trên tần suất code path đó được thực thi và mức độ rủi ro/chi phí nếu nó fail, không theo tuổi của ticket hay cảm tính về độ "xấu" của code. Một nợ nằm trên code path xử lý 10 triệu request/ngày với rủi ro data corruption phải được ưu tiên trước một nợ ở một cron job nội bộ chạy 1 lần/tháng, bất kể ticket nào cũ hơn.

## Summary

Quản lý nợ kỹ thuật hiệu quả bắt đầu từ việc biến nợ ngầm thành nợ tường minh — mọi đánh đổi được ghi lại thành ticket có owner, severity, và điều kiện trả, thay vì tồn tại dưới dạng comment TODO hay ký ức của một engineer. Phân biệt nợ cố ý và vô ý quyết định cách xử lý: nợ cố ý cần điều kiện trả rõ ràng ngay từ đầu, nợ vô ý cần cơ chế phát hiện chủ động qua review và static analysis. Trả nợ nên là một tỷ lệ capacity cố định chạy song song feature work, được bảo vệ khỏi áp lực deadline, thay vì một đợt "big bang" riêng biệt mà business hiếm khi chấp thuận. Ưu tiên trả nợ theo lãi suất đo được (tần suất, rủi ro) chứ không theo tuổi ticket hay cảm tính. Bỏ qua toàn bộ quy trình này không làm nợ biến mất — nó chỉ tích luỹ âm thầm cho đến khi chi phí trả nó vượt xa chi phí đã tiết kiệm được lúc đầu.

## Knowledge Graph

- Code Review Best Practices — nơi phần lớn nợ vô ý được phát hiện sớm nhất trước khi merge vào production.
- Postmortem / Incident Review — quy trình thường phát hiện ra nợ kỹ thuật ẩn giấu sau khi nó đã gây sự cố thực tế.
- Feature Flags — công cụ cho phép trả nợ (ví dụ thay thế implementation cũ) mà không cần dừng ship feature mới.
- Blue-Green/Canary Deployment — liên quan khi trả nợ đòi hỏi thay đổi lớn về hạ tầng hoặc kiến trúc cần rollout an toàn.
- Static Analysis / Code Quality Tools (SonarQube, CodeClimate) — cơ chế phát hiện tự động một phần nợ vô ý mà con người không cần nhớ ghi lại.
- Martin Fowler's Technical Debt Quadrant — mô hình phân loại nợ theo hai trục reckless/prudent và deliberate/inadvertent, nền tảng lý thuyết cho cách phân loại trong bài này.

## Five Things To Remember

- Nợ không ghi thành ticket ngay lúc phát sinh gần như chắc chắn bị quên.
- Nợ cố ý cần điều kiện trả rõ ràng; nợ vô ý cần cơ chế phát hiện chủ động.
- Ưu tiên trả nợ theo lãi suất thực đo được, không theo tuổi ticket.
- Dành tỷ lệ capacity cố định để trả nợ, đừng chờ một đợt feature freeze không bao giờ đến.
- Chi phí trả nợ tăng phi tuyến theo thời gian — càng để lâu càng đắt.
