---
id: partitioning
title: Partitioning
tags: ["database", "scalability"]
---

# Partitioning

> Status: Draft

## Problem

Một bảng `events` ghi log hành vi người dùng tăng 50 triệu dòng/tháng, sau hai năm đạt hơn 1.2 tỷ dòng trên một instance PostgreSQL duy nhất. Query `SELECT * FROM events WHERE created_at >= '2026-07-01'` vốn chỉ cần quét dữ liệu của tháng hiện tại, nhưng planner vẫn phải duyệt toàn bộ B-tree index bao trùm cả 1.2 tỷ dòng lịch sử, và job xóa dữ liệu cũ hơn 90 ngày bằng `DELETE FROM events WHERE created_at < ...` khóa hàng loạt dòng, tạo dead tuple khổng lồ khiến autovacuum chạy hàng giờ liền. Đội vận hành không nhận ra rằng vấn đề không nằm ở thiếu index, mà ở việc toàn bộ vòng đời dữ liệu — ghi, đọc theo range thời gian, xóa theo lô — đều đang bị ép chạy trên một cấu trúc vật lý đơn nhất không có ranh giới logic nào để cắt nhỏ.

## Pain Points

- Index trên bảng lớn phình to vượt RAM khả dụng của buffer pool/shared_buffers, khiến mọi truy vấn dù chỉ cần dữ liệu gần đây vẫn phải đọc từ đĩa thay vì cache.
- Xóa dữ liệu cũ bằng `DELETE` hàng loạt tạo lượng dead tuple lớn, kéo dài thời gian autovacuum (PostgreSQL) hoặc purge thread (InnoDB), có thể gây table bloat và làm chậm mọi query khác trong lúc vacuum chạy.
- Backup/restore, rebuild index, hay `ALTER TABLE` thêm cột trên bảng hàng tỷ dòng mất hàng giờ đến hàng ngày, chặn cửa sổ bảo trì và tăng rủi ro khi cần rollback gấp.
- Không có ranh giới vật lý để archive hoặc di chuyển dữ liệu cũ sang storage rẻ hơn (cold storage), buộc toàn bộ dữ liệu — cả nóng lẫn lạnh — phải nằm chung trên disk tier đắt nhất.

## Solution

Partitioning là kỹ thuật chia một bảng logic lớn thành nhiều bảng vật lý con (partition) dựa trên một partition key, trong khi vẫn nằm trên cùng một instance database và được truy vấn như một bảng duy nhất qua tầng router của engine. Ba chiến lược phổ biến: range (chia theo khoảng giá trị liên tục, ví dụ theo tháng của `created_at`), hash (chia theo giá trị băm của khóa để phân bố đều, ví dụ theo `user_id`), và list (chia theo tập giá trị rời rạc, ví dụ theo `region` hoặc `tenant_id`). Planner khi nhận query có điều kiện lọc trùng với partition key có thể áp dụng partition pruning — chỉ quét các partition liên quan thay vì toàn bộ bảng, biến một scan hàng tỷ dòng thành scan vài chục triệu dòng.

## How It Works

Ở tầng catalog, PostgreSQL từ bản 10 trở đi có declarative partitioning: bảng cha (`PARTITION BY RANGE/LIST/HASH`) không chứa dữ liệu thực, chỉ là một định nghĩa logic; mỗi partition con là một bảng vật lý riêng với file trên đĩa, index riêng, và constraint kiểm tra ranh giới giá trị riêng. Khi query tới, planner so khớp điều kiện `WHERE` với ranh giới từng partition ngay ở bước lập kế hoạch (constraint exclusion / partition pruning) — nếu điều kiện không thể khớp với một partition nào đó (ví dụ `created_at >= '2026-07-01'` không thể rơi vào partition tháng 1), partition đó bị loại khỏi kế hoạch thực thi hoàn toàn, không tốn một I/O nào. MySQL/InnoDB triển khai tương tự nhưng partition được quản lý ở tầng storage engine, mỗi partition có `.ibd` file riêng; range partitioning trên MySQL còn hỗ trợ `RANGE COLUMNS` cho phép partition theo nhiều cột. Insert một dòng mới buộc engine phải tính giá trị partition key của dòng đó và định tuyến (route) trực tiếp đến đúng partition vật lý — với range theo thời gian, điều này đòi hỏi có sẵn partition tương ứng (PostgreSQL không tự tạo partition mới khi insert rơi ngoài mọi ranh giới hiện có, sẽ báo lỗi "no partition found for row" nếu thiếu default partition). Xóa dữ liệu cũ trên range partition theo thời gian trở thành thao tác `DROP TABLE`/`DETACH PARTITION` cấp catalog — gần như tức thời, không cần quét và xóa từng dòng, không tạo dead tuple, khác hẳn với `DELETE` hàng loạt trên bảng không partition.

## Production Architecture

