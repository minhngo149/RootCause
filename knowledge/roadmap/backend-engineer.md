---
id: backend-engineer
title: Backend Engineer
tags: ["roadmap", "career"]
---

# Backend Engineer

> Status: Draft

## Problem

Một team backend thường có nhiều Junior viết được feature khi có chỉ dẫn rõ, và một vài Senior/Staff lo kiến trúc tổng thể, nhưng lại thiếu lớp giữa: người có thể nhận một yêu cầu nghiệp vụ mơ hồ (vd. "cho phép user hủy đơn trong vòng 30 phút"), tự thiết kế API, tự quyết định schema DB, tự viết test đủ tin cậy để deploy production mà không cần ai review từng dòng logic. Backend Engineer (mid-level) là cấp bậc lấp đúng khoảng trống này — người làm chủ toàn bộ vòng đời một feature từ thiết kế đến vận hành, nhưng chưa cần gánh trách nhiệm kiến trúc toàn hệ thống hay quyết định công nghệ cho cả team. Thiếu lớp này, Senior buộc phải tự tay làm luôn phần triển khai chi tiết, còn Junior thì bị giao việc vượt quá năng lực và tạo ra nợ kỹ thuật.
## Pain Points

- Team thiếu Backend Engineer mid-level khiến Senior/Staff bị kéo xuống làm việc triển khai chi tiết (viết CRUD, sửa bug logic) thay vì dành thời gian cho kiến trúc, mentoring, và quyết định kỹ thuật có phạm vi ảnh hưởng lớn hơn.
- Junior được giao thẳng feature đòi hỏi thiết kế API và schema DB độc lập thường tạo ra API không nhất quán (vd. một endpoint trả `snake_case`, endpoint khác trả `camelCase`) hoặc schema thiếu index/ràng buộc, gây nợ kỹ thuật phải dọn sau nhiều tháng.
- Kỹ sư không biết rõ tiêu chí "mid-level" thường bị kẹt ở việc chỉ hoàn thành ticket được giao đúng như mô tả, không tự đặt câu hỏi về edge case, không viết test cho nhánh lỗi — dẫn đến bug production lặp lại dù code review đã pass.
- Thiếu chuẩn rõ ràng cho cấp bậc này khiến việc đánh giá performance review mang tính cảm tính, kỹ sư giỏi bị đánh giá ngang kỹ sư mới vì không ai định nghĩa được sự khác biệt thực sự giữa hai cấp.

## Solution

Backend Engineer (mid-level) là người thành thạo ít nhất một ngôn ngữ backend ở mức sản xuất (không chỉ biết cú pháp mà hiểu runtime, concurrency model, và pitfall của nó), tự thiết kế được API cho một domain vừa phải (không cần review từng endpoint), hiểu và vận dụng đúng các khái niệm DB cốt lõi (index, transaction, isolation level ở mức đủ dùng), và viết test đủ để tự tin deploy mà không cần QA thủ công toàn bộ luồng. Vai trò chính là nhận một yêu cầu nghiệp vụ, tự chia nhỏ thành các quyết định kỹ thuật (schema, API contract, error handling), và giao tiếp được các trade-off đó với Senior hoặc PM mà không cần cầm tay chỉ việc.

## How It Works

Trục kỹ thuật cốt lõi gồm bốn phần cụ thể: (1) **Ngôn ngữ/runtime** — hiểu memory model, cách xử lý concurrency (thread pool, event loop, async/await tùy stack), và pitfall runtime phổ biến (vd. blocking I/O trong Node.js event loop, N+1 goroutine leak trong Go); (2) **API design** — biết chọn đúng REST resource shape, versioning strategy, status code phù hợp, xử lý idempotency cho POST/PUT khi client retry, và pagination cho response lớn; (3) **Database** — hiểu khi nào cần index, đọc được execution plan cơ bản, biết isolation level nào phù hợp cho use case nào (vd. Read Committed đủ cho hầu hết CRUD, cần Serializable hoặc optimistic lock cho race condition tài chính); (4) **Testing** — viết unit test cho logic nghiệp vụ, integration test cho tương tác DB/API thật, và biết phân biệt khi nào mock, khi nào cần test với dependency thật.

Trục phi kỹ thuật quan trọng không kém: phạm vi ảnh hưởng (impact scope) mở rộng từ "hoàn thành đúng ticket" sang "chủ động phát hiện edge case không có trong spec" (vd. tự hỏi "nếu user gọi API hủy đơn hai lần liên tiếp thì sao?" trước khi bị hỏi). Giao tiếp kỹ thuật là việc viết được một PR description giải thích rõ trade-off đã chọn (vd. "chọn eventual consistency ở đây vì latency quan trọng hơn accuracy tức thời"), và biết khi nào cần escalate lên Senior thay vì tự quyết định một mình (vd. thay đổi ảnh hưởng tới schema được 3 service khác dùng chung).

