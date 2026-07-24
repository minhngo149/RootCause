---
id: image-layer-caching
title: Image Layer Caching
tags: ["docker"]
---

# Image Layer Caching

> Status: Draft

## Problem

Một Dockerfile viết cẩu thả — `COPY . .` ngay đầu file, rồi mới `RUN npm install` hay `RUN pip install -r requirements.txt` — khiến mọi lần sửa một dòng code, dù chỉ đổi log message, đều buộc Docker build lại từ cache miss ở bước copy, kéo theo cài lại toàn bộ dependency từ đầu. Trên CI, một build lẽ ra chỉ mất 10-15 giây (khi cache hit đúng chỗ) kéo dài thành 3-5 phút vì `npm install` hoặc `pip install` chạy lại full network round-trip mỗi lần, nhân với hàng chục lần push/PR mỗi ngày trên một team trung bình. Vấn đề không nằm ở Docker chậm, mà ở việc thứ tự instruction trong Dockerfile vô tình vô hiệu hóa cơ chế cache vốn đã có sẵn.

## Pain Points

- CI pipeline chậm gấp 10-20 lần so với khả năng thực tế vì mỗi commit đều trigger cài lại dependency từ đầu, kéo dài thời gian chờ deploy và làm giảm tần suất release trong ngày.
- Chi phí CI runner (GitHub Actions minutes, GitLab CI compute, self-hosted runner CPU) tăng tuyến tính theo số lần build không cache được — với team chạy hàng trăm build/ngày, phần lớn chi phí compute là lãng phí cho việc tải lại cùng một bộ dependency.
- Image size phình to vì mỗi `RUN apt-get install` riêng lẻ không dọn cache (`apt-get clean`, `rm -rf /var/lib/apt/lists/*`) tạo ra layer chứa rác vĩnh viễn nằm trong image, ngay cả khi lệnh dọn dẹp có ở layer sau — layer trước đã ghi dữ liệu vào rồi.
- Multi-stage build viết sai thứ tự khiến cache của stage build (biên dịch, compile) bị miss liên tục dù source code business logic không đổi, chỉ vì một file cấu hình ít khi thay đổi lại nằm phía trên trong Dockerfile.
- Debug "tại sao build này chậm bất thường" tốn hàng giờ nếu không hiểu cơ chế cache theo layer, vì log build không tự nhiên chỉ ra layer nào invalidate cache và vì sao.

## Solution

Docker image được xây dựng như một chồng layer (layer stack), mỗi instruction trong Dockerfile (`FROM`, `RUN`, `COPY`, `ADD`, đôi khi `ENV`/`ARG`) tạo ra đúng một layer chỉ-đọc (read-only), và layer đó được cache lại dựa trên nội dung của chính instruction cộng với layer cha của nó. Khi build lại, Docker daemon so khớp từng instruction với cache có sẵn theo thứ tự từ trên xuống — hễ một layer cache miss, mọi layer phía sau nó trong Dockerfile buộc phải build lại dù nội dung của chúng không hề thay đổi. Do đó nguyên tắc cốt lõi để tận dụng cache là: đặt các instruction ít thay đổi (cài hệ điều hành, cài dependency) lên trước, và các instruction hay thay đổi (copy source code, build application) xuống cuối — tách biệt rõ "cái gì ổn định" khỏi "cái gì thay đổi liên tục".

## How It Works

Docker build engine (cả builder cũ dựa trên `docker build` lẫn BuildKit hiện là default từ Docker 23+) xử lý Dockerfile tuần tự theo từng instruction, và với mỗi instruction, nó tính một cache key dựa trên: (1) layer cha ngay phía trên (parent layer ID/digest), và (2) chính instruction đó. Với `RUN`, cache key dựa trên chuỗi lệnh (string command) y hệt về mặt ký tự — chỉ cần đổi một khoảng trắng hay thứ tự flag cũng đủ tạo cache miss vì Docker không phân tích ngữ nghĩa lệnh, nó chỉ so sánh chuỗi. Với `COPY`/`ADD`, cache key dựa trên checksum (thường là nội dung + metadata như permission, timestamp tùy driver) của toàn bộ file/thư mục được copy — nếu bất kỳ file nào trong tập hợp đó thay đổi, kể cả một file không liên quan tới logic build, layer bị coi là miss.

