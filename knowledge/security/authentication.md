---
id: authentication
title: Authentication
tags: ["security"]
---

# Authentication

> Status: Draft

## Problem

Mọi request tới API đều mang theo một câu hỏi ngầm: "ai đang gọi request này?". Nếu hệ thống không có cơ chế xác thực danh tính rõ ràng, server buộc phải tin vào bất kỳ thông tin nào client tự khai báo — user ID trong body request, header tự đặt, hay cookie không được ký. Kẻ tấn công chỉ cần sửa một trường trong request (đổi `user_id=1001` thành `user_id=1002`) là có thể mạo danh người dùng khác mà server không có cách nào phát hiện. Authentication giải quyết đúng vấn đề này: xác minh danh tính người gọi trước khi cho phép bất kỳ hành động nào, tách bạch hoàn toàn với authorization (được phép làm gì) — một hệ thống có thể biết chính xác "đây là user 1002" (authentication đúng) nhưng vẫn để user 1002 xóa dữ liệu của user 1001 nếu thiếu kiểm tra quyền, đó là lỗi authorization chứ không phải authentication. Vấn đề càng phức tạp hơn trong kiến trúc phân tán: danh tính xác thực ở một service (login service) phải được truyền và tin cậy được bởi hàng chục service khác (order service, payment service) mà không phải mỗi service tự hỏi lại người dùng mật khẩu.

## Pain Points

- Session giả mạo do thiếu ký (signing): cookie session chỉ chứa `user_id=1002` dạng plaintext, không có chữ ký hoặc mã hóa, cho phép attacker tự sửa giá trị để chiếm quyền tài khoản khác mà không cần biết mật khẩu.
- Token không hết hạn hoặc không thể thu hồi: JWT access token bị đánh cắp (qua XSS, log bị lộ) nhưng có thời hạn sống 30 ngày và hệ thống không có cơ chế revoke, khiến kẻ tấn công dùng token đó truy cập hợp lệ suốt thời gian dài dù nạn nhân đã đổi mật khẩu.
- Session server-side không scale ngang được: hệ thống lưu session trong memory của từng instance, khi load balancer định tuyến request sang instance khác (không có sticky session hoặc shared store), user bị văng ra ngoài dù vẫn đang đăng nhập, gây trải nghiệm gián đoạn và tăng ticket hỗ trợ.
- Thiếu MFA cho tài khoản có quyền cao: một admin bị lộ mật khẩu qua phishing hoặc credential stuffing (dùng lại mật khẩu từ vụ lộ dữ liệu khác), kẻ tấn công đăng nhập thẳng vào hệ thống quản trị mà không gặp bất kỳ rào cản thứ hai nào, dẫn tới truy cập trái phép toàn bộ dữ liệu khách hàng.
- Chi phí vận hành khi thiết kế sai từ đầu: chuyển từ session-based sang token-based (hoặc ngược lại) giữa chừng dự án đòi hỏi sửa toàn bộ luồng đăng nhập, middleware xác thực ở mọi service, và migration hàng triệu session đang hoạt động — tốn nhiều tuần engineering và rủi ro downtime khi launch.

## Solution

Authentication là quá trình xác minh danh tính của một chủ thể (người dùng, service, thiết bị) trước khi cấp quyền truy cập, dựa trên một hoặc nhiều yếu tố chứng minh (factor): thứ bạn biết (mật khẩu), thứ bạn có (điện thoại nhận OTP, security key), hoặc thứ bạn là (vân tay, khuôn mặt). Hai mô hình triển khai phổ biến nhất cho web/API là session-based (server lưu trạng thái đăng nhập, client chỉ giữ một định danh session) và token-based (server không lưu trạng thái, client giữ một token tự chứa đủ thông tin đã được ký số để server chỉ cần xác minh chữ ký). Multi-factor authentication (MFA) nâng cao độ an toàn bằng cách yêu cầu kết hợp từ hai factor độc lập trở lên, khiến việc chỉ đánh cắp một yếu tố (mật khẩu) không còn đủ để chiếm quyền tài khoản.

