---
id: container-resource-limits
title: "Container Resource Limits"
tags: ["docker"]
---

# Container Resource Limits

> Status: Draft

## Problem

Mặc định, một container Docker chạy trên host không có bất kỳ giới hạn CPU/memory nào — nó có thể dùng toàn bộ CPU và memory của host y như một process bình thường. Engineer thường lầm tưởng "container" tự thân đã là một môi trường cách ly về tài nguyên, trong khi thực chất container chỉ cách ly về namespace (filesystem, network, PID...) chứ không tự động giới hạn resource trừ khi chủ động khai báo `--memory`/`--cpus`. Hệ quả là một container bị memory leak hoặc một job tính toán nặng có thể âm thầm chiếm hết RAM/CPU của cả host, kéo theo mọi container khác trên cùng máy — kể cả container không liên quan gì tới sự cố — bị ảnh hưởng hoặc bị kernel kill.

## Pain Points

- Một container không có memory limit bị leak dần theo thời gian, chiếm hết RAM host, kernel OOM killer can thiệp và có thể kill nhầm container khác (kể cả process quan trọng hơn) thay vì thủ phạm, vì OOM killer chọn theo `oom_score`, không theo "ai gây ra vấn đề".
- Một container batch/report chạy CPU-bound không giới hạn chiếm hết CPU host, khiến các container API đang phục vụ traffic thật bị chậm lại đột ngột dù chính chúng không có lỗi gì — noisy neighbor trên cùng một máy Docker host.
- Container bị OOMKilled (exit code 137) giữa chừng, không kịp flush log buffer hay chạy graceful shutdown, khiến log cuối cùng trước khi crash biến mất — sự cố trông như "container tự nhiên biến mất" mà không có traceback nào để debug.
- Chi phí vận hành tăng khi nhiều team chạy nhiều container trên cùng một VM mà không giới hạn gì, dẫn tới phải mua VM lớn hơn nhiều so với tổng nhu cầu thực tế chỉ vì một vài container "tham lam" ngẫu nhiên.
- Trong CI/CD, một container build/test chiếm hết memory runner khiến các job song song khác trên cùng runner bị OOMKilled ngẫu nhiên — lỗi flaky rất khó tái hiện vì phụ thuộc vào việc job nào đang chạy cùng lúc.

## Solution

Docker giới hạn tài nguyên container bằng cách cấu hình trực tiếp **cgroups** (control groups) của Linux kernel thông qua các flag khi chạy container: `--memory` (hard cap RAM), `--memory-swap`, `--cpus` (số CPU core tương đương), `--cpu-shares` (tỷ trọng CPU tương đối khi tranh chấp). Docker daemon không tự implement cơ chế giới hạn — nó chỉ là lớp API tiện lợi để ghi các giá trị này vào file cgroup tương ứng, còn việc thực thi (enforce) hoàn toàn do kernel đảm nhiệm. Hiểu đúng cgroups là hiểu đúng bản chất: container không phải VM có giới hạn tài nguyên "tự nhiên", giới hạn đó phải được khai báo tường minh và được kernel — không phải Docker — thực thi.

## How It Works

**cgroups là gì**: mỗi container khi khởi chạy được Docker (qua containerd/runc) gán vào một cgroup riêng — một nhóm process mà kernel áp dụng giới hạn tài nguyên chung. Với cgroup v2 (mặc định trên các distro hiện đại: Ubuntu 22.04+, Docker 20.10+ có hỗ trợ), các giới hạn được viết vào file ảo dưới `/sys/fs/cgroup/<container-id>/`, ví dụ `memory.max`, `cpu.max`. Với cgroup v1 (hệ thống cũ hơn), tương ứng là `memory.limit_in_bytes` và `cpu.cfs_quota_us`/`cpu.cfs_period_us` dưới `/sys/fs/cgroup/memory/docker/<container-id>/`.

**Memory limit và OOMKilled**: khi chạy `docker run --memory=512m`, Docker ghi `536870912` vào `memory.max` (cgroup v2) của container đó. Memory, khác với CPU, không thể "throttle" — không có khái niệm chờ tới chu kỳ sau. Khi container cố cấp phát (allocate) vượt quá con số này, kernel OOM killer được kích hoạt ngay trong phạm vi cgroup đó, chọn process có điểm `oom_score_adj` cao nhất bên trong container (thường là process PID 1 của container, hoặc process ăn nhiều memory nhất nếu dùng multi-process) và gửi `SIGKILL`. Container dừng ngay lập tức với exit code 137 (128 + signal 9), không chạy được cleanup handler, không kịp flush buffer log ra stdout — đây là lý do `docker logs` sau một lần OOMKilled thường thiếu dòng log cuối cùng lẽ ra phải giải thích nguyên nhân. Chạy `docker inspect <container> --format='{{.State.OOMKilled}}'` là cách xác nhận chắc chắn nguyên nhân dừng là do vượt memory limit chứ không phải crash logic.

