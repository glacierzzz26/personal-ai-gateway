package server

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strconv"
	"time"
)

const maxBodyBytes = 1 << 20 // 管理面请求体上限 1MB

// apiErr 写统一错误体 {"error":{type,message}}。
func apiErr(w http.ResponseWriter, status int, typ, msg string) {
	writeJSON(w, status, map[string]any{
		"error": map[string]any{"type": typ, "message": msg},
	})
}

// decodeBody 解析 JSON 请求体;失败已写 400。
func decodeBody(w http.ResponseWriter, r *http.Request, v any) bool {
	r.Body = http.MaxBytesReader(w, r.Body, maxBodyBytes)
	defer r.Body.Close()
	dec := json.NewDecoder(r.Body)
	if err := dec.Decode(v); err != nil {
		if errors.Is(err, io.EOF) {
			apiErr(w, http.StatusBadRequest, "validation", "empty request body")
			return false
		}
		apiErr(w, http.StatusBadRequest, "validation", "invalid json: "+err.Error())
		return false
	}
	return true
}

// queryStr 查询串读取。
func queryStr(r *http.Request, key string) string { return r.URL.Query().Get(key) }

// queryInt 查询串整型(def 为缺省;解析失败用 def)。
func queryInt(r *http.Request, key string, def int) int {
	v := r.URL.Query().Get(key)
	if v == "" {
		return def
	}
	n, err := strconv.Atoi(v)
	if err != nil {
		return def
	}
	return n
}

// queryTime 解析可空时间条件(空返回 nil;支持 RFC3339 或 YYYY-MM-DD)。
func queryTime(r *http.Request, key string) (*time.Time, error) {
	v := r.URL.Query().Get(key)
	if v == "" {
		return nil, nil
	}
	for _, layout := range []string{time.RFC3339, "2006-01-02 15:04:05", "2006-01-02"} {
		if t, err := time.Parse(layout, v); err == nil {
			u := t.UTC()
			return &u, nil
		}
	}
	return nil, errors.New("bad time: " + v)
}

// pageRange 由 page(1 基)/size 计算 limit/offset。
func pageRange(r *http.Request, defSize int) (limit, offset int) {
	page := queryInt(r, "page", 1)
	size := queryInt(r, "size", defSize)
	if page < 1 {
		page = 1
	}
	if size < 1 {
		size = defSize
	}
	if size > 200 {
		size = 200
	}
	return size, (page - 1) * size
}
