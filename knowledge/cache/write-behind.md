---
id: write-behind
title: Write-Behind (Write-Back)
tags: ["cache"]
---

# Write-Behind (Write-Back)

> Status: Draft

## Problem
Một hệ thống ghi log sự kiện (event tracking) nhận 20.000 write/s từ client, mỗi write là một INSERT vào PostgreSQL. Ở write-through hay ghi thẳng DB, mỗi request phải chờ transaction commit (fsync xuống disk) trước khi trả response, khiến p99 latency ghi lên tới 80-150ms và DB connection pool bão hòa trước cả khi CPU DB chạm 50%. Đội ngũ engineer cần một cách ghi nhanh ở tầng ứng dụng mà không bắt client chờ toàn bộ chi phí ghi DB đồng bộ — đây là động lực để dùng write-behind, chấp nhận đánh đổi rủi ro để đổi lấy write latency thấp gần như hằng số.

## Pain Points
- Write latency bị chặn bởi disk I/O và transaction commit của DB; ở tải cao, p99 latency ghi tăng phi tuyến khi connection pool và lock contention bắt đầu xếp hàng.
- DB phải xử lý từng write riêng lẻ dù nhiều write liên tiếp có thể gộp lại (ví dụ update cùng một counter nhiều lần/giây) — lãng phí I/O và tăng write amplification.
- Không có lớp đệm ghi, mọi spike traffic đột biến (viral event, batch job) dội thẳng vào DB, dễ gây connection exhaustion hoặc lock timeout dây chuyền sang các query khác.
- Chi phí vận hành tăng vì phải scale DB (thêm write capacity, IOPS cao hơn) chỉ để đáp ứng đúng lúc traffic đỉnh, dù average load thấp hơn nhiều.

## Solution
Write-behind (hay write-back) là pattern ghi dữ liệu trước vào cache và trả response ngay cho client, sau đó một tiến trình nền (background worker) bất đồng bộ đẩy dữ liệu này xuống DB theo lô hoặc theo khoảng thời gian định kỳ. Khác với write-through — nơi cache và DB được ghi đồng bộ trong cùng request — write-behind tách rời hoàn toàn thời điểm client nhận response khỏi thời điểm dữ liệu thực sự persist xuống storage bền vững. Đổi lại tốc độ ghi cực nhanh (chỉ phụ thuộc round-trip tới cache, thường dưới 1ms), hệ thống chấp nhận một cửa sổ rủi ro: nếu cache/worker crash trước khi flush, dữ liệu trong cửa sổ đó mất vĩnh viễn.

## How It Works
Luồng ghi gồm hai giai đoạn tách biệt hoàn toàn về thời gian. Giai đoạn 1 (đồng bộ, trong request): ứng dụng ghi giá trị vào cache (hoặc một write buffer/queue trong bộ nhớ) và trả response thành công ngay lập tức — DB hoàn toàn không tham gia vào đường dẫn này. Giai đoạn 2 (bất đồng bộ, nền): một worker định kỳ (theo thời gian, ví dụ mỗi 500ms-5s, hoặc theo kích thước buffer, ví dụ khi tích lũy đủ 1000 record) đọc các thay đổi đã tích lũy, gộp (coalesce) các write trùng key thành một write cuối cùng, rồi flush xuống DB bằng batch INSERT/UPDATE để giảm số round-trip và tận dụng transaction hiệu quả hơn. Việc gộp write là điểm khác biệt cốt lõi so với write-through: nếu cùng một counter được tăng 100 lần trong 1 giây, write-behind chỉ cần 1 write DB cuối cùng phản ánh giá trị sau cùng, thay vì 100 write riêng lẻ. Cơ chế theo dõi "dirty" (đã đổi nhưng chưa flush) thường dùng một queue hoặc bitmap đánh dấu key nào cần ghi, và worker phải xử lý retry/backoff khi DB tạm thời không sẵn sàng — nếu retry liên tục thất bại, dữ liệu dirty tích lũy trong cache có thể vượt quá dung lượng cache hoặc TTL, dẫn tới mất dữ liệu nếu không có cơ chế durable buffer (ví dụ ghi trước vào WAL/queue message broker thay vì chỉ giữ trong RAM cache).

