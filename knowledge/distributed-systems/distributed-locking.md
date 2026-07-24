---
id: distributed-locking
title: Distributed Locking
tags: ["distributed-systems"]
---

# Distributed Locking

> Status: Draft

## Problem

Trong một process đơn, `mutex`/`synchronized` đủ để đảm bảo chỉ một thread chạy vào critical section tại một thời điểm — nhưng khi hệ thống scale ra nhiều instance (nhiều pod Kubernetes chạy cùng một cron job, nhiều worker cùng đọc một hàng đợi, nhiều API server cùng xử lý request cập nhật một resource), mutex trong bộ nhớ không còn tác dụng vì mỗi process có không gian bộ nhớ riêng. Cần một cơ chế khóa nằm ngoài process, được nhiều node cùng thấy và cùng tôn trọng, để đảm bảo chỉ một node được thực hiện một thao tác tại một thời điểm — ví dụ chỉ một trong năm pod chạy cron job "tính lương cuối tháng", hoặc chỉ một request được phép ghi đè file cấu hình chia sẻ.

## Pain Points

- Nhiều instance của cùng một cron job (deploy dạng Kubernetes CronJob hoặc scheduler chạy trên nhiều node) cùng chạy song song do lệch múi giờ leader election hoặc do một lần chạy trước chưa kết thúc — dẫn đến gửi email trùng lặp, tính phí khách hàng hai lần, hoặc ghi đè lẫn nhau lên cùng một báo cáo.
- Race condition khi nhiều worker cùng lấy một job từ queue chưa hỗ trợ khóa nguyên tử (ví dụ đọc-rồi-xóa không atomic trên Redis List cũ) — cùng một đơn hàng bị xử lý hai lần, trừ kho hai lần.
- Không có khóa khi cập nhật một resource dùng chung (file, cấu hình, số dư) từ nhiều node dẫn tới lost update tương tự thiếu isolation trong database, nhưng khó phát hiện hơn vì các node độc lập, log rải rác, không có transaction log tập trung để truy vết.
- Chi phí vận hành tăng khi phải revert dữ liệu thủ công sau sự cố double-processing, đặc biệt nghiêm trọng với thao tác không idempotent như gửi tiền, gửi SMS, hoặc gọi API bên thứ ba tính phí theo lượt gọi.

## Solution

Distributed lock là một cơ chế khóa được lưu trữ ở một nơi mà mọi node trong hệ thống đều truy cập được (thường là Redis, ZooKeeper, etcd, hoặc bảng trong database quan hệ), cho phép nhiều process/node phối hợp để chỉ một trong số đó được giữ quyền thực hiện một thao tác tại một thời điểm. Cài đặt đơn giản nhất là `SET key value NX PX ttl` trên Redis — chỉ node nào set thành công (key chưa tồn tại) mới được coi là giữ lock, TTL đảm bảo lock tự giải phóng nếu node giữ lock chết mà không kịp unlock. Redlock là thuật toán do Redis đề xuất để làm distributed lock đáng tin cậy hơn khi chạy trên nhiều Redis instance độc lập, thay vì tin vào một instance Redis duy nhất — chấp nhận lock hợp lệ chỉ khi đa số (quorum, ví dụ 3/5) instance xác nhận cùng một lock trong cùng khung thời gian.

## How It Works

Cơ chế lock đơn instance dựa trên tính nguyên tử của lệnh `SET key client_id NX PX 30000` trên Redis: `NX` đảm bảo chỉ ghi khi key chưa tồn tại (không ai đang giữ lock), `PX 30000` đặt TTL 30 giây để lock tự hết hạn nếu client giữ lock crash mà không gọi unlock. Việc unlock phải kiểm tra `client_id` khớp trước khi xóa (thường bằng Lua script để đảm bảo check-and-delete atomic), tránh trường hợp client A hết hạn lock, client B lấy được lock mới, rồi client A (vẫn tưởng mình còn giữ lock) xóa nhầm lock của B.