Trong hệ thống logging/event-tracking dùng PostgreSQL, bảng `events` thường được range-partition theo `created_at` với chu kỳ tháng hoặc tuần, kèm một cron job tự động tạo partition mới cho kỳ sắp tới (qua extension `pg_partman` hoặc script riêng) và một job khác `DETACH` + archive partition cũ hơn N tháng sang S3/cold storage rồi drop. Với hệ thống multi-tenant SaaS có phân bố tenant không đều (vài tenant lớn chiếm phần lớn dữ liệu), hash partitioning theo `tenant_id` giúp phân tán đều I/O ghi giữa các partition, tránh một partition range bị hotspot khi tenant lớn tập trung ghi liên tục. Trong MySQL, các hệ thống billing thường dùng list partitioning theo `region` hoặc `country_code` để mỗi partition ánh xạ trực tiếp với một ranh giới nghiệp vụ/pháp lý (ví dụ yêu cầu lưu trữ dữ liệu EU tách biệt theo GDPR), kết hợp với việc đặt partition đó trên tablespace/volume riêng. Một số kiến trúc kết hợp partitioning với read replica: partition "nóng" (tháng hiện tại) được ưu tiên cache và phục vụ từ primary, còn partition "lạnh" được đọc chủ yếu từ replica dành riêng cho truy vấn phân tích/báo cáo.

## Trade-offs

- Partition pruning chỉ hiệu quả khi query có điều kiện lọc trực tiếp trên partition key; query không lọc theo cột đó (ví dụ tìm theo `user_id` trên bảng range-partition theo `created_at`) vẫn phải quét toàn bộ mọi partition, đôi khi chậm hơn một bảng không partition có index tốt vì phải mở nhiều cây B-tree riêng biệt.
- Foreign key trỏ đến bảng đã partition bị giới hạn đáng kể trên PostgreSQL (không thể tạo FK tham chiếu tới bảng partition cha một cách tự nhiên như bảng thường trong nhiều phiên bản), buộc phải thiết kế lại ràng buộc toàn vẹn dữ liệu ở tầng ứng dụng hoặc trigger.
- Global unique constraint không còn khả thi nếu unique key không chứa partition key — mỗi partition tự đảm bảo unique độc lập, nên `UNIQUE(id)` mà không có `created_at` trong range-partition theo `created_at` là không hợp lệ, buộc phải mở rộng khóa unique thành composite `(id, created_at)`.
- Số lượng partition tăng theo thời gian (đặc biệt range theo ngày trên hệ thống chạy nhiều năm) làm catalog phình to, tăng thời gian lập kế hoạch truy vấn (planning time) và tăng chi phí quản lý metadata, nên cần chiến lược archive/drop partition cũ định kỳ chứ không thể giữ vô hạn.
- Hash partitioning phân bố đều nhưng loại bỏ khả năng pruning theo range — không thể "chỉ quét dữ liệu tuần này" nếu partition key là hash, nên hash phù hợp cho cân bằng tải ghi, không phù hợp cho truy vấn phân tích theo thời gian.

## Best Practices

- Chọn partition key trùng với cột xuất hiện trong hầu hết mệnh đề `WHERE` của các truy vấn quan trọng nhất — nếu không, partitioning chỉ tăng độ phức tạp mà không mang lại lợi ích pruning.
- Với range partitioning theo thời gian, tự động hóa việc tạo partition tương lai trước (qua `pg_partman` hoặc cron job riêng) để tránh insert thất bại vì thiếu partition đích, và tự động hóa luôn việc archive/drop partition cũ.
- Đưa partition key vào mọi unique constraint và primary key trên PostgreSQL, vì engine không hỗ trợ global unique index xuyên partition.
- Giám sát số lượng partition và kích thước từng partition định kỳ — partition quá nhỏ (hàng nghìn partition rỗng) làm chậm planning time, partition quá lớn làm mất ý nghĩa của việc chia nhỏ.
- Kiểm tra kỹ execution plan (`EXPLAIN`) sau khi partition để xác nhận pruning thực sự xảy ra — không giả định rằng cứ partition là query sẽ tự động nhanh hơn.

## Common Mistakes

- Partition theo một cột không xuất hiện trong điều kiện lọc thực tế của ứng dụng, khiến mọi query vẫn quét toàn bộ partition trong khi vẫn gánh thêm overhead quản lý nhiều bảng con.
- Quên tạo default partition hoặc partition cho kỳ tương lai trên PostgreSQL, dẫn đến insert lỗi giữa production khi dữ liệu rơi ngoài mọi ranh giới đã định nghĩa.
- Nhầm lẫn partitioning với sharding — partitioning vẫn nằm trên một instance/server duy nhất, không giải quyết được giới hạn CPU/RAM/disk I/O của một node như sharding thực sự làm.
- Thiết kế unique constraint hoặc primary key không bao gồm partition key trên PostgreSQL, khiến deploy schema thất bại hoặc phải sửa lại toàn bộ ứng dụng.
- Để số lượng partition tăng không kiểm soát qua nhiều năm mà không có job archive/drop, khiến catalog phình to và planning time tăng dần một cách âm thầm cho tới khi trở thành vấn đề rõ rệt.

