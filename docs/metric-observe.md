### 2-layer structure
Metric Observer gồm có 2 tầng hoạt động:

1. Observer với lastGoodValues đóng vai trò bộ nhớ ngắn hạn

PrometheusMetricsObserver chỉ lưu 1 giá trị duy nhất mỗi metric, mục đích duy nhất là state-value protection, bởi Prometheus có thể trả về 0 (do rate() window chưa align với scrape interval thì dùng giá trị cũ thay thế, lastGoodValues sẽ đươc ghi đè nếu giá trị > 0)

2. Controller: history (rolling buffer cho predictor)

Mỗi lần reconcile, scale controller sẽ append snapshot vào trong history và predictor sẽ đọc history từ đây

History mặc định đang có retention là 24h, sau 24h thì sẽ bắt đầu cắt bớt các giá trị ở đầu
