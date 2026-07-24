---
id: api-versioning
title: API Versioning
tags: ["engineering", "backend"]
---

# API Versioning

> Status: Draft

## Problem

Một team backend expose API `GET /orders/:id` cho 40 client bên ngoài (mobile app, đối tác tích hợp qua webhook, một vài internal service khác). Để thêm tính năng "gộp đơn hàng", họ đổi field `items` từ array phẳng thành object có `groups` lồng bên trong, deploy thẳng lên production vì "chỉ là thêm field". Kết quả: mobile app cũ (chưa force-update, còn 30% người dùng) parse `items` ra `null`, crash ở màn hình chi tiết đơn hàng; đối tác tích hợp qua REST bị lỗi 500 vì code của họ gọi `.items.map()` trên object không phải array. Không có versioning, mọi thay đổi API là một cuộc đặt cược rằng không client nào phụ thuộc vào shape cũ — và cược đó gần như luôn thua khi số lượng client tăng lên.

## Pain Points

- Breaking change deploy thẳng vào production làm crash hàng loạt client cũ đồng thời, không có cách rollback nhanh vì server chỉ có một phiên bản logic đang chạy.
- Đối tác tích hợp (đặc biệt B2B, hợp đồng SLA) mất niềm tin khi API đổi shape không báo trước, dẫn đến khiếu nại hợp đồng hoặc rời bỏ nền tảng.
- Không có version tường minh khiến team backend sợ refactor, tích luỹ technical debt vì "đổi field này không biết ai đang dùng".
- Chi phí vận hành tăng khi phải support đồng thời nhiều "phiên bản ngầm" của API thông qua nhánh if/else rải rác trong code thay vì một cơ chế versioning rõ ràng, làm codebase ngày càng khó bảo trì.

## Solution

API versioning là cơ chế cho phép một service phục vụ đồng thời nhiều "hợp đồng" (contract) khác nhau với client, để thay đổi backend mà không phá vỡ client đang chạy phiên bản cũ. Có hai cách tiếp cận chính: URI versioning (nhúng version vào đường dẫn, ví dụ `/v1/orders`, `/v2/orders`) và header versioning (giữ nguyên URI, version truyền qua HTTP header như `Accept: application/vnd.company.v2+json` hoặc custom header `X-API-Version: 2026-01-15`). Đi kèm với đó là khái niệm breaking change — bất kỳ thay đổi nào khiến client hiện tại parse sai hoặc gọi sai — và deprecation, quy trình rút một version cũ khỏi production một cách có kiểm soát thay vì tắt đột ngột.

## How It Works

Với URI versioning, router của framework (Express, Gin, Spring) map tiền tố path (`/v1`, `/v2`) tới các module handler khác nhau, thường tách hẳn thư mục code theo version hoặc dùng adapter pattern để version mới gọi lại logic nghiệp vụ dùng chung, chỉ khác lớp serialize response. Cơ chế này đơn giản để implement, dễ debug qua log (nhìn URL biết ngay version), dễ cache ở CDN vì URL khác nhau là cache key khác nhau, nhưng phá vỡ nguyên tắc REST rằng URI định danh resource chứ không định danh phiên bản API — cùng một order lại có hai định danh khác nhau (`/v1/orders/123` và `/v2/orders/123`).

Với header versioning (thường gọi là content negotiation, dùng chuẩn `Accept` header với media type mở rộng theo RFC 6839, hoặc custom header), server đọc header ở middleware layer trước khi route tới handler, chọn serializer tương ứng. URI resource giữ nguyên định danh (`/orders/123` luôn là cùng một order dù version nào), đúng tinh thần REST hơn, nhưng khó test bằng trình duyệt (không thể gõ URL và Enter để xem version khác), khó debug qua log truy cập thông thường (phải log riêng header), và một số proxy/CDN trung gian có thể strip custom header nếu không cấu hình đúng.

Một biến thể phổ biến ở các API lớn (Stripe là ví dụ điển hình) là date-based versioning: mỗi tài khoản được gán một "version" mặc định là ngày đăng ký (`2023-10-16`), truyền qua header `Stripe-Version`; mỗi thay đổi breaking được ghi vào changelog gắn với một ngày cụ thể, và server giữ một lớp "version transformation" áp dụng tuần tự các phép biến đổi từ version cũ nhất tới version request yêu cầu, cho phép hàng trăm version cùng tồn tại mà chỉ cần maintain logic nghiệp vụ ở một chỗ (version mới nhất), còn lại là transform layer xử lý tương thích ngược.