Khi một layer bị cache miss, Docker không chỉ build lại đúng layer đó — nó build lại **mọi layer phía sau** trong Dockerfile, vì mỗi layer sau dựa trên layer trước làm parent, và parent đã đổi digest thì con không thể tái sử dụng cache cũ (cache key của layer con luôn bao gồm parent digest). Đây là lý do thứ tự instruction quan trọng tuyệt đối: một `COPY package.json .` đặt trước `RUN npm install` và trước `COPY . .` (copy toàn bộ source) đảm bảo `npm install` chỉ chạy lại khi `package.json`/`package-lock.json` đổi, chứ không phải mỗi khi bất kỳ file `.js` nào trong source đổi.

BuildKit bổ sung thêm cơ chế cache mount (`RUN --mount=type=cache,target=/root/.npm`) — một dạng cache nằm ngoài layer chính thức của image, được giữ lại giữa các lần build trên cùng build node, tách biệt hoàn toàn khỏi cơ chế layer cache truyền thống. Điều này giải quyết trường hợp package manager cache (npm cache, pip cache, Go module cache) cần được giữ lại kể cả khi layer `RUN npm install` bị miss (vì `package.json` đổi), tránh phải tải lại toàn bộ dependency graph từ registry mỗi lần chỉ vì thêm một package mới. BuildKit cũng hỗ trợ cache export/import qua registry (`--cache-to`, `--cache-from` với `type=registry`), cho phép chia sẻ cache giữa các CI runner khác nhau — điều mà cache cục bộ trên một máy build đơn lẻ không làm được.

## Production Architecture

Trong một pipeline CI/CD thực tế build image Node.js hoặc Python cho microservice, Dockerfile production thường tách rõ theo pattern: layer OS base và system dependency (`apt-get install`) ở trên cùng vì gần như không đổi; sau đó copy riêng file khai báo dependency (`package.json`/`package-lock.json`, `requirements.txt`, `go.mod`/`go.sum`, `pom.xml`) và chạy install ngay sau đó — layer này chỉ invalidate khi dependency thực sự thay đổi (vài lần/tuần), không phải mỗi commit; cuối cùng mới `COPY . .` toàn bộ source code và chạy build/compile. Với multi-stage build (build stage dùng image nặng có compiler/toolchain, final stage chỉ copy artifact sang base image nhẹ như `distroless` hoặc `alpine`), pattern cache tương tự áp dụng độc lập cho từng stage — build stage cache hit giúp skip toàn bộ bước compile nếu source không đổi, còn final stage build rất nhanh vì chỉ có vài lệnh `COPY --from=builder`. Trên CI runner dùng self-hosted hoặc GitHub Actions với `docker/build-push-action`, cache thường được đẩy lên registry (`--cache-to=type=registry,ref=<image>:buildcache`) để runner ephemeral (không giữ local disk giữa các job) vẫn tận dụng được cache từ lần build trước, thay vì luôn build from scratch — đây là khác biệt giữa build 8 phút và build 40 giây cho cùng một service không đổi dependency.

## Trade-offs

