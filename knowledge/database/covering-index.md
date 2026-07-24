---
id: covering-index
title: Covering Index
tags: ["database", "index", "performance"]
---

# Covering Index

> Status: Draft

## Problem

Một endpoint `GET /users/by-email` chỉ trả về `id` và `name`, đã có index trên `email`, nhưng vẫn chậm dần khi bảng `users` lớn lên. Đội ngũ kiểm tra `EXPLAIN`, thấy planner dùng đúng index trên `email` — vậy tại sao vẫn chậm? Vấn đề nằm ở bước sau khi tìm thấy index entry: DB vẫn phải quay lại đọc bảng gốc để lấy `id` và `name`, vì index trên `email` không tự chứa hai cột đó.

## Pain Points

- Có index đúng cột `WHERE` nhưng vẫn chậm là một nghịch lý gây khó hiểu cho engineer mới — họ tin "có index là đủ", trong khi vấn đề thực sự nằm ở bước lookup sau đó, không phải ở việc tìm index entry.
- Mỗi dòng khớp điều kiện tốn thêm một lần random I/O để quay lại heap/bảng gốc; với query trả về hàng nghìn dòng, tổng chi phí random I/O này có thể vượt xa lợi ích của việc dùng index ngay từ đầu.
- `SELECT *` là thủ phạm âm thầm phổ biến nhất: nó buộc DB lấy toàn bộ cột, kể cả những cột ứng dụng không dùng tới, nên gần như luôn phá vỡ khả năng dùng covering index dù index đã được thiết kế đúng.
- Vì buộc phải tính rows tránh planner mới lấy bước lookup phụ, chi phí này không hiện rõ ràng trong `cost` ước lượng của `EXPLAIN` nếu không chạy `ANALYZE`, khiến vấn đề dễ bị bỏ qua khi review query bằng mắt.

## Solution

Covering index là index chứa đủ mọi cột mà một câu query cần — cả cột lọc (`WHERE`) lẫn cột trả về (`SELECT`) — để database trả kết quả hoàn toàn từ chính index, không cần quay lại đọc bảng gốc. Thao tác "quay lại đọc bảng gốc" này có tên khác nhau tùy engine: table lookup, bookmark lookup, hoặc heap fetch — nhưng bản chất giống nhau: một lần random I/O phụ cho mỗi dòng khớp điều kiện. Loại bỏ hoàn toàn bước đó là lý do covering index thường nhanh hơn hẳn so với index thường trên cùng một query.

## How It Works

Một B-Tree index thông thường lưu giá trị của (các) cột được index kèm một con trỏ tới vị trí dòng đầy đủ trong bảng chính (rowid ở SQLite, ctid ở Postgres, hoặc khóa clustered index ở InnoDB). Khi query chỉ cần lọc theo cột đã index nhưng SELECT thêm cột khác, planner phải thực hiện thêm một bước: dùng con trỏ đó để nhảy sang bảng chính và đọc phần dữ liệu còn thiếu — đây chính là index lookup/bookmark lookup. Nếu index được mở rộng để tự chứa luôn các cột SELECT (ví dụ `CREATE INDEX ... (email) INCLUDE (id, name)` ở Postgres/SQL Server, hoặc thiết kế composite index đủ cột ở MySQL), toàn bộ dữ liệu cần trả về đã nằm sẵn trong index entry, nên planner chọn được "index-only scan" (Postgres) hay tương đương — bỏ hẳn bước nhảy sang bảng chính. Điều kiện tiên quyết để index-only scan hoạt động đúng còn phụ thuộc vào visibility map (Postgres): nếu trang dữ liệu chưa được đánh dấu "all visible" (do vacuum chưa chạy kịp sau nhiều update/delete), planner vẫn phải ghé qua heap để kiểm tra visibility của dòng, dù index đã chứa đủ cột.

## Production Architecture

Trong một service tra cứu tần suất cao (ví dụ endpoint xác thực chạy hàng nghìn request/giây để tra `user_id` và `role` theo `email`), covering index trên `(email) INCLUDE (user_id, role)` giúp toàn bộ request được phục vụ chỉ bằng cách đọc index — thường đã nằm gọn trong buffer cache/page cache của DB, nên độ trễ ổn định ở mức dưới mili-giây ngay cả khi bảng chính có hàng chục triệu dòng. Ngược lại, một service báo cáo (reporting) chạy `SELECT *` để export dữ liệu định kỳ sẽ không bao giờ tận dụng được covering index dù DBA có thiết kế index tốt đến đâu, vì đặc tính "lấy mọi cột" của chính câu query đã loại bỏ khả năng đó — đây là lý do các team production tách riêng OLTP path (query hẹp, cột cụ thể) khỏi reporting/export path (thường chấp nhận chậm hơn, chạy trên read replica).

## Trade-offs

