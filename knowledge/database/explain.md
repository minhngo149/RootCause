---
id: explain
title: EXPLAIN
tags: ["database", "performance"]
---

# EXPLAIN

> Status: Draft

## Problem

Một query chạy chậm trong production, và câu hỏi đầu tiên luôn là "tại sao chậm". Nhìn vào SQL không trả lời được câu hỏi đó — cùng một câu `SELECT` có thể được optimizer thực thi theo hàng chục cách khác nhau tùy vào index hiện có, kích thước bảng, và thống kê phân bố dữ liệu. Không có `EXPLAIN`, kỹ sư chỉ có thể đoán: thử thêm index, thử viết lại query, rồi deploy và chờ xem có nhanh hơn không — một vòng lặp debug mù trên hệ thống production.

## Pain Points

- Sửa sai vấn đề: thêm index vào cột không liên quan đến chậm thực sự, tốn write overhead mà không cải thiện gì.
- Không phát hiện được `Seq Scan` trên bảng hàng chục triệu dòng cho đến khi table lớn dần và query từ vài ms thành vài giây, thường phát hiện lúc đã ảnh hưởng người dùng.
- Không thấy được sai lệch giữa số dòng optimizer ước lượng và số dòng thực tế (row estimate vs actual) — dấu hiệu thống kê bảng đã lỗi thời khiến optimizer chọn plan tồi trên toàn hệ thống.
- Không phân biệt được chi phí compile-time (plan) với chi phí runtime thật (I/O, network, lock wait) dẫn đến kết luận sai về nguyên nhân chậm.

## Solution

`EXPLAIN` là lệnh yêu cầu database engine hiển thị execution plan mà optimizer chọn cho một câu query, mà không thực thi nó. `EXPLAIN ANALYZE` thực thi query thật, rồi trả về plan kèm số liệu đo được thực tế (thời gian từng bước, số dòng thực sự xử lý), cho phép so sánh trực tiếp giữa ước lượng và thực tế.

## How It Works

Khi nhận một câu SQL, optimizer sinh ra nhiều plan khả thi (các cách chọn access method, thứ tự join, thuật toán join, có sort hay không), rồi ước lượng chi phí (cost) của từng plan dựa trên thống kê bảng (số dòng, histogram phân bố giá trị, số trang đĩa) và chọn plan có cost thấp nhất — đây gọi là cost-based optimizer. `EXPLAIN` chỉ chạy đến bước sinh plan rồi in ra, không thực thi node nào cả, nên chi phí hiển thị (`cost=0.29..8.31`) là số ước lượng, đơn vị tương đối (không phải mili-giây).

`EXPLAIN ANALYZE` chạy plan đó thật sự, đo `actual time` và `actual rows` cho từng node bằng cách chèn instrumentation vào executor, sau đó rollback nếu là câu lệnh ghi dữ liệu trong transaction (PostgreSQL) — nhưng bản thân hiệu ứng ghi vẫn xảy ra trong lúc chạy, chỉ transaction wrapper mới quyết định có commit hay không. Plan tree đọc từ trong ra ngoài, dưới lên trên: node lá (`Seq Scan`, `Index Scan`) chạy trước, kết quả được đẩy lên node cha (`Nested Loop`, `Hash Join`, `Sort`) xử lý tiếp. Cột quan trọng cần đọc theo thứ tự: loại node (access method), `cost` (ước lượng, chỉ so sánh tương đối trong cùng plan), `rows` (ước lượng số dòng trả về), và với `ANALYZE` thêm `actual time=start..end` và `actual rows` cùng `loops` (số lần node được thực thi lại, quan trọng với nested loop join). Sai lệch lớn giữa `rows` ước lượng và `actual rows` là tín hiệu rõ ràng nhất cho biết optimizer đang quyết định dựa trên thống kê sai (cần `ANALYZE` table hoặc tăng `statistics target`).

## Production Architecture

