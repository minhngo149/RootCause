---
id: oauth2
title: OAuth2
tags: ["security", "authentication"]
---

# OAuth2

> Status: Draft

## Problem

Một ứng dụng thứ ba (vd. dashboard nội bộ, mobile app, tích hợp CI/CD) cần truy cập tài nguyên của user trên một hệ thống khác (Google Drive, GitHub API, hệ thống SSO nội bộ) mà không được cầm mật khẩu của user. Nếu buộc user nhập username/password trực tiếp vào ứng dụng thứ ba (mô hình cũ gọi là "password anti-pattern"), ứng dụng đó có toàn quyền vĩnh viễn như chính user, không thể thu hồi riêng lẻ, và mật khẩu bị lộ diện ra một hệ thống không kiểm soát được. OAuth2 giải quyết đúng bài toán: cấp quyền truy cập có giới hạn (scope), có thời hạn, có thể thu hồi, mà không bao giờ chia sẻ credential gốc.

## Pain Points

- Không có OAuth2, mỗi tích hợp bên thứ ba đòi hỏi lưu mật khẩu thật của user ở một nơi khác — một vụ rò rỉ ở ứng dụng thứ ba kéo theo chiếm đoạt tài khoản gốc.
- Không phân biệt được quyền hạn: ứng dụng chỉ cần đọc email nhưng lại có quyền như chính user, vi phạm nguyên tắc least privilege.
- Không thể thu hồi quyền của một ứng dụng cụ thể mà không đổi mật khẩu của toàn bộ tài khoản, gây gián đoạn mọi tích hợp khác.
- Nhầm lẫn giữa "đăng nhập bằng Google" (authentication — xác thực danh tính) với việc cấp quyền truy cập API (authorization) dẫn đến lỗ hổng kinh điển: dùng access token của OAuth2 để suy luận danh tính user, trong khi access token không đảm bảo tính toàn vẹn hay đối tượng nhận (audience) cho mục đích đó.

## Solution

OAuth2 là một **authorization framework** — chuẩn hóa cách một ứng dụng (client) xin quyền truy cập tài nguyên được bảo vệ (protected resource) thay mặt user, thông qua một authorization server trung gian mà không cần biết mật khẩu của user. Cốt lõi là access token — một chuỗi có thời hạn ngắn, mang phạm vi quyền (scope) cụ thể, được resource server dùng để quyết định cho phép hay từ chối request. Refresh token đi kèm để lấy access token mới mà không cần user đăng nhập lại. Với các luồng cần xác thực danh tính user (đăng nhập), OpenID Connect (OIDC) — một lớp mở rộng trên OAuth2 — bổ sung ID token chuẩn hóa để giải quyết đúng bài toán authentication.

## How It Works

Luồng phổ biến và an toàn nhất là **Authorization Code Flow**, gồm bốn vai trò: Resource Owner (user), Client (ứng dụng cần quyền), Authorization Server (vd. Auth0, Keycloak, Google OAuth), Resource Server (API chứa dữ liệu).

1. Client redirect user đến Authorization Server kèm `client_id`, `redirect_uri`, `scope`, và một `state` ngẫu nhiên (chống CSRF). Với public client (SPA, mobile app không giữ được secret), bắt buộc thêm PKCE: client sinh `code_verifier` ngẫu nhiên, hash SHA-256 ra `code_challenge` gửi kèm request này.
2. User đăng nhập vào Authorization Server (không phải vào Client) và đồng ý cấp scope được yêu cầu. Authorization Server redirect về `redirect_uri` kèm một `authorization code` — mã dùng một lần, sống rất ngắn (thường dưới 60 giây), và `state` để client verify khớp với giá trị đã gửi.
3. Client (ở backend, không phải trình duyệt) đổi `authorization code` lấy token bằng cách gọi trực tiếp token endpoint của Authorization Server, gửi kèm `client_secret` (confidential client) hoặc `code_verifier` (PKCE, public client) để chứng minh chính client đã khởi tạo request ban đầu — bước này ngăn kẻ tấn công chặn được code ở bước redirect (qua log, browser history, referrer header) rồi tự đổi lấy token.
4. Authorization Server trả về `access_token` (và `refresh_token` nếu scope `offline_access` được cấp, cùng `id_token` nếu là luồng OIDC). Client dùng `access_token` gắn vào header `Authorization: Bearer <token>` khi gọi Resource Server.
5. Resource Server verify access_token — nếu là JWT thì kiểm tra chữ ký, `exp`, `aud`, `scope` cục bộ không cần gọi lại Authorization Server; nếu là opaque token thì phải gọi introspection endpoint. Khi access token hết hạn (thường 15 phút – 1 giờ), Client dùng `refresh_token` gọi lại token endpoint để lấy access token mới mà không cần user tương tác lại — đây là lý do refresh token phải sống lâu hơn nhiều và được bảo vệ nghiêm ngặt hơn access token (lưu ở nơi không truy cập được từ JavaScript, ví dụ HttpOnly cookie hoặc secure storage native).

