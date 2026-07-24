---
id: n-plus-one
title: N+1 Query
tags: ["database", "performance", "orm"]
---

# N+1 Query

> Status: Draft

## Problem

Một ORM chạy 1 query để lấy danh sách N bản ghi (vd. `SELECT * FROM orders`), sau đó với mỗi bản ghi trong danh sách lại chạy thêm 1 query riêng để lấy dữ liệu quan hệ của nó (vd. `SELECT * FROM customers WHERE id = ?` lặp lại N lần). Tổng cộng ứng dụng thực thi 1 + N query thay vì một hoặc hai query gộp, dù kết quả trả về cho người dùng giống hệt nhau. Vấn đề này gần như vô hình trong lập trình — code đọc rất tự nhiên (`order.customer.name` trong vòng lặp), và với N nhỏ ở môi trường dev/test nó chạy nhanh và không ai để ý.

## Pain Points

- Độ trễ response tăng tuyến tính theo kích thước danh sách — trang danh sách 20 dòng ở dev chạy êm nhưng lên production với 5.000 dòng biến thành hàng nghìn round-trip DB trong một request.
- Mỗi query, dù nhỏ, đều tốn round-trip network tới DB (thường 1-5ms trong cùng datacenter, hàng chục ms nếu qua VPC peering hoặc connection pooler bên ngoài) — chi phí này cộng dồn chứ không song song hóa được nếu code chạy tuần tự.
- N+1 là nguyên nhân phổ biến nhất khiến connection pool bị cạn kiệt dưới tải: một request giữ connection lâu hơn hẳn bình thường vì đang chạy hàng trăm query nhỏ, làm các request khác phải chờ connection.
- Vấn đề thường không xuất hiện trong code review (đoạn code trông vô hại) và không lộ ra trong test với dataset nhỏ, chỉ nổ ra khi dữ liệu production đủ lớn — lúc đó đã thành incident thay vì bug thường.

## Solution

Giải pháp cốt lõi là **eager loading**: thay vì để ORM lười tải quan hệ (lazy loading) từng dòng một khi được truy cập, ứng dụng khai báo trước rằng quan hệ này cần được tải cùng lúc với bản ghi chính, gộp thành một hoặc hai query duy nhất bất kể N lớn cỡ nào. Cơ chế cụ thể là dùng JOIN (gộp cả hai bảng vào một query) hoặc dùng chiến lược "IN query" (một query lấy list ID, một query thứ hai lấy toàn bộ quan hệ với `WHERE id IN (...)`), tùy ORM và tùy loại quan hệ.

## How It Works

Lazy loading mặc định trong hầu hết ORM (Hibernate, ActiveRecord, Sequelize, Entity Framework, Django ORM) hoạt động bằng proxy object: khi truy vấn `orders`, ORM chỉ tải dữ liệu bảng `orders`, còn field quan hệ (`customer`) được gán một proxy rỗng. Proxy này chỉ thực sự chạy query khi bị truy cập lần đầu (`order.customer.name`) — đây gọi là lazy initialization. Trong vòng lặp `for order in orders: print(order.customer.name)`, mỗi lần truy cập `order.customer` ở một object khác nhau kích hoạt một query SELECT riêng vì proxy của từng object độc lập với nhau, ORM không biết trước rằng vòng lặp sắp truy cập toàn bộ N object.

Eager loading giải quyết bằng cách yêu cầu ORM tải trước (`JOIN FETCH` trong JPA/Hibernate, `.includes()`/`.eager_load()` trong ActiveRecord, `.select_related()`/`.prefetch_related()` trong Django, `.Include()` trong EF Core). Có hai cơ chế chính: (1) **JOIN-based** — ORM sinh một câu SQL duy nhất `SELECT o.*, c.* FROM orders o JOIN customers c ON o.customer_id = c.id`, phù hợp với quan hệ 1-1 hoặc many-to-1 vì không nhân bản dữ liệu nhiều; (2) **batch/IN-based** — ORM chạy 2 query tách biệt: `SELECT * FROM orders`, rồi thu thập toàn bộ `customer_id` distinct và chạy `SELECT * FROM customers WHERE id IN (...)`, sau đó map kết quả lại trong bộ nhớ ứng dụng — cách này phù hợp hơn với quan hệ 1-nhiều (`order.line_items`) vì JOIN trực tiếp sẽ làm nhân bản dòng `orders` theo số `line_items`, gây tốn băng thông network và bộ nhớ không cần thiết (Cartesian-like row explosion).

## Production Architecture

