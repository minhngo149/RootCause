---
id: rate-limit
title: "Rate Limiting (Security Angle)"
tags: ["security", "backend"]
---

# Rate Limiting (Security Angle)

> Status: Draft

## Problem

Một endpoint `/login` hoặc `/api/reset-password` không giới hạn số request sẽ nhận bao nhiêu request tùy ý từ bất kỳ client nào. Kẻ tấn công lợi dụng điều này để thử hàng chục nghìn mật khẩu mỗi phút (brute-force) hoặc thử lại danh sách cặp email/password rò rỉ từ vụ leak khác (credential stuffing), và không có gì ở tầng ứng dụng ngăn cản việc đó ngoài chính giới hạn vật lý của băng thông kẻ tấn công. Đây không phải lỗi logic nghiệp vụ mà là thiếu một lớp phòng thủ độc lập với logic xác thực.

## Pain Points

- Tài khoản user bị chiếm quyền do brute-force mật khẩu yếu hoặc credential stuffing từ data breach nơi khác, dẫn đến khiếu nại, mất niềm tin, và đôi khi trách nhiệm pháp lý (GDPR, PCI-DSS).
- Endpoint tốn tài nguyên (search, export báo cáo, gửi OTP/SMS) bị gọi lặp vô hạn gây DoS thực chất mà không cần botnet lớn — chỉ một script đơn giản.
- Chi phí vận hành tăng vọt với API tính phí theo lượt gọi bên thứ ba (SMS OTP, email, payment gateway) khi bị lạm dụng gửi request hàng loạt.
- Đội SRE nhận alert CPU/DB connection pool cạn kiệt vào giữa đêm mà nguyên nhân gốc chỉ là một IP gọi API tìm kiếm 500 lần/giây, không phải traffic thật.

## Solution

Rate limiting giới hạn số lượng request mà một client (theo IP, user ID, API key, hoặc tổ hợp) được phép thực hiện trong một khoảng thời gian nhất định, và từ chối (HTTP 429) các request vượt ngưỡng. Về mặt bảo mật, đây là lớp phòng thủ độc lập với logic xác thực — nó không quan tâm mật khẩu đúng hay sai, chỉ quan tâm tần suất, nên vẫn hiệu quả kể cả khi attacker có credential hợp lệ (ví dụ chiếm được token nhưng lạm dụng nó).

## How It Works

Hai thuật toán phổ biến nhất là **token bucket** và **sliding window**, khác nhau ở cách xử lý burst và độ chính xác.

**Token bucket**: mỗi client có một "bucket" chứa tối đa N token, được nạp lại với tốc độ cố định (vd. 10 token/giây). Mỗi request tiêu tốn 1 token; nếu bucket rỗng, request bị từ chối. Cơ chế này cho phép burst tức thời lên đến sức chứa bucket (vd. cho phép 50 request dồn trong 1 giây nếu bucket đã đầy từ trước), sau đó throttle về tốc độ nạp lại ổn định. Redis triển khai phổ biến bằng Lua script atomic (kiểm tra + trừ token trong một lệnh duy nhất để tránh race condition) hoặc dùng lệnh `INCR` + `EXPIRE` đơn giản hơn cho biến thể fixed window.

**Sliding window** (sliding window log hoặc sliding window counter): thay vì reset counter cứng theo mốc thời gian cố định (fixed window có lỗ hổng ở biên — client có thể gửi N request vào cuối window rồi N request nữa ngay đầu window kế tiếp, thực chất là 2N request trong khoảng thời gian ngắn hơn một window), sliding window tính toán số request trong một cửa sổ thời gian trượt liên tục tính từ thời điểm hiện tại lùi lại. Sliding window log lưu timestamp từng request (chính xác nhưng tốn bộ nhớ), còn sliding window counter nội suy tuyến tính giữa window hiện tại và window trước đó (xấp xỉ, tiết kiệm bộ nhớ hơn nhiều, đủ chính xác cho hầu hết use case).

Token bucket phù hợp khi cần cho phép burst hợp lệ (user thao tác nhanh một loạt request rồi nghỉ); sliding window phù hợp khi cần giới hạn chặt và đều đặn, tránh chính xác lỗ hổng ở biên window mà attacker có thể khai thác có chủ đích.