## Production Architecture
Trong một hệ thống tracking sự kiện quảng cáo, tầng ứng dụng ghi mỗi click/impression vào Redis dưới dạng list hoặc stream (`XADD events:buffer`), trả response cho client trong vài mili-giây. Một worker riêng (chạy như cron job hoặc consumer liên tục) đọc từ Redis Stream theo batch 500-2000 record mỗi 1-2 giây, transform và bulk-insert vào ClickHouse hoặc PostgreSQL bằng `COPY`/multi-row INSERT. Để giảm rủi ro mất dữ liệu khi Redis crash, hệ thống production nghiêm túc không dùng cache thuần RAM làm buffer duy nhất mà đặt một message broker có persistence (Kafka, hoặc Redis với AOF fsync mỗi giây) làm lớp trung gian giữa client và DB — cache trong trường hợp này thực chất là một durable queue chứ không phải cache in-memory đơn thuần. Một use case khác phổ biến hơn là bộ đếm (view count, like count): ứng dụng tăng counter trong Redis (`INCR`) ngay lập tức, và một worker định kỳ mỗi 10-30 giây đọc giá trị hiện tại rồi ghi đè (không cộng dồn) xuống DB — chấp nhận mất tối đa vài giây dữ liệu đếm nếu Redis crash, vì đây là dữ liệu không quan trọng bằng giao dịch tài chính.

## Trade-offs
Write-behind đánh đổi độ bền dữ liệu (durability) để lấy write throughput và latency thấp: mọi write nằm trong cache mà chưa được flush đều có nguy cơ mất vĩnh viễn nếu cache/worker crash, mất điện, hoặc container bị kill trước cửa sổ flush tiếp theo — đây là lý do pattern này không phù hợp cho dữ liệu giao dịch tài chính hay bất cứ nơi nào yêu cầu ACID nghiêm ngặt. Việc gộp write giúp giảm tải DB nhưng cũng có nghĩa là các bước ghi trung gian biến mất — nếu ứng dụng cần audit trail đầy đủ từng thay đổi, write-behind đơn thuần không đáp ứng được trừ khi ghi thêm change log riêng. Độ phức tạp vận hành tăng đáng kể so với write-through: cần theo dõi độ trễ giữa cache và DB (replication lag kiểu write-behind), xử lý thứ tự ghi khi có nhiều worker chạy song song (dễ race condition nếu không partition đúng theo key), và có kế hoạch rõ ràng cho trường hợp flush thất bại liên tục (dead-letter, alerting, backpressure).

## Best Practices
- Không dùng cache in-memory thuần làm nơi lưu duy nhất dữ liệu chưa flush — đặt một lớp durable (AOF, Kafka, disk-backed queue) giữa client và DB để giới hạn cửa sổ mất dữ liệu.
- Giới hạn kích thước và thời gian buffer rõ ràng (ví dụ flush khi đạt 1000 record hoặc sau 2 giây, tùy điều kiện nào tới trước) để cân bằng giữa throughput và độ trễ dữ liệu tới DB.
- Theo dõi và alert riêng độ trễ giữa thời điểm ghi cache và thời điểm flush thành công xuống DB (write lag) — lag tăng bất thường là dấu hiệu sớm worker đang không theo kịp hoặc DB đang chậm.
- Đảm bảo flush là idempotent (dùng upsert hoặc khóa duy nhất) để retry sau lỗi không tạo ra dữ liệu trùng lặp trong DB.
- Chỉ áp dụng write-behind cho dữ liệu chấp nhận được mất mát trong cửa sổ ngắn (metrics, counter, log phân tích); không dùng cho dữ liệu giao dịch cần đảm bảo ACID.

## Common Mistakes
- Coi Redis/cache in-memory là đủ bền vững mà không bật persistence (RDB/AOF) hay không có lớp queue durable phía sau, dẫn tới mất toàn bộ dữ liệu chưa flush khi restart hoặc OOM-kill.
- Không giới hạn kích thước buffer, khiến khi DB downtime kéo dài, dữ liệu dirty tích lũy tràn bộ nhớ cache và gây crash toàn bộ hệ thống thay vì chỉ mất một phần dữ liệu.
- Flush không idempotent, nên khi worker crash giữa chừng và retry, dữ liệu bị ghi trùng lặp xuống DB.
- Dùng write-behind cho dữ liệu cần tính nhất quán mạnh (số dư tài khoản, trạng thái đơn hàng) chỉ vì muốn latency thấp, bỏ qua yêu cầu nghiệp vụ về durability.
- Không có cơ chế giám sát write lag, nên khi worker bị treo âm thầm (deadlock, exception nuốt lỗi), team chỉ phát hiện khi dữ liệu trong DB đã thiếu hàng giờ.

