---
id: authorization
title: Authorization
tags: ["security"]
---

# Authorization

> Status: Draft

## Problem

Hệ thống xác thực đúng danh tính người dùng (biết chính xác đây là user nào), nhưng đó chỉ trả lời câu hỏi "bạn là ai", không trả lời câu hỏi "bạn được làm gì". Một API endpoint `GET /api/orders/{id}` kiểm tra JWT hợp lệ, giải mã ra `user_id`, rồi query thẳng `orders` theo `id` trên URL mà không đối chiếu `orders.user_id` có khớp với `user_id` trong token hay không — kết quả là user A đăng nhập hợp lệ, đổi `id` trên URL sang số của user B, và đọc được đơn hàng của người khác. Đây không phải lỗ hổng authentication (login vẫn đúng, token vẫn hợp lệ) mà là lỗ hổng authorization: hệ thống không kiểm tra quyền truy cập trên từng tài nguyên cụ thể, chỉ kiểm tra "đã đăng nhập chưa". Vấn đề càng rõ khi hệ thống có nhiều vai trò (admin, staff, customer) và nhiều tài nguyên: nếu authorization logic rải rác trong từng controller thay vì tập trung, khả năng bỏ sót một endpoint là chắc chắn xảy ra khi codebase lớn dần.

## Pain Points

- IDOR (Insecure Direct Object Reference): đổi `id` trên URL hoặc request body để đọc/sửa/xóa tài nguyên của người khác — đây là hạng mục phổ biến nhất trong OWASP Top 10 "Broken Access Control" và là lỗi thực chiến hay gặp nhất trong pentest API.
- Privilege escalation theo chiều ngang lẫn chiều dọc: user thường tự set `role=admin` trong request body khi API không validate field nào được phép client tự gán, hoặc user A truy cập được dữ liệu user B cùng cấp quyền.
- Chi phí pháp lý và uy tín khi rò rỉ dữ liệu nhạy cảm (hồ sơ y tế, thông tin tài chính) do thiếu kiểm tra quyền ở tầng resource — đây là loại sự cố dễ bị quy vào vi phạm tuân thủ (GDPR, PCI-DSS) chứ không chỉ là bug kỹ thuật.
- Authorization logic rải rác, không nhất quán: mỗi team tự viết `if user.role == 'admin'` ở từng nơi khác nhau, dẫn đến tình trạng một endpoint mới thêm vào quên check quyền, hoặc check sai điều kiện do copy-paste từ chỗ khác không đúng ngữ cảnh.
- Chi phí audit và onboarding nhân viên mới: khi không có mô hình phân quyền rõ ràng, việc trả lời câu hỏi "user này có thể làm gì" đòi hỏi đọc code thay vì tra bảng permission, làm chậm security review và incident response.

## Solution

Authorization là quá trình xác định và thực thi quyền: sau khi biết danh tính (authentication), hệ thống phải quyết định danh tính đó được phép thực hiện hành động nào trên tài nguyên nào. Hai mô hình phổ biến để tổ chức quyết định này là RBAC (Role-Based Access Control) — gán quyền theo vai trò cố định (admin, editor, viewer) — và ABAC (Attribute-Based Access Control) — quyết định quyền dựa trên thuộc tính động của user, tài nguyên, và ngữ cảnh (ví dụ: chỉ được sửa đơn hàng do chính mình tạo, chỉ trong giờ hành chính, chỉ nếu đơn chưa bị khóa). Nguyên tắc nền tảng chi phối cả hai mô hình là least privilege: mặc định từ chối tất cả, chỉ cấp đúng quyền tối thiểu cần thiết cho từng hành động cụ thể, và luôn kiểm tra quyền ở tầng server cho từng request, không tin vào bất kỳ điều kiện nào client tự khai báo.

## How It Works

