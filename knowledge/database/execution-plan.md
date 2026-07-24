---
id: execution-plan
title: Execution Plan
tags: ["database", "performance"]
---

# Execution Plan

> Status: Draft

## Problem

Một API `GET /orders?customer_id=123` chạy ổn định vài ms suốt nhiều tháng, rồi một ngày latency tăng vọt lên vài giây mà không có deploy nào liên quan. Team kiểm tra code, kiểm tra connection pool, kiểm tra cache — tất cả bình thường. Vấn đề thực sự nằm ở chỗ không ai nhìn vào execution plan: bảng `orders` đã lớn dần từ 10 nghìn lên 50 triệu dòng, và query đang chạy `Seq Scan` toàn bảng thay vì `Index Scan`, vì thống kê (statistics) của bảng đã lỗi thời khiến optimizer đánh giá sai chi phí giữa hai phương án.

## Pain Points

- Không đọc execution plan nghĩa là không biết query đang quét toàn bảng hay dùng index — chỉ đo được "chậm" chứ không biết chậm ở bước nào, dẫn đến tối ưu sai chỗ (thêm cache, tăng CPU) thay vì sửa đúng nguyên nhân.
- Sai lệch lớn giữa `rows estimate` và số dòng thực tế khiến optimizer chọn nhầm join strategy (ví dụ Nested Loop thay vì Hash Join), làm một query đáng lẽ chạy vài ms lại chạy hàng chục giây.
- Statistics lỗi thời sau khi bulk insert/delete hàng loạt (migration, batch job) khiến optimizer ra quyết định dựa trên dữ liệu không còn đúng, và triệu chứng chỉ xuất hiện production chứ không tái hiện ở staging (nơi dữ liệu nhỏ và mới).
- Đội ngũ vận hành thiếu kỹ năng đọc `EXPLAIN ANALYZE` phải chờ DBA hoặc chuyên gia bên ngoài mỗi khi có sự cố performance, kéo dài MTTR (mean time to recovery) trong lúc production đang bị ảnh hưởng.

## Solution

Execution plan (hay query plan) là cây các bước cụ thể mà database engine sẽ thực hiện để lấy ra kết quả của một câu SQL: quét bảng bằng cách nào (seq scan, index scan, index-only scan, bitmap scan), join theo thứ tự và thuật toán nào (nested loop, hash join, merge join), sort/aggregate ở bước nào. Optimizer (cost-based optimizer) chọn plan bằng cách ước lượng chi phí (cost) của nhiều phương án khả thi dựa trên thống kê đã thu thập về bảng, rồi chọn phương án có chi phí thấp nhất — không phải phương án nhanh nhất thực sự, mà là phương án rẻ nhất theo mô hình chi phí của nó. `EXPLAIN` hiển thị plan đó kèm chi phí ước lượng mà không chạy query thật; `EXPLAIN ANALYZE` chạy query thật và bổ sung số liệu thực tế (thời gian thực, số dòng thực) để so sánh với ước lượng.

## How It Works

Optimizer dựa vào bảng thống kê (`pg_statistic` trong Postgres, cập nhật qua `ANALYZE`; hoặc statistics trong SQL Server, InnoDB) lưu các thông tin như: số dòng ước lượng của bảng (`reltuples`), số giá trị phân biệt trên mỗi cột (n_distinct), phân bố giá trị (histogram), tỷ lệ NULL. Từ đó nó tính selectivity — tỷ lệ dòng thỏa điều kiện WHERE trên tổng số dòng — rồi nhân với chi phí I/O và CPU ước lượng cho từng phương án truy cập dữ liệu.

