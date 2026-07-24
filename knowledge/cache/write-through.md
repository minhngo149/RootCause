---
id: write-through
title: Write Through
tags: ["cache"]
---

# Write Through

> Status: Draft

## Problem

Khi ứng dụng dùng cache-aside (lazy loading), luồng ghi thường chỉ update DB rồi xóa hoặc bỏ qua cache, khiến request đọc kế tiếp gặp cache miss và phải tự nạp lại dữ liệu từ DB. Với các entity đọc liên tục ngay sau khi ghi — giá sản phẩm vừa cập nhật, số dư ví vừa trừ, trạng thái đơn hàng vừa chuyển — khoảng trống giữa "ghi xong" và "cache có lại dữ liệu mới" tạo ra một cửa sổ cache miss dồn dập, đặc biệt nguy hiểm khi nhiều client cùng đọc một key nóng ngay sau khi nó bị invalidate. Ứng dụng cần một cơ chế đảm bảo cache và DB được cập nhật cùng lúc trong một thao tác ghi, thay vì tách rời hai việc "ghi DB" và "làm mới cache" thành hai bước độc lập dễ lệch nhau.

## Pain Points

- Xóa cache khi ghi (cache invalidation) mà không nạp lại ngay tạo ra "thundering herd": hàng trăm request đọc cùng lúc miss cache, tất cả cùng dội xuống DB để load lại cùng một key.
- Giữa lúc DB đã update nhưng cache chưa kịp invalidate (do lỗi mạng, timeout, hoặc thứ tự thao tác sai), client đọc cache trúng dữ liệu cũ — số dư sai, giá cũ, trạng thái đơn hàng lỗi thời.
- Logic ghi rải rác nhiều nơi trong codebase (nhiều service cùng ghi một bảng) dễ quên bước invalidate cache ở một nhánh code, tạo ra cache không nhất quán âm thầm kéo dài.
- Chi phí vận hành tăng khi phải điều tra "tại sao dữ liệu hiển thị sai" mà không rõ là do cache stale hay do bug logic nghiệp vụ, vì hai luồng ghi DB và ghi cache không đồng bộ atomic với nhau.

## Solution

Write-through là chiến lược ghi trong đó mỗi thao tác write đi qua cache trước, cache ghi đồng bộ xuống DB, và chỉ khi DB xác nhận ghi thành công thì thao tác write mới được coi là hoàn tất. Cache luôn chứa bản sao mới nhất của dữ liệu ngay sau khi ghi (không có cửa sổ stale), đổi lại latency của mỗi write tăng lên vì phải chờ cả hai lớp — cache và DB — xác nhận trước khi trả kết quả cho client.

## How It Works

Trong write-through thuần túy, ứng dụng (hoặc một lớp trừu tượng cache-provider) không ghi trực tiếp xuống DB mà gọi vào cache, và chính cache chịu trách nhiệm ghi xuống storage phía sau theo mô hình đồng bộ (synchronous): cache nhận request ghi, ghi vào bộ nhớ của nó, đồng thời gọi ghi xuống DB, và chỉ trả kết quả thành công cho caller sau khi DB commit xong. Đây là điểm khác biệt cốt lõi so với write-back (write-behind), nơi cache trả kết quả ngay sau khi ghi vào bộ nhớ và đẩy xuống DB bất đồng bộ sau đó — write-through chấp nhận latency cao hơn để đổi lấy việc không bao giờ có dữ liệu "chỉ nằm trong cache mà chưa persist".

Trong thực tế ở các hệ thống dùng Redis/Memcached kết hợp RDBMS, write-through thường không phải tính năng có sẵn của cache engine mà được lập trình ở tầng ứng dụng hoặc thư viện data-access: một hàm `save(key, value)` thực hiện transaction ghi DB trước, sau đó ghi (SET) vào cache trong cùng logic, và chỉ coi là thành công khi cả hai bước hoàn tất. Thứ tự "DB trước, cache sau" quan trọng hơn vẻ ngoài của nó: nếu ghi cache trước rồi ghi DB thất bại, cache sẽ chứa dữ liệu không tồn tại trong DB (dirty cache), còn nếu ghi DB trước rồi ghi cache thất bại, hệ thống chỉ rơi về trạng thái cache miss tạm thời — vốn tự phục hồi được ở lần đọc kế tiếp (fallback sang cache-aside), nên an toàn hơn nhiều so với chiều ngược lại.

Vì hai thao tác ghi (DB và cache) không nằm trong cùng một transaction ACID xuyên hệ thống, write-through vẫn có khe hở nhất quán ở mức micro: nếu tiến trình chết đúng lúc giữa "DB commit xong" và "cache SET xong", cache sẽ thiếu bản ghi mới nhất — đây là lý do write-through vẫn cần một cơ chế TTL hoặc invalidation dự phòng, không thể coi cache là nguồn sự thật tuyệt đối chỉ vì dùng write-through.