Redlock mở rộng cơ chế này ra N instance Redis độc lập (khuyến nghị N=5, không phải cluster/replication mà là các instance hoàn toàn tách biệt): client tuần tự thử `SET NX PX` trên từng instance, tính tổng thời gian đã trôi qua, và chỉ coi là "giữ được lock" nếu set thành công trên đa số instance (ít nhất 3/5) và tổng thời gian còn lại (TTL trừ đi thời gian đã tốn) vẫn còn dương. Ý tưởng là nếu một vài instance Redis bị mất kết nối hoặc chậm, hệ thống vẫn hoạt động được nhờ quorum, giống cơ chế quorum trong Raft/Paxos — nhưng khác biệt quan trọng là Redlock dựa vào đồng hồ vật lý (wall clock, TTL tính bằng thời gian thực) chứ không dựa vào fencing token hay logical clock, đây chính là điểm Martin Kleppmann chỉ trích trong bài phân tích nổi tiếng "How to do distributed locking" — TTL có thể sai lệch do GC pause, network delay, hoặc clock drift giữa các node, khiến lock "hết hạn" theo đồng hồ của Redis nhưng client vẫn đang tưởng mình còn giữ lock và tiếp tục ghi dữ liệu.

Rủi ro cốt lõi là khoảng trống giữa "lock hết hạn theo TTL" và "client thực sự dừng thao tác": nếu client A giữ lock để ghi file, bị full GC pause 40 giây trong khi TTL chỉ 30 giây, lock của A hết hạn, client B lấy được lock và bắt đầu ghi — sau đó A tỉnh dậy từ GC pause và tiếp tục ghi phần còn lại của thao tác cũ, tưởng mình vẫn còn lock hợp lệ. Kết quả là cả A và B cùng ghi vào cùng một resource, đúng thứ mà distributed lock được sinh ra để ngăn chặn. Giải pháp đúng đắn là dùng fencing token — một số nguyên tăng dần do lock service cấp mỗi lần cấp lock (không phải do client tự sinh), gửi kèm mọi thao tác ghi tới resource cuối (storage, database); resource đó từ chối thao tác nào có token nhỏ hơn token lớn nhất đã thấy, nên dù A "tưởng" mình còn giữ lock, thao tác ghi với token cũ của A vẫn bị chặn ở tầng cuối cùng.

## Production Architecture

Trong một hệ thống xử lý thanh toán chạy nhiều instance API để chịu tải, thao tác "khóa số dư tài khoản để trừ tiền" thường dùng Redis lock với key theo `account_id`, TTL ngắn (vài giây) đủ cho thao tác trừ tiền hoàn tất, kèm retry-with-backoff nếu không lấy được lock ngay — đây là cơ chế nhanh, chấp nhận rủi ro TTL thấp để đổi lấy latency thấp cho thao tác không critical tuyệt đối (vì tầng database bên dưới vẫn có `SELECT ... FOR UPDATE` làm lớp bảo vệ thứ hai). Với cron job dạng "chỉ một node được chạy", nhiều đội dùng Kubernetes Lease object (dựa trên etcd, có fencing token tự nhiên qua `resourceVersion`) thay vì tự cài Redlock, vì etcd vốn được thiết kế cho leader election với đảm bảo mạnh hơn Redis đơn thuần. Ở hệ thống cần đảm bảo mạnh (ví dụ hàng đợi xử lý giao dịch tài chính không được xử lý trùng), distributed lock thường chỉ là lớp tối ưu hóa (giảm việc thừa, giảm contention) chứ không phải lớp đảm bảo đúng đắn duy nhất — đúng đắn thực sự đến từ idempotency key ở tầng nghiệp vụ (unique constraint trên `transaction_id` trong database) kết hợp với fencing token, để dù lock có sai sót, hệ thống vẫn không xử lý trùng.

## Trade-offs

