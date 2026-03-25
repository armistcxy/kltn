### First run, found out load balancing error when keeping connection alive for too long

![alt text](image-5.png)

![alt text](image-6.png)

![alt text](image-7.png)

![alt text](image-8.png)

![alt text](image-9.png)

### Second run (after fix load balance error)

```
╔═══════════════════════════════════════╗
║           Final Summary               ║
╠═══════════════════════════════════════╣
║  Duration:      18m59.966s            ║
║  Total Ops:     433876                ║
║  Errors:        0                     ║
║  TPS:           380.60                ║
║  P50 Latency:   93.311ms              ║
║  P95 Latency:   350.975ms             ║
║  P99 Latency:   524.799ms             ║
║  P99.9 Latency: 767.487ms             ║
╚═══════════════════════════════════════╝
```


![alt text](image-11.png)

![alt text](image-10.png)

![alt text](image-12.png)


![alt text](image-13.png)


Ở phase 900-1100 RPS, actual TPS chỉ ~500-700 và actual TPS không bao giờ đạt đủ target, điều này là do hoặc controller chưa scale đủ nhanh hoặc chưa đủ replica. Tuy nhiên theo hình vẽ mình có thấy lên được tới mức 6 instance rồi nhưng có vẻ như vấn đề là do không đủ số lượng node gke để phục vụ việc test.ii


### Third run
Lần chạy này đã có tận 6 node nên không quá lo lắng về tranh chấp tài nguyên nữa 

sau khi fix một số lỗi liên quan đến private node, disk request thì cuối cùng cũng có thể test


![alt text](image-14.png)

![alt text](image-16.png)

![alt text](image-15.png)

![alt text](image-17.png)


TPS đạt 1530 ở peak (target 1400), P50 ổn định ~55-65ms ở sustained load
=> 6 nodes giải quyết hoàn toàn bottleneck trước đó

Trong 90s phút 3:05-4:35, P50 nhảy lên 275-334ms khi 1 replica bị quá tải, recovery rất nhanh khi replica mới được tạo rớt xuống 66ms rồi xuống 5ms 


### 4th run


### 5th run


### 6th run


### 7th run

### 8th run
