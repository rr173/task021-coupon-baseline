// Package httpapi 提供优惠券规则引擎的 HTTP 接口。
// 服务无内部可变状态，可被多个 goroutine 复用。
package httpapi

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"

	"task021-coupon/internal/coupon"
)

// ErrBadJSON 表示请求体不是单个合法 JSON 对象。
var ErrBadJSON = errors.New("请求体不是合法的单个 JSON 对象")

// API 是优惠券规则引擎服务的 HTTP 接口实现。
type API struct{}

// New 创建服务实例。
func New() *API { return &API{} }

// Handler 返回 HTTP 路由。
func (a *API) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", a.health)
	mux.HandleFunc("POST /apply", a.apply)
	mux.HandleFunc("POST /recommend", a.recommend)
	return mux
}

// decodeJSON 解码单个 JSON 对象，拒绝多段 JSON 与未知字段。
func decodeJSON(r *http.Request, v any) error {
	if r.Body == nil {
		return ErrBadJSON
	}
	dec := json.NewDecoder(io.LimitReader(r.Body, 1<<20))
	dec.DisallowUnknownFields()
	if err := dec.Decode(v); err != nil {
		return fmt.Errorf("%w: %v", ErrBadJSON, err)
	}
	var extra any
	if err := dec.Decode(&extra); err != io.EOF {
		return ErrBadJSON
	}
	return nil
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func errResp(err error) map[string]string {
	return map[string]string{"error": err.Error()}
}

func (a *API) health(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func (a *API) apply(w http.ResponseWriter, r *http.Request) {
	var ord coupon.Order
	if err := decodeJSON(r, &ord); err != nil {
		writeJSON(w, http.StatusBadRequest, errResp(err))
		return
	}
	res, err := coupon.Apply(ord)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, errResp(err))
		return
	}
	writeJSON(w, http.StatusOK, res)
}

func (a *API) recommend(w http.ResponseWriter, r *http.Request) {
	var ord coupon.Order
	if err := decodeJSON(r, &ord); err != nil {
		writeJSON(w, http.StatusBadRequest, errResp(err))
		return
	}
	res, err := coupon.Recommend(ord)
	if err != nil {
		writeJSON(w, http.StatusOK, errResp(err))
		return
	}
	writeJSON(w, http.StatusOK, res)
}
