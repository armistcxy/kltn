### Calibration với kịch bản read-heavy

Read heavy gồm có 3 query type

1. Account Lookup (40%)
    
    Nhẹ, dùng index seek, dưới 10ms
    
    $1 = random aid trong [1, totalAccounts]
    
    ```sql
    SELECT aid, bid, abalance, filler
        FROM pgbench_accounts WHERE aid = $1    
    ```
    
2. Branch Range Report (35%)
index range scan + sort
    
    $1 = random start aid
    
    Scan 5000 rows
    
    ```sql
    SELECT aid, bid, abalance
        FROM pgbench_accounts
        WHERE aid BETWEEN $1 AND $1 + 4999
        ORDER BY abalance DESC
    ```
    
3. Branch Financial Summary (25%)
$1 = random start aid
Scan 20,000 rows (20% of 1 branch), 6 aggregates
    
    CPU intensive 
    
    ```sql
    SELECT count(*), avg(abalance), min(abalance), max(abalance),
               sum(abalance), stddev_samp(abalance)
        FROM pgbench_accounts
        WHERE aid BETWEEN $1 AND $1 + 19999
    ```

- 1 node --concurrency 10
    
    ```
    [prepare] pgbench-read-heavy: detected scale factor = 50
    [   5s] TPS:   365.8 | P50: 28.40ms | P95: 65.98ms | P99: 86.40ms | Errors: 0 | Total: 1829
    [  10s] TPS:   385.7 | P50: 28.25ms | P95: 60.54ms | P99: 72.06ms | Errors: 0 | Total: 3758
    [  15s] TPS:   360.1 | P50: 28.70ms | P95: 64.61ms | P99: 116.73ms | Errors: 0 | Total: 5558
    [  20s] TPS:   368.8 | P50: 29.54ms | P95: 60.54ms | P99: 85.18ms | Errors: 0 | Total: 7402
    [  25s] TPS:   353.6 | P50: 29.84ms | P95: 65.53ms | P99: 105.09ms | Errors: 0 | Total: 9170
    [  30s] TPS:   374.9 | P50: 28.62ms | P95: 61.92ms | P99: 89.53ms | Errors: 0 | Total: 11050
    [  35s] TPS:   382.2 | P50: 27.79ms | P95: 61.63ms | P99: 82.11ms | Errors: 0 | Total: 12955
    [  40s] TPS:   388.5 | P50: 28.05ms | P95: 59.62ms | P99: 71.36ms | Errors: 0 | Total: 14898
    [  45s] TPS:   375.1 | P50: 29.63ms | P95: 62.30ms | P99: 76.93ms | Errors: 0 | Total: 16775
    [  50s] TPS:   384.6 | P50: 28.77ms | P95: 59.94ms | P99: 75.71ms | Errors: 0 | Total: 18697
    [  55s] TPS:   369.0 | P50: 27.52ms | P95: 65.79ms | P99: 88.45ms | Errors: 0 | Total: 20541
    
    ╔═══════════════════════════════════════╗
    ║           Final Summary               ║
    ╠═══════════════════════════════════════╣
    ║  Duration:      59.936s               ║
    ║  Total Ops:     22423                 ║
    ║  Errors:        10                    ║
    ║  TPS:           374.12                ║
    ║  P50 Latency:   28.719ms              ║
    ║  P95 Latency:   62.303ms              ║
    ║  P99 Latency:   84.223ms              ║
    ║  P99.9 Latency: 146.303ms             ║
    ╚═══════════════════════════════════════╝
    ```
    
