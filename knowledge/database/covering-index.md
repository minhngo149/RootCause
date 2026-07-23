---
id: covering-index
title: Covering Index
tags: [database, index, performance]
---

# Covering Index

## Concept

Một **covering index** là index chứa đủ tất cả cột mà query cần, nên database có thể trả kết quả chỉ bằng cách đọc index — không cần quay lại đọc bảng dữ liệu gốc (thao tác gọi là "table lookup" hay "bookmark lookup" ở một số DB).

## Vì sao quan trọng

Đọc index thường nhanh hơn nhiều so với đọc cả bảng, vì index nhỏ hơn và có thể đã nằm sẵn trong cache. Nếu index không "cover" đủ cột, DB phải tốn thêm một bước đọc bảng cho mỗi dòng khớp — với bảng lớn, chi phí này cộng dồn rất nhanh.

`SELECT *` gần như luôn phá vỡ khả năng dùng covering index, vì DB phải lấy toàn bộ cột — kể cả những cột không nằm trong bất kỳ index nào — nên buộc phải quay lại đọc bảng gốc dù index có tồn tại.

## Trade-off

Covering index tốn thêm dung lượng lưu trữ và làm chậm thao tác ghi (INSERT/UPDATE phải cập nhật thêm index). Không nên tạo covering index cho mọi query — chỉ nên áp dụng cho các query đọc thường xuyên, hiệu năng quan trọng.

## Production Example

Query `SELECT id, name FROM users WHERE email = ?` có thể chạy cực nhanh nếu có index `(email) INCLUDE (id, name)` — DB trả kết quả chỉ từ index. Nhưng nếu đổi thành `SELECT * FROM users WHERE email = ?`, DB buộc phải đọc thêm bảng gốc cho từng dòng, kể cả khi chỉ có 2 cột người dùng thực sự cần.

## Interview

**Hỏi**: Tại sao `SELECT *` bị coi là anti-pattern về hiệu năng, không chỉ về style code?

**Trả lời**: Vì nó ngăn optimizer tận dụng covering index, buộc phải table lookup thêm cho mỗi dòng, và còn kéo theo chi phí network/serialize không cần thiết cho các cột ứng dụng không dùng tới.
