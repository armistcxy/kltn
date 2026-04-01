## Charts
 
| Tab | Description |
|-----|-------------|
| TPS timeline | TPS actual vs expected + P95 per run |
| Latency timeline | P50 / P95 / P99 over time |
| Overlay compare | All runs on same axis |
| Percentiles | P50/P95/P99/P99.9 bar comparison |
| Saturation | Seconds above P95 threshold (50/100/200/300ms) |
| Summary table | All metrics side by side |


### Meaning

pgscaleviz support 6 loại chart

1. TPS timeline: chart này bao gồm TPS thực tế, TPS kì vọng và P95 latency trên cùng một graph, khoảng gap giữa TPS kì vọng và thực tế là SLA debt, thời điểm hệ thống xử lý không kịp tải, chart này sẽ được sử dụng để giải thích hành vi qua các thời điểm của các config trong lần chạy benchmark 

2. Latency timeline: Vẽ P50, P95, P99 theo thời gian cho từng run. Tách biệt latency khỏi TPS để thấy rõ hơn mức độ ảnh hưởng đến người dùng, nếu P50 leo cao nghĩa là median user bị chậm (case nguy hiểm) còn P95 cao nhưng P50 thấp nghĩa là chỉ outlier bị ảnh hưởng

3. Overlay compare: Tất cả chạy trên cùng một trục, chart trên là TPS còn chart dưới là P95 để so sánh song song (so sánh các config ngay trên cùng một panel thì sẽ sử dụng chart này)

4. Percentile: gồm các bar chart nằm ngang, cho latency để trả lời câu hỏi config nào tốt hơn nhìn chung

5. Saturation: So sánh số giây mà P95 vượt ngưỡng (tính cho 4 ngưỡng là 50ms, 100ms, 200ms, 300ms). Chart này sẽ trả lời câu hỏi về SLA violate (số giây saturation là cái giá phải trả khi dùng autoscaling thay vì static over-provisioning)

6. Summary table: bảng tổng hợp tất cả metrics (avg TPS, P50, P95, P99, P99.9, errors, saturation >200ms) cho tất cả các lần chạy