Mỗi cột thêm vào covering index (`INCLUDE`) làm tăng kích thước vật lý của index trên đĩa và trong bộ nhớ cache, nghĩa là ít index hơn có thể fit vừa cache cùng lúc — đánh đổi tốc độ đọc lấy dung lượng. Mọi thao tác ghi (`INSERT`/`UPDATE`/`DELETE`) chạm tới các cột nằm trong index đều phải cập nhật thêm chính index đó, nên viết covering index cho một bảng ghi nhiều nhưng đọc ít có thể làm chậm write path nhiều hơn lợi ích đọc mang lại. Vì vậy covering index không phải là tối ưu mặc định nên áp dụng cho mọi index — chỉ hợp lý cho các query đọc lặp lại nhiều, nhạy cảm về độ trễ, đã được xác định rõ qua đo đạc thực tế (`pg_stat_statements` hoặc APM), không phải suy đoán.

## Best Practices

- Ưu tiên `SELECT` đúng cột ứng dụng cần thay vì `SELECT *`, để bản thân query có khả năng tận dụng covering index nếu có.
- Thiết kế covering index cho các query đọc tần suất cao đã đo được (qua `pg_stat_statements`/slow query log), không tạo tràn lan cho mọi query.
- Dùng `EXPLAIN (ANALYZE, BUFFERS)` để xác nhận planner thực sự chọn index-only scan, thay vì chỉ tin vào tên index đã đặt.
- Đảm bảo autovacuum chạy đủ tần suất (Postgres) để visibility map luôn cập nhật — nếu không, index-only scan có thể "rớt" về lookup thường mà không rõ nguyên nhân.
- Theo dõi chi phí ghi sau khi thêm covering index trên bảng ghi nhiều, không chỉ đo lợi ích đọc.

## Common Mistakes

- Tạo index đúng cột `WHERE` rồi cho rằng đã tối ưu xong, không để ý query còn `SELECT` thêm cột ngoài index.
- Dùng `SELECT *` trong code rồi thắc mắc vì sao index "không được dùng" dù `EXPLAIN` cho thấy index scan — thực chất index có được dùng, chỉ là kèm thêm bước lookup tốn kém.
- Thêm quá nhiều cột vào `INCLUDE` "cho chắc" mà không đo tác động lên write path và kích thước index.
- Không kiểm tra lại `EXPLAIN` sau khi đổi schema/thêm cột SELECT — covering index cũ có thể không còn "cover" đủ, âm thầm quay lại lookup thường.

## Interview Questions

**Hỏi**: Tại sao một query đã có index đúng cột `WHERE` vẫn có thể chậm?

**Trả lời**: Vì index đó không "cover" đủ các cột mà `SELECT` yêu cầu, nên planner vẫn phải quay lại đọc bảng gốc (table lookup) cho mỗi dòng khớp điều kiện — chi phí random I/O này có thể lớn hơn nhiều so với chi phí tìm kiếm trên chính index.

**Hỏi**: `SELECT *` ảnh hưởng thế nào tới khả năng dùng covering index?

**Trả lời**: `SELECT *` buộc DB lấy toàn bộ cột của bảng, nên gần như không index nào có thể "cover" đủ trừ khi index chứa mọi cột của bảng (không thực tế) — kết quả là query luôn cần table lookup, bất kể index đã thiết kế tốt thế nào.

## Summary

Covering index loại bỏ bước quay lại đọc bảng gốc bằng cách tự chứa đủ mọi cột một query cần, cả cột lọc lẫn cột trả về. Lợi ích lớn nhất thể hiện ở các query đọc tần suất cao, độ trễ thấp; chi phí đánh đổi là dung lượng lưu trữ và tốc độ ghi. `SELECT *` là kẻ thù trực tiếp của covering index vì nó xoá bỏ khả năng "cover đủ cột" ngay từ bản chất câu query.

## Knowledge Graph

- Execution Plan — covering index thay đổi lựa chọn trong execution plan từ index scan thường sang index-only scan.
- Secondary Index — covering index là một biến thể mở rộng của secondary index thông thường.
- Clustered Index — trong engine dùng clustered index (InnoDB), secondary index luôn kèm khóa clustered, ảnh hưởng cách covering index được thiết kế.
- Rule SQL001 (Avoid SELECT *) — trực tiếp tham chiếu tới bài viết này vì `SELECT *` phá vỡ khả năng dùng covering index.

## Five Things To Remember

- Có index đúng cột `WHERE` không có nghĩa là query đã tối ưu — còn phụ thuộc cột `SELECT`.
- Covering index tự chứa đủ cột cần trả về, tránh hẳn bước quay lại đọc bảng gốc.
- `SELECT *` gần như luôn phá vỡ khả năng dùng covering index.
- Đổi lại chi phí dung lượng lưu trữ và tốc độ ghi, không phải miễn phí.
- Chỉ nên áp dụng cho query đọc tần suất cao đã đo đạc, không áp dụng tràn lan.
