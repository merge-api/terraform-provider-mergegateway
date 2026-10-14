package provider

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
)

// apiError describes a failed API call for a diagnostic: the HTTP status and
// the "detail" the API sent. The API sends detail as a string, as
// {"code", "message"}, or (for 422) as a list of {"loc", "msg"}. Only the
// detail is shown, never the rest of the body, since some responses echo secrets.
func apiError(status int, body []byte) string {
	msg := fmt.Sprintf("HTTP %d %s", status, http.StatusText(status))

	var resp struct {
		Detail json.RawMessage `json:"detail"`
	}
	if err := json.Unmarshal(body, &resp); err != nil || len(resp.Detail) == 0 {
		return msg
	}

	var text string
	if err := json.Unmarshal(resp.Detail, &text); err == nil {
		return msg + ": " + text
	}

	var structured struct {
		Code    string `json:"code"`
		Message string `json:"message"`
	}
	if err := json.Unmarshal(resp.Detail, &structured); err == nil && structured.Message != "" {
		return fmt.Sprintf("%s: %s (%s)", msg, structured.Message, structured.Code)
	}

	var validation []struct {
		Loc []any  `json:"loc"`
		Msg string `json:"msg"`
	}
	if err := json.Unmarshal(resp.Detail, &validation); err == nil && len(validation) > 0 {
		lines := make([]string, 0, len(validation))
		for _, v := range validation {
			loc := make([]string, 0, len(v.Loc))
			for _, l := range v.Loc {
				loc = append(loc, fmt.Sprint(l))
			}
			lines = append(lines, strings.Join(loc, ".")+": "+v.Msg)
		}
		return msg + ":\n" + strings.Join(lines, "\n")
	}

	return msg
}
