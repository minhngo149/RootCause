---
id: grafana
title: Grafana
tags: ["observability"]
---

# Grafana

> Status: Draft

## Problem

Một hệ thống production sinh ra hàng nghìn metric mỗi giây từ Prometheus, CloudWatch, InfluxDB, Loki — nhưng nếu không có lớp visualize thống nhất, engineer phải tự query từng nguồn bằng công cụ riêng (PromQL trên Prometheus UI, console AWS riêng cho CloudWatch, log grep riêng cho Loki) mỗi khi debug incident. Không có dashboard tổng hợp nghĩa là không ai có cái nhìn theo thời gian thực về sức khỏe hệ thống trước khi incident xảy ra, và khi incident thực sự xảy ra, thời gian đầu tiên bị tốn vào việc mở nhiều tab, nhớ lại câu query đúng, và ghép dữ liệu từ nhiều nguồn lại bằng mắt.

## Pain Points

- Không có dashboard chuẩn hóa nghĩa là mỗi engineer tự viết query riêng khi cần xem CPU/latency, dẫn đến số liệu không nhất quán giữa các lần điều tra và không ai tin tưởng hoàn toàn vào con số người khác đưa ra.
- Thời gian phát hiện sự cố (MTTD) tăng đáng kể nếu không có alerting gắn liền với dashboard — team chỉ biết có vấn đề khi khách hàng report, thay vì khi p99 latency vượt ngưỡng 5 phút trước đó.
- Khi on-call phải tự nhớ và gõ lại PromQL phức tạp giữa lúc production đang cháy, tốc độ phản ứng chậm đi rõ rệt so với việc có sẵn dashboard đã build từ trước cho đúng use case đó.
- Không có nơi tổng hợp nhiều nguồn dữ liệu (metrics + logs + traces) trên cùng một timeline khiến việc correlate một spike CPU với một dòng log lỗi cụ thể tốn nhiều thời gian hơn cần thiết, kéo dài MTTR.

## Solution

Grafana là công cụ visualize và dashboard mã nguồn mở, đóng vai trò lớp hiển thị thống nhất (unified visualization layer) phía trên nhiều nguồn dữ liệu khác nhau — phổ biến nhất là Prometheus cho metrics, nhưng cũng hỗ trợ Loki (logs), Tempo/Jaeger (traces), InfluxDB, Elasticsearch, MySQL/PostgreSQL, CloudWatch. Grafana không lưu trữ dữ liệu — nó chỉ query nguồn dữ liệu tại thời điểm render và vẽ lại dưới dạng panel (graph, gauge, table, heatmap), đồng thời cung cấp hệ thống alerting rules để tự động cảnh báo khi giá trị vượt ngưỡng.

## How It Works

Grafana hoạt động theo mô hình datasource-agnostic: mỗi dashboard gồm nhiều panel, mỗi panel gắn với một datasource và một câu query viết bằng ngôn ngữ riêng của datasource đó (PromQL cho Prometheus, LogQL cho Loki, SQL cho MySQL). Khi người dùng mở dashboard, Grafana backend gửi song song các query này tới từng datasource, nhận về dữ liệu dạng time series hoặc table, rồi render tại frontend bằng các plugin panel (Time series, Stat, Gauge, Table, Heatmap...). Vì không lưu dữ liệu, hiệu năng dashboard phụ thuộc hoàn toàn vào tốc độ trả lời của datasource phía sau — một dashboard chậm thường là dấu hiệu Prometheus đang phải quét quá nhiều series (high cardinality) chứ không phải do Grafana.

Alerting trong Grafana (kể từ Grafana 8+, unified alerting) hoạt động độc lập với datasource: engine alerting định kỳ (theo `evaluation_interval`, thường 10-60s) chạy lại câu query của alert rule, so kết quả với điều kiện ngưỡng (vd. `avg() OF query(A, 5m, now) > 0.05`), và nếu điều kiện đúng liên tục qua khoảng thời gian `for` (vd. 5 phút) thì rule chuyển từ trạng thái `Pending` sang `Firing`. Khi `Firing`, Grafana gửi alert tới Alertmanager (nội bộ hoặc Prometheus Alertmanager bên ngoài), nơi xử lý routing, grouping, silencing, và inhibition trước khi đẩy notification ra kênh cuối (Slack, PagerDuty, email, webhook). Cơ chế `for` này quan trọng vì nó tránh alert nhiễu (flapping) do spike tức thời chỉ kéo dài vài giây.