## Production Architecture

Trong một hệ thống auth thực tế, rate limit được áp dụng nhiều tầng chồng lên nhau: tầng edge/CDN (Cloudflare, AWS WAF) chặn theo IP với ngưỡng thô để hấp thụ volumetric attack trước khi vào hạ tầng; tầng API gateway (Kong, Envoy, hoặc middleware tự viết) áp rate limit theo API key/user ID cho từng endpoint với ngưỡng tinh hơn (vd. `/login` giới hạn 5 request/phút/IP và đồng thời 10 request/phút/username để chặn cả tấn công phân tán theo IP lẫn tấn công tập trung vào một tài khoản); tầng ứng dụng dùng Redis (do có TTL và atomic operations native) làm store trung tâm để các instance stateless chia sẻ counter, tránh tình trạng mỗi instance tự đếm riêng dẫn đến giới hạn thực tế cao gấp N lần số instance. Với các endpoint nhạy cảm như login, reset password, OTP, kiến trúc production thường kết hợp thêm progressive delay (mỗi lần sai tăng dần thời gian chờ) và CAPTCHA sau một ngưỡng thất bại nhất định, thay vì chỉ chặn cứng.

## Trade-offs

Rate limit theo IP dễ bị bypass bởi attacker dùng botnet phân tán hàng nghìn IP khác nhau, mỗi IP gọi vài request dưới ngưỡng — lúc này rate limit theo IP gần như vô dụng và phải kết hợp thêm tín hiệu khác (device fingerprint, hành vi bất thường). Ngưỡng đặt quá chặt gây false positive chặn nhầm user thật dùng chung NAT/proxy công ty (nhiều user sau cùng một IP public); đặt quá lỏng thì không còn tác dụng phòng thủ. Sliding window log chính xác nhưng tốn bộ nhớ tuyến tính theo số request, không scale tốt ở traffic cao; sliding window counter tiết kiệm hơn nhưng là xấp xỉ, có sai số nhỏ chấp nhận được. Rate limit phân tán qua Redis tạo thêm một điểm phụ thuộc (nếu Redis chậm hoặc down, cần quyết định fail-open hay fail-closed — fail-closed an toàn hơn về bảo mật nhưng có thể chặn nhầm toàn bộ traffic hợp lệ khi Redis gặp sự cố).

## Best Practices

- Áp rate limit theo nhiều chiều cùng lúc (IP, user ID, API key) cho endpoint nhạy cảm, không chỉ dựa vào một chiều duy nhất.
- Trả về `429 Too Many Requests` kèm header `Retry-After` để client hợp lệ (và cả bên tích hợp API) biết chính xác khi nào thử lại, không đoán mò retry ngay.
- Với login/reset password, kết hợp rate limit với progressive delay và khóa tạm thời tài khoản sau ngưỡng thất bại, không chỉ chặn theo IP.
- Đặt rate limit ở tầng gateway/edge trước khi request chạm vào tầng ứng dụng và DB, để hấp thụ tấn công sớm nhất có thể, giảm tải cho hạ tầng phía sau.
- Theo dõi tỷ lệ request bị 429 như một metric bảo mật, vì tăng đột biến là tín hiệu sớm của một đợt tấn công đang diễn ra, không chỉ là nhiễu.

## Common Mistakes

- Chỉ rate limit theo IP mà quên rate limit theo username/account, khiến credential stuffing phân tán qua nhiều IP vẫn lọt qua dễ dàng.
- Dùng fixed window ngây thơ (reset counter theo mốc phút chẵn) mà không nhận ra lỗ hổng ở biên window cho phép gấp đôi lượng request thực tế trong thời gian ngắn.
- Đặt rate limit chỉ ở tầng ứng dụng, không có tầng edge/CDN phía trước, nên volumetric attack vẫn làm nghẽn hạ tầng trước khi tới được logic chặn.
- Mỗi instance ứng dụng tự đếm counter độc lập (in-memory) thay vì dùng store tập trung (Redis), khiến giới hạn thực tế cao gấp N lần số instance đang chạy.
- Không phân biệt rate limit chống lạm dụng (an toàn) với rate limit chỉ để giảm tải (capacity) — dẫn đến ngưỡng đặt sai mục đích, quá lỏng cho bảo mật hoặc quá chặt cho trải nghiệm người dùng bình thường.

