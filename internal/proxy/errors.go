package proxy

import (
	"encoding/json"
	"net/http"
)

// WriteError 按入站协议写出统一的错误 JSON(透传上游错误时不需要走这里)。
// anthropic 协议: {"type":"error","error":{...}}
// openai 协议:  {"error":{...}}
func WriteError(w http.ResponseWriter, proto string, status int, typ, msg string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	var body any
	if proto == ProtoAnthropic {
		body = map[string]any{
			"type": "error",
			"error": map[string]string{
				"type":    typ,
				"message": msg,
			},
		}
	} else {
		body = map[string]any{
			"error": map[string]string{
				"type":    typ,
				"message": msg,
			},
		}
	}
	_ = json.NewEncoder(w).Encode(body)
}
