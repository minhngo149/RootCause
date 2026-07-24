---
id: api-security
title: API Security
tags: ["security"]
---

# API Security

> Status: Draft

## Problem

Một API nhận request từ client, tin tưởng dữ liệu client gửi lên, và chỉ kiểm tra "user đã đăng nhập chưa" mà không kiểm tra "user có được phép truy cập đúng đối tượng này không". Đây là lỗ hổng phổ biến nhất trong các hệ thống REST/GraphQL hiện đại — không phải vì thiếu authentication, mà vì thiếu authorization ở mức object và thiếu validation ở mức input. Kẻ tấn công không cần khai thác lỗ hổng phức tạp, chỉ cần đổi một số trong URL (`/api/orders/1042` thành `/api/orders/1043`) hoặc gửi một payload JSON có thêm field không mong muốn.

## Pain Points

- Rò rỉ dữ liệu người dùng khác qua endpoint hợp lệ về mặt cú pháp (đúng token, đúng format) nhưng sai đối tượng — không có exception, không có log lỗi, nên incident chỉ phát hiện khi khách hàng report hoặc bị public trên bug bounty.
- Input không validate dẫn đến injection (SQL, NoSQL, command), hoặc dữ liệu rác làm crash service downstream, hoặc mass assignment cho phép client tự set field `role: "admin"`, `isVerified: true`.
- Chi phí vận hành tăng vọt sau incident: phải audit toàn bộ log truy cập lịch sử, thông báo người dùng bị ảnh hưởng (nghĩa vụ pháp lý theo GDPR/Nghị định 13), và build lại toàn bộ tầng authorization dưới áp lực thời gian.
- API không rate-limit hoặc không giới hạn kích thước payload là vector DoS rẻ tiền — một script vài dòng có thể làm sập service mà không cần kỹ thuật tấn công phức tạp.

## Solution

OWASP API Security Top 10 (bản 2023) xếp hạng các rủi ro phổ biến nhất trong API, đứng đầu là **BOLA — Broken Object Level Authorization**: API xác thực được user nhưng không kiểm tra user đó có quyền trên đúng object được yêu cầu hay không. Giải pháp cốt lõi gồm hai lớp: (1) **input validation** nghiêm ngặt ở boundary của hệ thống — không tin bất kỳ dữ liệu nào từ client, validate type/format/range/whitelist trước khi xử lý; (2) **object-level authorization check** ở mọi endpoint nhận resource ID — mỗi request phải verify record trả về thuộc về đúng user/tenant đang gọi, không chỉ verify token hợp lệ.

## How It Works

Cơ chế BOLA vận hành qua một lỗ hổng logic đơn giản: endpoint `GET /api/invoices/:id` chạy query `SELECT * FROM invoices WHERE id = :id` mà không thêm điều kiện `AND user_id = :current_user_id` (hoặc `tenant_id` trong hệ thống multi-tenant). Middleware authentication xác nhận JWT hợp lệ, request đi qua, nhưng tầng authorization — vốn phải kiểm tra "current_user có sở hữu invoice này không" — bị bỏ sót vì developer mặc định "đã có token nghĩa là hợp lệ". Kẻ tấn công chỉ cần một tài khoản test, brute-force tăng dần ID (Insecure Direct Object Reference — IDOR, dạng cụ thể nhất của BOLA), và đọc được dữ liệu của toàn bộ user khác.

Input validation hoạt động ở nhiều lớp: schema validation (JSON Schema, Zod, class-validator) chặn request có type sai hoặc field lạ trước khi vào business logic; whitelist field (chỉ đọc field được khai báo tường minh, không dùng `Object.assign(user, req.body)` trực tiếp) ngăn **mass assignment** — client gửi thêm `{"role": "admin"}` vào payload update profile và field đó vô tình được ORM ghi thẳng xuống DB nếu model cho phép gán hàng loạt; parameterized query/prepared statement ngăn injection bằng cách tách dữ liệu khỏi câu lệnh thực thi ở tầng driver DB, khiến input dù chứa `' OR '1'='1` cũng chỉ được hiểu là literal string chứ không phải cú pháp SQL.