Với `Seq Scan`, chi phí gần như tuyến tính theo kích thước bảng: đọc tuần tự toàn bộ block trên đĩa, chi phí I/O thấp trên mỗi trang nhưng tổng số trang đọc lớn. Với `Index Scan`, optimizer seek trực tiếp vào B-Tree để tìm vị trí, rồi với mỗi match phải nhảy sang heap (bảng chính) để lấy dữ liệu đầy đủ — chi phí seek thấp nhưng mỗi lần nhảy sang heap là một random I/O riêng biệt, nên nếu số dòng match quá lớn (selectivity thấp), tổng chi phí random I/O của index scan vượt qua chi phí sequential I/O của seq scan. Đây là lý do optimizer đôi khi "cố tình" bỏ qua index đã tạo: không phải index vô dụng, mà vì với tỷ lệ dòng match quá cao, quét tuần tự thực sự rẻ hơn.

`Index-only scan` (Postgres) hoặc covering index (SQL Server, MySQL) là biến thể tránh hẳn bước nhảy sang heap nếu mọi cột cần trong SELECT đã có sẵn trong chính index — nhanh hơn hẳn index scan thường nhưng phụ thuộc vào visibility map (Postgres) đủ mới để biết dòng có còn "visible" mà không cần check heap.

`rows estimate` là con số optimizer dự đoán mỗi node trong plan sẽ trả về bao nhiêu dòng, dựa hoàn toàn vào thống kê tại thời điểm `ANALYZE` chạy lần cuối. Khi `EXPLAIN ANALYZE` chạy, nó ghi thêm `rows actual` — số dòng thực tế đi qua node đó. Sai lệch lớn giữa hai con số (ví dụ ước lượng 100 dòng nhưng thực tế 2 triệu dòng) là dấu hiệu rõ ràng nhất của statistics lỗi thời hoặc cột có phân bố dữ liệu phức tạp (correlated columns) mà optimizer không mô hình hóa được — và khi ước lượng sai, mọi quyết định phía trên nó trong cây plan (chọn join algorithm, thứ tự join) đều có nguy cơ sai theo, vì optimizer tính cost dựa trên rows estimate của bước trước.

## Production Architecture

Trong hệ thống OLTP có bảng lớn tăng trưởng liên tục (ví dụ bảng `events` ghi hàng triệu dòng mỗi ngày), autovacuum/auto-analyze (Postgres) hoặc scheduled statistics update (SQL Server) phải chạy đủ thường xuyên để thống kê không bị lệch quá xa thực tế; nhiều team production tắt hoặc tune autovacuum sai cách rồi gặp đúng vấn đề "plan tốt tuần trước, tệ tuần này" dù schema không đổi. Ở hệ thống dùng connection pooler với prepared statement (PgBouncer transaction mode, hoặc driver dùng prepared statement mặc định), Postgres có thể chuyển sang "generic plan" sau vài lần thực thi thay vì "custom plan" theo giá trị tham số cụ thể — khiến plan tối ưu cho tham số phổ biến nhưng rất tệ cho tham số hiếm (parameter sniffing), một nguồn incident khó chẩn đoán vì cùng một query text nhưng chạy nhanh/chậm tùy tham số truyền vào. Trong pipeline CI/CD nghiêm túc, một số team chạy `EXPLAIN` tự động trên migration mới hoặc query mới thêm vào để bắt sớm các plan có `Seq Scan` trên bảng lớn trước khi merge, thay vì đợi phát hiện ở production.

## Trade-offs

