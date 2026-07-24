---
id: backpressure
title: Backpressure
tags: ["backend", "reliability"]
---

# Backpressure

> Status: Draft

## Problem

Producer sinh dữ liệu (request, message, event) nhanh hơn tốc độ consumer xử lý được. Nếu không có cơ chế nào báo ngược lại cho producer biết "chậm lại", hệ thống chỉ còn một cách xử lý phần chênh lệch: xếp vào buffer. Buffer đó — queue trong process, channel, socket buffer, message broker — phình to không giới hạn cho đến khi hết bộ nhớ hoặc hết dung lượng lưu trữ. Đây không phải lỗi hiếm, mà là hệ quả tất yếu của bất kỳ hệ thống nào có tốc độ sinh và tốc độ tiêu thụ dữ liệu lệch nhau kéo dài, dù chỉ một khoảng thời gian ngắn dưới tải cao.

## Pain Points

- Buffer trong memory phình to không kiểm soát dẫn đến OOM kill (Linux OOM killer, Kubernetes pod bị evict do vượt memory limit), sự cố xảy ra đột ngột chứ không suy giảm từ từ.
- Latency tăng dần đều theo độ dài queue (queueing delay cộng dồn) — request cuối hàng đợi phải chờ xử lý hết toàn bộ request phía trước, dẫn đến timeout hàng loạt dù mỗi request xử lý riêng lẻ vẫn nhanh.
- Không có tín hiệu ngược, producer tiếp tục gửi ở tốc độ cũ hoặc thậm chí retry khi timeout, làm tăng thêm tải cho consumer đã quá tải — vòng xoáy tự khuếch đại (death spiral).
- Khi buffer vỡ ở một node, hệ thống mất dữ liệu (drop message) hoặc crash rồi restart, gây mất trạng thái và cascading failure sang các service downstream phụ thuộc.
- Chi phí vận hành tăng vì phải liên tục scale buffer/broker (tăng RAM, tăng disk cho Kafka/RabbitMQ) để "chữa cháy" thay vì xử lý nguyên nhân gốc là tốc độ tiêu thụ không theo kịp.

## Solution

Backpressure là cơ chế để consumer chủ động báo ngược lại cho producer (hoặc tầng trung gian) biết nó đang quá tải, buộc producer giảm tốc độ gửi, tạm dừng, hoặc bị hệ thống từ chối nhận thêm dữ liệu một cách có kiểm soát. Nguyên tắc cốt lõi: buffer là bộ đệm hấp thụ chênh lệch tốc độ tạm thời, không phải nơi chứa vô hạn — khi buffer đạt ngưỡng, phải có hành động rõ ràng (block, drop, reject) thay vì để nó tăng trưởng không giới hạn. Backpressure biến một lỗi chậm (queue phình to rồi OOM sau nhiều phút) thành một tín hiệu tức thời mà hệ thống có thể phản ứng ngay: throttle, shed load, hoặc scale.

## How It Works

Backpressure hoạt động dựa trên nguyên tắc **bounded buffer + signal ngược**. Ở mức thấp nhất, TCP đã có cơ chế này sẵn: receive window (`rwnd`) báo cho sender biết bao nhiêu byte còn có thể nhận, khi buffer nhận đầy, window co lại về 0, sender buộc phải dừng gửi cho đến khi nhận được ACK với window mới — đây là backpressure ở tầng transport, hoàn toàn tự động, không cần code ứng dụng can thiệp.

Ở tầng ứng dụng, có ba mô hình triển khai chính:

1. **Blocking/bounded queue**: hàng đợi có giới hạn kích thước cố định (vd. `BlockingQueue` với capacity trong Java, channel có buffer size trong Go). Khi đầy, thao tác `put`/`send` block cho đến khi có chỗ trống — producer tự động chậm lại vì bị chặn, không cần logic riêng.
2. **Reactive Streams / pull-based**: consumer chủ động yêu cầu (`request(n)`) số lượng phần tử nó sẵn sàng xử lý tiếp theo (mô hình trong Reactor, RxJava, Project Reactor). Producer chỉ được phát ra đúng số lượng đã được yêu cầu, không bao giờ đẩy nhiều hơn khả năng consumer công bố — đảo ngược mô hình push truyền thống thành pull có kiểm soát.
3. **Credit-based flow control**: consumer cấp một số "credit" cho producer (mỗi credit tương ứng một message được phép gửi), producer tiêu credit khi gửi và chỉ gửi tiếp khi còn credit — AMQP 0.9.1 dùng cơ chế `channel.flow`, HTTP/2 dùng window-based flow control tương tự per-stream.

