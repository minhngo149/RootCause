---
id: feature-flags
title: Feature Flags
tags: ["engineering"]
---

# Feature Flags

> Status: Draft

## Problem

Team deploy một tính năng lớn (redesign checkout, thay đổi thuật toán tính phí ship) bằng cách merge trực tiếp vào `main` và release nguyên khối — nếu tính năng có bug ở production, cách duy nhất để dừng thiệt hại là rollback toàn bộ deploy, kéo theo mọi thay đổi khác (bug fix, tính năng nhỏ) đã đi kèm trong cùng release bị hoàn tác theo, kể cả những phần hoàn toàn không liên quan. Vì deploy và release bị coi là một hành động duy nhất, team buộc phải chọn giữa hai thái cực tệ: hoặc trì hoãn deploy code đã xong (đợi tính năng "chín" 100% mới merge, feature branch sống hàng tuần, conflict dồn ứ), hoặc bật tính năng cho toàn bộ user cùng lúc và chấp nhận rủi ro "big bang launch" không thể revert cục bộ. Không có cơ chế nào tách "code đã lên server production" khỏi "user thấy hành vi mới", nên thử nghiệm với một nhóm nhỏ, tắt riêng một phần lỗi mà không đụng phần còn lại, hay A/B test đều gần như bất khả thi nếu chỉ dựa vào git branch và CI/CD pipeline.

## Pain Points

- Rollback toàn bộ vì một bug cục bộ: một lỗi nhỏ trong tính năng mới buộc revert deploy chứa hàng chục commit khác, kéo lùi cả những fix quan trọng không liên quan, làm chậm toàn bộ team trong lúc điều tra.
- Không kiểm soát được blast radius: tính năng mới bật cho 100% traffic ngay khi deploy, nếu có bug logic (tính sai giá, sai luồng thanh toán) thì toàn bộ user bị ảnh hưởng cùng lúc thay vì chỉ 1-5% nhóm canary.
- Feature branch sống quá lâu gây merge hell: để tránh release nửa vời, engineer giữ code trên branch riêng hàng tuần/tháng, dẫn tới conflict lớn khi merge, và code review mất ngữ cảnh vì thay đổi quá nhiều cùng lúc.
- Không thể A/B test hay thử nghiệm dần: muốn so sánh thuật toán recommendation cũ và mới trên từng nhóm user đòi hỏi hai codebase song song hoặc deploy riêng biệt, thay vì bật/tắt runtime cho từng cohort.
- Kill switch không tồn tại: khi một tích hợp bên thứ ba (payment gateway, search service) bắt đầu lỗi ở production, không có cách nào tắt riêng phần phụ thuộc đó mà không rollback cả deploy — sự cố kéo dài lâu hơn cần thiết vì đội ứng trực phải chờ pipeline CI/CD chạy lại thay vì bấm một nút.

## Solution

Feature flag (hay feature toggle) là cơ chế bọc một đoạn code hoặc hành vi trong một điều kiện runtime có thể bật/tắt độc lập với việc deploy code đó lên production. Ý tưởng cốt lõi: deploy (đưa code lên server) và release (cho phép user thấy/dùng tính năng) là hai hành động tách biệt — code có thể nằm im trên production, "ngủ" sau một flag đang tắt, và được kích hoạt bất cứ lúc nào (cho toàn bộ user, một phần trăm nhỏ, hoặc một danh sách user cụ thể) mà không cần deploy lại. Flag được đánh giá tại runtime dựa trên cấu hình lấy từ một nguồn tập trung (config service, database, SDK của bên thứ ba như LaunchDarkly/Unleash/Flagsmith), cho phép thay đổi hành vi ứng dụng gần như tức thời mà không đụng vào pipeline CI/CD.

## How It Works

**Vòng đời một feature flag**: flag thường trải qua các trạng thái: `off` (mặc định khi mới tạo, code path mới hoàn toàn không chạy) → `on for internal/canary` (bật cho nhân viên nội bộ hoặc 1-5% traffic để quan sát metrics, log lỗi) → `on for percentage rollout` (tăng dần 10% → 50% → 100% theo từng bước, theo dõi error rate và latency ở mỗi bước) → `on for everyone` (ổn định, coi như default) → cuối cùng flag phải được xóa khỏi code (dọn dẹp cả nhánh `if` lẫn cấu hình), vì một khi tính năng đã ổn định vĩnh viễn, flag chỉ còn là code chết đang được đánh giá ở runtime một cách vô ích.