**RBAC (Role-Based Access Control)**: mỗi user được gán một hoặc nhiều role (`admin`, `editor`, `viewer`), mỗi role được gán một tập permission cố định (`orders:read`, `orders:write`, `users:delete`). Khi request tới, middleware tra `user.roles`, hợp nhất permission của tất cả role đó, rồi kiểm tra permission cần thiết cho action hiện tại có nằm trong tập đó không. Về mặt dữ liệu, mô hình chuẩn dùng ba bảng: `users`, `roles`, `role_permissions`, cùng bảng trung gian `user_roles` cho quan hệ nhiều-nhiều. Việc thêm quyền mới cho một nhóm user chỉ cần sửa `role_permissions`, không cần sửa code — đây là lý do RBAC dễ audit: nhìn vào bảng là biết role nào có quyền gì. Giới hạn cố hữu của RBAC là nó tĩnh theo vai trò, không biểu diễn được điều kiện phụ thuộc dữ liệu cụ thể (ví dụ "chỉ sửa được đơn hàng của chính mình") — RBAC trả lời được "role này có quyền `orders:write` không" nhưng không trả lời được "user này có quyền sửa đơn hàng #1234 không", nên hầu hết hệ thống thực tế phải kết hợp RBAC (permission theo action) với một lớp kiểm tra ownership/ngữ cảnh riêng ở tầng resource.

**ABAC (Attribute-Based Access Control)**: quyết định quyền dựa trên một hàm đánh giá policy nhận nhiều thuộc tính làm input — thuộc tính của subject (role, department, clearance level), thuộc tính của resource (owner_id, status, sensitivity), thuộc tính của action (read/write/delete), và thuộc tính của environment (thời gian, IP, thiết bị). Một policy ABAC điển hình viết dưới dạng rule: "cho phép write nếu `subject.department == resource.department` VÀ `resource.status != 'locked'` VÀ `environment.time` trong giờ hành chính". Engine phổ biến để hiện thực ABAC là OPA (Open Policy Agent) với ngôn ngữ policy Rego, cho phép tách hoàn toàn logic authorization ra khỏi code ứng dụng — service gọi OPA qua API, gửi kèm context (subject, resource, action, environment), nhận về quyết định allow/deny, không tự viết if-else phân tán trong controller. ABAC mạnh hơn RBAC ở khả năng biểu diễn điều kiện động và ngữ cảnh, nhưng đánh đổi bằng độ phức tạp: policy khó đọc hơn bảng permission đơn giản, và việc audit "user này có quyền gì" không còn là truy vấn bảng mà phải chạy thử policy engine với nhiều tổ hợp input.

**Thực thi ở tầng resource, không chỉ tầng route**: bất kể chọn RBAC hay ABAC, điểm sống còn là kiểm tra phải diễn ra ngay trước khi truy cập dữ liệu, không chỉ ở middleware kiểm tra route. Với ví dụ IDOR ở trên, cách sửa đúng không phải chỉ thêm middleware kiểm tra "user đã login", mà controller xử lý `GET /api/orders/{id}` phải query có điều kiện `WHERE id = ? AND user_id = ?` (ownership check), hoặc sau khi load record phải so sánh `record.user_id == current_user.id` (deny nếu không khớp) trước khi trả response. Đây là lý do authorization thường được chia hai tầng: coarse-grained (route-level, kiểm tra role có quyền gọi action này không, thực hiện ở middleware) và fine-grained (resource-level, kiểm tra user cụ thể có quyền trên record cụ thể không, thực hiện trong business logic/repository layer).

## Production Architecture

Trong kiến trúc microservices, authorization thường tách thành hai lớp: API Gateway thực hiện coarse-grained check (route nào cần role gì, dựa vào claims trong JWT) để chặn sớm request không đủ quyền trước khi vào service, còn fine-grained check (ownership, điều kiện nghiệp vụ) nằm trong từng service vì chỉ service đó có đủ ngữ cảnh dữ liệu để quyết định. Các hệ thống lớn thường tách policy engine riêng (OPA, hoặc dịch vụ authorization nội bộ như Google Zanzibar-style ReBAC cho quan hệ phức tạp kiểu Google Drive "ai được share file nào") để tránh mỗi service tự implement logic phân quyền riêng rẽ, dẫn đến không nhất quán. JWT access token trong hệ thống OAuth2/OIDC thường mang theo claims như `roles` hoặc `permissions` để middleware kiểm tra nhanh mà không cần gọi ngược về auth service mỗi request, nhưng điều này kéo theo vấn đề token đã phát hành không tự cập nhật khi quyền user thay đổi — cần cơ chế revoke hoặc short-lived token để bù lại. Ở tầng database, nhiều hệ thống multi-tenant còn dùng Row-Level Security (RLS, có sẵn ở PostgreSQL) như lớp phòng thủ cuối cùng: dù application code có bug bỏ sót điều kiện `WHERE tenant_id = ?`, database vẫn tự động lọc theo policy RLS gắn với session, ngăn rò rỉ dữ liệu chéo tenant kể cả khi tầng ứng dụng sai sót.

