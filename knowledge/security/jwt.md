---
id: jwt
title: JWT
tags: ["security", "authentication"]
---

# JWT

> Status: Draft

## Problem
Hệ thống microservices cần xác thực request giữa client-service và service-service mà không muốn mỗi service phải query lại database session ở một nơi tập trung. Session cookie truyền thống buộc mọi service phải hỏi ngược về server lưu session (Redis, DB), tạo ra một điểm phụ thuộc và độ trễ mạng cho mọi request. JWT (JSON Web Token) được sinh ra để giải quyết bài toán "self-contained token" — token tự mang đủ thông tin để service xác thực mà không cần round-trip tới nơi lưu trữ.

## Pain Points
- Team mới dùng JWT thường nhét thẳng thông tin nhạy cảm (mật khẩu, số thẻ, PII) vào payload vì tưởng token được "mã hoá" — thực chất payload chỉ Base64URL-encode, ai cũng decode đọc được.
- Không có cơ chế revoke tức thời: user đổi mật khẩu, bị khoá tài khoản, hoặc admin thu hồi quyền nhưng access token cũ vẫn hợp lệ cho đến khi hết hạn.
- Access token sống quá lâu (vd 7 ngày) để giảm phiền cho user → nếu token bị đánh cắp (XSS, log leak, MITM proxy nội bộ), attacker có quyền truy cập trong suốt thời gian đó mà server không biết.
- Payload phình to (nhét role, permissions, profile) làm tăng kích thước header của mọi HTTP request, ảnh hưởng băng thông ở scale hàng triệu request/phút.

## Solution
JWT gồm 3 phần Base64URL-encode nối bằng dấu chấm: `header.payload.signature`. Header khai báo thuật toán ký (`alg`) và loại token (`typ`). Payload chứa claims — dữ liệu dạng key-value, ví dụ `sub`, `exp`, `iat`, `role`. Signature là kết quả ký (HMAC-SHA256 hoặc RSA/ECDSA) trên chuỗi `header.payload` bằng secret hoặc private key, dùng để server xác minh token không bị chỉnh sửa — không phải để giấu nội dung payload.

## How It Works
Ví dụ với thuật toán HS256: server tính `signature = HMACSHA256(base64UrlEncode(header) + "." + base64UrlEncode(payload), secret)`. Khi verify, server thực hiện lại phép tính này với secret của mình và so sánh với signature trong token — nếu payload bị sửa dù chỉ 1 bit, signature tính lại sẽ khác, request bị từ chối. Với RS256, service phát hành ký bằng private key, mọi service khác chỉ cần public key để verify — phù hợp kiến trúc phân tán vì không cần chia sẻ secret giữa các service. Payload không hề mã hoá: `atob()` trên phần giữa hai dấu chấm là đọc được ngay, đây là lý do không được lưu dữ liệu nhạy cảm — chỉ nên lưu claim tối thiểu (`sub`, `exp`, `roles` ở dạng ID) và luôn tra cứu chi tiết nhạy cảm (PII, số dư tài khoản) từ database bằng `sub` khi cần, không tin payload là nguồn sự thật cho dữ liệu cần bảo mật. `exp` (expiry) là claim bắt buộc kiểm tra ở middleware verify — token hết hạn phải bị từ chối bất kể signature hợp lệ.

## Production Architecture
Kiến trúc phổ biến: API Gateway hoặc Auth service (Keycloak, Auth0, hoặc tự viết) phát hành access token JWT ngắn hạn (5-15 phút) ký bằng RS256, kèm refresh token dài hạn lưu trong DB (có thể revoke). Các backend service (order-service, payment-service, inventory-service) chỉ cần public key của Auth service để verify JWT độc lập, không gọi ngược Auth service cho mỗi request — giảm tải và độ trễ đáng kể ở hệ thống có hàng chục microservices. Khi access token hết hạn, client dùng refresh token gọi lại Auth service để lấy access token mới; đây là điểm duy nhất Auth service có thể chặn user bị revoke, bằng cách kiểm tra refresh token trong DB (đánh dấu is_revoked). Một số hệ thống dùng thêm short-lived JWT (1-2 phút) cho các thao tác nhạy cảm (thanh toán) để giảm cửa sổ rủi ro nếu token bị lộ.

## Trade-offs
- Đổi lấy việc không cần lookup session ở mỗi request, ta mất khả năng revoke tức thời access token — chỉ có thể chờ hết hạn hoặc thêm blacklist (quay lại vấn đề stateful mà JWT muốn tránh).
- Token càng nhiều claim càng tiện cho service đọc nhanh, nhưng càng tăng kích thước request header và càng nhiều dữ liệu "đóng băng" tại thời điểm phát hành — nếu role user đổi giữa chừng, token cũ vẫn mang role cũ cho tới khi hết hạn.
- RS256 (asymmetric) an toàn hơn cho hệ phân tán vì service khác không cần giữ secret, nhưng chi phí tính toán ký/verify cao hơn HS256 đáng kể ở scale lớn.
- Access token ngắn hạn giảm rủi ro bị đánh cắp lâu dài, nhưng tăng số lần gọi refresh token, tăng tải cho Auth service và độ phức tạp xử lý concurrent refresh ở client.

