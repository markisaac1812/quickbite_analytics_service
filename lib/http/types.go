package httpx

// SuccessEnvelope is {"success": true, "data": ..., "meta"?: ...}.
type SuccessEnvelope struct {
	Success bool `json:"success"`
	Data    any  `json:"data"`
	Meta    any  `json:"meta,omitempty"`
}

// ErrorEnvelope is {"success": false, "error": {"code": "...", "message": "..."}}.
type ErrorEnvelope struct {
	Success bool      `json:"success"`
	Error   ErrorBody `json:"error"`
}

type ErrorBody struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

// PaginationMeta is the cursor-pagination meta block (same as the Node side).
type PaginationMeta struct {
	NextCursor *string `json:"nextCursor"`
	HasMore    bool    `json:"hasMore"`
	Count      int     `json:"count"`
}