- 1 node --concurrency 20
    
    ```
    ╔═══════════════════════════════════════════════════╗
    ║  loadgen — PostgreSQL load generator              ║
    ╠═══════════════════════════════════════════════════╣
    ║  workload:    pgbench-read-heavy                  ║
    ║  concurrency: 20                                  ║
    ║  duration:    1m0s                                ║
    ║  rate cap:    unlimited                           ║
    ╚═══════════════════════════════════════════════════╝
    
    [prepare] pgbench-read-heavy: detected scale factor = 50
    [   5s] TPS:   341.1 | P50: 64.86ms | P95: 133.12ms | P99: 163.20ms | Errors: 0 | Total: 1706
    [  10s] TPS:   362.5 | P50: 59.42ms | P95: 130.94ms | P99: 166.14ms | Errors: 0 | Total: 3518
    [  15s] TPS:   382.7 | P50: 58.40ms | P95: 120.64ms | P99: 148.48ms | Errors: 0 | Total: 5433
    [  20s] TPS:   375.9 | P50: 58.49ms | P95: 127.10ms | P99: 162.18ms | Errors: 0 | Total: 7312
    [  25s] TPS:   377.7 | P50: 58.88ms | P95: 119.30ms | P99: 171.13ms | Errors: 0 | Total: 9200
    [  30s] TPS:   386.8 | P50: 58.81ms | P95: 119.49ms | P99: 142.46ms | Errors: 0 | Total: 11134
    [  35s] TPS:   372.0 | P50: 57.31ms | P95: 128.25ms | P99: 217.22ms | Errors: 0 | Total: 12994
    [  40s] TPS:   363.0 | P50: 58.88ms | P95: 131.07ms | P99: 176.38ms | Errors: 0 | Total: 14809
    [  45s] TPS:   377.2 | P50: 59.36ms | P95: 122.24ms | P99: 155.52ms | Errors: 0 | Total: 16695
    [  50s] TPS:   352.8 | P50: 61.28ms | P95: 134.53ms | P99: 197.63ms | Errors: 0 | Total: 18459
    [  55s] TPS:   379.0 | P50: 57.98ms | P95: 122.94ms | P99: 158.46ms | Errors: 0 | Total: 20354
    
    ╔═══════════════════════════════════════╗
    ║           Final Summary               ║
    ╠═══════════════════════════════════════╣
    ║  Duration:      59.919s               ║
    ║  Total Ops:     22200                 ║
    ║  Errors:        19                    ║
    ║  TPS:           370.50                ║
    ║  P50 Latency:   59.519ms              ║
    ║  P95 Latency:   126.079ms             ║
    ║  P99 Latency:   164.735ms             ║
    ║  P99.9 Latency: 243.327ms             ║
    ╚═══════════════════════════════════════╝
    ```
    
- 1 node --concurrency 50
    
    ```
    ╔═══════════════════════════════════════════════════╗
    ║  loadgen — PostgreSQL load generator              ║
    ╠═══════════════════════════════════════════════════╣
    ║  workload:    pgbench-read-heavy                  ║
    ║  concurrency: 50                                  ║
    ║  duration:    1m0s                                ║
    ║  rate cap:    unlimited                           ║
    ╚═══════════════════════════════════════════════════╝
    
    [prepare] pgbench-read-heavy: detected scale factor = 50
    [   5s] TPS:   318.8 | P50: 157.44ms | P95: 374.78ms | P99: 445.44ms | Errors: 0 | Total: 1596
    [  10s] TPS:   369.1 | P50: 144.77ms | P95: 322.05ms | P99: 376.57ms | Errors: 0 | Total: 3441
    [  15s] TPS:   368.0 | P50: 145.79ms | P95: 337.92ms | P99: 388.10ms | Errors: 0 | Total: 5280
    [  20s] TPS:   390.9 | P50: 142.72ms | P95: 302.59ms | P99: 342.78ms | Errors: 0 | Total: 7234
    [  25s] TPS:   354.1 | P50: 160.77ms | P95: 327.94ms | P99: 370.43ms | Errors: 0 | Total: 9005
    [  30s] TPS:   368.5 | P50: 153.34ms | P95: 315.90ms | P99: 367.62ms | Errors: 0 | Total: 10847
    [  35s] TPS:   343.2 | P50: 154.37ms | P95: 353.28ms | P99: 459.01ms | Errors: 0 | Total: 12563
    [  40s] TPS:   378.6 | P50: 150.14ms | P95: 311.30ms | P99: 398.33ms | Errors: 0 | Total: 14456
    [  45s] TPS:   367.0 | P50: 148.99ms | P95: 318.46ms | P99: 382.21ms | Errors: 0 | Total: 16291
    [  50s] TPS:   389.7 | P50: 149.50ms | P95: 295.42ms | P99: 336.13ms | Errors: 0 | Total: 18240
    [  55s] TPS:   379.0 | P50: 141.69ms | P95: 312.83ms | P99: 363.52ms | Errors: 0 | Total: 20135
    
    ╔═══════════════════════════════════════╗
    ║           Final Summary               ║
    ╠═══════════════════════════════════════╣
    ║  Duration:      59.679s               ║
    ║  Total Ops:     21949                 ║
    ║  Errors:        50                    ║
    ║  TPS:           367.79                ║
    ║  P50 Latency:   148.991ms             ║
    ║  P95 Latency:   323.327ms             ║
    ║  P99 Latency:   388.863ms             ║
    ║  P99.9 Latency: 473.599ms             ║
    ╚═══════════════════════════════════════╝
    ```
    