**Đánh giá flag ở runtime**: mỗi lần code chạy tới điểm có flag, application gọi SDK (hoặc kiểm tra config cục bộ) với input là flag key và evaluation context (user ID, org ID, region, % rollout bucket được tính từ hash của user ID để đảm bảo cùng một user luôn nhận cùng kết quả qua nhiều lần gọi). SDK trả về boolean (hoặc variant, nếu là multivariate flag dùng cho A/B test) dựa trên rule engine: targeting rules (user thuộc nhóm beta), percentage rollout (hash(user_id) % 100 < rollout_percent), và fallback mặc định nếu không rule nào khớp. Để tránh mỗi lần evaluate phải gọi network tới flag service (thêm latency vào critical path), SDK production-grade luôn cache toàn bộ ruleset cục bộ trong bộ nhớ ứng dụng, chỉ đồng bộ định kỳ (poll mỗi vài giây) hoặc qua streaming connection (SSE/WebSocket) khi rule thay đổi — việc evaluate thực tế là một phép tính cục bộ, không phải network call.

**Kiến trúc phân phối cấu hình**: flag service trung tâm lưu định nghĩa flag và targeting rules, expose qua API cho dashboard quản trị (để engineer/PM bật tắt) và qua streaming/polling endpoint cho SDK client. Với hệ thống nhiều instance (auto-scaling, nhiều pod Kubernetes), mỗi instance chạy SDK riêng, đồng bộ độc lập từ cùng flag service — không có single point of failure ở đường đánh giá flag vì mỗi instance tự quyết định dựa trên bản cache cục bộ, kể cả khi flag service tạm thời không phản hồi (SDK dùng lại giá trị cache cuối cùng, hoặc fallback về default an toàn được định nghĩa cứng trong code).

**Kill switch khác targeting rollout**: kill switch là dạng flag đặc biệt dùng để tắt khẩn cấp một tính năng hoặc một tích hợp bên ngoài đang gây sự cố (ví dụ tắt gọi tới một recommendation service đang timeout, fallback về danh sách tĩnh) — mục tiêu là phản ứng nhanh (giây, không phải phút) nên kill switch luôn được thiết kế để đánh giá cực rẻ (một boolean đơn giản, không cần targeting phức tạp) và không phụ thuộc vào cùng hạ tầng có thể đang là nguyên nhân sự cố.

## Production Architecture

Trong một hệ thống e-commerce production, feature flag thường xuất hiện ở ba lớp: (1) release flag bọc tính năng đang phát triển (redesign trang sản phẩm), cho phép merge code vào `main` sớm và deploy liên tục dù tính năng chưa hoàn thiện, chỉ bật khi PM quyết định; (2) experiment flag (multivariate) dùng cho A/B test thuật toán ranking, gắn với hệ thống phân tích để đo conversion rate theo từng variant; (3) ops flag (kill switch) bọc quanh mọi lời gọi tới dịch vụ bên ngoài không do team kiểm soát (payment gateway, third-party search), cho phép đội trực tắt ngay khi dịch vụ đó có sự cố mà không cần deploy. Các công ty ở quy mô lớn (Uber, Airbnb, Shopify) đều vận hành flag service nội bộ hoặc dùng SaaS (LaunchDarkly, Unleash, Split) tích hợp trực tiếp vào CI/CD: mỗi pull request tạo tính năng mới bắt buộc phải đi kèm một flag mới trong flag registry, và một bước trong pipeline release kiểm tra flag đã tồn tại quá X ngày mà chưa dọn (stale flag) sẽ tự động tạo cảnh báo hoặc block merge tiếp cho tới khi dọn dẹp.

## Trade-offs

