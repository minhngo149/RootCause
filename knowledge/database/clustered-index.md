---
id: clustered-index
title: Clustered Index
tags: ["database", "index"]
---

# Clustered Index

> Status: Draft

## Problem

Một đội ngũ thiết kế bảng `orders` với primary key là UUID random (`gen_random_uuid()`), insert vài triệu dòng/ngày qua nhiều connection song song. Sau vài tháng, throughput insert giảm dần, `iostat` cho thấy disk I/O tăng bất thường so với lượng dữ liệu ghi thực tế, và bảng bị bloat nhanh hơn dự kiến. Không ai trong đội hiểu rằng thứ tự vật lý của dữ liệu trên đĩa — không chỉ index B-tree logic — bị chi phối trực tiếp bởi giá trị primary key, và UUID random đang buộc InnoDB ghi dữ liệu vào ngẫu nhiên khắp các trang (page) thay vì tuần tự.

## Pain Points

- Insert với PK ngẫu nhiên (UUID v4) gây "random I/O" trên mỗi lần ghi, vì hàng mới phải chèn vào giữa các trang đã đầy thay vì nối vào cuối — dẫn tới page split liên tục.
- Page split làm phân mảnh (fragmentation) file dữ liệu, giảm hiệu quả cache buffer pool vì các dòng liên quan về mặt logic không còn nằm gần nhau vật lý.
- Bảng không có primary key rõ ràng khiến InnoDB tự sinh cột `ROW_ID` ẩn 6 byte làm clustered key — mất kiểm soát thứ tự lưu trữ, tăng rủi ro khi replication hoặc khi cần truy vấn theo range.
- Secondary index trên bảng lớn với PK dài (ví dụ UUID 36 ký tự hoặc composite key nhiều cột) khiến mọi secondary index đều phình to, vì mỗi entry secondary index phải nhúng kèm giá trị PK để trỏ ngược vào clustered index.

## Solution

Clustered index là cách tổ chức dữ liệu vật lý trên đĩa theo đúng thứ tự của index đó — nói cách khác, bản thân dữ liệu của bảng chính là các lá (leaf node) của cây B-tree clustered index, không phải một cấu trúc tách rời trỏ đến dữ liệu. Vì dữ liệu chỉ có thể được sắp xếp vật lý theo một thứ tự duy nhất tại một thời điểm, mỗi bảng chỉ có thể có đúng một clustered index. Trong InnoDB (MySQL), clustered index mặc định chính là primary key; nếu bảng không khai báo PK, InnoDB sẽ chọn unique key không-null đầu tiên, hoặc tự tạo `ROW_ID` ẩn nếu không có lựa chọn nào phù hợp.

## How It Works

Trong InnoDB, mỗi bảng được lưu dưới dạng một cây B+tree mà node lá chứa toàn bộ dữ liệu của dòng (không chỉ con trỏ) — đây chính là clustered index. Mọi secondary index khác (index phụ trên các cột không phải PK) là cây B+tree riêng biệt, nhưng lá của nó không chứa dữ liệu dòng mà chỉ chứa giá trị cột được index cộng với giá trị PK — nghĩa là truy vấn qua secondary index luôn cần thêm một bước tra ngược vào clustered index để lấy các cột còn lại (gọi là "double lookup"). Khi insert một dòng mới, InnoDB phải chèn nó vào đúng vị trí theo thứ tự PK trong cây; nếu trang (page, mặc định 16KB) chứa vị trí đó đã đầy, InnoDB thực hiện page split — tách trang thành hai, ghi lại một nửa dữ liệu sang trang mới, cực kỳ tốn kém so với việc chỉ append vào cuối trang hiện có. Đây là lý do PK tăng dần đơn điệu (auto_increment, hoặc ULID/Snowflake ID) giúp insert luôn rơi vào trang cuối cùng, gần như luôn là append thuần túy, tránh page split gần như hoàn toàn. PostgreSQL không có khái niệm clustered index tự động duy trì như InnoDB — bảng heap trong PostgreSQL không được sắp xếp vật lý; lệnh `CLUSTER` chỉ sắp xếp lại một lần tại thời điểm chạy, và thứ tự đó không được duy trì cho các insert sau đó trừ khi chạy lại `CLUSTER` định kỳ.