## Interview Questions

**Hỏi**: Partitioning khác sharding như thế nào?

**Trả lời**: Partitioning chia một bảng lớn thành nhiều partition vật lý nhưng vẫn nằm trên cùng một instance database, giải quyết vấn đề kích thước bảng và hiệu năng quét/quản lý dữ liệu trong phạm vi một server. Sharding chia dữ liệu ra nhiều server/instance độc lập, giải quyết giới hạn tài nguyên (CPU, RAM, disk I/O, connection) mà một node duy nhất không thể đáp ứng — hai kỹ thuật giải quyết hai lớp vấn đề khác nhau và có thể kết hợp cùng lúc (mỗi shard tự partition bên trong).

**Hỏi**: Partition pruning là gì và tại sao nó không phải lúc nào cũng xảy ra?

**Trả lời**: Partition pruning là khi query planner loại bỏ các partition không thể chứa dữ liệu khớp điều kiện `WHERE` ngay từ bước lập kế hoạch, chỉ quét các partition liên quan. Nó chỉ xảy ra khi điều kiện lọc tham chiếu trực tiếp (hoặc có thể suy ra được) giá trị của partition key; nếu query lọc theo cột khác không liên quan đến partition key, planner buộc phải quét toàn bộ mọi partition vì không có cách nào loại trừ chúng.

**Hỏi**: Tại sao PostgreSQL yêu cầu partition key phải nằm trong primary key/unique constraint của bảng đã partition?

**Trả lời**: Vì mỗi partition tự quản lý index và ràng buộc unique một cách độc lập, không có cấu trúc index toàn cục xuyên suốt mọi partition để đảm bảo tính duy nhất trên toàn bảng logic. Nếu unique constraint không chứa partition key, engine không thể đảm bảo hai dòng ở hai partition khác nhau không trùng giá trị, nên PostgreSQL buộc composite key phải bao gồm partition key để việc kiểm tra unique luôn nằm gọn trong phạm vi một partition.

## Summary

Partitioning chia một bảng lớn thành nhiều partition vật lý theo range, hash, hoặc list, trong khi vẫn nằm trên cùng một instance database và được truy vấn như một bảng logic duy nhất. Lợi ích cốt lõi là partition pruning — planner chỉ quét các partition liên quan đến điều kiện truy vấn — và khả năng xóa/archive dữ liệu cũ gần như tức thời bằng `DROP`/`DETACH` thay vì `DELETE` hàng loạt tốn kém. Đánh đổi chính nằm ở giới hạn ràng buộc toàn vẹn dữ liệu (unique constraint, foreign key phải bao gồm partition key) và việc chọn sai partition key khiến mọi lợi ích biến mất trong khi vẫn gánh thêm độ phức tạp vận hành. Partitioning không thay thế sharding — nó giải quyết vấn đề trong phạm vi một node, không giải quyết giới hạn tài nguyên vật lý của node đó. Chọn đúng partition key dựa trên pattern truy vấn thực tế của ứng dụng là yếu tố quyết định thành công của toàn bộ chiến lược.

## Knowledge Graph

- Sharding Strategy — phân tán dữ liệu ra nhiều instance/server, giải quyết lớp vấn đề khác với partitioning nhưng thường kết hợp cùng nhau.
- Clustered Index — quyết định thứ tự vật lý trong mỗi partition, ảnh hưởng đến hiệu năng insert/range scan bên trong từng partition con.
- Execution Plan — công cụ để xác nhận partition pruning có thực sự xảy ra hay planner đang quét toàn bộ partition.
- Locks — thao tác `DETACH PARTITION`/`ATTACH PARTITION` cần cấp khóa phù hợp để tránh chặn truy vấn đang chạy.
- Composite Index — nguyên tắc thiết kế composite key áp dụng trực tiếp khi partition key bắt buộc phải nằm trong unique constraint.
- Vacuum/Autovacuum — job dọn dead tuple mà partitioning giúp giảm tải bằng cách biến DELETE hàng loạt thành DROP partition.

## Five Things To Remember

- Partitioning chia bảng thành nhiều phần vật lý nhưng vẫn trên một instance duy nhất, khác với sharding.
- Chọn partition key trùng với cột lọc phổ biến nhất, nếu không pruning sẽ không xảy ra.
- Range partition theo thời gian biến việc xóa dữ liệu cũ thành DROP/DETACH gần như tức thời.
- PostgreSQL buộc partition key phải nằm trong mọi unique constraint và primary key.
- Luôn kiểm tra EXPLAIN để xác nhận partition pruning thực sự diễn ra, đừng giả định.
