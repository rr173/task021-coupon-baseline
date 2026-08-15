// Package selfcheck 提供无需外部依赖的自检：通过 httptest 启动真实 HTTP
// 服务，覆盖结算、推荐端点与全部边界约束。成功返回 0，任一失败返回 1。
package selfcheck

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"

	"task021-coupon/internal/httpapi"
)

// settleResp 解析结算/推荐端点的响应（含错误字段）。
type settleResp struct {
	OriginalTotal int64 `json:"original_total"`
	Floor         int64 `json:"floor"`
	Payable       int64 `json:"payable"`
	Applied       []struct {
		ID        string `json:"id"`
		Type      string `json:"type"`
		Reduction int64  `json:"reduction"`
	} `json:"applied"`
	Skipped []struct {
		ID     string `json:"id"`
		Reason string `json:"reason"`
	} `json:"skipped"`
	Chosen []string `json:"chosen"`
	Error  string   `json:"error"`
}

// Run 执行自检并返回退出码。
func Run() int {
	passed, failed := 0, 0
	check := func(name string, fn func() error) {
		if err := fn(); err != nil {
			failed++
			fmt.Printf("FAIL %-36s %v\n", name, err)
		} else {
			passed++
			fmt.Printf("PASS %s\n", name)
		}
	}

	srv := httptest.NewServer(httpapi.New().Handler())
	defer srv.Close()

	do := func(method, path, body string) (*http.Response, []byte, error) {
		var r io.Reader
		if body != "" {
			r = bytes.NewReader([]byte(body))
		}
		req, err := http.NewRequest(method, srv.URL+path, r)
		if err != nil {
			return nil, nil, err
		}
		if body != "" {
			req.Header.Set("Content-Type", "application/json")
		}
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			return nil, nil, err
		}
		data, readErr := io.ReadAll(resp.Body)
		resp.Body.Close()
		return resp, data, readErr
	}

	// body 拼装结算请求体。
	body := func(items []map[string]any, coupons []map[string]any, floor int) string {
		m := map[string]any{
			"items":         items,
			"coupons":       coupons,
			"floor_rate_bps": floor,
		}
		b, _ := json.Marshal(m)
		return string(b)
	}
	it := func(sku, cat string, price int64, qty int) map[string]any {
		return map[string]any{"sku": sku, "category": cat, "unit_price": price, "qty": qty}
	}

	apply := func(b string) (int, settleResp, error) {
		resp, data, err := do(http.MethodPost, "/apply", b)
		if err != nil {
			return 0, settleResp{}, err
		}
		var s settleResp
		_ = json.Unmarshal(data, &s)
		return resp.StatusCode, s, nil
	}
	recommend := func(b string) (int, settleResp, error) {
		resp, data, err := do(http.MethodPost, "/recommend", b)
		if err != nil {
			return 0, settleResp{}, err
		}
		var s settleResp
		_ = json.Unmarshal(data, &s)
		return resp.StatusCode, s, nil
	}

	// ---- 健康检查 ----
	check("健康检查", func() error {
		resp, _, err := do(http.MethodGet, "/healthz", "")
		if err != nil {
			return err
		}
		if resp.StatusCode != http.StatusOK {
			return fmt.Errorf("status=%d", resp.StatusCode)
		}
		return nil
	})

	// ---- 结算：满件折 ----
	check("结算满件折", func() error {
		b := body([]map[string]any{it("A", "", 1000, 3)},
			[]map[string]any{{"id": "c1", "type": "ITEM_BUNDLE", "sku": "A", "bundle_qty": 2, "bundle_rate_bps": 8000}},
			0)
		status, s, err := apply(b)
		if err != nil {
			return err
		}
		if status != http.StatusOK || s.OriginalTotal != 3000 || s.Payable != 2400 {
			return fmt.Errorf("status=%d orig=%d payable=%d want 200/3000/2400", status, s.OriginalTotal, s.Payable)
		}
		if len(s.Applied) != 1 || s.Applied[0].ID != "c1" || s.Applied[0].Reduction != 600 {
			return fmt.Errorf("applied=%v want c1/600", s.Applied)
		}
		return nil
	})

	// ---- 结算：订单折扣 ----
	check("结算订单折扣", func() error {
		b := body([]map[string]any{it("A", "", 1000, 3)},
			[]map[string]any{{"id": "c1", "type": "DISCOUNT", "rate_bps": 8000}},
			0)
		status, s, err := apply(b)
		if err != nil {
			return err
		}
		if status != http.StatusOK || s.Payable != 2400 {
			return fmt.Errorf("status=%d payable=%d want 200/2400", status, s.Payable)
		}
		if len(s.Applied) != 1 || s.Applied[0].Reduction != 600 {
			return fmt.Errorf("applied=%v want reduction 600", s.Applied)
		}
		return nil
	})

	// ---- 结算：满减命中 ----
	check("结算满减命中", func() error {
		b := body([]map[string]any{it("A", "", 1000, 3)},
			[]map[string]any{{"id": "c1", "type": "FULL_REDUCTION", "threshold": 2500, "amount": 200}},
			0)
		status, s, err := apply(b)
		if err != nil {
			return err
		}
		if status != http.StatusOK || s.Payable != 2800 {
			return fmt.Errorf("status=%d payable=%d want 200/2800", status, s.Payable)
		}
		return nil
	})

	// ---- 边界1：动态满减门槛（折扣使满减被跳过）----
	check("动态满减门槛被跳过", func() error {
		b := body([]map[string]any{it("A", "", 1000, 3)},
			[]map[string]any{
				{"id": "c1", "type": "DISCOUNT", "rate_bps": 8000},
				{"id": "c2", "type": "FULL_REDUCTION", "threshold": 2500, "amount": 200},
			}, 0)
		status, s, err := apply(b)
		if err != nil {
			return err
		}
		if status != http.StatusOK || s.Payable != 2400 {
			return fmt.Errorf("status=%d payable=%d want 200/2400 (full reduction skipped)", status, s.Payable)
		}
		if len(s.Applied) != 1 || s.Applied[0].ID != "c1" {
			return fmt.Errorf("applied=%v want only c1", s.Applied)
		}
		if len(s.Skipped) != 1 || s.Skipped[0].ID != "c2" {
			return fmt.Errorf("skipped=%v want c2", s.Skipped)
		}
		if !strings.Contains(s.Skipped[0].Reason, "门槛") {
			return fmt.Errorf("skip reason should mention threshold, got: %q", s.Skipped[0].Reason)
		}
		return nil
	})

	// ---- 边界2：底价保护 ----
	check("底价保护抬升", func() error {
		b := body([]map[string]any{it("A", "", 1000, 1)},
			[]map[string]any{{"id": "c1", "type": "FLAT", "amount": 950}},
			1000)
		status, s, err := apply(b)
		if err != nil {
			return err
		}
		if status != http.StatusOK || s.Floor != 100 || s.Payable != 100 {
			return fmt.Errorf("status=%d floor=%d payable=%d want 200/100/100", status, s.Floor, s.Payable)
		}
		if len(s.Applied) != 1 || s.Applied[0].Reduction != 950 {
			return fmt.Errorf("applied=%v want reduction 950", s.Applied)
		}
		return nil
	})

	// ---- 边界3：互斥组冲突返回 400 ----
	check("互斥组冲突返回400", func() error {
		b := body([]map[string]any{it("A", "", 1000, 1)},
			[]map[string]any{
				{"id": "c1", "type": "FLAT", "amount": 100, "exclusive_group": "g1"},
				{"id": "c2", "type": "FLAT", "amount": 200, "exclusive_group": "g1"},
			}, 0)
		status, s, err := apply(b)
		if err != nil {
			return err
		}
		if status != http.StatusBadRequest {
			return fmt.Errorf("status=%d want 400", status)
		}
		if !strings.Contains(s.Error, "互斥") {
			return fmt.Errorf("error should mention 互斥, got: %q", s.Error)
		}
		return nil
	})

	// ---- 边界4：可用性跳过（满件未达件数）----
	check("满件未达件数跳过", func() error {
		b := body([]map[string]any{it("A", "", 1000, 1)},
			[]map[string]any{{"id": "c1", "type": "ITEM_BUNDLE", "sku": "A", "bundle_qty": 2, "bundle_rate_bps": 8000}},
			0)
		status, s, err := apply(b)
		if err != nil {
			return err
		}
		if status != http.StatusOK || s.Payable != 1000 {
			return fmt.Errorf("status=%d payable=%d want 200/1000", status, s.Payable)
		}
		if len(s.Applied) != 0 || len(s.Skipped) != 1 {
			return fmt.Errorf("applied=%v skipped=%v want 0/1", s.Applied, s.Skipped)
		}
		if !strings.Contains(s.Skipped[0].Reason, "门槛") {
			return fmt.Errorf("skip reason should mention threshold, got: %q", s.Skipped[0].Reason)
		}
		return nil
	})

	// ---- 可用性跳过：折扣率越界 ----
	check("折扣率越界跳过", func() error {
		b := body([]map[string]any{it("A", "", 1000, 3)},
			[]map[string]any{{"id": "c1", "type": "DISCOUNT", "rate_bps": 10000}},
			0)
		status, s, err := apply(b)
		if err != nil {
			return err
		}
		if status != http.StatusOK || s.Payable != 3000 {
			return fmt.Errorf("status=%d payable=%d want 200/3000", status, s.Payable)
		}
		if len(s.Skipped) != 1 || !strings.Contains(s.Skipped[0].Reason, "折扣率") {
			return fmt.Errorf("skipped=%v want reason with 折扣率", s.Skipped)
		}
		return nil
	})

	// ---- 可用性跳过：同作用域折扣重复 ----
	check("同作用域折扣重复跳过", func() error {
		b := body([]map[string]any{it("A", "", 1000, 3)},
			[]map[string]any{
				{"id": "c1", "type": "DISCOUNT", "rate_bps": 8000},
				{"id": "c2", "type": "DISCOUNT", "rate_bps": 5000},
			}, 0)
		status, s, err := apply(b)
		if err != nil {
			return err
		}
		if status != http.StatusOK || s.Payable != 2400 {
			return fmt.Errorf("status=%d payable=%d want 200/2400", status, s.Payable)
		}
		if len(s.Applied) != 1 || s.Applied[0].ID != "c1" {
			return fmt.Errorf("applied=%v want only c1", s.Applied)
		}
		if len(s.Skipped) != 1 || s.Skipped[0].ID != "c2" || !strings.Contains(s.Skipped[0].Reason, "重复") {
			return fmt.Errorf("skipped=%v want c2 with 重复", s.Skipped)
		}
		return nil
	})

	// ---- 品类折扣 ----
	check("品类折扣作用域", func() error {
		b := body([]map[string]any{
			it("A", "book", 1000, 3),
			it("B", "toy", 1000, 1),
		}, []map[string]any{{"id": "c1", "type": "DISCOUNT", "rate_bps": 5000, "category": "book"}}, 0)
		status, s, err := apply(b)
		if err != nil {
			return err
		}
		if status != http.StatusOK || s.OriginalTotal != 4000 || s.Payable != 2500 {
			return fmt.Errorf("status=%d orig=%d payable=%d want 200/4000/2500", status, s.OriginalTotal, s.Payable)
		}
		if len(s.Applied) != 1 || s.Applied[0].Reduction != 1500 {
			return fmt.Errorf("applied=%v want reduction 1500", s.Applied)
		}
		return nil
	})

	// ---- 四阶段叠加（顺序无关）----
	check("四阶段叠加顺序无关", func() error {
		b := body([]map[string]any{
			it("A", "book", 2000, 2),
			it("B", "toy", 1000, 1),
		}, []map[string]any{
			{"id": "c1", "type": "FLAT", "amount": 200},
			{"id": "c2", "type": "FULL_REDUCTION", "threshold": 3000, "amount": 500},
			{"id": "c3", "type": "DISCOUNT", "rate_bps": 9000},
			{"id": "c4", "type": "ITEM_BUNDLE", "sku": "A", "bundle_qty": 2, "bundle_rate_bps": 8000},
		}, 1000)
		status, s, err := apply(b)
		if err != nil {
			return err
		}
		if status != http.StatusOK || s.OriginalTotal != 5000 || s.Floor != 500 || s.Payable != 3080 {
			return fmt.Errorf("status=%d orig=%d floor=%d payable=%d want 200/5000/500/3080", status, s.OriginalTotal, s.Floor, s.Payable)
		}
		want := []struct {
			id        string
			reduction int64
		}{{"c4", 800}, {"c3", 420}, {"c2", 500}, {"c1", 200}}
		if len(s.Applied) != len(want) {
			return fmt.Errorf("applied=%v want %d entries", s.Applied, len(want))
		}
		for i, w := range want {
			if s.Applied[i].ID != w.id || s.Applied[i].Reduction != w.reduction {
				return fmt.Errorf("applied[%d]=%v want %s/%d", i, s.Applied[i], w.id, w.reduction)
			}
		}
		if len(s.Skipped) != 0 {
			return fmt.Errorf("skipped=%v want none", s.Skipped)
		}
		return nil
	})

	// ---- 输入校验：重复 SKU ----
	check("重复SKU返回400", func() error {
		b := body([]map[string]any{it("A", "", 100, 1), it("A", "", 200, 1)}, nil, 0)
		status, _, err := apply(b)
		if err != nil {
			return err
		}
		if status != http.StatusBadRequest {
			return fmt.Errorf("status=%d want 400", status)
		}
		return nil
	})

	// ---- 输入校验：未知券类型 ----
	check("未知券类型返回400", func() error {
		b := body([]map[string]any{it("A", "", 100, 1)},
			[]map[string]any{{"id": "c1", "type": "WEIRD"}}, 0)
		status, _, err := apply(b)
		if err != nil {
			return err
		}
		if status != http.StatusBadRequest {
			return fmt.Errorf("status=%d want 400", status)
		}
		return nil
	})

	// ---- 输入校验：非法 JSON ----
	check("非法JSON返回400", func() error {
		status, _, err := apply("{not json")
		if err != nil {
			return err
		}
		if status != http.StatusBadRequest {
			return fmt.Errorf("status=%d want 400", status)
		}
		return nil
	})

	// ---- 输入校验：多段 JSON ----
	check("多段JSON返回400", func() error {
		b := body([]map[string]any{it("A", "", 100, 1)}, nil, 0)
		status, _, err := apply(b + b)
		if err != nil {
			return err
		}
		if status != http.StatusBadRequest {
			return fmt.Errorf("status=%d want 400", status)
		}
		return nil
	})

	// ---- 输入校验：未知字段 ----
	check("未知字段返回400", func() error {
		b := body([]map[string]any{it("A", "", 100, 1)}, nil, 0)
		b = strings.TrimSuffix(b, "}") + `,"extra":1}`
		status, _, err := apply(b)
		if err != nil {
			return err
		}
		if status != http.StatusBadRequest {
			return fmt.Errorf("status=%d want 400", status)
		}
		return nil
	})

	// ---- 推荐：最优组合 ----
	check("推荐最优组合", func() error {
		b := body([]map[string]any{it("A", "", 1000, 5)},
			[]map[string]any{
				{"id": "c1", "type": "FULL_REDUCTION", "threshold": 5000, "amount": 500, "exclusive_group": "g1"},
				{"id": "c2", "type": "DISCOUNT", "rate_bps": 8000, "exclusive_group": "g1"},
				{"id": "c3", "type": "FLAT", "amount": 300},
				{"id": "c4", "type": "ITEM_BUNDLE", "sku": "A", "bundle_qty": 3, "bundle_rate_bps": 5000},
			}, 0)
		status, s, err := recommend(b)
		if err != nil {
			return err
		}
		if status != http.StatusOK || s.Payable != 1700 {
			return fmt.Errorf("status=%d payable=%d want 200/1700", status, s.Payable)
		}
		want := []string{"c2", "c3", "c4"}
		if len(s.Chosen) != len(want) {
			return fmt.Errorf("chosen=%v want %v", s.Chosen, want)
		}
		for i, w := range want {
			if s.Chosen[i] != w {
				return fmt.Errorf("chosen[%d]=%q want %q", i, s.Chosen[i], w)
			}
		}
		// 互斥组 g1 至多一张：c1 与 c2 不可同时出现。
		has := map[string]bool{}
		for _, id := range s.Chosen {
			has[id] = true
		}
		if has["c1"] && has["c2"] {
			return fmt.Errorf("recommend selected both c1 and c2 (same exclusive group): %v", s.Chosen)
		}
		return nil
	})

	// ---- 推荐：互斥组内选更优 ----
	check("推荐互斥组选更优", func() error {
		b := body([]map[string]any{it("A", "", 1000, 10)},
			[]map[string]any{
				{"id": "c1", "type": "FULL_REDUCTION", "threshold": 10000, "amount": 1000, "exclusive_group": "g1"},
				{"id": "c2", "type": "FULL_REDUCTION", "threshold": 10000, "amount": 3000, "exclusive_group": "g1"},
			}, 0)
		status, s, err := recommend(b)
		if err != nil {
			return err
		}
		if status != http.StatusOK || s.Payable != 7000 {
			return fmt.Errorf("status=%d payable=%d want 200/7000", status, s.Payable)
		}
		if len(s.Chosen) != 1 || s.Chosen[0] != "c2" {
			return fmt.Errorf("chosen=%v want [c2]", s.Chosen)
		}
		return nil
	})

	// ---- 推荐：无券 ----
	check("推荐无券返回原价", func() error {
		b := body([]map[string]any{it("A", "", 1000, 2)}, []map[string]any{}, 0)
		status, s, err := recommend(b)
		if err != nil {
			return err
		}
		if status != http.StatusOK || s.Payable != 2000 {
			return fmt.Errorf("status=%d payable=%d want 200/2000", status, s.Payable)
		}
		if len(s.Chosen) != 0 || len(s.Applied) != 0 || len(s.Skipped) != 0 {
			return fmt.Errorf("chosen=%v applied=%v skipped=%v want all empty", s.Chosen, s.Applied, s.Skipped)
		}
		return nil
	})

	// ---- 推荐：超过券数量上限 ----
	check("推荐超券数上限返回400", func() error {
		coupons := make([]map[string]any, 0, 17)
		for i := 0; i < 17; i++ {
			coupons = append(coupons, map[string]any{
				"id": fmt.Sprintf("c%d", i), "type": "FLAT", "amount": 1,
			})
		}
		b := body([]map[string]any{it("A", "", 1000, 1)}, coupons, 0)
		status, _, err := recommend(b)
		if err != nil {
			return err
		}
		if status != http.StatusBadRequest {
			return fmt.Errorf("status=%d want 400", status)
		}
		return nil
	})

	fmt.Printf("\n%d passed, %d failed\n", passed, failed)
	if failed > 0 {
		return 1
	}
	return 0
}
