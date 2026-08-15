package coupon

import (
	"testing"
)

// TestProbe_MulBpsHalfUp 验证 mulBps 对非整除的金额×折扣率采用四舍五入。
// 1001 * 8000bps = 800.8分，应 half-up 为 801 而非截断为 800。
func TestProbe_MulBpsHalfUp(t *testing.T) {
	got := mulBps(1001, 8000)
	if got != 801 {
		t.Errorf("mulBps(1001, 8000) = %d, want 801 (half-up rounding)", got)
	}
}

// TestProbe_CategoryDiscountUsesPostBundleBase 验证品类折扣的基数使用满件折后的有效金额。
// SKU A (book) 1000x3=3000，满2件打5折->1500；之后品类 book 折扣(8折)应基于 1500 而非 3000。
func TestProbe_CategoryDiscountUsesPostBundleBase(t *testing.T) {
	o := Order{
		Items: []Item{{SKU: "A", Category: "book", UnitPrice: 1000, Qty: 3}},
		Coupons: []Coupon{
			{ID: "c1", Type: CouponItemBundle, SKU: "A", BundleQty: 2, BundleRateBps: 5000},
			{ID: "c2", Type: CouponDiscount, RateBps: 8000, Category: "book"},
		},
	}
	r, err := Apply(o)
	if err != nil {
		t.Fatalf("Apply error: %v", err)
	}
	// 满件折后 A=1500, 品类折扣 base=1500, 8折->1200, 减免 300, payable=1200
	if r.Payable != 1200 {
		t.Errorf("payable = %d, want 1200 (category discount base should be post-bundle)", r.Payable)
	}
}

// TestProbe_ExclusiveGroupConflictReturnsError 验证同一互斥组内两张券应被拒绝。
func TestProbe_ExclusiveGroupConflictReturnsError(t *testing.T) {
	o := Order{
		Items: []Item{{SKU: "A", UnitPrice: 1000, Qty: 1}},
		Coupons: []Coupon{
			{ID: "c1", Type: CouponFlat, Amount: 100, ExclusiveGroup: "g1"},
			{ID: "c2", Type: CouponFlat, Amount: 200, ExclusiveGroup: "g1"},
		},
	}
	_, err := Apply(o)
	if err == nil {
		t.Error("同一互斥组内两张券应返回错误，但 Apply 返回了 nil")
	}
}

// TestProbe_FullReductionExactThreshold 验证当前总额恰好等于满减门槛时应命中。
// A 1000x3=3000，满减门槛 3000 减 500，应付 2500。
func TestProbe_FullReductionExactThreshold(t *testing.T) {
	o := Order{
		Items:   []Item{{SKU: "A", UnitPrice: 1000, Qty: 3}},
		Coupons: []Coupon{{ID: "c1", Type: CouponFullReduction, Threshold: 3000, Amount: 500}},
	}
	r, err := Apply(o)
	if err != nil {
		t.Fatalf("Apply error: %v", err)
	}
	if r.Payable != 2500 {
		t.Errorf("payable = %d, want 2500 (exact threshold should trigger full reduction)", r.Payable)
	}
	if len(r.Applied) != 1 || r.Applied[0].ID != "c1" {
		t.Errorf("applied = %v, want [c1]", r.Applied)
	}
}