## How It Works

**Session-based authentication**: sau khi client gửi đúng thông tin đăng nhập, server tạo một session record (chứa `user_id`, thời điểm tạo, thời điểm hết hạn, metadata như IP/device) lưu ở phía server — thường trong Redis để chia sẻ được giữa nhiều instance — và sinh một session ID ngẫu nhiên (đủ entropy để không đoán được, thường 128 bit trở lên) trả về client dưới dạng cookie có cờ `HttpOnly`, `Secure`, `SameSite=Lax/Strict`. Mọi request sau đó, client tự động gửi kèm cookie này, server tra cứu session ID trong store để lấy lại thông tin user và xác nhận session còn hiệu lực. Vì trạng thái nằm ở server, việc thu hồi (logout, ban tài khoản, đổi mật khẩu) chỉ cần xóa record trong store — có hiệu lực ngay lập tức trên mọi request tiếp theo.

**Token-based authentication (JWT)**: server không lưu trạng thái đăng nhập ở đâu cả. Sau khi xác thực đúng, server tạo một JSON Web Token gồm ba phần: header (thuật toán ký, ví dụ HS256 hoặc RS256), payload (claims như `sub` là user ID, `exp` thời điểm hết hạn, `iat` thời điểm phát hành, các custom claim như `role`), và chữ ký được tính từ header+payload bằng secret key (HS256, đối xứng) hoặc private key (RS256, bất đối xứng). Client lưu token này (thường ở memory hoặc, kém an toàn hơn, ở localStorage) và gửi kèm mọi request qua header `Authorization: Bearer <token>`. Server chỉ cần verify chữ ký bằng secret/public key tương ứng để tin tưởng nội dung payload mà không cần tra cứu database hay bất kỳ store trung tâm nào — đây là điểm khác biệt cốt lõi so với session: khả năng xác thực stateless, không cần round-trip tới storage.

**Vấn đề thu hồi token và refresh token pattern**: vì JWT tự chứa đủ thông tin và server không tra cứu store nào để verify, một token đã phát hành không thể bị vô hiệu hóa trước khi nó tự hết hạn (`exp`) — đây là hệ quả trực tiếp của tính stateless. Giải pháp thực tế là dùng cặp access token (thời hạn ngắn, 5-15 phút, dùng cho mọi request) và refresh token (thời hạn dài, vài ngày đến vài tuần, chỉ dùng để lấy access token mới). Refresh token được lưu ở server (hoặc ít nhất server lưu danh sách refresh token đã revoke), nên khi cần thu hồi quyền truy cập (logout, phát hiện token bị đánh cắp), server chỉ cần revoke refresh token — access token cũ vẫn còn hiệu lực tối đa vài phút nữa rồi tự hết hạn, giới hạn đáng kể cửa sổ rủi ro so với việc dùng một token sống 30 ngày.

**MFA — cơ chế TOTP phổ biến nhất**: Time-based One-Time Password (TOTP, chuẩn RFC 6238) hoạt động dựa trên một secret key được chia sẻ giữa server và thiết bị người dùng (thường mã hóa dưới dạng QR code khi setup, quét bằng Google Authenticator/Authy). Cả hai bên độc lập tính ra cùng một mã 6 chữ số bằng công thức `HMAC(secret_key, current_unix_time / 30)`, đổi mã mỗi 30 giây. Khi đăng nhập, sau khi xác thực đúng mật khẩu (factor 1: thứ bạn biết), server yêu cầu nhập mã TOTP hiện tại (factor 2: thứ bạn có — thiết bị chứa secret key), và so khớp với mã tự tính ở server (thường chấp nhận sai lệch ±1 chu kỳ 30 giây để bù trừ đồng hồ lệch). Vì secret key không bao giờ truyền qua mạng sau bước setup ban đầu, kẻ tấn công có được mật khẩu vẫn không tính được mã TOTP nếu không có thiết bị vật lý chứa secret key đó.