## Production Architecture

Trong một hệ thống ví điện tử, luồng trừ tiền chạy qua một service `WalletService.debit()`: service mở transaction ghi bảng `wallet_balance` trong Postgres, commit xong thì ngay trong cùng request ghi lại số dư mới vào Redis với key `wallet:{user_id}:balance`, rồi mới trả response thành công cho client. Mọi request đọc số dư sau đó (hiển thị trên app, kiểm tra trước giao dịch tiếp theo) đọc thẳng từ Redis và luôn thấy giá trị đúng ngay lập tức, không có cửa sổ cache miss hay dữ liệu cũ. Ở các hệ thống catalog thương mại điện tử, khi admin cập nhật giá sản phẩm qua CMS, service ghi đồng thời xuống DB và cache theo write-through để đảm bảo trang sản phẩm không hiển thị giá cũ dù chỉ một giây — khác với mô tả sản phẩm (ít nhạy cảm hơn) vẫn có thể dùng cache-aside với TTL vài phút. Việc chọn write-through thường giới hạn ở tập entity có yêu cầu "đọc ngay sau ghi phải chính xác" (số dư, tồn kho, trạng thái đơn hàng), không áp dụng tràn lan cho toàn bộ dữ liệu vì chi phí latency ghi.

## Trade-offs

- Latency ghi tăng rõ rệt vì mỗi write phải chờ cả cache và DB xác nhận tuần tự, thay vì chỉ chờ DB (cache-aside) hoặc chỉ chờ cache (write-back) — với hệ thống có write throughput cao, đây là chi phí đáng kể.
- Đổi lại, cache luôn "nóng" và chính xác cho các key vừa ghi, loại bỏ hoàn toàn vấn đề thundering herd do cache miss sau invalidation.
- Nếu ghi cache thất bại sau khi DB đã commit, ứng dụng phải quyết định: coi write là thành công (chấp nhận cache tạm thời thiếu, dựa vào cơ chế fallback) hay retry/rollback — cả hai lựa chọn đều thêm độ phức tạp logic.
- Không giải quyết được bài toán ghi hàng loạt (bulk write) hiệu quả, vì mỗi bản ghi đều phải qua cache tuần tự — các luồng batch/import thường bypass write-through và dùng invalidation theo lô thay thế.

## Best Practices

- Chỉ áp dụng write-through cho các entity có yêu cầu đọc-ngay-sau-ghi nghiêm ngặt (số dư, tồn kho, trạng thái giao dịch), không dùng cho toàn bộ dữ liệu để tránh tăng latency ghi không cần thiết.
- Luôn ghi DB trước, cache sau — nếu ghi cache thất bại, hệ thống chỉ rơi về cache miss (tự phục hồi), an toàn hơn nhiều so với việc cache chứa dữ liệu chưa từng tồn tại trong DB.
- Vẫn đặt TTL dự phòng cho key dù dùng write-through, để tự dọn dẹp các trường hợp cache lệch DB do lỗi tiến trình giữa hai bước ghi.
- Đo và giám sát riêng latency của bước ghi cache trong tổng thời gian write, để phát hiện sớm khi cache engine trở thành điểm nghẽn mới của luồng ghi.
- Cân nhắc dùng transaction hoặc outbox pattern khi nghiệp vụ cực kỳ nhạy cảm, thay vì ghi cache "cố gắng tốt nhất" (best-effort) ngay sau DB commit.

## Common Mistakes

- Ghi cache trước rồi mới ghi DB: nếu bước ghi DB thất bại hoặc rollback, cache đã chứa dữ liệu không tồn tại trong nguồn sự thật, gây sai lệch nghiêm trọng hơn cache miss.
- Áp dụng write-through cho mọi bảng/entity trong hệ thống, kể cả dữ liệu ít đọc lại ngay, khiến latency ghi tăng vô ích trên toàn bộ luồng ghi.
- Coi write-through là transaction atomic thật sự giữa cache và DB, trong khi thực chất là hai lời gọi tuần tự có thể thất bại độc lập — không xử lý trường hợp ghi cache lỗi sau khi DB đã commit.
- Dùng write-through cho luồng ghi hàng loạt (bulk insert/update), khiến mỗi bản ghi phải chờ round-trip cache tuần tự, làm chậm job batch một cách không cần thiết.
- Không đặt TTL dự phòng vì tin tưởng tuyệt đối vào write-through, dẫn đến cache lệch DB âm thầm tồn tại mãi nếu có một lần ghi cache thất bại không được phát hiện.

## Interview Questions

