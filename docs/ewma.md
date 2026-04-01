### EWMA (Exponential Weighted Moving Average)

Thay vì average đều các điểm lịch sử như SMA, EWMA gán trọng số giảm dần theo thời gian

```
ewma[t] = α × value[t] + (1 - α) × ewma[t-1]
```

Trong config nếu set alpha = 0.3 thì điểm hiện tại chiếm 30% giá trị mới và 70% còn lại kế thừa từ EWMA trước đó, cảm giác giá trị sẽ smooth hơn so với SMA, alpha càng lớn thì phản ứng càng nhanh trước thay đổi, giá trị alpha còn mang "ý nghĩa quá khứ" để chống lại các outlier (các sudden spike)

OLS Trend Projection sẽ giúp dự báo tương lai, giả sử lấy trendWindow là 6 tức là lấy 6 điểm EWMA gần nhất và fit một đường thẳng qua chúng bằng Ordinary Least Squares: y = a + b*t, sau đó tính giá trị tại t_now + horizon (3 phút sau) với công thức predicted = ewma_now + slope × horizon

Rủi ro của thuật toán này là overshoot nếu trend đảo chiều đột ngột (dễ xuất hiện trong các case có sudden spike)
 
### Ví dụ

Ví dụ:

Giả sử đang ở phút thứ 5 (bước rps=530), 6 điểm EWMA gần nhất của backends:

t-5: 18
t-4: 22
t-3: 27
t-2: 32
t-1: 16
t0:  40

OLS tính slope ~ +4.4 connections / 30s = +8.8/min

predicted(t+3m) = 40 + 8.8 × 3 = 66.4 connections
ceil(66.4 / 25) = 3 replicas (predictive target)

reactive tại t0: ceil(40/25) = 2 replicas
=> hybrid = max(2, 3) = 3 (sẽ scale trước khi load thực sự đến)