## Production Architecture

Trong kiến trúc microservices, xác thực thường tách khỏi từng service nghiệp vụ và tập trung ở API Gateway hoặc một Identity Provider (IdP) riêng — Auth0, Keycloak, AWS Cognito, hoặc service tự viết theo chuẩn OAuth2/OIDC. Client đăng nhập một lần với IdP, nhận về JWT access token; API Gateway verify chữ ký token ở mọi request trước khi forward xuống service nội bộ, các service downstream tin tưởng claims trong token (đã verify ở gateway) mà không cần tự gọi lại IdP — mô hình này gọi là "verify once, trust everywhere trong trust boundary", giảm tải đáng kể so với việc mỗi service tự gọi session store. Với ứng dụng web truyền thống (server-rendered, monolith), session-based với Redis làm shared session store vẫn phổ biến vì đơn giản hơn để revoke và ít rủi ro XSS hơn (cookie `HttpOnly` không đọc được bằng JavaScript, trong khi JWT lưu localStorage thì đọc được). Trong production thực tế, một thiết lập phổ biến là kết hợp cả hai: JWT access token thời hạn ngắn cho toàn bộ giao tiếp API/microservices (stateless, nhanh), cộng với một session/refresh token thời hạn dài lưu ở server để giữ khả năng revoke tức thời khi cần (đổi mật khẩu, phát hiện xâm nhập, logout toàn bộ thiết bị). MFA thường được enforce có điều kiện qua risk-based authentication — chỉ yêu cầu nhập mã OTP khi phát hiện dấu hiệu bất thường (đăng nhập từ IP/thiết bị lạ, quốc gia khác với lịch sử), thay vì bắt mọi lần đăng nhập đều qua MFA, để cân bằng giữa bảo mật và trải nghiệm người dùng.

## Trade-offs

Session-based đơn giản để revoke ngay lập tức và an toàn hơn trước XSS (nhờ `HttpOnly`), nhưng đòi hỏi shared storage (Redis) để scale ngang, thêm một round-trip network tới store cho mỗi request cần xác thực, và khó dùng xuyên domain/mobile app (cookie gắn với domain, không tự nhiên cho native app hay third-party API). Token-based (JWT) stateless nên scale ngang dễ dàng, không cần round-trip tới store để verify, và dùng tốt cho mobile/SPA/microservices, nhưng đánh đổi bằng việc không thể revoke token trước khi hết hạn (buộc phải thiết kế thêm refresh token và thời hạn ngắn để giảm thiểu rủi ro), payload token bị lộ (dù không đọc được nội dung nếu mã hóa, nhưng JWT mặc định chỉ ký chứ không mã hóa nên ai cũng decode được payload bằng base64), và nếu lưu ở localStorage thì dễ bị đánh cắp qua XSS hơn cookie `HttpOnly`. MFA giảm mạnh rủi ro chiếm tài khoản qua lộ mật khẩu đơn thuần, nhưng thêm ma sát vào luồng đăng nhập (tăng tỷ lệ bỏ ngang ở bước đăng ký/đăng nhập nếu bắt buộc cho mọi user), và tạo thêm một điểm cần hỗ trợ vận hành thực tế: người dùng mất điện thoại chứa app authenticator cần một luồng account recovery riêng, luồng này nếu thiết kế lỏng lẻo (ví dụ chỉ cần trả lời câu hỏi bảo mật) lại trở thành chính lỗ hổng mà MFA muốn ngăn chặn.

## Best Practices

