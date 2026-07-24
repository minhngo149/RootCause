---
id: secondary-index
title: Secondary Index
tags: ["database", "index"]
---

# Secondary Index

> Status: Draft

## Problem

Bảng dữ liệu luôn có một cấu trúc lưu trữ vật lý chính — thường là clustered index (InnoDB) hoặc heap có rowid (PostgreSQL). Khi query lọc theo một cột không phải khóa chính (`WHERE email = ?`, `WHERE status = ?`), engine không có cách nào tìm nhanh dòng phù hợp nếu không quét toàn bộ bảng. Secondary index được tạo ra để giải quyết đúng bài toán này, nhưng nhiều engineer coi nó tương đương với primary index về tốc độ, dẫn đến hiểu sai khi phân tích execution plan hoặc thiết kế schema.

## Pain Points

- Query dùng secondary index nhưng `SELECT *` hoặc lấy cột ngoài index buộc engine phải thực hiện thêm một bước lookup về clustered index/heap cho từng dòng khớp — I/O tăng tuyến tính theo số dòng match, không phải hằng số như nhiều người tưởng.
- Trên InnoDB, secondary index lưu giá trị primary key thay vì rowid vật lý; nếu primary key dài (UUID) hoặc thay đổi (do rebuild), mọi secondary index đều phình to và lookup chậm hơn.
- Bảng có nhiều secondary index làm chậm INSERT/UPDATE/DELETE vì mỗi thay đổi dữ liệu phải cập nhật đồng bộ tất cả index liên quan — production từng ghi nhận throughput ghi giảm 30-50% khi thêm index thứ 5, 6 vào bảng nóng.
- Thiếu hiểu biết cơ chế 2 bước (index lookup + table lookup) khiến engineer đọc execution plan thấy "Index Scan" và kết luận nhầm là đã tối ưu, trong khi phần lớn thời gian thực tế nằm ở bước lookup bảng gốc.

## Solution

Secondary index là một cấu trúc index độc lập, được sắp xếp theo cột không phải khóa chính, mỗi entry trỏ tới vị trí dòng dữ liệu thật thông qua clustered key (InnoDB) hoặc rowid/ctid (PostgreSQL, SQLite). Nếu index chứa đủ cột mà query cần thì không cần bước lookup thứ hai (covering index); ngược lại, mọi truy vấn qua secondary index không covering đều tốn thêm một round-trip đọc bảng gốc cho mỗi dòng khớp.

## How It Works

Trong InnoDB, bảng được tổ chức dưới dạng clustered index B+Tree theo primary key — lá của cây này chứa toàn bộ dữ liệu dòng. Một secondary index cũng là B+Tree riêng, nhưng lá của nó chỉ chứa giá trị cột được index cùng với giá trị primary key tương ứng, không chứa các cột khác. Khi query chạy `WHERE email = 'a@b.com'`, engine trước tiên tìm trong B+Tree của secondary index `idx_email` để lấy ra primary key khớp, sau đó dùng chính primary key đó để đi tiếp vào clustered index B+Tree lần thứ hai nhằm lấy toàn bộ dòng dữ liệu — đây chính là bước "bookmark lookup"/"key lookup" xuất hiện trong execution plan. Với PostgreSQL, cơ chế khác một chút vì bảng là heap không sắp xếp: secondary index (btree, hash, gin...) lưu trực tiếp `ctid` — con trỏ vật lý (file, block, offset) tới dòng trong heap — nên bước lookup là truy cập trực tiếp trang heap thay vì đi qua một B+Tree thứ hai, nhưng chi phí I/O ngẫu nhiên vẫn tương tự nếu các dòng khớp nằm rải rác trên nhiều block khác nhau (do không có clustering vật lý theo mặc định). Số lượng bước lookup này tỷ lệ thuận với số dòng khớp điều kiện WHERE, nên optimizer sẽ so sánh chi phí ước tính giữa "Index Scan + N lookup" với "Seq Scan" và chuyển sang quét toàn bảng nếu N đủ lớn (thường ngưỡng khoảng 5-15% tổng số dòng tùy cost model).

## Production Architecture

Trong một hệ thống e-commerce dùng MySQL/InnoDB, bảng `orders` có primary key `id BIGINT AUTO_INCREMENT` và secondary index trên `customer_id` để phục vụ trang "lịch sử đơn hàng". Query `SELECT id, total, created_at FROM orders WHERE customer_id = ? ORDER BY created_at DESC LIMIT 20` chạy qua secondary index `idx_customer_id`, rồi phải lookup lại clustered index 20 lần để lấy `total` và `created_at` — nếu đổi thành composite index `(customer_id, created_at, total)` thì trở thành covering index, loại bỏ hoàn toàn bước lookup thứ hai. Ở PostgreSQL, các dashboard analytics dùng secondary btree index trên `event_type` kết hợp `CLUSTER` định kỳ để sắp xếp lại heap theo index đó, giảm số random I/O khi lookup vì các dòng cùng `event_type` nằm gần nhau trên đĩa — dù `CLUSTER` chỉ có hiệu lực tại thời điểm chạy, không tự động duy trì cho dữ liệu ghi mới.

## Trade-offs

Secondary index tăng tốc đọc theo cột không phải khóa chính, nhưng đổi lại: (1) tốn thêm dung lượng lưu trữ, thường 20-40% kích thước bảng gốc cho mỗi index; (2) làm chậm mọi thao tác ghi vì phải cập nhật đồng thời B+Tree của index; (3) nếu không phải covering index, mỗi dòng khớp phát sinh thêm một I/O ngẫu nhiên để lookup bảng gốc, chi phí này có thể vượt qua lợi ích của việc dùng index khi số dòng khớp lớn; (4) trên InnoDB, primary key càng dài thì mọi secondary index càng phình to vì mỗi entry đều nhúng theo primary key — đây là lý do UUID làm primary key thường bị khuyến cáo tránh trong bảng có nhiều secondary index.

