package runtime

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"strconv"
)

const DefaultMaxPages = 100

func PaginateAll(ctx context.Context, hostname, method, basePath string, body any, opts ClientOptions, hint PaginationHint, listPath string, maxPages int) ([]byte, error) {
	if maxPages <= 0 {
		maxPages = DefaultMaxPages
	}

	var allItems []json.RawMessage
	currentPath := basePath
	var offset int

	for page := 0; page < maxPages; page++ {
		data, err := DoRaw(ctx, hostname, method, currentPath, body, opts)
		if err != nil {
			return nil, err
		}

		items := extractItemsRaw(data, listPath)
		if len(items) == 0 {
			break
		}
		allItems = append(allItems, items...)

		switch hint.Strategy {
		case "cursor":
			token := extractJSONString(data, hint.TokenField)
			if token == "" {
				goto done
			}
			currentPath = setQueryParam(basePath, hint.TokenParam, token)
		case "offset":
			offset += len(items)
			currentPath = setQueryParam(basePath, hint.TokenParam, strconv.Itoa(offset))
		default:
			goto done
		}
	}
done:
	return buildMergedJSON(allItems, listPath)
}

func extractItemsRaw(data []byte, listPath string) []json.RawMessage {
	var root any
	if err := json.Unmarshal(data, &root); err != nil {
		return nil
	}
	var arr []any
	if listPath == "" {
		var ok bool
		arr, ok = root.([]any)
		if !ok {
			return nil
		}
	} else {
		obj, ok := root.(map[string]any)
		if !ok {
			return nil
		}
		raw, ok := getNestedPath(obj, listPath)
		if !ok {
			return nil
		}
		arr, ok = raw.([]any)
		if !ok {
			return nil
		}
	}
	out := make([]json.RawMessage, 0, len(arr))
	for _, v := range arr {
		raw, err := json.Marshal(v)
		if err != nil {
			continue
		}
		out = append(out, raw)
	}
	return out
}

func extractJSONString(data []byte, field string) string {
	if field == "" {
		return ""
	}
	var obj map[string]any
	if err := json.Unmarshal(data, &obj); err != nil {
		return ""
	}
	raw, ok := getNestedPath(obj, field)
	if !ok {
		return ""
	}
	s, _ := raw.(string)
	return s
}

func setQueryParam(basePath, key, value string) string {
	qIdx := -1
	for i, c := range basePath {
		if c == '?' {
			qIdx = i
			break
		}
	}
	var pathPart, queryStr string
	if qIdx >= 0 {
		pathPart = basePath[:qIdx]
		queryStr = basePath[qIdx+1:]
	} else {
		pathPart = basePath
	}
	q, err := url.ParseQuery(queryStr)
	if err != nil {
		q = url.Values{}
	}
	q.Set(key, value)
	return fmt.Sprintf("%s?%s", pathPart, q.Encode())
}

func buildMergedJSON(items []json.RawMessage, listPath string) ([]byte, error) {
	if listPath == "" {
		return json.Marshal(items)
	}
	envelope := map[string]any{}
	if err := setNestedPath(envelope, listPath, items); err != nil {
		return nil, err
	}
	return json.Marshal(envelope)
}
