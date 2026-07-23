---
id: mongodb-where-operator
title: The $where Operator
tags: [mongodb, performance, security]
---

# The $where Operator

## Concept

`$where` cho phép nhúng một biểu thức JavaScript chạy trên từng document để lọc kết quả. Linh hoạt, nhưng đánh đổi nhiều về hiệu năng và an toàn.

## Vì sao quan trọng

- **Không dùng được index**: MongoDB phải deserialize và chạy JavaScript cho từng document trong collection — luôn là collection scan, bất kể index đã tạo.
- **Không chạy song song được**: JavaScript execution trong `$where` chạy tuần tự, không tận dụng nhiều lõi CPU như các operator gốc.
- **Rủi ro injection**: nếu chuỗi JavaScript được build bằng cách nối chuỗi từ input người dùng, đây là NoSQL injection — thực thi mã JS ngay phía server.

## Trade-off

`$where` đôi khi là cách duy nhất để biểu diễn một điều kiện lọc so sánh giữa các field với nhau mà operator gốc không hỗ trợ trực tiếp. Nên ưu tiên `$expr` (MongoDB 3.6+) trước, vì nó làm được việc tương tự mà không cần chạy JavaScript.

## Production Example

Một API tìm kiếm cho phép người dùng nhập điều kiện lọc, rồi build chuỗi JavaScript kiểu `this.name == '<input>'` để đưa vào `$where` — vừa chậm (collection scan) vừa là lỗ hổng injection nghiêm trọng nếu `input` chưa được sanitize.

## Interview

**Hỏi**: Khi nào nên dùng `$expr` thay vì `$where`?

**Trả lời**: Khi cần so sánh giữa các field trong cùng một document — điều mà query filter thông thường không hỗ trợ. `$expr` dùng aggregation expression thay vì JavaScript nên vẫn có thể tận dụng index trong nhiều trường hợp và không có rủi ro injection giống `$where`.