- Luôn ký (HS256/RS256) hoặc mã hóa dữ liệu định danh phía client, không bao giờ tin vào giá trị client tự khai báo trong request mà không xác minh qua chữ ký hoặc tra cứu server-side.
- Dùng access token thời hạn ngắn (5-15 phút) kết hợp refresh token thời hạn dài lưu ở server để vừa có tốc độ stateless vừa giữ khả năng revoke.
- Set cookie session với `HttpOnly`, `Secure`, `SameSite=Strict/Lax` để giảm rủi ro XSS và CSRF; không bao giờ lưu session ID hoặc JWT nhạy cảm ở localStorage nếu ứng dụng có bề mặt XSS đáng kể.
- Enforce MFA bắt buộc cho tài khoản có quyền cao (admin, quyền thanh toán) và cân nhắc risk-based MFA (chỉ hỏi khi có dấu hiệu bất thường) cho user thường để cân bằng bảo mật và trải nghiệm.
- Thiết kế luồng revoke rõ ràng ngay từ đầu (danh sách blacklist/refresh token đã thu hồi, hoặc rotate secret key) — đừng đợi tới khi có sự cố lộ token mới nhận ra hệ thống không có cách nào chặn token cũ.

## Common Mistakes

- Dùng JWT nhưng đặt thời hạn access token quá dài (vài giờ đến vài ngày) mà không có refresh token, khiến token bị đánh cắp có giá trị sử dụng gần như vô thời hạn.
- Lưu JWT ở localStorage cho ứng dụng có nguy cơ XSS mà không cân nhắc rủi ro — bất kỳ đoạn script độc hại nào chạy được trên trang cũng đọc được token và gửi đi nơi khác.
- Nhầm lẫn authentication với authorization: xác thực đúng danh tính user nhưng không kiểm tra user đó có quyền truy cập resource cụ thể hay không (thiếu kiểm tra `resource.owner_id == current_user.id`).
- Tự implement thuật toán mã hóa/hash mật khẩu (tự viết hoặc dùng MD5/SHA1 trần) thay vì dùng thuật toán chuyên dụng có salt và cost factor như bcrypt, scrypt, Argon2.
- Coi MFA là "đã bật một lần thì an toàn tuyệt đối" mà bỏ qua luồng account recovery — recovery yếu (câu hỏi bí mật, gửi lại OTP qua SMS dễ bị SIM swap) trở thành đường vòng qua MFA.

## Interview Questions

**Hỏi**: Sự khác biệt cốt lõi giữa session-based và token-based authentication là gì, và nó ảnh hưởng thế nào tới khả năng revoke?
**Trả lời**: Session-based lưu trạng thái đăng nhập ở server (thường Redis), client chỉ giữ một ID trỏ tới trạng thái đó, nên revoke chỉ cần xóa record ở server và có hiệu lực ngay lập tức. Token-based (JWT) tự chứa đủ thông tin đã ký, server verify bằng chữ ký mà không tra cứu store nào, nên nhanh và stateless hơn nhưng không thể thu hồi một token đã phát hành trước khi nó tự hết hạn — đây là lý do hệ thống JWT thực tế cần thêm refresh token thời hạn dài lưu ở server để bù lại khả năng revoke.

**Hỏi**: Tại sao JWT mặc định không được coi là "mã hóa" dữ liệu, và điều này có ý nghĩa gì khi thiết kế payload?
**Trả lời**: JWT chuẩn (JWS) chỉ ký (sign) header và payload để đảm bảo tính toàn vẹn — chứng minh dữ liệu không bị sửa và đến từ nguồn hợp lệ — chứ không mã hóa nội dung; payload chỉ encode bằng base64url nên ai cũng decode và đọc được. Vì vậy không bao giờ được đặt thông tin nhạy cảm (mật khẩu, số thẻ, dữ liệu cá nhân cần bảo mật) trực tiếp vào claims của JWT; nếu bắt buộc phải mã hóa nội dung thì cần dùng biến thể JWE (JSON Web Encryption) thay vì JWS thông thường.

