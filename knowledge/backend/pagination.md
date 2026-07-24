---
id: pagination
title: Pagination (Offset vs Cursor)
tags: ["backend", "database", "performance"]
---

# Pagination (Offset vs Cursor)

> Status: Draft

## Problem

Một API danh sách (`GET /orders?page=3&limit=20`) thường được implement bằng `OFFSET`/`LIMIT` vì đây là cách trực quan nhất để "nhảy tới trang N": `SELECT * FROM orders ORDER BY created_at DESC LIMIT 20 OFFSET 40`. Cách này chạy đúng và nhanh khi bảng còn nhỏ hoặc offset còn thấp, nên nó lọt qua code review và test mà không ai đặt câu hỏi. Vấn đề chỉ lộ ra khi dữ liệu production tích lũy đủ lớn và người dùng (hoặc bot crawl) cuộn sâu tới những trang có offset hàng trăm nghìn — lúc đó mỗi request trở nên chậm hẳn, không phải vì bảng thiếu index mà vì bản chất cơ chế `OFFSET` buộc DB phải quét và bỏ qua toàn bộ số dòng đứng trước.

## Pain Points

- Latency của endpoint phân trang tăng gần tuyến tính theo giá trị offset — trang 5 nhanh, trang 5.000 có thể chậm gấp hàng trăm lần dù `LIMIT` không đổi, vì DB vẫn phải duyệt qua toàn bộ các dòng bị bỏ qua trước khi trả về `LIMIT` dòng cuối.
- Dưới tải cao, các query `OFFSET` lớn chiếm dụng CPU và I/O của DB lâu hơn bình thường, làm chậm luôn cả các query khác đang chạy song song — một endpoint phân trang tưởng chừng vô hại có thể kéo cả cluster DB xuống trong giờ cao điểm nếu bị crawler hoặc client lỗi request liên tục các trang sâu.
- Offset pagination không ổn định khi dữ liệu thay đổi giữa các lần gọi (insert/delete xảy ra giữa lúc user load trang 1 và trang 2): kết quả có thể bị trùng lặp dòng hoặc bỏ sót dòng, gây ra bug hiển thị khó tái hiện và khó debug vì không có lỗi rõ ràng ở tầng code.
- Chi phí vận hành tăng theo thời gian: một bảng feed/audit-log tăng trưởng liên tục sẽ khiến các trang cuối (thường là dữ liệu cũ nhất, ít người xem nhưng vẫn được cào bởi export job hoặc bot) ngày càng chậm, và nếu không nhận ra sớm, việc sửa sau này đòi hỏi đổi API contract, ảnh hưởng tới mọi client đang dùng `page`/`offset`.

## Solution

Giải pháp cốt lõi là **cursor pagination** (còn gọi là **keyset pagination**): thay vì yêu cầu DB đếm và bỏ qua N dòng đầu, client gửi lên một "con trỏ" (cursor) là giá trị của cột sắp xếp tại dòng cuối cùng đã nhận được, và query tiếp theo lọc trực tiếp bằng điều kiện so sánh (`WHERE created_at < :cursor ORDER BY created_at DESC LIMIT 20`) thay vì `OFFSET`. Nhờ vậy DB tận dụng được index trên cột sắp xếp để nhảy thẳng tới vị trí cần, độ phức tạp gần như hằng số bất kể đang ở "trang" nào, không phụ thuộc vào việc đã bỏ qua bao nhiêu dòng trước đó.

## How It Works

Với offset pagination, kế hoạch thực thi (execution plan) của `LIMIT 20 OFFSET 40000` trên phần lớn RDBMS (PostgreSQL, MySQL) vẫn phải duyệt tuần tự qua ít nhất 40.020 dòng theo thứ tự sắp xếp rồi mới loại bỏ 40.000 dòng đầu để trả về 20 dòng cuối — kể cả khi cột `ORDER BY` đã có index, DB vẫn tốn công đếm/bỏ qua từng dòng (index chỉ giúp tránh sort chứ không giúp "nhảy" tới offset). Độ phức tạp vì vậy là O(offset + limit), tăng tuyến tính theo offset.

