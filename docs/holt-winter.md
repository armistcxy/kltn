### HoltWinter
Thuật toán này duy trì 2 thành phần song song 

level[t] = α × value[t] + (1-α) × (level[t-1] + trend[t-1])
trend[t] = β × (level[t] - level[t-1]) + (1-β) × trend[t-1]

Dự báo tại t + h:
forecast = level[t] + h × trend[t]

với:
- level theo dõi giá trị hiện tại (giống ewma)
- trend theo dõi tốc độ thay đổi của level

beta thấp (0.1) thì trend thay đổi chậm, ổn định hơn nhưng lag khi trend đảo chiều