- 2 node --concurrency 10
    
    ```
    ╔═══════════════════════════════════════════════════╗
    ║  loadgen — PostgreSQL load generator              ║
    ╠═══════════════════════════════════════════════════╣
    ║  workload:    pgbench-read-heavy                  ║
    ║  concurrency: 10                                  ║
    ║  duration:    1m0s                                ║
    ║  rate cap:    unlimited                           ║
    ╚═══════════════════════════════════════════════════╝
    
    [prepare] pgbench-read-heavy: detected scale factor = 50
    [   5s] TPS:   326.7 | P50: 14.05ms | P95: 130.50ms | P99: 230.14ms | Errors: 0 | Total: 1636
    [  10s] TPS:   330.5 | P50: 15.92ms | P95: 118.78ms | P99: 169.34ms | Errors: 0 | Total: 3286
    [  15s] TPS:   371.7 | P50: 15.78ms | P95: 89.79ms | P99: 130.69ms | Errors: 0 | Total: 5148
    [  20s] TPS:   405.1 | P50: 15.77ms | P95: 76.09ms | P99: 116.03ms | Errors: 0 | Total: 7171
    [  25s] TPS:   394.2 | P50: 15.58ms | P95: 72.64ms | P99: 136.83ms | Errors: 0 | Total: 9141
    [  30s] TPS:   411.5 | P50: 15.89ms | P95: 70.72ms | P99: 97.02ms | Errors: 0 | Total: 11199
    [  35s] TPS:   421.9 | P50: 15.51ms | P95: 71.23ms | P99: 92.22ms | Errors: 0 | Total: 13308
    [  40s] TPS:   426.8 | P50: 15.81ms | P95: 67.78ms | P99: 91.52ms | Errors: 0 | Total: 15442
    [  45s] TPS:   432.3 | P50: 15.40ms | P95: 66.37ms | P99: 86.97ms | Errors: 0 | Total: 17604
    [  50s] TPS:   410.4 | P50: 16.25ms | P95: 70.08ms | P99: 104.96ms | Errors: 0 | Total: 19655
    [  55s] TPS:   435.3 | P50: 15.38ms | P95: 66.94ms | P99: 90.62ms | Errors: 0 | Total: 21835
    
    ╔═══════════════════════════════════════╗
    ║           Final Summary               ║
    ╠═══════════════════════════════════════╣
    ║  Duration:      59.946s               ║
    ║  Total Ops:     23982                 ║
    ║  Errors:        7                     ║
    ║  TPS:           400.06                ║
    ║  P50 Latency:   15.519ms              ║
    ║  P95 Latency:   75.967ms              ║
    ║  P99 Latency:   125.823ms             ║
    ║  P99.9 Latency: 230.143ms             ║
    ╚═══════════════════════════════════════╝
    ```
    