Trong một pipeline CI/CD nghiêm túc cho database, `EXPLAIN` được chạy tự động trên staging trước khi merge migration có thay đổi index hoặc query pattern, so sánh plan trước/sau để chặn regression sớm (kiểu `Seq Scan` xuất hiện thay cho `Index Scan`). Trong vận hành thực tế, khi APM (Application Performance Monitoring) báo một endpoint có p99 latency tăng đột biến, bước điều tra chuẩn là lấy raw SQL từ `pg_stat_statements` (PostgreSQL) hoặc slow query log (MySQL), rồi chạy `EXPLAIN (ANALYZE, BUFFERS)` trực tiếp trên một replica có dữ liệu tương đương production — không chạy trên production primary vì `ANALYZE` thực thi query thật, có thể gây tải hoặc side effect với câu lệnh ghi. Nhiều công cụ giám sát production (bao gồm RootCause) tự động chèn `EXPLAIN` vào luồng chẩn đoán khi phát hiện query chậm, để rút ngắn thời gian từ "phát hiện" đến "nguyên nhân gốc rễ".

## Trade-offs

- `EXPLAIN` (không `ANALYZE`) an toàn tuyệt đối vì không thực thi, nhưng cost ước lượng có thể sai xa thực tế nếu thống kê bảng lỗi thời hoặc query có tham số động (parameter sniffing).
- `EXPLAIN ANALYZE` cho số liệu thật nhưng thực thi query thật — chạy trên `INSERT/UPDATE/DELETE` sẽ thực sự ghi dữ liệu (dùng trong transaction rồi `ROLLBACK` để an toàn), và bản thân việc đo instrumentation (đặc biệt `BUFFERS`, `TIMING`) tạo overhead khiến thời gian đo được chậm hơn thời gian chạy thực tế không đo.
- Plan tốt trên staging không đảm bảo tốt trên production nếu kích thước dữ liệu, phân bố giá trị, hoặc cấu hình (`work_mem`, `shared_buffers`) khác nhau — plan phụ thuộc trạng thái tại thời điểm chạy, không phải thuộc tính cố định của câu SQL.

## Best Practices

- Luôn dùng `EXPLAIN (ANALYZE, BUFFERS, FORMAT TEXT)` khi điều tra query chậm thật sự, không chỉ `EXPLAIN` suông — cost ước lượng không phản ánh I/O thực tế.
- Không chạy `EXPLAIN ANALYZE` trực tiếp trên production primary với query ghi dữ liệu chưa kiểm chứng; luôn bọc trong transaction và `ROLLBACK`, hoặc chạy trên replica.
- So sánh `rows` ước lượng với `actual rows` ở từng node trước khi kết luận nguyên nhân — sai lệch lớn (hơn 10 lần) là dấu hiệu cần `ANALYZE` lại bảng, không phải cần thêm index.
- Đọc plan từ node sâu nhất (trong cùng) ra ngoài, chú ý `loops=N` trên các node bên trong nested loop — thời gian thực của node đó là `actual time * loops`, không phải số hiển thị đơn lẻ.
- Lưu lại plan trước và sau khi thay đổi index/query để có baseline so sánh, thay vì chỉ nhớ cảm tính "nhanh hơn".

## Common Mistakes

- Chỉ nhìn `cost` mà bỏ qua `actual time` khi đã dùng `ANALYZE`, dẫn đến tối ưu sai chỗ vì cost là ước lượng, không phải thời gian thật.
- Chạy `EXPLAIN ANALYZE` cho câu lệnh `DELETE`/`UPDATE` lớn trên production mà không bọc transaction, gây thay đổi dữ liệu ngoài ý muốn.
- Đọc plan chỉ dựa vào tên node (thấy `Index Scan` thì mặc định là tốt) mà không xem `rows` và `loops` — `Index Scan` với `loops=100000` bên trong nested loop tệ vẫn có thể là nút thắt cổ chai chính.
- So sánh cost giữa hai plan khác nhau, khác thời điểm, khác trạng thái cache (cold cache vs warm cache) rồi kết luận plan nào "tốt hơn" một cách tuyệt đối.
- Quên rằng plan trên staging với dữ liệu nhỏ/giả không đại diện cho plan thật trên production với dữ liệu lớn và phân bố lệch (data skew).

## Interview Questions

**Hỏi**: `EXPLAIN` và `EXPLAIN ANALYZE` khác nhau ở điểm nào, và khi nào không nên dùng `EXPLAIN ANALYZE`?