## Production Architecture

Trong một hệ thống SaaS multi-tenant (vd. nền tảng quản lý nhân sự phục vụ nhiều công ty khách hàng), mọi bảng nghiệp vụ đều có cột `tenant_id`, và authorization check không được đặt rải rác trong từng controller mà tập trung ở một tầng **policy/guard** dùng chung (vd. CASL, Open Policy Agent, hoặc middleware tự viết) chạy sau authentication và trước khi controller chạm vào DB — mọi query bắt buộc đi qua repository layer tự động inject `tenant_id` từ context, không cho phép controller tự ý bỏ qua điều kiện này. Input validation được đặt ở API gateway (schema validation cho từng route) và lặp lại ở tầng service (business rule validation), theo nguyên tắc defense in depth — không tin rằng gateway đã lọc sạch mọi thứ. Rate limiting (theo user, theo IP, theo API key) và payload size limit được cấu hình ở gateway/load balancer (Nginx, Kong, AWS API Gateway) trước khi request chạm đến application server.

## Trade-offs

Thêm authorization check ở mọi query tăng độ phức tạp code và có thể tăng latency nhẹ (thêm JOIN hoặc điều kiện WHERE, thêm một lượt gọi policy engine), đặc biệt rõ trong hệ thống có phân quyền phức tạp nhiều tầng (role, resource, action). Validation nghiêm ngặt ở boundary làm chậm tốc độ phát triển tính năng mới — mỗi field mới trong payload đòi hỏi cập nhật schema, và schema quá cứng nhắc có thể chặn nhầm request hợp lệ trong giai đoạn migration API. Tập trung authorization vào một tầng chung (policy engine) giảm rủi ro sai sót rải rác nhưng tạo single point of failure về mặt thiết kế — một bug trong policy engine ảnh hưởng toàn bộ hệ thống thay vì chỉ một endpoint.

## Best Practices

- Luôn kiểm tra quyền sở hữu/quyền truy cập object ở tầng service/repository, không chỉ dựa vào việc token hợp lệ; áp dụng cho mọi endpoint nhận resource ID qua path, query string, hay body.
- Validate input bằng schema tường minh (whitelist field, kiểu dữ liệu, độ dài, format) ở boundary trước khi dữ liệu chạm business logic, từ chối request thay vì cố "sửa" dữ liệu sai.
- Không bao giờ gán hàng loạt (mass assignment) trực tiếp từ request body vào model; chỉ định rõ field nào được phép ghi cho từng loại request.
- Dùng parameterized query/ORM chuẩn cho mọi truy vấn động, không nối chuỗi SQL/NoSQL thủ công dù chỉ một lần cho "trường hợp đặc biệt".
- Rate-limit theo user/API key và giới hạn kích thước payload ở gateway, kết hợp logging chi tiết truy cập theo resource ID để phát hiện pattern brute-force ID.

## Common Mistakes

- Chỉ kiểm tra authentication (token hợp lệ) mà quên authorization (token này có quyền trên object này không) — nguồn gốc phổ biến nhất của BOLA/IDOR.
- Dùng ID tuần tự dễ đoán (`/orders/1042`) mà không có kiểm soát bù trừ, khiến việc dò quét trở nên rẻ; dùng UUID không thay thế được cho authorization check, chỉ giảm khả năng đoán được ID.
- Validate input ở frontend rồi tin tưởng backend không cần kiểm tra lại, trong khi request có thể gọi thẳng API mà không qua UI.
- Trả về lỗi quá chi tiết (stack trace, tên bảng, cấu trúc DB) trong response lỗi, vô tình cung cấp thông tin trinh sát cho kẻ tấn công.
- Áp dụng schema validation không đầy đủ — chỉ kiểm tra field bắt buộc có tồn tại mà không whitelist, để lọt field thừa xuống tầng ORM.

## Interview Questions

**Hỏi**: Phân biệt Broken Authentication và Broken Object Level Authorization (BOLA)?

