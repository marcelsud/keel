package payments

type Request struct {
	ScreeningID    string `json:"screeningId"`
	OrderID        string `json:"orderId"`
	CustomerID     string `json:"customerId"`
	RequestID      string `json:"requestId"`
	Mode           string `json:"mode"`
	DelayMs        int32  `json:"delayMs"`
	RefundFailures int32  `json:"refundFailures"`
}