**CPU limit và CFS quota**: `--cpus=1.5` được Docker chuyển thành `cpu.max` dạng `150000 100000` (cgroup v2) — nghĩa là container được phép dùng tối đa 150000 microsecond CPU time trong mỗi period 100000 microsecond (100ms), tương đương 1.5 core. Linux CFS (Completely Fair Scheduler) theo dõi CPU time container đã tiêu thụ trong period hiện tại; nếu chạm quota trước khi period kết thúc, mọi thread trong container bị **throttle** — không được cấp thêm CPU time cho tới period tiếp theo bắt đầu. Khác với memory, vượt CPU limit không giết container, chỉ làm nó chạy chậm lại — nhưng nếu container multi-threaded burst dùng gần hết quota trong vài millisecond đầu period, nó có thể bị đóng băng phần lớn thời gian còn lại dù usage trung bình cả giây vẫn thấp, biểu hiện ra ngoài là latency tăng theo chu kỳ khó dò bằng biểu đồ CPU usage trung bình theo phút.

**`--cpu-shares` khác `--cpus`**: `--cpus` là giới hạn tuyệt đối (hard cap), còn `--cpu-shares` (mặc định 1024) chỉ là tỷ trọng tương đối, chỉ có tác dụng khi CPU trên host thực sự bị tranh chấp (nhiều container cùng cần CPU cùng lúc). Một container với `--cpu-shares=2048` không bị giới hạn gì nếu host còn CPU rảnh — nó chỉ được ưu tiên gấp đôi container `--cpu-shares=1024` khác khi cả hai cùng tranh giành CPU tại cùng thời điểm. Nhầm lẫn hai cơ chế này là lỗi phổ biến: đặt `cpu-shares` cao rồi tưởng đã "giới hạn" được container, trong khi nó vẫn có thể chiếm 100% CPU host lúc không có ai tranh chấp.

**`--memory-swap` và swap accounting**: nếu chỉ đặt `--memory` mà không đặt `--memory-swap`, Docker mặc định cho phép container dùng thêm lượng swap bằng đúng memory limit (tổng gấp đôi). Đặt `--memory-swap` bằng đúng `--memory` để tắt hoàn toàn swap cho container — cần thiết với ứng dụng nhạy latency vì swap I/O chậm hơn RAM hàng nghìn lần, một khi container bắt đầu swap, latency tăng đột biến trước khi kịp bị OOMKilled.

## Production Architecture

Một Docker host chạy nhiều container cho các service khác nhau (ví dụ một VM đơn lẻ chạy app backend, Redis cache, và một cron job xử lý batch định kỳ) cần đặt `--memory` cho từng container dựa trên profile usage thực tế: Redis được cấp memory limit sát với `maxmemory` cấu hình trong chính Redis (cộng thêm overhead cho fork khi RDB save) để tránh OOM killer can thiệp trước khi Redis tự evict key theo policy của nó; batch job được giới hạn `--cpus` thấp hơn số core thực của host để không cạnh tranh CPU với service đang phục vụ traffic. Trong CI/CD chạy trên self-hosted runner dùng Docker để build/test song song nhiều job, mỗi container job cần `--memory` rõ ràng để một test suite ngốn RAM không kéo sập các job khác chạy cùng runner — nhiều pipeline thất bại ngẫu nhiên (flaky) hóa ra là do container khác trên cùng máy bị OOMKilled chứ không phải lỗi test. Với `docker-compose`, giới hạn này khai báo qua `deploy.resources.limits` (Compose v3+, chỉ có tác dụng khi chạy dưới Swarm mode) hoặc field cấp cũ hơn `mem_limit`/`cpus` (chạy được trực tiếp với `docker compose up` không cần Swarm) — dễ nhầm lẫn vì cùng file Compose nhưng field nào có hiệu lực phụ thuộc vào việc có Swarm hay không.

## Trade-offs

