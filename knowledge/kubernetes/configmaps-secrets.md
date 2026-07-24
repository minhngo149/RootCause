---
id: configmaps-secrets
title: "ConfigMaps & Secrets"
tags: ["kubernetes"]
---

# ConfigMaps & Secrets

> Status: Draft

## Problem

Một team build image Docker chứa cả file `application.yml` với connection string database, API key bên thứ ba, và feature flag hardcode ngay trong `Dockerfile`. Khi cần đổi endpoint database từ staging sang production, hoặc chỉ đơn giản là bật một feature flag, cách duy nhất là sửa code, build lại image, push lên registry, rồi rollout lại toàn bộ Deployment — một thay đổi cấu hình đáng lẽ mất vài giây lại kéo theo cả một pipeline CI/CD chạy 15-20 phút. Tệ hơn, cùng một image phải build riêng cho từng environment (dev, staging, production) vì config bị đóng cứng bên trong, phá vỡ nguyên tắc "build once, deploy everywhere" — artifact chạy ở production không còn là artifact đã test ở staging.

## Pain Points

- Mỗi lần đổi một giá trị config (timeout, feature flag, endpoint) đều bắt buộc build lại image và rollout lại Pod, biến một thao tác vận hành nhỏ thành một sự kiện deploy đầy rủi ro.
- Secret (mật khẩu database, API key, private key TLS) nằm trong image nghĩa là bất kỳ ai có quyền pull image — kể cả từ registry nội bộ — đều đọc được secret bằng `docker save` và `tar -xf`, không cần truy cập cluster.
- Không thể tách biệt quyền truy cập giữa người được xem config ứng dụng và người được xem secret nhạy cảm, vì cả hai nằm chung trong cùng file, cùng image, cùng quyền RBAC.
- Rotate credential (đổi mật khẩu database định kỳ, xoay API key sau khi rò rỉ) đòi hỏi build và deploy lại toàn bộ ứng dụng thay vì chỉ cập nhật một object cấu hình độc lập.

## Solution

ConfigMap và Secret là hai object Kubernetes tách dữ liệu cấu hình ra khỏi image container, cho phép cùng một image chạy ở nhiều environment khác nhau chỉ bằng cách gắn ConfigMap/Secret khác nhau vào Pod. ConfigMap lưu dữ liệu không nhạy cảm (feature flag, URL endpoint, file cấu hình dạng key-value hoặc toàn bộ nội dung file), còn Secret có cùng cấu trúc API nhưng dành cho dữ liệu nhạy cảm (mật khẩu, token, private key) — về bản chất lưu trữ mặc định, Secret chỉ encode base64 chứ không mã hóa, nên cần thêm một lớp bảo vệ riêng (encryption at rest, KMS, hoặc sealed-secrets) mới thực sự an toàn.

## How It Works

ConfigMap và Secret đều là object lưu trong etcd dưới dạng key-value, được gắn vào Pod qua ba cách: biến môi trường (`envFrom`/`valueFrom.configMapKeyRef`), volume mount (mỗi key trở thành một file trong thư mục mount, nội dung file là value), hoặc command-line argument dựng từ biến môi trường. Khi mount dưới dạng volume, kubelet đồng bộ thay đổi của ConfigMap/Secret xuống Pod thông qua cơ chế watch trên API server — theo mặc định sau khoảng 1 phút (chu kỳ sync của kubelet cộng thời gian TTL cache của Secret/ConfigMap manager), file trong Pod sẽ được cập nhật mà không cần restart container; tuy nhiên ứng dụng bên trong container phải tự đọc lại file đó (qua watch trên filesystem, hoặc reload định kỳ) vì Kubernetes không tự động restart process. Với biến môi trường thì khác hẳn — env var chỉ được set một lần lúc container khởi động, nên đổi ConfigMap/Secret không ảnh hưởng gì tới Pod đang chạy cho tới khi Pod bị recreate.

Về lưu trữ, Secret trong etcd mặc định chỉ base64-encode, không mã hóa — base64 là một encoding có thể decode ngay lập tức bằng `base64 -d`, không phải một cơ chế bảo mật. Bất kỳ ai đọc trực tiếp được dữ liệu etcd (backup etcd không mã hóa, snapshot rò rỉ, hoặc truy cập trái phép vào etcd) đều đọc được toàn bộ Secret dưới dạng gần như plaintext. Để mã hóa thực sự tại rest, phải bật `EncryptionConfiguration` ở API server với provider như `aescbc` hoặc tích hợp KMS (AWS KMS, GCP Cloud KMS, HashiCorp Vault) — khi đó API server mã hóa giá trị Secret trước khi ghi xuống etcd bằng key được quản lý ngoài cluster, và chỉ giải mã lại khi phục vụ request đọc hợp lệ. Về phân quyền, RBAC kiểm soát ai được `get`/`list` Secret ở tầng Kubernetes API, nhưng bất kỳ Pod nào mount được Secret đó vẫn đọc được giá trị plaintext bên trong container của nó — RBAC không ngăn được việc lộ secret nếu một container trong Pod bị compromise.

