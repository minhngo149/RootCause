---
id: deadlocks
title: Deadlocks
tags: ["database", "concurrency", "production-incident"]
---

# Deadlocks

> Status: Draft

## Problem

Hai transaction chạy song song, mỗi transaction đã khóa một tài nguyên và đang chờ khóa tài nguyên mà transaction kia đang giữ. Cả hai đứng yên vô thời hạn vì không bên nào chịu buông khóa trước — đây là deadlock. Nó không phải lỗi logic ứng dụng theo nghĩa thông thường, mà là hệ quả tất yếu khi nhiều transaction cùng khóa nhiều dòng theo thứ tự khác nhau.

## Pain Points

- Request bị treo (hang) cho đến khi DB timeout hoặc tự phát hiện và abort — với ứng dụng không xử lý retry, user nhận lỗi 500 ngẫu nhiên, khó tái hiện.
- Deadlock thường chỉ xuất hiện dưới tải cao hoặc trong race condition hiếm, nên qua code review và test bình thường không phát hiện được, chỉ nổ ra ở production.
- Transaction bị abort mất toàn bộ công việc đã làm (rollback), gây retry storm nếu nhiều request cùng lúc đụng deadlock và cùng retry ngay lập tức.
- Debug deadlock đòi hỏi đọc deadlock graph/log của DB, một kỹ năng ít engineer luyện tập cho đến khi gặp sự cố thật.

## Solution

Bản thân deadlock không thể ngăn hoàn toàn ở tầng DB vì nó phát sinh từ thứ tự khóa do ứng dụng quyết định. Giải pháp cốt lõi là **lock ordering**: đảm bảo mọi transaction luôn khóa các tài nguyên theo cùng một thứ tự cố định (vd. luôn theo `id` tăng dần), để không thể tồn tại một chu trình chờ khóa vòng tròn. Kết hợp với đó, ứng dụng phải coi deadlock là lỗi có thể retry an toàn, không phải lỗi nghiêm trọng cần alert khẩn cấp một lần.

## How It Works

DB dùng hai loại lock chính trong transaction: shared lock (đọc) và exclusive lock (ghi). Khi transaction A giữ exclusive lock trên dòng X và đang chờ lock trên dòng Y, trong khi transaction B giữ exclusive lock trên dòng Y và đang chờ lock trên dòng X, ta có một chu trình chờ (wait-for cycle). DB (PostgreSQL, MySQL/InnoDB, SQL Server...) duy trì một **wait-for graph** nội bộ giữa các transaction đang chờ lock. Định kỳ (hoặc ngay khi một transaction bắt đầu chờ), DB chạy thuật toán phát hiện chu trình (cycle detection) trên đồ thị này. Khi phát hiện chu trình, DB chọn một "victim" — thường là transaction có ít công việc rollback nhất hoặc mới bắt đầu gần đây nhất — và abort transaction đó với lỗi cụ thể (PostgreSQL: `ERROR: deadlock detected`, MySQL: `Error 1213: Deadlock found when trying to get lock`), rollback toàn bộ thay đổi của nó, giải phóng lock để transaction còn lại tiếp tục chạy bình thường. Đây khác với **lock timeout** (`lock_timeout`, `innodb_lock_wait_timeout`) — timeout xảy ra khi một transaction chờ lock quá lâu dù chưa chắc có chu trình thật sự, còn deadlock detection xác nhận chắc chắn có chu trình vòng tròn trước khi abort.

## Production Architecture

Trong một hệ thống order/payment (vd. e-commerce checkout), transaction A cập nhật `inventory` trước rồi `orders` sau, trong khi một job batch khác (vd. reconcile job) cập nhật `orders` trước rồi `inventory` sau — hai luồng code khác nhau, viết bởi hai người khác nhau, cùng động vào hai bảng nhưng ngược thứ tự. Dưới tải thấp không sao, nhưng khi traffic checkout tăng đúng lúc job batch chạy, deadlock xuất hiện với tần suất tỷ lệ thuận với concurrency. Kiến trúc production đúng đắn quy định: mọi transaction động đến nhiều bảng/dòng phải tuân theo một thứ tự khóa toàn cục (documented lock order, vd. luôn `accounts` → `orders` → `inventory`), và tầng application có retry middleware tự động bắt lỗi deadlock (SQLSTATE `40P01` ở Postgres, `1213` ở MySQL) để retry transaction với exponential backoff.

## Trade-offs

Lock ordering triệt để đòi hỏi kỷ luật toàn team — chỉ cần một đoạn code mới (migration script, job nền, admin tool) không tuân thủ thứ tự đã thống nhất là deadlock quay lại. Giảm kích thước transaction (khóa ít dòng hơn, giữ lock thời gian ngắn hơn) làm giảm khả năng deadlock nhưng có thể phải tách một nghiệp vụ atomic thành nhiều transaction nhỏ, đánh đổi tính nhất quán tức thời lấy khả năng chịu tải. Retry tự động khi deadlock giúp ứng dụng "tự lành" nhưng che giấu vấn đề gốc nếu không có logging/metric riêng — đội ngũ có thể sống chung với deadlock rate cao mà không biết vì user không thấy lỗi.

## Best Practices

