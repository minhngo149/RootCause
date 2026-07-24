---
id: cache-aside
title: Cache Aside
tags: ["cache"]
---

# Cache Aside

> Status: Draft

## Problem
Một API đọc dữ liệu sản phẩm trực tiếp từ PostgreSQL cho mỗi request. Ở tải 50 req/s, mỗi query join 3 bảng mất 40ms, connection pool (max 100) bão hòa trong giờ cao điểm, request bắt đầu xếp hàng chờ connection và p99 latency vọt lên 2-3s. Đội ngũ engineer cần một lớp giảm tải cho DB mà không phải viết lại toàn bộ luồng đọc/ghi hiện có — đây là lý do cache-aside trở thành pattern mặc định khi thêm cache vào một hệ thống đã chạy production.

## Pain Points
- DB trở thành single point of failure về performance: một query chậm lặp lại hàng nghìn lần/giây gây connection pool exhaustion, kéo theo timeout dây chuyền sang các service khác dùng chung DB.
- Chi phí vận hành tăng: phải scale DB instance (vertical) hoặc thêm read replica (horizontal) chỉ để phục vụ lại cùng một dữ liệu ít thay đổi, trong khi cache trên RAM rẻ hơn nhiều lần cho cùng throughput.
- Không có cache-aside, mỗi team tự chế cơ chế cache riêng (biến static, cache trong process) dẫn đến dữ liệu cũ (stale) không đồng bộ giữa các instance khi scale ngang.
- Khi outage DB xảy ra, không có lớp cache đứng giữa nghĩa là toàn bộ traffic đọc dội thẳng vào DB đang phục hồi, gây thundering herd làm DB sập lần hai ngay sau khi vừa lên lại.

## Solution
Cache-aside (còn gọi là lazy loading) là pattern trong đó ứng dụng — không phải cache hay DB — chịu trách nhiệm quản lý luồng dữ liệu: đọc cache trước, nếu miss thì đọc DB, sau đó ghi kết quả vào cache trước khi trả về client. Cache chỉ chứa những gì thực sự được truy vấn (lazy population), khác với các pattern chủ động nạp trước như read-through hay write-through. Đây là pattern phổ biến nhất vì tương thích với gần như mọi loại cache (Redis, Memcached) và mọi loại DB mà không cần thay đổi schema hay driver.

## How It Works
Luồng đọc gồm 4 bước tuần tự trong code ứng dụng: (1) tạo cache key theo convention rõ ràng, ví dụ `product:{id}`; (2) gọi `GET` tới cache — nếu hit, deserialize và trả về ngay, bỏ qua DB hoàn toàn; (3) nếu miss, query DB, và (4) `SET` kết quả vào cache kèm TTL (ví dụ 300s) rồi mới trả về client. Luồng ghi (update/delete) không đi qua cache mà ghi thẳng vào DB, sau đó ứng dụng chủ động xóa (invalidate) cache key liên quan — không update cache trực tiếp — để lần đọc kế tiếp tự nạp lại giá trị mới nhất từ DB (đảm bảo tính nhất quán cao hơn so với việc set giá trị mới ngay tại chỗ, vốn dễ race condition với một write khác đang chạy song song). TTL là cơ chế tự phục hồi (self-healing): nếu bước invalidate bị bỏ sót hoặc thất bại, cache entry vẫn tự hết hạn sau một khoảng thời gian xác định thay vì stale vĩnh viễn. Vì bước đọc-rồi-ghi không atomic, có một race window kinh điển: request A đọc DB (thấy giá trị cũ) đúng lúc request B vừa update DB và invalidate cache xong, sau đó A ghi giá trị cũ vào cache — cache stale cho tới TTL tiếp theo.

## Production Architecture
Trong một API thương mại điện tử, tầng service gọi `cache.get(key)` trước khi chạm tới repository layer; Redis được đặt cùng region với ứng dụng để giữ round-trip dưới 1ms, còn PostgreSQL primary chỉ nhận request khi cache miss. Endpoint lấy thông tin sản phẩm dùng key `product:{sku}` với TTL 5-10 phút; endpoint session/giỏ hàng dùng TTL ngắn hơn (30-60s) vì dữ liệu thay đổi thường xuyên hơn. Khi có update sản phẩm từ trang admin, service phát một lệnh `DEL product:{sku}` (hoặc publish sự kiện invalidation qua message queue nếu có nhiều instance/service cùng cache dữ liệu đó) ngay sau khi transaction DB commit thành công, không phải trước. Để chống thundering herd khi cache toàn cục hết hạn cùng lúc (ví dụ sau khi restart Redis), nhiều hệ thống thêm jitter ngẫu nhiên vào TTL (300s ± 30s) để các key không đồng loạt expire tại cùng một thời điểm.

## Trade-offs
Cache-aside đánh đổi tính nhất quán để lấy hiệu năng: luôn tồn tại một cửa sổ thời gian (tối đa bằng TTL, hoặc ngắn hơn nếu invalidation hoạt động đúng) mà client có thể đọc dữ liệu cũ. Request đầu tiên sau mỗi lần miss/expire luôn chịu full latency của DB (cache penalty), không có cơ chế nào loại bỏ hoàn toàn độ trễ này trừ khi kết hợp thêm warming hoặc refresh-ahead. Logic đọc/ghi/invalidate nằm rải rác trong code ứng dụng thay vì tập trung một chỗ, nghĩa là mỗi service/team phải tự implement đúng và dễ quên bước invalidate ở một trong nhiều đường ghi dữ liệu. Ngoài ra pattern này không tự bảo vệ khỏi cache stampede: nếu một key nóng (hot key) expire đúng lúc traffic cao, hàng trăm request cùng miss và cùng dội vào DB tại cùng một thời điểm.