Khi buffer đạt ngưỡng và không thể block (vd. network socket không thể chặn vô hạn, hoặc SLA yêu cầu phản hồi nhanh), hệ thống chuyển sang **load shedding**: chủ động từ chối request mới bằng HTTP 429/503 kèm `Retry-After`, hoặc drop message cũ nhất/mới nhất tùy chiến lược (drop-tail vs drop-head), thay vì cố nhận hết rồi sập.

## Production Architecture

Trong một pipeline event streaming (vd. Kafka), backpressure thể hiện qua consumer lag: producer ghi vào partition nhanh hơn consumer group đọc và xử lý, offset của consumer tụt lại ngày càng xa so với offset mới nhất. Kafka không chặn producer (broker vẫn nhận ghi bình thường vì disk-based log không phải in-memory buffer), nhưng hệ thống giám sát phải theo dõi consumer lag như một chỉ báo sớm và có cơ chế autoscale consumer (thêm partition/consumer instance) hoặc alert trước khi lag vượt retention period và mất dữ liệu.

Trong một API gateway đứng trước service downstream chậm (vd. gọi một service tính toán nặng hoặc gọi DB đang bị lock contention), gateway áp dụng **connection pool có giới hạn** (vd. max connections tới upstream) kết hợp **circuit breaker**: khi pool đầy hoặc tỷ lệ lỗi/timeout tăng, gateway trả 503 ngay cho client mới thay vì xếp hàng vô hạn, đồng thời thông báo qua header `Retry-After` để client (hoặc load balancer) tự retry sau. Ở tầng message queue nội bộ (RabbitMQ), consumer set `prefetch count` (QoS) giới hạn số message chưa ack được gửi tới cùng lúc — đây chính là credit-based backpressure, ngăn một consumer chậm bị broker dồn hàng nghìn message vào memory cùng lúc.

## Trade-offs

Backpressure luôn đánh đổi giữa ba lựa chọn không thể có đồng thời: giữ tất cả dữ liệu (block producer, chấp nhận tăng latency), giữ latency thấp (drop dữ liệu khi quá tải), hoặc tăng tài nguyên (scale buffer, tốn chi phí và vẫn có giới hạn). Chọn block producer bảo toàn dữ liệu nhưng lan truyền độ chậm ngược lên toàn bộ chuỗi gọi phía trên — một consumer chậm có thể làm chậm cả hệ thống upstream nếu không có timeout hợp lý. Chọn drop/load-shedding giữ hệ thống sống nhưng mất dữ liệu hoặc trả lỗi cho user, cần nghiệp vụ chấp nhận được điều đó (không phù hợp cho giao dịch tài chính, phù hợp cho analytics event non-critical). Reactive/pull-based model chính xác nhất nhưng phức tạp hơn để triển khai và debug, đòi hỏi toàn bộ chuỗi (từ nguồn tới đích) đều tuân thủ cùng một giao thức backpressure — chỉ cần một mắt xích dùng push không kiểm soát là toàn bộ nỗ lực vô nghĩa.

## Best Practices

- Luôn đặt giới hạn cứng (bounded) cho mọi queue/buffer trong hệ thống — không có buffer "unbounded" nào là an toàn trong production, kể cả khi nghĩ traffic sẽ luôn thấp.
- Theo dõi độ dài queue/consumer lag như một metric hạng nhất (không chỉ CPU/memory), vì nó là chỉ báo sớm nhất của mất cân bằng tốc độ trước khi thành sự cố.
- Kết hợp backpressure với timeout và circuit breaker ở mọi lời gọi giữa các service — backpressure không có timeout đi kèm dễ biến thành block vô hạn thay vì chậm lại có kiểm soát.
- Chọn chiến lược drop rõ ràng (drop-oldest hay drop-newest, reject hay queue) dựa trên nghiệp vụ, đừng để default của framework quyết định thay.
- Test hệ thống dưới tải vượt khả năng consumer có chủ đích (load test với producer nhanh hơn consumer) để xác nhận cơ chế backpressure thực sự kích hoạt, không chỉ tồn tại trên giấy.

## Common Mistakes

- Dùng queue/channel không giới hạn kích thước (unbounded) vì "đơn giản hơn", để rồi phát hiện ra giới hạn thật sự chỉ là RAM của máy khi OOM xảy ra ở production.
- Thêm buffer/queue lớn hơn để "giải quyết" cảnh báo quá tải mà không xử lý nguyên nhân gốc — chỉ trì hoãn sự cố, khiến nó xảy ra muộn hơn nhưng nghiêm trọng hơn.
- Producer retry ngay lập tức khi bị từ chối (429/503) mà không có backoff, biến cơ chế bảo vệ (load shedding) thành nguyên nhân gây thêm tải.
- Áp dụng backpressure ở một tầng của hệ thống nhưng bỏ sót tầng khác (vd. giới hạn queue trong app nhưng không giới hạn connection pool tới DB), khiến điểm nghẽn chỉ dịch chuyển chứ không biến mất.
- Không phân biệt giữa chậm tạm thời (traffic spike ngắn, buffer hấp thụ được) và quá tải kéo dài (consumer cần scale) — dẫn đến hoặc alert giả liên tục, hoặc bỏ lỡ dấu hiệu cần scale thật sự.