## Production Architecture

Trong một kiến trúc microservices điển hình, mỗi service expose metrics endpoint `/metrics` theo định dạng Prometheus, một Prometheus server scrape định kỳ (15-30s) và lưu vào TSDB cục bộ hoặc remote_write sang Thanos/Mimir để lưu dài hạn. Grafana được deploy như một service riêng, cấu hình Prometheus (và thường cả Loki, Tempo) làm datasource, với các dashboard được version-controlled dưới dạng JSON (provisioning qua file hoặc Terraform provider `grafana_dashboard`) thay vì chỉnh tay qua UI để tránh drift giữa các môi trường. Alert rules được định nghĩa tương tự dưới dạng code (Grafana provisioning YAML hoặc `grafana_rule_group` trong Terraform), route qua Alertmanager tới PagerDuty cho alert mức critical (vd. error rate > 5% trong 5 phút) và Slack cho alert mức warning (vd. disk usage > 80%). Một pattern phổ biến là "RED dashboard" (Rate, Errors, Duration) cho mỗi service làm dashboard mặc định khi on-call mở lên đầu tiên khi có incident, kết hợp với một "USE dashboard" (Utilization, Saturation, Errors) cho tầng hạ tầng (node, pod, disk).

## Trade-offs

Vì Grafana không lưu trữ dữ liệu, hiệu năng và độ chính xác của nó hoàn toàn phụ thuộc vào datasource phía sau — Grafana không thể "cứu" một Prometheus đang bị quá tải cardinality, và một dashboard đẹp vẫn vô dụng nếu metric nguồn bị sai hoặc thiếu. Alerting native của Grafana linh hoạt hơn Prometheus Alertmanager thuần (hỗ trợ multi-datasource alert, mixed queries) nhưng lại phức tạp hơn để vận hành ở quy mô lớn, và có giai đoạn migrate từ legacy alerting sang unified alerting (Grafana 8-9) gây gián đoạn cho nhiều team. Việc cho phép engineer tự tạo dashboard qua UI rất tiện cho khám phá nhanh, nhưng nếu không kiểm soát bằng provisioning-as-code, số lượng dashboard "ad-hoc" tăng không kiểm soát, nhiều cái trùng lặp hoặc lỗi thời, làm giảm lòng tin vào toàn bộ hệ thống dashboard.

## Best Practices

- Định nghĩa dashboard và alert rules dưới dạng code (JSON provisioning, Terraform, hoặc Grafonnet/Jsonnet) và commit vào git, không chỉnh sửa trực tiếp qua UI ở môi trường production để tránh drift và mất lịch sử thay đổi.
- Luôn đặt `for` duration hợp lý (thường 2-5 phút) cho alert rule để tránh flapping do spike tức thời, và tách rõ mức độ nghiêm trọng (`severity: critical/warning`) để route đúng kênh (PagerDuty vs Slack).
- Xây dashboard theo mô hình phân tầng: overview dashboard cấp cao (SLO/business metric) → service-level RED dashboard → drill-down dashboard chi tiết, thay vì một dashboard khổng lồ chứa mọi thứ.
- Gắn link trực tiếp từ alert notification tới dashboard/panel liên quan (`dashboardUId` + `panelId` trong alert annotation) để on-call không phải tự tìm dashboard đúng giữa lúc incident.
- Theo dõi cardinality của label trong query panel — panel dùng `group by` trên label có cardinality cao (vd. `user_id`) sẽ làm chậm cả dashboard lẫn Prometheus backend.

## Common Mistakes

- Tạo alert rule không có `for` duration hoặc để giá trị quá thấp, khiến alert bắn liên tục theo từng scrape interval do spike ngắn hạn, gây alert fatigue cho team on-call.
- Dashboard chỉ hiển thị dữ liệu nhưng không gắn alerting nào, nghĩa là vẫn phải có người ngồi nhìn màn hình để phát hiện bất thường thay vì được chủ động thông báo.
- Query panel dùng khoảng thời gian (range) hoặc bước nhảy (step/interval) không phù hợp với retention và resolution thật của Prometheus, khiến graph hiển thị sai lệch (aliasing) hoặc timeout khi zoom vào khoảng dài.
- Cho phép mọi người tự do tạo dashboard qua UI mà không có quy ước đặt tên/tag, dẫn tới hàng trăm dashboard trùng lặp, không ai biết cái nào là "nguồn sự thật" khi debug.
- Không set quyền hạn (RBAC) rõ ràng giữa Viewer/Editor/Admin, khiến một thay đổi vô tình trên dashboard production (xóa panel, sửa query) ảnh hưởng tới toàn team mà không có audit trail.

