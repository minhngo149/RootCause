---
id: mongodb-where-operator
title: "The $where Operator"
tags: ["mongodb", "performance", "security"]
---

# The $where Operator

> Status: Draft

## Problem

`$where` cho phép nhúng một biểu thức JavaScript tùy ý vào query filter, chạy trên từng document để quyết định document đó có khớp hay không — vd. `db.orders.find({ $where: "this.total > this.limit" })`. Cú pháp trông giống một điều kiện lọc bình thường nên developer dùng nó như một "escape hatch" tiện lợi mỗi khi native query operator (`$gt`, `$expr`, aggregation pipeline) không biểu diễn được điều kiện mong muốn, đặc biệt là so sánh giữa hai field trong cùng document. Vấn đề là `$where` không được MongoDB xử lý như một filter thông thường — nó buộc engine phải thực thi một JavaScript interpreter cho từng document, và nếu chuỗi JavaScript đó được ghép từ input người dùng, nó trở thành một vector thực thi mã tùy ý ở phía server.

## Pain Points

- Luôn luôn là collection scan: `$where` không có cách nào để MongoDB dùng index, kể cả khi các field liên quan đã được index đầy đủ, vì engine phải load và deserialize từng document rồi chạy JS mới biết có khớp hay không.
- JavaScript execution chạy tuần tự trên một luồng duy nhất (V8/SpiderMonkey embedded), không tận dụng được nhiều lõi CPU như native query operator, nên throughput giảm mạnh trên collection lớn.
- NoSQL injection: nếu code build chuỗi `$where` bằng string concatenation từ input người dùng (vd. `"this.name == '" + input + "'"`), attacker có thể chèn JavaScript tùy ý — đọc toàn bộ dữ liệu collection khác trong cùng context, gây DoS bằng vòng lặp vô hạn, hoặc exfiltrate dữ liệu qua side-channel timing.
- Chi phí vận hành âm thầm tăng dần: một query `$where` chạy ổn ở collection vài nghìn document trong staging có thể trở thành query mất hàng chục giây khi collection production lên tới hàng triệu document, vì thời gian tăng tuyến tính theo số document chứ không được index cắt giảm.

## Solution

Giải pháp cốt lõi là loại bỏ hoàn toàn `$where` và thay bằng operator gốc của MongoDB. Với điều kiện so sánh giá trị đơn giản, dùng `$gt`, `$lt`, `$eq`, `$regex`... Với điều kiện cần so sánh giữa nhiều field trong cùng document — trường hợp phổ biến nhất khiến người ta chọn `$where` — dùng `$expr` (MongoDB 3.6+), cho phép viết aggregation expression ngay trong `find()`/`match` mà không cần chạy JavaScript, vd. `db.orders.find({ $expr: { $gt: ["$total", "$limit"] } })`.

## How It Works

Khi MongoDB gặp `$where`, query planner không thể đưa nó vào execution plan dạng index scan như các predicate khác — nó buộc phải rơi vào `COLLSCAN` (collection scan) rồi với mỗi document, server khởi tạo một scope JavaScript, bind `this` vào document hiện tại, rồi thực thi biểu thức thông qua engine JS nhúng sẵn trong `mongod`. Kết quả trả về (truthy/falsy) quyết định document có được giữ lại hay không. Vì bước này xảy ra sau khi đã đọc document từ storage engine, `$where` không thể kết hợp với bất kỳ index nào để giảm số document cần kiểm tra — kể cả khi query có thêm điều kiện khác trong cùng filter object, MongoDB chỉ dùng được index cho các điều kiện đó, còn `$where` vẫn phải chạy full scan trên tập kết quả trung gian.

`$expr` hoạt động khác về bản chất: nó dùng aggregation expression syntax (`$gt`, `$eq`, `$subtract`...) được biên dịch thành một dạng cây biểu thức mà query planner hiểu trực tiếp, không cần interpreter JS riêng. Với các bản MongoDB mới (4.2+), nếu biểu thức trong `$expr` có thể được rewrite thành một range tương đương trên field đã index (vd. so sánh field với một hằng số, dù lồng trong `$expr`), planner có thể tận dụng index; còn khi so sánh giữa hai field động với nhau thì vẫn cần scan, nhưng tối thiểu không phải trả chi phí khởi tạo JS scope cho từng document như `$where`.