## Production Architecture

Trong một pipeline GitOps điển hình, ConfigMap chứa file `application.properties` hoặc `nginx.conf` được version-hoá cùng Helm chart hoặc Kustomize overlay, khác nhau giữa từng environment nhưng dùng chung một image tag. Secret thực tế hiếm khi được tạo trực tiếp bằng `kubectl create secret` hay commit vào Git dưới dạng YAML thô — thay vào đó, sealed-secrets (Bitnami) mã hóa Secret bằng public key của controller ngay trên máy dev, cho phép commit bản mã hóa (`SealedSecret`) vào Git một cách an toàn, rồi controller chạy trong cluster giải mã lại thành Secret thường khi apply. Ở quy mô lớn hơn, External Secrets Operator đồng bộ Secret từ một nguồn quản lý tập trung bên ngoài cluster (AWS Secrets Manager, HashiCorp Vault, GCP Secret Manager) vào Kubernetes Secret theo định kỳ, giữ nguồn sự thật (source of truth) nằm ở hệ thống quản lý secret chuyên dụng có audit log, rotation tự động, và versioning — thay vì etcd. Ở tầng runtime, sidecar container của Vault Agent Injector có thể inject secret trực tiếp vào filesystem của Pod dưới dạng file tạm trong `tmpfs`, hoàn toàn không đi qua Kubernetes Secret object, giảm bề mặt tấn công xuống mức tối thiểu.

## Trade-offs

- Mount ConfigMap/Secret qua volume cho phép cập nhật không cần restart Pod, nhưng ứng dụng phải tự implement logic reload — nếu không, thay đổi config nằm im trên file mà process vẫn dùng giá trị cũ đã đọc lúc khởi động.
- Sealed-secrets và External Secrets Operator giải quyết bài toán "Secret trong Git" nhưng thêm một thành phần vận hành mới (controller, đồng bộ định kỳ) có thể là điểm lỗi — nếu controller down, Secret cũ trong cluster vẫn dùng được nhưng rotate mới bị chặn.
- Bật encryption at rest với KMS tăng độ trễ mỗi lần đọc/ghi Secret do phải gọi ra dịch vụ KMS bên ngoài, và nếu KMS key bị mất hoặc revoke, toàn bộ Secret đã mã hóa bằng key đó không thể phục hồi.
- Env var đơn giản, dễ debug (`kubectl exec -- env`), nhưng chính vì dễ đọc như vậy nên cũng dễ vô tình lộ qua log ứng dụng, error trace, hoặc lệnh debug — Secret dạng file mount an toàn hơn về mặt này dù phức tạp hơn để tích hợp.
- Giới hạn kích thước 1MiB cho mỗi ConfigMap/Secret (giới hạn của etcd) buộc phải tách nhỏ nếu cấu hình hoặc certificate bundle quá lớn, tạo thêm object cần quản lý.

## Best Practices

- Không bao giờ commit Secret dạng YAML thô (dù chỉ base64) vào Git — dùng sealed-secrets, External Secrets Operator, hoặc SOPS để mã hóa trước khi lưu trữ.
- Bật `EncryptionConfiguration` với KMS provider cho etcd ngay từ khi thiết lập cluster production, không đợi tới khi có yêu cầu audit hoặc compliance mới bổ sung.
- Dùng volume mount thay vì env var cho dữ liệu cần rotate thường xuyên (credential database, token ngắn hạn), vì volume mount hỗ trợ cập nhật không cần restart.
- Giới hạn quyền RBAC `get`/`list`/`watch` trên Secret ở namespace level, tách riêng service account của từng ứng dụng thay vì dùng chung một service account có quyền đọc mọi Secret trong namespace.
- Rotate Secret định kỳ qua nguồn quản lý tập trung (Vault, AWS Secrets Manager) và để hệ thống tự đồng bộ vào cluster, thay vì rotate thủ công bằng `kubectl edit secret`.

## Common Mistakes

- Coi base64 là mã hóa và yên tâm commit Secret trực tiếp vào Git — bất kỳ ai clone repo đều decode được bằng một lệnh `base64 -d` duy nhất.
- Đổi ConfigMap được mount qua env var và mong Pod tự nhận giá trị mới mà không rollout lại — env var chỉ đọc một lần lúc container start, thay đổi không có tác dụng cho tới khi Pod bị recreate.
- Nhét cả cấu hình không nhạy cảm lẫn credential vào chung một Secret object, khiến không thể áp RBAC chi tiết hoặc audit riêng biệt việc ai đọc phần nào.
- Không bật encryption at rest cho etcd ở cluster production vì nghĩ RBAC đã đủ, quên rằng bất kỳ ai truy cập được etcd backup hoặc snapshot đều đọc được Secret gần như plaintext.
- Log toàn bộ biến môi trường hoặc object cấu hình khi debug (`kubectl describe pod`, log ứng dụng dump env) mà không lọc field nhạy cảm, vô tình đẩy Secret vào hệ thống logging tập trung không có kiểm soát truy cập tương đương.