- 2 node --concurrency 20
    
    ```
    ╔═══════════════════════════════════════════════════╗
    ║  loadgen — PostgreSQL load generator              ║
    ╠═══════════════════════════════════════════════════╣
    ║  workload:    pgbench-read-heavy                  ║
    ║  concurrency: 20                                  ║
    ║  duration:    1m0s                                ║
    ║  rate cap:    unlimited                           ║
    ╚═══════════════════════════════════════════════════╝
    
    [prepare] pgbench-read-heavy: detected scale factor = 50
    [   5s] TPS:   458.1 | P50: 37.98ms | P95: 98.94ms | P99: 169.60ms | Errors: 0 | Total: 2291
    [  10s] TPS:   505.0 | P50: 35.33ms | P95: 90.75ms | P99: 125.69ms | Errors: 0 | Total: 4816
    [  15s] TPS:   499.1 | P50: 36.38ms | P95: 92.29ms | P99: 126.91ms | Errors: 0 | Total: 7313
    [  20s] TPS:   501.2 | P50: 33.60ms | P95: 103.23ms | P99: 154.50ms | Errors: 0 | Total: 9819
    [  25s] TPS:   487.9 | P50: 35.84ms | P95: 94.14ms | P99: 189.69ms | Errors: 0 | Total: 12257
    [  30s] TPS:   472.8 | P50: 37.73ms | P95: 99.65ms | P99: 152.70ms | Errors: 0 | Total: 14622
    [  35s] TPS:   505.9 | P50: 36.35ms | P95: 88.38ms | P99: 115.84ms | Errors: 0 | Total: 17150
    [  40s] TPS:   425.8 | P50: 38.56ms | P95: 112.06ms | P99: 263.68ms | Errors: 0 | Total: 19281
    [  45s] TPS:   531.9 | P50: 33.92ms | P95: 86.72ms | P99: 133.25ms | Errors: 0 | Total: 21938
    [  50s] TPS:   529.8 | P50: 34.05ms | P95: 85.50ms | P99: 121.15ms | Errors: 0 | Total: 24589
    [  55s] TPS:   529.8 | P50: 32.70ms | P95: 86.27ms | P99: 118.78ms | Errors: 0 | Total: 27238
    
    ╔═══════════════════════════════════════╗
    ║           Final Summary               ║
    ╠═══════════════════════════════════════╣
    ║  Duration:      59.91s                ║
    ║  Total Ops:     29653                 ║
    ║  Errors:        7                     ║
    ║  TPS:           494.96                ║
    ║  P50 Latency:   35.519ms              ║
    ║  P95 Latency:   93.823ms              ║
    ║  P99 Latency:   141.951ms             ║
    ║  P99.9 Latency: 290.815ms             ║
    ╚═══════════════════════════════════════╝
    ```
    
