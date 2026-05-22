### Exponential Weighted Moving Average

Giả sử theo dõi #connections vào Postgres mỗi 30s và có một chuỗi giá trị
```
T-5m: 100,  T-4m30: 110,  T-4m: 105,  T-3m30: 130,  T-3m: 150,  ...
```

Với SMA (normal Moving Average), lấy trung bình tất cả (các điểm có trọng số như nhau), vấn đề là giá trị 5 phút trước không nên có mức độ quan trọng tương đương với giá trị xảy ra vào 30s trước -> EWMA giải quyết bằng cách điểm càng gần hiện tại trọng số càng lớn, điểm càng cũ thì trọng số càng giảm dần theo hàm mũ

```
ewma_new = alpha × value_now + (1 - αlpha) × ewma_old
```

Nếu alpha thấp thì smooth, phản ứng chậm và chống nhiễu tốt, nếu alpha lớn thì theo sát giá trị thực nhưng dễ bị nhiễu
Chọn một giá trị cân bằng, đâu đó quanh 0.3 -> 0.5 là ổn

EWMA chỉ cho biết level hiện tại, cần làm thêm bước nữa là tính slope từ 30 điểm gần nhất -> biết "đang tăng/giảm bao nhiêu mỗi giây"


```
Input: history []DataPoint, horizon time.Duration, alpha float64
 
Step 1 - Compute EWMA of values:
  ewma = history[0].Value
  for i := 1; i < len(history); i++ {
      ewma = alpha * history[i].Value + (1 - alpha) * ewma
  }

Step 2 - Compute trend (slope) from last N points:
  Use last min(30, len(history)) points
  slope = linearSlope(recentPoints)  // simple OLS slope
  
Step 3 - Extrapolate:
  steps = horizon / avgInterval(history)
  predicted = ewma + slope * steps
  
Step 4 - Floor at 0:
  return max(predicted, 0)
```