Cursor/keyset pagination loại bỏ hoàn toàn bước đếm-bỏ-qua này bằng cách biến "vị trí trang" thành một điều kiện `WHERE` trực tiếp trên giá trị dữ liệu. Nếu sắp xếp theo `created_at DESC`, cursor là giá trị `created_at` (thường kèm `id` để đảm bảo thứ tự duy nhất khi có nhiều dòng trùng timestamp) của dòng cuối cùng đã trả về ở trang trước: `WHERE (created_at, id) < (:cursor_created_at, :cursor_id) ORDER BY created_at DESC, id DESC LIMIT 20`. Với một B-Tree index đúng trên `(created_at, id)`, DB dùng index seek để nhảy thẳng tới vị trí thỏa điều kiện rồi đọc tiếp 20 dòng liền kề — độ phức tạp là O(limit), không phụ thuộc vào việc đã có bao nhiêu dòng trước cursor. Cursor thường được encode (base64 hoặc opaque token) để client không tự ý sửa hoặc suy luận ra cấu trúc dữ liệu nội bộ, và để dễ đổi cột sắp xếp về sau mà không phá vỡ API contract.

Điểm khác biệt quan trọng là cursor pagination không hỗ trợ "nhảy tới trang N" tùy ý (vd. nhảy thẳng tới trang 500) vì không có khái niệm "trang" — nó chỉ hỗ trợ điều hướng tuần tự (next/previous) từ một vị trí đã biết, nên phù hợp với infinite scroll, feed, API pull dữ liệu tuần tự hơn là UI phân trang kiểu "1 2 3 ... 500" truyền thống.

## Production Architecture

Trong một hệ thống feed hoạt động (activity feed, notification list, audit log) với bảng hàng chục triệu dòng tăng liên tục, endpoint `GET /feed?cursor=xxx&limit=20` là dạng phổ biến nhất: client lưu cursor trả về từ response trước (thường là trường `next_cursor` chứa `(created_at, id)` đã encode) và gửi lại ở lần gọi kế tiếp. Kiến trúc thường tách riêng hai use case: các trang UI cần hiển thị số trang cụ thể và tổng số kết quả (vd. trang quản trị nội bộ, danh sách đơn hàng có ít bản ghi và cần "nhảy trang") vẫn dùng offset vì trải nghiệm đó đòi hỏi random access; còn các luồng dữ liệu lớn, tăng liên tục, hoặc API công khai cho third-party tích hợp (webhook backfill, export API, mobile feed infinite-scroll) chuyển hẳn sang cursor để tránh degrade theo thời gian. Nhiều API công khai lớn (Stripe, GitHub, Twitter/X) dùng cursor-based pagination làm chuẩn mặc định cho chính lý do này — dữ liệu của họ tăng liên tục và client không kiểm soát được offset sẽ lớn tới đâu.

## Trade-offs

Offset pagination hỗ trợ nhảy tới bất kỳ trang nào và hiển thị tổng số trang/tổng số kết quả dễ dàng (`SELECT COUNT(*)` một lần), điều mà cursor pagination không làm được một cách tự nhiên — muốn biết tổng số kết quả vẫn phải chạy `COUNT` riêng, tốn kém tương tự vấn đề gốc nếu bảng lớn. Cursor pagination đánh đổi tính linh hoạt điều hướng để lấy hiệu năng ổn định: nó chỉ tốt cho next/previous tuần tự, không hỗ trợ "nhảy thẳng trang 50", nên không thay thế được offset trong mọi UI (bảng dữ liệu quản trị cần đánh số trang rõ ràng vẫn cần offset hoặc giải pháp lai). Cursor cũng đòi hỏi cột sắp xếp phải có tính duy nhất/ổn định (kèm tie-breaker như `id`) và index đúng thứ tự composite, thiết kế sai một trong hai điều này sẽ gây trùng lặp hoặc bỏ sót dòng y hệt lỗi mà cursor vốn được sinh ra để tránh. Ngoài ra cursor pagination phức tạp hơn để implement và debug — offset debug bằng cách nhìn số `page`/`offset` trực quan, còn cursor là một token encode khó đọc bằng mắt khi trace log.