## Production Architecture

Trong cấu trúc team thực tế, Backend Engineer mid-level thường báo cáo cho một Engineering Manager hoặc Tech Lead, làm việc trực tiếp với Product Manager để nhận requirement và tự chuyển hóa thành technical spec ngắn (không cần Senior viết hộ), và phối hợp ngang hàng với Frontend Engineer để thống nhất API contract trước khi code (thường qua OpenAPI spec hoặc RFC ngắn). Họ thường là người được giao ownership một microservice hoặc một module lớn trong monolith (vd. "service quản lý payment"), chịu trách nhiệm on-call cho phần đó, và là người đầu tiên được page khi service đó gặp sự cố production trước khi escalate lên Senior nếu vượt quá hiểu biết. Trong review process, PR của họ vẫn cần Senior duyệt cho các thay đổi ảnh hưởng kiến trúc, nhưng với thay đổi trong phạm vi module họ sở hữu, review thường chỉ tập trung vào chất lượng code chứ không phải tính đúng đắn của thiết kế tổng thể.

## Trade-offs

Lên mid-level đồng nghĩa với nhận thêm ownership và ít được cầm tay chỉ việc hơn, đổi lại là ít thời gian "code thuần túy" hơn vì phải dành thời gian viết spec, review PR của Junior, và tham gia thảo luận thiết kế trước khi code. Kỹ sư quen với việc chỉ nhận ticket rõ ràng có thể thấy giai đoạn chuyển tiếp này khó chịu vì đột ngột phải tự chịu trách nhiệm cho các quyết định mơ hồ (vd. "API này nên trả 404 hay 200 với body rỗng khi không tìm thấy resource?") mà trước đây luôn có người quyết định hộ. Đồng thời, việc mở rộng phạm vi kỹ thuật (API, DB, testing) đồng nghĩa với việc phải hy sinh chiều sâu chuyên môn cực đại ở một mảng hẹp — mid-level là giai đoạn "rộng vừa đủ, sâu vừa đủ", chưa phải lúc chuyên sâu một ngách như Staff Engineer về sau.

## Best Practices

- Chủ động viết technical spec ngắn (1 trang, gồm API contract và schema thay đổi) trước khi code cho bất kỳ feature nào lớn hơn một PR nhỏ, để Senior review thiết kế trước khi tốn công triển khai sai hướng.
- Luôn tự hỏi "case lỗi/edge case nào không có trong spec" trước khi coi ticket là hoàn thành — đây là ranh giới rõ nhất giữa Junior (làm đúng spec) và mid-level (mở rộng spec).
- Viết test cho cả nhánh lỗi (invalid input, race condition, timeout) chứ không chỉ happy path, và đảm bảo test chạy được độc lập với dữ liệu thật trong CI.
- Đọc execution plan của các query quan trọng trước khi merge, không chỉ tin tưởng ORM tự tối ưu — đây là kỹ năng phân biệt rõ người hiểu DB thật với người chỉ biết gọi ORM.
- Chủ động xin feedback định kỳ về phạm vi ảnh hưởng (không chỉ chất lượng code) từ Senior/Manager, vì tiêu chí lên Senior thường nằm ở impact chứ không chỉ ở kỹ năng code thuần túy.

## Common Mistakes

- Chỉ tối ưu cho "code chạy đúng" mà bỏ qua việc giao tiếp trade-off với team, khiến Senior phải audit lại toàn bộ quyết định thiết kế sau khi code đã xong, gây lãng phí công sức làm lại.
- Tự thiết kế API/schema mà không tham khảo pattern đã có trong hệ thống, dẫn đến convention rời rạc (mỗi module một style khác nhau) làm codebase khó bảo trì về lâu dài.
- Coi testing là bước phụ làm cho có, chỉ viết test cho happy path, khiến bug ở nhánh lỗi lọt qua CI và chỉ phát hiện khi khách hàng report.
- Không dám tự quyết định vì sợ sai, cứ hỏi lại Senior cho mọi việc nhỏ, khiến bản thân mãi không thoát khỏi vai trò Junior dù đã đủ kỹ năng kỹ thuật.
- Ngược lại, tự quyết định cả những thay đổi vượt phạm vi ownership của mình (vd. đổi schema được nhiều service dùng chung) mà không escalate, gây incident ảnh hưởng rộng hơn dự kiến.