## Interview Questions

**Hỏi**: Grafana có lưu trữ dữ liệu metric không? Vì sao dashboard chậm thường không phải lỗi của Grafana?

**Trả lời**: Không, Grafana không lưu trữ dữ liệu — nó chỉ là lớp visualize, query trực tiếp tới datasource (Prometheus, Loki...) tại thời điểm render. Vì vậy tốc độ dashboard phụ thuộc hoàn toàn vào tốc độ trả lời của datasource phía sau; dashboard chậm thường là dấu hiệu Prometheus đang phải quét quá nhiều time series (high cardinality) hoặc query range quá rộng, chứ không phải Grafana xử lý chậm.

**Hỏi**: Cơ chế `for` trong alert rule của Grafana dùng để làm gì và tại sao nó quan trọng?

**Trả lời**: `for` là khoảng thời gian điều kiện alert phải đúng liên tục trước khi chuyển từ trạng thái `Pending` sang `Firing`. Nó quan trọng vì tránh alert nhiễu (flapping) do các spike tức thời chỉ kéo dài vài giây — nếu không có `for`, mọi dao động ngắn hạn của metric đều bắn alert, gây alert fatigue và làm giảm độ tin cậy của hệ thống cảnh báo.

**Hỏi**: Vì sao nên quản lý dashboard và alert rule bằng provisioning-as-code thay vì chỉnh trực tiếp qua UI?

**Trả lời**: Vì chỉnh qua UI không có lịch sử thay đổi, dễ gây drift giữa các môi trường (dev/staging/prod), và không thể review trước khi áp dụng. Provisioning-as-code (JSON/Terraform/Grafonnet commit vào git) cho phép version control, code review, rollback, và tái tạo dashboard nhất quán trên nhiều môi trường hoặc khi disaster recovery.

## Summary

Grafana là lớp visualize thống nhất phía trên nhiều nguồn dữ liệu, phổ biến nhất là kết hợp với Prometheus làm datasource metrics chính, cùng Loki và Tempo cho logs và traces. Bản thân Grafana không lưu trữ dữ liệu nên hiệu năng và độ chính xác phụ thuộc hoàn toàn vào datasource phía sau. Alerting rules cho phép chủ động phát hiện bất thường thay vì chờ người xem dashboard, với cơ chế `for` duration để tránh flapping và Alertmanager để xử lý routing/grouping trước khi gửi notification. Best practice quan trọng nhất là quản lý dashboard và alert rule dưới dạng code để tránh drift và duy trì một nguồn sự thật thống nhất cho toàn team.

## Knowledge Graph

- Prometheus — datasource phổ biến nhất của Grafana, cung cấp metrics dạng time series qua PromQL.
- Alertmanager — thành phần xử lý routing, grouping, silencing cho alert do Grafana hoặc Prometheus sinh ra.
- Loki — datasource logs của Grafana, cho phép correlate log với metric trên cùng dashboard qua LogQL.
- SLO/SLI — dashboard cấp cao trong Grafana thường được xây dựng để theo dõi trực tiếp các chỉ số SLI so với mục tiêu SLO.
- Cardinality — high cardinality trong label của Prometheus là nguyên nhân phổ biến nhất khiến dashboard Grafana chậm hoặc timeout.
- MTTD/MTTR — alerting rules trong Grafana là cơ chế chính giảm MTTD, trong khi dashboard drill-down giúp giảm MTTR khi điều tra incident.

## Five Things To Remember

- Grafana không lưu dữ liệu, nó chỉ visualize — hiệu năng dashboard phụ thuộc vào datasource phía sau, thường là Prometheus.
- Prometheus là datasource phổ biến nhất, dùng PromQL để query; Loki và Tempo bổ sung logs và traces trên cùng dashboard.
- Alert rule chuyển từ `Pending` sang `Firing` chỉ sau khi điều kiện đúng liên tục qua khoảng `for`, để tránh flapping.
- Alertmanager xử lý routing/grouping/silencing sau khi alert firing, trước khi notification tới Slack/PagerDuty.
- Luôn quản lý dashboard và alert rule bằng provisioning-as-code (JSON/Terraform) để tránh drift và giữ một nguồn sự thật duy nhất.
