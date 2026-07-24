---
id: sharding
title: Sharding
tags: ["database", "scalability", "distributed-systems"]
---

# Sharding

> Status: Draft

## Problem

Một bảng `users` trên PostgreSQL đạt 400 triệu dòng, đĩa NVMe đã dùng hết dung lượng của instance lớn nhất mà nhà cung cấp cloud cho thuê, và CPU của node master luôn ở mức 90% chỉ để phục vụ write. Vertical scaling (nâng cấu hình instance) đã chạm trần — không còn instance nào lớn hơn để thuê, hoặc chi phí tăng phi tuyến so với lợi ích. Đội ngũ nhận ra vấn đề không còn là tối ưu query hay thêm index, mà là toàn bộ dữ liệu và toàn bộ tải đang dồn vào đúng một cỗ máy vật lý duy nhất, và không có cách nào để một máy đơn lẻ phục vụ mãi một khối lượng dữ liệu và traffic tăng vô hạn.

## Pain Points

- Write throughput bị giới hạn bởi CPU/IOPS của một node duy nhất — thêm read replica chỉ giúp scale read, không giúp gì cho write vì mọi replica vẫn phải áp dụng lại toàn bộ write từ master.
- Chi phí vertical scaling tăng phi tuyến: instance gấp đôi RAM/CPU không rẻ gấp đôi mà thường đắt hơn 3-4 lần, và ở một ngưỡng nào đó nhà cung cấp không còn instance lớn hơn để bán.
- Toàn bộ dữ liệu và traffic dồn vào một failure domain duy nhất — node đó down (hardware failure, OOM, network partition) là toàn hệ thống ngừng hoạt động, không có cách cô lập sự cố ở một tập con dữ liệu.
- Vacuum/maintenance/backup trên một bảng hàng trăm triệu-tỷ dòng mất hàng giờ, chiếm tài nguyên I/O trong lúc chạy và kéo dài cửa sổ bảo trì tới mức không còn khả thi vận hành.

## Solution

Sharding là kỹ thuật chia dữ liệu thành nhiều phần (shard) và đặt mỗi phần lên một database instance vật lý độc lập — mỗi shard chạy trên máy chủ, tiến trình database, và bộ nhớ/đĩa riêng của chính nó. Việc một dòng dữ liệu thuộc shard nào được quyết định bởi shard key (ví dụ `user_id`, `tenant_id`) đi qua một hàm định tuyến (hash, range, hoặc lookup table). Khác với partitioning — vốn chia bảng thành các partition vẫn nằm trong cùng một database instance/cluster để tối ưu quản lý và truy vấn cục bộ — sharding tách dữ liệu ra nhiều instance độc lập hoàn toàn, mỗi instance không biết gì về dữ liệu của instance khác, qua đó nhân bản được cả CPU, RAM, IOPS và dung lượng đĩa theo số lượng shard thay vì bị giới hạn bởi một máy duy nhất.

## How It Works

Ứng dụng (hoặc một lớp routing/proxy trung gian) tính `shard_id = f(shard_key)` cho mỗi query, rồi mở kết nối tới đúng instance chứa shard đó. Có ba chiến lược định tuyến phổ biến: **hash sharding** (`shard_id = hash(shard_key) % N`) phân bố dữ liệu đều nhưng khó thêm/bớt shard vì đổi N làm hash của gần như mọi key đổi theo, dẫn tới phải di chuyển lại phần lớn dữ liệu; **range sharding** (chia theo khoảng giá trị, vd. user_id 1-10M vào shard 1, 10M-20M vào shard 2) dễ mở rộng thêm shard mới ở đầu/cuối range nhưng dễ tạo hotspot nếu traffic không phân bố đều theo range (ví dụ user mới luôn ghi vào shard cuối cùng); **consistent hashing** giải quyết vấn đề của hash sharding thuần túy bằng cách ánh xạ cả shard và key lên một vòng hash, khi thêm/bớt shard chỉ một phần nhỏ dữ liệu (khoảng 1/N) cần di chuyển thay vì gần như toàn bộ. Một thành phần bắt buộc là lớp lưu trữ metadata định tuyến (config server trong MongoDB, hoặc bảng ánh xạ shard trong Vitess/Citus) để biết mỗi shard key/range nằm ở instance nào — thành phần này bản thân nó phải cực kỳ sẵn sàng vì mọi request đều phải tra cứu nó trước khi biết đi đâu. Cross-shard query (join hoặc aggregate dữ liệu nằm ở nhiều shard khác nhau, ví dụ "tổng doanh thu toàn hệ thống") không thể thực hiện bằng một câu SQL đơn giản như trên database đơn — ứng dụng hoặc lớp scatter-gather phải gửi query tới tất cả shard liên quan rồi tự gộp kết quả ở tầng ứng dụng, và transaction xuyên nhiều shard không còn ACID tự nhiên, phải dùng 2PC hoặc Saga pattern để mô phỏng.

