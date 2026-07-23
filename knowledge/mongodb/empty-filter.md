---
id: mongodb-empty-filter
title: DeleteMany/UpdateMany Without a Filter
tags: [mongodb, safety, production-incident]
---

# DeleteMany/UpdateMany Without a Filter

## Concept

`DeleteMany` và `UpdateMany` nhận filter document làm tham số đầu tiên (sau `context.Context`). Filter rỗng — `bson.M{}` hoặc `bson.D{}` — khớp với **toàn bộ document trong collection**, tương đương với `DELETE`/`UPDATE` không có `WHERE` trong SQL.

## Vì sao quan trọng

Đây là lỗi "đúng cú pháp nhưng sai ý định" — driver không báo lỗi, không có exception, câu lệnh chạy thành công và xóa/sửa toàn bộ collection. Vì không có gì để bắt lỗi, hậu quả thường chỉ được phát hiện sau khi đã xảy ra.

## Trade-off

Không phải lúc nào filter rỗng cũng là lỗi — có những collection tạm/staging cần xóa hoặc reset toàn bộ theo chủ đích. Vì vậy rule này cảnh báo ở mức cao nhưng không tự động chặn, và nên cho phép đánh dấu ngoại lệ tường minh khi hành vi là cố ý.

## Production Example

Một job dọn dẹp session gọi đúng `sessions.DeleteMany(ctx, bson.M{"expired_at": bson.M{"$lt": cutoff}})`. Nhưng nếu refactor nhầm làm mất biến filter và truyền `bson.M{}` thay vào, job này sẽ xóa sạch toàn bộ collection `sessions`, không chỉ các session đã hết hạn.

## Interview

**Hỏi**: Làm sao phát hiện sớm một `DeleteMany`/`UpdateMany` với filter rỗng trước khi nó chạy trên production?

**Trả lời**: Kết hợp static rule (như RootCause), code review bắt buộc cho thao tác chạm dữ liệu hàng loạt, và ở tầng ứng dụng có thể yêu cầu một cờ xác nhận tường minh (vd. `confirmFullCollection = true`) trước khi cho phép thực thi khi filter rỗng.