- Đặt memory limit chặt để tránh một container chiếm hết RAM host, nhưng đặt quá sát usage thực tế biến một đợt tăng traffic hoặc một GC pause bình thường thành sự cố OOMKilled không lường trước.
- Không đặt CPU limit giúp container burst nhanh khi cần (ví dụ warm-up cache lúc khởi động), nhưng đánh đổi bằng rủi ro nó chiếm hết CPU host trong lúc burst, ảnh hưởng container khác — nhiều production setup cố tình không giới hạn CPU cứng mà chỉ dùng `cpu-shares` để giữ khả năng burst mà vẫn công bằng khi tranh chấp.
- Tắt swap hoàn toàn (`--memory-swap` = `--memory`) giúp tránh latency spike do swap I/O, nhưng cũng nghĩa là container chạm limit sẽ bị OOMKilled ngay lập tức thay vì có một khoảng đệm chạy chậm bằng swap trước khi chết — đánh đổi giữa "chết nhanh, dễ debug" và "chạy chậm, có cơ hội tự phục hồi".
- Cgroup v1 và v2 có cách tính toán và file cấu hình khác nhau; container image/tooling cũ giả định cgroup v1 (ví dụ đọc trực tiếp `/sys/fs/cgroup/memory/memory.limit_in_bytes` để tự tính heap size cho JVM) có thể đọc sai hoặc lỗi hoàn toàn trên host đã chuyển sang cgroup v2 nếu không kiểm tra kỹ.
- Giới hạn tài nguyên per-container trên một Docker host đơn lẻ không có cơ chế "đặt chỗ" (reservation) như Kubernetes request — hai container cùng đặt limit hợp lý riêng lẻ vẫn có thể tranh chấp CPU/memory thực tế nếu tổng limit vượt quá tài nguyên vật lý host, vì Docker không kiểm tra tổng limit so với capacity host trước khi cho phép chạy thêm container.

## Best Practices

- Luôn đặt `--memory` cho container chạy production, kể cả khi chưa chắc chắn con số chính xác — thiếu nó, một memory leak có thể kéo sập cả host qua kernel OOM killer chọn nhầm nạn nhân.
- Đo usage thực tế qua `docker stats` hoặc metrics dài hạn (cAdvisor/Prometheus) trước khi chốt giá trị limit, thay vì đoán một lần rồi giữ nguyên — usage pattern khác nhau đáng kể giữa service I/O-bound và CPU-bound.
- Đặt `--memory-swap` bằng `--memory` (tắt swap) cho service nhạy latency; để mặc định (cho phép swap) chỉ với batch job chấp nhận chạy chậm hơn là bị kill giữa chừng.
- Kiểm tra `docker inspect --format='{{.State.OOMKilled}}'` như bước đầu tiên khi container dừng bất thường với exit code 137, trước khi đi tìm bug trong logic ứng dụng.
- Với ứng dụng chạy trên JVM/Node cũ, xác nhận runtime có nhận diện đúng cgroup memory limit hay không (`-XX:+UseContainerSupport` cho JVM 10+, biến môi trường cho Node) — runtime không nhận diện được sẽ tự cấp phát heap theo RAM toàn host thay vì theo container limit, gây OOMKilled dù trông như "vẫn còn heap".

## Common Mistakes

- Tin rằng container tự động bị cô lập tài nguyên chỉ vì chạy trong Docker — không có `--memory`/`--cpus`, container dùng tài nguyên không giới hạn như một process thường trên host.
- Đặt `--cpu-shares` cao và nhầm tưởng đây là giới hạn cứng, trong khi nó chỉ là tỷ trọng tương đối, không có tác dụng gì nếu host không có tranh chấp CPU tại thời điểm đó.
- Đặt memory limit bằng đúng usage trung bình quan sát qua `docker stats` mà không chừa margin cho spike hoặc GC pause, dẫn tới OOMKilled định kỳ mỗi khi traffic tăng nhẹ.
- Copy-paste giá trị `--memory`/`--cpus` giữa các service khác nhau mà không đo lại — service I/O-bound và CPU-bound có usage pattern hoàn toàn khác, số liệu phù hợp cho service này có thể gây throttling hoặc OOMKilled cho service khác.
- Nhầm lẫn cấu hình `mem_limit` trong Docker Compose thường (có hiệu lực ngay) với `deploy.resources.limits` (chỉ có hiệu lực khi chạy dưới Swarm mode) — set nhầm field khiến limit không được áp dụng dù file YAML "trông đúng".

## Interview Questions

**Hỏi**: Docker giới hạn CPU/memory của container bằng cơ chế nào ở tầng kernel?

**Trả lời**: Bằng cgroups (control groups) của Linux. Docker (qua containerd/runc) ghi giá trị `--memory`/`--cpus` vào file cgroup tương ứng (`memory.max`, `cpu.max` ở cgroup v2). Kernel — không phải Docker daemon — là bên thực thi giới hạn này: memory vượt limit kích hoạt OOM killer, CPU vượt quota bị CFS scheduler throttle.

**Hỏi**: Tại sao container bị OOMKilled không throttle chậm lại như CPU mà bị kill ngay lập tức?