## Best Practices
- Luôn set TTL cho mọi cache entry, kể cả khi có invalidation chủ động — TTL là lưới an toàn cuối cùng chống stale data vĩnh viễn.
- Invalidate cache sau khi DB commit thành công, không phải trước hoặc song song, để tránh cache bị nạp lại giá trị cũ ngay sau đó bởi một reader khác.
- Thêm jitter ngẫu nhiên (±10-20%) vào TTL để tránh nhiều key cùng expire tại một thời điểm gây cache stampede.
- Dùng lock (mutex/distributed lock) hoặc kỹ thuật "request coalescing" cho các key cực nóng để chỉ một request đi query DB khi miss, các request khác chờ kết quả thay vì cùng query DB.
- Log và alert riêng cache hit ratio theo từng nhóm key — hit ratio tụt đột ngột thường là dấu hiệu sớm của incident (cache bị flush, key pattern đổi, hoặc TTL cấu hình sai).

## Common Mistakes
- Update giá trị mới trực tiếp vào cache tại thời điểm ghi DB thay vì invalidate (xóa) — dễ dính race condition khi có nhiều writer đồng thời, dẫn tới cache lưu giá trị sai lâu dài.
- Không set TTL, cho rằng invalidation logic sẽ luôn bắt hết mọi đường ghi dữ liệu — thực tế luôn có một đường ghi bị bỏ sót (batch job, migration, thao tác trực tiếp trên DB).
- Cache cả kết quả lỗi hoặc giá trị null mà không có TTL riêng ngắn hơn, khiến một lỗi tạm thời (DB timeout) bị "đóng băng" thành lỗi vĩnh viễn cho tới khi cache tự hết hạn.
- Dùng cache key không có namespace/version, nên khi đổi schema dữ liệu, các client cũ và mới đọc chung một key và deserialize sai hoặc crash.
- Không xử lý cache miss hàng loạt sau khi restart Redis hoặc deploy — toàn bộ traffic dội thẳng vào DB cùng lúc mà không có cơ chế warming hay rate-limit tạm thời.

## Interview Questions
**Hỏi**: Cache-aside khác gì với write-through cache?
**Trả lời**: Cache-aside để ứng dụng tự quản lý việc đọc/ghi cache (lazy, chỉ cache khi có request thực sự và miss); write-through ghi đồng thời vào cache và DB trong cùng một thao tác ghi, do đó cache luôn có dữ liệu mới nhất ngay sau khi ghi nhưng tốn chi phí ghi cache cho cả những dữ liệu không bao giờ được đọc lại.

**Hỏi**: Tại sao nên invalidate (xóa) cache thay vì update giá trị mới khi dữ liệu DB thay đổi?
**Trả lời**: Vì update trực tiếp dễ dính race condition — nếu có hai write gần như đồng thời, write chậm hơn có thể ghi đè cache bằng giá trị cũ hơn write nhanh; xóa cache buộc lần đọc kế tiếp phải lấy lại giá trị mới nhất từ DB, đơn giản và an toàn hơn.

**Hỏi**: Cache stampede là gì và cache-aside có tự chống được không?
**Trả lời**: Cache stampede xảy ra khi một key nóng hết hạn và nhiều request cùng lúc miss, cùng dội vào DB để tính lại giá trị. Cache-aside tự thân không chống được hiện tượng này; cần bổ sung kỹ thuật như lock/request-coalescing, TTL jitter, hoặc refresh-ahead để giảm thiểu.

## Summary
Cache-aside là pattern cache phổ biến nhất: ứng dụng đọc cache trước, miss thì đọc DB rồi ghi lại vào cache, còn ghi dữ liệu thì đi thẳng vào DB kèm invalidate cache liên quan. Ưu điểm là đơn giản, tương thích với hầu hết cache/DB sẵn có và chỉ cache đúng dữ liệu thực sự được truy vấn. Đánh đổi chính là một cửa sổ thời gian có thể đọc dữ liệu cũ và penalty latency ở lần miss đầu tiên. Production system cần bổ sung TTL jitter, request coalescing và giám sát hit ratio để pattern này vận hành ổn định ở tải cao. Hiểu đúng thứ tự invalidate-sau-commit là yếu tố quyết định giữa một cache đáng tin cậy và một cache âm thầm trả dữ liệu sai.

## Knowledge Graph
- Write-Through Cache — pattern thay thế, ghi đồng bộ cache và DB tại thời điểm write thay vì lazy load khi đọc.
- Cache Stampede / Thundering Herd — rủi ro vận hành trực tiếp phát sinh từ TTL đồng loạt hết hạn trong cache-aside.
- Cache Invalidation — cơ chế lõi quyết định tính đúng đắn của cache-aside khi dữ liệu nguồn thay đổi.
- Read Replica — giải pháp thay thế/bổ sung để giảm tải DB ở tầng đọc, thường dùng song song với cache-aside.
- Distributed Lock — công cụ dùng để hiện thực request coalescing, ngăn nhiều request cùng query DB khi cache miss.
- TTL (Time To Live) — tham số cấu hình chống stale data, quyết định độ trễ tối đa của tính nhất quán trong pattern này.

## Five Things To Remember
- Đọc cache trước, miss mới đọc DB rồi ghi ngược lại cache.
- Ghi dữ liệu thì invalidate cache sau khi DB commit, không update cache trực tiếp.
- Luôn set TTL kể cả khi đã có invalidation chủ động.
- Thêm jitter vào TTL để tránh nhiều key cùng hết hạn một lúc.
- Cache-aside không tự chống cache stampede, cần lock hoặc request coalescing bổ sung.