- 2 node --concurrency 50
    
    ```
    ╔═══════════════════════════════════════════════════╗
    ║  loadgen — PostgreSQL load generator              ║
    ╠═══════════════════════════════════════════════════╣
    ║  workload:    pgbench-read-heavy                  ║
    ║  concurrency: 50                                  ║
    ║  duration:    1m0s                                ║
    ║  rate cap:    5000 rps                            ║
    ╚═══════════════════════════════════════════════════╝
    
    [prepare] pgbench-read-heavy: detected scale factor = 50
    [   5s] TPS:   467.2 | P50: 76.86ms | P95: 278.01ms | P99: 371.45ms | Errors: 0 | Total: 2338
    [  10s] TPS:   470.9 | P50: 85.69ms | P95: 273.92ms | P99: 372.22ms | Errors: 0 | Total: 4700
    [  15s] TPS:   526.2 | P50: 77.69ms | P95: 237.18ms | P99: 317.18ms | Errors: 0 | Total: 7323
    [  20s] TPS:   482.9 | P50: 79.74ms | P95: 272.64ms | P99: 383.23ms | Errors: 0 | Total: 9736
    [  25s] TPS:   529.8 | P50: 79.30ms | P95: 233.34ms | P99: 294.65ms | Errors: 0 | Total: 12384
    [  30s] TPS:   491.4 | P50: 83.45ms | P95: 252.29ms | P99: 348.93ms | Errors: 0 | Total: 14841
    [  35s] TPS:   519.6 | P50: 78.02ms | P95: 242.56ms | P99: 306.69ms | Errors: 0 | Total: 17439
    [  40s] TPS:   477.2 | P50: 82.81ms | P95: 256.77ms | P99: 345.09ms | Errors: 0 | Total: 19828
    [  45s] TPS:   513.8 | P50: 80.00ms | P95: 236.80ms | P99: 300.80ms | Errors: 0 | Total: 22394
    [  50s] TPS:   446.7 | P50: 83.45ms | P95: 310.27ms | P99: 486.40ms | Errors: 0 | Total: 24630
    [  55s] TPS:   550.1 | P50: 74.69ms | P95: 221.31ms | P99: 294.65ms | Errors: 0 | Total: 27384
    
    ╔═══════════════════════════════════════╗
    ║           Final Summary               ║
    ╠═══════════════════════════════════════╣
    ║  Duration:      59.813s               ║
    ║  Total Ops:     29835                 ║
    ║  Errors:        22                    ║
    ║  TPS:           498.80                ║
    ║  P50 Latency:   80.191ms              ║
    ║  P95 Latency:   250.495ms             ║
    ║  P99 Latency:   345.343ms             ║
    ║  P99.9 Latency: 495.359ms             ║
    ╚═══════════════════════════════════════╝
    ```

Có thể nhận thấy rằng 1 instance có ngưỡng TPS là 370, khi tăng lên thành 2 instance thì con số này +35% hiệu quả

![alt text](image.png)

![alt text](image-1.png)

### Forgot to isolate
Đã nhận ra vấn đề, loadgen và read replica nằm chung một node nên ảnh hưởng đến tài nguyên của nhau, sau khi move loadgen sang một node riêng thì mọi thứ đã đúng như dự tính hơn

- 2 node --concurrency 10
    
    ```
    ╔═══════════════════════════════════════════════════╗
    ║  loadgen — PostgreSQL load generator              ║
    ╠═══════════════════════════════════════════════════╣
    ║  workload:    pgbench-read-heavy                  ║
    ║  concurrency: 10                                  ║
    ║  duration:    1m0s                                ║
    ║  rate cap:    unlimited                           ║
    ╚═══════════════════════════════════════════════════╝
    
    [prepare] pgbench-read-heavy: detected scale factor = 50
    
    [   5s] TPS:   674.3 | P50: 14.05ms | P95: 39.42ms | P99: 50.91ms | Errors: 0 | Total: 3372
    [  10s] TPS:   687.0 | P50: 13.68ms | P95: 38.62ms | P99: 52.38ms | Errors: 0 | Total: 6807
    [  15s] TPS:   656.6 | P50: 14.19ms | P95: 40.38ms | P99: 58.91ms | Errors: 0 | Total: 10090
    [  20s] TPS:   721.9 | P50: 13.35ms | P95: 36.83ms | P99: 47.97ms | Errors: 0 | Total: 13701
    [  25s] TPS:   663.6 | P50: 14.11ms | P95: 38.30ms | P99: 49.70ms | Errors: 0 | Total: 17019
    [  30s] TPS:   706.3 | P50: 13.22ms | P95: 37.89ms | P99: 52.83ms | Errors: 0 | Total: 20549
    [  35s] TPS:   672.8 | P50: 14.04ms | P95: 37.50ms | P99: 48.93ms | Errors: 0 | Total: 23913
    [  40s] TPS:   678.3 | P50: 13.62ms | P95: 39.04ms | P99: 58.66ms | Errors: 0 | Total: 27305
    [  45s] TPS:   662.1 | P50: 13.77ms | P95: 39.26ms | P99: 66.43ms | Errors: 0 | Total: 30616
    [  50s] TPS:   704.4 | P50: 13.29ms | P95: 38.08ms | P99: 53.47ms | Errors: 0 | Total: 34138
    [  55s] TPS:   686.7 | P50: 13.86ms | P95: 37.02ms | P99: 50.08ms | Errors: 0 | Total: 37573
    
    ╔═══════════════════════════════════════╗
    ║           Final Summary               ║
    ╠═══════════════════════════════════════╣
    ║  Duration:      59.957s               ║
    ║  Total Ops:     40967                 ║
    ║  Errors:        10                    ║
    ║  TPS:           683.28                ║
    ║  P50 Latency:   13.735ms              ║
    ║  P95 Latency:   38.431ms              ║
    ║  P99 Latency:   53.375ms              ║
    ║  P99.9 Latency: 83.967ms              ║
    ╚═══════════════════════════════════════╝
    ```
    
