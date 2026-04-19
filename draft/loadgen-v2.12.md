# Loadgen v2.12 — Tình trạng hiện tại

## 1. Phân phối connection — không thể biết trước

Khi dùng `--db-url postgres://...@pg-cluster-r:5432/app` (ClusterIP service), kube-proxy dùng
iptables với xác suất ngẫu nhiên để route mỗi TCP connection mới đến một pod. Với 100 connection
mở đồng thời, phân phối thực tế có thể là 10/15/25/28/22 thay vì 20/20/20/20/20.

Khi dùng DiscoveryPool (`--discovery-host`) + worker affinity (`workerIdx % podCount`):
- Mỗi worker được gán cố định vào một pod
- Warmup mở đúng `concurrency/podCount` connection vào mỗi pod
- Về lý thuyết đảm bảo phân phối đều

Tuy nhiên qua quan sát thực tế (psql poll 2 phút), **số connection trên mỗi pod không đo được**
do psql authentication fail thầm lặng từ bên ngoài pod — chưa xác nhận được DiscoveryPool
có thực sự phân phối đều hay không trong môi trường GKE hiện tại.

Kết quả: phân phối connection vẫn là **unknown** ở runtime.


## 2. Cách đo thời gian của một request

Trong `engine.go` (`workerLoop`):

```
start := time.Now()
execErr := e.config.Workload.Execute(ctx, conn)
e.stats.Record(time.Since(start), execErr)
```

Thời gian được đo **sau khi đã có connection** (conn đã được Acquire thành công). Tức là:

- **Bao gồm**: thời gian gửi query, PostgreSQL xử lý, nhận kết quả, decode rows phía client
- **Không bao gồm**: thời gian chờ Acquire connection từ pool (nếu pool cạn)
- **Không bao gồm**: thời gian chờ rate limiter

Với `branchRangeReport` (35% traffic) trả về 5.000 rows/call, thời gian đo được bao gồm
cả thời gian decode ~5.000 rows trong Go (pgx allocate per row) — đây là CPU overhead
phía client, không phải latency thực của PostgreSQL.

Vấn đề: latency đo được tại loadgen ≠ latency thực tại PostgreSQL do lẫn client-side
processing time (đặc biệt với range scan nặng).


## 3. Tính biến thiên TPS rất mạnh

### Quan sát

| Cách test                                    | TPS đo được     |
|----------------------------------------------|-----------------|
| Riêng từng node (--concurrency 20, pod IP)   | ≥ 330/node      |
| Kỳ vọng tổng 5 node                          | ~1.650          |
| Qua service pg-cluster-r (--concurrency 100) | 1.100 – 1.400   |
| Qua DiscoveryPool (worker affinity)           | 1.100 – 1.200   |

### Nguyên nhân biến thiên

**a) Client CPU bị saturate bởi row decoding**
`branchRangeReport` trả 5.000 rows/call. Tại 385 call/s (35% × 1.100 TPS):
- 385 × 5.000 = ~1,9 triệu rows/s cần decode trong Go
- Mỗi row: allocate + Scan 3 int → ước ~500ns → ~962ms CPU/s ≈ gần 1 core chỉ để decode
- Một process loadgen với 1–2 vCPU bị CPU-bound → TPS bị cap không phải do DB mà do client

**b) Phân phối connection không đều (khi dùng kube-proxy)**
Pod nhận ít connection hơn threshold bão hòa (~20) sẽ under-utilize CPU → TPS thấp hơn
330. Tổng TPS bị kéo xuống bởi pod yếu nhất.

**c) Worker bouncing (đã fix nhưng chưa verify)**
Round-robin per-acquire cũ khiến mỗi transaction của một worker có thể đi đến pod khác →
buffer cache trên mỗi pod không warm đều → cache miss tăng → latency spike → TPS drop.

**d) GC pressure trong Go**
Với 1,9 triệu row allocation/s, GC của Go chạy liên tục, gây stop-the-world pause ngắn
nhưng đủ để làm TPS fluctuate ±100–200 theo chu kỳ GC.

### Tóm tắt

Biến thiên TPS hiện tại xuất phát từ **client-side bottleneck**, không phải PostgreSQL.
Tổng TPS bị cap bởi khả năng xử lý của một loadgen process đơn lẻ, không phản ánh
đúng capacity thực của 5 PostgreSQL replica.

Fix đề xuất: thêm `LIMIT 100` vào `branchRangeReport` để giảm 50× row transfer về client,
loại bỏ CPU bottleneck tại loadgen mà không thay đổi workload phía PostgreSQL (DB vẫn
scan + sort đủ 5.000 rows).
