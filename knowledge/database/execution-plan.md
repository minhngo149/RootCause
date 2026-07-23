---
id: execution-plan
title: Execution Plan
tags: [database, performance]
---

# Execution Plan

## Concept

Trước khi chạy một câu query, database phải quyết định **cách** lấy dữ liệu: quét toàn bộ bảng (sequential scan), dùng index nào, join theo thứ tự nào, sort ở đâu. Kế hoạch đó gọi là **execution plan**.

`EXPLAIN` cho bạn xem plan đó. `EXPLAIN ANALYZE` chạy query thật và cho biết plan dự đoán khác thực tế bao nhiêu.

## Vì sao quan trọng

Cùng một câu SQL có thể chạy nhanh hoặc chậm gấp hàng trăm lần tùy vào plan được chọn. Đọc execution plan là kỹ năng bắt buộc để biết:

- Query có đang quét toàn bảng (`Seq Scan`) khi lẽ ra nên dùng index không.
- Optimizer ước lượng số dòng (`rows estimate`) có gần đúng với thực tế không — sai lệch lớn thường là dấu hiệu thống kê (statistics) của bảng đã lỗi thời.
- Chi phí (`cost`) nằm ở bước nào trong plan.

## Trade-off

Đọc plan tĩnh (chỉ nhìn SQL) không đủ để kết luận chắc chắn — cùng một câu query có thể có plan khác nhau tùy dữ liệu, index hiện có, và cấu hình DB. Vì vậy công cụ phân tích tĩnh (như rule `SQL001`) chỉ nên đưa ra **cảnh báo có khả năng xảy ra vấn đề**, không khẳng định tuyệt đối — cần `EXPLAIN ANALYZE` thật trên dữ liệu thật để xác nhận.

## Production Example

Một query `SELECT * FROM orders WHERE customer_id = ?` chạy nhanh khi bảng có 10K dòng, nhưng khi bảng lớn lên 50 triệu dòng mà không có index trên `customer_id`, plan chuyển từ tra cứu nhanh sang `Seq Scan` toàn bảng — query từ vài ms thành vài giây.

## Interview

**Hỏi**: Sự khác biệt giữa `EXPLAIN` và `EXPLAIN ANALYZE` là gì?

**Trả lời**: `EXPLAIN` chỉ ước lượng plan dựa trên thống kê, không chạy query. `EXPLAIN ANALYZE` chạy thật và cho số liệu thực tế (thời gian, số dòng thật) — nhưng có side effect nếu query là `INSERT/UPDATE/DELETE` (nó thực sự thực thi thay đổi dữ liệu).
