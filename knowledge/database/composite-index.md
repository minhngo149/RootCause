---
id: composite-index
title: Composite Index
tags: ["database", "index"]
---

# Composite Index

> Status: Draft

## Problem

Một bảng `orders` có query phổ biến `SELECT * FROM orders WHERE tenant_id = ? AND status = ? ORDER BY created_at DESC`. Team tạo ba index riêng lẻ trên `tenant_id`, `status`, `created_at` vì nghĩ "mỗi cột lọc một index là đủ". Query vẫn chậm dần khi bảng lớn lên, vì optimizer chỉ chọn được index tốt nhất cho một điều kiện, các điều kiện còn lại vẫn phải filter bằng cách quét thêm.

## Pain Points

- Optimizer chỉ dùng được một trong các single-column index cho phần lọc chính, các điều kiện còn lại thành "recheck condition" hoặc filter sau khi đã lấy dòng — tốn CPU và I/O không cần thiết.
- Với bảng multi-tenant, thiếu composite index đúng thứ tự khiến query của tenant lớn quét chung index với tenant nhỏ, gây noisy neighbor: một tenant có traffic cao làm chậm toàn bộ query khác dùng chung index.
- Nhiều index đơn cột trên cùng bảng làm tăng chi phí ghi (mỗi INSERT/UPDATE phải cập nhật từng index riêng biệt) nhưng vẫn không đạt hiệu năng đọc của một composite index đúng.
- ORDER BY sau WHERE không tận dụng được index nếu composite index không đưa cột sort vào đúng vị trí, dẫn tới bước sort riêng (thường là `Sort` node tốn memory/disk trong execution plan) dù dữ liệu đã được lọc rất ít.

## Solution

Composite index (hay compound index) là một index được xây trên **nhiều cột theo một thứ tự cố định**, ví dụ `CREATE INDEX idx_orders_tenant_status_created ON orders (tenant_id, status, created_at)`. Thay vì tạo nhiều index đơn lẻ, ta gộp các cột thường xuất hiện cùng nhau trong WHERE/ORDER BY vào một cấu trúc B-Tree duy nhất, được sắp xếp lồng nhau theo đúng thứ tự khai báo.

## How It Works

B-Tree composite index lưu key là bộ giá trị `(tenant_id, status, created_at)` được sắp xếp theo thứ tự từ điển: trước hết theo `tenant_id`, trong mỗi nhóm `tenant_id` bằng nhau thì sắp theo `status`, trong mỗi nhóm `(tenant_id, status)` bằng nhau thì sắp theo `created_at`. Vì cấu trúc này là lồng nhau, optimizer chỉ có thể "nhảy thẳng" (seek) vào đúng vị trí nếu điều kiện WHERE khớp với các cột **từ trái sang phải liên tục** — đây là leftmost prefix rule.

Cụ thể: query lọc `tenant_id = ? AND status = ?` dùng được index seek trên cả hai cột, rồi phần `created_at` đã sẵn được sắp xếp trong phạm vi đó nên `ORDER BY created_at DESC` không cần bước sort riêng. Nhưng query chỉ lọc `status = ?` (bỏ qua `tenant_id`) không seek được gì cả — vì trong B-Tree, các giá trị `status` giống nhau nằm rải rác ở nhiều vị trí khác nhau tùy theo `tenant_id` đứng trước nó, buộc optimizer phải quét toàn bộ index (index scan) hoặc bỏ qua index luôn (seq scan). Tương tự, lọc `tenant_id = ? AND created_at > ?` mà bỏ qua `status` vẫn seek được trên `tenant_id`, nhưng bên trong mỗi tenant, các dòng theo `created_at` bị trộn lẫn giữa các `status` khác nhau, nên phải filter thêm — index vẫn hữu ích nhưng không tối ưu bằng khi có đủ prefix.

Postgres, MySQL (InnoDB), SQL Server đều tuân theo leftmost prefix rule vì tất cả dùng B-Tree cho index mặc định — đây không phải hành vi riêng của một DB mà là hệ quả toán học của cấu trúc dữ liệu.

