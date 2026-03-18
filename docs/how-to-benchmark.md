### Một benchmark method đáng tin cậy ?

Reproducibility: Mọi parameter phải ghi rõ: DB config, cluster spec, number of runs

Statistical significance: Mỗi scenario x config cần chạy tối thiểu 3-5 lần, lấy mean +- standard deviation. Dùng welch t-test để chứng minh kết quả không phải đo ngẫu nhiên 

Controlled variabes: mỗi lần chạy chỉ thay đổi 1 thứ (scaling method), giữ nguyên mọi thứ khác

Cần có warm up period để cho hệ thống stabilize trước khi nhảy vào benchmark


### Các scenario quyết định cho vào trong khoá luận
1. Gradual Ramp
Traffic tăng dần từ 8am->12pm
Trend rõ ràng và dễ forecast


```yaml
name: gradual_ramp
steps:
  # Phase 1: Baseline thấp (warm-up + establish history cho predictor)
  - duration: 120s
    rps: 50
  
  # Phase 2: Tăng dần, mỗi step tăng ~200 RPS
  - duration: 60s
    rps: 150 # 1 replica
  - duration: 60s
    rps: 250 # 1 replica
  - duration: 60s
    rps: 350 # 1 replica
  - duration: 60s
    rps: 500 # 2 replica
  - duration: 60s
    rps: 700 # 2 replica
  - duration: 60s
    rps: 900 # 3 replica
  - duration: 60s
    rps: 1100 # 3 replica
  - duration: 60s
    rps: 1400 # 4-5 replica
  
  # Phase 3: Sustain peak
  - duration: 180s
    rps: 1400 # 5 replica
  
  # Phase 4: Giảm dần
  - duration: 60s
    rps: 1000
  - duration: 60s
    rps: 700
  - duration: 60s
    rps: 400
  - duration: 60s
    rps: 200
  - duration: 120s
    rps: 50
```

Scenario 2: Sudden Spike
Mô phỏng các đợt flash sale, stress test tốc độ phản ứng
-> predictive sẽ không lợi thế trong case này nên có thể sẽ cần cách tiếp cận hybrid dùng cả reactive method ở đây

```
name: sudden_spike
steps:
  # Phase 1: Baseline ổn định (đủ dài để predictor có history)
  - duration: 180s
    rps: 200
  
  # Phase 2:s
  - duration: 5s
    rps: 2500
  
  # Phase 3: Sustain high load
  - duration: 300s
    rps: 2500
  
  # Phase 4: Drop đột ngột
  - duration: 5s
    rps: 200
  
  # Phase 5: Baseline lại
  - duration: 180s
    rps: 200
```


Scenario 3: Periodic Wave
Đây là case tương đối quan trọng để chứng minh predictor học được pattern lặp lại nên dễ forecast

Config dưới đây dùng 3 chu kì, nếu predictor tốt thì latency spike ở chu kì 3 nên thấp hơn chu kì 1

```yaml
name: periodic_wave
steps:
  # Chu kỳ 1: Predictor chưa biết gì
  # Valley
  - duration: 60s
    rps: 200
  # Rising
  - duration: 30s
    rps: 400
  - duration: 30s
    rps: 700
  - duration: 30s
    rps: 1000
  - duration: 30s
    rps: 1400
  # Peak
  - duration: 60s
    rps: 1800
  # Falling
  - duration: 30s
    rps: 1400
  - duration: 30s
    rps: 1000
  - duration: 30s
    rps: 700
  - duration: 30s
    rps: 400
  # Valley
  - duration: 60s
    rps: 200

  # Chu kỳ 2: Predictor bắt đầu nhận pattern
  - duration: 60s
    rps: 200
  - duration: 30s
    rps: 400
  - duration: 30s
    rps: 700
  - duration: 30s
    rps: 1000
  - duration: 30s
    rps: 1400
  - duration: 60s
    rps: 1800
  - duration: 30s
    rps: 1400
  - duration: 30s
    rps: 1000
  - duration: 30s
    rps: 700
  - duration: 30s
    rps: 400
  - duration: 60s
    rps: 200

  # Chu kỳ 3: Predictor nên predict tốt
  - duration: 60s
    rps: 200
  - duration: 30s
    rps: 400
  - duration: 30s
    rps: 700
  - duration: 30s
    rps: 1000
  - duration: 30s
    rps: 1400
  - duration: 60s
    rps: 1800
  - duration: 30s
    rps: 1400
  - duration: 30s
    rps: 1000
  - duration: 30s
    rps: 700
  - duration: 30s
    rps: 400
  - duration: 60s
    rps: 200
```




Scenario 4: Real world mix
Mô phỏng gần với thực tế bằng cách kết hợp cả ramp + sustain + spike + noise (thực tế hiếm có pattern sạch và luôn tồn tại noise + bất ngờ nên scenario này sẽ được dùng để test khả năng tổng hợp)

```yaml 
name: realworld_mix
steps:
  # Phase 1: Morning ramp-up
  - duration: 60s
    rps: 100
  - duration: 60s
    rps: 300
  - duration: 60s
    rps: 600
  - duration: 60s
    rps: 900
  
  # Phase 2: Mid-morning sustain với fluctuation
  - duration: 60s
    rps: 1100
  - duration: 30s
    rps: 900
  - duration: 60s
    rps: 1200
  - duration: 30s
    rps: 1000
  - duration: 60s
    rps: 1300
  
  # Phase 3: Lunch spike (unexpected promotion)
  - duration: 10s
    rps: 2500 # spike bất ngờ
  - duration: 120s
    rps: 2500 # duy trì spike
  - duration: 30s
    rps: 2000 # giảm dần sau spike
  - duration: 30s
    rps: 1500
  
  # Phase 4: Afternoon steady
  - duration: 120s
    rps: 1200
  - duration: 30s
    rps: 800 # Brief dip
  - duration: 60s
    rps: 1100
  
  # Phase 5: Evening wind-down
  - duration: 60s
    rps: 800
  - duration: 60s
    rps: 500
  - duration: 60s
    rps: 200
  - duration: 60s
    rps: 100
```