**Trả lời**: Broken Authentication là lỗ hổng trong việc xác minh danh tính user (session bị đánh cắp, token yếu, thiếu MFA) — vấn đề "bạn là ai". BOLA là lỗ hổng xảy ra sau khi authentication đã thành công: hệ thống biết chính xác user là ai nhưng không kiểm tra user đó có quyền trên object cụ thể đang truy cập hay không — vấn đề "bạn được phép làm gì với thứ này". Một API có authentication hoàn hảo vẫn có thể bị BOLA nếu thiếu authorization check ở tầng object.

**Hỏi**: Vì sao dùng UUID thay vì ID tăng dần không giải quyết được BOLA?

**Trả lời**: UUID chỉ làm cho việc đoán ID khó hơn (giảm khả năng brute-force), nhưng nếu attacker có được một UUID hợp lệ (qua log, referral link, response leak ở endpoint khác) mà hệ thống vẫn không kiểm tra quyền sở hữu, request vẫn thành công. UUID che giấu triệu chứng chứ không sửa nguyên nhân gốc — nguyên nhân gốc là thiếu authorization check, phải sửa ở tầng logic chứ không phải ở tầng định dạng ID.

**Hỏi**: Mass assignment vulnerability là gì và cách phòng chống?

**Trả lời**: Mass assignment xảy ra khi framework/ORM cho phép gán toàn bộ field trong request body trực tiếp vào model (vd. `User.update(req.body)`), khiến client có thể set cả những field không nên tự sửa như `role`, `isAdmin`, `balance`. Phòng chống bằng whitelist tường minh field được phép ghi cho từng action (DTO, serializer, hoặc `permit`/`select` field cụ thể), không bao giờ pass thẳng object request vào update.

## Summary

API Security xoay quanh việc không tin bất kỳ dữ liệu nào từ client và không mặc định rằng authentication hợp lệ đồng nghĩa với authorization hợp lệ. BOLA — lỗ hổng đứng đầu OWASP API Top 10 — xảy ra khi hệ thống xác thực đúng user nhưng quên kiểm tra user đó có quyền trên đúng object đang truy cập. Input validation nghiêm ngặt ở boundary (schema, whitelist field, parameterized query) ngăn injection và mass assignment trước khi dữ liệu chạm business logic. Kiến trúc production đúng đắn tập trung authorization vào một tầng chung (policy/guard) chạy trên mọi request, thay vì rải rác kiểm tra thủ công trong từng controller. Rate limiting và giới hạn payload ở gateway là lớp phòng thủ bổ sung bắt buộc, không thay thế được cho authorization đúng đắn ở tầng object.

## Knowledge Graph

- OWASP Top 10 (Web) — API Top 10 là phiên bản chuyên biệt hóa cho kiến trúc API/microservices của cùng bộ nguyên tắc OWASP.
- JWT & Session Management — authentication đúng đắn là điều kiện cần nhưng không đủ, phải đi kèm authorization ở tầng object.
- Multi-tenancy — BOLA trong hệ thống multi-tenant thường biểu hiện dưới dạng thiếu điều kiện `tenant_id` trong query.
- SQL Injection — hệ quả trực tiếp của thiếu input validation và không dùng parameterized query.
- Rate Limiting — lớp phòng thủ bổ sung chống brute-force ID và DoS, không thay thế authorization check.
- Least Privilege — nguyên tắc thiết kế nền tảng đứng sau mọi quyết định về phân quyền object-level.

## Five Things To Remember

- Token hợp lệ chỉ chứng minh danh tính, không chứng minh quyền truy cập đúng object — hai việc khác nhau.
- BOLA/IDOR là lỗ hổng phổ biến nhất trong API vì dễ khai thác (chỉ cần đổi ID) và dễ bị bỏ sót khi review code.
- Không bao giờ gán trực tiếp request body vào model; luôn whitelist field được phép ghi.
- Validate input ở backend luôn, bất kể frontend đã kiểm tra gì, vì API có thể bị gọi trực tiếp ngoài UI.
- Đổi ID tuần tự sang UUID chỉ giảm rủi ro đoán ID, không thay thế được cho authorization check thật sự.