## Interview Questions
**Hỏi**: Write-behind khác gì với write-through về mặt đảm bảo dữ liệu?
**Trả lời**: Write-through ghi đồng bộ cả cache và DB trong cùng một request, nên khi request trả về thành công thì dữ liệu chắc chắn đã persist xuống DB; write-behind chỉ ghi cache rồi trả response ngay, DB được cập nhật sau bởi worker nền, nên tồn tại cửa sổ thời gian dữ liệu chỉ tồn tại ở cache và có thể mất nếu cache crash trước khi flush.

**Hỏi**: Làm sao giảm rủi ro mất dữ liệu trong write-behind mà vẫn giữ được lợi ích về throughput?
**Trả lời**: Đặt một lớp durable ở giữa (Kafka, Redis với AOF fsync thường xuyên, hoặc WAL riêng) thay vì chỉ dựa vào RAM cache thuần, giới hạn buffer theo kích thước/thời gian để cửa sổ rủi ro nhỏ và có thể đo lường được, đồng thời giám sát write lag để phát hiện sớm khi worker flush không theo kịp.

**Hỏi**: Khi nào không nên dùng write-behind dù cần tối ưu write throughput?
**Trả lời**: Khi dữ liệu yêu cầu tính nhất quán mạnh và không chấp nhận mất mát dù nhỏ, ví dụ giao dịch tài chính, đơn hàng, hoặc bất cứ nghiệp vụ cần ACID — trong các trường hợp này nên dùng write-through hoặc ghi trực tiếp DB với transaction, chấp nhận latency cao hơn để đổi lấy durability.

## Summary
Write-behind ghi dữ liệu vào cache trước và trả response ngay cho client, sau đó một worker nền bất đồng bộ gộp và flush dữ liệu xuống DB theo lô, giúp giảm mạnh write latency và tải ghi lên DB. Cơ chế gộp write (coalescing) là lợi ích cốt lõi khi nhiều write trùng key xảy ra liên tiếp, chỉ cần một write cuối cùng xuống DB. Đánh đổi chính là rủi ro mất dữ liệu nếu cache/worker crash trước khi flush, nên pattern này chỉ phù hợp với dữ liệu chấp nhận mất mát trong cửa sổ ngắn. Production system nghiêm túc luôn đặt một lớp durable (queue có persistence) giữa client và DB thay vì dựa hoàn toàn vào RAM cache, cùng với giám sát write lag chặt chẽ. Hiểu rõ ranh giới giữa "đã trả response" và "đã persist thật sự" là yếu tố quyết định có nên dùng write-behind cho một loại dữ liệu cụ thể hay không.

## Knowledge Graph
- Write-Through Cache — pattern đối lập, ghi đồng bộ cache và DB cùng lúc để đảm bảo durability ngay lập tức thay vì trì hoãn.
- Cache Aside — pattern quản lý đọc/ghi khác, nơi ứng dụng tự điều phối cache và DB nhưng không có cơ chế gộp write bất đồng bộ.
- Message Queue / Kafka — lớp durable thường được dùng để thay thế hoặc bổ trợ cache in-memory làm buffer ghi bền vững hơn.
- Batching / Write Coalescing — kỹ thuật lõi bên trong write-behind giúp giảm số lượng write thực tế xuống DB.
- Eventual Consistency — mô hình nhất quán mà write-behind chấp nhận, vì dữ liệu DB luôn trễ hơn cache một khoảng thời gian.
- Durability (ACID) — thuộc tính bị hy sinh một phần trong write-behind để đổi lấy throughput và latency thấp.

## Five Things To Remember
- Ghi cache trước, trả response ngay, DB được cập nhật sau bởi worker nền.
- Gộp nhiều write trùng key thành một write cuối cùng giúp giảm tải DB đáng kể.
- Dữ liệu chưa flush có thể mất vĩnh viễn nếu cache/worker crash — không dùng cho dữ liệu giao dịch.
- Luôn đặt lớp durable (queue/AOF) giữa client và DB thay vì chỉ dựa vào RAM cache.
- Giám sát write lag chặt chẽ để phát hiện sớm khi flush không theo kịp tốc độ ghi.