Điểm phân biệt OAuth2 và OpenID Connect: OAuth2 chỉ định nghĩa access token để **authorization** (client được phép làm gì trên resource server), không có cấu trúc chuẩn cho danh tính user và không đảm bảo access token dành riêng cho client nào (không có `aud` bắt buộc theo spec gốc). OIDC thêm một token thứ ba — **ID token**, luôn là JWT có chữ ký, chứa claim chuẩn hóa (`sub`, `aud`, `iss`, `exp`, `iat`) để xác nhận **authentication** (user này đúng là ai, đăng nhập lúc nào, dành cho client nào). Hệ quả thực dụng: không bao giờ dùng access_token để xác định danh tính user trong hệ thống của bạn — dùng id_token cho việc đó, vì id_token được ký và có `aud` ràng buộc rõ với client_id, còn access_token về bản chất chỉ là "vé vào cửa API", không phải chứng minh thư.

## Production Architecture

Trong một hệ thống microservices với API Gateway đứng trước, Gateway xác thực `access_token` (JWT) bằng public key lấy từ JWKS endpoint của Authorization Server (vd. `https://auth.company.com/.well-known/jwks.json`), verify chữ ký và `exp` ngay tại Gateway mà không cần gọi network đến Authorization Server cho mỗi request — giảm latency và tránh Authorization Server thành single point of failure khi traffic lớn. Service nội bộ nhận request kèm claim `scope`/`permissions` đã được Gateway forward, tự quyết định authorization ở tầng nghiệp vụ (vd. scope `orders:read` chỉ cho phép GET, không cho phép DELETE). Với mobile app, refresh token được lưu trong secure storage của hệ điều hành (Keychain/Keystore), và một pattern phổ biến là **refresh token rotation**: mỗi lần dùng refresh token, Authorization Server phát hành refresh token mới và vô hiệu hóa cái cũ ngay lập tức — nếu refresh token cũ bị dùng lại (dấu hiệu bị đánh cắp), toàn bộ chuỗi token liên quan bị thu hồi tức thì.

## Trade-offs

Authorization Code Flow an toàn nhất nhưng phức tạp nhất để triển khai đúng — nhiều lỗ hổng thực tế (như Uber, GitLab từng gặp) đến từ việc thiếu `state` validation hoặc thiếu PKCE cho public client. JWT access token cho phép Resource Server verify offline (nhanh, không phụ thuộc mạng) nhưng đánh đổi khả năng thu hồi tức thì — token đã phát hành vẫn hợp lệ đến khi hết hạn dù user đã bị khóa tài khoản, trừ khi có thêm cơ chế blacklist/short TTL. Refresh token sống lâu là nguy cơ bảo mật lớn nếu bị đánh cắp (XSS đánh cắp token lưu trong localStorage là lỗi kinh điển), nhưng nếu TTL quá ngắn thì user phải đăng nhập lại liên tục, ảnh hưởng trải nghiệm. Scope càng chi tiết (fine-grained) càng đúng nguyên tắc least privilege nhưng càng phức tạp để thiết kế và quản lý ở cả authorization server lẫn resource server.

## Best Practices

- Luôn dùng Authorization Code Flow + PKCE cho mọi loại client, kể cả confidential client — PKCE không chỉ dành riêng cho public client, nó là lớp phòng thủ bổ sung không tốn kém.
- Không bao giờ lưu access_token hay refresh_token trong `localStorage`/`sessionStorage` ở web app — dùng HttpOnly, Secure, SameSite cookie để chống XSS đánh cắp token.
- Verify đầy đủ `aud`, `iss`, `exp`, và chữ ký của token ở Resource Server, không chỉ kiểm tra token "còn hạn" hay tồn tại.
- Bật refresh token rotation và revoke toàn bộ chuỗi token khi phát hiện refresh token cũ bị tái sử dụng.
- Cấp scope tối thiểu cần thiết cho từng client, không dùng một scope "admin" chung cho mọi tích hợp.

## Common Mistakes

- Dùng access_token (đặc biệt là opaque token) để lấy thông tin danh tính user thay vì dùng id_token của OIDC, dẫn đến sai lệch giữa "ai được cấp token" và "resource server nào token này dành cho".
- Bỏ qua `state` parameter trong Authorization Code Flow, mở đường cho CSRF trong luồng OAuth.
- Cấu hình `redirect_uri` lỏng lẻo (cho phép wildcard hoặc match không chính xác), cho phép kẻ tấn công đánh cắp authorization code qua redirect đến domain kiểm soát bởi họ.
- Set thời hạn access token quá dài (vd. vài ngày) vì ngại việc refresh, làm mất lợi ích "thời hạn ngắn, thu hồi nhanh" cốt lõi của mô hình.
- Nhầm OAuth2 là một giao thức xác thực hoàn chỉnh, tự chế thêm claim danh tính vào access token thay vì dùng OIDC chuẩn, dẫn đến hệ thống tự triển khai không tương thích và thiếu các bảo đảm bảo mật đã được chuẩn hóa.