- Tách nhiều `COPY`/`RUN` nhỏ để tối ưu cache giúp build nhanh hơn khi cache hit, nhưng tăng số lượng layer trong image — mỗi layer có overhead metadata riêng, và với driver storage cũ (không dùng OverlayFS2 hiệu quả) số layer quá nhiều có thể làm chậm thao tác pull/extract image trên production node.
- Cache mount (`--mount=type=cache`) tăng tốc install dependency đáng kể, nhưng cache đó không nằm trong image cuối cùng và không tự động đồng bộ giữa các build node khác nhau nếu không cấu hình cache export rõ ràng — dễ tạo cảm giác "máy tôi build nhanh, máy CI build chậm" vì cache cục bộ không portable.
- Cache theo checksum nội dung file (`COPY`) chính xác tuyệt đối (không bao giờ dùng cache sai khi nội dung thực sự đổi) nhưng cũng nghiêm ngặt tới mức một thay đổi không liên quan (sửa comment, đổi timestamp file do một tool linter chạy qua) vẫn có thể vô tình invalidate cache nếu vô tình nằm trong cùng thư mục được copy sớm.
- Multi-stage build giảm image size cuối cùng đáng kể nhưng làm Dockerfile phức tạp hơn để đọc và debug — xác định chính xác layer nào trong stage nào gây cache miss đòi hỏi hiểu rõ cấu trúc từng stage, không đơn giản như Dockerfile một stage.
- Cache registry export/import (`--cache-to`/`--cache-from`) giải quyết vấn đề chia sẻ cache giữa runner ephemeral, nhưng tốn thêm băng thông push/pull cache layer tới registry mỗi lần build, và nếu registry chậm hoặc rate-limit, lợi ích tốc độ build có thể bị bù trừ bởi thời gian đẩy/kéo cache.

## Best Practices

- Copy file khai báo dependency (`package.json`, `requirements.txt`, `go.mod`) và chạy install trước khi `COPY . .` toàn bộ source code, để layer install chỉ invalidate khi dependency thực sự đổi.
- Gộp các lệnh liên quan trong cùng một `RUN` bằng `&&` khi chúng luôn cần chạy cùng nhau (ví dụ `apt-get update && apt-get install -y ... && rm -rf /var/lib/apt/lists/*`) để tránh layer trung gian giữ lại rác không thể dọn ở layer sau.
- Dùng multi-stage build để tách toolchain compile (nặng, nhiều layer) khỏi runtime image cuối cùng (nhẹ, ít layer, ít bề mặt tấn công bảo mật).
- Dùng `.dockerignore` loại bỏ file không cần thiết (`node_modules`, `.git`, file build output cũ) khỏi context copy vào image — vừa giảm kích thước context gửi lên Docker daemon, vừa tránh những file hay đổi (như log, artifact tạm) vô tình nằm trong tập hợp được `COPY` sớm và làm cache miss oan.
- Với CI dùng runner ephemeral, cấu hình cache export/import qua registry (`--cache-to`/`--cache-from` với BuildKit) thay vì chỉ dựa vào local Docker cache, để cache có thể tái sử dụng giữa các runner khác nhau.

## Common Mistakes

- `COPY . .` toàn bộ source code ngay từ đầu Dockerfile, trước cả bước install dependency — biến mọi thay đổi code, dù nhỏ, thành cache miss cho toàn bộ pipeline install phía sau.
- Dùng `ADD` thay vì `COPY` cho file thông thường (không phải remote URL hay tarball cần tự giải nén) — `ADD` có hành vi phụ (tự extract tarball, tự tải URL) không cần thiết và làm cache behavior khó dự đoán hơn `COPY`.
- Đặt `RUN apt-get update` và `RUN apt-get install` ở hai layer riêng biệt — nếu layer `update` được cache lại từ trước còn layer `install` build mới, danh sách package index cũ có thể không khớp với package thực sự cần cài, gây lỗi "package not found" khó tái hiện.
- Không dùng `.dockerignore`, khiến context build (và đôi khi cả các file được `COPY .` sớm) chứa `node_modules`, `.git`, log file — những thứ thay đổi liên tục và làm cache miss dù logic ứng dụng không đổi gì.
- Tin rằng build cache luôn đúng và bỏ qua trường hợp cache giả (stale cache) khi base image (`FROM node:18`) được cập nhật ở registry nhưng tag không đổi — build vẫn dùng digest cache cũ, bỏ lỡ bản vá bảo mật quan trọng của base image cho tới khi chủ động `docker pull` hoặc `--no-cache`/`--pull`.

## Interview Questions

**Hỏi**: Tại sao đổi thứ tự `COPY package.json .` lên trước `COPY . .` lại giúp build nhanh hơn đáng kể?