- Thống nhất và document thứ tự khóa cố định cho mọi bảng hay bị động chạm cùng lúc, áp dụng cho cả code ứng dụng lẫn migration/batch job.
- Giữ transaction càng ngắn càng tốt — không gọi API bên ngoài, không xử lý logic nặng trong lúc transaction đang mở lock.
- Bắt riêng lỗi deadlock (theo SQLSTATE/error code cụ thể) và retry với backoff, không dùng try/catch chung chung nuốt lỗi.
- Dùng `SELECT ... FOR UPDATE` có chủ đích và theo đúng thứ tự đã quy định, tránh để ORM tự sinh thứ tự khóa không kiểm soát được.
- Theo dõi deadlock rate như một metric production (không chỉ log), vì rate tăng đột biến là tín hiệu sớm của contention đang xấu đi trước khi thành outage.

## Common Mistakes

- Bắt lỗi deadlock rồi retry ngay lập tức không backoff, gây retry storm làm deadlock rate tăng thêm thay vì giảm.
- Coi deadlock là bug hiếm gặp một lần rồi bỏ qua, không nhận ra nó là dấu hiệu của một thứ tự khóa không nhất quán sẽ tái diễn dưới tải cao hơn.
- Để ORM/framework tự động quyết định thứ tự các câu UPDATE trong transaction (vd. theo thứ tự khai báo association) mà không kiểm soát tường minh.
- Tăng lock timeout thật cao để "né" lỗi deadlock, khiến request treo lâu hơn thay vì fail nhanh và retry.
- Không phân biệt lock timeout với deadlock thật khi debug, dẫn đến chẩn đoán sai nguyên nhân gốc.

## Interview Questions

**Hỏi**: Deadlock khác gì với một transaction đơn giản chờ lock lâu (lock contention thông thường)?

**Trả lời**: Lock contention thông thường là một transaction chờ transaction khác giải phóng lock, và sẽ được tiếp tục ngay khi lock đó được giải phóng. Deadlock là một chu trình chờ vòng tròn giữa hai (hay nhiều) transaction, không transaction nào có thể tự giải phóng vì đang chờ lẫn nhau — DB phải chủ động can thiệp abort một bên thì phần còn lại mới chạy tiếp được.

**Hỏi**: Vì sao lock ordering giải quyết được deadlock về mặt lý thuyết?

**Trả lời**: Deadlock chỉ xảy ra khi tồn tại một chu trình trong wait-for graph. Nếu mọi transaction luôn xin khóa theo cùng một thứ tự toàn cục (vd. luôn tăng dần theo ID), không thể có hai transaction chờ lẫn nhau theo hai hướng ngược nhau — về mặt toán học, một thứ tự tuyến tính (total order) không thể sinh ra chu trình.

**Hỏi**: Ứng dụng nên xử lý thế nào khi nhận lỗi deadlock từ DB?

**Trả lời**: Bắt đúng mã lỗi deadlock (không bắt exception chung), rollback transaction hiện tại (thường DB đã tự rollback), sau đó retry toàn bộ transaction từ đầu với exponential backoff và giới hạn số lần retry, đồng thời log/metric lại để theo dõi tần suất.

## Summary

Deadlock xảy ra khi hai transaction khóa chéo nhau, mỗi bên giữ một lock mà bên kia cần và ngược lại, tạo thành chu trình chờ vòng tròn. DB phát hiện chu trình này qua wait-for graph và chủ động abort một transaction (victim) để giải phóng bế tắc, khác với lock timeout thông thường. Giải pháp phòng ngừa cốt lõi là lock ordering — buộc mọi transaction khóa tài nguyên theo cùng một thứ tự cố định để loại trừ khả năng hình thành chu trình. Ứng dụng production vẫn cần retry logic cho lỗi deadlock vì không thể loại trừ 100% trong hệ thống concurrency cao, và cần theo dõi deadlock rate như một chỉ báo sức khỏe hệ thống.

## Knowledge Graph

- Execution Plan — deadlock thường lộ ra khi đọc log/plan của các transaction đang chờ lock lẫn nhau.
- UPDATE/DELETE Without WHERE — cả hai đều là lớp lỗi liên quan đến quản lý transaction/khóa dữ liệu thiếu kỷ luật.
- Isolation Level — mức cô lập transaction càng cao (SERIALIZABLE) càng dễ gây conflict và deadlock hơn READ COMMITTED.
- Row-level Locking — deadlock là hệ quả trực tiếp của cơ chế khóa ở mức dòng khi nhiều transaction truy cập chéo.
- Retry Storm — retry deadlock không backoff hợp lý có thể tự gây ra một dạng retry storm.
- Two-Phase Locking (2PL) — mô hình lý thuyết giải thích vì sao transaction giữ lock đến cuối và có thể dẫn đến deadlock.

## Five Things To Remember

- Deadlock là chu trình chờ khóa vòng tròn giữa ít nhất hai transaction, không phải một transaction chờ lâu bình thường.
- DB tự phát hiện qua wait-for graph và tự abort một transaction (victim) để giải thoát các bên còn lại.
- Lock ordering nhất quán trên toàn hệ thống là cách phòng ngừa triệt để nhất, không phải retry.
- Luôn bắt riêng mã lỗi deadlock và retry có backoff, không nuốt lỗi hay retry ngay lập tức.
- Deadlock rate tăng là tín hiệu sớm của contention xấu đi, cần theo dõi như một metric production, không chỉ log khi có sự cố.