## Interview Questions

**Hỏi**: Vì sao Authorization Code Flow cần bước đổi code lấy token ở backend thay vì trả token trực tiếp ngay ở bước redirect?

**Trả lời**: Vì bước redirect đi qua trình duyệt (URL, browser history, log của proxy/server trung gian, HTTP Referrer header), token nằm trực tiếp trong URL sẽ dễ bị lộ. Authorization code có thời hạn cực ngắn và dùng một lần; việc đổi code lấy token đòi hỏi thêm `client_secret` hoặc `code_verifier` (PKCE) — thứ chỉ client hợp lệ mới có — nên dù code bị lộ qua trình duyệt, kẻ tấn công vẫn không tự đổi được ra token.

**Hỏi**: Access token và refresh token khác nhau ở vai trò và cách xử lý như thế nào?

**Trả lời**: Access token dùng để gọi Resource Server, thời hạn ngắn (phút đến giờ), gửi kèm mọi request qua header Authorization. Refresh token không bao giờ gửi đến Resource Server, chỉ dùng để gọi Authorization Server xin access token mới, thời hạn dài hơn nhiều (ngày đến tháng) và cần được bảo vệ nghiêm ngặt hơn vì nếu lộ, kẻ tấn công có thể tự cấp access token mới liên tục.

**Hỏi**: OAuth2 và OpenID Connect khác nhau ở điểm nào, và vì sao không nên dùng access token của OAuth2 thuần để xác thực user?

**Trả lời**: OAuth2 giải quyết authorization — client được phép làm gì trên resource server — và access token của nó không có cấu trúc chuẩn hóa cho danh tính, không bắt buộc `aud` ràng buộc client cụ thể. OIDC là lớp mở rộng thêm id_token — luôn là JWT ký số với claim chuẩn (`sub`, `aud`, `iss`) dành riêng cho việc xác nhận danh tính (authentication). Dùng access token để suy ra "user này là ai" là sai vì access token có thể được cấp cho nhiều mục đích/audience khác nhau và không đảm bảo tính toàn vẹn cho việc nhận diện.

## Summary

OAuth2 là framework ủy quyền cho phép một client truy cập tài nguyên thay mặt user mà không cần biết mật khẩu, thông qua access token có thời hạn ngắn và scope giới hạn, cùng refresh token để duy trì phiên mà không cần đăng nhập lại. Authorization Code Flow (kèm PKCE) là luồng chuẩn và an toàn nhất, tách bạch bước xin quyền (qua trình duyệt) khỏi bước đổi lấy token (qua backend). OAuth2 tự thân chỉ giải quyết authorization, không phải authentication — OpenID Connect bổ sung id_token chuẩn hóa để giải quyết đúng bài toán xác thực danh tính. Trong production, refresh token rotation, verify đầy đủ claim của token, và scope tối thiểu là những yếu tố quyết định giữa một hệ thống OAuth2 an toàn và một hệ thống có lỗ hổng chờ bị khai thác. Nhầm lẫn giữa access token và id_token là lỗi kiến trúc phổ biến nhất khi engineer tự triển khai OAuth2/OIDC mà không hiểu rõ ranh giới giữa hai chuẩn.

## Knowledge Graph

- JWT — access token và id_token trong OAuth2/OIDC thường được triển khai dưới dạng JWT có chữ ký.
- API Gateway — nơi thường đặt logic verify access token tập trung cho toàn bộ microservices.
- RBAC/ABAC — scope và claim trong access token thường ánh xạ sang mô hình phân quyền nội bộ ở resource server.
- CSRF — `state` parameter trong Authorization Code Flow tồn tại để chống chính dạng tấn công này.
- Session Management — refresh token rotation là một biến thể của bài toán quản lý phiên đăng nhập dài hạn.
- SSO (Single Sign-On) — OpenID Connect là nền tảng phổ biến nhất để triển khai SSO giữa nhiều ứng dụng.

## Five Things To Remember

- OAuth2 giải quyết authorization (được phép làm gì), không phải authentication (là ai) — đó là việc của OpenID Connect.
- Access token ngắn hạn dùng để gọi API, refresh token dài hạn chỉ dùng để xin access token mới, không bao giờ gửi thẳng đến resource server.
- Luôn dùng Authorization Code Flow kèm PKCE, kể cả với confidential client.
- Xác thực danh tính user phải dựa vào id_token có chữ ký của OIDC, không bao giờ suy luận từ access token.
- Refresh token rotation và verify đầy đủ claim (`aud`, `iss`, `exp`) là ranh giới giữa triển khai OAuth2 an toàn và có lỗ hổng.