**Trả lời**: Vì cache key của mỗi layer phụ thuộc vào layer cha cộng nội dung chính layer đó — nếu `COPY package.json .` đứng trước và chạy `npm install` ngay sau, layer đó chỉ invalidate khi `package.json` đổi. Nếu `COPY . .` (toàn bộ source) đứng trước `npm install`, bất kỳ thay đổi nào trong source code, kể cả không liên quan tới dependency, cũng làm layer copy đó miss, kéo theo `npm install` phải chạy lại từ đầu dù dependency không hề đổi.

**Hỏi**: Khi một layer bị cache miss, điều gì xảy ra với các layer phía sau nó trong Dockerfile?

**Trả lời**: Toàn bộ layer phía sau đều buộc phải build lại, bất kể nội dung của chúng có thay đổi hay không — vì cache key của mỗi layer bao gồm digest của layer cha, và layer cha đã đổi digest do bị miss thì mọi layer con không còn cách nào tái sử dụng cache cũ được nữa.

**Hỏi**: `RUN --mount=type=cache` của BuildKit khác gì so với cơ chế layer cache thông thường?

**Trả lời**: Cache mount tạo ra một vùng lưu trữ (thường dùng cho package manager cache như npm/pip/Go module cache) tồn tại độc lập với layer chính thức của image, được giữ lại giữa các lần build trên cùng build node mà không trở thành một phần của image cuối cùng. Nó giải quyết trường hợp layer install bị cache miss (vì dependency file đổi) nhưng vẫn muốn tránh tải lại toàn bộ package từ registry, khác với layer cache vốn chỉ tái sử dụng toàn bộ kết quả khi cache key khớp y hệt.

## Summary

Mỗi instruction trong Dockerfile tạo ra một layer, và Docker cache từng layer dựa trên nội dung của chính nó cộng với digest của layer cha — cache hit chỉ xảy ra khi toàn bộ chuỗi từ đầu Dockerfile tới layer đó không đổi. Một layer cache miss kéo theo mọi layer phía sau buộc phải build lại, nên nguyên tắc cốt lõi là đặt instruction ổn định (cài OS, cài dependency) lên trước và instruction hay đổi (copy source, build code) xuống sau. Multi-stage build và cache mount của BuildKit là hai công cụ bổ sung: cái đầu tách toolchain nặng khỏi runtime image, cái sau giữ cache package manager độc lập với layer cache chính thức. Hiểu đúng cơ chế này biến build time từ vài phút xuống vài chục giây trên một pipeline CI chạy hàng trăm lần mỗi ngày, và là kỹ năng cơ bản nhưng thường bị bỏ qua khi viết Dockerfile cho production.

## Knowledge Graph

- Multi-stage Build — kỹ thuật tách build stage và runtime stage, áp dụng cache riêng cho từng stage để vừa tối ưu tốc độ vừa giảm image size.
- BuildKit — build engine hiện đại của Docker, bổ sung cache mount và cache export/import qua registry vượt ngoài cơ chế layer cache truyền thống.
- .dockerignore — cơ chế loại bỏ file khỏi build context, ảnh hưởng trực tiếp tới việc `COPY` có vô tình làm cache miss hay không.
- CI/CD Pipeline — nơi image layer caching có tác động lớn nhất về mặt chi phí và tốc độ, vì cùng một Dockerfile được build lặp lại hàng trăm lần mỗi ngày.
- Container Image Registry — nơi lưu trữ cache export khi dùng `--cache-to`/`--cache-from` với BuildKit để chia sẻ cache giữa các build runner ephemeral.
- OverlayFS — storage driver quyết định cách các layer được ghép lại thành filesystem cuối cùng khi container chạy, ảnh hưởng tới overhead khi số lượng layer quá nhiều.

## Five Things To Remember

- Mỗi instruction trong Dockerfile là một layer, được cache theo nội dung của chính nó cộng digest layer cha.
- Một layer cache miss kéo theo mọi layer phía sau đều phải build lại, bất kể nội dung có đổi hay không.
- Copy file khai báo dependency và install trước, copy toàn bộ source code sau cùng.
- `RUN --mount=type=cache` giữ cache package manager độc lập với layer cache, không nằm trong image cuối cùng.
- `.dockerignore` ngăn file hay đổi (node_modules, .git, log) vô tình làm cache miss oan.
