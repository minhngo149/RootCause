---
id: multi-stage-builds
title: "Multi-stage Builds"
tags: ["docker"]
---

# Multi-stage Builds

> Status: Draft

## Problem

Khi một Dockerfile chỉ dùng một `FROM` duy nhất, toàn bộ toolchain cần để build ứng dụng — compiler, dev dependencies, header files, source code, cache của package manager — đều nằm chung layer với binary/artifact chạy production. Với ứng dụng Go, image cuối cùng vẫn kéo theo Go compiler (hàng trăm MB) dù runtime chỉ cần một binary tĩnh vài chục MB. Với Node.js, `node_modules` chứa cả devDependencies (webpack, babel, test framework) bị đóng gói chung với `dependencies` cần cho production, vì `npm install` chạy một lần duy nhất trong cùng stage build. Vấn đề không chỉ là kích thước image phình to — mà là mọi thứ cần để *biên dịch* mã nguồn (bao gồm cả source code gốc, đôi khi cả secret dùng để fetch private package) đều tồn tại vĩnh viễn trong image chạy production, làm tăng attack surface và làm image trở nên khó audit.

## Pain Points

- Image production nặng gấp 5-10 lần mức cần thiết vì mang theo compiler, build tool, cache của package manager (`~/.m2`, `~/.npm`, `pip cache`) — kéo theo thời gian `docker pull` khi deploy chậm, đặc biệt với rolling update trên cluster nhiều node.
- Toolchain build (gcc, make, dev headers) tồn tại trong image production là attack surface thừa: một CVE trên compiler hoặc dev library không ảnh hưởng runtime nhưng vẫn bị security scanner (Trivy, Grype) gắn cờ, làm image fail compliance gate dù rủi ro thực tế bằng không.
- Source code đầy đủ (bao gồm comment, biến môi trường debug, đôi khi cả file `.env` copy nhầm) nằm nguyên trong image cuối, ai có quyền `docker pull` hoặc `docker save` đều đọc được toàn bộ logic nghiệp vụ, kể cả phần không nên lộ ra ngoài.
- Build cache và layer chứa credential tạm thời (SSH key để clone private repo, token để fetch private npm package) nếu không tách stage đúng cách sẽ bị "đông cứng" vào layer image, kéo theo `docker history`/`docker inspect` có thể lộ ra dù Dockerfile "trông có vẻ" đã xoá key ở bước sau — vì layer trước đó vẫn giữ nguyên trong image.
- Chi phí lưu trữ registry và băng thông CI/CD tăng tuyến tính với số lượng image build mỗi ngày, khi mỗi image production đều cõng theo hàng trăm MB toolchain không bao giờ được chạy.

## Solution

Multi-stage build cho phép một Dockerfile chứa nhiều khối `FROM ... AS <tên-stage>`, mỗi stage là một build context độc lập với filesystem riêng, nhưng stage sau có thể dùng `COPY --from=<tên-stage>` để lấy có chọn lọc chỉ những file cần thiết (binary đã compile, thư mục `node_modules` production, static asset đã build) từ stage trước sang, mà không mang theo bất kỳ thứ gì khác của stage đó. Kết quả là image cuối cùng chỉ chứa runtime base (thường là distro tối giản hoặc `scratch`/`distroless`) cộng với đúng artifact cần chạy — toàn bộ compiler, dev dependencies, source code trung gian bị bỏ lại ở các stage build trước, không xuất hiện trong image được push lên registry.

## How It Works

**Build context và layer độc lập theo từng stage**: mỗi khối `FROM` khởi tạo một filesystem hoàn toàn mới bắt đầu từ base image được chỉ định — không kế thừa bất kỳ layer nào của stage trước, kể cả khi cùng dùng một Dockerfile. Docker build engine (BuildKit) xây dựng một DAG (directed acyclic graph) giữa các stage: stage nào không được stage khác tham chiếu tới qua `COPY --from` hoặc không phải target cuối cùng (`docker build --target`) sẽ không được build nếu không cần thiết, và BuildKit có thể build song song các stage độc lập không phụ thuộc nhau để giảm thời gian build tổng thể.

**`COPY --from` chỉ copy đúng phần được chỉ định**: cú pháp `COPY --from=builder /app/dist /usr/share/nginx/html` chỉ lấy đúng đường dẫn `/app/dist` từ filesystem của stage `builder` tại thời điểm nó build xong, ghi thành layer mới trong stage hiện tại — không copy lịch sử layer, không copy build cache, không copy biến môi trường hay `ENV`/`ARG` đã set ở stage nguồn. Đây là điểm khác biệt cốt lõi so với việc build một image rồi `docker cp` file ra ngoài: `COPY --from` xảy ra ngay trong quá trình build, kết quả cuối chỉ có duy nhất các layer của stage cuối cùng cộng với những gì được copy vào, layer của các stage trước hoàn toàn không tồn tại trong image output.