## Production Architecture

Trong một hệ thống production thực tế, versioning nằm ở API gateway hoặc lớp middleware ngay trước khi request chạm vào business logic — ví dụ Kong hoặc AWS API Gateway route theo path prefix `/v1`, `/v2` tới các target group khác nhau (có thể là cùng một service chạy nhiều phiên bản container, hoặc route tới microservice độc lập nếu breaking change đủ lớn để tách hẳn). Bên trong service, kiến trúc phổ biến là giữ một "core domain model" duy nhất (không version hoá) và một lớp DTO/serializer riêng cho từng API version, để logic nghiệp vụ không bị nhân bản — ví dụ GitHub REST API giữ logic Git/repository dùng chung, chỉ khác cách serialize response JSON giữa `v3` và các phiên bản sau. Observability đi kèm bắt buộc phải tag version vào metric và log (ví dụ Datadog tag `api_version:v1`) để team biết chính xác còn bao nhiêu traffic thực tế đang dùng version cũ trước khi quyết định sunset.

## Trade-offs

- URI versioning dễ thao tác, dễ cache, dễ đọc log, nhưng vi phạm nguyên tắc REST về định danh resource và dẫn đến nhân bản route khi số version tăng.
- Header versioning giữ URI sạch, đúng REST hơn, nhưng khó debug/test thủ công và phụ thuộc vào việc mọi proxy trung gian đều truyền header đúng, tăng rủi ro vận hành ở hệ thống nhiều lớp network.
- Giữ nhiều version cùng lúc (dù bằng cách nào) luôn tốn chi phí bảo trì thực sự: mỗi version cần test riêng, mỗi bug security phải patch trên tất cả version đang hỗ trợ, không có cách nào "miễn phí" để giữ tương thích ngược mãi mãi.
- Version càng ít (semver kiểu chỉ tăng major khi breaking) giúp dễ quản lý nhưng gộp nhiều thay đổi lại thành một đợt breaking lớn, khiến mỗi lần migrate với client tốn nhiều công sức hơn so với chiến lược version nhỏ, thường xuyên như date-based của Stripe.

## Best Practices

- Định nghĩa rõ ràng "breaking change" bằng văn bản trong team (xoá field, đổi kiểu dữ liệu field, đổi status code mặc định, đổi ý nghĩa field là breaking; thêm field mới optional không phải breaking) để tránh tranh cãi chủ quan mỗi lần release.
- Luôn thiết kế response có thể mở rộng thêm field mà không cần bump version (client nên bỏ qua field lạ thay vì strict-parse toàn bộ payload), giảm số lần thực sự cần version mới.
- Công bố deprecation với timeline cụ thể (ví dụ tối thiểu 6-12 tháng cho API B2B), trả về header `Deprecation` và `Sunset` (RFC 8594) trong response để client tự động phát hiện qua tooling thay vì phải đọc changelog thủ công.
- Đo lường traffic theo version trước khi sunset — không tắt version cũ chỉ vì "đã đủ lâu", mà dựa trên số liệu thực tế còn bao nhiêu client đang gọi.
- Giữ một core domain model dùng chung, chỉ version hoá lớp serialize/DTO, để tránh nhân bản logic nghiệp vụ across version dẫn đến bug fix phải sửa N lần.

## Common Mistakes

- Deploy breaking change không tăng version vì nghĩ "chỉ là sửa nhỏ", trong khi với client bên ngoài mọi thay đổi shape response đều là breaking change tiềm tàng.
- Duy trì version cũ vô thời hạn vì sợ làm phiền đối tác, khiến team phải bảo trì 5-6 version song song, mỗi lần fix bug bảo mật phải patch riêng từng bản.
- Tắt version cũ đột ngột đúng ngày hết hạn ghi trong changelog dù dữ liệu traffic cho thấy vẫn còn client đang gọi, gây outage cho khách hàng thực.
- Version hoá URI nhưng vẫn để logic nghiệp vụ phân nhánh if/else theo version rải khắp codebase thay vì tách lớp serialize, khiến core logic ngày càng khó đọc và dễ sinh bug chéo version.
- Không version API nội bộ (internal microservice) với lý do "chỉ mình team dùng", tới khi một service khác trong công ty phụ thuộc vào response shape và bị break bởi một deploy không liên quan.