## Trade-offs

RBAC đơn giản, dễ audit, dễ implement (một bảng permission là đủ cho phần lớn ứng dụng CRUD), nhưng scale kém khi số lượng điều kiện đặc thù tăng — hệ thống dễ rơi vào tình trạng "role explosion", tạo hàng chục role gần giống nhau chỉ khác một điều kiện nhỏ (`editor_department_a`, `editor_department_b`) vì RBAC không biểu diễn được điều kiện động. ABAC linh hoạt hơn nhiều, giải quyết triệt để vấn đề đó, nhưng đổi lại độ phức tạp vận hành: viết policy sai một điều kiện có thể vô tình cấp quyền cho nhiều đối tượng hơn dự định, và việc test coverage cho mọi tổ hợp thuộc tính gần như không thể làm thủ công 100% — cần bộ test tự động cho policy engine. Tách policy engine riêng (OPA) giúp tập trung logic, dễ audit và thay đổi không cần deploy lại service, nhưng thêm một network call (hoặc ít nhất một dependency runtime) vào mỗi request có kiểm tra quyền, tăng latency và thêm một điểm lỗi (nếu policy engine down, cần quyết định fail-open hay fail-closed — fail-closed an toàn hơn nhưng có thể gây outage diện rộng nếu policy engine gặp sự cố). RLS ở tầng database là lớp phòng thủ mạnh nhưng không thay thế được kiểm tra ở tầng ứng dụng, vì RLS chỉ áp dụng cho truy vấn SQL trực tiếp — logic nghiệp vụ phức tạp (join nhiều điều kiện, quyết định dựa trên state machine) vẫn cần code ở tầng application.

## Best Practices

- Mặc định deny-all: mọi endpoint mới phải khai báo rõ permission/role cần thiết, không có chuyện "quên khai báo thì mặc định cho phép".
- Luôn kiểm tra ownership/quan hệ ở tầng resource (query có điều kiện `WHERE owner_id = ?` hoặc so sánh sau khi load record), không chỉ dựa vào việc user đã qua middleware xác thực route.
- Không bao giờ tin field liên quan tới quyền (`role`, `is_admin`, `user_id` sở hữu tài nguyên) do client gửi trong request body — luôn lấy từ session/token đã xác thực ở server.
- Tập trung authorization logic vào một lớp dùng chung (middleware, policy engine, hoặc service riêng) thay vì để mỗi controller tự viết điều kiện riêng — giảm rủi ro bỏ sót và giúp audit dễ hơn.
- Log mọi quyết định deny quan trọng (đặc biệt access vào dữ liệu nhạy cảm) để phục vụ điều tra sự cố và phát hiện hành vi dò quét IDOR.

## Common Mistakes

- Chỉ kiểm tra "đã đăng nhập" mà không kiểm tra "có quyền trên tài nguyên cụ thể này" — nguồn gốc trực tiếp của lỗi IDOR.
- Ẩn chức năng ở giao diện (hide nút xóa nếu không phải admin) nhưng không chặn ở API — security qua obscurity ở frontend không thay thế được kiểm tra ở backend, vì client có thể gọi thẳng API.
- Cấp quyền dựa trên field client tự gửi lên (`role` trong request body khi tạo user) mà không validate field đó chỉ được set bởi người có quyền cao hơn.
- Viết điều kiện phân quyền trùng lặp, không nhất quán ở nhiều nơi trong codebase, dẫn đến một chỗ sửa logic nhưng quên sửa chỗ khác tương tự.
- Không phân biệt coarse-grained (route-level) và fine-grained (resource-level) check, dẫn đến middleware tưởng đã đủ an toàn trong khi resource-level vẫn hở.

## Interview Questions

**Hỏi**: Authentication và Authorization khác nhau ở điểm nào, và tại sao một hệ thống có thể authentication đúng nhưng vẫn bị khai thác qua lỗi authorization?
**Trả lời**: Authentication xác định danh tính (bạn là ai), Authorization xác định quyền (bạn được làm gì với danh tính đó). Một hệ thống có thể xác thực đúng 100% (token hợp lệ, đúng người dùng) nhưng vẫn bị khai thác nếu không kiểm tra quyền trên từng tài nguyên cụ thể — ví dụ IDOR, nơi user hợp lệ chỉ cần đổi ID trên URL để truy cập tài nguyên không thuộc về mình.