- `EXPLAIN ANALYZE` chạy query thật nên có side effect thực sự nếu query là `INSERT/UPDATE/DELETE` — dùng nhầm trên production với câu lệnh ghi dữ liệu là tự tay thực thi thay đổi không mong muốn; Postgres có `EXPLAIN (ANALYZE, ..., dblink)` hoặc bọc trong transaction rollback để tránh, nhưng không phải ai cũng nhớ làm vậy.
- Đọc plan tĩnh không đủ để kết luận chắc chắn: cùng một câu SQL có thể ra plan khác nhau tùy phân bố dữ liệu thực tế, index hiện có, cấu hình `work_mem`/`random_page_cost`, và thậm chí tùy tham số truyền vào (parameter sniffing) — nên công cụ phân tích tĩnh chỉ nên đưa ra cảnh báo có khả năng xảy ra vấn đề, không khẳng định tuyệt đối.
- Chi phí (cost) trong plan là đơn vị nội bộ arbitrary (dựa trên `seq_page_cost`, `random_page_cost`, `cpu_tuple_cost`...), không phải mili-giây thực tế — so sánh cost giữa hai plan có ý nghĩa, nhưng suy ra thời gian thực thi tuyệt đối từ riêng con số cost là sai lầm phổ biến.
- Ép optimizer chọn plan cụ thể (qua hint, hoặc tắt seq scan bằng `SET enable_seqscan = off`) có thể giải quyết một vấn đề tức thời nhưng đánh đổi bằng việc optimizer không còn tự thích nghi khi dữ liệu thay đổi theo thời gian — dễ tạo nợ kỹ thuật ẩn.

## Best Practices

- Chạy `EXPLAIN (ANALYZE, BUFFERS)` (Postgres) thay vì chỉ `EXPLAIN` khi cần chẩn đoán thật, để có cả thời gian thực tế lẫn số lần đọc buffer cache/đĩa cho từng bước.
- So sánh trực tiếp `rows` (ước lượng) với `actual rows` ở từng node trong plan — sai lệch lớn ở node nào là nơi cần chạy `ANALYZE <table>` lại hoặc xem xét tăng `statistics target` cho cột đó.
- Luôn wrap `EXPLAIN ANALYZE` cho câu lệnh ghi dữ liệu (`UPDATE`/`DELETE`/`INSERT`) trong transaction có `ROLLBACK` khi chạy trên production, tránh side effect ngoài ý muốn.
- Theo dõi plan thay đổi theo thời gian trên các query quan trọng (qua `pg_stat_statements` hoặc APM) thay vì chỉ kiểm tra một lần lúc viết query — dữ liệu tăng trưởng có thể lật ngược lựa chọn tối ưu của optimizer.
- Đảm bảo autovacuum/auto-analyze chạy đủ tần suất trên các bảng ghi nhiều, đặc biệt sau các đợt bulk load/migration lớn — chạy `ANALYZE` thủ công ngay sau khi nạp dữ liệu hàng loạt thay vì chờ tự động.

## Common Mistakes

- Chỉ nhìn `EXPLAIN` (không `ANALYZE`) rồi kết luận query nhanh hay chậm — con số cost và rows là ước lượng, không phản ánh thời gian hay số dòng thực tế đã thực thi.
- Chạy `EXPLAIN ANALYZE` trực tiếp trên production cho câu `UPDATE`/`DELETE` mà quên rằng nó thực sự thực thi thay đổi dữ liệu, không phải mô phỏng.
- Thấy có index trên cột lọc rồi mặc định optimizer sẽ dùng nó, không kiểm tra plan thực tế — với bảng nhỏ hoặc selectivity thấp, optimizer hoàn toàn có thể chọn seq scan một cách hợp lý.
- Không cập nhật statistics sau bulk insert/delete lớn (migration, batch job), khiến optimizer ra quyết định dựa trên phân bố dữ liệu cũ hoàn toàn không còn đúng.
- So sánh cost giữa hai DB khác nhau hoặc hai instance có cấu hình khác nhau (`random_page_cost`, RAM, loại đĩa) như thể chúng là cùng một đơn vị đo — cost chỉ có ý nghĩa so sánh nội bộ trong cùng một cấu hình.

## Interview Questions

**Hỏi**: Sự khác biệt giữa `EXPLAIN` và `EXPLAIN ANALYZE` là gì?

**Trả lời**: `EXPLAIN` chỉ hiển thị plan và chi phí ước lượng dựa trên thống kê, không thực sự chạy query. `EXPLAIN ANALYZE` chạy query thật, bổ sung thời gian thực và số dòng thực tế (`actual rows`) để so sánh với ước lượng — nhưng vì chạy thật nên có side effect nếu query là câu lệnh ghi dữ liệu.