## Interview Questions

**Hỏi**: Token bucket và sliding window khác nhau ở điểm nào, khi nào dùng cái nào?

**Trả lời**: Token bucket cho phép burst tức thời lên đến sức chứa bucket rồi throttle về tốc độ nạp lại ổn định, phù hợp khi cần dung sai cho hành vi dùng dồn dập hợp lệ. Sliding window tính số request trong cửa sổ thời gian trượt liên tục, tránh lỗ hổng ở biên của fixed window, phù hợp khi cần giới hạn đều đặn và chặt chẽ hơn, đặc biệt cho các endpoint nhạy cảm về bảo mật như login.

**Hỏi**: Vì sao rate limit theo IP không đủ để chống credential stuffing?

**Trả lời**: Credential stuffing hiện đại dùng botnet với hàng nghìn IP khác nhau, mỗi IP chỉ gửi vài request nên không vượt ngưỡng rate limit theo IP. Cần kết hợp thêm rate limit theo username/tài khoản đích (nhiều IP cùng nhắm vào một username vẫn bị chặn) và các tín hiệu khác như device fingerprint hoặc bất thường về hành vi đăng nhập.

**Hỏi**: Nếu Redis lưu rate limit counter bị down, hệ thống nên fail-open hay fail-closed?

**Trả lời**: Tùy mức độ nhạy cảm của endpoint. Với endpoint nhạy cảm bảo mật cao (login, payment), nên fail-closed (chặn tạm thời) để tránh cửa sổ hở cho brute-force trong lúc Redis down. Với endpoint ít nhạy cảm hơn, fail-open (cho qua) để tránh outage toàn hệ thống do một dependency phụ trợ gặp sự cố — đây là quyết định đánh đổi cụ thể theo rủi ro, không có câu trả lời chung cho mọi trường hợp.

## Summary

Rate limiting là lớp phòng thủ giới hạn tần suất request theo client, độc lập với logic xác thực, chống lại brute-force, credential stuffing và DoS ở tầng ứng dụng. Token bucket cho phép burst rồi throttle về tốc độ ổn định, còn sliding window tránh lỗ hổng ở biên của fixed window bằng cách tính số request trong cửa sổ thời gian trượt liên tục. Trong production, rate limit cần áp ở nhiều tầng (edge, gateway, ứng dụng) và nhiều chiều (IP, user, API key) cùng lúc vì một chiều duy nhất luôn có cách bypass. Đánh đổi chính nằm ở độ chính xác so với chi phí bộ nhớ, và ở quyết định fail-open/fail-closed khi store trung tâm (Redis) gặp sự cố.

## Knowledge Graph

- Retry Storm — client bị 429 mà retry ngay lập tức không backoff có thể tự tạo ra một dạng retry storm ngược lại lên hệ thống.
- Credential Stuffing — mối đe dọa cụ thể mà rate limit theo username/tài khoản được thiết kế để chặn.
- API Gateway — tầng hạ tầng phổ biến nhất triển khai rate limit tập trung cho nhiều service phía sau.
- Distributed Locking — cùng dùng Redis atomic operations làm nền tảng kỹ thuật, khác mục đích.
- CAPTCHA / Progressive Delay — lớp phòng thủ bổ sung thường đi kèm rate limit cho endpoint đăng nhập nhạy cảm.
- DoS/DDoS Mitigation — rate limit là một công cụ trong bộ giải pháp chống DoS rộng hơn, không phải giải pháp duy nhất.

## Five Things To Remember

- Rate limit là lớp phòng thủ độc lập với logic xác thực, chặn theo tần suất chứ không quan tâm request đúng hay sai.
- Token bucket cho phép burst rồi throttle ổn định; sliding window tránh lỗ hổng ở biên của fixed window.
- Rate limit chỉ theo IP không đủ chống credential stuffing phân tán qua botnet, cần thêm chiều theo username/tài khoản.
- Dùng store tập trung (Redis) cho counter khi có nhiều instance, tránh mỗi instance tự đếm riêng làm giới hạn thực tế bị nhân lên.
- 429 tăng đột biến là tín hiệu bảo mật cần theo dõi chủ động, không chỉ là nhiễu vận hành bình thường.