## Production Architecture

Trong một hệ thống thương mại điện tử multi-tenant, `tenant_id` thường được chọn làm shard key vì gần như mọi truy vấn nghiệp vụ (đơn hàng, sản phẩm, khách hàng của một tenant) đều tự nhiên giới hạn trong phạm vi một shard — tránh được cross-shard query cho phần lớn traffic thực tế. Vitess (dùng bởi YouTube, Slack) và Citus (extension sharding cho PostgreSQL, dùng bởi Microsoft) đóng vai trò lớp routing/proxy đứng giữa ứng dụng và các shard MySQL/PostgreSQL vật lý, giúp ứng dụng viết SQL gần như bình thường trong khi lớp này lo việc định tuyến, gộp kết quả cross-shard, và resharding khi cần thêm node. MongoDB có sharding tích hợp sẵn (`mongos` router + config server + shard replica set), tự động cân bằng lại dữ liệu (balancer) khi một shard chứa nhiều dữ liệu hơn các shard khác. Ở quy mô rất lớn (Discord, Instagram), shard key còn được thiết kế nhúng thời gian (ví dụ Snowflake ID chứa timestamp) để vừa phân tán đều vừa giữ được thứ tự sắp xếp gần đúng theo thời gian tạo, phục vụ các query kiểu "N tin nhắn gần nhất".

## Trade-offs

- Cross-shard query/transaction mất tính đơn giản và ACID tự nhiên của database đơn — phải trả giá bằng độ phức tạp ứng dụng (scatter-gather, Saga, 2PC) hoặc chấp nhận thiết kế shard key sao cho hầu hết truy vấn không bao giờ cần cross-shard.
- Resharding (thêm/bớt shard khi dữ liệu tăng) là một trong những thao tác vận hành phức tạp và rủi ro nhất trong toàn bộ vòng đời hệ thống — di chuyển dữ liệu giữa các instance đang phục vụ traffic sống mà không được downtime hoặc mất dữ liệu.
- Chọn sai shard key (ví dụ theo giá trị phân bố lệch, hoặc theo cột hay bị truy vấn range xuyên shard) tạo ra hotspot — một shard quá tải trong khi các shard khác gần như rảnh, phủ nhận toàn bộ lợi ích của việc sharding.
- Chi phí vận hành nhân lên theo số lượng shard: backup, migration schema, monitoring, patching đều phải chạy trên N instance thay vì một, và một lỗi schema drift giữa các shard rất khó phát hiện sớm.
- Foreign key constraint và referential integrity do database đảm bảo tự nhiên trên một instance hoàn toàn biến mất khi dữ liệu liên quan nằm ở hai shard khác nhau — ứng dụng phải tự đảm bảo tính toàn vẹn này.

## Best Practices

- Chọn shard key trùng với chiều truy vấn phổ biến nhất của hệ thống (thường là `tenant_id` hoặc `user_id`) để tối đa hóa tỷ lệ query chỉ chạm một shard duy nhất.
- Trì hoãn sharding tới khi thực sự cần — thử hết read replica, caching, vertical scaling, và tối ưu schema/index trước, vì sharding không thể đảo ngược dễ dàng và tăng độ phức tạp toàn hệ thống đáng kể.
- Thiết kế shard key có đủ cardinality và phân bố đều để tránh hotspot, tránh dùng cột có giá trị tăng dần đơn điệu (như timestamp thuần) làm shard key chính nếu có thể tránh được.
- Xây dựng khả năng resharding từ đầu (consistent hashing, hoặc lớp routing tách biệt khỏi ứng dụng) thay vì hardcode `shard_id = user_id % N` trực tiếp trong code nghiệp vụ.
- Với truy vấn bắt buộc phải cross-shard (báo cáo, dashboard tổng hợp), tách riêng sang một pipeline ETL/data warehouse thay vì bắt hệ thống OLTP scatter-gather theo thời gian thực.

## Common Mistakes

- Hardcode công thức `hash(key) % N` trực tiếp trong code ứng dụng — khi cần thêm shard, N đổi làm gần như toàn bộ key bị định tuyến lại vị trí khác, gây một đợt migrate dữ liệu khổng lồ đáng lẽ có thể tránh bằng consistent hashing.
- Chọn shard key theo cột có phân bố lệch nghiêm trọng (ví dụ `country_code` khi 80% người dùng ở một quốc gia) — một shard trở thành hotspot trong khi các shard còn lại gần như không tải.
- Sharding quá sớm khi vấn đề thực ra có thể giải quyết bằng read replica hoặc caching, tự tạo ra độ phức tạp vận hành và cross-shard query không cần thiết.
- Không đồng bộ schema migration giữa các shard — chạy migration trên shard 1 nhưng quên hoặc chạy muộn trên shard 3, dẫn tới lỗi runtime khó tái hiện chỉ xảy ra với một số tenant nhất định.
- Thiết kế ứng dụng giả định foreign key hoặc transaction xuyên bảng vẫn hoạt động như trên database đơn, không nhận ra ràng buộc đó đã biến mất khi hai bảng liên quan nằm ở hai shard khác nhau.