**Trả lời**: Vì memory không có khái niệm "trả lại theo thời gian" như CPU cycle. Khi container cố cấp phát vượt `memory.max`, kernel buộc phải giải phóng RAM ngay để tránh toàn hệ thống cạn kiệt memory, nên OOM killer chọn process có `oom_score_adj` cao nhất trong cgroup và gửi SIGKILL tức thì — không có cơ chế "đợi period sau" như CPU throttling.

**Hỏi**: Container resource limit của Docker khác gì so với `resources.requests`/`resources.limits` của Kubernetes?

**Trả lời**: Về cơ chế thực thi (enforcement) là giống hệt nhau — cả hai đều cấu hình cùng một cgroups của kernel Linux, `docker run --memory` và K8s `limits.memory` cuối cùng đều ghi vào cùng file cgroup. Khác biệt nằm ở tầng trên: Docker chỉ có "limit" (chặn trần khi chạy), không có khái niệm "request" dùng để đặt chỗ trước khi chạy — mỗi Docker host tự chạy container độc lập, không có bộ lập lịch nào kiểm tra tổng limit so với capacity trước khi cho phép chạy thêm. Kubernetes thêm hẳn một tầng `requests` dùng bởi kube-scheduler để quyết định Pod được xếp vào node nào (bind-time decision, không liên quan runtime), cộng với QoS Class (Guaranteed/Burstable/BestEffort) suy ra từ cách khai báo request/limit để quyết định thứ tự evict khi node thiếu tài nguyên — những khái niệm này không tồn tại ở tầng Docker đơn lẻ.

## Summary

Container Docker không tự động bị giới hạn tài nguyên — engineer phải chủ động khai báo `--memory`/`--cpus`, giá trị này được Docker ghi trực tiếp vào cgroups của kernel Linux và do kernel thực thi, không phải Docker daemon. Memory limit dẫn tới OOMKilled (SIGKILL tức thì, exit code 137) khi vượt trần vì memory không thể throttle được; CPU limit dẫn tới throttling qua CFS quota, làm container chạy chậm lại theo chu kỳ 100ms mặc định thay vì bị kill. `--cpu-shares` chỉ là tỷ trọng tương đối khi tranh chấp, khác hoàn toàn với `--cpus` là giới hạn cứng — nhầm lẫn hai khái niệm này là lỗi phổ biến. So với Kubernetes, Docker chỉ có tầng "limit" runtime mà thiếu tầng "request" dùng cho scheduling và QoS Class dùng cho quyết định evict — hai khái niệm đó là phần Kubernetes bổ sung lên trên cùng cơ chế cgroups nền tảng.

## Knowledge Graph

- Resource Requests & Limits (Kubernetes) — kế thừa cùng cơ chế cgroups nhưng thêm tầng request cho scheduling và QoS Class cho eviction mà Docker đơn lẻ không có.
- cgroups & CFS Scheduler — cơ chế Linux nền tảng Docker dựa vào để thực thi cả CPU throttling lẫn memory hard limit.
- OOM Killer — thành phần kernel chọn và kill process khi cgroup/hệ thống vượt memory limit, quyết định dựa trên `oom_score_adj` chứ không theo "ai gây lỗi".
- Docker Compose `deploy.resources` — cách khai báo resource limit ở tầng orchestration nhẹ, chỉ có hiệu lực đầy đủ dưới Swarm mode.
- Container Runtime (runc/containerd) — lớp thực thi trung gian chuyển flag Docker CLI thành thao tác ghi cgroup thực tế.
- Linux Namespaces — cơ chế cách ly khác của container (filesystem, network, PID), dễ bị nhầm với resource limiting nhưng phục vụ mục đích hoàn toàn khác (cô lập view, không giới hạn tài nguyên).

## Five Things To Remember

- Container không tự động bị giới hạn tài nguyên — phải khai báo tường minh `--memory`/`--cpus`, nếu không nó dùng tài nguyên host không giới hạn.
- Memory vượt limit gây OOMKilled tức thì (SIGKILL, exit code 137) vì memory không thể throttle; CPU vượt limit chỉ bị throttle chậm lại qua CFS quota.
- `--cpu-shares` là tỷ trọng tương đối khi tranh chấp, không phải giới hạn cứng như `--cpus` — hai cơ chế khác nhau hoàn toàn.
- `docker inspect --format='{{.State.OOMKilled}}'` là bước đầu tiên cần kiểm tra khi container dừng bất thường với exit code 137.
- Docker chỉ có "limit" runtime; Kubernetes thêm tầng "request" cho scheduling và QoS Class cho eviction trên cùng nền cgroups.