**Chỉ layer của final stage được push/lưu**: khi build xong, Docker chỉ gắn tag và lưu layer thuộc chuỗi kế thừa của stage cuối cùng (hoặc stage được chỉ định bằng `--target`); các stage trung gian tồn tại trong build cache cục bộ (để tái sử dụng cho lần build sau, tăng tốc qua cache layer không đổi) nhưng không nằm trong image được đẩy lên registry. Đây là lý do một binary Go 15MB build từ base `golang:1.22` (hơn 800MB) vẫn cho ra image cuối chỉ khoảng 20-30MB nếu stage cuối dùng `alpine` hoặc `scratch` — toàn bộ 800MB của Go toolchain chỉ tồn tại trong stage `builder`, không bao giờ đi vào image cuối.

**`--target` cho phép dừng build ở một stage bất kỳ**: `docker build --target builder .` build và dừng lại ở stage tên `builder`, hữu ích để tạo image debug (có đầy đủ toolchain, dùng để `exec` vào chạy test hoặc profiling) tách biệt hoàn toàn khỏi image production build từ cùng Dockerfile bằng `docker build --target production .` (hoặc không chỉ định target, mặc định lấy stage cuối cùng). Cùng một file Dockerfile phục vụ được cả nhu cầu debug lẫn production mà không cần duy trì hai file riêng.

**Secret không rò rỉ nếu dùng đúng BuildKit secret mount**: nếu SSH key hoặc token được truyền vào qua `ARG`/`ENV` rồi `RUN` lệnh dùng nó ở một stage trung gian, giá trị đó vẫn bị ghi vào layer cache của stage đó (dù stage đó không được copy sang final image, `docker history` trên image trung gian hoặc build cache export vẫn có thể lộ). Cách đúng là dùng `RUN --mount=type=secret,id=github_token` của BuildKit — secret được mount vào filesystem tạm thời trong quá trình chạy lệnh, không bao giờ được ghi vào layer, biến mất ngay sau khi `RUN` kết thúc.

## Production Architecture

Trong CI/CD pipeline build image Go hoặc Rust, Dockerfile thường có ba stage: stage `deps` cache riêng bước tải dependency (`go mod download`, `cargo fetch`) để tận dụng Docker layer cache — chỉ invalidate khi file `go.mod`/`Cargo.toml` đổi, không phải mỗi khi source code đổi; stage `builder` kế thừa từ `deps`, copy source code vào và compile ra binary tĩnh (`CGO_ENABLED=0 go build`); stage cuối `FROM gcr.io/distroless/static` hoặc `FROM scratch`, chỉ `COPY --from=builder /app/bin/server /server` và set `ENTRYPOINT`. Image cuối không có shell, không có package manager, không có bất kỳ binary nào ngoài chính ứng dụng — giảm đáng kể attack surface và khiến các lỗ hổng CVE của OS layer (vốn chiếm phần lớn cảnh báo từ security scanner) không còn áp dụng vì OS layer không tồn tại.

Với ứng dụng frontend (React/Vue build ra static asset), stage đầu dùng `node:20` để chạy `npm ci && npm run build` sinh ra thư mục `dist/`, stage cuối `FROM nginx:alpine` chỉ `COPY --from=build /app/dist /usr/share/nginx/html` — toàn bộ Node.js runtime, `node_modules`, source code TypeScript/JSX gốc không hề tồn tại trong image production, image cuối chỉ là Nginx phục vụ static file với kích thước vài chục MB thay vì gần 1GB nếu build và serve cùng một image Node.

Ở tầng platform engineering, nhiều tổ chức chuẩn hoá một base Dockerfile template dùng multi-stage cho toàn bộ service viết cùng ngôn ngữ, đưa vào internal registry để mọi team dùng chung — đảm bảo mọi image production đều đi qua cùng một quy trình strip toolchain, cùng một base image tối giản đã được security team audit, thay vì mỗi team tự viết Dockerfile theo cách riêng và có nguy cơ để lộ toolchain hoặc secret khác nhau.

## Trade-offs