- 2 node --concurrency 20
    
    ```
    ╔═══════════════════════════════════════════════════╗
    ║  loadgen — PostgreSQL load generator              ║
    ╠═══════════════════════════════════════════════════╣
    ║  workload:    pgbench-read-heavy                  ║
    ║  concurrency: 20                                  ║
    ║  duration:    1m0s                                ║
    ║  rate cap:    unlimited                           ║
    ╚═══════════════════════════════════════════════════╝
    
    [prepare] pgbench-read-heavy: detected scale factor = 50
    [   5s] TPS:   669.7 | P50: 26.91ms | P95: 81.73ms | P99: 104.00ms | Errors: 0 | Total: 3349
    [  10s] TPS:   673.6 | P50: 25.97ms | P95: 83.26ms | P99: 130.69ms | Errors: 0 | Total: 6717
    [  15s] TPS:   705.1 | P50: 25.81ms | P95: 77.69ms | P99: 103.17ms | Errors: 0 | Total: 10242
    [  20s] TPS:   691.5 | P50: 26.86ms | P95: 77.06ms | P99: 104.51ms | Errors: 0 | Total: 13700
    [  25s] TPS:   690.2 | P50: 26.54ms | P95: 78.59ms | P99: 112.06ms | Errors: 0 | Total: 17151
    [  30s] TPS:   687.2 | P50: 26.82ms | P95: 78.46ms | P99: 113.92ms | Errors: 0 | Total: 20587
    [  35s] TPS:   685.7 | P50: 25.97ms | P95: 76.93ms | P99: 137.98ms | Errors: 0 | Total: 24015
    [  40s] TPS:   725.3 | P50: 24.59ms | P95: 73.98ms | P99: 100.80ms | Errors: 0 | Total: 27642
    [  45s] TPS:   699.3 | P50: 26.19ms | P95: 74.24ms | P99: 108.86ms | Errors: 0 | Total: 31138
    [  50s] TPS:   694.9 | P50: 26.09ms | P95: 76.73ms | P99: 103.23ms | Errors: 0 | Total: 34613
    [  55s] TPS:   724.0 | P50: 25.38ms | P95: 74.81ms | P99: 96.89ms | Errors: 0 | Total: 38233
    
    ╔═══════════════════════════════════════╗
    ║           Final Summary               ║
    ╠═══════════════════════════════════════╣
    ║  Duration:      59.895s               ║
    ║  Total Ops:     41628                 ║
    ║  Errors:        15                    ║
    ║  TPS:           695.02                ║
    ║  P50 Latency:   26.079ms              ║
    ║  P95 Latency:   77.631ms              ║
    ║  P99 Latency:   108.863ms             ║
    ║  P99.9 Latency: 177.407ms             ║
    ╚═══════════════════════════════════════╝
    ```
    