**Hỏi**: RBAC và ABAC khác nhau như thế nào, khi nào nên chọn cái nào?
**Trả lời**: RBAC gán quyền theo vai trò cố định, đơn giản và dễ audit, phù hợp khi số lượng điều kiện phân quyền ít và ổn định (CRUD cơ bản theo role admin/editor/viewer). ABAC quyết định quyền dựa trên thuộc tính động của subject, resource, action, và ngữ cảnh, phù hợp khi điều kiện phức tạp và thay đổi theo dữ liệu thực tế (multi-tenant, quyền phụ thuộc department, thời gian, trạng thái resource) — đánh đổi bằng độ phức tạp vận hành và khó audit hơn RBAC.

**Hỏi**: Tại sao chỉ kiểm tra quyền ở middleware route-level là không đủ?
**Trả lời**: Middleware route-level chỉ trả lời được "role này có được gọi action này không" (coarse-grained), không biết được ngữ cảnh cụ thể của resource đang truy cập. Cần thêm kiểm tra fine-grained ở tầng business logic/repository — ví dụ so sánh `resource.owner_id` với `current_user.id` — ngay trước khi đọc hoặc ghi dữ liệu, nếu không hệ thống sẽ hở IDOR dù middleware đã chặn đúng route.

## Summary

Authorization là bước xác định và thực thi quyền sau khi đã biết danh tính, trả lời câu hỏi "được làm gì" thay vì "là ai". RBAC tổ chức quyền theo vai trò cố định, đơn giản và dễ audit nhưng cứng nhắc với điều kiện động; ABAC quyết định quyền dựa trên thuộc tính của subject/resource/action/environment, linh hoạt hơn nhưng phức tạp hơn để vận hành và kiểm thử. Lỗi phổ biến nhất trong thực tế là broken access control dạng IDOR: kiểm tra đã đăng nhập nhưng quên kiểm tra quyền sở hữu trên tài nguyên cụ thể, cho phép user hợp lệ truy cập dữ liệu không thuộc về mình chỉ bằng cách đổi ID. Nguyên tắc cốt lõi để tránh lỗi này là mặc định deny-all, không tin bất kỳ thông tin quyền nào client tự gửi, và luôn kiểm tra ở cả hai tầng: coarse-grained tại route và fine-grained tại resource. Trong production, authorization thường tách thành nhiều lớp phòng thủ (gateway, service, policy engine, RLS ở database) để một điểm sai sót không dẫn thẳng đến rò rỉ dữ liệu.

## Knowledge Graph

- Authentication — bước xác định danh tính diễn ra trước authorization; authorization luôn giả định danh tính đã được xác thực đúng.
- OWASP Top 10 (Broken Access Control) — hạng mục lỗ hổng bao trùm phần lớn lỗi authorization thực chiến, đặc biệt IDOR.
- JWT & OAuth2 — cơ chế phổ biến để mang claims (role, permission) dùng cho coarse-grained authorization ở middleware/gateway.
- Multi-tenancy — mô hình dữ liệu nhiều khách hàng dùng chung hệ thống, nơi authorization sai sót dễ dẫn tới rò rỉ dữ liệu chéo tenant.
- Row-Level Security (RLS) — lớp phòng thủ authorization ở tầng database, bổ sung cho kiểm tra ở tầng ứng dụng.
- Rate Limiting — cùng nhóm biện pháp bảo vệ endpoint nhưng giải quyết vấn đề khác (lạm dụng tần suất, không phải quyền truy cập).

## Five Things To Remember

- Authorization trả lời "được làm gì", khác với authentication trả lời "là ai".
- IDOR xảy ra khi hệ thống kiểm tra đăng nhập mà quên kiểm tra quyền sở hữu trên tài nguyên cụ thể.
- RBAC đơn giản và dễ audit nhưng cứng nhắc; ABAC linh hoạt nhưng phức tạp hơn để vận hành.
- Không bao giờ tin field liên quan tới quyền do client tự gửi trong request.
- Luôn kiểm tra quyền ở cả route-level (coarse-grained) lẫn resource-level (fine-grained).
