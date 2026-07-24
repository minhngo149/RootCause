---
id: mongodb-empty-filter
title: DeleteMany/UpdateMany Without a Filter
tags: ["mongodb", "safety", "production-incident"]
---

# DeleteMany/UpdateMany Without a Filter

> Status: Draft

## Problem
Một service viết `db.orders.DeleteMany(ctx, bson.M{})` với ý định "xóa các order test", nhưng biến filter được build động (ví dụ từ query params) và trong một nhánh code, filter rơi về rỗng vì điều kiện lọc không được set. Với MongoDB, `bson.M{}` là filter khớp mọi document trong collection — không có khái niệm "quên điều kiện thì không làm gì" như một số hệ thống khác. Kết quả: toàn bộ collection `orders` bị xóa hoặc bị update trong một lệnh, không có cảnh báo, không có dry-run, không có bước xác nhận.

## Pain Points
- Mất dữ liệu toàn bộ collection trong một request, thường phát hiện ra khi khách hàng báo "đơn hàng của tôi biến mất" chứ không phải qua alert.
- `UpdateMany(bson.M{}, update)` với filter rỗng ghi đè field trên toàn bộ document — ví dụ set `status: "cancelled"` cho toàn bộ đơn hàng đang active.
- Rollback gần như không thể nếu không có backup/oplog gần thời điểm sự cố, và point-in-time restore trên production tốn hàng giờ downtime.
- Chi phí điều tra rất cao vì log ứng dụng thường chỉ ghi "DeleteMany executed, matched: 1200000" mà không ghi filter thực tế đã dùng.

## Solution
Nguyên tắc cốt lõi: không bao giờ cho phép một filter rỗng hoặc "toàn tập" được truyền vào `DeleteMany`/`UpdateMany` mà không có xác nhận tường minh. Cụ thể hóa bằng cách tách API "xóa có điều kiện" khỏi "xóa toàn bộ" (yêu cầu flag riêng như `AllowFullCollection: true`), validate filter ở tầng gọi driver trước khi gửi lệnh, và luôn log filter dưới dạng string trước khi thực thi các lệnh mutate hàng loạt.

## How It Works
MongoDB driver gửi filter xuống server dưới dạng BSON document trong command `delete`/`update`; server không phân biệt "rỗng vì lỗi logic" với "rỗng vì cố ý xóa hết" — cả hai đều match mọi document qua collection scan hoặc dùng index `_id` nếu có sẵn. Với `DeleteMany`, mỗi document match sẽ bị xóa tuần tự trong các batch (mặc định driver Go gom theo `bulkWrite` nội bộ), và thao tác này ghi vào oplog dưới dạng nhiều entry xóa từng `_id` — nghĩa là oplog sẽ phình rất nhanh và có thể đẩy các secondary vào trạng thái lag nặng khi replicate hàng triệu delete op. Với `UpdateMany` dùng `$set`, mỗi document match được ghi lại (rewrite) kể cả khi giá trị field không đổi, gây write amplification và tăng WiredTiger cache pressure. Vì các lệnh này không cần index để chạy (chỉ chậm hơn nếu thiếu index), một filter rỗng vẫn "thành công" về mặt kỹ thuật — không có timeout hay lỗi nào báo hiệu rằng có gì bất thường, nó chỉ đơn giản là chạy chậm hơn vì phải quét toàn bộ collection.

## Production Architecture
Trong kiến trúc thực tế, các lệnh `DeleteMany`/`UpdateMany` hàng loạt thường nằm trong: (1) cron job dọn dữ liệu hết hạn (ví dụ xóa session hết TTL thủ công thay vì dùng TTL index), (2) API nội bộ cho admin panel để "bulk update trạng thái đơn hàng", và (3) migration script chạy một lần khi đổi schema. Cả ba trường hợp đều có điểm chung là filter được build động từ input bên ngoài (query param, form admin, config file migration) — đây chính là nơi filter rỗng lọt qua nếu thiếu validate. Một pattern an toàn phổ biến là bọc các lệnh này qua một wrapper nội bộ (`SafeDeleteMany`) bắt buộc filter phải có ít nhất một key non-wildcard, kèm middleware ghi audit log chứa filter dạng JSON trước khi gọi driver.

## Trade-offs
Thêm lớp validate filter giúp an toàn nhưng làm chậm và phức tạp hóa các thao tác migration hợp lệ thật sự cần xóa/update toàn bộ collection (ví dụ dọn bảng staging) — buộc phải có cơ chế "override có chủ đích" (flag, code review bắt buộc, hoặc chạy qua script riêng ngoài luồng API thường). Việc bắt buộc log filter trước khi chạy cũng tăng độ trễ nhỏ và tăng dung lượng log, nhưng đây là chi phí chấp nhận được so với rủi ro mất dữ liệu không thể phục hồi.