## Best Practices
- Không lưu bất kỳ dữ liệu nhạy cảm/PII nào trong payload — coi payload như dữ liệu public, chỉ ký chứ không mã hoá.
- Luôn set `exp` ngắn cho access token (phút, không phải ngày) và dùng refresh token có thể revoke trong DB cho phiên dài hạn.
- Dùng RS256/ES256 thay vì HS256 khi nhiều service cần verify token độc lập, tránh phải chia sẻ secret dùng chung.
- Luôn validate `alg` trong header khi verify, không tin theo giá trị client gửi lên — chặn tấn công "alg: none" hoặc đổi từ RS256 sang HS256.
- Với thao tác nhạy cảm (đổi mật khẩu, xoá tài khoản, thanh toán), luôn re-check trạng thái user trong DB thay vì chỉ tin claims trong token.

## Common Mistakes
- Nhét mật khẩu, số thẻ, hoặc access-control chi tiết (full permission list) vào payload rồi ngạc nhiên khi bị leak qua log hoặc browser devtools.
- Dùng cùng một secret HS256 cho JWT và cho việc khác (ký session cookie, API key) — lộ secret ở một chỗ kéo sập toàn hệ thống.
- Không kiểm tra `exp` hoặc không đồng bộ đồng hồ server (clock skew) dẫn tới chấp nhận token đã hết hạn hoặc từ chối token còn hợp lệ.
- Set access token sống quá lâu (nhiều ngày) để "cho tiện UX", biến JWT bị đánh cắp thành rủi ro dài hạn tương đương session bị lộ.
- Tưởng revoke được JWT bằng cách xoá ở phía client (localStorage) — token vẫn hợp lệ ở server tới khi hết hạn, không hề bị vô hiệu hoá thực sự.

## Interview Questions
**Hỏi**: JWT có được mã hoá không? Vì sao không nên lưu dữ liệu nhạy cảm trong payload?
**Trả lời**: Không. Header và payload chỉ Base64URL-encode, ai cũng decode đọc được bằng công cụ thông thường. Chỉ signature đảm bảo token không bị chỉnh sửa, không đảm bảo tính bảo mật của nội dung, nên payload phải được coi là dữ liệu public.

**Hỏi**: Làm sao revoke một JWT trước khi nó hết hạn?
**Trả lời**: JWT về bản chất là stateless nên không có cơ chế revoke tức thời có sẵn. Giải pháp thực tế là dùng access token ngắn hạn kèm refresh token lưu trong DB (revoke được), hoặc duy trì danh sách blacklist/token version kiểm tra ở mỗi request — nhưng cách này lại đánh đổi lợi ích stateless ban đầu của JWT.

**Hỏi**: HS256 và RS256 khác nhau thế nào, khi nào chọn cái nào?
**Trả lời**: HS256 dùng chung một secret để ký và verify (symmetric), phù hợp khi chỉ một service phát hành và verify token. RS256 dùng cặp private/public key (asymmetric) — service phát hành giữ private key, các service khác chỉ cần public key để verify, phù hợp kiến trúc microservices phân tán vì không phải chia sẻ secret nhạy cảm giữa nhiều service.

## Summary
JWT là token tự chứa gồm header, payload và signature, cho phép service xác thực request mà không cần tra cứu session tập trung. Payload chỉ được encode chứ không mã hoá, nên tuyệt đối không lưu dữ liệu nhạy cảm trong đó. Đánh đổi lớn nhất của JWT là mất khả năng revoke tức thời — giải pháp thực tế là kết hợp access token ngắn hạn với refresh token có thể thu hồi ở DB. Trong kiến trúc production, JWT thường được ký RS256 để các service verify độc lập bằng public key, giảm phụ thuộc vào Auth service trung tâm.

## Knowledge Graph
- OAuth2 / OpenID Connect — JWT thường được dùng làm access token/ID token trong các chuẩn này.
- Session Cookie — mô hình stateful đối lập, cần lookup server-side, dễ revoke hơn JWT.
- Refresh Token — cơ chế bù đắp cho việc JWT access token không revoke được tức thời.
- HMAC / RSA / ECDSA — các thuật toán ký nền tảng quyết định mô hình symmetric hay asymmetric của JWT.
- API Gateway — nơi thường verify JWT tập trung trước khi route request tới microservices.
- Token Blacklist / Denylist — giải pháp bổ sung để giả lập khả năng revoke cho JWT.

## Five Things To Remember
- JWT chỉ ký, không mã hoá — payload luôn đọc được, đừng lưu dữ liệu nhạy cảm.
- Signature xác minh tính toàn vẹn, không bảo vệ tính bí mật của nội dung.
- JWT stateless nên không revoke được tức thời trước khi hết hạn.
- Dùng access token ngắn hạn kèm refresh token revoke được để bù đắp giới hạn revoke.
- RS256 phù hợp hệ phân tán vì service khác chỉ cần public key, không cần giữ secret chung.