## Best Practices

- Dùng cursor/keyset pagination cho mọi bảng tăng trưởng liên tục và không cần random access tới trang cụ thể (feed, log, API export, infinite scroll).
- Luôn thêm tie-breaker duy nhất (thường là primary key) vào điều kiện sắp xếp và cursor (`ORDER BY created_at DESC, id DESC`) để tránh trùng/sót dòng khi có nhiều bản ghi cùng giá trị cột sắp xếp chính.
- Tạo composite index đúng thứ tự với `ORDER BY` trong query cursor (`(created_at, id)`), nếu không index sẽ không được tận dụng và cursor pagination mất hết lợi thế hiệu năng.
- Encode cursor thành opaque token (base64 hoặc JWT-lite) thay vì để client thấy trực tiếp giá trị cột — vừa tránh lộ cấu trúc dữ liệu nội bộ, vừa dễ đổi schema sau này.
- Với offset pagination bắt buộc phải giữ (vd. UI admin cần đánh số trang), giới hạn cứng số trang tối đa có thể truy cập hoặc cảnh báo/chặn khi offset vượt ngưỡng để tránh một request đơn lẻ kéo chậm cả DB.

## Common Mistakes

- Dùng `OFFSET` cho bảng tăng trưởng không giới hạn (feed, log) mà không đo latency ở offset lớn trước khi lên production, chỉ phát hiện khi khách hàng hoặc bot crawl chạm tới trang sâu.
- Thiết kế cursor pagination thiếu tie-breaker duy nhất, dẫn đến trùng lặp hoặc bỏ sót dòng khi nhiều bản ghi có cùng giá trị cột sắp xếp (vd. nhiều đơn hàng tạo cùng millisecond).
- Tạo index sai thứ tự hoặc thiếu index composite cho cột dùng trong cursor, khiến DB vẫn phải full scan dù đã chuyển sang cursor-based query.
- Để client tự truyền giá trị cột thô làm cursor (không encode), vừa lộ chi tiết schema nội bộ vừa khiến việc đổi cột sắp xếp sau này phá vỡ mọi client đang tích hợp.
- Trộn lẫn offset và cursor trong cùng một API mà không tài liệu hóa rõ ràng, khiến client dùng sai cách (vd. vừa truyền `page` vừa truyền `cursor`) gây kết quả không xác định.

## Interview Questions

**Hỏi**: Vì sao `OFFSET` càng lớn thì query càng chậm, ngay cả khi cột `ORDER BY` đã có index?

**Trả lời**: Vì index giúp DB tránh phải sort dữ liệu, nhưng không giúp DB "nhảy" trực tiếp tới dòng thứ N — DB vẫn phải duyệt tuần tự qua toàn bộ offset dòng đứng trước rồi mới bỏ qua chúng để lấy `LIMIT` dòng tiếp theo. Độ phức tạp là O(offset + limit), nên offset càng lớn, số dòng phải duyệt và bỏ qua càng nhiều, latency tăng gần tuyến tính.

**Hỏi**: Tại sao cursor pagination cần thêm tie-breaker như primary key vào điều kiện sắp xếp, thay vì chỉ dùng một cột như `created_at`?