## Interview Questions

**Hỏi**: Secret trong Kubernetes có thực sự an toàn hơn ConfigMap không?

**Trả lời**: Về mặt lưu trữ mặc định thì không nhiều — cả hai đều lưu trong etcd, và Secret chỉ base64-encode chứ không mã hóa, nên ai đọc được etcd đều đọc được cả hai gần như ở cùng mức độ. Secret an toàn hơn về mặt API và quy ước sử dụng: Kubernetes không log giá trị Secret ra một số nơi mà ConfigMap có thể bị log, RBAC có thể áp policy riêng cho resource type Secret, và các công cụ như sealed-secrets/KMS encryption chỉ tích hợp sẵn cho Secret. Để Secret thực sự an toàn, bắt buộc phải bật thêm encryption at rest hoặc dùng KMS/Vault.

**Hỏi**: Đổi giá trị trong ConfigMap đã mount vào Pod thì ứng dụng có tự động thấy giá trị mới không?

**Trả lời**: Phụ thuộc cách mount. Nếu mount qua volume, kubelet đồng bộ file mới xuống Pod sau khoảng một phút mà không cần restart container, nhưng ứng dụng phải tự đọc lại file (qua watch hoặc reload định kỳ) mới nhận giá trị mới — bản thân Kubernetes không restart process. Nếu inject qua biến môi trường, giá trị chỉ được set một lần lúc container khởi động, nên phải xóa/tạo lại Pod (rollout restart) mới nhận được giá trị mới.

**Hỏi**: Vì sao không nên commit Secret YAML (kể cả đã base64) vào Git, và sealed-secrets giải quyết vấn đề này như thế nào?

**Trả lời**: Base64 chỉ là encoding, không phải mã hóa, nên bất kỳ ai đọc được repo (kể cả trong lịch sử commit đã xóa) đều decode được giá trị gốc bằng một lệnh đơn giản, biến toàn bộ lịch sử Git thành một kho lộ credential. Sealed-secrets mã hóa Secret bằng public key bất đối xứng của controller ngay trên máy dev trước khi commit, tạo ra object `SealedSecret` mà chỉ private key nằm trong controller (chạy trong cluster đích) mới giải mã được — vì vậy commit bản mã hóa vào Git công khai vẫn an toàn, và chỉ cluster có đúng controller đó mới tạo lại được Secret gốc.

## Summary

ConfigMap và Secret tách cấu hình ra khỏi image container, cho phép cùng một artifact chạy ở nhiều environment và cập nhật config mà không cần build lại hay luôn phải restart Pod. Cả hai được kubelet đồng bộ xuống Pod qua volume mount (hỗ trợ cập nhật động) hoặc biến môi trường (chỉ đọc lúc khởi động), và cùng lưu trong etcd dưới dạng key-value. Điểm dễ hiểu nhầm nhất là Secret chỉ base64-encode chứ không mã hóa mặc định — cần bật `EncryptionConfiguration` với KMS hoặc dùng công cụ như sealed-secrets/Vault để có bảo mật thực sự tại rest và khi lưu trong Git. Production nghiêm túc luôn kết hợp thêm một lớp quản lý secret bên ngoài cluster (Vault, AWS Secrets Manager) đồng bộ vào Kubernetes thay vì coi Secret object là nguồn sự thật duy nhất.

## Knowledge Graph

- RBAC — kiểm soát ai được đọc/ghi Secret ở tầng Kubernetes API, nhưng không ngăn được lộ dữ liệu nếu Pod mount Secret bị compromise.
- etcd — nơi lưu trữ vật lý của mọi ConfigMap và Secret, là mục tiêu chính cần bảo vệ bằng encryption at rest.
- Sealed Secrets / External Secrets Operator — công cụ bổ sung lớp mã hóa và đồng bộ để bù đắp việc Secret mặc định chỉ base64.
- Vault (HashiCorp) — hệ thống quản lý secret tập trung bên ngoài cluster, thường được chọn làm source of truth thay vì Kubernetes Secret.
- Deployment & Rollout — cơ chế Pod nhận giá trị env var mới, vì env var chỉ set lúc container khởi động nên cần rollout mới áp dụng thay đổi.
- Service Account — danh tính mà Pod dùng để xác thực với API server, quyết định Pod đó được phép mount những Secret nào.

## Five Things To Remember

- Base64 không phải mã hóa — Secret mặc định chỉ encode, ai đọc được etcd đều đọc được giá trị gốc.
- Volume mount hỗ trợ cập nhật động (khoảng 1 phút), env var chỉ đọc một lần lúc container khởi động.
- Bật `EncryptionConfiguration` với KMS cho etcd là bắt buộc, không phải tùy chọn, ở cluster production.
- Không commit Secret YAML thô vào Git dù đã base64 — dùng sealed-secrets hoặc SOPS để mã hóa trước.
- RBAC kiểm soát truy cập API, nhưng Pod mount được Secret vẫn đọc được plaintext bên trong container của nó.