**Hỏi**: MFA bảo vệ được những kiểu tấn công nào, và không bảo vệ được kiểu nào?
**Trả lời**: MFA vô hiệu hóa hiệu quả các cuộc tấn công chỉ dựa trên việc chiếm được một factor duy nhất — credential stuffing (dùng lại mật khẩu rò rỉ từ nơi khác), phishing đơn giản lấy được mật khẩu, hay brute force — vì kẻ tấn công vẫn thiếu factor thứ hai. Nó không bảo vệ được các cuộc tấn công real-time phishing (kẻ tấn công dựng trang giả, nhận cả mật khẩu lẫn mã OTP ngay lúc nạn nhân nhập, rồi replay ngay lập tức tới hệ thống thật) hoặc tấn công vào chính kênh MFA (SIM swap để chiếm OTP qua SMS), nên MFA nên được xem là một lớp phòng thủ bổ sung mạnh chứ không phải giải pháp tuyệt đối.

## Summary

Authentication là bước xác minh danh tính người gọi trước khi cấp quyền truy cập, tách biệt rõ với authorization (được phép làm gì sau khi đã xác thực). Hai mô hình chính là session-based (server lưu trạng thái, dễ revoke, cần shared store để scale) và token-based/JWT (stateless, verify nhanh bằng chữ ký, nhưng khó revoke nên cần kết hợp access token ngắn hạn và refresh token dài hạn). MFA bổ sung một lớp phòng thủ bằng cách yêu cầu kết hợp nhiều factor độc lập (biết, có, là), giảm mạnh rủi ro khi một factor (thường là mật khẩu) bị lộ, phổ biến nhất qua TOTP dựa trên secret key chia sẻ và thời gian. Trong production, lựa chọn giữa hai mô hình phụ thuộc vào kiến trúc (monolith vs microservices/mobile) và yêu cầu revoke tức thời, còn MFA nên áp dụng có phân tầng theo mức độ rủi ro (bắt buộc cho tài khoản quyền cao, risk-based cho user thường). Sai lầm phổ biến nhất không nằm ở việc chọn sai mô hình mà ở việc bỏ sót khả năng thu hồi quyền truy cập khi cần — đây mới là thứ quyết định thiệt hại thực tế khi có sự cố lộ token hay tài khoản.

## Knowledge Graph

- Idempotency — cùng thuộc nhóm cơ chế bảo vệ API khỏi hành vi lặp/giả mạo, nhưng giải quyết vấn đề khác (trùng lặp side-effect thay vì danh tính).
- Distributed Locking — cơ chế atomic check-and-set tương tự cách session store xử lý race condition khi tạo/xóa session đồng thời.
- Circuit Breaker — cùng nằm trong lớp phòng thủ hạ tầng bảo vệ hệ thống, thường đặt sau lớp authentication ở API Gateway.
- Rate Limiting — thường áp dụng cùng lớp với authentication ở API Gateway để chặn brute force vào endpoint đăng nhập.
- OAuth2/OpenID Connect — chuẩn giao thức phổ biến để triển khai authentication tập trung (Identity Provider) trong kiến trúc microservices.
- Authorization (RBAC/ABAC) — bước xử lý ngay sau authentication, quyết định danh tính đã xác thực được phép làm gì.

## Five Things To Remember

- Authentication trả lời "bạn là ai", authorization trả lời "bạn được làm gì" — không bao giờ nhầm lẫn hai khái niệm này.
- Session-based dễ revoke ngay lập tức vì trạng thái nằm ở server; token-based (JWT) nhanh và stateless nhưng khó revoke trước khi hết hạn.
- JWT mặc định chỉ ký chứ không mã hóa — không bao giờ đặt dữ liệu nhạy cảm trực tiếp vào claims.
- Access token nên sống ngắn, refresh token sống dài và lưu ở server để giữ khả năng thu hồi quyền truy cập.
- MFA vô hiệu hóa tấn công dựa trên một factor bị lộ, nhưng không miễn nhiễm với phishing real-time hay SIM swap.