Trong một hệ thống e-commerce hiển thị trang "Đơn hàng của tôi", API trả về danh sách 50 order cùng tên khách hàng, tổng tiền và 3 sản phẩm đầu tiên mỗi đơn. Nếu viết code kiểu `orders.map(o => ({...o, customer: o.customer.name, items: o.items}))` mà không eager load, request này sinh ra 1 (lấy orders) + 50 (lấy customer) + 50 (lấy items) = 101 query. Dưới tải bình thường DB trả về nhanh nên latency vẫn chấp nhận được, nhưng khi traffic tăng gấp 10 lần vào giờ cao điểm, riêng lượng round-trip này đã chiếm phần lớn connection pool (vd. pool size 20 trên PgBouncer), khiến các request khác timeout chờ connection dù CPU DB còn dư. Kiến trúc đúng là ở tầng API/service layer, mọi endpoint trả về list phải khai báo tường minh các quan hệ cần eager load ngay tại query gốc (`Order.includes(:customer, :items).where(...)`), và team thường bật một công cụ giám sát số lượng query mỗi request (vd. Bullet gem cho Rails, `django-debug-toolbar`, Hibernate statistics) chạy trong CI hoặc APM để tự động cảnh báo khi một endpoint vượt ngưỡng query count bất thường.

## Trade-offs

Eager loading giảm số round-trip nhưng tăng lượng dữ liệu trả về mỗi query nếu dùng JOIN cho quan hệ 1-nhiều — mỗi dòng `orders` bị lặp lại theo số `items`, gây lãng phí băng thông và bộ nhớ nếu số lượng quan hệ lớn (order có 200 items sẽ nhân bản dòng order 200 lần qua network). Chiến lược batch/IN tránh được vấn đề nhân bản nhưng đổi lại tốn thêm round-trip thứ hai và cần ORM hỗ trợ map kết quả đúng cách, không phải lúc nào cũng có sẵn ở mọi ORM/ngôn ngữ. Eager load "quá tay" — tải trước mọi quan hệ có thể có, kể cả khi không dùng đến ở nhánh code cụ thể — lại lãng phí ngược, tải dữ liệu không cần thiết và làm chậm chính request đó; N+1 chỉ nên fix đúng chỗ bị lặp, không phải fix bằng cách eager load toàn bộ object graph mặc định.

## Best Practices

- Luôn khai báo eager loading tường minh tại nơi định nghĩa query gốc (`includes`, `select_related`, `JOIN FETCH`), không dựa vào lazy loading rồi hy vọng ORM tự tối ưu.
- Bật công cụ phát hiện N+1 tự động trong môi trường dev/test (Bullet, `django-debug-toolbar`, Hibernate `hbm2ddl` statistics, hoặc APM query-count tracking) để bắt lỗi trước khi merge, vì N+1 không lộ ra với dataset nhỏ.
- Dùng chiến lược batch/IN cho quan hệ 1-nhiều thay vì JOIN trực tiếp để tránh row explosion, chỉ dùng JOIN cho quan hệ 1-1/many-to-1.
- Theo dõi số lượng query mỗi request như một metric APM (vd. New Relic, Datadog APM query count per transaction), không chỉ theo dõi tổng latency — N+1 có thể ẩn sau một latency trung bình vẫn chấp nhận được.
- Với danh sách rất lớn hoặc quan hệ lồng nhiều tầng, cân nhắc denormalize (lưu sẵn dữ liệu tổng hợp) hoặc dùng dataloader pattern (batch + cache trong phạm vi một request) thay vì cố eager load toàn bộ cây quan hệ.

## Common Mistakes

- Chỉ test với 5-10 bản ghi mẫu ở local nên không bao giờ thấy N+1 gây ảnh hưởng thật, đến khi lên production với hàng nghìn bản ghi mới phát hiện qua incident.
- Eager load thừa — include toàn bộ quan hệ có thể có "cho chắc" ở mọi endpoint, kể cả nơi không dùng đến, làm tăng payload và thời gian query không cần thiết.
- Dùng JOIN cho quan hệ 1-nhiều mà không nhận ra nó nhân bản dữ liệu bảng chính, dẫn đến kết quả đúng nhưng tốn network/bộ nhớ gấp nhiều lần cần thiết.
- Fix N+1 ở tầng view/serializer thay vì tầng query gốc — vd. gọi `.includes()` sau khi đã lười tải một phần dữ liệu ở nhánh code khác, khiến vẫn còn N+1 rải rác không kiểm soát được.
- Không giám sát số lượng query trong CI/APM nên N+1 mới regressed lại sau một lần refactor không ai phát hiện cho đến khi khách hàng báo chậm.