**Trả lời**: `EXPLAIN` chỉ sinh và hiển thị plan dựa trên ước lượng thống kê, không thực thi query. `EXPLAIN ANALYZE` thực thi query thật và trả về số liệu đo được (thời gian, số dòng thực). Không nên dùng `EXPLAIN ANALYZE` trực tiếp trên production cho câu lệnh ghi dữ liệu (`INSERT/UPDATE/DELETE`) chưa được bọc trong transaction có `ROLLBACK`, vì nó sẽ thực sự thay đổi dữ liệu.

**Hỏi**: Nếu `rows` ước lượng trong plan lệch rất xa so với `actual rows` sau khi chạy `EXPLAIN ANALYZE`, nguyên nhân thường gặp nhất là gì và cách khắc phục?

**Trả lời**: Nguyên nhân phổ biến nhất là thống kê bảng (statistics) đã lỗi thời do dữ liệu thay đổi nhiều mà chưa `ANALYZE` (hoặc `ANALYZE TABLE` ở MySQL) lại, khiến optimizer ước lượng sai và có thể chọn nhầm access method hoặc thứ tự join. Khắc phục bằng cách chạy lại `ANALYZE`, tăng `default_statistics_target` cho cột có phân bố phức tạp, hoặc kiểm tra query có tham số động gây parameter sniffing.

**Hỏi**: `loops` trong plan của `EXPLAIN ANALYZE` (PostgreSQL) có ý nghĩa gì?

**Trả lời**: `loops` là số lần một node trong plan được thực thi lại, thường xuất hiện ở node phía trong của nested loop join. Thời gian thực tế node đó tiêu tốn là `actual time` nhân với `loops`, nên một node có `actual time` nhỏ nhưng `loops` rất lớn vẫn có thể là nguyên nhân chính khiến query chậm.

## Summary

`EXPLAIN` cho biết optimizer dự định chạy query như thế nào mà không thực thi nó; `EXPLAIN ANALYZE` chạy thật và trả về số liệu đo được để đối chiếu với ước lượng. Đọc plan đúng nghĩa là so sánh cost/rows ước lượng với actual time/actual rows thực tế, đặc biệt chú ý `loops` trong các join lồng nhau. Sai lệch lớn giữa ước lượng và thực tế thường bắt nguồn từ thống kê bảng lỗi thời, không phải thiếu index. Trong production, luôn chạy `EXPLAIN ANALYZE` trên replica hoặc trong transaction có `ROLLBACK` để tránh side effect với các câu lệnh ghi dữ liệu.

## Knowledge Graph

- Execution Plan — `EXPLAIN` là công cụ để xem execution plan; hai khái niệm gắn liền nhau.
- Secondary Index — plan chuyển từ `Seq Scan` sang `Index Scan` phụ thuộc trực tiếp vào việc có secondary index phù hợp hay không.
- Covering Index — xuất hiện trong plan dưới dạng `Index Only Scan`, loại bỏ được bước truy cập bảng chính.
- Composite Index — thứ tự cột trong composite index quyết định optimizer có dùng được index hay không, thể hiện rõ qua plan.
- Missing WHERE Clause — một trong những nguyên nhân phổ biến khiến plan chọn `Seq Scan` toàn bảng thay vì tra cứu có điều kiện.
- Deadlocks — `EXPLAIN` không phát hiện deadlock, nhưng thứ tự truy cập dữ liệu thể hiện trong plan giúp suy luận nguy cơ lock contention.

## Five Things To Remember

- `EXPLAIN` không chạy query, `EXPLAIN ANALYZE` chạy thật và có thể gây side effect với lệnh ghi dữ liệu.
- Cost trong `EXPLAIN` là số ước lượng tương đối, không phải mili-giây thực tế.
- Sai lệch lớn giữa rows ước lượng và actual rows là dấu hiệu thống kê bảng đã lỗi thời.
- Đọc plan từ node trong cùng ra ngoài, và luôn nhân `actual time` với `loops` để biết chi phí thật.
- Không chạy `EXPLAIN ANALYZE` cho câu lệnh ghi dữ liệu trên production mà không bọc transaction có `ROLLBACK`.