## Production Architecture

Một hệ thống quản lý kho thường cần lọc các đơn hàng có `shipped_quantity` vượt quá `ordered_quantity` (dấu hiệu lỗi dữ liệu) — một điều kiện so sánh giữa hai field. Team ban đầu implement bằng `db.orders.find({ $where: "this.shipped_quantity > this.ordered_quantity" })`, chạy tốt trên vài chục nghìn đơn hàng ở giai đoạn đầu. Sau một năm, collection `orders` đạt 20 triệu document, và endpoint báo cáo dùng query này bắt đầu timeout, chiếm CPU của `mongod` hàng chục giây liên tục mỗi lần gọi, ảnh hưởng tới các query khác đang chờ cùng connection pool. Kiến trúc đúng thay `$where` bằng `$expr: { $gt: ["$shipped_quantity", "$ordered_quantity"] }` trong một aggregation `$match` stage, và với truy vấn API công khai chấp nhận điều kiện lọc từ người dùng, team enforce một allowlist các field/operator được phép build filter thay vì cho phép bất kỳ hình thức raw JavaScript nào chạm tới tầng database.

## Trade-offs

`$where` vẫn là cách duy nhất trong một số MongoDB phiên bản cũ (trước 3.6, không có `$expr`) để biểu diễn so sánh giữa các field, hoặc các logic phức tạp mà aggregation expression không hỗ trợ trực tiếp (vd. dùng regex kết hợp logic string phức tạp mà `$regexMatch` chưa xử lý được ở thời điểm đó). Nhưng cái giá phải trả — mất index, chạy tuần tự, và bề mặt injection — gần như luôn lớn hơn lợi ích linh hoạt đó trên production hiện đại, nơi hầu hết logic có thể diễn đạt lại bằng aggregation pipeline (`$expr`, `$function` có kiểm soát chặt hơn, hoặc xử lý ở tầng ứng dụng sau khi lấy về tập nhỏ hơn bằng filter native trước).

## Best Practices

- Cấm hoàn toàn `$where` trong code review và static analysis (như rule MONGO002) — hầu như không có use case production nào không thể thay bằng `$expr` hoặc aggregation pipeline trên MongoDB 3.6+.
- Không bao giờ build chuỗi JavaScript cho `$where` (hoặc cho `$function`, `mapReduce`) bằng string concatenation/interpolation từ input người dùng.
- Ưu tiên `$expr` cho mọi so sánh giữa các field trong cùng document, vì planner có cơ hội tối ưu tốt hơn interpreter JS.
- Nếu logic thực sự quá phức tạp cho aggregation expression, cân nhắc denormalize thêm một field tính sẵn (computed field) khi ghi dữ liệu, để query lúc đọc chỉ cần so sánh giá trị đơn giản có thể index được.
- Bật giám sát `COLLSCAN` qua `db.currentOp()`/profiler hoặc Atlas Performance Advisor để phát hiện sớm các query không dùng index, bao gồm cả những query ẩn `$where` được thêm sau này bởi một PR không qua review kỹ.

## Common Mistakes

- Dùng `$where` như giải pháp mặc định cho bất kỳ điều kiện lọc "hơi phức tạp" nào, thay vì thử `$expr`/aggregation pipeline trước.
- Ghép chuỗi input người dùng trực tiếp vào biểu thức `$where` mà không nhận ra đây là injection tương đương `eval()` phía server.
- Test query `$where` trên dataset nhỏ ở local/staging, không phát hiện ra vấn đề hiệu năng cho tới khi collection production đủ lớn.
- Tưởng rằng thêm index cho các field được so sánh trong `$where` sẽ giúp tăng tốc — thực tế `$where` không bao giờ dùng được index dù field có được đánh index hay không.
- Không giới hạn timeout/maxTimeMS cho các query có `$where`, khiến một query chậm có thể giữ resource lâu và ảnh hưởng dây chuyền tới các request khác.