## Best Practices

- Ưu tiên thiết kế composite/covering index cho các query đọc nhiều (hot path) để loại bỏ bước lookup thứ hai.
- Dùng `EXPLAIN ANALYZE` để kiểm tra thực tế có "Heap Fetches" (PostgreSQL) hay "Using index condition"/rows examined lớn (MySQL) hay không, thay vì chỉ nhìn tên loại scan.
- Giữ primary key ngắn và ổn định (INT/BIGINT tăng dần) khi dùng InnoDB, vì mọi secondary index đều gánh chi phí lưu trữ theo kích thước primary key.
- Định kỳ rà soát và xóa các secondary index không còn được optimizer sử dụng (unused index) để giảm chi phí ghi và bảo trì.
- Với PostgreSQL, cân nhắc `VACUUM`/autovacuum đều đặn để index-only scan (dựa trên visibility map) hoạt động hiệu quả, tránh bị fallback về heap fetch.

## Common Mistakes

- Tạo secondary index riêng lẻ trên từng cột thay vì composite index đúng thứ tự theo pattern query thực tế, dẫn đến optimizer không tận dụng được đầy đủ.
- Dùng `SELECT *` trên bảng có secondary index, vô tình phá vỡ khả năng covering index và ép engine luôn phải lookup bảng gốc.
- Thêm quá nhiều secondary index trên bảng ghi nhiều (write-heavy) mà không đo tác động lên throughput INSERT/UPDATE.
- Nhầm lẫn secondary index và clustered index có cùng tốc độ truy vấn, bỏ qua chi phí ẩn của bước lookup thứ hai khi ước tính hiệu năng.
- Dùng UUID ngẫu nhiên làm primary key trên bảng có nhiều secondary index mà không cân nhắc chi phí lưu trữ và fragmentation tăng thêm.

## Interview Questions

**Hỏi**: Secondary index khác clustered index như thế nào về mặt lưu trữ dữ liệu?

**Trả lời**: Clustered index sắp xếp và lưu trực tiếp toàn bộ dữ liệu dòng theo thứ tự khóa chính (lá của B+Tree chính là dữ liệu). Secondary index là cấu trúc riêng, lá chỉ chứa giá trị cột được index cùng con trỏ (primary key ở InnoDB, hoặc ctid ở PostgreSQL) trỏ về dòng dữ liệu thật, nên cần thêm một bước lookup nếu không phải covering index.

**Hỏi**: Vì sao một query dùng secondary index vẫn có thể chậm dù execution plan hiện "Index Scan"?

**Trả lời**: Vì "Index Scan" chỉ đảm bảo bước tìm kiếm trên index nhanh; nếu index không covering hết các cột SELECT, mỗi dòng khớp còn phát sinh thêm một I/O ngẫu nhiên lookup về bảng gốc — với số dòng khớp lớn, tổng chi phí lookup này có thể lớn hơn cả một sequential scan.

**Hỏi**: Tại sao dùng UUID làm primary key lại ảnh hưởng tới hiệu năng của secondary index trên InnoDB?

**Trả lời**: Vì InnoDB lưu giá trị primary key trong mọi entry của secondary index để làm con trỏ lookup; primary key càng dài (UUID 16 bytes so với BIGINT 8 bytes) thì mọi secondary index đều tăng kích thước tương ứng, làm giảm hiệu quả cache và tăng I/O.

## Summary

Secondary index là cấu trúc tra cứu độc lập trỏ tới clustered index (qua primary key) hoặc heap (qua rowid/ctid), giúp tránh quét toàn bảng khi lọc theo cột không phải khóa chính. Nếu index không chứa đủ cột mà query cần, engine phải thực hiện thêm một bước lookup về dữ liệu gốc cho mỗi dòng khớp, đây là chi phí thường bị đánh giá thấp khi đọc execution plan. Thiết kế covering index đúng theo pattern truy vấn thực tế loại bỏ hoàn toàn bước lookup này, đổi lại tốn thêm dung lượng và chi phí ghi. Việc chọn kiểu dữ liệu primary key và số lượng secondary index trên bảng cần cân bằng giữa tốc độ đọc, tốc độ ghi, và dung lượng lưu trữ.

## Knowledge Graph

- Covering Index — trường hợp đặc biệt của secondary index loại bỏ được bước lookup thứ hai.
- Execution Plan — công cụ để phát hiện bước lookup ẩn sau khi dùng secondary index.
- Clustered Index — cấu trúc lưu trữ dữ liệu vật lý mà secondary index trỏ tới (InnoDB).
- B+Tree — cấu trúc dữ liệu nền tảng cho cả clustered và secondary index trong hầu hết RDBMS.
- Index-Only Scan — kỹ thuật PostgreSQL tránh heap fetch khi index và visibility map đủ điều kiện.
- Write Amplification — hệ quả phụ khi bảng có nhiều secondary index và tần suất ghi cao.

## Five Things To Remember

- Secondary index trỏ tới clustered key hoặc rowid, không chứa trực tiếp dữ liệu dòng.
- Không phải covering index thì luôn cần thêm một bước lookup bảng gốc cho mỗi dòng khớp.
- `SELECT *` phá vỡ khả năng covering index dù đã có index phù hợp cho điều kiện WHERE.
- Primary key dài (UUID) làm phình to mọi secondary index trên InnoDB.
- Càng nhiều secondary index thì ghi dữ liệu càng chậm, cần cân bằng theo tỷ lệ đọc/ghi thực tế.