- 2 node --concurrency 50
    
    ```
    loadgen run --db-url postgres://app:password@pg-cluster-r:5432/app --workload pgbench-read-heavy --concurrency 50
    ╔═══════════════════════════════════════════════════╗
    ║  loadgen — PostgreSQL load generator              ║
    ╠═══════════════════════════════════════════════════╣
    ║  workload:    pgbench-read-heavy                  ║
    ║  concurrency: 50                                  ║
    ║  duration:    1m0s                                ║
    ║  rate cap:    unlimited                           ║
    ╚═══════════════════════════════════════════════════╝
    
    [prepare] pgbench-read-heavy: detected scale factor = 50
    [   5s] TPS:   653.6 | P50: 75.39ms | P95: 190.21ms | P99: 233.47ms | Errors: 0 | Total: 3286
    [  10s] TPS:   692.3 | P50: 70.02ms | P95: 189.44ms | P99: 264.19ms | Errors: 0 | Total: 6748
    [  15s] TPS:   682.2 | P50: 75.65ms | P95: 183.68ms | P99: 226.56ms | Errors: 0 | Total: 10158
    [  20s] TPS:   674.0 | P50: 71.36ms | P95: 188.54ms | P99: 260.61ms | Errors: 0 | Total: 13528
    [  25s] TPS:   672.1 | P50: 73.79ms | P95: 189.31ms | P99: 258.43ms | Errors: 0 | Total: 16889
    [  30s] TPS:   680.5 | P50: 72.45ms | P95: 182.14ms | P99: 338.18ms | Errors: 0 | Total: 20291
    [  35s] TPS:   686.2 | P50: 73.41ms | P95: 184.19ms | P99: 226.30ms | Errors: 0 | Total: 23722
    [  40s] TPS:   681.0 | P50: 70.91ms | P95: 183.29ms | P99: 246.66ms | Errors: 0 | Total: 27128
    [  45s] TPS:   716.1 | P50: 70.66ms | P95: 183.04ms | P99: 229.25ms | Errors: 0 | Total: 30707
    [  50s] TPS:   723.5 | P50: 71.49ms | P95: 173.06ms | P99: 223.62ms | Errors: 0 | Total: 34325
    [  55s] TPS:   686.9 | P50: 73.02ms | P95: 182.01ms | P99: 229.50ms | Errors: 0 | Total: 37759
    
    ╔═══════════════════════════════════════╗
    ║           Final Summary               ║
    ╠═══════════════════════════════════════╣
    ║  Duration:      59.881s               ║
    ║  Total Ops:     41137                 ║
    ║  Errors:        49                    ║
    ║  TPS:           686.98                ║
    ║  P50 Latency:   72.383ms              ║
    ║  P95 Latency:   184.831ms             ║
    ║  P99 Latency:   244.479ms             ║
    ║  P99.9 Latency: 376.319ms             ║
    ╚═══════════════════════════════════════╝
    ```

->  Benchmark isolation matters

![alt text](image-2.png)

![alt text](image-3.png)

![alt text](image-4.png)

### Explaination

Sử dụng little law có thể giải thích được việc tại sao cứ gấp đôi backend là latency lại gia tăng

```
latency = concurrency / throughput
```

throughput của 1 db instance bị kẹt ở mức 370TPS, concurrency ở đây là số lượng connection đồng thời tới database

```
c=10:  latency = 10 / 374 = 26.7ms [ ] thực tế 29ms
c=20:  latency = 20 / 370 = 54.0ms [ ] thực tế 60ms
c=50:  latency = 50 / 368 = 135.8ms [ ] thực tế 149ms
```

Điều này chứng minh backend là một metric tốt cho autoscaling, khi backend tăng thì queue dài hơn, latency cũng tăng nên cần phải thêm replica