## Interview Questions

**Hỏi**: Sharding khác partitioning ở điểm nào?

**Trả lời**: Partitioning chia một bảng thành nhiều phần nhỏ hơn nhưng vẫn nằm trong cùng một database instance/cluster, phục vụ mục đích quản lý và tối ưu truy vấn cục bộ (vd. partition pruning); engine vẫn nhìn thấy toàn bộ dữ liệu và có thể join/transaction xuyên partition gần như bình thường. Sharding tách dữ liệu ra nhiều instance vật lý độc lập hoàn toàn — mỗi instance có CPU, RAM, đĩa riêng và không biết gì về instance khác — nên nhân bản được tài nguyên tính toán theo số shard, đổi lại mất khả năng join/transaction tự nhiên xuyên shard.

**Hỏi**: Vì sao hash sharding thuần túy (`hash(key) % N`) gây khó khăn khi resharding, và consistent hashing giải quyết vấn đề đó thế nào?

**Trả lời**: Với `hash(key) % N`, khi N thay đổi (thêm/bớt shard), kết quả modulo của gần như mọi key đổi theo, buộc phải di chuyển lại gần như toàn bộ dữ liệu giữa các shard. Consistent hashing ánh xạ cả shard và key lên cùng một vòng giá trị hash cố định; khi thêm/bớt một shard, chỉ dữ liệu nằm trong đoạn vòng tròn liền kề shard đó cần di chuyển (khoảng 1/N tổng dữ liệu), phần còn lại giữ nguyên vị trí.

**Hỏi**: Làm sao thực hiện một transaction cập nhật dữ liệu nằm ở hai shard khác nhau mà vẫn giữ tính nhất quán?

**Trả lời**: Không có ACID transaction tự nhiên xuyên hai instance vật lý độc lập. Hai cách tiếp cận phổ biến: two-phase commit (2PC) — điều phối commit đồng thời trên cả hai shard, đảm bảo atomicity nhưng tốn chi phí lock và giảm availability nếu coordinator hoặc một shard gặp sự cố giữa chừng; hoặc Saga pattern — chia thành các bước cục bộ tại từng shard kèm compensating action để hoàn tác nếu bước sau thất bại, chấp nhận eventual consistency thay vì atomicity tức thời.

## Summary

Sharding chia dữ liệu ra nhiều database instance vật lý độc lập theo shard key, cho phép nhân bản CPU, RAM, IOPS và dung lượng đĩa theo số lượng shard thay vì bị giới hạn bởi một máy duy nhất — khác với partitioning vốn chỉ chia nhỏ trong cùng một instance. Shard key được định tuyến qua hash, range, hoặc consistent hashing, mỗi cách đánh đổi khác nhau giữa độ đều của phân bố dữ liệu và chi phí resharding. Cái giá phải trả là mất ACID transaction và foreign key tự nhiên xuyên shard, cross-shard query phải scatter-gather ở tầng ứng dụng, và resharding là một trong những thao tác vận hành rủi ro nhất. Nên trì hoãn sharding tới khi các giải pháp đơn giản hơn (read replica, caching, vertical scaling) đã cạn, và khi làm thì chọn shard key trùng chiều truy vấn phổ biến nhất để tối thiểu hóa cross-shard query.

## Knowledge Graph

- Partitioning — khái niệm dễ nhầm lẫn nhất, chia nhỏ dữ liệu nhưng vẫn trong cùng một database instance.
- Consistent Hashing — kỹ thuật định tuyến giảm chi phí resharding so với hash modulo thuần túy.
- Saga Pattern — cách mô phỏng transaction xuyên shard khi 2PC quá tốn chi phí hoặc giảm availability quá nhiều.
- ACID — tính chất chỉ đảm bảo tự nhiên trong phạm vi một shard/database instance, không xuyên shard.
- Replication — thường kết hợp với sharding, mỗi shard tự có replica riêng để đảm bảo high availability độc lập với các shard khác.
- Clustered Index — quyết định thứ tự vật lý bên trong một shard, độc lập với cách dữ liệu được phân chia giữa các shard.

## Five Things To Remember

- Sharding tách dữ liệu qua nhiều máy vật lý độc lập, partitioning chỉ chia nhỏ trong một máy.
- Shard key nên trùng với chiều truy vấn phổ biến nhất để tránh cross-shard query.
- Hash modulo thuần túy làm resharding cực kỳ tốn kém; consistent hashing giảm chi phí đó xuống còn khoảng 1/N.
- Transaction và foreign key xuyên shard không còn tự nhiên — cần Saga hoặc 2PC để mô phỏng.
- Trì hoãn sharding tới khi read replica, caching, và vertical scaling đã không còn đủ.