**Hỏi**: Vì sao optimizer đôi khi bỏ qua index đã tạo và chọn `Seq Scan`?

**Trả lời**: Vì index scan phải trả giá bằng random I/O cho mỗi dòng match khi nhảy từ index sang heap để lấy dữ liệu đầy đủ; nếu số dòng match chiếm tỷ lệ lớn trong bảng (selectivity thấp), tổng chi phí random I/O đó vượt qua chi phí sequential I/O của việc quét toàn bảng, nên optimizer chọn seq scan vì nó thực sự rẻ hơn theo mô hình chi phí, không phải vì index bị lỗi.

**Hỏi**: Khi thấy `rows estimate` lệch rất xa `actual rows` trong plan, bước tiếp theo nên làm gì?

**Trả lời**: Chạy `ANALYZE <table>` để cập nhật lại thống kê, kiểm tra xem cột đó có cần tăng `statistics target` (Postgres) vì phân bố dữ liệu phức tạp (nhiều giá trị, correlated với cột khác) hay không, và xác nhận autovacuum/auto-analyze có đang chạy đủ tần suất trên bảng đó, đặc biệt sau các đợt ghi dữ liệu hàng loạt gần nhất.

## Summary

Execution plan là cây các bước cụ thể mà database engine dùng để lấy dữ liệu, được cost-based optimizer chọn dựa trên thống kê về bảng chứ không phải chạy thử tất cả phương án. `EXPLAIN` cho xem plan ước lượng, `EXPLAIN ANALYZE` chạy thật và cho số liệu thực tế để đối chiếu — sai lệch lớn giữa rows ước lượng và thực tế là dấu hiệu thống kê lỗi thời, kéo theo quyết định sai ở mọi bước phía trên trong cây plan. Seq scan không phải luôn là dấu hiệu xấu: với selectivity thấp, nó có thể rẻ hơn thực sự so với index scan vì tránh được random I/O khi nhảy sang heap. Đọc plan tĩnh không đủ để kết luận chắc chắn vì plan phụ thuộc vào dữ liệu, cấu hình, và cả tham số truyền vào — cần xác nhận bằng dữ liệu thật trước khi hành động.

## Knowledge Graph

- Composite Index — execution plan là công cụ xác nhận composite index có thực sự được optimizer seek hay chỉ filter sau scan.
- Covering Index — index-only scan trong plan là dấu hiệu covering index đang hoạt động đúng, tránh được bước nhảy sang heap.
- Clustered Index — ảnh hưởng trực tiếp đến chi phí random I/O khi optimizer cân nhắc giữa index scan và seq scan.
- Missing WHERE Clause — công cụ phân tích tĩnh dựa một phần vào các dấu hiệu tương tự plan (thiếu điều kiện lọc dẫn đến seq scan chắc chắn) để cảnh báo sớm.
- Locks — thời gian một transaction giữ lock phụ thuộc vào plan được chọn, plan chậm (seq scan trên bảng lớn) kéo dài thời gian giữ lock và tăng nguy cơ deadlock.

## Five Things To Remember

- Execution plan là kế hoạch cụ thể database chọn để lấy dữ liệu, dựa trên chi phí ước lượng chứ không phải thử tất cả phương án.
- `EXPLAIN` chỉ ước lượng, `EXPLAIN ANALYZE` chạy thật — cẩn thận side effect với câu lệnh ghi dữ liệu.
- Seq scan không phải luôn xấu: với selectivity thấp, nó có thể rẻ hơn index scan vì tránh random I/O.
- Sai lệch lớn giữa rows ước lượng và thực tế là dấu hiệu statistics lỗi thời, cần `ANALYZE` lại.
- Plan phụ thuộc vào dữ liệu, cấu hình và tham số thực tế — không suy luận chắc chắn chỉ từ nhìn SQL tĩnh.