- Image production nhỏ và an toàn hơn đáng kể, nhưng Dockerfile phức tạp hơn để đọc và maintain — engineer mới cần hiểu rõ ranh giới giữa các stage, dễ copy nhầm đường dẫn hoặc quên copy một file runtime cần thiết (ví dụ quên `COPY --from=builder /app/config` khiến container thiếu file cấu hình lúc chạy, chỉ phát hiện khi container start fail).
- `scratch`/`distroless` giảm attack surface tối đa nhưng không có shell, không có package manager — debug production sự cố (không `exec` vào container để chạy `curl`/`cat`/`ps`) khó hơn nhiều, buộc phải dựa hoàn toàn vào log và metrics, hoặc build riêng một image debug từ stage trung gian có toolchain đầy đủ.
- Build cache theo layer giúp build nhanh hơn khi lặp lại, nhưng nếu thứ tự `COPY`/`RUN` trong stage `deps` không tách biệt đúng (ví dụ copy toàn bộ source code trước khi cài dependency), cache bị invalidate liên tục, build chậm đi thay vì nhanh lên — multi-stage không tự động tối ưu cache, vẫn cần thiết kế đúng thứ tự.
- Nhiều stage đồng nghĩa CI runner cần đủ tài nguyên và thời gian để chạy toàn bộ pipeline build (kể cả các stage không nằm trong final image nhưng vẫn cần build để tạo artifact) — với monorepo nhiều service, tổng thời gian build tăng nếu không tận dụng BuildKit cache export giữa các lần chạy CI.
- Việc tối ưu kích thước image tới mức tối đa (dùng `scratch`, static binary) đòi hỏi ứng dụng phải tương thích (Go với `CGO_ENABLED=0`, Rust với `musl` target) — không phải ngôn ngữ/runtime nào cũng dễ dàng build ra static binary, với Python hay Java, "runtime tối giản" thực tế vẫn cần base image có interpreter/JVM, giới hạn mức giảm kích thước có thể đạt được.

## Best Practices

- Tách stage `deps` riêng để cache dependency download, copy `go.mod`/`package.json`/`requirements.txt` trước rồi mới copy toàn bộ source code — tận dụng Docker layer cache, tránh re-download dependency mỗi lần source đổi.
- Dùng base image tối giản cho stage cuối (`distroless`, `alpine`, hoặc `scratch` nếu binary tĩnh hoàn toàn) — chỉ giữ lại đúng những gì runtime cần, không mang theo shell/package manager nếu không thực sự cần debug trực tiếp trong container.
- Không bao giờ truyền secret qua `ARG`/`ENV` rồi `RUN` sử dụng nó — dùng `RUN --mount=type=secret` của BuildKit để secret không bị ghi vào layer cache dưới bất kỳ hình thức nào.
- Đặt tên rõ ràng cho từng stage (`AS deps`, `AS builder`, `AS production`) thay vì để mặc định đánh số, giúp `COPY --from` và `--target` dễ đọc, dễ maintain khi Dockerfile có nhiều stage.
- Build và scan cả image debug (target trung gian có toolchain) lẫn image production (target cuối) trong CI, đảm bảo image production thực sự nhỏ và không lẫn toolchain thừa trước khi push lên registry.

## Common Mistakes

- Copy toàn bộ source code (`COPY . .`) trước khi chạy `npm install`/`go mod download`, khiến mọi thay đổi source dù nhỏ đều invalidate cache dependency, build chậm không cần thiết dù đã dùng multi-stage.
- Dùng `COPY --from=builder /app /app` copy nguyên thư mục thay vì chỉ định đúng file/thư mục cần thiết, vô tình mang theo file build trung gian, cache, hoặc source code không cần cho runtime vào image cuối — làm mất một phần lợi ích giảm kích thước của multi-stage.
- Truyền private registry token hoặc SSH key qua `ARG` để `git clone`/`npm install` private package ở stage `builder`, tin rằng vì stage đó không được copy sang final image nên secret "an toàn" — thực tế secret vẫn nằm trong build cache và có thể lộ qua `docker history` hoặc cache export nếu không dùng `RUN --mount=type=secret`.
- Build production image với target là `scratch` cho ứng dụng cần chứng chỉ TLS hệ thống hoặc timezone data, quên copy `ca-certificates`/`tzdata` từ stage builder sang, dẫn tới lỗi kết nối HTTPS hoặc sai timezone lúc runtime mà rất khó nhận ra nguyên nhân vì image build và chạy vẫn thành công.
- Không set `--target` rõ ràng trong CI, vô tình build và push nhầm stage debug (có shell, có toolchain, có thể có source code đầy đủ) lên production registry thay vì stage runtime tối giản.

## Interview Questions

**Hỏi**: Multi-stage build giải quyết vấn đề gì mà một Dockerfile một stage không giải quyết được?

**Trả lời**: Một Dockerfile một stage buộc toolchain build (compiler, dev dependencies, source code trung gian) phải tồn tại chung layer với artifact chạy production, vì mọi lệnh đều chạy trên cùng một filesystem kế thừa từ một `FROM` duy nhất. Multi-stage cho phép tách biệt hoàn toàn: stage build compile ra artifact, stage runtime chỉ `COPY --from` đúng artifact cần thiết sang một base image tối giản khác — kết quả image cuối không mang theo bất kỳ thứ gì của quá trình build, giảm kích thước và attack surface đáng kể.