## Best Practices
- Không bao giờ hardcode `bson.M{}` làm giá trị mặc định cho biến filter; dùng giá trị khởi tạo là `nil` và validate trước khi gọi driver.
- Bọc `DeleteMany`/`UpdateMany` qua hàm nội bộ yêu cầu filter phải chứa ít nhất một field khóa (ví dụ `_id`, `tenant_id`, `status`) trước khi cho phép chạy.
- Log filter dưới dạng string (BSON → JSON) kèm `matchedCount`/`deletedCount` ngay sau khi lệnh chạy để phục vụ điều tra sự cố.
- Với các thao tác xóa hàng loạt thật sự cần thiết, dùng `CountDocuments` với cùng filter trước để xác nhận số lượng match nằm trong ngưỡng kỳ vọng, và log/alert nếu vượt ngưỡng.
- Ưu tiên soft-delete (field `deletedAt`) thay vì xóa cứng cho các collection nghiệp vụ quan trọng, giảm rủi ro không thể rollback.

## Common Mistakes
- Build filter bằng cách merge nhiều điều kiện có điều kiện (`if x != "" { filter["x"] = x }`) mà không có fallback khi tất cả điều kiện đều rỗng.
- Test unit chỉ test case "có filter", không test case "tất cả tham số đầu vào đều rỗng" — đây chính là input thực tế gây sự cố production.
- Tin tưởng `matchedCount`/`deletedCount` trả về sau khi chạy thay vì kiểm tra trước bằng `CountDocuments` cho các thao tác không thể hoàn tác.
- Chạy migration script trực tiếp trên production database credentials thay vì trên bản sao/staging trước.
- Không giới hạn quyền (RBAC ở tầng ứng dụng) cho phép user thường gọi API bulk-update/delete vốn chỉ nên dành cho admin.

## Interview Questions
**Hỏi**: Vì sao `DeleteMany(bson.M{})` lại nguy hiểm hơn `Delete` với điều kiện sai?
**Trả lời**: Vì `bson.M{}` là filter hợp lệ về cú pháp và ngữ nghĩa — nó khớp toàn bộ document trong collection, nên MongoDB không báo lỗi, chỉ đơn giản thực thi và xóa hết. Một điều kiện sai (ví dụ so sánh sai kiểu dữ liệu) thường chỉ khớp 0 document, ít gây hại hơn nhiều so với filter rỗng khớp tất cả.

**Hỏi**: Làm sao phát hiện một `UpdateMany`/`DeleteMany` với filter rỗng đã chạy trên production sau khi sự cố xảy ra?
**Trả lời**: Kiểm tra oplog (`local.oplog.rs`) quanh thời điểm nghi ngờ để xem số lượng entry `d`/`u` bất thường trên cùng namespace trong khoảng thời gian ngắn; nếu có audit logging của MongoDB Enterprise hoặc log ứng dụng ghi filter, đối chiếu trực tiếp câu lệnh đã gửi.

**Hỏi**: TTL index có giải quyết được vấn đề filter rỗng khi dọn dữ liệu hết hạn không?
**Trả lời**: Có với các trường hợp dọn theo thời gian — TTL index để MongoDB tự xóa document hết hạn dựa trên field datetime, loại bỏ hoàn toàn nhu cầu viết `DeleteMany` thủ công với filter build động, do đó loại bỏ luôn rủi ro filter rỗng cho use case đó.

## Summary
Filter rỗng trong `DeleteMany`/`UpdateMany` là filter hợp lệ khớp toàn bộ collection, không phải lỗi cú pháp, nên MongoDB thực thi mà không cảnh báo. Rủi ro lớn nhất đến từ filter được build động từ input bên ngoài mà thiếu bước validate "phải có ít nhất một điều kiện lọc thật sự". Giải pháp bền vững là bọc các lệnh mutate hàng loạt qua wrapper bắt buộc kiểm tra filter, log filter trước khi thực thi, và ưu tiên soft-delete cho dữ liệu nghiệp vụ quan trọng. Đây là loại lỗi khó test bằng unit test thông thường vì nó chỉ lộ ra khi input thực tế (không phải input giả lập) rơi vào trường hợp rỗng.

## Knowledge Graph
- TTL Index — cơ chế MongoDB tự xóa document hết hạn, loại bỏ nhu cầu DeleteMany thủ công theo thời gian.
- Soft Delete Pattern — thay thế xóa cứng bằng đánh dấu `deletedAt`, giảm rủi ro không thể rollback.
- Oplog Replication Lag — hệ quả trực tiếp khi DeleteMany hàng loạt sinh ra quá nhiều entry ghi.
- Bulk Write Amplification — write amplification khi UpdateMany rewrite toàn bộ document dù giá trị không đổi.
- Point-in-Time Restore — phương án khôi phục dữ liệu sau sự cố xóa nhầm toàn bộ collection.
- RBAC ở tầng ứng dụng — kiểm soát quyền gọi API bulk-update/delete để giảm bề mặt tấn công của lỗi này.

## Five Things To Remember
- `bson.M{}` là filter hợp lệ khớp toàn bộ collection, MongoDB không tự chặn hay cảnh báo.
- Luôn validate filter có ít nhất một điều kiện thật sự trước khi gọi DeleteMany/UpdateMany.
- Log filter dạng string trước khi thực thi lệnh mutate hàng loạt để phục vụ điều tra sau này.
- Dùng CountDocuments để xác nhận số lượng match trước khi chạy thao tác không thể hoàn tác.
- Ưu tiên soft-delete cho dữ liệu nghiệp vụ quan trọng thay vì xóa cứng.