## Interview Questions

**Hỏi**: URI versioning và header versioning khác nhau ở điểm nào, và khi nào nên chọn cái nào?

**Trả lời**: URI versioning nhúng version vào path (`/v2/orders`), dễ cache, dễ debug qua log, dễ test bằng tay, phù hợp với public API có nhiều client bên ngoài cần đơn giản. Header versioning giữ nguyên URI, version truyền qua header (`Accept` hoặc custom), đúng nguyên tắc REST hơn về định danh resource, nhưng khó test/debug hơn và phụ thuộc proxy trung gian truyền header đúng — phù hợp hơn với API nội bộ nơi team kiểm soát toàn bộ hạ tầng network.

**Hỏi**: Thế nào được coi là một breaking change trong API?

**Trả lời**: Bất kỳ thay đổi nào khiến client hiện tại, viết theo hợp đồng cũ, xử lý sai hoặc lỗi — ví dụ xoá field, đổi kiểu dữ liệu (string sang object), đổi status code mặc định, đổi ý nghĩa ngữ nghĩa của field dù kiểu dữ liệu không đổi. Thêm field mới optional thường không phải breaking change nếu client được thiết kế để bỏ qua field lạ.

**Hỏi**: Chiến lược deprecate an toàn cho một API version cần những gì?

**Trả lời**: Cần timeline công bố rõ ràng (thường tối thiểu vài tháng đến một năm với B2B), tín hiệu tự động cho client qua header `Deprecation`/`Sunset` thay vì chỉ ghi changelog, và đo lường traffic thực tế theo version trước khi tắt hẳn — chỉ sunset khi số liệu xác nhận traffic đã giảm về gần 0 hoặc các client còn lại đã được liên hệ trực tiếp, không tắt chỉ vì đã tới ngày hẹn trên giấy.

## Summary

API versioning tồn tại để một service có thể tiến hoá mà không phá vỡ client đang chạy phiên bản cũ, với hai kỹ thuật chính là URI versioning (đơn giản, dễ debug, nhưng vi phạm định danh REST) và header versioning (đúng REST hơn, khó vận hành hơn). Breaking change cần được định nghĩa rõ ràng bằng văn bản để tránh chủ quan, và kiến trúc production nên tách core domain model khỏi lớp serialize theo version để tránh nhân bản logic. Deprecation an toàn đòi hỏi timeline công bố trước, tín hiệu tự động qua header chuẩn, và quyết định dựa trên số liệu traffic thực tế chứ không phải lịch hẹn chủ quan. Không có version nào miễn phí để duy trì — mỗi version sống thêm một ngày là một ngày team phải test và patch bảo mật thêm một lần.

## Knowledge Graph

- Semantic Versioning (SemVer) — quy ước đánh số version (major.minor.patch) thường được mượn để quyết định khi nào bump major version của API.
- Backward Compatibility — nguyên tắc thiết kế giúp giảm số lần thực sự cần một API version mới.
- API Gateway — lớp hạ tầng (Kong, AWS API Gateway) nơi routing theo version thường được cấu hình tập trung.
- Content Negotiation (HTTP) — cơ chế chuẩn (`Accept` header, RFC 6839) làm nền tảng cho header versioning.
- Feature Flags — công cụ liên quan giúp rollout thay đổi backend dần dần thay vì gắn cứng vào version API.
- Observability & Metrics Tagging — cần thiết để đo traffic theo version, làm căn cứ quyết định deprecate.

## Five Things To Remember

- Mọi thay đổi shape response với client bên ngoài đều nên bị nghi ngờ là breaking change.
- URI versioning dễ vận hành, header versioning đúng REST hơn — chọn theo ai là người dùng API.
- Tách core domain logic khỏi lớp serialize theo version để tránh nhân bản code.
- Công bố deprecation bằng cả văn bản lẫn header tự động (`Deprecation`, `Sunset`), không chỉ changelog.
- Chỉ tắt version cũ khi số liệu traffic xác nhận, không phải khi lịch hẹn tới.