## Production Architecture

Trong một hệ thống OLTP dùng MySQL/InnoDB cho bảng `orders`, PK thường được thiết kế là `BIGINT AUTO_INCREMENT` thay vì UUID, chính xác vì lý do clustered index: insert mới luôn nối vào cuối cây B-tree, giữ buffer pool "nóng" tập trung ở vài trang cuối thay vì rải khắp bảng. Với các hệ thống cần ID phân tán (tránh lộ số lượng bản ghi, tránh single point sinh ID), người ta chuyển sang dùng ULID hoặc Snowflake ID — cả hai đều tăng dần theo thời gian (time-ordered) nên vẫn giữ được đặc tính insert tuần tự của clustered index, khác với UUID v4 hoàn toàn ngẫu nhiên. Trong thiết kế multi-tenant, một số hệ thống dùng composite clustered key `(tenant_id, id)` để các dòng của cùng một tenant nằm liền kề vật lý, giúp các query kiểu "lấy toàn bộ đơn hàng của tenant X" quét liên tục thay vì nhảy trang.

## Trade-offs

- PK tăng dần giúp insert nhanh và giảm fragmentation, nhưng làm lộ thông tin thứ tự/khối lượng dữ liệu (ai cũng đoán được có bao nhiêu order đã tạo) và tạo hotspot ghi trên node cuối cùng khi sharding.
- Range query theo PK (`WHERE id BETWEEN ... AND ...`) cực nhanh nhờ clustered index vì dữ liệu đã nằm liền kề vật lý, nhưng range query theo cột không phải PK vẫn phải trả giá double lookup qua secondary index.
- PK ngắn (INT/BIGINT) giữ secondary index nhỏ gọn; PK dài (UUID, composite nhiều cột) làm phình mọi secondary index vì PK bị nhúng vào từng entry — đánh đổi giữa tính phân tán của ID và chi phí lưu trữ toàn hệ thống.
- Chạy `CLUSTER` định kỳ trên PostgreSQL cải thiện data locality nhưng khóa bảng (ACCESS EXCLUSIVE) trong lúc chạy, không khả thi cho bảng lớn đang phục vụ traffic production liên tục.

## Best Practices

- Chọn PK tăng dần đơn điệu (auto_increment, ULID, Snowflake ID theo thời gian) cho bảng InnoDB có insert rate cao, tránh UUID v4 ngẫu nhiên làm PK.
- Giữ PK ngắn và ổn định (không đổi giá trị sau khi tạo) vì mọi secondary index đều gián tiếp phụ thuộc vào nó.
- Với truy vấn range phổ biến theo một cột cụ thể (vd. `tenant_id`, `created_at`), cân nhắc đưa cột đó vào đầu composite PK để tận dụng locality vật lý của clustered index.
- Trên PostgreSQL, nếu cần data locality cho một pattern truy vấn cụ thể, đánh giá kỹ chi phí khóa bảng của `CLUSTER` và lên lịch chạy vào cửa sổ bảo trì, không chạy tùy hứng trên bảng production đang hoạt động.
- Luôn khai báo PK tường minh cho mọi bảng InnoDB — không để MySQL tự sinh `ROW_ID` ẩn, vì nó không kiểm soát được và gây khó khăn khi cần replication hoặc partition sau này.

## Common Mistakes

- Dùng UUID v4 làm PK cho bảng insert rate cao mà không đo tác động lên page split và buffer pool hit ratio.
- Nhầm lẫn clustered index với covering index — clustered index quyết định thứ tự vật lý của toàn bộ dòng dữ liệu, còn covering index chỉ là secondary index chứa đủ cột cho một query cụ thể.
- Cho rằng PostgreSQL cũng tự động duy trì clustered order như InnoDB, dẫn đến kỳ vọng sai về hiệu năng range query sau một thời gian bảng có nhiều insert/update/delete xen kẽ.
- Thiết kế composite PK theo thứ tự cột sai (vd. `(created_at, tenant_id)` thay vì `(tenant_id, created_at)`) khiến pattern truy vấn phổ biến nhất theo tenant không tận dụng được locality.
- Đổi giá trị cột PK sau khi dòng đã tồn tại (update PK) — với InnoDB, đây thực chất là delete + insert lại toàn bộ dòng ở vị trí vật lý mới, chi phí cao hơn nhiều so với update một cột thường.