Feature flag thêm một nhánh rẽ (`if flagEnabled`) vào mọi nơi tính năng được bọc, làm tăng cyclomatic complexity của codebase — càng nhiều flag đang mở song song, càng nhiều tổ hợp trạng thái cần test (2 flag độc lập = 4 tổ hợp, 5 flag = 32 tổ hợp), và test coverage thực tế gần như không bao giờ theo kịp số tổ hợp lý thuyết này. Flag đưa logic quyết định hành vi ra khỏi code và vào một hệ thống cấu hình runtime bên ngoài, nghĩa là hành vi ứng dụng ở một thời điểm không còn suy ra được chỉ từ đọc source code — phải tra thêm trạng thái flag hiện tại, gây khó khăn khi debug sự cố ("tại sao user A thấy hành vi khác user B dù cùng version code") nếu không có công cụ quan sát trạng thái flag tốt. Phụ thuộc vào flag service bên ngoài (hoặc SaaS) đưa vào một điểm phụ thuộc mới: nếu flag service down và SDK không có fallback hợp lý, ứng dụng có thể đánh giá sai flag hàng loạt đúng lúc cần ổn định nhất. Chi phí lớn nhất và dễ bị bỏ qua nhất là nợ kỹ thuật: mỗi flag được tạo ra là một cam kết ngầm phải quay lại dọn dẹp sau khi tính năng ổn định — nếu không ai chịu trách nhiệm dọn, flag cũ tích tụ, code chứa hàng chục nhánh `if` cho những tính năng đã bật vĩnh viễn từ lâu, và không ai còn dám xóa vì không chắc flag nào thực sự an toàn để bỏ.

## Best Practices

- Đặt chủ sở hữu (owner) và ngày hết hạn dự kiến cho mỗi flag ngay khi tạo, và đưa việc dọn flag vào Definition of Done của ticket, không phải việc "làm sau khi rảnh".
- Phân loại flag rõ ràng (release/experiment/ops) và xử lý vòng đời khác nhau: release flag nên bị xóa trong vài tuần sau khi rollout 100%, ops flag (kill switch) có thể sống vĩnh viễn vì bản chất là công cụ vận hành.
- Luôn có giá trị fallback an toàn hard-code trong code (không chỉ dựa vào flag service phản hồi đúng), để flag service down không kéo sập ứng dụng.
- Dùng percentage rollout dựa trên hash cố định của user ID (không random mỗi request) để đảm bảo trải nghiệm nhất quán cho cùng một user qua nhiều lần truy cập.
- Thiết lập dashboard hoặc báo cáo định kỳ liệt kê flag đã bật 100% quá N ngày (stale flag) để buộc team review và dọn dẹp theo chu kỳ, thay vì để tích tụ vô thời hạn.

## Common Mistakes

- Tạo flag rồi không bao giờ xóa sau khi tính năng ổn định, khiến codebase tích tụ hàng chục nhánh `if` chết mà không ai còn hiểu ngữ cảnh ban đầu, tạo ra chi phí bảo trì âm thầm kéo dài nhiều năm.
- Lồng nhiều flag chồng lên nhau trong cùng một luồng logic mà không kiểm soát, dẫn tới số tổ hợp trạng thái tăng theo cấp số nhân và không ai (kể cả người viết) còn nhớ tổ hợp nào đã được test.
- Dùng random() mỗi lần đánh giá thay vì hash cố định theo user ID cho percentage rollout, khiến cùng một user thấy hành vi khác nhau giữa các lần request, gây trải nghiệm không nhất quán và khó debug.
- Đặt logic nghiệp vụ phức tạp trực tiếp trong điều kiện flag (thay vì tách hàm riêng), khiến code vừa khó đọc vừa khó test độc lập từng nhánh.
- Không đo lường hay log việc flag được đánh giá thế nào trong production, nên khi có sự cố liên quan tới hành vi khác nhau giữa user, không có dữ liệu để xác nhận flag có phải nguyên nhân hay không.

## Interview Questions

**Hỏi**: Feature flag khác gì với việc dùng nhiều branch và merge khi tính năng hoàn thiện?
**Trả lời**: Branch tách code ở tầng version control, nghĩa là code chưa merge thì chưa tồn tại trên production, và merge là hành động không thể chọn lọc runtime — một khi merge, mọi user đều thấy thay đổi. Feature flag tách hành vi ở tầng runtime trong khi code đã nằm trên production từ trước, cho phép bật/tắt tức thời, rollout dần theo phần trăm, hoặc target riêng một nhóm user mà không cần deploy lại hay merge lại — đây là điểm khác biệt cốt lõi giữa "deploy" và "release".

**Hỏi**: Tại sao percentage rollout cần dùng hash cố định của user ID thay vì random mỗi lần?
**Trả lời**: Nếu dùng random() mỗi lần đánh giá, cùng một user có thể rơi vào nhóm "bật" ở lần request này và "tắt" ở lần request khác, gây trải nghiệm không nhất quán (UI nhấp nháy giữa hai phiên bản) và khiến việc debug sự cố gần như bất khả thi vì không tái hiện được trạng thái. Hash cố định (hash(user_id) % 100 < rollout_percent) đảm bảo cùng một user luôn rơi vào cùng nhóm ở mọi lần đánh giá, miễn là rollout_percent không đổi, giữ trải nghiệm nhất quán và cho phép tăng dần phần trăm mà không đảo lộn nhóm user đã có.