- Redlock đánh đổi độ phức tạp vận hành (phải chạy N instance Redis độc lập, không dùng chung cluster) để lấy độ tin cậy cao hơn một instance đơn — nhưng vẫn không loại bỏ hoàn toàn rủi ro do dựa vào wall clock thay vì logical clock.
- Lock với TTL ngắn giảm rủi ro "lock treo mãi" khi client chết nhưng tăng rủi ro "lock hết hạn giữa chừng" khi thao tác chạy lâu hơn dự kiến (GC pause, network hiccup); TTL dài giảm rủi ro đó nhưng tăng thời gian resource bị khóa nếu client thực sự chết.
- Dùng ZooKeeper/etcd cho lock (ephemeral node, session-based) đảm bảo mạnh hơn Redis TTL-based (lock tự giải phóng khi session mất kết nối, có fencing token tự nhiên) nhưng vận hành nặng hơn và latency cao hơn so với một lệnh `SET NX` trên Redis.
- Thêm fencing token giải quyết đúng vấn đề TTL hết hạn giữa chừng nhưng đòi hỏi mọi thành phần ghi vào resource cuối cùng (database, storage, API bên thứ ba) phải hỗ trợ kiểm tra token — nhiều hệ thống legacy hoặc API bên thứ ba không hỗ trợ, khiến fencing chỉ áp dụng được một phần.

## Best Practices

- Luôn đặt TTL cho lock (không bao giờ dùng lock vô thời hạn) và ước lượng TTL dựa trên p99 latency thực tế của thao tác, cộng thêm biên an toàn, thay vì đoán mò.
- Dùng fencing token (số tăng dần) khi thao tác được bảo vệ có khả năng gây hậu quả không thể hoàn tác (ghi file, trừ tiền, gọi API bên thứ ba tính phí) — đừng tin tưởng tuyệt đối vào TTL.
- Unlock bằng script atomic (Lua trên Redis) kiểm tra đúng chủ sở hữu lock trước khi xóa, tránh xóa nhầm lock của client khác đã lấy được sau khi TTL hết hạn.
- Coi distributed lock là lớp tối ưu hóa giảm contention, không phải lớp đảm bảo tính đúng đắn duy nhất — luôn có idempotency key hoặc unique constraint ở tầng dữ liệu làm lớp bảo vệ cuối cùng.
- Với nhu cầu leader election hoặc lock cần đảm bảo mạnh (tài chính, hạ tầng), ưu tiên etcd/ZooKeeper (session-based, có fencing tự nhiên) thay vì tự cài Redlock từ đầu.

## Common Mistakes

- Coi Redlock là giải pháp "chắc chắn đúng" mà không nhận ra nó vẫn có thể sai khi có GC pause hoặc clock drift dài hơn TTL — không phải Redlock kém, mà là bản chất TTL-based lock có giới hạn.
- Set TTL quá ngắn so với thời gian thực thi thực tế của thao tác dưới tải cao, khiến lock hết hạn giữa chừng thường xuyên và nhiều node cùng chạy vào critical section tưởng chừng đã được bảo vệ.
- Unlock bằng cách xóa key trực tiếp (`DEL key`) mà không kiểm tra chủ sở hữu, dẫn tới xóa nhầm lock của client khác đã lấy được lock sau khi TTL của mình hết hạn.
- Không xử lý trường hợp lấy lock thất bại một cách rõ ràng (retry vô hạn không backoff, hoặc bỏ qua lỗi và chạy tiếp mà không có lock) — biến lock từ cơ chế bảo vệ thành điểm mù.
- Dùng distributed lock như là lớp đảm bảo đúng đắn duy nhất cho thao tác tài chính hoặc không thể hoàn tác, mà không có idempotency key hoặc fencing token ở tầng dưới để chặn thao tác trùng khi lock thất bại.

## Interview Questions

**Hỏi**: Redlock hoạt động như thế nào và vì sao cần quorum trên nhiều Redis instance thay vì chỉ dùng một instance?

**Trả lời**: Client tuần tự thử `SET NX PX` trên N instance Redis độc lập (thường N=5), và chỉ coi là giữ được lock nếu thành công trên đa số (ví dụ 3/5) trong khi tổng thời gian còn lại của TTL vẫn dương. Dùng nhiều instance với quorum giúp hệ thống chịu được một vài instance chết hoặc mất kết nối mà vẫn hoạt động đúng, tương tự cơ chế quorum trong Raft/Paxos, thay vì phụ thuộc vào một điểm lỗi duy nhất là một instance Redis đơn.

**Hỏi**: Vì sao TTL-based lock (kể cả Redlock) không đủ để bảo vệ hoàn toàn một thao tác ghi quan trọng, và fencing token giải quyết vấn đề đó như thế nào?