**Trả lời**: Vì nếu nhiều dòng có cùng giá trị `created_at`, chỉ dùng một cột này làm điều kiện `WHERE created_at < :cursor` có thể bỏ sót hoặc lặp lại các dòng cùng giá trị đó giữa hai lần gọi liên tiếp. Thêm `id` (hoặc cột duy nhất khác) làm tie-breaker đảm bảo thứ tự sắp xếp là duy nhất và ổn định tuyệt đối, tránh tình trạng dữ liệu không nhất quán giữa các trang.

**Hỏi**: Khi nào nên giữ offset pagination thay vì chuyển hẳn sang cursor?

**Trả lời**: Khi UI cần hỗ trợ random access tới trang cụ thể (nhảy thẳng "trang 50") hoặc hiển thị tổng số trang/tổng kết quả, và bảng dữ liệu đủ nhỏ hoặc có giới hạn rõ ràng (vd. trang quản trị nội bộ, danh sách ít khi vượt vài nghìn dòng) khiến vấn đề offset lớn không thực sự xảy ra trong thực tế.

## Summary

Offset pagination (`LIMIT`/`OFFSET`) đơn giản và hỗ trợ nhảy trang tùy ý, nhưng độ phức tạp tăng tuyến tính theo giá trị offset vì DB phải duyệt và bỏ qua toàn bộ số dòng đứng trước, gây chậm rõ rệt khi dữ liệu production đủ lớn. Cursor/keyset pagination giải quyết vấn đề này bằng cách biến vị trí trang thành điều kiện `WHERE` trực tiếp trên cột sắp xếp có index, cho phép DB seek thẳng tới vị trí cần với độ phức tạp gần như hằng số. Đánh đổi là cursor không hỗ trợ random access tới trang cụ thể và phức tạp hơn để implement đúng, đặc biệt cần tie-breaker duy nhất và index composite chính xác. Trong thực tế, hệ thống production thường dùng cả hai: offset cho các trang UI cần đánh số và nhảy trang trên dữ liệu giới hạn, cursor cho feed/log/API tăng trưởng liên tục. Việc chọn sai chiến lược ngay từ đầu khiến việc sửa sau này tốn kém vì phải đổi API contract ảnh hưởng tới mọi client đang tích hợp.

## Knowledge Graph

- Composite Index — cursor pagination chỉ đạt hiệu năng O(limit) khi có composite index đúng thứ tự với điều kiện cursor.
- Covering Index — kết hợp với cursor pagination giúp query trả kết quả mà không cần truy cập bảng chính, tối ưu thêm độ trễ.
- Execution Plan — đọc execution plan là cách xác nhận offset lớn đang gây full scan/duyệt tuần tự thay vì index seek.
- Sharding — hệ thống sharded cần thiết kế cursor cẩn thận hơn vì thứ tự toàn cục giữa các shard không tự nhiên nhất quán.
- N+1 Query — cùng thuộc nhóm vấn đề hiệu năng ẩn ở dev vì dataset nhỏ, chỉ lộ rõ khi dữ liệu production đủ lớn.
- Read Replica — offset pagination trên replica có độ trễ replication có thể gây trùng/sót dòng nghiêm trọng hơn do dữ liệu thay đổi giữa các lần đọc.

## Five Things To Remember

- `OFFSET` buộc DB duyệt và bỏ qua toàn bộ dòng đứng trước, nên độ phức tạp tăng tuyến tính theo offset dù cột sắp xếp đã có index.
- Cursor/keyset pagination biến vị trí trang thành điều kiện `WHERE` trên cột có index, đạt độ phức tạp gần như hằng số bất kể "trang" nào.
- Cursor luôn cần tie-breaker duy nhất (thường là primary key) để tránh trùng lặp hoặc bỏ sót dòng khi giá trị cột sắp xếp trùng nhau.
- Cursor pagination đánh đổi khả năng nhảy trang tùy ý và đếm tổng kết quả để lấy hiệu năng ổn định theo thời gian.
- Dùng offset cho UI cần đánh số trang trên dữ liệu giới hạn, dùng cursor cho feed/log/API tăng trưởng liên tục không kiểm soát được offset.