## Interview Questions

**Hỏi**: Điều gì phân biệt rõ nhất một Backend Engineer mid-level với một Junior Engineer, ngoài số năm kinh nghiệm?

**Trả lời**: Không phải số năm mà là phạm vi tự chủ: Junior hoàn thành đúng spec được giao, mid-level tự phát hiện được edge case không có trong spec, tự thiết kế API/schema cho một domain vừa phải mà không cần review từng chi tiết, và tự viết test đủ tin cậy để deploy mà không cần QA thủ công toàn bộ luồng.

**Hỏi**: Khi nhận một yêu cầu nghiệp vụ mơ hồ như "cho phép user hủy đơn trong 30 phút", bạn sẽ bắt đầu từ đâu?

**Trả lời**: Trước tiên làm rõ các case biên bằng câu hỏi cụ thể (đơn đã thanh toán thì sao, hủy hai lần liên tiếp thì sao, timezone tính "30 phút" theo server hay client), sau đó viết spec ngắn mô tả API contract (endpoint, status code cho từng case) và thay đổi schema cần thiết (vd. thêm cột `cancellable_until`), rồi mới bắt đầu code kèm test cho cả case hợp lệ và các case biên đã liệt kê.

**Hỏi**: Bạn xử lý thế nào khi một thay đổi bạn định làm ảnh hưởng tới một service khác không thuộc phạm vi bạn sở hữu?

**Trả lời**: Escalate và trao đổi trước với owner của service đó hoặc Tech Lead thay vì tự quyết định một mình — đây chính là ranh giới giữa việc tự chủ trong phạm vi ownership và việc gây rủi ro ngoài phạm vi kiểm soát của mình, một dấu hiệu quan trọng khi đánh giá năng lực ra quyết định ở cấp mid-level.

## Summary

Backend Engineer mid-level là người làm chủ toàn bộ vòng đời một feature — từ thiết kế API, schema DB, đến testing và vận hành — trong phạm vi một module hoặc service cụ thể, mà không cần Senior cầm tay chỉ việc từng bước. Năng lực cốt lõi gồm bốn trục kỹ thuật (ngôn ngữ/runtime, API design, database, testing) cộng với khả năng phi kỹ thuật quan trọng hơn về lâu dài là mở rộng phạm vi ảnh hưởng và giao tiếp trade-off rõ ràng. Thiếu lớp kỹ sư này khiến Senior bị kéo xuống làm việc chi tiết còn Junior bị giao việc vượt năng lực, tạo ra nợ kỹ thuật và đánh giá performance thiếu tiêu chí rõ ràng. Đánh đổi khi lên cấp này là ít thời gian code thuần túy hơn để đổi lấy ownership và tự chủ ra quyết định. Ranh giới rõ nhất để phân biệt mid-level với Junior không phải là kỹ năng code mà là khả năng tự phát hiện edge case và biết khi nào cần escalate thay vì tự quyết định vượt phạm vi.

## Knowledge Graph

- Senior Engineer — cấp bậc tiếp theo, mở rộng phạm vi ảnh hưởng từ một module sang toàn bộ kiến trúc hệ thống.
- N+1 Query — lỗi DB điển hình mà năng lực đọc execution plan ở cấp mid-level giúp phát hiện trước khi merge.
- ACID — kiến thức nền tảng transaction mà mid-level cần hiểu để chọn đúng isolation level cho use case.
- Isolation Levels — trục năng lực database cụ thể mid-level cần vận dụng đúng, không chỉ biết định nghĩa.
- Saga Pattern — kỹ thuật thiết kế API/transaction phân tán mid-level cần làm quen khi hệ thống chuyển sang microservices.
- Code Review — kỹ năng giao tiếp kỹ thuật gắn liền với việc viết PR description rõ trade-off ở cấp mid-level.

## Five Things To Remember

- Mid-level được đo bằng phạm vi tự chủ, không phải số năm kinh nghiệm hay khối lượng code viết ra.
- Ranh giới rõ nhất với Junior là khả năng tự phát hiện edge case không có trong spec.
- Bốn trục kỹ thuật cần vững là ngôn ngữ/runtime, API design, database, và testing thực chất chứ không hời hợt.
- Biết khi nào tự quyết định và khi nào escalate quan trọng ngang với kỹ năng code.
- Lên cấp đổi lấy ownership rộng hơn bằng ít thời gian code thuần túy hơn, đây là đánh đổi cần chấp nhận chứ không phải dấu hiệu đi lệch hướng.
