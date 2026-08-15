package httpapi

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
)

// TestProbe_RecommendTooManyCouponsReturns400 验证 /recommend 端点在券数量超上限时返回 400。
// 传入 17 张券（超过上限 16），期望 HTTP 400。
func TestProbe_RecommendTooManyCouponsReturns400(t *testing.T) {
	api := New()
	srv := httptest.NewServer(api.Handler())
	defer srv.Close()

	coupons := make([]map[string]any, 0, 17)
	for i := 0; i < 17; i++ {
		coupons = append(coupons, map[string]any{
			"id": fmt.Sprintf("c%d", i), "type": "FLAT", "amount": 1,
		})
	}
	payload := map[string]any{
		"items":          []map[string]any{{"sku": "A", "unit_price": 1000, "qty": 1}},
		"coupons":        coupons,
		"floor_rate_bps": 0,
	}
	body, _ := json.Marshal(payload)

	resp, err := http.Post(srv.URL+"/recommend", "application/json", bytes.NewReader(body))
	if err != nil {
		t.Fatalf("请求失败: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("期望 status=400, 实际 status=%d", resp.StatusCode)
	}
}