**Trả lời**: Vì TTL dựa vào wall clock — nếu client bị GC pause hoặc network delay lâu hơn TTL, lock hết hạn và bị client khác lấy trong khi client cũ vẫn tưởng mình còn giữ lock và tiếp tục ghi, gây ghi trùng. Fencing token là số tăng dần do lock service cấp mỗi lần lock được trao, gửi kèm mọi thao tác ghi tới resource cuối; resource đó từ chối token nhỏ hơn token lớn nhất đã thấy, nên dù client cũ "tưởng" còn giữ lock, thao tác của nó vẫn bị chặn ở điểm ghi cuối cùng.

**Hỏi**: Khi nào nên dùng ZooKeeper/etcd thay vì Redis cho distributed lock?

**Trả lời**: Khi cần đảm bảo mạnh hơn TTL-based (session-based lock tự giải phóng ngay khi client mất kết nối thay vì chờ hết TTL cố định) và cần fencing token tự nhiên (như `resourceVersion` trong etcd hoặc zxid trong ZooKeeper), đặc biệt cho leader election hoặc hạ tầng quan trọng. Đổi lại phải chấp nhận latency cao hơn và vận hành phức tạp hơn so với một lệnh `SET NX` trên Redis, nên với lock ngắn hạn, tần suất cao, ưu tiên throughput thấp thì Redis vẫn phù hợp hơn.

## Summary

Distributed lock giải quyết bài toán mutex trong bộ nhớ không đủ dùng khi hệ thống chạy trên nhiều process/node, bằng cách đưa trạng thái khóa ra một nơi mọi node cùng thấy được như Redis, ZooKeeper hoặc etcd. Redlock mở rộng cơ chế lock đơn instance sang quorum trên nhiều instance độc lập để tăng độ tin cậy, nhưng vẫn dựa vào TTL theo wall clock nên không loại bỏ hoàn toàn rủi ro lock hết hạn giữa chừng do GC pause hay network delay. Rủi ro cốt lõi là khoảng trống giữa thời điểm lock hết hạn theo đồng hồ và thời điểm client thực sự dừng thao tác, cách xử lý đúng đắn là fencing token ở tầng ghi dữ liệu cuối cùng chứ không chỉ tin vào TTL. Trong production, distributed lock nên được coi là lớp tối ưu giảm contention, còn tính đúng đắn tuyệt đối phải đến từ idempotency key hoặc unique constraint ở tầng dữ liệu.

## Knowledge Graph

- ACID / Isolation Levels — locking trong database là dạng lock cục bộ một instance, distributed lock mở rộng khái niệm này ra nhiều node.
- Idempotency Key — lớp bảo vệ bổ sung cần thiết vì distributed lock một mình không đảm bảo tuyệt đối tránh xử lý trùng.
- Leader Election / Raft, Paxos — cơ chế quorum nền tảng mà Redlock mô phỏng, nhưng dùng logical clock thay vì wall clock.
- Fencing Token — cơ chế khắc phục rủi ro lock hết hạn giữa chừng, cần thiết cho thao tác ghi không thể hoàn tác.
- Deadlock — rủi ro liên quan khi nhiều node cùng cố lấy nhiều lock theo thứ tự khác nhau trong hệ phân tán.
- CAP Theorem — Redlock và ZooKeeper/etcd nằm ở các điểm khác nhau trên trục consistency-availability khi instance bị mất kết nối.

## Five Things To Remember

- Mutex trong bộ nhớ không có tác dụng qua nhiều process/node — cần lock nằm ở nơi mọi node cùng thấy.
- TTL của lock phải lớn hơn thời gian thực thi thực tế cộng biên an toàn, không phải số đoán mò.
- Lock hết hạn giữa chừng do GC pause hay network delay là rủi ro thật, không phải trường hợp hiếm gặp lý thuyết.
- Fencing token chặn thao tác ghi trùng ở điểm ghi cuối cùng, TTL một mình không đủ để đảm bảo điều đó.
- Distributed lock là lớp tối ưu giảm contention, idempotency key ở tầng dữ liệu mới là lớp đảm bảo đúng đắn cuối cùng.