## Interview Questions

**Hỏi**: Vì sao `$where` luôn dẫn đến collection scan, kể cả khi các field liên quan đã được index?

**Trả lời**: Vì MongoDB phải load từng document từ storage engine rồi chạy biểu thức JavaScript trên nó để biết có khớp hay không — bước đánh giá này xảy ra sau khi đọc document, không phải trong giai đoạn tra cứu index. Query planner không có cách nào biết trước document nào sẽ thỏa `$where` mà không thực sự chạy nó, nên buộc phải kiểm tra tuần tự toàn bộ collection.

**Hỏi**: Làm sao thay thế `$where` khi cần so sánh giữa hai field trong cùng document?

**Trả lời**: Dùng `$expr` với aggregation expression, vd. `{ $expr: { $gt: ["$fieldA", "$fieldB"] } }`. Cách này không chạy JavaScript, có sẵn từ MongoDB 3.6+, và trong một số trường hợp query planner còn có thể tận dụng index tốt hơn `$where`.

**Hỏi**: Tại sao `$where` được xem là rủi ro bảo mật nghiêm trọng hơn hầu hết các operator khác?

**Trả lời**: Vì nó thực thi JavaScript tùy ý ngay trên server database. Nếu chuỗi biểu thức được build từ input chưa sanitize, attacker có thể chèn logic JS để đọc dữ liệu ngoài phạm vi query dự kiến, gây DoS bằng vòng lặp tốn CPU, hoặc khai thác side-channel — mức độ nghiêm trọng tương đương SQL injection nhưng thực thi mã thay vì chỉ thay đổi câu truy vấn.

## Summary

`$where` cho phép chạy JavaScript tùy ý trên từng document để lọc kết quả, nhưng cái giá là mất hoàn toàn khả năng dùng index (luôn collection scan), thực thi tuần tự chậm hơn native operator, và mở ra rủi ro NoSQL injection nếu biểu thức được build từ input người dùng. Từ MongoDB 3.6 trở đi, `$expr` giải quyết được phần lớn lý do người ta từng cần `$where` — đặc biệt là so sánh giữa các field — mà không cần chạy JS và với cơ hội tối ưu tốt hơn. Best practice production là cấm `$where` bằng static analysis, không bao giờ ghép chuỗi input vào biểu thức JS, và giám sát `COLLSCAN` để bắt sớm các query chưa được tối ưu. Đánh đổi linh hoạt của `$where` gần như không còn đáng giá trên các phiên bản MongoDB hiện đại.

## Knowledge Graph

- Execution Plan — đọc execution plan là cách xác nhận trực tiếp một query `$where` đang chạy `COLLSCAN` thay vì index scan.
- Secondary Index — index không giúp ích gì cho `$where` vì bước đánh giá JS xảy ra sau khi đã đọc document.
- Missing WHERE Clause — cùng nhóm lỗi truy vấn nguy hiểm ở tầng NoSQL/SQL do thiếu kiểm soát tường minh điều kiện lọc.
- N+1 Query — cùng là lớp vấn đề hiệu năng ẩn ở dev/staging với dataset nhỏ, chỉ lộ rõ khi dữ liệu production đủ lớn.
- SQL Injection — cùng họ lỗ hổng injection do ghép chuỗi input người dùng trực tiếp vào ngôn ngữ truy vấn/thực thi phía server.

## Five Things To Remember

- `$where` chạy JavaScript trên từng document, nên luôn là collection scan bất kể index đã tạo.
- JavaScript execution trong `$where` chạy tuần tự, không song song hóa được như native operator.
- Ghép input người dùng vào chuỗi `$where` là NoSQL injection, tương đương thực thi mã tùy ý phía server.
- `$expr` (MongoDB 3.6+) thay thế được hầu hết use case của `$where`, đặc biệt so sánh giữa các field, mà không cần chạy JS.
- Cấm `$where` bằng static analysis và giám sát `COLLSCAN` là cách phòng ngừa hiệu quả hơn phát hiện qua sự cố production.