## Production Architecture

Trong hệ thống multi-tenant SaaS (ví dụ nền tảng billing xử lý hàng chục nghìn tenant), pattern chuẩn là composite index bắt đầu bằng `tenant_id` cho mọi bảng lớn — không chỉ vì hiệu năng mà còn vì tận dụng partition pruning nếu bảng được partition theo tenant. Ở hệ thống event-sourcing hoặc audit log, composite index dạng `(aggregate_id, version)` hoặc `(entity_type, entity_id, created_at)` là xương sống để trả lời "lấy lịch sử của entity X theo thứ tự thời gian" mà không cần sort runtime. Ở API pagination dùng keyset pagination (`WHERE (created_at, id) < (?, ?) ORDER BY created_at DESC, id DESC LIMIT 20`), composite index trên đúng `(created_at, id)` là bắt buộc, nếu không mỗi trang sau sẽ chậm dần theo offset.

## Trade-offs

- Composite index chỉ tối ưu cho một (hoặc một vài) pattern truy vấn cụ thể theo đúng thứ tự cột — đổi thứ tự điều kiện trong query không sao (optimizer tự sắp xếp lại), nhưng đổi cột nào đứng trước trong DDL là đổi hẳn tập query được hưởng lợi.
- Composite 3-4 cột tốn dung lượng đáng kể hơn index đơn cột, và làm chậm ghi vì mỗi INSERT/UPDATE/DELETE phải maintain thêm entry trong B-Tree, đặc biệt đau với bảng ghi nhiều (write-heavy).
- Composite index rộng dễ dẫn đến index bloat trong Postgres nếu cột đầu có cardinality thấp và bị update thường xuyên (MVCC tạo nhiều dead tuple trong index).
- Thêm composite index không loại bỏ nhu cầu index đơn cột khác nếu có query độc lập lọc riêng cột đứng sau — phải cân nhắc có giữ cả hai không, hay chấp nhận query đó chạy kém tối ưu hơn để đổi lấy ít index hơn.

## Best Practices

- Đặt cột có tính chọn lọc cao và xuất hiện trong mọi query liên quan lên đầu tiên trước, thường là cột dùng cho equality filter (`tenant_id`, `user_id`), không phải cột dùng cho range filter.
- Đưa cột dùng cho `ORDER BY` vào ngay sau các cột equality filter để tránh bước sort runtime, đúng thứ tự tăng/giảm khai báo trong index (`ASC`/`DESC`) khớp với query.
- Dùng `EXPLAIN ANALYZE` để xác nhận optimizer thực sự seek trên index (`Index Cond`) chứ không chỉ filter sau khi scan (`Filter`) — hai thứ trông giống nhau trên bề mặt nhưng chi phí khác nhau rất nhiều.
- Gộp các index đơn cột trùng lặp về mặt logic thành một composite index duy nhất nếu chúng luôn được dùng cùng nhau, để giảm chi phí ghi và duy trì.
- Với PostgreSQL, cân nhắc composite index kèm `INCLUDE` (covering index) cho các cột chỉ cần đọc chứ không cần lọc/sort, để tránh table lookup mà không làm phình kích thước phần B-Tree chính.

## Common Mistakes

- Tạo nhiều index đơn cột thay vì một composite index, tin rằng optimizer sẽ "kết hợp" chúng hiệu quả như một composite — trên thực tế bitmap index scan kết hợp nhiều index đơn luôn tốn hơn một seek trực tiếp trên composite đúng.
- Đặt cột có cardinality thấp (ví dụ `status` chỉ có vài giá trị) lên đầu tiên, khiến index gần như vô dụng để seek vì mỗi giá trị match hàng loạt dòng.
- Đặt cột range filter (`created_at > ?`, `amount BETWEEN`) ở giữa composite thay vì cuối cùng, làm mất khả năng seek cho các cột đứng sau nó — trong B-Tree, một khi gặp range condition, thứ tự các cột phía sau không còn liên tục nữa.
- Tạo composite index rồi viết query filter bỏ qua cột đầu tiên (dùng OR, hoặc chỉ lọc cột thứ hai), không nhận ra leftmost prefix rule khiến index gần như không được dùng.
- Không kiểm tra lại execution plan sau khi thêm composite index, tin rằng "có index là nhanh" trong khi optimizer đôi khi vẫn chọn seq scan nếu thống kê bảng (statistics) lỗi thời hoặc selectivity ước lượng sai.