**Hỏi**: Vì sao dùng `ARG`/`ENV` để truyền secret vào một stage build trung gian vẫn có thể gây rò rỉ, dù stage đó không được copy sang image cuối?

**Trả lời**: Vì mỗi `RUN` trong stage đó vẫn tạo ra layer cache lưu lại trạng thái filesystem tại thời điểm chạy lệnh, bao gồm biến môi trường được set qua `ARG`/`ENV` nếu nó được ghi ra file hoặc log trong quá trình build. Layer đó tồn tại trong build cache cục bộ và có thể bị export/inspect (`docker history`, cache backend chia sẻ giữa CI runner), dù không xuất hiện trong image cuối cùng được push. Cách an toàn là dùng `RUN --mount=type=secret` của BuildKit, secret chỉ tồn tại tạm thời trong quá trình chạy lệnh và không bao giờ được ghi vào layer.

**Hỏi**: Tại sao tách stage cài dependency (`deps`) riêng khỏi stage copy source code (`builder`) lại quan trọng cho tốc độ build?

**Trả lời**: Docker build cache invalidate theo layer dựa trên nội dung input của lệnh đó; nếu copy toàn bộ source code trước khi cài dependency, bất kỳ thay đổi nhỏ nào trong source cũng làm layer `COPY . .` đổi, kéo theo mọi layer sau đó (bao gồm cả `npm install`/`go mod download`) bị invalidate và chạy lại từ đầu. Tách riêng stage chỉ copy file khai báo dependency (`package.json`, `go.mod`) và cài đặt trước, rồi mới copy source code, giúp layer cài dependency chỉ invalidate khi file khai báo đó thực sự đổi — build lặp lại nhanh hơn nhiều trong vòng lặp phát triển và CI.

## Summary

Multi-stage build tách một Dockerfile thành nhiều `FROM` độc lập, mỗi stage có filesystem riêng, cho phép stage runtime cuối cùng chỉ lấy đúng artifact cần thiết qua `COPY --from` mà không mang theo toolchain build, dependency phát triển, hay source code trung gian. Cơ chế này dựa trên việc Docker chỉ lưu và push layer thuộc stage cuối cùng (hoặc stage được chỉ định qua `--target`), các stage trước chỉ tồn tại trong build cache cục bộ để tăng tốc build lần sau. Kết quả thực tế là image production nhỏ hơn nhiều lần, ít bề mặt tấn công hơn, và không vô tình để lộ source code hay secret build-time nếu áp dụng đúng kèm `RUN --mount=type=secret`. Đánh đổi chính là Dockerfile phức tạp hơn, image tối giản (`scratch`/`distroless`) khó debug trực tiếp hơn, và cache phải được thiết kế đúng thứ tự mới thực sự tăng tốc build. Kỹ thuật này gần như là mặc định bắt buộc cho mọi service compiled-language chạy production hiện đại.

## Knowledge Graph

- Distroless / scratch base image — base image tối giản thường dùng làm stage runtime cuối trong multi-stage build.
- BuildKit secret mount — cơ chế truyền secret vào build mà không ghi vào layer cache, bắt buộc khi build stage cần credential.
- Docker layer caching — cơ chế cache theo layer mà thứ tự `COPY`/`RUN` trong từng stage phải thiết kế đúng để tận dụng.
- Image vulnerability scanning (Trivy/Grype) — công cụ đánh giá attack surface, hưởng lợi trực tiếp từ việc loại bỏ toolchain build khỏi image production.
- Static binary compilation (CGO_ENABLED=0, musl) — điều kiện kỹ thuật cần có để một binary chạy được trên base image `scratch`.
- Container image registry cost — chi phí lưu trữ/băng thông registry giảm trực tiếp theo kích thước image nhờ multi-stage.

## Five Things To Remember

- Mỗi `FROM` trong Dockerfile mở một stage filesystem hoàn toàn độc lập, không kế thừa layer của stage khác.
- Chỉ layer của stage cuối cùng (hoặc stage chỉ định bằng `--target`) được lưu và push lên registry.
- `COPY --from` chỉ mang đúng file/thư mục được chỉ định sang, không mang theo toolchain hay cache của stage nguồn.
- Không truyền secret qua `ARG`/`ENV` — dùng `RUN --mount=type=secret` để secret không bao giờ bị ghi vào layer.
- Tách stage cài dependency khỏi stage copy source code để tận dụng cache, giảm thời gian build lặp lại.