## Interview Questions

**Hỏi**: Tại sao mỗi bảng chỉ có thể có một clustered index?

**Trả lời**: Vì clustered index không phải là một cấu trúc tra cứu tách rời — chính dữ liệu của bảng được sắp xếp vật lý theo thứ tự của index đó. Dữ liệu chỉ có thể tồn tại theo một thứ tự vật lý duy nhất trên đĩa tại một thời điểm, nên không thể có hai clustered index song song cho cùng một bảng.

**Hỏi**: Tại sao dùng UUID ngẫu nhiên làm primary key trong InnoDB lại làm chậm insert khi bảng lớn dần?

**Trả lời**: Vì InnoDB phải chèn mỗi dòng mới vào đúng vị trí theo thứ tự giá trị PK trong cây B+tree; với UUID ngẫu nhiên, vị trí chèn rải đều khắp bảng thay vì luôn ở cuối, khiến các trang giữa bảng liên tục bị đầy và phải page split — tốn I/O ghi và gây phân mảnh, trong khi PK tăng dần chỉ cần append vào trang cuối.

**Hỏi**: Truy vấn qua secondary index tốn kém hơn truy vấn qua clustered index như thế nào?

**Trả lời**: Lá của secondary index chỉ chứa cột được index và giá trị PK, không chứa toàn bộ dòng dữ liệu, nên sau khi tìm được PK qua secondary index, InnoDB phải thực hiện thêm một lượt tra cứu vào clustered index để lấy các cột còn lại — gọi là double lookup — trong khi truy vấn trực tiếp qua PK (clustered index) chỉ cần một lượt duy nhất.

## Summary

Clustered index quyết định thứ tự lưu trữ vật lý của toàn bộ dữ liệu trong bảng, và trong InnoDB đó chính là primary key — mỗi bảng chỉ có đúng một clustered index vì dữ liệu chỉ tồn tại theo một thứ tự vật lý tại một thời điểm. Lựa chọn PK sai (đặc biệt là UUID ngẫu nhiên) gây page split, phân mảnh, và giảm hiệu quả cache trên các hệ thống insert rate cao. Mọi secondary index đều gián tiếp phụ thuộc vào clustered key, nên PK cần ngắn, ổn định, và lý tưởng là tăng dần theo thời gian. PostgreSQL không duy trì clustered order tự động như InnoDB, nên các giả định về data locality cần được kiểm chứng riêng cho từng engine. Hiểu đúng cơ chế này giúp thiết kế schema tránh được các vấn đề hiệu năng chỉ lộ rõ khi dữ liệu tăng lên tới quy mô production thực sự.

## Knowledge Graph

- Covering Index — secondary index chứa đủ cột cho query, khác với clustered index vốn quyết định thứ tự vật lý toàn bộ bảng.
- Execution Plan — công cụ để xác nhận query có đang tận dụng clustered index (range scan) hay phải table/index lookup tốn kém.
- Primary Key Design — lựa chọn kiểu dữ liệu và chiến lược sinh giá trị PK ảnh hưởng trực tiếp đến hiệu năng clustered index.
- Page Split — hệ quả vật lý khi insert vào giữa cây B+tree clustered index đã đầy trang.
- Buffer Pool — vùng nhớ cache của InnoDB, hiệu quả cache phụ thuộc nhiều vào locality vật lý do clustered index tạo ra.
- Sharding Strategy — PK tăng dần đơn điệu có thể tạo hotspot ghi khi phân mảnh dữ liệu theo node, cần cân nhắc cùng lúc với thiết kế clustered index.

## Five Things To Remember

- Mỗi bảng chỉ có một clustered index vì dữ liệu chỉ có một thứ tự vật lý tại một thời điểm.
- Trong InnoDB, clustered index mặc định chính là primary key.
- UUID ngẫu nhiên làm PK gây page split và phân mảnh khi insert rate cao.
- Secondary index luôn cần double lookup qua clustered index để lấy đủ cột.
- PostgreSQL không tự động duy trì clustered order như InnoDB.