## Interview Questions

**Hỏi**: Vì sao N+1 khó phát hiện trong quá trình phát triển nhưng lại nghiêm trọng ở production?

**Trả lời**: Vì code gây N+1 (truy cập quan hệ trong vòng lặp) đọc hoàn toàn tự nhiên và đúng logic nghiệp vụ, không có dấu hiệu bug rõ ràng khi review. Với N nhỏ (dataset dev/test), tổng thời gian vẫn nhanh nên không ai để ý; nhưng độ trễ tăng tuyến tính theo N, nên chỉ khi dữ liệu production đủ lớn (hàng nghìn bản ghi) vấn đề mới lộ rõ, lúc đó đã ảnh hưởng thật đến người dùng.

**Hỏi**: Khi nào nên dùng JOIN-based eager loading và khi nào nên dùng batch/IN-based?

**Trả lời**: JOIN-based phù hợp cho quan hệ 1-1 hoặc many-to-1 (mỗi dòng chính chỉ khớp với đúng một dòng quan hệ), vì không gây nhân bản dữ liệu. Batch/IN-based phù hợp hơn cho quan hệ 1-nhiều, vì JOIN trực tiếp sẽ lặp lại dòng bảng chính theo số lượng bản ghi quan hệ, gây lãng phí băng thông và bộ nhớ đáng kể khi số quan hệ lớn.

**Hỏi**: Eager load toàn bộ quan hệ ở mọi query có phải là best practice không?

**Trả lời**: Không. Eager load nên áp dụng đúng vào những quan hệ thực sự được dùng ở nhánh code đó. Eager load thừa tải về dữ liệu không cần thiết, làm chậm chính request đang cố tối ưu và lãng phí tài nguyên DB/network ngược lại với mục tiêu ban đầu.

## Summary

N+1 query xảy ra khi ORM chạy 1 query lấy danh sách rồi chạy thêm N query riêng lẻ để lấy quan hệ của từng bản ghi, do cơ chế lazy loading mặc định không biết trước toàn bộ object sắp được truy cập. Vấn đề khó phát hiện lúc code review hay test với dataset nhỏ, nhưng gây độ trễ tăng tuyến tính và cạn kiệt connection pool khi dữ liệu production đủ lớn. Giải pháp là eager loading tường minh, dùng JOIN cho quan hệ 1-1/many-to-1 và batch/IN query cho quan hệ 1-nhiều để tránh nhân bản dữ liệu. Cần cân bằng giữa việc fix đúng chỗ N+1 thật sự tồn tại và tránh eager load thừa gây lãng phí ngược. Giám sát số lượng query mỗi request bằng công cụ tự động (Bullet, APM query count) là cách phòng ngừa hiệu quả nhất, thay vì chỉ phát hiện qua sự cố.

## Knowledge Graph

- Covering Index — cả hai đều là kỹ thuật giảm số round-trip/IO không cần thiết khi truy vấn dữ liệu.
- Execution Plan — đọc execution plan giúp phát hiện các query lặp lại bất thường ẩn sau N+1.
- Deadlocks — cả hai đều là lớp lỗi hiệu năng/concurrency thường vô hình ở dev nhưng nổ ra dưới tải production thật.
- Connection Pool Exhaustion — N+1 là nguyên nhân phổ biến khiến pool bị giữ connection lâu bất thường, dẫn đến cạn kiệt.
- Dataloader Pattern — kỹ thuật batch + cache trong phạm vi một request, giải pháp phổ biến cho N+1 ở tầng GraphQL/API.
- Missing WHERE Clause — cả hai đều là lớp lỗi truy vấn xuất phát từ việc dùng ORM thiếu kiểm soát tường minh câu SQL sinh ra.

## Five Things To Remember

- N+1 là 1 query lấy list cộng thêm N query lấy quan hệ cho từng dòng, thay vì gộp thành 1-2 query.
- Vấn đề ẩn mình ở dev vì N nhỏ, chỉ lộ rõ khi dữ liệu production đủ lớn để độ trễ tăng tuyến tính rõ rệt.
- Eager loading (JOIN hoặc batch/IN) là giải pháp cốt lõi, khai báo tường minh tại query gốc chứ không dựa vào ORM tự tối ưu.
- Dùng JOIN cho quan hệ 1-1/many-to-1, dùng batch/IN cho quan hệ 1-nhiều để tránh nhân bản dữ liệu qua network.
- Giám sát số lượng query mỗi request bằng công cụ tự động là cách phòng ngừa đáng tin cậy hơn là chỉ phát hiện qua sự cố production.
