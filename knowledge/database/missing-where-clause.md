---
id: missing-where-clause
title: UPDATE/DELETE Without WHERE
tags: [database, safety, production-incident]
---

# UPDATE/DELETE Without WHERE

## Concept

`UPDATE` hoặc `DELETE` không có mệnh đề `WHERE` sẽ áp dụng cho **toàn bộ dòng trong bảng**, không phải một dòng nào cụ thể. Đây là một trong những nguyên nhân phổ biến nhất gây sự cố production nghiêm trọng.

## Vì sao quan trọng

Khác với lỗi hiệu năng (chậm nhưng vẫn đúng), thiếu `WHERE` là lỗi **đúng cú pháp nhưng sai ý định** — câu lệnh chạy thành công, không báo lỗi, nhưng xóa/sửa nhầm toàn bộ dữ liệu. Vì không có exception để bắt, lỗi này thường chỉ được phát hiện sau khi đã gây hậu quả.

## Trade-off

Không phải mọi `UPDATE`/`DELETE` không `WHERE` đều là lỗi — đôi khi xóa/reset toàn bảng là chủ đích (vd. bảng cache tạm, bảng staging). Vì vậy rule này nên cảnh báo ở mức **cao nhưng không tự động chặn**, và cho phép người dùng đánh dấu ngoại lệ tường minh (vd. comment `-- intentional: full table reset`).

## Production Example

Case kinh điển: chạy nhầm `DELETE FROM users;` (thiếu `WHERE id = ?`) trong lúc thao tác thủ công trên production console — xóa sạch bảng người dùng trong vài mili giây, không có cách nào undo ngoài restore từ backup.

## Interview

**Hỏi**: Làm sao giảm rủi ro chạy nhầm `DELETE`/`UPDATE` không `WHERE` trên production?

**Trả lời**: Kết hợp nhiều lớp phòng vệ: rule tĩnh cảnh báo trước khi merge (như RootCause làm), bật chế độ "safe update" của DB client chặn UPDATE/DELETE không WHERE, review bắt buộc cho migration chạm dữ liệu, và luôn có point-in-time backup/restore sẵn sàng.