**Hỏi**: Vì sao feature flag lại được coi là nguồn nợ kỹ thuật nếu không được dọn dẹp, và điều đó xảy ra như thế nào trong thực tế?
**Trả lời**: Mỗi flag thêm một nhánh rẽ runtime vào code, và giá trị của nó chỉ tồn tại trong giai đoạn tính năng chưa ổn định; một khi tính năng đã chạy 100% và ổn định lâu dài, flag trở thành code chết vẫn được đánh giá ở mọi request nhưng không còn phục vụ mục đích gì, đồng thời làm tăng số tổ hợp trạng thái cần hiểu và test. Trong thực tế, vì dọn flag không mang lại giá trị kinh doanh trực tiếp và dễ bị đẩy xuống cuối backlog, số flag tích tụ tăng dần theo thời gian, tới mức không ai còn dám xóa vì sợ ảnh hưởng tới hành vi đang chạy ổn định — đây là lý do nhiều team bắt buộc gắn expiry date và owner cho flag ngay từ lúc tạo.

## Summary

Feature flag tách hành động "deploy code lên production" khỏi "release tính năng cho user", cho phép code nằm im trên production và được kích hoạt runtime cho toàn bộ, một phần trăm, hoặc một nhóm user cụ thể mà không cần deploy lại. Cơ chế này giải quyết ba nhu cầu chính: rollout dần có kiểm soát (giảm blast radius), A/B testing (multivariate flag), và kill switch để phản ứng nhanh với sự cố ở dịch vụ phụ thuộc. Đánh giá flag diễn ra cục bộ trong ứng dụng dựa trên ruleset được cache và đồng bộ định kỳ từ flag service trung tâm, không phải network call trên mỗi request. Trade-off lớn nhất không nằm ở hiệu năng mà ở nợ kỹ thuật: mỗi flag là một nhánh rẽ tạm thời cần được dọn dẹp sau khi tính năng ổn định, và nếu không ai chịu trách nhiệm, số flag tích tụ khiến codebase ngày càng khó hiểu và khó test. Kỷ luật vòng đời flag — owner rõ ràng, ngày hết hạn, dashboard theo dõi stale flag — quan trọng không kém việc implement cơ chế bật/tắt.

## Knowledge Graph

- Circuit Breaker — cùng thuộc lớp resilience runtime, nhưng circuit breaker tự động ngắt dựa trên error rate còn kill switch (một dạng feature flag) do con người chủ động bật/tắt.
- Graceful Shutdown — cả hai đều là cơ chế kiểm soát hành vi ứng dụng production mà không cần deploy lại code mới.
- Idempotency — khi rollout dần một tính năng ghi dữ liệu (ví dụ đổi luồng thanh toán), cần đảm bảo cả hai nhánh flag (cũ và mới) đều idempotent để tránh xử lý trùng khi user chuyển qua lại giữa các lần rollout.
- CAP Theorem / Eventual Consistency — flag service phân tán (nhiều instance SDK cache cục bộ) phải chấp nhận độ trễ đồng bộ giữa các node, tức eventual consistency về trạng thái flag.
- Health Check — dashboard quản trị flag thường tích hợp cùng hệ thống giám sát sức khỏe dịch vụ để quyết định có nên tiếp tục tăng phần trăm rollout hay rollback.
- Retry & Exponential Backoff — SDK đánh giá flag cần chiến lược retry/backoff riêng khi đồng bộ ruleset từ flag service thất bại tạm thời.

## Five Things To Remember

- Feature flag tách deploy khỏi release; deploy là đưa code lên server, release là cho user thấy tính năng.
- Đánh giá flag phải là phép tính cục bộ trong bộ nhớ ứng dụng, không phải network call trên mỗi request.
- Percentage rollout dùng hash cố định của user ID, không dùng random mỗi lần đánh giá.
- Mỗi flag là nợ kỹ thuật tạm thời; phải có owner và ngày dọn dẹp ngay từ lúc tạo.
- Kill switch cần đánh giá cực rẻ và không phụ thuộc vào chính hạ tầng có thể đang gây sự cố.