**Hỏi**: Write-through khác write-back (write-behind) ở điểm nào, và khi nào chọn cái nào?

**Trả lời**: Write-through ghi đồng bộ vào cả cache và DB, chỉ trả kết quả khi DB xác nhận — cache luôn mới nhưng ghi chậm hơn. Write-back trả kết quả ngay sau khi ghi cache, rồi đẩy xuống DB bất đồng bộ sau — ghi nhanh hơn nhưng có rủi ro mất dữ liệu nếu cache crash trước khi kịp flush. Chọn write-through cho dữ liệu cần chính xác tuyệt đối và đọc ngay sau ghi (số dư, tồn kho); chọn write-back cho luồng ghi cực nhiều, chấp nhận rủi ro mất mát nhỏ để đổi lấy throughput.

**Hỏi**: Trong write-through, nên ghi DB trước hay ghi cache trước? Vì sao thứ tự này quan trọng?

**Trả lời**: Nên ghi DB trước, cache sau. Nếu ghi cache trước mà DB ghi thất bại, cache sẽ chứa dữ liệu không tồn tại trong nguồn sự thật (dirty cache) — sai lệch nghiêm trọng và khó phát hiện. Nếu ghi DB trước mà cache ghi thất bại, hệ thống chỉ rơi về cache miss tạm thời, tự phục hồi được ở lần đọc kế tiếp qua cơ chế fallback.

**Hỏi**: Write-through có loại bỏ hoàn toàn khả năng cache và DB lệch nhau không?

**Trả lời**: Không. Vì hai thao tác ghi không nằm trong một transaction ACID xuyên hệ thống, nếu tiến trình chết giữa lúc DB đã commit và cache chưa kịp ghi, cache sẽ thiếu bản ghi mới nhất. Đây là lý do vẫn cần TTL hoặc invalidation dự phòng dù đã dùng write-through.

## Summary

Write-through là chiến lược ghi đồng thời vào cache và DB trong cùng một thao tác, đảm bảo cache luôn phản ánh dữ liệu mới nhất ngay sau khi ghi, đổi lại latency của mỗi write tăng lên vì phải chờ cả hai lớp xác nhận. Thứ tự đúng là ghi DB trước, cache sau, vì lỗi ở bước ghi cache chỉ dẫn đến cache miss tự phục hồi, trong khi lỗi ở chiều ngược lại tạo ra dữ liệu cache không có thật. Chiến lược này phù hợp với các entity cần đọc-ngay-sau-ghi chính xác tuyệt đối như số dư ví, tồn kho, trạng thái giao dịch, không nên áp dụng tràn lan vì chi phí latency ghi. Vì không phải transaction atomic thật sự, write-through vẫn cần TTL hoặc cơ chế invalidation dự phòng để xử lý khe hở nhất quán khi tiến trình lỗi giữa hai bước ghi. So với write-back, write-through đánh đổi throughput ghi để lấy độ tin cậy của dữ liệu cache.

## Knowledge Graph

- Cache-Aside — chiến lược đối lập, cache chỉ được nạp khi đọc và lazy-invalidate khi ghi, tạo cửa sổ cache miss mà write-through loại bỏ.
- Write-Back (Write-Behind) — chiến lược ghi bất đồng bộ xuống DB sau khi ghi cache, đổi lấy throughput cao hơn nhưng rủi ro mất dữ liệu khi cache crash.
- ACID — write-through không đạt được tính atomic thật sự giữa cache và DB vì đây là hai lời gọi tuần tự, không phải một transaction xuyên hệ thống.
- Thundering Herd — vấn đề cache-aside dễ gặp khi nhiều request cùng miss một key sau invalidation, mà write-through tránh được nhờ cache luôn được nạp ngay khi ghi.
- Outbox Pattern — kỹ thuật bổ sung để đảm bảo tính nhất quán khi ghi nhiều đích (DB, cache, message queue) mà không dùng transaction phân tán.
- TTL / Cache Invalidation — cơ chế dự phòng vẫn cần thiết trong write-through để xử lý khe hở khi ghi cache thất bại sau khi DB đã commit.

## Five Things To Remember

- Write-through ghi đồng bộ vào cả cache và DB, chỉ hoàn tất khi DB xác nhận.
- Luôn ghi DB trước, cache sau — lỗi ghi cache chỉ gây cache miss tự phục hồi, không phải dữ liệu ảo.
- Cache luôn mới nhưng latency ghi cao hơn cache-aside hoặc write-back.
- Chỉ dùng cho entity cần đọc-ngay-sau-ghi chính xác tuyệt đối, không áp dụng tràn lan.
- Vẫn cần TTL dự phòng vì hai bước ghi không phải một transaction atomic thật sự.