## Interview Questions

**Hỏi**: Leftmost prefix rule là gì và tại sao nó tồn tại?

**Trả lời**: Đó là quy tắc composite index chỉ seek được nếu điều kiện WHERE khớp liên tục từ cột đầu tiên của index trở đi. Nó tồn tại vì composite index là một B-Tree duy nhất, sắp xếp lồng nhau theo đúng thứ tự cột khai báo — bỏ qua cột đầu nghĩa là dữ liệu cần tìm nằm rải rác khắp cây, không thể seek trực tiếp.

**Hỏi**: Index `(a, b, c)` có giúp được gì cho query `WHERE b = ? AND c = ?` (bỏ qua `a`) không?

**Trả lời**: Không seek được theo `b` hay `c` vì cả hai đều không phải prefix của index. Một số optimizer (như Postgres) có thể vẫn dùng index này qua index-only scan hoặc bitmap scan quét toàn bộ index rồi filter, nhưng chi phí gần tương đương seq scan trên bảng lớn, không tận dụng được lợi thế thật sự của composite index.

**Hỏi**: Có nên tạo cả `(a, b)` và `(a)` cùng lúc không?

**Trả lời**: Thường không cần — index `(a, b)` đã có thể phục vụ mọi query chỉ lọc theo `a` (vì `a` là leftmost prefix), nên index `(a)` riêng trở thành dư thừa, chỉ nên giữ nếu có lý do đặc biệt như query chỉ cần covering cho riêng cột `a` với kích thước index nhỏ hơn đáng kể.

## Summary

Composite index gộp nhiều cột vào một B-Tree được sắp xếp lồng nhau theo thứ tự khai báo, thay vì rải rác trên nhiều index đơn cột. Hiệu quả của nó phụ thuộc hoàn toàn vào leftmost prefix rule: query chỉ tận dụng được seek nếu điều kiện WHERE khớp liên tục từ cột đầu tiên trở đi. Thứ tự cột nên ưu tiên equality filter có cardinality cao trước, cột dùng cho sort ngay sau, và range filter luôn đặt cuối cùng. Composite index đổi lấy hiệu năng đọc bằng chi phí ghi và dung lượng lưu trữ cao hơn, nên không nên lạm dụng cho mọi tổ hợp cột có thể có.

## Knowledge Graph

- Covering Index — composite index có thể mở rộng bằng `INCLUDE` để trở thành covering index, tránh cả seek lẫn table lookup.
- Execution Plan — công cụ để xác nhận composite index có thực sự được optimizer chọn seek hay chỉ filter sau scan.
- Cardinality — yếu tố quyết định cột nào nên đứng đầu trong composite index.
- Multi-tenancy — pattern phổ biến nhất buộc `tenant_id` phải là cột đầu tiên trong hầu hết composite index.
- Keyset Pagination — kỹ thuật phân trang phụ thuộc trực tiếp vào composite index đúng thứ tự `(sort_column, id)`.
- Index Bloat — hậu quả vận hành khi composite index rộng bị update/delete thường xuyên trên cột cardinality thấp.

## Five Things To Remember

- Composite index là một B-Tree duy nhất, sắp xếp lồng nhau theo đúng thứ tự cột khai báo.
- Leftmost prefix rule: bỏ qua cột đầu tiên trong WHERE là mất khả năng seek của toàn bộ index.
- Đặt cột equality-filter cardinality cao lên đầu, cột sort ngay sau, range filter đặt cuối cùng.
- Nhiều index đơn cột không thay thế được một composite index đúng thứ tự.
- Luôn xác nhận bằng `EXPLAIN ANALYZE` xem optimizer seek (`Index Cond`) hay chỉ filter, đừng suy đoán.