## Interview Questions

**Hỏi**: Backpressure khác gì với rate limiting?

**Trả lời**: Rate limiting giới hạn tốc độ request được chấp nhận dựa trên một ngưỡng cố định định trước (vd. 1000 req/s), áp dụng bất kể consumer có đang rảnh hay không. Backpressure là phản hồi động dựa trên trạng thái thực tế của consumer (buffer đầy bao nhiêu, đang xử lý bao nhiêu) — producer chỉ bị chậm lại khi consumer thực sự quá tải, không theo một con số tĩnh định sẵn.

**Hỏi**: Vì sao "cứ thêm buffer to hơn" không phải là giải pháp cho vấn đề backpressure?

**Trả lời**: Buffer to hơn chỉ tăng thời gian trước khi hết bộ nhớ, không giải quyết chênh lệch tốc độ giữa producer và consumer. Nếu tốc độ sinh dữ liệu vẫn lớn hơn tốc độ tiêu thụ trong thời gian dài, buffer nào cũng sẽ đầy — vấn đề thật là cần cơ chế báo hiệu và giảm tốc/scale, không phải trì hoãn thời điểm buffer đầy.

**Hỏi**: TCP có cơ chế backpressure không, và nó hoạt động thế nào?

**Trả lời**: Có, qua receive window (`rwnd`) trong flow control. Bên nhận công bố kích thước buffer còn trống của mình trong mỗi ACK; khi buffer nhận đầy, window co về 0, buộc bên gửi dừng gửi thêm dữ liệu cho đến khi nhận ACK báo window đã mở lại. Đây là backpressure tự động ở tầng transport, không cần logic riêng ở tầng ứng dụng.

## Summary

Backpressure là cơ chế phản hồi ngược để consumer báo cho producer biết mình đang quá tải, thay vì để producer tiếp tục gửi và buffer phình to không giới hạn dẫn tới OOM hoặc cascading failure. Cơ chế này thể hiện qua nhiều mức: TCP receive window ở tầng transport, bounded queue/blocking ở tầng ứng dụng, credit-based flow control ở message broker, và load shedding khi không thể chặn producer. Mọi buffer trong hệ thống phải có giới hạn cứng và một hành động rõ ràng khi đạt ngưỡng — block, drop, hoặc reject — thay vì tăng trưởng vô hạn. Trade-off cốt lõi luôn nằm giữa giữ dữ liệu (chấp nhận tăng latency) và giữ latency thấp (chấp nhận mất dữ liệu), lựa chọn phụ thuộc vào nghiệp vụ cụ thể. Theo dõi độ dài queue và consumer lag như metric hạng nhất là cách phát hiện sớm nhất trước khi backpressure thất bại và hệ thống sập.

## Knowledge Graph

- Circuit Breaker — cơ chế bổ trợ ngăn lời gọi tới service đã quá tải, thường đi kèm backpressure để tránh block vô hạn.
- Rate Limiting — giới hạn tốc độ tĩnh, khác cơ chế phản hồi động của backpressure nhưng thường triển khai cùng nhau.
- Consumer Lag (Kafka) — chỉ số cụ thể đo mức độ producer vượt tốc độ consumer trong hệ thống streaming.
- Load Shedding — chiến lược xử lý khi backpressure không thể chặn producer, chủ động từ chối bớt tải.
- Retry Storm — hậu quả thường gặp khi producer không tôn trọng tín hiệu backpressure (429/503) và retry ngay không backoff.
- Bounded Queue — cấu trúc dữ liệu nền tảng hiện thực hóa nguyên tắc "không buffer vô hạn" của backpressure.

## Five Things To Remember

- Backpressure là tín hiệu ngược từ consumer báo producer chậm lại, không phải một loại buffer.
- Mọi queue/buffer trong hệ thống phải có giới hạn cứng, không có buffer "unbounded" nào an toàn ở production.
- Khi buffer đầy, phải có hành động rõ ràng: block, drop, hoặc reject — không để nó âm thầm tăng trưởng.
- Theo dõi độ dài queue/consumer lag như metric hạng nhất để phát hiện mất cân bằng tốc độ trước khi thành sự cố.
- Producer phải tôn trọng tín hiệu từ chối (429/503) bằng backoff, nếu không cơ chế bảo vệ sẽ tự biến thành nguyên nhân gây tải thêm.
